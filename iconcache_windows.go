//go:build windows

package main

// 面板图标的**落盘缓存**：把站点图标固化成 .ico 文件，好让快捷方式、任务栏固定项
// 这类「Windows 自己保管的东西」也能用上站点图标。
//
// 为什么必须有这一步：
//   - 标题栏与任务栏按钮的图标是我们自己 `WM_SETICON` 上去的，进程活着就有；
//   - 但**快捷方式（.lnk）和任务栏固定项的图标是 shell 存的一份静态引用**
//     （IShellLinkW::SetIconLocation 指向某个 .ico 或 exe）。它不认内存里的 HICON，
//     也不认网页 —— 必须给磁盘上一个真实存在的图标文件。
//
// 缓存位置固定在**配置文件旁边**的 `icons\` 目录（便携模式下即 `<exe>\data\icons\`）：
// 与配置文件同生共死，整包拷走时图标跟着走；面板删除时一并清掉，不留孤儿文件。
//
// ── 关于 ICO 编码 ─────────────────────────────────────────────────────────────
//
// ICO 是容器，一个文件里可以有多帧，shell 按显示尺寸挑最合适的一帧 ——
// 16×16（列表/小图标）、32×32（任务栏）、48×48（中图标）、256×256（大图标/磁贴）。
// 只塞一帧的后果是：某些尺寸下 shell 把 16×16 拉伸到 256，糊成一片。
//
// 两种帧格式混用，按尺寸分界：
//   - ≤48 用 **DIB 帧**（BITMAPINFOHEADER + 自底向上的 BGRA + AND 掩码）：
//     这是 ICO 最古老也最兼容的格式，所有 shell 版本都认；
//   - >48 用 **PNG 帧**：Vista 起被 ICO 容器接受（配合 CreateIconFromResourceEx 的
//     0x00030000 版本号），体积比 DIB 小一个数量级（256×256 的 DIB 光像素就 256KB）。
//
// 帧里的 alpha 一律是**直通（straight）**，不是预乘 —— 这是 ICO 的规范约定，
// 而 image.RGBA 内部存的是预乘值，所以出帧前必须过一遍 panelToNRGBA 反预乘。
// 写反了的表现是半透明边缘偏亮/发白，不放大看不出来。

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── 常量与 Win32 声明 ───────────────────────────────────────────────────────

const (
	// panelIconCacheDirName 是缓存目录名，与 config.json 同级。
	panelIconCacheDirName = "icons"
	// panelIconCacheExt 是缓存文件扩展名。
	panelIconCacheExt = ".ico"

	// panelICODIBMaxSize 是「用 DIB 帧」的尺寸上限，超过就用 PNG 帧。
	panelICODIBMaxSize = 48
	// panelICOMaxSize 是 ICO 单帧能表达的最大边长（宽高字段各 1 字节，256 写作 0）。
	panelICOMaxSize = 256
)

// SHChangeNotify 的事件与标志（shlobj_core.h）。
const (
	shcneUpdateItem = 0x00002000
	shcneUpdateDir  = 0x00001000
	shcnfPathW      = 0x0005
	shcnfFlush      = 0x1000
)

var panelSHChangeNotify = shortcutShell32.NewProc("SHChangeNotify")

// ─── 目录与路径 ──────────────────────────────────────────────────────────────

// panelIconCacheDirFor 由**配置文件的路径**推出图标缓存目录：就在它旁边。
//
// 刻意不重新推导一遍 portable / %APPDATA%：configStore.path 才是事实，
// 重新推导会在「配置文件被显式指定到别处」时把图标写到另一个地方。
func panelIconCacheDirFor(configPath string) string {
	if configPath == "" {
		configPath = defaultConfigPath()
	}
	return filepath.Join(filepath.Dir(configPath), panelIconCacheDirName)
}

