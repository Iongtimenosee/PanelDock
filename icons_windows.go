//go:build windows

package main

// 面板窗口图标的解析与安装。
//
// 一个站点往往同时给出好几种图标，尺寸和用途都不同：锁到任务栏时系统要的是**大图标**
// （32×32 起，高 DPI 下 48），而自绘标题栏左上角那个位置只有 16×16。所以流程是
// 「广撒网收集候选 → 统统下下来解码 → 按各自的目标边长分别择优」，而不是只认第一个
// `<link rel=icon>` 就完事。
//
// 候选来源（优先级由高到低）：
//  1. Web App Manifest 的 `icons[]`（PWA 标准，通常带 192/512 的大图）；
//  2. `<link rel="apple-touch-icon">`（通常 180×180）；
//  3. `<link rel~="icon">`（常见的 16/32/48，也可能带 sizes 声明）；
//  4. 站点根目录的 `/favicon.ico`（最后兜底；一个文件里常含 16/32/48 多帧）。
//
// 一个都拿不到、或全部下载/解码失败时，自绘「首字母色块」（monogram），
// 保证任务栏上永远是个有辨识度的图标，而不是系统默认的白纸。
//
// 与 titlebar_windows.go 的分工：那边只管把图标**画**到标题栏上，本文件负责
// 「图标从哪儿来、要哪几个尺寸」。两者通过 tabState 里的几个句柄交接。

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── 常量与 Win32 绑定 ───────────────────────────────────────────────────────

const (
	// 候选来源优先级（小的优先）。同尺寸并列时用它决胜。
	panelIconSourceManifest = iota
	panelIconSourceAppleTouch
	panelIconSourceLink
	panelIconSourceRootFavicon
)

const (
	// panelIconMaxFetch 一次解析最多下载几个候选。页面塞几十个 <link rel=icon>
	// 的站点不少，不设上限会拖住任务栏图标的出现时间。
	panelIconMaxFetch = 8
	// panelIconFetchTimeout 单个候选的下载超时。
	panelIconFetchTimeout = 6 * time.Second
	// panelIconMaxBytes 单个候选的体积上限（大图 512×512 PNG 也就几百 KB）。
	panelIconMaxBytes = 4 << 20
)

const (
	win32WMSetIcon  = 0x0080
	win32WMIconBig  = 1
	win32WMIconSm   = 0
	panelSMCXIcon   = 11
	panelSMCXSMIcon = 49

	// panelIconResVersion 是 CreateIconFromResourceEx 的 dwVer：0x00030000 = Vista 起的
	// 图标资源版本，**也是 PNG 压缩帧被接受的版本**（XP 时代的 0x00020000 只认 DIB 帧）。
	panelIconResVersion = 0x00030000
	// panelIconResIsIcon 对应 fIcon 参数（TRUE = 图标，FALSE = 光标）。
	panelIconResIsIcon = 1
)

var (
	panelGetSystemMetrics         = panelUser32.NewProc("GetSystemMetrics")
	panelGetSystemMetricsForDpi   = panelUser32.NewProc("GetSystemMetricsForDpi")
	panelGetDpiForWindow          = panelUser32.NewProc("GetDpiForWindow")
	panelCreateIconFromResourceEx = panelUser32.NewProc("CreateIconFromResourceEx")
)

// ─── 页面侧收集 ─────────────────────────────────────────────────────────────

// panelIconScript 在页面里把所有图标来源一次性翻出来，返回 JSON。
//
// 为什么不在 Go 侧逐个问页面：每多调一次 ExecuteScript 就多一次跨进程往返，
// 而且页面可能已经导航走了。一次拿全，后面纯本地决策。
//
// manifest 只回传地址，内容由 Go 侧去取 —— manifest 是独立文件（通常同源但不是必须），
// 页面里读不到它的内容（除非同源且能 fetch，那还要跟 CSP 斗）。
const panelIconScript = `(function(){
function abs(u){try{return new URL(u,document.baseURI).href;}catch(e){return '';}}
var out={manifest:'',apple:[],icons:[],title:'',host:'',origin:''};
try{out.title=document.title||'';}catch(e){}
try{out.host=location.hostname||'';}catch(e){}
try{out.origin=location.origin||'';}catch(e){}
try{var m=document.querySelector('link[rel~="manifest"]');if(m){out.manifest=abs(m.getAttribute('href'));}}catch(e){}
try{var a=document.querySelectorAll('link[rel~="apple-touch-icon"],link[rel~="apple-touch-icon-precomposed"]');
for(var i=0;i<a.length;i++){var h=abs(a[i].getAttribute('href'));if(h){out.apple.push({href:h,sizes:a[i].getAttribute('sizes')||''});}}}catch(e){}
try{var l=document.querySelectorAll('link[rel~="icon"]');
for(var i=0;i<l.length;i++){var h=abs(l[i].getAttribute('href'));if(h){out.icons.push({href:h,sizes:l[i].getAttribute('sizes')||''});}}}catch(e){}
return out;})()`

// panelDecodeIconPayload 解析 ExecuteScript 的返回。
//
// **必须容忍两种形态**，因为 ExecuteScript 返回的是「脚本完成值的 JSON 编码」：
//   - 脚本写 `return out;`（对象）   → raw = `{"manifest":...}`，直接解结构体；
//   - 脚本写 `return JSON.stringify(out);`（字符串）→ raw = `"{\"manifest\":...}"`，
//     多了一层引号与转义，直接解结构体**必然失败**。
//
// 2026-09-30 就栽在第二种写法上：脚本把对象多 stringify 了一次，Go 侧解不出来，
// 而失败路径是静默 `return 0` —— 症状是「favicon 永远不出现，日志一片干净」，
// 排查方向还容易被带偏去怀疑 vtable 槽位。两种形态都收下，让这个坑彻底消失。
func panelDecodeIconPayload(raw string) (panelIconPayload, bool) {
	var payload panelIconPayload
	if err := json.Unmarshal([]byte(raw), &payload); err == nil {
		return payload, true
	}
	// 退一步：raw 本身可能是一个 JSON 字符串，里面才是真正要解的内容。
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err != nil {
		return panelIconPayload{}, false
	}
	if err := json.Unmarshal([]byte(inner), &payload); err != nil {
		return panelIconPayload{}, false
	}
	return payload, true
}

