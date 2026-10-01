//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── sizes 解析 ─────────────────────────────────────────────────────────────

func TestPanelParseIconSizes(t *testing.T) {
	cases := []struct {
		in      string
		w, h    int
		comment string
	}{
		{"192x192", 192, 192, "最常见的形式"},
		{"16x16 32x32 48x48", 48, 48, "多尺寸取最大"},
		{"32x32 16x16", 32, 32, "顺序无关，仍取最大"},
		{"any", 0, 0, "矢量（SVG），本实现不处理"},
		{"", 0, 0, "空"},
		{"192", 0, 0, "缺 x，无法解析"},
		{"axb", 0, 0, "非数字"},
		{"0x0", 0, 0, "零尺寸无意义"},
		{" 180X180 ", 180, 180, "大小写与空格"},
		{"any 512x512", 512, 512, "混写时取可解析的那个"},
	}
	for _, c := range cases {
		w, h := panelParseIconSizes(c.in)
		if w != c.w || h != c.h {
			t.Errorf("panelParseIconSizes(%q) = %dx%d，期望 %dx%d（%s）", c.in, w, h, c.w, c.h, c.comment)
		}
	}
}

// ─── 脚本返回值的解析 ───────────────────────────────────────────────────────

// TestPanelDecodeIconPayloadHandlesBothShapes 守住 2026-09-30 那个「静默不工作」的坑。
//
// `ExecuteScript` 返回的是**脚本完成值的 JSON 编码**，于是脚本写 `return out`（对象）
// 与写 `return JSON.stringify(out)`（字符串）会得到两种完全不同的 raw：
// 前者的 raw 是对象，后者是「对象外面又套了一层引号并转义」。
// 当时脚本用了第二种写法、Go 侧直接解结构体，解不出来后静默 return 0 ——
// favicon 永远不出现，日志干净得像什么都没发生。
func TestPanelDecodeIconPayloadHandlesBothShapes(t *testing.T) {
	const inner = `{"manifest":"http://n/m.json","apple":[{"href":"http://n/a.png","sizes":"180x180"}],` +
		`"icons":[{"href":"http://n/i.png","sizes":"16x16 32x32"}],"title":"T","host":"n","origin":"http://n"}`

	// ① 脚本 `return out`：raw 就是对象本身。
	if got, ok := panelDecodeIconPayload(inner); !ok {
		t.Error("对象形态解析失败")
	} else if len(got.Icons) != 1 || got.Icons[0].Href != "http://n/i.png" {
		t.Errorf("对象形态解出的 icons = %+v", got.Icons)
	} else if got.Manifest != "http://n/m.json" || got.Host != "n" {
		t.Errorf("对象形态丢字段：manifest=%q host=%q", got.Manifest, got.Host)
	}

	// ② 脚本 `return JSON.stringify(out)`：raw 是「带引号并转义」的 JSON 字符串。
	quoted := strconv.Quote(inner) // 等价于 JSON.stringify 之后的原始字节
	if got, ok := panelDecodeIconPayload(quoted); !ok {
		t.Error("字符串形态解析失败（这就是 2026-09-30 事故的写法）")
	} else if len(got.Icons) != 1 || got.Icons[0].Href != "http://n/i.png" {
		t.Errorf("字符串形态解出的 icons = %+v", got.Icons)
	}

	// ③ 真正解不出来时要返回 false，而不是交出一个空 payload 冒充成功 ——
	//    空 payload 会让下游画出「首字母色块」而不是保持默认，属于以假乱真。
	for _, bad := range []string{"", "not json", "123", `"a"`, `[1,2]`} {
		if _, ok := panelDecodeIconPayload(bad); ok {
			t.Errorf("%q 不该解析成功", bad)
		}
	}
}

// ─── 候选收集与排序 ─────────────────────────────────────────────────────────