// panelIconCacheGUID 把面板 ID 规整成安全的文件名。
// 面板 ID 正常是 UUID，但它来自可被手工编辑的配置文件，不能让 `../` 之类穿出去。
func panelIconCacheGUID(panelID string) string {
	var b strings.Builder
	for _, r := range panelID {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// panelIconCachePathIn 返回某面板的缓存文件完整路径；ID 不合法时返回空串。
func panelIconCachePathIn(dir, panelID string) string {
	safe := panelIconCacheGUID(panelID)
	if dir == "" || safe == "" {
		return ""
	}
	return filepath.Join(dir, safe+panelIconCacheExt)
}

// ─── 落盘的批量包装 ──────────────────────────────────────────────────────────

// panelIconCacheWrite 把面板图标写到 dir/<面板ID>.ico，返回写成的路径。
func panelIconCacheWrite(dir, panelID string, frames []image.Image) (string, error) {
	path := panelIconCachePathIn(dir, panelID)
	if path == "" {
		return "", errCode(errIconIDInvalid)
	}
	data := encodePanelICO(frames)
	if len(data) == 0 {
		return "", errCode(errIconNoFrames)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", errCodeWrap(errIconMkdirFailed, err)
	}
	// 原子落盘：先写 .tmp 再改名。中途失败不会留下一个「半截的 .ico」——
	// 那种文件会被 shell 当成损坏图标，比没有更难看，而且看不出是谁的锅。
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", errCodeWrap(errIconWriteFailed, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", errCodeWrap(errIconSaveFailed, err)
	}
	return path, nil
}

func panelIconRemove(dir, panelID string) bool {
	path := panelIconCachePathIn(dir, panelID)
	if path == "" {
		return false
	}
	return os.Remove(path) == nil
}

func panelIconCached(dir, panelID string) bool {
	path := panelIconCachePathIn(dir, panelID)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Size() > 0
}

// ─── 一次解析的样本登记 ──────────────────────────────────────────────────────
//
// 「刷新图标」按钮的前提是「网页窗口那边已经加载好图标」。这个判断不能靠再去页面里问一遍
// （面板窗口可能根本没开，也可能是别的站点），只能靠面板窗口解析时留下的一份样本。
//
// 为什么不直接把样本交给 App 持久保存：样本里是解码后的 image.Image，只在进程内有意义；
// 真正要长期保存的是它编码出来的 .ico，那是在用户点「应用」时才落盘的。

type panelIconSample struct {
	tabID     string
	panelID   string
	pageURL   string
	host      string
	pageTitle string
	tabName   string
	// decoded 是本站点**真实取到的**图标，不含自绘的首字母色块。
	// 一个都没有 = 「当前地址没有图标可用」。
	decoded []panelDecodedIcon
}

var panelIconRegistry = struct {
	mu sync.Mutex
	m  map[string]panelIconSample
}{m: make(map[string]panelIconSample)}

func registerPanelIconSample(s panelIconSample) {
	if s.tabID == "" {
		return
	}
	panelIconRegistry.mu.Lock()
	panelIconRegistry.m[s.tabID] = s
	panelIconRegistry.mu.Unlock()
}

func lookupPanelIconSample(tabID string) (panelIconSample, bool) {
	panelIconRegistry.mu.Lock()
	defer panelIconRegistry.mu.Unlock()
	s, ok := panelIconRegistry.m[tabID]
	return s, ok
}

// unregisterPanelIconSamples 清掉若干标签的样本。
// 面板窗口销毁时必须调 —— 否则窗口关了、页面没了，「刷新图标」按钮还会拿旧样本说
// 「有图标可用」，然后给用户写一个早就过期的图标。
func unregisterPanelIconSamples(tabIDs []string) {
	panelIconRegistry.mu.Lock()
	for _, id := range tabIDs {
		delete(panelIconRegistry.m, id)
	}
	panelIconRegistry.mu.Unlock()
}

// ─── shell 通知 ─────────────────────────────────────────────────────────────

// notifyShellIconChanged 告诉 shell 这些文件的图标变了，让它重画。
//
// 不通知的后果是「改了但看不见」：.lnk 里的图标位置是新值，桌面上却还是旧图标，
// 用户以为功能没生效。带 SHCNF_FLUSH 是为了同步 —— 我们要在返回后才跟用户说「已生效」。
func notifyShellIconChanged(paths []string) {
	for _, p := range paths {
		if p == "" {
			continue
		}
		ptr, err := windows.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		panelSHChangeNotify.Call(
			shcneUpdateItem,
			shcnfPathW|shcnfFlush,
			uintptr(unsafe.Pointer(ptr)),
			0,
		)
	}
}

// notifyShellDirChanged 通知某个目录的内容变了（任务栏固定项的图标缓存比单个文件顽固，
// 改完固定项的 .lnk 后光靠 UpdateItem 有时不刷新，得把目录也提一句）。
func notifyShellDirChanged(dir string) {
	if dir == "" {
		return
	}
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return
	}
	panelSHChangeNotify.Call(
		shcneUpdateDir,
		shcnfPathW|shcnfFlush,
		uintptr(unsafe.Pointer(ptr)),
		0,
	)
}