// panelIconRef 是页面侧回传的一个图标引用（link 元素；manifest 里的字段叫 src，另有一版）。
type panelIconRef struct {
	Href  string `json:"href"`
	Sizes string `json:"sizes"`
}

// panelIconPayload 是 panelIconScript 的返回结构。
type panelIconPayload struct {
	Manifest string         `json:"manifest"`
	Apple    []panelIconRef `json:"apple"`
	Icons    []panelIconRef `json:"icons"`
	Title    string         `json:"title"`
	Host     string         `json:"host"`
	Origin   string         `json:"origin"`
}

// panelManifestIcon 是 Web App Manifest 里 icons[] 的一项（字段名是 src 不是 href）。
type panelManifestIcon struct {
	Src   string `json:"src"`
	Sizes string `json:"sizes"`
	Type  string `json:"type"`
}

type panelWebManifest struct {
	Icons []panelManifestIcon `json:"icons"`
}

// ─── 候选与择优 ─────────────────────────────────────────────────────────────

type panelIconCandidate struct {
	url    string
	source int
	w, h   int // 页面/清单声明的尺寸；0,0 = 未声明（只能等下载后才知道）
}

type panelDecodedIcon struct {
	cand panelIconCandidate
	img  image.Image
	size int // 实际像素边长（取宽高较大者）
}

// panelParseIconSizes 解析 sizes 属性，返回其中面积最大的一组边长。
//
// 接受的写法："192x192"、"16x16 32x32 48x48"（取最大）、"any"（矢量，本实现不处理 → 0,0）。
// 解析不出来也返回 0,0，交给下载后按真实像素判断。
func panelParseIconSizes(sizes string) (int, int) {
	bestW, bestH := 0, 0
	for _, tok := range strings.Fields(sizes) {
		if strings.EqualFold(tok, "any") {
			continue
		}
		parts := strings.SplitN(strings.ToLower(tok), "x", 2)
		if len(parts) != 2 {
			continue
		}
		w, errW := parseIntStrict(parts[0])
		h, errH := parseIntStrict(parts[1])
		if errW != nil || errH != nil || w <= 0 || h <= 0 {
			continue
		}
		if w*h > bestW*bestH {
			bestW, bestH = w, h
		}
	}
	return bestW, bestH
}

func parseIntStrict(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("空")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("非数字")
		}
		n = n*10 + int(r-'0')
		if n > 1<<16 {
			return 0, fmt.Errorf("过大")
		}
	}
	return n, nil
}

// panelBuildIconCandidates 把页面传回的 payload 摊平成候选列表，按「来源优先级 → 尺寸从大到小」排序。
//
// manifest 的 icons[] 需要额外下载一次 manifest 文件才知道，由调用方补齐后传进来。
func panelBuildIconCandidates(payload panelIconPayload, manifestIcons []panelIconRef) []panelIconCandidate {
	var cands []panelIconCandidate

	add := func(refs []panelIconRef, source int) {
		for _, r := range refs {
			if r.Href == "" {
				continue
			}
			w, h := panelParseIconSizes(r.Sizes)
			cands = append(cands, panelIconCandidate{url: r.Href, source: source, w: w, h: h})
		}
	}

	add(manifestIcons, panelIconSourceManifest)
	add(payload.Apple, panelIconSourceAppleTouch)
	add(payload.Icons, panelIconSourceLink)

	// 根目录 /favicon.ico：只有在页面自己一个都没声明时才值得试。
	// 页面已经声明了图标还去猜根目录，纯属白花一次请求。
	if len(cands) == 0 && payload.Origin != "" {
		cands = append(cands, panelIconCandidate{
			url:    strings.TrimSuffix(payload.Origin, "/") + "/favicon.ico",
			source: panelIconSourceRootFavicon,
		})
	}

	// 去重（同一个地址可能既在 manifest 里又写成 link）。
	seen := make(map[string]bool, len(cands))
	uniq := cands[:0]
	for _, c := range cands {
		if seen[c.url] {
			continue
		}
		seen[c.url] = true
		uniq = append(uniq, c)
	}
	cands = uniq

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].source != cands[j].source {
			return cands[i].source < cands[j].source
		}
		return cands[i].w*cands[i].h > cands[j].w*cands[j].h
	})
	return cands
}

// panelPickIcon 从已解码的候选里挑最合适 target 边长的一张。
//
// 规则与浏览器选 favicon 的思路一致：
//  1. 够大的（size ≥ target）里挑**最小**的 —— 最接近原生尺寸，缩小损失最小；
//  2. 没有够大的，就挑现存最大的 —— 宁可放大，也别让位子空着；
//  3. 尺寸相同看来源优先级（manifest > apple-touch-icon > link > /favicon.ico）。
//
// 「够大的里挑最小的」这一条很关键：manifest 常带 512×512，直接缩到 16 会糊成一团，
// 而页面里那个原生 16×16 反而最锐利。反过来说任务栏要 48 时，180 的 apple-touch-icon
// 又比 512 更合适。
func panelPickIcon(imgs []panelDecodedIcon, target int) *panelDecodedIcon {
	var best *panelDecodedIcon
	for i := range imgs {
		c := &imgs[i]
		if best == nil {
			best = c
			continue
		}
		if panelIconBetter(c, best, target) {
			best = c
		}
	}
	return best
}

