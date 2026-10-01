//go:build windows

package main

// 图标缓存（iconcache_windows.go）的回归用例。
//
// 这组用例守的都是「全绿也发现不了」的错法：
//   - ICO 帧数写少一档 → 任务栏大图标糊成一片，单测不会报错；
//   - DIB 帧写成自顶向下 / RGBA 顺序 → 图标上下颠倒或红蓝互换，只有肉眼看得出；
//   - 快捷方式只改图标却把目标参数丢了 → .lnk 变成打不开的空壳；
//   - 「有没有图标可用」判断错了源头 → 面板窗口都关了还给人写一份过期图标。

import (
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── 测试辅助 ───────────────────────────────────────────────────────────────

// panelLoadImage 走 user32 读 .ico 文件 —— shell 读快捷方式图标用的是同一条路径。
var panelLoadImage = panelUser32T.NewProc("LoadImageW")

// solidRGBA 造一张 size×size 的纯色不透明图。
func solidRGBA(size int, c color.NRGBA) *image.RGBA {
	c.A = 0xFF
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := img.PixOffset(x, y)
			// image.RGBA 的 Pix 是**预乘**存储；alpha=255 时等于直通值。
			img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = c.R, c.G, c.B, 0xFF
		}
	}
	return img
}

// ringRGBA 造一张「中心不透明纯色、四周全透明」的图，用来验证 alpha 通道没有错位。
func ringRGBA(size int, c color.NRGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	lo, hi := size/4, size*3/4
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := img.PixOffset(x, y)
			if x >= lo && x < hi && y >= lo && y < hi {
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = c.R, c.G, c.B, 0xFF
			} else {
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0, 0, 0, 0
			}
		}
	}
	return img
}

type icoFrameInfo struct {
	w, h   int
	offset int
	size   int
}

// parseICOFrames 解析 ICO 容器头，返回每一帧的声明尺寸与数据位置。
func parseICOFrames(t *testing.T, data []byte) []icoFrameInfo {
	t.Helper()
	if len(data) < 6 {
		t.Fatalf("ICO 过短：%d 字节", len(data))
	}
	if data[0] != 0 || data[1] != 0 || data[2] != 1 || data[3] != 0 {
		t.Fatalf("ICO 头不对：% x", data[:4])
	}
	count := int(binary.LittleEndian.Uint16(data[4:6]))
	if len(data) < 6+count*16 {
		t.Fatalf("ICO 目录被截断：count=%d 只有 %d 字节", count, len(data))
	}
	out := make([]icoFrameInfo, 0, count)
	for i := 0; i < count; i++ {
		off := 6 + i*16
		w, h := int(data[off]), int(data[off+1])
		if w == 0 {
			w = 256
		}
		if h == 0 {
			h = 256
		}
		out = append(out, icoFrameInfo{
			w: w, h: h,
			size:   int(binary.LittleEndian.Uint32(data[off+8 : off+12])),
			offset: int(binary.LittleEndian.Uint32(data[off+12 : off+16])),
		})
	}
	return out
}

// stubIconCacheSeams 把「桌面目录 / 本程序文件名 / 任务栏固定目录」三个接缝
// 一起重定向到临时目录，避免用例动到用户真实的桌面与任务栏固定项。
func stubIconCacheSeams(t *testing.T, desktop, pinned string) {
	t.Helper()
	origDesktop, origBase, origPinned := shortcutDesktopDirectory, shortcutSelfExeBase, shortcutTaskbarPinnedDir
	shortcutDesktopDirectory = func() (string, error) { return desktop, nil }
	shortcutSelfExeBase = func() string { return shortcutTestExeName }
	shortcutTaskbarPinnedDir = func() (string, error) { return pinned, nil }
	t.Cleanup(func() {
		shortcutDesktopDirectory = origDesktop
		shortcutSelfExeBase = origBase
		shortcutTaskbarPinnedDir = origPinned
	})
}

func pixelAt(img image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
}

// ─── ICO 编码 ───────────────────────────────────────────────────────────────