// ─── 从解析结果挑帧 ─────────────────────────────────────────────────────────

// panelIconCacheTargets 是写进 .ico 的帧尺寸。覆盖 shell 实际会用的几档：
// 16（列表/详细信息）、32（任务栏/小图标）、48（中图标）、
// 64/128（中大图标）、256（大图标/超大图标）。
var panelIconCacheTargets = []int{16, 32, 48, 64, 128, 256}

// panelPickIconAtLeast 返回边长 ≥ target 的**最小**源图；都不够大时返回 nil。
// 取最小的是因为「大图缩下去」比「小图拉上去」清晰得多。
func panelPickIconAtLeast(decoded []panelDecodedIcon, target int) *panelDecodedIcon {
	var best *panelDecodedIcon
	for i := range decoded {
		c := &decoded[i]
		if c.size < target {
			continue
		}
		if best == nil || c.size < best.size {
			best = c
		}
	}
	return best
}

// panelIconCacheFrames 把一次解析出的候选编成要写进 .ico 的多帧图（按尺寸从小到大）。
//
// 源图不够大的档位直接**跳过**，不做放大：站点只给了 32×32 的 favicon 时，
// 硬拉出一个 256×256 的帧只会得到一个模糊的块，还不如让 shell 自己缩放 32 那一帧。
// 例外是 16×16 这一档 —— 它是列表视图的最低要求，总要有一帧，实在没有就用最大的源缩下去。
func panelIconCacheFrames(decoded []panelDecodedIcon) []image.Image {
	var frames []image.Image
	for _, target := range panelIconCacheTargets {
		src := panelPickIconAtLeast(decoded, target)
		if src == nil {
			if target != panelIconCacheTargets[0] {
				continue
			}
			// 16 档兜底：挑最大的那张。
			for i := range decoded {
				if src == nil || decoded[i].size > src.size {
					src = &decoded[i]
				}
			}
			if src == nil {
				continue
			}
		}
		if img := panelResample(src.img, target); img != nil {
			frames = append(frames, img)
		}
	}
	return frames
}

// ─── ICO 编码 ───────────────────────────────────────────────────────────────

// panelToNRGBA 把内部用的预乘 RGBA 转成直通 alpha 的 NRGBA。
//
// 必须转：ICO 帧（无论 DIB 还是 PNG）里的 alpha 都是直通语义，而 panelResample
// 产出的是预乘值。直接把预乘值当直通写出去，半透明像素会偏亮（边缘发白）。
func panelToNRGBA(src *image.RGBA) *image.NRGBA {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		so := src.PixOffset(b.Min.X, b.Min.Y+y)
		do := dst.PixOffset(0, y)
		for x := 0; x < b.Dx(); x++ {
			a := src.Pix[so+3]
			dst.Pix[do+3] = a
			switch a {
			case 0:
				// 全透明像素的 RGB 归零：留着预乘前的脏色，某些缩放路径会把它显出来。
			case 255:
				dst.Pix[do] = src.Pix[so]
				dst.Pix[do+1] = src.Pix[so+1]
				dst.Pix[do+2] = src.Pix[so+2]
			default:
				// 反预乘：straight = premultiplied * 255 / alpha。预乘时的舍入
				// 会让结果偶尔溢出 255，必须夹住。
				for i := 0; i < 3; i++ {
					v := uint32(src.Pix[so+i]) * 0xff / uint32(a)
					if v > 0xff {
						v = 0xff
					}
					dst.Pix[do+i] = uint8(v)
				}
			}
			so += 4
			do += 4
		}
	}
	return dst
}