func panelIconBetter(a, b *panelDecodedIcon, target int) bool {
	aFits, bFits := a.size >= target, b.size >= target
	if aFits != bFits {
		return aFits
	}
	if aFits {
		if a.size != b.size {
			return a.size < b.size
		}
	} else if a.size != b.size {
		return a.size > b.size
	}
	return a.cand.source < b.cand.source
}

// ─── 下载与解码 ─────────────────────────────────────────────────────────────

// panelFetchBytes 下载一个图标 / 清单文件。
//
// User-Agent 固定带 `PanelDock/` 前缀是**有意的**：E2E 用例靠它把「宿主去取的图标」
// 与「Chromium 自己抓的 favicon」区分开（后者带 Chrome UA），见
// TestE2ETitlebarFollowsRealNavigation。改这里请连带改那条用例的判定。
func panelFetchBytes(rawURL string) []byte {
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "PanelDock/1.0")
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/x-icon,image/*,*/*;q=0.5")

	client := &http.Client{Timeout: panelIconFetchTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, panelIconMaxBytes))
	if err != nil || len(data) < 8 {
		return nil
	}
	return data
}

// panelManifestIcons 取回 manifest 并列出其中的图标（地址已按 manifest 的位置解析为绝对地址）。
func panelManifestIcons(manifestURL string) []panelIconRef {
	data := panelFetchBytes(manifestURL)
	if len(data) == 0 {
		return nil
	}
	var mf panelWebManifest
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil
	}
	base, err := url.Parse(manifestURL)
	if err != nil {
		return nil
	}

	refs := make([]panelIconRef, 0, len(mf.Icons))
	for _, ic := range mf.Icons {
		if ic.Src == "" {
			continue
		}
		href := ic.Src
		if rel, err := url.Parse(ic.Src); err == nil && base != nil {
			href = base.ResolveReference(rel).String()
		}
		refs = append(refs, panelIconRef{Href: href, Sizes: ic.Sizes})
	}
	return refs
}

// decodeIconImages 按魔数把一份图标数据解成**一组**图像（可能多帧）。
//
// ICO 是个容器，常见做法是同一个文件里塞 16/32/48 三帧 —— 一次下载就能同时喂饱
// 标题栏和任务栏，所以这里返回全部帧而不是像以前那样只挑一张。
// SVG 不支持（没有矢量光栅化器），返回空。
func decodeIconImages(data []byte) []image.Image {
	if len(data) < 8 {
		return nil
	}
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}):
		if img, err := png.Decode(bytes.NewReader(data)); err == nil {
			return []image.Image{img}
		}
	case data[0] == 0xFF && data[1] == 0xD8:
		if img, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
			return []image.Image{img}
		}
	case bytes.HasPrefix(data, []byte("GIF8")):
		if img, err := gif.Decode(bytes.NewReader(data)); err == nil {
			return []image.Image{img}
		}
	case data[0] == 0 && data[1] == 0 && data[2] == 1 && data[3] == 0:
		return decodeICOAll(data)
	}
	return nil
}