func TestPanelBuildIconCandidates(t *testing.T) {
	payload := panelIconPayload{
		Origin: "https://nas.lan",
		Apple:  []panelIconRef{{Href: "https://nas.lan/apple.png", Sizes: "180x180"}},
		Icons: []panelIconRef{
			{Href: "https://nas.lan/small.png", Sizes: "16x16"},
			{Href: "https://nas.lan/apple.png", Sizes: "180x180"}, // 与 apple 重复
		},
	}
	manifest := []panelIconRef{{Href: "https://nas.lan/m512.png", Sizes: "512x512"}}

	got := panelBuildIconCandidates(payload, manifest)

	if len(got) != 3 {
		t.Fatalf("候选数 = %d，期望 3（manifest 1 + apple 1 + link 2 去重后 1）", len(got))
	}
	// 来源优先级：manifest 在前，其次 apple-touch-icon，最后 link。
	wantOrder := []string{
		"https://nas.lan/m512.png",
		"https://nas.lan/apple.png",
		"https://nas.lan/small.png",
	}
	for i, want := range wantOrder {
		if got[i].url != want {
			t.Errorf("候选[%d] = %s，期望 %s", i, got[i].url, want)
		}
	}

	// 页面自己声明了图标时，**不能**再去猜根目录 /favicon.ico —— 白花一次请求。
	for _, c := range got {
		if c.source == panelIconSourceRootFavicon {
			t.Errorf("页面已声明图标，却仍生成了根目录兜底候选：%s", c.url)
		}
	}
}

func TestPanelBuildIconCandidatesFallsBackToRootFavicon(t *testing.T) {
	got := panelBuildIconCandidates(panelIconPayload{Origin: "https://router.lan"}, nil)
	if len(got) != 1 {
		t.Fatalf("候选数 = %d，期望 1", len(got))
	}
	if got[0].url != "https://router.lan/favicon.ico" {
		t.Errorf("兜底地址 = %s，期望 https://router.lan/favicon.ico", got[0].url)
	}
	if got[0].source != panelIconSourceRootFavicon {
		t.Errorf("来源 = %d，期望根目录兜底", got[0].source)
	}

	// 没有 origin（比如页面还没给出）时不该凭空编地址。
	if got := panelBuildIconCandidates(panelIconPayload{}, nil); len(got) != 0 {
		t.Errorf("无 origin 时仍生成了候选：%v", got)
	}
}

// ─── 择优规则 ───────────────────────────────────────────────────────────────

func TestPanelPickIconPrefersSmallestThatFits(t *testing.T) {
	mk := func(src, size int) panelDecodedIcon {
		return panelDecodedIcon{
			cand: panelIconCandidate{source: src, url: "u"},
			img:  image.NewNRGBA(image.Rect(0, 0, size, size)),
			size: size,
		}
	}
	// 512 的 manifest 大图 + 原生 16 的 link 图标。标题栏要 16 → 必须挑原生那张，
	// 把 512 缩到 16 会糊成一团。
	imgs := []panelDecodedIcon{
		mk(panelIconSourceManifest, 512),
		mk(panelIconSourceLink, 16),
	}
	if got := panelPickIcon(imgs, 16); got == nil || got.size != 16 {
		t.Errorf("target=16 挑到 %v，期望 16（够大的里挑最小的）", got)
	}
	// 任务栏要 48：16 不够大，180 没有，只剩 512 够大 → 只能挑它。
	if got := panelPickIcon(imgs, 48); got == nil || got.size != 512 {
		t.Errorf("target=48 挑到 %v，期望 512（没有够大的之外更小的选择）", got)
	}

	// 都不够大时挑最大的，宁可放大也别空着。
	small := []panelDecodedIcon{mk(panelIconSourceLink, 16), mk(panelIconSourceAppleTouch, 32)}
	if got := panelPickIcon(small, 512); got == nil || got.size != 32 {
		t.Errorf("全都不够大时挑到 %v，期望 32（最大的）", got)
	}

	// 尺寸相同看来源优先级。
	tie := []panelDecodedIcon{mk(panelIconSourceLink, 192), mk(panelIconSourceManifest, 192)}
	if got := panelPickIcon(tie, 48); got == nil || got.cand.source != panelIconSourceManifest {
		t.Errorf("同尺寸并列时挑到来源 %v，期望 manifest 优先", got)
	}

	if got := panelPickIcon(nil, 32); got != nil {
		t.Errorf("没有候选时应返回 nil，实际 %v", got)
	}
}