// encodePanelICO 把若干帧打成单个 .ico。frames 为 nil / 全部无效时返回 nil。
func encodePanelICO(frames []image.Image) []byte {
	type entry struct {
		data []byte
		size int
	}
	entries := make([]entry, 0, len(frames))

	for _, f := range frames {
		if f == nil {
			continue
		}
		rgba, ok := f.(*image.RGBA)
		if !ok {
			rgba = panelResample(f, f.Bounds().Dx())
		}
		img := panelToNRGBA(rgba)
		if img == nil {
			continue
		}
		b := img.Bounds()
		size := maxInt(b.Dx(), b.Dy())
		if size <= 0 {
			continue
		}
		if size > panelICOMaxSize {
			img = panelToNRGBA(panelResample(img, panelICOMaxSize))
			if img == nil {
				continue
			}
			size = panelICOMaxSize
		}

		var data []byte
		if size <= panelICODIBMaxSize {
			data = panelICODIBFrame(img)
		} else {
			data = panelICOPNGFrame(img)
		}
		if len(data) == 0 {
			continue
		}
		entries = append(entries, entry{data: data, size: size})
	}

	if len(entries) == 0 {
		return nil
	}

	var buf bytes.Buffer
	// ICONDIR：reserved(2)=0, type(2)=1(图标), count(2)
	buf.Write([]byte{0, 0, 1, 0})
	_ = binary.Write(&buf, binary.LittleEndian, uint16(len(entries)))

	// ICONDIRENTRY 表：每项 16 字节，偏移从整个文件头之后开始算。
	offset := 6 + 16*len(entries)
	for _, e := range entries {
		dim := byte(e.size)
		if e.size >= panelICOMaxSize {
			dim = 0 // 256 在这里必须写 0（字段只有 1 字节）
		}
		buf.Write([]byte{dim, dim, 0, 0})                                // 宽、高、调色板色数、保留
		_ = binary.Write(&buf, binary.LittleEndian, uint16(1))           // 色彩平面数
		_ = binary.Write(&buf, binary.LittleEndian, uint16(32))          // 每像素位数
		_ = binary.Write(&buf, binary.LittleEndian, uint32(len(e.data))) // 帧数据长度
		_ = binary.Write(&buf, binary.LittleEndian, uint32(offset))      // 帧数据偏移
		offset += len(e.data)
	}
	for _, e := range entries {
		buf.Write(e.data)
	}
	return buf.Bytes()
}

// panelICODIBFrame 生成一帧 DIB（BITMAPINFOHEADER + 像素 + AND 掩码）。
//
// 两个容易写反的地方：
//   - biHeight 必须写**两倍**高度：ICO 的 DIB 帧把 XOR 位图与 AND 掩码当成一张
//     「上下拼接」的位图描述，写一倍高度会让后半截被解读成像素；
//   - 像素是**自底向上**存的（跟 BMP 一样），第一行写的是图像最后一行。
func panelICODIBFrame(img *image.NRGBA) []byte {
	if img == nil {
		return nil
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}

	header := make([]byte, 40)
	binary.LittleEndian.PutUint32(header[0:], 40)          // biSize
	binary.LittleEndian.PutUint32(header[4:], uint32(w))   // biWidth
	binary.LittleEndian.PutUint32(header[8:], uint32(h*2)) // biHeight = XOR + AND
	binary.LittleEndian.PutUint16(header[12:], 1)          // biPlanes
	binary.LittleEndian.PutUint16(header[14:], 32)         // biBitCount
	// biCompression = BI_RGB(0)，其余字段留 0（BI_RGB 下 biSizeImage 可为 0）。

	// AND 掩码每行按 4 字节对齐（1bpp）。
	maskStride := ((w + 31) / 32) * 4

	out := make([]byte, 0, 40+w*h*4+maskStride*h)
	out = append(out, header...)
	for y := h - 1; y >= 0; y-- {
		row := img.Pix[img.PixOffset(0, y):]
		for x := 0; x < w; x++ {
			o := x * 4
			// NRGBA 的字节序是 R,G,B,A；DIB 要 B,G,R,A。
			out = append(out, row[o+2], row[o+1], row[o], row[o+3])
		}
	}
	// AND 掩码全 0：32bpp 帧的透明度由 alpha 通道决定，掩码只是给不支持 alpha 的
	// 老路径留的兼容位。全 0 表示「都不透明」，让 alpha 说话。
	out = append(out, make([]byte, maskStride*h)...)
	return out
}