// TestEncodePanelICOKeepsEveryFrame 守住「一个 .ico 里必须有多档尺寸」。
//
// 只塞一帧的后果不是报错，而是**某些尺寸下糊**：shell 在 256×256 视图里会把
// 唯一那帧 16×16 拉大，任务栏固定项点开「大图标」就是一坨马赛克。
func TestEncodePanelICOKeepsEveryFrame(t *testing.T) {
	frames := []image.Image{
		solidRGBA(16, color.NRGBA{R: 0xFF}),
		solidRGBA(32, color.NRGBA{R: 0xFF}),
		solidRGBA(48, color.NRGBA{R: 0xFF}),
		solidRGBA(256, color.NRGBA{R: 0xFF}),
	}
	data := encodePanelICO(frames)
	if len(data) == 0 {
		t.Fatal("encodePanelICO 返回空")
	}

	entries := parseICOFrames(t, data)
	want := []int{16, 32, 48, 256}
	if len(entries) != len(want) {
		t.Fatalf("帧数 = %d，期望 %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e.w != want[i] || e.h != want[i] {
			t.Errorf("第 %d 帧声明尺寸 = %dx%d，期望 %d×%d", i, e.w, e.h, want[i], want[i])
		}
		if e.offset+e.size > len(data) {
			t.Errorf("第 %d 帧数据越界：offset=%d size=%d 总长=%d", i, e.offset, e.size, len(data))
		}
	}

	// 自己写的编码器要能被自己写的解码器还原 —— 两边对格式的理解必须一致。
	decoded := decodeICOAll(data)
	if len(decoded) != len(want) {
		t.Fatalf("解码出 %d 帧，期望 %d", len(decoded), len(want))
	}
	for i, img := range decoded {
		b := img.Bounds()
		if b.Dx() != want[i] || b.Dy() != want[i] {
			t.Errorf("解回第 %d 帧 = %dx%d，期望 %d", i, b.Dx(), b.Dy(), want[i])
		}
		if c := pixelAt(img, want[i]/2, want[i]/2); c.R != 0xFF || c.A != 0xFF {
			t.Errorf("解回第 %d 帧中心像素 = %+v，期望不透明纯红", i, c)
		}
	}
}

// TestPanelICODIBFrameIsBottomUpBGR 盯死 DIB 帧最容易写反的两件事。
//
//   - 方向：ICO 的 DIB 帧跟 BMP 一样**自底向上**，文件里第一行存的是图像最后一行。
//     写反的表现是图标上下颠倒 —— 有些字母图标颠倒过来还挺像样，肉眼更难发现；
//   - 通道序：DIB 是 BGRA，而内存里的 image 是 RGBA。写反的表现是红蓝互换，
//     而很多图标是单色的，互换后照样「能看」。
//
// 所以这里造一张**上红下蓝**的图：方向与通道序一次全测。
func TestPanelICODIBFrameIsBottomUpBGR(t *testing.T) {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			p := img.PixOffset(x, y)
			if y < size/2 {
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0xFF, 0x00, 0x00, 0xFF // 上半：红
			} else {
				img.Pix[p], img.Pix[p+1], img.Pix[p+2], img.Pix[p+3] = 0x00, 0x00, 0xFF, 0xFF // 下半：蓝
			}
		}
	}

	frame := panelICODIBFrame(panelToNRGBA(img))
	if len(frame) < 40+size*size*4 {
		t.Fatalf("DIB 帧过短：%d 字节", len(frame))
	}

	if got := binary.LittleEndian.Uint32(frame[8:12]); got != size*2 {
		t.Errorf("biHeight = %d，期望 %d（ICO 的 DIB 帧高度含 AND 掩码，必须是两倍）", got, size*2)
	}

	// 文件里第一个像素应当来自**图像最后一行**（蓝色）。
	first := frame[40:44]
	if first[0] != 0xFF || first[1] != 0x00 || first[2] != 0x00 || first[3] != 0xFF {
		t.Errorf("首像素字节 = % x，期望 ff 00 00 ff（蓝在 B 通道、自底向上）", first)
	}
	// 最后一行（图像第一行，红色）在像素区的末尾 —— 前面还有 AND 掩码，所以按位置算。
	last := frame[40+(size*size-1)*4 : 40+size*size*4]
	if last[0] != 0x00 || last[1] != 0x00 || last[2] != 0xFF || last[3] != 0xFF {
		t.Errorf("末像素字节 = % x，期望 00 00 ff ff（红在 R 通道）", last)
	}
}

// TestPanelICORoundTripKeepsTransparency 验证透明区不会变成黑块。
// 中心不透明、四周透明，往返之后四角 alpha 必须是 0。
func TestPanelICORoundTripKeepsTransparency(t *testing.T) {
	img := ringRGBA(48, color.NRGBA{R: 0x1F, G: 0x6F, B: 0xEB})
	data := encodePanelICO([]image.Image{img})
	if len(data) == 0 {
		t.Fatal("encodePanelICO 返回空")
	}
	decoded := decodeICOAll(data)
	if len(decoded) != 1 {
		t.Fatalf("解码出 %d 帧，期望 1", len(decoded))
	}
	got := decoded[0]
	if c := pixelAt(got, 24, 24); c.A != 0xFF {
		t.Errorf("中心像素 alpha = %d，期望 255（%+v）", c.A, c)
	}
	for _, p := range [][2]int{{0, 0}, {47, 0}, {0, 47}, {47, 47}} {
		if c := pixelAt(got, p[0], p[1]); c.A != 0 {
			t.Errorf("角落 (%d,%d) alpha = %d，期望 0（透明区被写成实色）", p[0], p[1], c.A)
		}
	}
}