// ─── ICO 多帧 ───────────────────────────────────────────────────────────────

// makeTestICO 用 PNG 载荷拼一个 .ico（ICONDIR + ICONDIRENTRY + 数据）。
// 真实站点的 /favicon.ico 就是这么塞 16/32/48 三帧的 —— 一次下载喂饱标题栏和任务栏。
func makeTestICO(t *testing.T, sizes ...int) []byte {
	t.Helper()

	payloads := make([][]byte, len(sizes))
	for i, s := range sizes {
		img := image.NewNRGBA(image.Rect(0, 0, s, s))
		for y := 0; y < s; y++ {
			for x := 0; x < s; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: 0xFF, A: 0xFF})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("png.Encode: %v", err)
		}
		payloads[i] = buf.Bytes()
	}

	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // type = icon
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(sizes)))

	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		b := byte(s)
		if s >= 256 {
			b = 0 // 0 表示 256
		}
		out.Write([]byte{b, b, 0, 0})
		_ = binary.Write(&out, binary.LittleEndian, uint16(1))  // planes
		_ = binary.Write(&out, binary.LittleEndian, uint16(32)) // bpp
		_ = binary.Write(&out, binary.LittleEndian, uint32(len(payloads[i])))
		_ = binary.Write(&out, binary.LittleEndian, uint32(offset))
		offset += len(payloads[i])
	}
	for _, p := range payloads {
		out.Write(p)
	}
	return out.Bytes()
}

func TestDecodeICOAllKeepsEveryFrame(t *testing.T) {
	// 16 + 32 两帧：标题栏要 16、任务栏要 32，一次下载两份都齐。
	got := decodeICOAll(makeTestICO(t, 16, 32))
	if len(got) != 2 {
		t.Fatalf("解出 %d 帧，期望 2（ICO 是容器，不能只挑一张）", len(got))
	}
	// 必须按边长升序 —— panelPickIcon 靠这个顺序在同尺寸并列时稳定取舍。
	if b := got[0].Bounds(); b.Dx() != 16 {
		t.Errorf("第一帧 = %dpx，期望 16（升序）", b.Dx())
	}
	if b := got[1].Bounds(); b.Dx() != 32 {
		t.Errorf("第二帧 = %dpx，期望 32", b.Dx())
	}

	// 从 decodeIconImages 进来也要一样（魔数分发的入口）。
	if all := decodeIconImages(makeTestICO(t, 48)); len(all) != 1 {
		t.Errorf("decodeIconImages 对 ICO 返回 %d 帧，期望 1", len(all))
	}
}

func TestDecodeICOBMPToleratesTruncatedData(t *testing.T) {
	// 截断的 ICO 不能让宿主崩掉 —— 页面上挂个半截文件是很常见的事。
	if got := decodeICOAll([]byte{0, 0, 1, 0, 5, 0}); got != nil {
		t.Errorf("条目数声明为 5 但数据不足，应返回 nil，实际 %d 帧", len(got))
	}
	if got := decodeICOBMP([]byte{1, 2, 3}); got != nil {
		t.Errorf("过短的 BMP 应返回 nil，实际 %v", got)
	}
	if got := decodeIconImages([]byte("<svg></svg>")); got != nil {
		t.Errorf("SVG 不支持，应返回 nil，实际 %d 帧", len(got))
	}
}

// ─── 首字母色块 ─────────────────────────────────────────────────────────────