// panelICOPNGFrame 生成一帧 PNG。用 NRGBA 交给 png.Encode —— 它对 NRGBA 会原样
// 写出直通 alpha，不会像对 RGBA 那样再乘一遍 alpha。
func panelICOPNGFrame(img *image.NRGBA) []byte {
	if img == nil {
		return nil
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil
	}
	return buf.Bytes()
}

// ─── App 侧：管理窗口的「刷新图标」 ───────────────────────────────────────────
//
// 交互刻意分两步，不合成一个「点一下全做完」：
//  1. InspectPanelIcon —— 只回答「能不能做、会变成什么样、会动到哪几个文件」；
//  2. ApplyPanelIcon   —— 用户认可之后才落盘、才去改快捷方式。
//
// 为什么要分开：改写快捷方式图标会动到**用户自己创建的东西**（桌面快捷方式、
// 任务栏固定项）。这种事必须先把「会改成什么」摆出来让人点头，不能点一下就直接改。

func (a *App) iconCacheDir() string {
	configPath := ""
	if a != nil && a.config != nil {
		configPath = a.config.path
	}
	return panelIconCacheDirFor(configPath)
}

// cachedPanelIconPath 返回该面板已缓存好的 .ico 路径；没有缓存时返回空串。
// 供「创建快捷方式」时顺手带上站点图标用（缓存过就直接用，不用再点一次刷新）。
func (a *App) cachedPanelIconPath(panelID string) string {
	dir := a.iconCacheDir()
	if !panelIconCached(dir, panelID) {
		return ""
	}
	return panelIconCachePathIn(dir, panelID)
}

// PanelIconPreview 是「刷新图标」按钮的预检结果，直接决定前端说什么话。
type PanelIconPreview struct {
	// Available 为真表示站点给出了能用的大图标，可以缓存并改写快捷方式。
	Available bool `json:"available"`
	// Reason 是 Available 为假时给用户看的一句话（前端直接显示，不再自己组织措辞）。
	Reason string `json:"reason"`

	PanelName string `json:"panelName"`
	TabName   string `json:"tabName"`
	PageURL   string `json:"pageURL"`
	Host      string `json:"host"`

	// Sizes 是即将写进 .ico 的帧尺寸（从小到大，单位像素）。
	Sizes []int `json:"sizes"`
	// Preview 是 data:image/png;base64,... 的图标预览，前端直接塞进 <img src>。
	Preview string `json:"preview"`
	// Shortcuts 是这次会被改到图标的 .lnk（桌面保留的那一份 + 任务栏固定项）。
	Shortcuts []string `json:"shortcuts"`
	// IconPath 是缓存将要写到（或已经写过）的位置，让用户知道东西放哪了。
	IconPath string `json:"iconPath"`
	// AlreadyCached 表示这个 .ico 之前已经生成过，这次是覆盖。
	AlreadyCached bool `json:"alreadyCached"`

	// ShortcutPath 是桌面上**已经存在**的、该分组的快捷方式（空串表示没有）。
	// 非空时本次是覆盖它 —— 前端要把这件事说出来再让用户点头。
	ShortcutPath string `json:"shortcutPath"`
	// WillCreateShortcut 为真表示本次会顺手在桌面新建一个快捷方式。
	//
	// 没有它的话，「刷新图标」在桌面没快捷方式时就只是一次静悄悄的文件下载 ——
	// 用户点完什么也看不到。
	WillCreateShortcut bool `json:"willCreateShortcut"`
	// Duplicates 是桌面上该分组多余的快捷方式，本次会收敛掉（一个分组只留一份）。
	Duplicates []string `json:"duplicates"`
}