// decodeICOAll 解析 .ico 的**全部**可解帧，按边长升序返回。
//
// 以前只挑「最接近 16」的一帧，那是标题栏的旧需求；现在同一份数据要服务多个目标尺寸，
// 所以整体交出去让 panelPickIcon 按目标挑。
func decodeICOAll(data []byte) []image.Image {
	if len(data) < 6 {
		return nil
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if count == 0 || len(data) < 6+count*16 {
		return nil
	}

	var imgs []image.Image
	for i := 0; i < count; i++ {
		off := 6 + i*16
		size := int(binary.LittleEndian.Uint32(data[off+8 : off+12]))
		offset := int(binary.LittleEndian.Uint32(data[off+12 : off+16]))
		if size <= 0 || offset < 0 || offset+size > len(data) {
			continue
		}
		payload := data[offset : offset+size]

		var img image.Image
		if bytes.HasPrefix(payload, []byte{0x89, 'P', 'N', 'G'}) {
			if p, err := png.Decode(bytes.NewReader(payload)); err == nil {
				img = p
			}
		} else {
			img = decodeICOBMP(payload)
		}
		if img != nil {
			imgs = append(imgs, img)
		}
	}

	sort.SliceStable(imgs, func(i, j int) bool {
		a, b := imgs[i].Bounds(), imgs[j].Bounds()
		return maxInt(a.Dx(), a.Dy()) < maxInt(b.Dx(), b.Dy())
	})
	return imgs
}

// decodeICOBMP 解码 ICO 里的 BMP 条目（24/32bpp，底行在上，带可选 AND 透明掩码）。
func decodeICOBMP(b []byte) image.Image {
	if len(b) < 40 {
		return nil
	}
	hdrSize := int(binary.LittleEndian.Uint32(b[0:4]))
	if hdrSize < 40 {
		return nil
	}
	w := int(int32(binary.LittleEndian.Uint32(b[4:8])))
	h := int(int32(binary.LittleEndian.Uint32(b[8:12]))) / 2 // ICO 的高度字段含掩码，是实际两倍
	bpp := int(binary.LittleEndian.Uint16(b[14:16]))
	if w <= 0 || h <= 0 || (bpp != 24 && bpp != 32) {
		return nil
	}
	bytesPerPx := bpp / 8
	stride := ((w*bpp + 31) / 32) * 4
	if len(b) < hdrSize+stride*h {
		return nil
	}

	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	allAlphaZero := bpp == 32
	for y := 0; y < h; y++ {
		src := hdrSize + (h-1-y)*stride
		for x := 0; x < w; x++ {
			px := src + x*bytesPerPx
			alpha := byte(0xFF)
			if bpp == 32 {
				alpha = b[px+3]
				if alpha != 0 {
					allAlphaZero = false
				}
			}
			o := img.PixOffset(x, y)
			img.Pix[o] = b[px+2]
			img.Pix[o+1] = b[px+1]
			img.Pix[o+2] = b[px]
			img.Pix[o+3] = alpha
		}
	}
	if bpp == 32 && allAlphaZero {
		// 有些 32bpp 图标 alpha 通道全 0（透明度全在 AND 掩码里），先按不透明处理。
		for o := 3; o < len(img.Pix); o += 4 {
			img.Pix[o] = 0xFF
		}
	}

	// AND 掩码（1bpp，bit=1 表示透明），有就应用。
	maskOff := hdrSize + stride*h
	maskStride := ((w + 31) / 32) * 4
	if len(b) >= maskOff+maskStride*h {
		for y := 0; y < h; y++ {
			src := maskOff + (h-1-y)*maskStride
			for x := 0; x < w; x++ {
				if b[src+x/8]&(0x80>>uint(x%8)) != 0 {
					img.Pix[img.PixOffset(x, y)+3] = 0
				}
			}
		}
	}
	return img
}

// decodeFaviconImage 解出「最接近 16×16」的一帧（旧单图标路径与单测仍在用）。
func decodeFaviconImage(data []byte) image.Image {
	imgs := decodeIconImages(data)
	if len(imgs) == 0 {
		return nil
	}
	var best image.Image
	bestSize := 0
	for _, img := range imgs {
		b := img.Bounds()
		s := maxInt(b.Dx(), b.Dy())
		if best == nil || absInt(s-16) < absInt(bestSize-16) {
			best, bestSize = img, s
		}
	}
	return best
}

// ─── 位图与图标句柄 ─────────────────────────────────────────────────────────

type panelBITMAPINFOHEADER struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type panelBITMAPINFO struct {
	Header panelBITMAPINFOHEADER
	Colors [1]uint32
}

// panelCreateDIB 建一张 size×size 的 32bpp 顶向下 DIB，返回句柄与像素缓冲。
func panelCreateDIB(size int) (uintptr, []byte) {
	var bmi panelBITMAPINFO
	bmi.Header.Size = uint32(unsafe.Sizeof(bmi.Header))
	bmi.Header.Width = int32(size)
	bmi.Header.Height = -int32(size) // 负值 = 顶向下，行序与 Go 图像一致
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = 0 // BI_RGB

	var bits unsafe.Pointer
	hbm, _, _ := panelCreateDIBSection.Call(
		0,
		uintptr(unsafe.Pointer(&bmi)),
		0, // DIB_RGB_COLORS
		uintptr(unsafe.Pointer(&bits)),
		0,
		0,
	)
	if hbm == 0 || bits == nil {
		return 0, nil
	}
	return hbm, unsafe.Slice((*byte)(bits), size*size*4)
}

// panelResample 把 src 缩放成 size×size，返回**预乘 alpha** 的 RGBA。
//
// 缩小走面积平均（盒式滤波），不是最近邻 —— 把 192×192 的 apple-touch-icon 缩到 16×16 时
// 最近邻会丢一整排像素、边缘抖得厉害。放大才退化为最近邻（图标放大本来就少见）。
func panelResample(src image.Image, size int) *image.RGBA {
	if src == nil || size <= 0 {
		return nil
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, size, size))

	for y := 0; y < size; y++ {
		sy0 := b.Min.Y + y*sh/size
		sy1 := b.Min.Y + (y+1)*sh/size
		if sy1 <= sy0 {
			sy1 = sy0 + 1
		}
		for x := 0; x < size; x++ {
			sx0 := b.Min.X + x*sw/size
			sx1 := b.Min.X + (x+1)*sw/size
			if sx1 <= sx0 {
				sx1 = sx0 + 1
			}

			var sr, sg, sb, sa, n uint64
			for yy := sy0; yy < sy1 && yy < b.Max.Y; yy++ {
				for xx := sx0; xx < sx1 && xx < b.Max.X; xx++ {
					// At().RGBA() 返回的正是 alpha 预乘的 16bit 通道，直接累加即可。
					r, g, bl, a := src.At(xx, yy).RGBA()
					sr += uint64(r)
					sg += uint64(g)
					sb += uint64(bl)
					sa += uint64(a)
					n++
				}
			}
			if n == 0 {
				continue
			}
			o := dst.PixOffset(x, y)
			dst.Pix[o] = uint8(sr / n >> 8)
			dst.Pix[o+1] = uint8(sg / n >> 8)
			dst.Pix[o+2] = uint8(sb / n >> 8)
			dst.Pix[o+3] = uint8(sa / n >> 8)
		}
	}
	return dst
}

// bitmapFromImage 生成 size×size 的 32bpp **预乘** alpha DIB，供 AlphaBlend（标题栏绘制）使用。
func bitmapFromImage(src image.Image, size int) uintptr {
	pm := panelResample(src, size)
	if pm == nil {
		return 0
	}
	hbm, buf := panelCreateDIB(size)
	if hbm == 0 {
		return 0
	}
	// **必须逐通道换位，不能用 copy**：DIB 的字节序是 BGRA，而 image.RGBA.Pix 是 RGBA，
	// 直接 copy 会把 R 写进 B 的位置 —— 纯红图标显示成纯蓝。
	//
	// 2026-09-30 把这段从 titlebar_windows.go 搬进本文件时图省事写成了 copy，favicon
	// 颜色整体反了（红↔蓝），而**编译、vet、单测、E2E 全部照常通过**（E2E 只断言
	// WM_GETICON 非零，单测没覆盖这条绘制路径）。是靠逐像素采样标题栏才发现的。
	// 配套回归用例：TestBitmapFromImageWritesBGR。
	for i := 0; i < size*size; i++ {
		o := i * 4
		buf[o] = pm.Pix[o+2]   // B
		buf[o+1] = pm.Pix[o+1] // G
		buf[o+2] = pm.Pix[o]   // R
		buf[o+3] = pm.Pix[o+3] // A
	}
	return hbm
}