func TestPanelMonogramLetter(t *testing.T) {
	cases := []struct{ host, title, want string }{
		{"openclash.lan", "", "O"},
		{"www.router.lan", "", "R"},
		{"-192.168.1.1", "", "1"},
		{"", "管理面板", "管"},
		{"", "", "?"},
		{"___.lan", "", "L"},
	}
	for _, c := range cases {
		if got := panelMonogramLetter(c.host, c.title); got != c.want {
			t.Errorf("panelMonogramLetter(%q, %q) = %q，期望 %q", c.host, c.title, got, c.want)
		}
	}
}

func TestPanelMonogramColorIsStableAndBounded(t *testing.T) {
	r1, g1, b1 := panelMonogramColor("openclash.lan")
	r2, g2, b2 := panelMonogramColor("openclash.lan")
	if r1 != r2 || g1 != g2 || b1 != b2 {
		t.Error("同一主机名两次取色不一致，任务栏图标会在重启后变色")
	}
	// 大小写不该影响取色（主机名本来就大小写不敏感）。
	r3, g3, b3 := panelMonogramColor("OpenClash.LAN")
	if r1 != r3 || g1 != g3 || b1 != b3 {
		t.Error("主机名大小写影响了取色")
	}
	if r1 == 0 && g1 == 0 && b1 == 0 {
		t.Error("取到纯黑，看不出是色块")
	}
}

func TestPanelMonogramImageShape(t *testing.T) {
	const size = 32
	img := panelMonogramImage("nas.lan", "NAS", size)
	if img == nil {
		t.Fatal("panelMonogramImage 返回 nil")
	}

	// 中心必须不透明（GDI 字体不可用时会退化成一个纯色块，断言依然成立）。
	_, _, _, a := img.At(size/2, size/2).RGBA()
	if a < 0xF000 {
		t.Errorf("中心 alpha = %#x，期望接近不透明", a)
	}
	// 四个角必须在圆角之外，完全透明。
	for _, p := range [][2]int{{0, 0}, {size - 1, 0}, {0, size - 1}, {size - 1, size - 1}} {
		_, _, _, a := img.At(p[0], p[1]).RGBA()
		if a != 0 {
			t.Errorf("角落 (%d,%d) alpha = %#x，期望 0（圆角之外）", p[0], p[1], a)
		}
	}
}

// ─── 标题栏位图的通道序 ─────────────────────────────────────────────────────

// TestBitmapFromImageWritesBGR 钉住 DIB 的字节序。
//
// `image.RGBA.Pix` 是 RGBA，而 32bpp DIB 是 **BGRA** —— 两者必须逐通道换位。
// 2026-09-30 重构时把这段写成了 `copy(buf, pm.Pix)`，于是 R 和 B 互换：
// 纯红的 favicon 在标题栏上显示成**纯蓝**。而这件事**编译、vet、单测、E2E 全都发现不了**
// （E2E 只断言 WM_GETICON 非零，跟画出来的颜色无关），是靠逐像素采样标题栏才逮到的。
// 纯红/纯蓝是刻意的取样色：任何其它颜色换位后都不会这么一望即知。
func TestBitmapFromImageWritesBGR(t *testing.T) {
	const size = 8

	src := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			src.SetNRGBA(x, y, color.NRGBA{R: 0xFF, A: 0xFF}) // 纯红，alpha 满
		}
	}

	hbm := bitmapFromImage(src, size)
	if hbm == 0 {
		t.Fatal("bitmapFromImage 返回 0")
	}
	defer panelDeleteObject.Call(hbm)

	// 用 GetDIBits 把像素读出来 —— 走的是「消费者视角」，不必碰位图内部指针
	// （DIB 段的 bmBits 虽然是给着的，但那属于实现细节，且 uintptr→指针转换会被 go vet 警告）。
	var bmi panelBITMAPINFO
	bmi.Header.Size = uint32(unsafe.Sizeof(bmi.Header))
	bmi.Header.Width = size
	bmi.Header.Height = size
	bmi.Header.Planes = 1
	bmi.Header.BitCount = 32
	bmi.Header.Compression = 0 // BI_RGB

	dc, _, _ := panelCreateCompatDC.Call(0)
	if dc == 0 {
		t.Fatal("CreateCompatibleDC 失败")
	}
	defer panelDeleteDC.Call(dc)

	pix := make([]byte, size*size*4)
	if r, _, _ := panelGetDIBits.Call(
		dc, hbm, 0, uintptr(size),
		uintptr(unsafe.Pointer(&pix[0])),
		uintptr(unsafe.Pointer(&bmi)),
		0, // DIB_RGB_COLORS
	); r == 0 {
		t.Fatal("GetDIBits 失败")
	}

	// 整图都是纯红，看开头 4 个字节即可。
	got := [4]byte{pix[0], pix[1], pix[2], pix[3]}
	// 纯红在 BGRA 字节序里是 B=0 G=0 R=255 A=255。
	if got != [4]byte{0x00, 0x00, 0xFF, 0xFF} {
		t.Errorf("像素 BGRA = %v，期望 [0 0 255 255]。\n"+
			"  若为 [255 0 0 255]，说明把 image.RGBA 的 RGBA 直接 copy 进了 BGRA 的 DIB，"+
			"R 与 B 互换（纯红图标会显示成纯蓝）。", got)
	}
}