// PanelIconApplyResult 是一次「刷新图标」的实际结果。
//
// 分开报 Updated / Failed 而不是一句「成功」：改写 .lnk 是逐个文件做的，
// 完全可能出现「桌面那个改了、任务栏固定项没改成」。含糊其辞会让用户以为全都好了。
type PanelIconApplyResult struct {
	IconPath string   `json:"iconPath"`
	Updated  []string `json:"updated"`
	Failed   []string `json:"failed"`
	// CreatedShortcut 为真表示这次顺带新建了桌面快捷方式（原本桌面上没有）。
	CreatedShortcut bool `json:"createdShortcut"`
	// Removed 是顺带收敛掉的、这个分组遗留在桌面上的多余快捷方式。
	Removed []string `json:"removed"`
}

// panelIconPlan 是一次「刷新图标」要动的全部东西，预检与实际执行共用同一套计算。
type panelIconPlan struct {
	sample panelIconSample
	frames []image.Image
	// shortcuts 是会被改到图标的 .lnk：桌面保留的那一份 + 任务栏固定项。
	shortcuts []string
	// duplicates 是桌面上该分组多余的快捷方式，本次收敛掉（一个分组只留一份）。
	duplicates []string
	// keep 是桌面上该保留的那一份；空串表示桌面上没有，本次要新建一个。
	keep string
	// desktop 是桌面目录（取不到时为空，此时不动桌面）。
	desktop  string
	iconPath string
}

// isDesktopDuplicate 报告 lnk 是不是桌面上该分组多余的那一份（该清理的）。
// 判定要有 desktop 可用，否则一律不当重复 —— 宁可漏清，也不能在拿不到桌面目录时瞎删。
func (p panelIconPlan) isDesktopDuplicate(lnk string) bool {
	if p.desktop == "" || strings.EqualFold(lnk, p.keep) {
		return false
	}
	return strings.EqualFold(filepath.Dir(lnk), p.desktop)
}

// planPanelIcon 算出「这个面板现在能不能刷图标、用哪几张图、会动到哪些 .lnk」。
//
// 返回的 error 是「这事本身没法做」（面板不存在之类）；「做不了」属于业务结果，
// 走 PanelIconPreview.Available=false + Reason，不当错误抛 —— 前端要拿它显示，
// 不是弹一个异常框。
func (a *App) planPanelIcon(cfg PanelConfig) (panelIconPlan, PanelIconPreview, error) {
	dir := a.iconCacheDir()
	preview := PanelIconPreview{
		PanelName:     cfg.Name,
		IconPath:      panelIconCachePathIn(dir, cfg.ID),
		AlreadyCached: panelIconCached(dir, cfg.ID),
	}

	if len(cfg.Tabs) == 0 {
		preview.Reason = errIconReasonNoTabs
		return panelIconPlan{}, preview, nil
	}
	// 多标签一律用**第一个标签**的图标：缓存是分组级的，一个分组只能有一份 .ico，
	// 而快捷方式也是指向整个分组的。用「当前正显示的那个标签」会让图标跟着用户
	// 切标签而变，同一个快捷方式两次刷新出来不一样，没法解释。
	first := cfg.Tabs[0]
	preview.TabName = first.Name
	preview.PageURL = first.URL

	sample, ok := lookupPanelIconSample(first.ID)
	if !ok {
		preview.Reason = errIconReasonNotOpen
		return panelIconPlan{}, preview, nil
	}
	preview.Host = sample.host
	if sample.pageURL != "" {
		preview.PageURL = sample.pageURL
	}
	if len(sample.decoded) == 0 {
		preview.Reason = errIconReasonNoIcon
		return panelIconPlan{}, preview, nil
	}

	frames := panelIconCacheFrames(sample.decoded)
	if len(frames) == 0 {
		preview.Reason = errIconReasonUndecodable
		return panelIconPlan{}, preview, nil
	}

	plan := panelIconPlan{sample: sample, frames: frames, iconPath: preview.IconPath}

	// 把扫描结果分桶：桌面只留一份（多余的收敛掉），固定目录里的逐个改图标。
	// 桌面那份要单独认出来 —— 没有的话本次要新建，而不是「只把图标存下来」。
	if desktop, derr := shortcutDesktopDirectory(); derr == nil {
		plan.desktop = desktop
		plan.keep = findPanelDesktopShortcut(desktop, cfg.ID, cfg.Shortcut)
	}
	for _, lnk := range listPanelShortcutsForIcon(cfg.ID, cfg.Shortcut) {
		if plan.isDesktopDuplicate(lnk) {
			plan.duplicates = append(plan.duplicates, lnk)
			continue
		}
		plan.shortcuts = append(plan.shortcuts, lnk)
	}

	preview.Available = true
	preview.Sizes = make([]int, 0, len(frames))
	for _, f := range frames {
		preview.Sizes = append(preview.Sizes, f.Bounds().Dx())
	}
	preview.Preview = panelIconPreviewDataURL(frames)
	preview.Shortcuts = plan.shortcuts
	preview.ShortcutPath = plan.keep
	preview.WillCreateShortcut = plan.keep == ""
	preview.Duplicates = plan.duplicates
	return plan, preview, nil
}