// TestPanelIconCacheFramesSkipUpscale 验证「源图不够大就跳过那一档」。
//
// 站点只给了 32×32 时，硬拉出 64/128/256 的帧只会得到一张模糊的图 ——
// 比让 shell 自己去缩放 32 那一帧更糟，而且文件白白大一截。
func TestPanelIconCacheFramesSkipUpscale(t *testing.T) {
	decoded := []panelDecodedIcon{{img: solidRGBA(32, color.NRGBA{G: 0xFF}), size: 32}}
	frames := panelIconCacheFrames(decoded)

	var sizes []int
	for _, f := range frames {
		sizes = append(sizes, f.Bounds().Dx())
	}
	want := []int{16, 32}
	if len(sizes) != len(want) {
		t.Fatalf("帧尺寸 = %v，期望 %v（不该把 32 拉大）", sizes, want)
	}
	for i := range want {
		if sizes[i] != want[i] {
			t.Fatalf("帧尺寸 = %v，期望 %v", sizes, want)
		}
	}

	// 源图只有 8×8（比最小档还小）时，仍然要有 16 这一档 —— 列表视图最低要求。
	tiny := []panelDecodedIcon{{img: solidRGBA(8, color.NRGBA{B: 0xFF}), size: 8}}
	if got := panelIconCacheFrames(tiny); len(got) != 1 || got[0].Bounds().Dx() != 16 {
		t.Errorf("8×8 源图应兜底生成一帧 16×16，实际 %d 帧", len(got))
	}
}

// TestPanelICOLoadableByWindows 是唯一一条「让 Windows 自己认」的验证。
//
// 前面几条都是自家编码配自家解码，两边同时理解错也测不出来。这里把 .ico 交给
// user32!LoadImageW —— 它走的是 shell 读快捷方式图标时用的那条解析路径。
// 返回 0 就说明 Windows 根本不认这份文件，那前面测得再绿也没用。
func TestPanelICOLoadableByWindows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.ico")
	data := encodePanelICO([]image.Image{
		solidRGBA(16, color.NRGBA{R: 0xFF}),
		solidRGBA(32, color.NRGBA{R: 0xFF}),
		solidRGBA(48, color.NRGBA{R: 0xFF}),
		solidRGBA(256, color.NRGBA{R: 0xFF}),
	})
	if len(data) == 0 {
		t.Fatal("encodePanelICO 返回空")
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	const (
		imageIcon      = 1
		lrLoadFromFile = 0x00000010
		lrDefaultSize  = 0x00000040
	)
	h, _, callErr := panelLoadImage.Call(
		0, uintptr(unsafe.Pointer(ptr)), imageIcon, 0, 0, lrLoadFromFile|lrDefaultSize,
	)
	if h == 0 {
		t.Fatalf("Windows 认不出这份 .ico：LoadImageW 返回 0（%v）", callErr)
	}
	panelDestroyIcon.Call(h)
}

// ─── 缓存路径 ───────────────────────────────────────────────────────────────

// TestPanelIconCachePathSanitizesPanelID 守住路径穿越。
// 面板 ID 来自可被手工编辑的 config.json，不能让它把文件写到 icons 目录之外。
func TestPanelIconCachePathSanitizesPanelID(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")

	for _, bad := range []string{"../../evil", `..\..\evil`, "a/b", "a\\b", ".."} {
		got := panelIconCachePathIn(dir, bad)
		if got == "" {
			continue // 全被过滤掉、判定为不可用，也是安全的结果
		}
		if filepath.Dir(got) != dir {
			t.Errorf("panelIconCachePathIn(%q) = %q，跑出了 icons 目录", bad, got)
		}
	}

	if got := panelIconCachePathIn(dir, "../.."); got != "" {
		t.Errorf("纯分隔符的 ID 应被拒绝，实际 %q", got)
	}
	if got := panelIconCachePathIn(dir, "0a1b2c3d-4e5f"); got != filepath.Join(dir, "0a1b2c3d-4e5f.ico") {
		t.Errorf("正常 UUID 路径 = %q", got)
	}
}

// TestPanelIconCacheWriteThenRemove 走一遍落盘、读回、删除。
func TestPanelIconCacheWriteThenRemove(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "icons")
	const panelID = "11112222-3333-4444-5555-666677778888"

	if panelIconCached(dir, panelID) {
		t.Fatal("还没写就说有缓存")
	}

	path, err := panelIconCacheWrite(dir, panelID, []image.Image{solidRGBA(32, color.NRGBA{R: 0x11, G: 0x22, B: 0x33})})
	if err != nil {
		t.Fatalf("panelIconCacheWrite: %v", err)
	}
	if !panelIconCached(dir, panelID) {
		t.Error("写完却说没有缓存")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回: %v", err)
	}
	if imgs := decodeICOAll(data); len(imgs) == 0 {
		t.Error("写出的文件解不出任何一帧")
	}

	if !panelIconRemove(dir, panelID) {
		t.Error("panelIconRemove 报告没删掉")
	}
	if panelIconCached(dir, panelID) {
		t.Error("删完还说有缓存")
	}
	// 目录里不该留下 .tmp 之类的半成品。
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("留下了临时文件 %s", e.Name())
		}
	}
}