// ─── HICON 往返（PNG 路径）─────────────────────────────────────────────────

var (
	panelUser32T = windows.NewLazySystemDLL("user32.dll")
	panelGdi32T  = windows.NewLazySystemDLL("gdi32.dll")

	panelGetIconInfo = panelUser32T.NewProc("GetIconInfo")
	panelGetObjectW  = panelGdi32T.NewProc("GetObjectW")
	panelDrawIconEx  = panelUser32T.NewProc("DrawIconEx")
	panelGetDIBits   = panelGdi32T.NewProc("GetDIBits")
)

// panelDINormal 是 DrawIconEx 的 DI_NORMAL：画图标+掩码，按其 alpha 正常合成。
const panelDINormal = 0x0003

// panelICONINFO2 与 Win32 ICONINFO 内存布局一致。
//
// 别省 xHotspot/yHotspot 这两个字段：它们在 x64 上占满偏移 4..11，
// 漏掉之后 hbmMask 会落到偏移 8（应该 16），于是读出来的是**掩码位图**（1bpp）
// 而不是彩色位图 —— 2026-09-30 就这么被坑过一轮，症状是「16×16 的图标读回来是 1bpp」。
// 生产代码里那版的 ICONINFO 也是同样的错，只是当时表现为 CreateIconIndirect 返回 NULL。
type panelICONINFO2 struct {
	FIcon    int32   // offset 0
	XHotspot uint32  // offset 4
	YHotspot uint32  // offset 8
	HbmMask  uintptr // offset 16（Go 自动在 12 处补 4 字节）
	HbmColor uintptr // offset 24
}

// panelBitmapInfo 与 Win32 BITMAP 布局一致（GetObject 用它读回位图信息）。
type panelBitmapInfo struct {
	Type       int32
	Width      int32
	Height     int32
	WidthBytes int32
	Planes     uint16
	BitsPixel  uint16
	Bits       uintptr
}