// panelIconPreviewDataURL 把最大的一帧缩到 64×64 编成 data URL，供对话框里预览。
func panelIconPreviewDataURL(frames []image.Image) string {
	var biggest image.Image
	for _, f := range frames {
		if biggest == nil || f.Bounds().Dx() > biggest.Bounds().Dx() {
			biggest = f
		}
	}
	if biggest == nil {
		return ""
	}
	const previewSide = 64
	img := panelToNRGBA(panelResample(biggest, previewSide))
	if img == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

// InspectPanelIcon 是「刷新图标」的第一步：只做检查与预告，不写任何文件。
func (a *App) InspectPanelIcon(id string) (PanelIconPreview, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return PanelIconPreview{}, errCode(errPanelNotFound)
	}
	_, preview, err := a.planPanelIcon(cfg)
	return preview, err
}

// ApplyPanelIcon 是「刷新图标」的第二步：用户认可之后才真正动手。
//
// 顺序：写 .ico → 桌面上**创建或覆盖**那份快捷方式 → 逐个改写任务栏固定项 → 通知 shell 重画。
// 先写 .ico 是因为快捷方式要指向它，文件得先存在；万一写失败就整件事中止，
// 不留「快捷方式指着一个不存在的图标」这种状态。
//
// 桌面那份没有就新建：不然桌面没快捷方式时，这个按钮点完只是把 .ico 悄悄存进文件夹，
// 用户看不到任何变化。有就覆盖它，不再新增副本。
func (a *App) ApplyPanelIcon(id string) (PanelIconApplyResult, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return PanelIconApplyResult{}, errCode(errPanelNotFound)
	}
	plan, preview, err := a.planPanelIcon(cfg)
	if err != nil {
		return PanelIconApplyResult{}, err
	}
	if !preview.Available {
		return PanelIconApplyResult{}, fmt.Errorf("%s", preview.Reason)
	}

	iconPath, err := panelIconCacheWrite(a.iconCacheDir(), cfg.ID, plan.frames)
	if err != nil {
		return PanelIconApplyResult{}, err
	}

	result := PanelIconApplyResult{IconPath: iconPath}

	// 桌面那份：没有就创建一份；已经有了就**只改图标，不动文件名**。
	//
	// 为什么已有时不走 createDesktopShortcut（它会把名字同步成当前面板名）：「刷新图标」是用户
	// 点名要做的那件事，顺手把文件名也改掉属于没人要求的副作用。名字同步只发生在
	// 「桌面快捷方式」按钮与面板改名这两条路径上（见 docs/behavior.md#桌面快捷方式）。
	desktop := listPanelShortcuts(cfg.ID, cfg.Shortcut)
	if len(desktop) > 0 {
		lnk := desktop[0]
		if err := setShortcutIcon(lnk, iconPath); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("桌面快捷方式（%v）", err))
		} else {
			result.Updated = append(result.Updated, lnk)
			_ = a.config.setShortcut(cfg.ID, lnk)
			// 顺手收敛：桌面还留着这个分组从前的多份时，只留刚改的这一份。
			result.Removed = a.collapseExtraShortcuts(cfg, lnk)
		}
	} else if exePath, exeErr := os.Executable(); exeErr != nil {
		result.Failed = append(result.Failed, fmt.Sprintf("桌面快捷方式（获取程序路径失败: %v）", exeErr))
	} else if write, err := createDesktopShortcut(exePath, cfg.ID, cfg.Name, iconPath, cfg.Shortcut); err != nil {
		// 图标已经落盘了，如实说清楚：图标存下了，但桌面快捷方式没弄成。
		result.Failed = append(result.Failed, fmt.Sprintf("桌面快捷方式（%v）", err))
	} else {
		result.Updated = append(result.Updated, write.Path)
		result.CreatedShortcut = write.Created
		_ = a.config.setShortcut(cfg.ID, write.Path)
		result.Removed = a.collapseExtraShortcuts(cfg, write.Path)
	}

	// 任务栏固定项：用户自己固定上去的那些，逐个改图标。桌面那份上面已经处理过。
	handled := make(map[string]bool, len(result.Updated))
	for _, p := range result.Updated {
		handled[strings.ToLower(p)] = true
	}
	for _, lnk := range plan.shortcuts {
		if handled[strings.ToLower(lnk)] {
			continue
		}
		if err := setShortcutIcon(lnk, iconPath); err != nil {
			result.Failed = append(result.Failed, fmt.Sprintf("%s（%v）", lnk, err))
			continue
		}
		result.Updated = append(result.Updated, lnk)
	}

	// 图标文件本身也要通知：shell 对 .ico 有缓存，不吭声的话新老图标会混着显示。
	notified := append([]string{iconPath}, result.Updated...)
	notifyShellIconChanged(notified)
	// 任务栏固定项的图标缓存比桌面顽固，改动过固定目录里的东西就额外踢一下那个目录。
	if pinned, err := shortcutTaskbarPinnedDir(); err == nil {
		for _, lnk := range result.Updated {
			if strings.EqualFold(filepath.Dir(lnk), pinned) {
				notifyShellDirChanged(pinned)
				break
			}
		}
	}
	return result, nil
}