// panelHIconFromImage 把一张图变成 HICON（供 WM_SETICON 用）。
//
// 做法是「重采样到目标边长 → 编码成 PNG → CreateIconFromResourceEx」，
// 而**不是** CreateIconIndirect + 手工造 DIB/掩码。
//
// 旧路径被删掉的原因值得记一笔：当时 CreateIconIndirect 稳定返回 NULL、GetLastError 恒为 0，
// 看上去像 Windows 的怪癖，我甚至给它加了个「重试三次」的补丁。真实原因是
// **panelICONINFO 结构体写错了** —— 漏了 xHotspot/yHotspot 两个字段，于是整个结构只有 24 字节、
// hbmMask 落在偏移 8（应为 16）、hbmColor 落在 16（应为 24），系统读到的掩码/位图句柄全是错位的。
// 补上两个字段后 CreateIconIndirect 第一次就成功（实测连调三次全中）。
//
// 也就是说「重试」从头到尾是在掩盖自己的 bug。教训：**任何 Windows 结构体都要按偏移量核一遍**
// （unsafe.Offsetof 打出来看），别凭字段名猜；调用返回 NULL 而 GetLastError 为 0 时，
// 优先怀疑参数结构体，而不是系统的脾气。
//
// 那为什么现在仍然走 PNG 而不是修好的 CreateIconIndirect？因为 PNG 这条路更短：
// 一次调用、不用造掩码、不用管 DIB 方向，而且**直通 alpha 正是 PNG 的语义** ——
// 少了一整套「预乘/直通」搞反的出错空间（AlphaBlend 要预乘、图标格式要直通，弄反就是一圈黑边）。
// 见 TestPanelHIconRoundTrip。附带好处：喂给系统的 PNG 尺寸恰好等于目标边长，
// 不必指望系统自己去缩放一张 512×512 的图标。
func panelHIconFromImage(src image.Image, size int) uintptr {
	if src == nil || size <= 0 {
		return 0
	}
	pm := panelResample(src, size)
	if pm == nil {
		return 0
	}
	var buf bytes.Buffer
	// image/png 对泛型 image.Image 会做 NRGBA 转换，即自动把预乘还原成直通，无需手工反预乘。
	if err := png.Encode(&buf, pm); err != nil {
		return 0
	}
	return panelHIconFromPNG(buf.Bytes(), size)
}

// panelHIconFromPNG 用 PNG 字节造 HICON，返回 0 表示失败（调用方保持既有图标）。
//
// cxDesired/cyDesired 传目标边长：PNG 帧自带尺寸时系统会照用，这里的显式值是为了
// 万一拿到一张尺寸不符的载荷（例如原图直接透传的场景）也能被缩到正确大小。
func panelHIconFromPNG(pngBytes []byte, size int) uintptr {
	if len(pngBytes) == 0 || size <= 0 {
		return 0
	}
	hicon, _, _ := panelCreateIconFromResourceEx.Call(
		uintptr(unsafe.Pointer(&pngBytes[0])),
		uintptr(len(pngBytes)),
		panelIconResIsIcon,
		panelIconResVersion,
		uintptr(size), uintptr(size),
		0, // LR_DEFAULTCOLOR
	)
	return hicon
}

// panelIconTargetsFor 返回该窗口当前 DPI 下「任务栏大图标」与「标题栏小图标」的目标边长。
//
// 进程在 wails.exe.manifest 里声明了 permonitorv2，Windows 要的是 **DPI 缩放后**的尺寸：
// 150% 下的任务栏大图标是 48 而不是 32。硬编码 32 会得到一个被系统拉糊的图标。
func panelIconTargetsFor(hwnd uintptr) (small, large int) {
	small, large = 16, 32
	dpi := uintptr(96)
	if hwnd != 0 && panelGetDpiForWindow.Find() == nil {
		if d, _, _ := panelGetDpiForWindow.Call(hwnd); d >= 96 {
			dpi = d
		}
	}
	if panelGetSystemMetricsForDpi.Find() == nil {
		if v, _, _ := panelGetSystemMetricsForDpi.Call(panelSMCXSMIcon, dpi); v > 0 {
			small = int(v)
		}
		if v, _, _ := panelGetSystemMetricsForDpi.Call(panelSMCXIcon, dpi); v > 0 {
			large = int(v)
		}
		return small, large
	}
	if v, _, _ := panelGetSystemMetrics.Call(panelSMCXSMIcon); v > 0 {
		small = int(v)
	}
	if v, _, _ := panelGetSystemMetrics.Call(panelSMCXIcon); v > 0 {
		large = int(v)
	}
	return small, large
}

// ─── 首字母色块（monogram）─────────────────────────────────────────────────

// panelMonogramPalette 取自常见的界面强调色，整体饱和度接近，混在任务栏里不刺眼。
var panelMonogramPalette = [][3]uint8{
	{0x1F, 0x6F, 0xEB},
	{0x0E, 0x9F, 0x6E},
	{0xD9, 0x77, 0x06},
	{0xDC, 0x26, 0x26},
	{0x7C, 0x3A, 0xED},
	{0x0E, 0x74, 0x90},
	{0xDB, 0x27, 0x77},
	{0x4B, 0x55, 0x63},
}

// panelMonogramLetter 决定色块上写哪个字：优先主机名的首字母，其次标题的首字。
//
// 主机名先剥掉 www.，再跳过连字符之类的分隔符 —— 否则 "www.-example.com" 这种
// 会画出一个「-」，毫无辨识度。
func panelMonogramLetter(host, title string) string {
	base := strings.TrimPrefix(strings.ToLower(host), "www.")
	for _, r := range base {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return strings.ToUpper(string(r))
		}
	}
	return "?"
}