// ─── 快捷方式图标改写 ───────────────────────────────────────────────────────

// TestSetShortcutIconKeepsTargetAndArgs 盯死「改图标把快捷方式改成空壳」。
//
// IShellLinkW 只有整份 Save：重新创建的对象是空的，不先把原目标读回来就 SetPath，
// 得到的 .lnk 双击没有任何反应 —— 而返回值全是 S_OK。
func TestSetShortcutIconKeepsTargetAndArgs(t *testing.T) {
	desktop := t.TempDir()
	stubIconCacheSeams(t, desktop, t.TempDir())

	lnk := writePanelShortcut(t, desktop, "面板.lnk", "panel-keep-test")
	icon := filepath.Join(t.TempDir(), "site.ico")
	if err := os.WriteFile(icon, encodePanelICO([]image.Image{solidRGBA(32, color.NRGBA{R: 0xFF})}), 0o644); err != nil {
		t.Fatal(err)
	}

	beforeTarget, beforeArgs, err := readShortcutTarget(lnk)
	if err != nil {
		t.Fatalf("读原始快捷方式: %v", err)
	}

	if err := setShortcutIcon(lnk, icon); err != nil {
		t.Fatalf("setShortcutIcon: %v", err)
	}

	afterTarget, afterArgs, err := readShortcutTarget(lnk)
	if err != nil {
		t.Fatalf("改写后读快捷方式: %v", err)
	}
	if afterTarget != beforeTarget {
		t.Errorf("目标被改掉了：%q → %q", beforeTarget, afterTarget)
	}
	if afterArgs != beforeArgs {
		t.Errorf("参数被改掉了：%q → %q", beforeArgs, afterArgs)
	}

	got, err := readShortcutIconPath(lnk)
	if err != nil {
		t.Fatalf("读回图标位置: %v", err)
	}
	if !strings.EqualFold(got, icon) {
		t.Errorf("图标位置 = %q，期望 %q", got, icon)
	}
}

// TestSetShortcutIconEmptyRevertsToExe 验证传空串能退回 exe 自带图标
// （删面板时要靠它把还留着的快捷方式救回来）。
func TestSetShortcutIconEmptyRevertsToExe(t *testing.T) {
	desktop := t.TempDir()
	stubIconCacheSeams(t, desktop, t.TempDir())

	lnk := writePanelShortcut(t, desktop, "面板.lnk", "panel-revert-test")
	icon := filepath.Join(t.TempDir(), "site.ico")
	if err := os.WriteFile(icon, encodePanelICO([]image.Image{solidRGBA(32, color.NRGBA{R: 0xFF})}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setShortcutIcon(lnk, icon); err != nil {
		t.Fatalf("setShortcutIcon: %v", err)
	}
	if err := setShortcutIcon(lnk, ""); err != nil {
		t.Fatalf("回退图标: %v", err)
	}

	got, err := readShortcutIconPath(lnk)
	if err != nil {
		t.Fatal(err)
	}
	target, _, err := readShortcutTarget(lnk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, target) {
		t.Errorf("回退后图标位置 = %q，期望等于目标程序 %q", got, target)
	}
}

// TestListPanelShortcutsForIconCoversTaskbarPinnedDir 守住扫描范围。
//
// 桌面那份和任务栏固定项那份是**两个独立文件**：固定时 Windows 把 .lnk 复制过去。
// 只扫桌面的话，用户在任务栏上看到的图标不会有任何变化 —— 那正是他报的现象。
func TestListPanelShortcutsForIconCoversTaskbarPinnedDir(t *testing.T) {
	desktop := t.TempDir()
	pinned := t.TempDir()
	stubIconCacheSeams(t, desktop, pinned)

	const mine, other = "panel-mine", "panel-other"
	desktopLnk := writePanelShortcut(t, desktop, "桌面.lnk", mine)
	pinnedLnk := writePanelShortcut(t, pinned, "固定项.lnk", mine)
	writePanelShortcut(t, pinned, "别人的固定项.lnk", other)

	got := listPanelShortcutsForIcon(mine, "")
	if len(got) != 2 {
		t.Fatalf("扫到 %d 个快捷方式（%v），期望 2 个（桌面 + 固定项）", len(got), got)
	}
	found := map[string]bool{}
	for _, p := range got {
		found[strings.ToLower(p)] = true
	}
	if !found[strings.ToLower(desktopLnk)] || !found[strings.ToLower(pinnedLnk)] {
		t.Errorf("扫描结果不含预期文件：%v", got)
	}

	// 删除面板时的清理范围必须**仍然只扫桌面**：把用户亲手固定的项目也删掉是越界。
	if got := listPanelShortcuts(mine, ""); len(got) != 1 || !strings.EqualFold(got[0], desktopLnk) {
		t.Errorf("listPanelShortcuts 扫描范围被放大了：%v（删面板会连固定项一起删）", got)
	}
}

// ─── 预检与应用 ─────────────────────────────────────────────────────────────

// TestPlanPanelIconStates 覆盖「能不能刷图标」的三种情况。
//
// 三种要分清楚，因为用户看到的提示完全不同：
//   - 窗口没开过 → 得先去打开，等页面出来；
//   - 窗口开了但站点没图标 → 「当前地址没有图标可用，只能用默认图标」；
//   - 有图标 → 可以刷。
//
// 如果「没开过」和「没图标」混成一句，用户会去站点上白找半天图标。
func TestPlanPanelIconStates(t *testing.T) {
	stubIconCacheSeams(t, t.TempDir(), t.TempDir())

	app := &App{config: newTestStore(t)}
	const tabID = "tab-plan-states"
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	cfg := PanelConfig{
		ID:      "panel-plan-states",
		Name:    "预检",
		Tabs:    []PanelTab{{ID: tabID, Name: "第一标签", URL: "http://nas.lan/"}},
		Enabled: true,
	}

	// ① 窗口没开过：注册表里没有任何样本。
	_, preview, err := app.planPanelIcon(cfg)
	if err != nil {
		t.Fatalf("planPanelIcon: %v", err)
	}
	if preview.Available {
		t.Error("没有样本时说成了可用")
	}
	if preview.Reason != errIconReasonNotOpen {
		t.Errorf("原因应为错误码 %q（前端按码翻译），实际 %q", errIconReasonNotOpen, preview.Reason)
	}

	// ② 窗口开过，但站点确实没有图标 —— 样本在，候选为空。
	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: cfg.ID, host: "nas.lan", pageURL: "http://nas.lan/",
	})
	if _, preview, _ = app.planPanelIcon(cfg); preview.Available {
		t.Error("站点没有图标时说成了可用")
	} else if preview.Reason != errIconReasonNoIcon {
		t.Errorf("原因应为错误码 %q（前端按码翻译），实际 %q", errIconReasonNoIcon, preview.Reason)
	}

	// ③ 站点给了图标。
	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: cfg.ID, host: "nas.lan", pageURL: "http://nas.lan/",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{R: 0xDB, G: 0x27, B: 0x77}), size: 64}},
	})
	plan, preview, err := app.planPanelIcon(cfg)
	if err != nil {
		t.Fatalf("planPanelIcon: %v", err)
	}
	if !preview.Available {
		t.Fatalf("有图标时说成不可用：%s", preview.Reason)
	}
	if len(plan.frames) == 0 || len(preview.Sizes) != len(plan.frames) {
		t.Errorf("帧数与预告尺寸对不上：frames=%d sizes=%v", len(plan.frames), preview.Sizes)
	}
	if !strings.HasPrefix(preview.Preview, "data:image/png;base64,") {
		t.Errorf("预览不是 data URL：%.40q", preview.Preview)
	}
	if preview.Host != "nas.lan" {
		t.Errorf("host = %q", preview.Host)
	}
}