// TestPanelHIconRoundTrip 把图标交出去再读回来，验证尺寸、颜色、alpha 都对。
//
// 这条用例替代了原先的「直通/预乘 alpha」用例 —— 那条测的是已经删掉的
// CreateIconIndirect + 手工 DIB/掩码路径。现在走 PNG（CreateIconFromResourceEx），
// 直通 alpha 本来就是 PNG 的语义，**没有可搞反的余地**，所以不必再测「有没有反预乘」；
// 该测的是「交给系统再拿回来，像素还是不是原来那些」，这正是旧路径做不到的事。
//
// 分两处取证，因为没有一个窗口能同时看到两件事：
//   - GetIconInfo → GetObject 才能拿到图标的真实尺寸与位深（但它是 DDB，**bmBits 为 NULL**，
//     读不到像素；而且 GetDIBits 对 32bpp DDB 的 alpha 通道不可靠）；
//   - DrawIconEx 把图标画到**白底**上才能看出透明区有没有保住（但看不出位深）。
//
// 白底是关键：画到黑底上的话，「透明」和「丢掉 alpha 后变成不透明黑」结果一样，
// 断言就成了空的。白底上两者分得清清楚楚。
func TestPanelHIconRoundTrip(t *testing.T) {
	const size = 32
	const block = 8

	src := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := size/2 - block/2; y < size/2+block/2; y++ {
		for x := size/2 - block/2; x < size/2+block/2; x++ {
			// NRGBA 是直通 alpha，与 PNG 一致。
			src.SetNRGBA(x, y, color.NRGBA{R: 0xFF, G: 0x66, B: 0x00, A: 0xFF})
		}
	}

	for _, target := range []int{16, 32, 48} {
		hicon := panelHIconFromImage(src, target)
		if hicon == 0 {
			t.Fatalf("target=%d：panelHIconFromImage 返回 0（PNG 路径失败）", target)
		}

		// ① 尺寸与位深：PNG 载荷必须被系统认下来并缩到目标边长。
		var info panelICONINFO2
		if r, _, _ := panelGetIconInfo.Call(hicon, uintptr(unsafe.Pointer(&info))); r == 0 {
			t.Fatalf("target=%d：GetIconInfo 失败", target)
		}
		var bm panelBitmapInfo
		if r, _, _ := panelGetObjectW.Call(info.HbmColor, unsafe.Sizeof(bm), uintptr(unsafe.Pointer(&bm))); r == 0 {
			t.Fatalf("target=%d：GetObject 读 color bitmap 失败", target)
		}
		if bm.BitsPixel != 32 {
			t.Errorf("target=%d：color bitmap 是 %d bpp，期望 32（1bpp 说明读到了掩码，ICONINFO 布局错）",
				target, bm.BitsPixel)
		}
		if int(bm.Width) != target || int(bm.Height) != target {
			t.Errorf("target=%d：图标位图 = %dx%d，期望 %dx%d", target, bm.Width, bm.Height, target, target)
		}

		// ② 像素：画到白底上读回。
		hbm, buf := panelCreateDIB(target)
		if hbm == 0 {
			t.Fatalf("panelCreateDIB 失败")
		}
		for i := 0; i < target*target; i++ {
			buf[i*4], buf[i*4+1], buf[i*4+2], buf[i*4+3] = 0xFF, 0xFF, 0xFF, 0xFF
		}
		dc, _, _ := panelCreateCompatDC.Call(0)
		if dc == 0 {
			t.Fatalf("CreateCompatibleDC 失败")
		}
		old, _, _ := panelSelectObject.Call(dc, hbm)
		if r, _, _ := panelDrawIconEx.Call(dc, 0, 0, hicon, uintptr(target), uintptr(target), 0, 0, panelDINormal); r == 0 {
			t.Fatalf("target=%d：DrawIconEx 失败", target)
		}
		panelSelectObject.Call(dc, old)
		panelDeleteDC.Call(dc)

		// DIB 是顶向下 + BGRA。
		px := func(x, y int) (uint8, uint8, uint8) {
			o := (y*target + x) * 4
			return buf[o+2], buf[o+1], buf[o]
		}

		// 不透明块中心：颜色原样保留（#FF6600 盖在白底上还是 #FF6600）。
		r, g, b := px(target/2, target/2)
		if r < 245 || g < 90 || g > 115 || b > 10 {
			t.Errorf("target=%d：中心颜色 = #%02X%02X%02X，期望 #FF6600（颜色被 alpha 乘过或丢了？）", target, r, g, b)
		}
		// 透明角落：白底必须原样露出来。若 alpha 丢了，这里会变成不透明的黑/橙。
		for _, pt := range [][2]int{{1, 1}, {target - 2, target - 2}, {1, target - 2}} {
			r, g, b = px(pt[0], pt[1])
			if r < 245 || g < 245 || b < 245 {
				t.Errorf("target=%d：角落 (%d,%d) = #%02X%02X%02X，期望白色（透明区没保住）",
					target, pt[0], pt[1], r, g, b)
			}
		}

		panelDeleteObject.Call(hbm)
		panelDestroyIcon.Call(hicon)
		panelDeleteObject.Call(info.HbmColor)
		panelDeleteObject.Call(info.HbmMask)
	}
}