// panelMonogramColor 由主机名稳定地选一个底色（同一站点每次都是同一个颜色）。
func panelMonogramColor(host string) (uint8, uint8, uint8) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(strings.ToLower(host)))
	c := panelMonogramPalette[int(h.Sum32()%uint32(len(panelMonogramPalette)))]
	return c[0], c[1], c[2]
}

// panelRoundedSquareCoverage 返回 (x,y) 处圆角方形的覆盖率（0..1）—— 用距离场算，
// 边缘天然抗锯齿，不必再手写一套画圆角的代码。
func panelRoundedSquareCoverage(x, y, size, radius int) float64 {
	if size <= 0 {
		return 0
	}
	fx, fy := float64(x)+0.5, float64(y)+0.5
	half := float64(size) / 2
	r := float64(radius)
	dx := math.Abs(fx-half) - (half - r)
	dy := math.Abs(fy-half) - (half - r)
	var dist float64
	if dx <= 0 || dy <= 0 {
		dist = math.Max(dx, dy)
	} else {
		dist = math.Sqrt(dx*dx+dy*dy) - r
	}
	// dist < 0 在形状内；用 1px 宽带做线性过渡。
	cov := 0.5 - dist
	if cov <= 0 {
		return 0
	}
	if cov >= 1 {
		return 1
	}
	return cov
}

// panelRenderGlyphCoverage 用 GDI 把首字母画到一张黑底 DIB 上，取灰度当字形覆盖率。
//
// 为什么要绕这一道：GDI 往 32bpp DIB 上画字**不会写 alpha 通道**，直接把结果当图标用
// 会得到一整块透明。所以只在灰度上取「字形盖住多少」，颜色合成留给 Go 侧，
// alpha 通道完全由我们掌控。
func panelRenderGlyphCoverage(letter string, size int) *image.Gray {
	if letter == "" || size <= 0 {
		return nil
	}
	dc, _, _ := panelCreateCompatDC.Call(0)
	if dc == 0 {
		return nil
	}
	defer panelDeleteDC.Call(dc)

	hbm, buf := panelCreateDIB(size)
	if hbm == 0 {
		return nil
	}
	defer panelDeleteObject.Call(hbm)

	oldBM, _, _ := panelSelectObject.Call(dc, hbm)
	defer panelSelectObject.Call(dc, oldBM)

	font := createPanelFont("Microsoft YaHei UI", -int32(float64(size)*0.66), 700)
	if font == 0 {
		return nil
	}
	defer panelDeleteObject.Call(font)
	oldFont, _, _ := panelSelectObject.Call(dc, font)
	defer panelSelectObject.Call(dc, oldFont)

	// 黑底白字：像素亮度直接就是覆盖率。用 ANTIALIASED 而不是 ClearType，
	// 后者的次像素渲染会在 RGB 三个通道上写出不同值，取灰度会有彩边。
	panelSetBkMode.Call(dc, 1)           // TRANSPARENT
	panelSetTextColor.Call(dc, 0xFFFFFF) // COLORREF 0x00BBGGRR，白

	text, err := windows.UTF16PtrFromString(letter)
	if err != nil {
		return nil
	}
	rect := panelRECT{Right: int32(size), Bottom: int32(size)}
	panelDrawTextW.Call(
		dc,
		uintptr(unsafe.Pointer(text)),
		^uintptr(0), // -1：按 NUL 结尾
		uintptr(unsafe.Pointer(&rect)),
		win32DTCenter|win32DTVCenter|win32DTSingleLine,
	)

	out := image.NewGray(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			o := (y*size + x) * 4
			v := buf[o]
			if buf[o+1] > v {
				v = buf[o+1]
			}
			if buf[o+2] > v {
				v = buf[o+2]
			}
			out.Pix[y*size+x] = v
		}
	}
	return out
}

// panelMonogramImage 生成 size×size 的首字母色块。
func panelMonogramImage(host, title string, size int) image.Image {
	if size <= 0 {
		return nil
	}
	letter := panelMonogramLetter(host, title)
	cr, cg, cb := panelMonogramColor(host)
	cover := panelRenderGlyphCoverage(letter, size)

	radius := size / 5
	if radius < 2 {
		radius = 2
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			shape := panelRoundedSquareCoverage(x, y, size, radius)
			if shape <= 0 {
				continue // 圆角外保持全透明
			}
			g := 0.0
			if cover != nil {
				g = float64(cover.Pix[y*size+x]) / 255
			}
			// 底色与白色字形按覆盖率线性插值（直通 alpha：颜色写实际色，透明度写在 A 上）。
			blend := func(base uint8) uint8 {
				return uint8(float64(base)*(1-g) + 255*g + 0.5)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: blend(cr),
				G: blend(cg),
				B: blend(cb),
				A: uint8(shape*255 + 0.5),
			})
		}
	}
	return img
}

// ─── 解析总入口 ─────────────────────────────────────────────────────────────

// panelResolvedIcons 是一次解析的产物，交回 UI 线程安装。
type panelResolvedIcons struct {
	tabID      string
	titleDIB   uintptr // 标题栏绘制用（预乘，16×16）
	smallHIcon uintptr // WM_SETICON(ICON_SMALL)
	bigHIcon   uintptr // WM_SETICON(ICON_BIG)
	monogram   bool
}

// dispose 释放这次解析产出的 GDI 资源（安装失败 / 窗口已关时用）。
func (r *panelResolvedIcons) dispose() {
	if r == nil {
		return
	}
	if r.titleDIB != 0 {
		panelDeleteObject.Call(r.titleDIB)
	}
	if r.smallHIcon != 0 {
		panelDestroyIcon.Call(r.smallHIcon)
	}
	if r.bigHIcon != 0 {
		panelDestroyIcon.Call(r.bigHIcon)
	}
}