// TestPlanPanelIconUsesFirstTabOnly 守住「多标签只取第一个标签的图标」。
//
// 缓存是**分组级**的（一个分组一份 .ico，快捷方式也指向整个分组）。
// 若跟着「当前显示的标签」走，同一个快捷方式两次刷新会给出不同的图标，没法解释。
func TestPlanPanelIconUsesFirstTabOnly(t *testing.T) {
	stubIconCacheSeams(t, t.TempDir(), t.TempDir())

	app := &App{config: newTestStore(t)}
	t.Cleanup(func() { unregisterPanelIconSamples([]string{"tab-first", "tab-second"}) })

	cfg := PanelConfig{
		ID:   "panel-first-tab",
		Name: "多标签",
		Tabs: []PanelTab{
			{ID: "tab-first", Name: "第一个", URL: "http://a.lan/"},
			{ID: "tab-second", Name: "第二个", URL: "http://b.lan/"},
		},
	}

	// 只有第二个标签解析出了图标：应当**仍不可用**（取的是第一个标签）。
	registerPanelIconSample(panelIconSample{
		tabID: "tab-second", panelID: cfg.ID, host: "b.lan",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{B: 0xFF}), size: 64}},
	})
	if _, preview, _ := app.planPanelIcon(cfg); preview.Available {
		t.Error("只有第二个标签有图标时不该说可用（缓存取的是第一个标签）")
	}

	// 第一个标签也有了 → 可用，且 host 来自第一个标签。
	registerPanelIconSample(panelIconSample{
		tabID: "tab-first", panelID: cfg.ID, host: "a.lan",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{G: 0xFF}), size: 64}},
	})
	_, preview, _ := app.planPanelIcon(cfg)
	if !preview.Available {
		t.Fatal("第一个标签有图标时应当可用")
	}
	if preview.Host != "a.lan" {
		t.Errorf("用了 %q 的图标，期望第一个标签的 a.lan", preview.Host)
	}
}