// dropPanelIconCache 在面板被删除时收尾图标缓存。
//
// 两件事，顺序不能反：
//  1. 把还留着的、正用着这份缓存的 .lnk（桌面上的 + 任务栏固定项）改回 exe 自带图标；
//  2. 删掉 .ico 文件。
//
// 先删文件的话，那些 .lnk 会指向一个不存在的图标 —— 桌面上显示成空白方块，
// 而用户完全不知道是自己刚才删了个面板导致的。
func (a *App) dropPanelIconCache(cfg PanelConfig) {
	dir := a.iconCacheDir()
	iconPath := panelIconCachePathIn(dir, cfg.ID)
	if iconPath == "" || !panelIconCached(dir, cfg.ID) {
		return // 从没缓存过：一个字节都不用动
	}

	var touched []string
	for _, lnk := range listPanelShortcutsForIcon(cfg.ID, cfg.Shortcut) {
		current, err := readShortcutIconPath(lnk)
		if err != nil {
			continue
		}
		if !strings.EqualFold(current, iconPath) {
			continue // 这个 .lnk 用的不是我们的缓存图标（用户自己换过），别去动它
		}
		// 传空图标路径 = 回退到 targetPath 的内嵌图标。
		if err := setShortcutIcon(lnk, ""); err == nil {
			touched = append(touched, lnk)
		}
	}

	panelIconRemove(dir, cfg.ID)
	notifyShellIconChanged(append(touched, iconPath))
}