// resolveTabIcons 是后台线程里的完整解析流程：收集候选 → 逐个下载解码 → 按目标尺寸择优 →
// 生成标题栏位图与两个 HICON。结果挂到 pendingIcons 上，发私有消息回 UI 线程安装。
//
// 这个函数**不碰 COM、不碰窗口**，只做 HTTP / 解码 / GDI 对象创建，所以能安全地跑在 goroutine 里。
func (p *panelWindow) resolveTabIcons(tabID string, payload panelIconPayload) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	hwnd := p.hwnd
	p.mu.Unlock()

	manifestIcons := []panelIconRef(nil)
	if payload.Manifest != "" {
		manifestIcons = panelManifestIcons(payload.Manifest)
	}
	cands := panelBuildIconCandidates(payload, manifestIcons)

	titleSize := titleBarIconSize
	smallTarget, largeTarget := panelIconTargetsFor(hwnd)

	var decoded []panelDecodedIcon
	for _, c := range cands {
		if len(decoded) >= panelIconMaxFetch {
			break
		}
		data := panelFetchBytes(c.url)
		if len(data) == 0 {
			continue
		}
		for _, img := range decodeIconImages(data) {
			b := img.Bounds()
			s := maxInt(b.Dx(), b.Dy())
			if s <= 0 {
				continue
			}
			decoded = append(decoded, panelDecodedIcon{cand: c, img: img, size: s})
		}
	}

	res := &panelResolvedIcons{tabID: tabID}

	// 留一份样本给管理窗口的「刷新图标」按钮（见 iconcache_windows.go）。
	// 只登记**站点真实给出**的候选，不含下面兜底自绘的首字母色块 ——
	// 色块是「没有图标」时画给自己看的替代品，不该被当成站点图标缓存进快捷方式。
	p.mu.Lock()
	closed := p.closed
	panelID, tabName, pageURL := p.id, "", ""
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			tabName = p.tabs[i].name
			pageURL = p.tabs[i].iconPageURL
			break
		}
	}
	p.mu.Unlock()
	if !closed {
		registerPanelIconSample(panelIconSample{
			tabID:     tabID,
			panelID:   panelID,
			pageURL:   pageURL,
			host:      payload.Host,
			pageTitle: payload.Title,
			tabName:   tabName,
			decoded:   decoded,
		})
	}

	titleImg := panelPickIcon(decoded, titleSize)
	smallImg := panelPickIcon(decoded, smallTarget)
	bigImg := panelPickIcon(decoded, largeTarget)

	// 页面一个图标都没给出（或全挂了）：自绘首字母色块，
	// 标题栏、小图标、大图标三处都要，可能有两处尺寸相同。
	if titleImg == nil && smallImg == nil && bigImg == nil {
		res.monogram = true
		titleSrc := panelMonogramImage(payload.Host, payload.Title, titleSize)
		smallSrc := panelMonogramImage(payload.Host, payload.Title, smallTarget)
		bigSrc := panelMonogramImage(payload.Host, payload.Title, largeTarget)

		res.titleDIB = bitmapFromImage(titleSrc, titleSize)
		res.smallHIcon = panelHIconFromImage(smallSrc, smallTarget)
		res.bigHIcon = panelHIconFromImage(bigSrc, largeTarget)
	} else {
		if titleImg != nil {
			res.titleDIB = bitmapFromImage(titleImg.img, titleSize)
		}
		if smallImg != nil {
			res.smallHIcon = panelHIconFromImage(smallImg.img, smallTarget)
		}
		if bigImg != nil {
			res.bigHIcon = panelHIconFromImage(bigImg.img, largeTarget)
		}
	}

	if res.titleDIB == 0 && res.smallHIcon == 0 && res.bigHIcon == 0 {
		return // 连色块都没做出来（GDI 起不来），保持默认图标
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		res.dispose()
		return
	}
	p.pendingIcons = append(p.pendingIcons, res)
	hwnd = p.hwnd
	p.mu.Unlock()

	if hwnd != 0 {
		panelPostMessage.Call(hwnd, win32WMIconsReady, 0, 0)
	}
}

// ─── UI 线程侧安装 ──────────────────────────────────────────────────────────

// applyPendingIcons 在 UI 线程上把解析好的图标装进各标签，并更新窗口图标。
//
// 之所以要「回 UI 线程」而不是让后台直接写：tabState 的图标句柄会被 paintTitleBar
// 读取并 AlphaBlend，而 WM_SETICON 必须在窗口所属线程上发。这里和绘制同在一个线程上，
// 所以「换新句柄 → 删旧句柄」之间不会插入一次绘制。
func (p *panelWindow) applyPendingIcons() {
	p.mu.Lock()
	if p.closed {
		pending := p.pendingIcons
		p.pendingIcons = nil
		p.mu.Unlock()
		for _, r := range pending {
			r.dispose()
		}
		return
	}

	pending := p.pendingIcons
	p.pendingIcons = nil
	active := p.activeTabID
	hwnd := p.hwnd

	var staleDIBs, staleIcons []uintptr
	var activeRes *panelResolvedIcons

	for _, r := range pending {
		idx := -1
		for i := range p.tabs {
			if p.tabs[i].tabID == r.tabID {
				idx = i
				break
			}
		}
		if idx < 0 {
			p.mu.Unlock()
			r.dispose()
			p.mu.Lock()
			continue
		}

		t := &p.tabs[idx]
		if t.iconTitleDIB != 0 {
			staleDIBs = append(staleDIBs, t.iconTitleDIB)
		}
		if t.iconSmallHIcon != 0 {
			staleIcons = append(staleIcons, t.iconSmallHIcon)
		}
		if t.iconBigHIcon != 0 {
			staleIcons = append(staleIcons, t.iconBigHIcon)
		}
		t.iconTitleDIB = r.titleDIB
		t.iconSmallHIcon = r.smallHIcon
		t.iconBigHIcon = r.bigHIcon
		t.iconMonogram = r.monogram

		if r.tabID == active {
			activeRes = r
		}
	}
	titleHwnd := p.titleBarHwnd
	p.mu.Unlock()

	// 旧资源要等新句柄已经挂上再删：WM_SETICON 换成新图标之后才知道旧的没人用了。
	for _, h := range staleDIBs {
		panelDeleteObject.Call(h)
	}
	for _, h := range staleIcons {
		panelDestroyIcon.Call(h)
	}

	if activeRes != nil {
		applyPanelWindowIcons(hwnd, activeRes.bigHIcon, activeRes.smallHIcon)
	}
	if titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
}