// TestApplyPanelIconRewritesEveryShortcut 是这条功能的端到端核心：
// 落盘一份 .ico → 桌面快捷方式与任务栏固定项**都**改用它的图标。
func TestApplyPanelIconRewritesEveryShortcut(t *testing.T) {
	desktop := t.TempDir()
	pinned := t.TempDir()
	stubIconCacheSeams(t, desktop, pinned)

	app := &App{config: newTestStore(t)}
	panel, err := app.config.create("应用测试", "http://nas.lan/", true)
	if err != nil {
		t.Fatal(err)
	}
	tabID := panel.Tabs[0].ID
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	desktopLnk := writePanelShortcut(t, desktop, "桌面.lnk", panel.ID)
	pinnedLnk := writePanelShortcut(t, pinned, "固定项.lnk", panel.ID)

	// 还没打开过窗口 → 应用必须被拒绝，且不留任何文件。
	if _, err := app.ApplyPanelIcon(panel.ID); err == nil {
		t.Fatal("没有可用图标时 ApplyPanelIcon 不该成功")
	}
	if panelIconCached(app.iconCacheDir(), panel.ID) {
		t.Error("被拒绝的申请却写下了缓存文件")
	}

	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: panel.ID, host: "nas.lan", pageURL: "http://nas.lan/",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{R: 0xDB, G: 0x27, B: 0x77}), size: 64}},
	})

	result, err := app.ApplyPanelIcon(panel.ID)
	if err != nil {
		t.Fatalf("ApplyPanelIcon: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("有失败项：%v", result.Failed)
	}
	if len(result.Updated) != 2 {
		t.Fatalf("改了 %d 个快捷方式（%v），期望桌面+固定项共 2 个", len(result.Updated), result.Updated)
	}
	if !panelIconCached(app.iconCacheDir(), panel.ID) {
		t.Fatal("缓存文件没写出来")
	}

	for _, lnk := range []string{desktopLnk, pinnedLnk} {
		got, err := readShortcutIconPath(lnk)
		if err != nil {
			t.Fatalf("读 %s 的图标: %v", lnk, err)
		}
		if !strings.EqualFold(got, result.IconPath) {
			t.Errorf("%s 的图标 = %q，期望 %q", lnk, got, result.IconPath)
		}
		target, args, err := readShortcutTarget(lnk)
		if err != nil {
			t.Fatal(err)
		}
		if !shortcutArgsMatchPanel(args, panel.ID) {
			t.Errorf("%s 的参数被动过：target=%q args=%q", lnk, target, args)
		}
	}

	// 再刷一次：覆盖缓存，不该报错，也不该因为「文件已存在」而拒绝。
	if _, err := app.ApplyPanelIcon(panel.ID); err != nil {
		t.Fatalf("重复刷新失败：%v", err)
	}
}

// TestApplyPanelIconCreatesShortcutWhenDesktopHasNone 桌面没有快捷方式时，
// 「刷新图标」不能只是把 .ico 悄悄存下来 —— 它顺手创建一份桌面快捷方式并用上这个图标。
//
// 否则用户点完在桌面上什么都看不到，只是文件夹里多了个文件（2026-09-30 用户原话：
// 「刷新图标后只是下载保存动作，这没啥意义」）。前端主按钮据此叫「创建快捷方式并应用」。
func TestApplyPanelIconCreatesShortcutWhenDesktopHasNone(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	stubIconCacheSeams(t, desktop, pinned)

	app := &App{config: newTestStore(t)}
	panel, err := app.config.create("没有快捷方式", "http://nas.lan/", true)
	if err != nil {
		t.Fatal(err)
	}
	tabID := panel.Tabs[0].ID
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: panel.ID, host: "nas.lan", pageURL: "http://nas.lan/",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{R: 0xDB, G: 0x27, B: 0x77}), size: 64}},
	})

	// 预检要把「本次会创建一份」说出来，前端才敢把按钮写成「创建快捷方式并应用」。
	// 这里必须走 config.get 拿最新配置（create 的返回值是当时的快照，不含后来记下的路径）。
	cfg, _ := app.config.get(panel.ID)
	_, preview, err := app.planPanelIcon(cfg)
	if err != nil {
		t.Fatalf("planPanelIcon: %v", err)
	}
	if !preview.Available {
		t.Fatalf("有图标时应可用：%s", preview.Reason)
	}
	if !preview.WillCreateShortcut {
		t.Error("桌面没有快捷方式时，预检应当说明本次会创建一个")
	}
	if preview.ShortcutPath != "" {
		t.Errorf("桌面本来没有快捷方式，ShortcutPath 却非空：%q", preview.ShortcutPath)
	}

	result, err := app.ApplyPanelIcon(panel.ID)
	if err != nil {
		t.Fatalf("ApplyPanelIcon: %v", err)
	}
	if len(result.Failed) != 0 {
		t.Fatalf("有失败项：%v", result.Failed)
	}
	if !result.CreatedShortcut {
		t.Error("应当报告这次创建了桌面快捷方式")
	}
	if len(result.Updated) != 1 {
		t.Fatalf("应只改到桌面这一份，实际 %v", result.Updated)
	}

	// 配置里必须记下来 —— 否则删面板时这份 .lnk 会漏在桌面上没人管。
	cfg, _ = app.config.get(panel.ID)
	if cfg.Shortcut == "" {
		t.Fatal("新建的桌面快捷方式应记进配置")
	}
	if _, err := os.Stat(cfg.Shortcut); err != nil {
		t.Fatalf("配置记下的路径不存在: %v", err)
	}
	if !strings.EqualFold(filepath.Dir(cfg.Shortcut), desktop) {
		t.Errorf("快捷方式应落在桌面目录，实际 %q", cfg.Shortcut)
	}
	// 图标指向缓存，参数仍能直达这个面板。
	if icon, err := readShortcutIconPath(cfg.Shortcut); err != nil || !strings.EqualFold(icon, result.IconPath) {
		t.Errorf("新建的快捷方式图标 = %q（err=%v），期望 %q", icon, err, result.IconPath)
	}
	if _, args, err := readShortcutTarget(cfg.Shortcut); err != nil || !shortcutArgsMatchPanel(args, panel.ID) {
		t.Errorf("新建的快捷方式参数不对：args=%q err=%v", args, err)
	}
}