// TestPanelHIconFromImageRejectsBadInput 空图/零尺寸不该造出图标，也不该崩。
func TestPanelHIconFromImageRejectsBadInput(t *testing.T) {
	if got := panelHIconFromImage(nil, 32); got != 0 {
		t.Error("nil 源应返回 0")
		panelDestroyIcon.Call(got)
	}
	if got := panelHIconFromImage(image.NewNRGBA(image.Rect(0, 0, 8, 8)), 0); got != 0 {
		t.Error("size=0 应返回 0")
		panelDestroyIcon.Call(got)
	}
	if got := panelHIconFromPNG(nil, 32); got != 0 {
		t.Error("空 PNG 载荷应返回 0")
	}
}

// TestPanelResampleAveragesInsteadOfSampling 验证缩小走面积平均而不是最近邻。
//
// 源图用 32×32 的黑白棋盘格缩到 4×4：每个目标格恰好覆盖 8 黑 + 8 白 → 面积平均后
// 必然是均一的中灰。最近邻只会取其中某一个像素，结果是**非黑即白**。
// 这正是「512×512 的大图标缩到 16×16 时边缘抖动」的来源。
//
// 反例提示：如果源图是「左半黑右半白」，4 倍缩小后每个目标格整体落在纯色区里，
// 平均和最近邻结果完全一样 —— 那种样本测不出任何东西，别用。
func TestPanelResampleAveragesInsteadOfSampling(t *testing.T) {
	const srcSize = 32
	src := image.NewRGBA(image.Rect(0, 0, srcSize, srcSize))
	for y := 0; y < srcSize; y++ {
		for x := 0; x < srcSize; x++ {
			if (x+y)%2 == 0 {
				src.SetRGBA(x, y, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
			} else {
				src.SetRGBA(x, y, color.RGBA{A: 0xFF})
			}
		}
	}

	got := panelResample(src, 4)
	if got == nil {
		t.Fatal("panelResample 返回 nil")
	}
	if b := got.Bounds(); b.Dx() != 4 || b.Dy() != 4 {
		t.Fatalf("尺寸 = %dx%d，期望 4x4", b.Dx(), b.Dy())
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			v := got.Pix[got.PixOffset(x, y)]
			// 每个 4×4 目标格覆盖 8 黑 8 白 → 中灰（128 上下）。
			if v < 112 || v > 144 {
				t.Errorf("(%d,%d) = %d，期望 ≈128（面积平均）。若为 0 或 255 说明退化成了最近邻采样。", x, y, v)
			}
			if a := got.Pix[got.PixOffset(x, y)+3]; a != 0xFF {
				t.Errorf("(%d,%d) alpha = %d，期望 255", x, y, a)
			}
		}
	}

	// 同尺寸缩放应当原样保留（不做无谓的重采样）。
	same := panelResample(src, srcSize)
	if same.Pix[same.PixOffset(2, 2)] != 0xFF || same.Pix[same.PixOffset(3, 2)] != 0 {
		t.Error("同尺寸重采样改动了像素")
	}
}

// ─── 可解析性判断 ───────────────────────────────────────────────────────────

func TestPanelIconResolvableURL(t *testing.T) {
	yes := []string{"http://nas.lan/", "https://192.168.1.1/admin"}
	no := []string{"", "about:blank", "chrome-error://chromewebdata/", "data:text/html,x", "file:///c:/x.html"}
	for _, u := range yes {
		if !panelIconResolvableURL(u) {
			t.Errorf("%q 应该去解析图标", u)
		}
	}
	for _, u := range no {
		if panelIconResolvableURL(u) {
			t.Errorf("%q 不该去解析图标", u)
		}
	}
}