// applyPanelWindowIcons 把图标装到窗口上。ICON_BIG 是任务栏按钮与 Alt+Tab 用的，
// ICON_SMALL 是标题栏（我们自绘，系统那份用不上，但设置上更规矩）。
func applyPanelWindowIcons(hwnd, big, small uintptr) {
	if hwnd == 0 {
		return
	}
	if big != 0 {
		panelSendMessage.Call(hwnd, win32WMSetIcon, win32WMIconBig, big)
	}
	if small != 0 {
		panelSendMessage.Call(hwnd, win32WMSetIcon, win32WMIconSm, small)
	}
}

// applyActiveTabIcons 切换到某标签时把窗口图标换成它的（任务栏按钮跟着切）。
func (p *panelWindow) applyActiveTabIcons(tabID string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	hwnd := p.hwnd
	var big, small uintptr
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			big = p.tabs[i].iconBigHIcon
			small = p.tabs[i].iconSmallHIcon
		}
	}
	p.mu.Unlock()
	applyPanelWindowIcons(hwnd, big, small)
}

// ─── COM 脚本回调 ───────────────────────────────────────────────────────────

type panelScriptHandlerVtbl struct {
	panelIUnknownVtbl
	Invoke panelCOMProc
}

type panelScriptHandler struct {
	Vtbl  *panelScriptHandlerVtbl
	panel *panelWindow
	tabID string
}

var panelScriptHandlerVTable = &panelScriptHandlerVtbl{
	panelIUnknownVtbl: panelIUnknownVtbl{
		QueryInterface: panelCOMProc(windows.NewCallback(panelQueryInterfaceIUnknown)),
		AddRef:         panelCOMProc(windows.NewCallback(panelScriptHandlerAddRef)),
		Release:        panelCOMProc(windows.NewCallback(panelScriptHandlerRelease)),
	},
	Invoke: panelCOMProc(windows.NewCallback(panelScriptHandlerInvoke)),
}

func panelScriptHandlerAddRef(uintptr) uintptr  { return 1 }
func panelScriptHandlerRelease(uintptr) uintptr { return 1 }

func panelScriptHandlerInvoke(h *panelScriptHandler, errorCode uintptr, resultObjectAsJSON *uint16) uintptr {
	if int32(errorCode) < 0 || resultObjectAsJSON == nil {
		return 0
	}
	payload, ok := panelDecodeIconPayload(windows.UTF16PtrToString(resultObjectAsJSON))
	if !ok {
		return 0
	}
	// 网络下载放后台 goroutine，UI 线程不等人；完成后发私有消息回 UI 线程安装。
	go h.panel.resolveTabIcons(h.tabID, payload)
	return 0
}

// panelIconResolvableURL 判断这个地址值不值得去解析图标。
// about:blank、chrome-error:// 之类的内部页面没有图标可言，硬解析只会先画出一个
// 首字母色块，再被随后加载的真页面推翻 —— 白闪一下，不如不画。
func panelIconResolvableURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// requestIcons 用 ExecuteScript 在页面里把图标来源一次性翻出来。
func (p *panelWindow) requestIcons(tabID string, w *panelWebView, pageURL string) {
	if w == nil || !panelIconResolvableURL(pageURL) {
		return
	}
	handler := &panelScriptHandler{Vtbl: panelScriptHandlerVTable, panel: p, tabID: tabID}

	var staleDIBs, staleIcons []uintptr
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	matched := false
	for i := range p.tabs {
		if p.tabs[i].tabID != tabID {
			continue
		}
		matched = true
		if p.tabs[i].iconPageURL == pageURL {
			// 同一个页面已经解析过（刷新失败重试、history 回退等），不重复下载。
			p.mu.Unlock()
			return
		}
		p.tabs[i].iconPageURL = pageURL
		// 换页了：先把上一个站点的图标撤下来。留着它会让标题栏和任务栏挂着
		// 别的站点的图标，直到新图标解析完 —— 那比短暂显示默认地球图标更糟。
		// 清理由本线程做，所以此刻删句柄是安全的（绘制也在这个线程上）。
		staleDIBs = append(staleDIBs, p.tabs[i].iconTitleDIB)
		staleIcons = append(staleIcons, p.tabs[i].iconSmallHIcon, p.tabs[i].iconBigHIcon)
		p.tabs[i].iconTitleDIB = 0
		p.tabs[i].iconSmallHIcon = 0
		p.tabs[i].iconBigHIcon = 0
		p.tabs[i].eventHandlers = append(p.tabs[i].eventHandlers, handler)
		break
	}
	titleHwnd := p.titleBarHwnd
	active := p.activeTabID == tabID
	p.mu.Unlock()

	if !matched {
		return
	}
	for _, h := range staleDIBs {
		if h != 0 {
			panelDeleteObject.Call(h)
		}
	}
	for _, h := range staleIcons {
		if h != 0 {
			panelDestroyIcon.Call(h)
		}
	}
	if active && titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}

	script, err := windows.UTF16PtrFromString(panelIconScript)
	if err != nil {
		return
	}
	w.Vtbl.ExecuteScript.Call(
		uintptr(unsafe.Pointer(w)),
		uintptr(unsafe.Pointer(script)),
		uintptr(unsafe.Pointer(handler)),
	)
}

// ─── 小工具 ─────────────────────────────────────────────────────────────────

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