// TestPlanPanelIconCollapsesDuplicateDesktopShortcuts 预检要把桌面上多出来的
// 同分组快捷方式点出来（会清理掉），并且**不**把它们算进「会被改到图标」的清单 ——
// 那几份马上就要被删掉，列进「已改用它」里是在骗人。
func TestPlanPanelIconCollapsesDuplicateDesktopShortcuts(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	stubIconCacheSeams(t, desktop, pinned)

	app := &App{config: newTestStore(t)}
	panel, err := app.config.create("多份快捷方式", "http://nas.lan/", true)
	if err != nil {
		t.Fatal(err)
	}
	tabID := panel.Tabs[0].ID
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	keep := writePanelShortcut(t, desktop, "要留的那份.lnk", panel.ID)
	dup := writePanelShortcut(t, desktop, "旧的 (2).lnk", panel.ID)
	if err := app.config.setShortcut(panel.ID, keep); err != nil {
		t.Fatal(err)
	}
	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: panel.ID, host: "nas.lan",
		decoded: []panelDecodedIcon{{img: solidRGBA(64, color.NRGBA{G: 0x9F}), size: 64}},
	})

	// 同上：要拿「已经记下快捷方式」的那份配置，而不是 create 时的快照。
	cfg, _ := app.config.get(panel.ID)
	plan, preview, err := app.planPanelIcon(cfg)
	if err != nil {
		t.Fatalf("planPanelIcon: %v", err)
	}
	if !preview.Available {
		t.Fatalf("应可用：%s", preview.Reason)
	}
	if preview.WillCreateShortcut {
		t.Error("桌面已经有这个分组的一份，不该说要创建")
	}
	if !strings.EqualFold(preview.ShortcutPath, keep) {
		t.Errorf("该保留的那份 = %q，期望配置记录的 %q", preview.ShortcutPath, keep)
	}
	if len(preview.Duplicates) != 1 || !strings.EqualFold(preview.Duplicates[0], dup) {
		t.Fatalf("重复项 = %v，期望只有 %q", preview.Duplicates, dup)
	}
	for _, lnk := range plan.shortcuts {
		if strings.EqualFold(lnk, dup) {
			t.Errorf("马上要被删掉的 %q 不该出现在「会被改到图标」的清单里", dup)
		}
	}

	// 应用之后：重复项清掉、留下的那份用上新图标、配置不变。
	result, err := app.ApplyPanelIcon(panel.ID)
	if err != nil {
		t.Fatalf("ApplyPanelIcon: %v", err)
	}
	if len(result.Removed) != 1 || !strings.EqualFold(result.Removed[0], dup) {
		t.Errorf("应清理 1 个重复项，实际 %v", result.Removed)
	}
	if _, err := os.Stat(dup); !os.IsNotExist(err) {
		t.Error("重复的快捷方式应已被清理")
	}
	if icon, err := readShortcutIconPath(keep); err != nil || !strings.EqualFold(icon, result.IconPath) {
		t.Errorf("留下的那份图标 = %q（err=%v），期望 %q", icon, err, result.IconPath)
	}
	cfg, _ = app.config.get(panel.ID)
	if !strings.EqualFold(cfg.Shortcut, keep) {
		t.Errorf("配置里该仍是 %q，实际 %q", keep, cfg.Shortcut)
	}
}

// TestDeletePanelAlsoDropsIconCache 守住「面板删了、图标留下」这个孤儿。
//
// 只删 .ico 是不够的：还指着它的快捷方式会变成空白图标，用户完全不知道
// 是自己刚删了个面板导致的。所以顺序必须是「先把 .lnk 图标改回 exe，再删 .ico」。
func TestDeletePanelAlsoDropsIconCache(t *testing.T) {
	desktop := t.TempDir()
	pinned := t.TempDir()
	stubIconCacheSeams(t, desktop, pinned)

	app := &App{config: newTestStore(t)}
	panel, err := app.config.create("待删除", "http://nas.lan/", true)
	if err != nil {
		t.Fatal(err)
	}
	tabID := panel.Tabs[0].ID
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	desktopLnk := writePanelShortcut(t, desktop, "桌面.lnk", panel.ID)
	pinnedLnk := writePanelShortcut(t, pinned, "固定项.lnk", panel.ID)
	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: panel.ID, host: "nas.lan",
		decoded: []panelDecodedIcon{{img: solidRGBA(32, color.NRGBA{R: 0xFF}), size: 32}},
	})
	result, err := app.ApplyPanelIcon(panel.ID)
	if err != nil {
		t.Fatal(err)
	}
	iconPath := result.IconPath

	// 保留快捷方式删除面板（removeShortcuts=false）。
	if err := app.DeletePanel(panel.ID, false); err != nil {
		t.Fatalf("DeletePanel: %v", err)
	}

	if panelIconCached(app.iconCacheDir(), panel.ID) {
		t.Error("面板删了，.ico 还留着")
	}
	if _, err := os.Stat(iconPath); err == nil {
		t.Errorf("图标文件 %s 仍然存在", iconPath)
	}

	// 快捷方式按选择保留，但图标必须已经从「指向被删的缓存」改回 exe 自带图标。
	for _, lnk := range []string{desktopLnk, pinnedLnk} {
		if _, err := os.Stat(lnk); err != nil {
			t.Fatalf("%s 应当被保留: %v", lnk, err)
		}
		got, err := readShortcutIconPath(lnk)
		if err != nil {
			t.Fatal(err)
		}
		if strings.EqualFold(got, iconPath) {
			t.Errorf("%s 的图标仍然指向已删除的 %s", lnk, iconPath)
		}
		target, _, err := readShortcutTarget(lnk)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.EqualFold(got, target) {
			t.Errorf("%s 的图标 = %q，期望回退到目标程序 %q", lnk, got, target)
		}
	}
}

// TestDropPanelIconCacheLeavesForeignIconAlone 验证「用户自己换过的图标不碰」。
//
// 如果那个 .lnk 的图标不是我们写的，删面板时把它改回 exe 图标就是擅自改动。
func TestDropPanelIconCacheLeavesForeignIconAlone(t *testing.T) {
	desktop := t.TempDir()
	stubIconCacheSeams(t, desktop, t.TempDir())

	app := &App{config: newTestStore(t)}
	panel, err := app.config.create("别的图标", "http://nas.lan/", true)
	if err != nil {
		t.Fatal(err)
	}
	tabID := panel.Tabs[0].ID
	t.Cleanup(func() { unregisterPanelIconSamples([]string{tabID}) })

	lnk := writePanelShortcut(t, desktop, "桌面.lnk", panel.ID)
	foreign := filepath.Join(t.TempDir(), "user-picked.ico")
	if err := os.WriteFile(foreign, encodePanelICO([]image.Image{solidRGBA(32, color.NRGBA{B: 0xFF})}), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := setShortcutIcon(lnk, foreign); err != nil {
		t.Fatal(err)
	}

	registerPanelIconSample(panelIconSample{
		tabID: tabID, panelID: panel.ID, host: "nas.lan",
		decoded: []panelDecodedIcon{{img: solidRGBA(32, color.NRGBA{R: 0xFF}), size: 32}},
	})
	if _, err := app.ApplyPanelIcon(panel.ID); err != nil {
		t.Fatal(err)
	}
	// 用户又自己换回去了。
	if err := setShortcutIcon(lnk, foreign); err != nil {
		t.Fatal(err)
	}

	app.dropPanelIconCache(panel)

	got, err := readShortcutIconPath(lnk)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(got, foreign) {
		t.Errorf("用户自己选的图标被改掉了：%q → %q", foreign, got)
	}
}

// ─── 进程内样本注册表 ───────────────────────────────────────────────────────

// TestPanelIconSamplesUnregister 守住面板关窗后的清理。
// 不清理的话，窗口都关了「刷新图标」还会说「有图标可用」，然后写一份过期图标出去。
func TestPanelIconSamplesUnregister(t *testing.T) {
	const tabID = "tab-unregister"
	registerPanelIconSample(panelIconSample{tabID: tabID, panelID: "p"})
	if _, ok := lookupPanelIconSample(tabID); !ok {
		t.Fatal("登记后查不到")
	}
	unregisterPanelIconSamples([]string{tabID, ""})
	if _, ok := lookupPanelIconSample(tabID); ok {
		t.Error("注销后还能查到")
	}
	// 空 ID 不该误伤别的记录。
	registerPanelIconSample(panelIconSample{tabID: "tab-other", panelID: "p"})
	unregisterPanelIconSamples([]string{""})
	if _, ok := lookupPanelIconSample("tab-other"); !ok {
		t.Error("注销空 ID 时误删了别的样本")
	}
	unregisterPanelIconSamples([]string{"tab-other"})
}
