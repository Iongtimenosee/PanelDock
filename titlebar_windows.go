//go:build windows

package main

// 自绘标题栏：面板窗口是无边框窗口（WM_NCCALCSIZE 吃掉了系统非客户区），
// 本文件实现它的「标题栏」—— 一个置顶的原生子窗口，从左到右：
//   favicon → 后退/前进/刷新/停止（图标按钮）→ 地址栏（EDIT）→ 置顶开关 → 设置（打开管理面板）
//   → 最小化 / 最大化 / 关闭。
//
// 置顶开关是唯一一个「常态显示状态」的按钮：未置顶画空心图钉（Pin），已置顶画实心图钉
// （Pinned）并加高亮色。其余按钮都只表达动作。
//
// 相关机制分四块：
//  1. 无边框框架（缩放边框命中测试 / 最大化约束 / DWM 阴影与直角）；
//  2. 标题栏子窗口（布局、绘制、悬停、点击）；
//  3. 地址栏 EDIT（子类化接管回车导航）；
//  4. WebView2 事件（驱动地址栏与按钮状态）。
//
// 图标的**来源**不在这里 —— 「从哪儿取、取哪几个尺寸」全在 icons_windows.go，
// 本文件只负责把已经解析好的那一张画到左上角。

import (
	"fmt"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// win32WMSetFont 已在 prompt_modal_windows.go 声明，这里直接用。
	win32EMSetMargins  = 0x00D3
	win32ESAutoHScroll = 0x0080
	// GWLP_WNDPROC = -4（SetWindowLongPtrW 的 nIndex）。
	win32GWLWndProc = ^uintptr(3)

	// BLENDFUNCTION{AC_SRC_OVER, 0, 255, AC_SRC_ALPHA} 按值传参的打包形式。
	blendSrcAlpha = 0x01FF0000

	// 置顶开关「已置顶」时的前景色由配色表给（chrome.accent）—— 深色下 #60a5fa、
	// 浅色下 #2563eb，写死一个会在另一侧糊掉。
)

// 标题栏按钮 ID。nav 前四个与布局数组下标对齐，paint/hit-test 共用。
const (
	tbBtnNone     = -1
	tbBtnBack     = 0
	tbBtnForward  = 1
	tbBtnReload   = 2
	tbBtnStop     = 3
	tbBtnSettings = 4
	tbBtnMin      = 5
	tbBtnMax      = 6
	tbBtnClose    = 7
	// 置顶开关：**新按钮一律往后取号**，别插进中间 —— 这些 ID 是 paint/hit-test/动作分发的
	// 共同索引，插号会让既有按钮全部错位（且编译期不会有任何提示）。
	tbBtnPin = 8
)

// Segoe MDL2 Assets 图标字形（Windows 10/11 自带，自定义标题栏的事实标准做法）。
const (
	tbGlyphBack     = ""
	tbGlyphForward  = ""
	tbGlyphReload   = ""
	tbGlyphStop     = ""
	tbGlyphSettings = ""
	tbGlyphMin      = ""
	tbGlyphMax      = ""
	tbGlyphRestore  = ""
	tbGlyphClose    = ""
	tbGlyphGlobe    = ""
	tbGlyphPin      = "" // Pin：未置顶（空心图钉）
	tbGlyphPinned   = "" // Pinned：已置顶（实心图钉）
)

var (
	// 标题栏 GDI 资源（进程级共享，创建后不释放）。
	panelTitleBarGDIOnce   sync.Once
	panelTbBrushCloseHover uintptr // #e81123（关闭按钮悬停红，与系统一致；两个主题同值，不进配色表）
	panelTbIconFont        uintptr // Segoe MDL2 Assets
	panelAddressFont       uintptr // 地址栏字体
)

// ─── 无边框框架 ─────────────────────────────────────────────────────────────

type panelMINMAXINFO struct {
	Reserved     panelPOINT
	MaxSize      panelPOINT
	MaxPosition  panelPOINT
	MinTrackSize panelPOINT
	MaxTrackSize panelPOINT
}

type panelMONITORINFO struct {
	CbSize    uint32
	RcMonitor panelRECT
	RcWork    panelRECT
	DwFlags   uint32
}

// uintptrToPtr 把「本来就是指针」的 lParam 还原为指针。
// 直接 unsafe.Pointer(uintptr) 会被 go vet 的 unsafeptr 检查拒掉，这里绕一道。
func uintptrToPtr(u uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

// setupPanelFrameless 在窗口创建后调用：强制重算非客户区、找回 DWM 阴影、关掉 Win11 圆角。
//
// 先 SetWindowPos(SWP_FRAMECHANGED)：**这一步不能省**。「客户区=窗口矩形」的判定
// 发生在窗口创建期（WM_NCCALCSIZE），窗口管理器会缓存算出来的边框；不重算的话，
// 创建期那一次若走了默认分支，系统标题栏就会一直留在屏幕上（实测踩到）。
//
// 阴影：NCCALCSIZE 之后 DWM 不再画阴影，扩 1px 玻璃边框换回来（客户区全被
// 子窗口覆盖，这 1px 不可见）。圆角：标题栏/WebView2 子窗口是直角，圆角会被
// 它们的角戳穿，干脆整体直角。
func setupPanelFrameless(hwnd uintptr) {
	panelSetWindowPos.Call(
		hwnd,
		0,
		0, 0, 0, 0,
		win32SWPFrameChanged|win32SWPNOMove|win32SWPNOSize|win32SWPNoZOrder|win32SWPNoActivate,
	)

	margins := [4]int32{1, 1, 1, 1} // MARGINS{cxLeft, cxRight, cyTop, cyBottom}
	panelDwmExtendFrame.Call(hwnd, uintptr(unsafe.Pointer(&margins[0])))

	corner := uint32(1) // DWMWCP_DONOTROUND
	// DWMWA_WINDOW_CORNER_PREFERENCE = 33；Win10 不认识该属性会返回错误，忽略。
	panelDwmSetAttribute.Call(hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
}

// panelFrameHitTest 无边框窗口的命中测试（panelWindowProc 的 WM_NCHITTEST）。
// 与 panel 对象无关，创建早期也会收到，只按几何判定：
//  1. 还原状态下四边 win32FrameBorder 像素是缩放边框（角优先）；
//  2. 顶部 titleBarHeight+tabBarHeight 这条横带是拖拽区（HTCAPTION）——标题栏与标签栏
//     两个子窗口把各自「没有控件 / 没有标签」的空白区穿透（HTTRANSPARENT）到这里，
//     于是拖动/双击最大化/右键系统菜单都是原生行为。**两条横带都要算**：只算标题栏的话，
//     标签右侧那片空白会被判成 HTCLIENT，鼠标在上面拖不动窗口；
//  3. 其余是客户区。
func panelFrameHitTest(hwnd uintptr, lParam uintptr) uintptr {
	x := int32(int16(lParam & 0xFFFF))
	y := int32(int16((lParam >> 16) & 0xFFFF))

	var rect panelRECT
	if ok, _, _ := panelGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return win32HTClient
	}
	cx, cy := x-rect.Left, y-rect.Top
	w, h := rect.Right-rect.Left, rect.Bottom-rect.Top

	if zoomed, _, _ := panelIsZoomed.Call(hwnd); zoomed == 0 {
		onL := cx < win32FrameBorder
		onR := cx >= w-win32FrameBorder
		onT := cy < win32FrameBorder
		onB := cy >= h-win32FrameBorder
		switch {
		case onT && onL:
			return win32HTTopLeft
		case onT && onR:
			return win32HTTopRight
		case onB && onL:
			return win32HTBottomLeft
		case onB && onR:
			return win32HTBottomRight
		case onL:
			return win32HTLeft
		case onR:
			return win32HTRight
		case onT:
			return win32HTTop
		case onB:
			return win32HTBottom
		}
	}
	if cy < titleBarHeight+tabBarHeight {
		return win32HTCaption
	}
	return win32HTClient
}

// panelAdjustMaximizedBounds 把最大化矩形约束回显示器工作区。
// 客户区=窗口矩形之后，系统默认最大化会连任务栏一起盖住。
func panelAdjustMaximizedBounds(hwnd uintptr, lParam uintptr) {
	info := (*panelMINMAXINFO)(uintptrToPtr(lParam))
	mon, _, _ := panelMonitorFromWindow.Call(hwnd, 2) // MONITOR_DEFAULTTONEAREST
	if mon == 0 {
		return
	}
	var mi panelMONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if ok, _, _ := panelGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return
	}
	info.MaxPosition.X = mi.RcWork.Left - mi.RcMonitor.Left
	info.MaxPosition.Y = mi.RcWork.Top - mi.RcMonitor.Top
	info.MaxSize.X = mi.RcWork.Right - mi.RcWork.Left
	info.MaxSize.Y = mi.RcWork.Bottom - mi.RcWork.Top
	info.MaxTrackSize = info.MaxSize
}

// ─── 标题栏布局与绘制 ───────────────────────────────────────────────────────

type panelTitleBarLayout struct {
	favicon  panelRECT
	nav      [4]panelRECT // back / forward / reload / stop（下标 = tbBtnBack..tbBtnStop）
	pin      panelRECT    // 置顶开关（用户要求：不进管理面板就能直接切）
	settings panelRECT
	minBtn   panelRECT
	maxBtn   panelRECT
	closeBtn panelRECT
	address  panelRECT
}

// panelTitleBarLayoutFor 计算给定宽度下的标题栏布局（绘制与命中测试共用同一份几何）。
func panelTitleBarLayoutFor(width int32) panelTitleBarLayout {
	var l panelTitleBarLayout
	iconTop := int32((titleBarHeight - titleBarIconSize) / 2)
	l.favicon = panelRECT{Left: 10, Top: iconTop, Right: 10 + titleBarIconSize, Bottom: iconTop + titleBarIconSize}

	navTop := int32((titleBarHeight - 26) / 2)
	left := int32(40)
	for i := range l.nav {
		l.nav[i] = panelRECT{Left: left, Top: navTop, Right: left + 32, Bottom: navTop + 26}
		left += 32
	}

	// 右侧窗口按钮：贴右缘、全高，手感与系统标题栏一致。
	l.closeBtn = panelRECT{Left: width - 46, Top: 0, Right: width, Bottom: titleBarHeight}
	l.maxBtn = panelRECT{Left: width - 92, Top: 0, Right: width - 46, Bottom: titleBarHeight}
	l.minBtn = panelRECT{Left: width - 138, Top: 0, Right: width - 92, Bottom: titleBarHeight}

	// 设置（打开管理面板）：窗口按钮左侧。
	l.settings = panelRECT{Left: width - 138 - 12 - 34, Top: navTop, Right: width - 138 - 12, Bottom: navTop + 26}
	// 置顶开关：设置左侧，留 6px 间距（两个图标挨着会被看成一个组，点错概率高）。
	l.pin = panelRECT{Left: l.settings.Left - 6 - 34, Top: navTop, Right: l.settings.Left - 6, Bottom: navTop + 26}

	// 地址栏占满中间剩余空间（加了置顶按钮，地址栏相应变窄 —— 这是用户认可的取舍）。
	addrLeft := left + 12
	addrRight := l.pin.Left - 10
	if addrRight < addrLeft {
		addrRight = addrLeft
	}
	l.address = panelRECT{Left: addrLeft, Top: navTop, Right: addrRight, Bottom: navTop + 26}
	return l
}

func rectContains(r panelRECT, x, y int32) bool {
	return x >= r.Left && x < r.Right && y >= r.Top && y < r.Bottom
}

func (l *panelTitleBarLayout) buttonAt(x, y int32) int {
	for i := range l.nav {
		if rectContains(l.nav[i], x, y) {
			return i
		}
	}
	switch {
	case rectContains(l.pin, x, y):
		return tbBtnPin
	case rectContains(l.settings, x, y):
		return tbBtnSettings
	case rectContains(l.minBtn, x, y):
		return tbBtnMin
	case rectContains(l.maxBtn, x, y):
		return tbBtnMax
	case rectContains(l.closeBtn, x, y):
		return tbBtnClose
	}
	return tbBtnNone
}

func (l *panelTitleBarLayout) buttonRect(id int) panelRECT {
	switch {
	case id >= tbBtnBack && id <= tbBtnStop:
		return l.nav[id]
	case id == tbBtnPin:
		return l.pin
	case id == tbBtnSettings:
		return l.settings
	case id == tbBtnMin:
		return l.minBtn
	case id == tbBtnMax:
		return l.maxBtn
	case id == tbBtnClose:
		return l.closeBtn
	}
	return panelRECT{}
}

func panelTitleBarInitGDI() {
	panelTitleBarGDIOnce.Do(func() {
		// COLORREF 布局为 0x00BBGGRR。
		panelTbBrushCloseHover, _, _ = panelCreateSolidBrush.Call(0x002311E8) // #e81123

		panelTbIconFont = createPanelFont("Segoe MDL2 Assets", -13, 400)
		if panelTbIconFont == 0 {
			panelTbIconFont, _, _ = panelGetStockObject.Call(17) // DEFAULT_GUI_FONT
		}
		panelAddressFont = createPanelFont("Microsoft YaHei UI", -13, 400)
		if panelAddressFont == 0 {
			panelAddressFont, _, _ = panelGetStockObject.Call(17)
		}
	})
}

func createPanelFont(face string, height int32, weight uintptr) uintptr {
	f, err := windows.UTF16PtrFromString(face)
	if err != nil {
		return 0
	}
	font, _, _ := panelCreateFontW.Call(
		uintptr(height), 0, 0, 0,
		weight,
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0,
		5, // CLEARTYPE_QUALITY
		0,
		uintptr(unsafe.Pointer(f)),
	)
	return font
}

// paintTitleBar 绘制整个标题栏（favicon、按钮、悬停/按下态；地址栏是 EDIT 子窗口自绘）。
func (p *panelWindow) paintTitleBar(hwnd uintptr) {
	panelTabBarInitGDI()
	panelTitleBarInitGDI()

	hdc, _, _ := panelGetDC.Call(hwnd)
	if hdc == 0 {
		return
	}
	defer panelReleaseDC.Call(hwnd, hdc)

	var client panelRECT
	panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	width := client.Right - client.Left

	p.mu.Lock()
	hover := p.hoverTitleBtn
	press := p.pressTitleBtn
	alwaysOnTop := p.alwaysOnTop
	var iconDIB uintptr
	canBack, canFwd, loading, hasTab := false, false, false, false
	for i := range p.tabs {
		if p.tabs[i].tabID == p.activeTabID {
			iconDIB = p.tabs[i].iconTitleDIB
			canBack = p.tabs[i].canGoBack
			canFwd = p.tabs[i].canGoForward
			loading = p.tabs[i].loading
			hasTab = true
		}
	}
	parent := p.hwnd
	p.mu.Unlock()
	chrome := p.chrome()

	bg := panelRECT{Right: width, Bottom: client.Bottom}
	panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&bg)), chrome.brBg)

	layout := panelTitleBarLayoutFor(width)

	// 站点图标：拿到位图就 AlphaBlend，否则画默认地球字形。
	if iconDIB != 0 {
		if memdc, _, _ := panelCreateCompatDC.Call(hdc); memdc != 0 {
			old, _, _ := panelSelectObject.Call(memdc, iconDIB)
			panelAlphaBlend.Call(
				hdc,
				uintptr(layout.favicon.Left), uintptr(layout.favicon.Top),
				uintptr(titleBarIconSize), uintptr(titleBarIconSize),
				memdc, 0, 0, uintptr(titleBarIconSize), uintptr(titleBarIconSize),
				blendSrcAlpha,
			)
			panelSelectObject.Call(memdc, old)
			panelDeleteDC.Call(memdc)
		}
	} else {
		paintTitleGlyph(hdc, layout.favicon, tbGlyphGlobe, uintptr(panelColorRef(chrome.text)))
	}

	// 导航按钮（后退/前进/刷新/停止）。
	navGlyphs := []string{tbGlyphBack, tbGlyphForward, tbGlyphReload, tbGlyphStop}
	navEnabled := []bool{canBack, canFwd, hasTab, loading && hasTab}
	for i := range layout.nav {
		paintTitleButton(hdc, layout.nav[i], navGlyphs[i], i, navEnabled[i], hover, press, false, chrome)
	}
	// 置顶开关：图标形状直接表达状态 —— 空心图钉=未置顶，实心图钉=已置顶，
	// 同时给已置顶加高亮色（只靠颜色区分对色觉障碍不友好，形状才是主信号）。
	pinGlyph, pinColor := tbGlyphPin, uint32(0)
	if alwaysOnTop {
		pinGlyph, pinColor = tbGlyphPinned, chrome.accent
	}
	paintTitleButtonWithColor(hdc, layout.pin, pinGlyph, tbBtnPin, true, hover, press, false, pinColor, chrome)
	// 设置（打开管理面板）。
	paintTitleButton(hdc, layout.settings, tbGlyphSettings, tbBtnSettings, true, hover, press, false, chrome)
	// 窗口按钮。
	paintTitleButton(hdc, layout.minBtn, tbGlyphMin, tbBtnMin, true, hover, press, false, chrome)
	maxGlyph := tbGlyphMax
	if zoomed, _, _ := panelIsZoomed.Call(parent); zoomed != 0 {
		maxGlyph = tbGlyphRestore
	}
	paintTitleButton(hdc, layout.maxBtn, maxGlyph, tbBtnMax, true, hover, press, false, chrome)
	paintTitleButton(hdc, layout.closeBtn, tbGlyphClose, tbBtnClose, true, hover, press, true, chrome)

	panelValidateRect.Call(hwnd, 0)
}

// paintChromeIconButton 画一个外壳图标按钮：悬停/按下底色 + 居中字形。
//
// 标题栏（导航 / 置顶 / 设置 / 窗口按钮）与标签栏（右端配色切换）共用，
// 两处的按钮观感本来就该一致。
func paintChromeIconButton(hdc uintptr, rect panelRECT, glyph string, chrome *panelChrome, hovered, pressed bool) {
	paintChromeIconButtonWithColor(hdc, rect, glyph, chrome, hovered, pressed, panelColorRef(chrome.text))
}

// paintChromeIconButtonWithColor 同上，只是前景色可覆盖（置顶开关、置灰按钮用）。
func paintChromeIconButtonWithColor(hdc uintptr, rect panelRECT, glyph string, chrome *panelChrome, hovered, pressed bool, color uint32) {
	switch {
	case pressed:
		panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), chrome.brPressed)
	case hovered:
		panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), chrome.brHover)
	}
	paintTitleGlyph(hdc, rect, glyph, uintptr(color))
}

// paintTitleButton 画一个图标按钮（悬停底色、按下底色、禁用置灰、关闭按钮悬停红）。
func paintTitleButton(hdc uintptr, rect panelRECT, glyph string, id int, enabled bool, hover, press int, isClose bool, chrome *panelChrome) {
	paintTitleButtonWithColor(hdc, rect, glyph, id, enabled, hover, press, isClose, 0, chrome)
}

// paintTitleButtonWithColor 与 paintTitleButton 完全相同，只是 overrideColor 非 0 时用它作前景色。
// 给置顶开关用：那个按钮的前景色随开关状态变化，其余按钮都是固定色。
func paintTitleButtonWithColor(hdc uintptr, rect panelRECT, glyph string, id int, enabled bool, hover, press int, isClose bool, overrideColor uint32, chrome *panelChrome) {
	// 关闭按钮悬停是系统惯例的红底白字，两个主题同值，不进配色表。
	if press != id && hover == id && isClose {
		panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), panelTbBrushCloseHover)
		paintTitleGlyph(hdc, rect, glyph, 0x00FFFFFF)
		return
	}

	color := chrome.text
	if !enabled {
		color = chrome.textDisabled
	} else if overrideColor != 0 {
		color = overrideColor
	}
	paintChromeIconButtonWithColor(hdc, rect, glyph, chrome, hover == id, press == id, panelColorRef(color))
}

func paintTitleGlyph(hdc uintptr, rect panelRECT, glyph string, color uintptr) {
	g, err := windows.UTF16PtrFromString(glyph)
	if err != nil {
		return
	}
	panelSetBkMode.Call(hdc, 1) // TRANSPARENT
	panelSetTextColor.Call(hdc, color)
	old, _, _ := panelSelectObject.Call(hdc, panelTbIconFont)
	panelDrawTextW.Call(
		hdc,
		uintptr(unsafe.Pointer(g)),
		^uintptr(0), // -1
		uintptr(unsafe.Pointer(&rect)),
		win32DTCenter|win32DTVCenter|win32DTSingleLine,
	)
	panelSelectObject.Call(hdc, old)
}

// ─── 标题栏子窗口 ───────────────────────────────────────────────────────────

func registerPanelTitleBarClass() error {
	panelTitleBarClassOnce.Do(func() {
		instance, err := panelModuleInstance()
		if err != nil {
			panelTitleBarClassError = err
			return
		}

		panelTabBarInitGDI()
		panelTitleBarInitGDI()
		cursor, _, _ := panelLoadCursor.Call(0, win32IDCArrow)
		windowClass := panelWNDCLASSEX{
			Size:       uint32(unsafe.Sizeof(panelWNDCLASSEX{})),
			WndProc:    panelTitleBarProcedure,
			Instance:   instance,
			Cursor:     cursor,
			Background: panelDefaultChrome().brBg,
			ClassName:  panelTitleBarClassName,
		}
		atom, _, registerErr := panelRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
		if atom == 0 && registerErr != syscall.Errno(1410) {
			panelTitleBarClassError = fmt.Errorf("register title bar window class: %w", registerErr)
		}
	})
	return panelTitleBarClassError
}

func createPanelTitleBar(parent uintptr) (uintptr, error) {
	instance, err := panelModuleInstance()
	if err != nil {
		return 0, err
	}

	var client panelRECT
	panelGetClientRect.Call(parent, uintptr(unsafe.Pointer(&client)))

	hwnd, _, createErr := panelCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(panelTitleBarClassName)),
		0,
		// WS_CLIPCHILDREN 不能少：地址栏 EDIT 是本窗口的子窗口，
		// 没它的话标题栏每次重绘都会把 EDIT 整个盖掉（实测地址栏整条消失）。
		win32WSChild|win32WSVisible|win32WSClipChildren,
		win32FrameBorder, // 初值，精确内缩摆位由 panelWindow.resize 完成
		0,
		uintptr(client.Right-client.Left-2*win32FrameBorder),
		titleBarHeight,
		parent,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create panel title bar: %w", createErr)
	}
	return hwnd, nil
}

func panelTitleBarProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	panel := getTitleBarWindow(hwnd)
	if panel == nil {
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	}

	switch message {
	case win32WMPaint:
		panel.paintTitleBar(hwnd)
		return 0
	case win32WMNCHitTest:
		return panel.titleBarHitTest(hwnd, lParam)
	case win32WMMouseMove:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16((lParam >> 16) & 0xFFFF))
		panel.updateTitleButtonHover(hwnd, x, y)
		return 0
	case win32WMMouseLeave:
		panel.mu.Lock()
		panel.hoverTitleBtn = -1
		panel.titleTracking = false
		panel.mu.Unlock()
		panelInvalidateRect.Call(hwnd, 0, 1)
		return 0
	case win32WMLButtonDown:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16((lParam >> 16) & 0xFFFF))
		panel.pressTitleBarButton(hwnd, x, y)
		return 0
	case win32WMLButtonUp:
		x := int32(int16(lParam & 0xFFFF))
		y := int32(int16((lParam >> 16) & 0xFFFF))
		panel.releaseTitleBarButton(hwnd, x, y)
		return 0
	case win32WMCtlColorEdit:
		// 地址栏 EDIT 的配色（深浅两套跟着主题走）。
		chrome := panel.chrome()
		panelSetTextColor.Call(wParam, uintptr(panelColorRef(chrome.fieldText)))
		panelSetBkColor.Call(wParam, uintptr(panelColorRef(chrome.field)))
		return chrome.brField
	case win32WMEraseBkgnd:
		// 窗口类背景刷是注册时定死的，跟不上换主题；自己擦，免得浅色主题下闪深色底。
		panel.eraseBackground(hwnd, wParam)
		return 1
	case win32WMSIZE:
		panel.layoutAddressEdit(hwnd)
	case win32WMTitleBarRefresh:
		panelInvalidateRect.Call(hwnd, 0, 1)
		return 0
	}
	result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

// titleBarHitTest 标题栏的命中分工：
//   - 按钮区 → HTCLIENT（自己收点击）；
//   - 顶部 win32FrameTopStrip → 穿透（父窗口判上边框缩放）；
//   - 其余空白 → 穿透（父窗口判 HTCAPTION：拖拽移动/双击最大化/右键系统菜单）。
//
// 地址栏 EDIT 是独立子窗口，命中不到这里。
func (p *panelWindow) titleBarHitTest(hwnd uintptr, lParam uintptr) uintptr {
	var rect panelRECT
	if ok, _, _ := panelGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return win32HTClient
	}
	x := int32(int16(lParam&0xFFFF)) - rect.Left
	y := int32(int16((lParam>>16)&0xFFFF)) - rect.Top
	if y < win32FrameTopStrip {
		return win32HTTransparent
	}
	layout := panelTitleBarLayoutFor(rect.Right - rect.Left)
	if layout.buttonAt(x, y) != tbBtnNone {
		return win32HTClient
	}
	return win32HTTransparent
}

// titleButtonEnabled 报告按钮当前是否可用（后退/前进看历史栈，停止看是否正在加载）。
func (p *panelWindow) titleButtonEnabled(id int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.tabs {
		if p.tabs[i].tabID != p.activeTabID {
			continue
		}
		switch id {
		case tbBtnBack:
			return p.tabs[i].canGoBack
		case tbBtnForward:
			return p.tabs[i].canGoForward
		case tbBtnReload:
			return true
		case tbBtnStop:
			return p.tabs[i].loading
		}
		return true
	}
	return id >= tbBtnSettings // 导航按钮在没有活动标签时不可用；设置/窗口按钮永远可用
}

func (p *panelWindow) invalidateTitleButton(hwnd uintptr, layout *panelTitleBarLayout, id int) {
	if id == tbBtnNone {
		return
	}
	r := layout.buttonRect(id)
	panelInvalidateRect.Call(hwnd, uintptr(unsafe.Pointer(&r)), 1)
}

func (p *panelWindow) updateTitleButtonHover(hwnd uintptr, x, y int32) {
	var client panelRECT
	panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	layout := panelTitleBarLayoutFor(client.Right - client.Left)

	id := layout.buttonAt(x, y)
	if id != tbBtnNone && !p.titleButtonEnabled(id) {
		id = tbBtnNone
	}

	p.mu.Lock()
	changed := id != p.hoverTitleBtn
	needTrack := !p.titleTracking
	p.titleTracking = true
	old := p.hoverTitleBtn
	p.hoverTitleBtn = id
	p.mu.Unlock()

	if needTrack {
		var tme panelTRACKMOUSEEVENT
		tme.CbSize = uint32(unsafe.Sizeof(tme))
		tme.DwFlags = win32TMELeave
		tme.HwndTrack = hwnd
		panelTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
	}
	if changed {
		p.invalidateTitleButton(hwnd, &layout, old)
		p.invalidateTitleButton(hwnd, &layout, id)
	}
}

func (p *panelWindow) pressTitleBarButton(hwnd uintptr, x, y int32) {
	var client panelRECT
	panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	layout := panelTitleBarLayoutFor(client.Right - client.Left)

	id := layout.buttonAt(x, y)
	if id == tbBtnNone || !p.titleButtonEnabled(id) {
		return
	}
	p.mu.Lock()
	p.pressTitleBtn = id
	p.mu.Unlock()
	panelSetCapture.Call(hwnd)
	p.invalidateTitleButton(hwnd, &layout, id)
}

func (p *panelWindow) releaseTitleBarButton(hwnd uintptr, x, y int32) {
	p.mu.Lock()
	id := p.pressTitleBtn
	p.pressTitleBtn = -1
	p.mu.Unlock()
	if id == tbBtnNone {
		return
	}
	panelReleaseCapture.Call()

	var client panelRECT
	panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))
	layout := panelTitleBarLayoutFor(client.Right - client.Left)
	p.invalidateTitleButton(hwnd, &layout, id)

	if layout.buttonAt(x, y) == id && p.titleButtonEnabled(id) {
		p.activateTitleBarButton(id)
	}
}

// activateTitleBarButton 执行按钮动作。
// 关闭按钮 = 给面板窗口发 WM_CLOSE（走交互式关闭，按设置询问），不是 p.close()！
func (p *panelWindow) activateTitleBarButton(id int) {
	if id == tbBtnPin {
		// 置顶开关：切当前状态。写配置与广播都在 setAlwaysOnTop 里 ——
		// 托盘菜单、管理面板走的是同一个方法，这里不要自己再抄一遍持久化。
		p.setAlwaysOnTop(!p.isAlwaysOnTop())
		return
	}
	if id == tbBtnSettings {
		if p.app != nil {
			p.app.ShowMainWindow()
		}
		return
	}

	p.mu.Lock()
	hwnd := p.hwnd
	var wv *panelWebView
	for i := range p.tabs {
		if p.tabs[i].tabID == p.activeTabID {
			wv = p.tabs[i].webview
		}
	}
	p.mu.Unlock()

	switch id {
	case tbBtnMin:
		if hwnd != 0 {
			panelShowWindow.Call(hwnd, win32SWMinimize)
		}
	case tbBtnMax:
		if hwnd != 0 {
			if zoomed, _, _ := panelIsZoomed.Call(hwnd); zoomed != 0 {
				panelShowWindow.Call(hwnd, win32SWRestore)
			} else {
				panelShowWindow.Call(hwnd, win32SWMaximize)
			}
		}
	case tbBtnClose:
		if hwnd != 0 {
			panelPostMessage.Call(hwnd, win32WMCLOSE, 0, 0)
		}
	case tbBtnBack:
		if wv != nil {
			wv.goBack()
		}
	case tbBtnForward:
		if wv != nil {
			wv.goForward()
		}
	case tbBtnReload:
		if wv != nil {
			wv.reload()
		}
	case tbBtnStop:
		if wv != nil {
			wv.stop()
		}
	}
}

// syncTitleBarFromTab 把标题栏（地址栏文本 + 按钮区）同步到指定标签的状态。
// 用户正在地址栏里输入时不覆盖文本。
func (p *panelWindow) syncTitleBarFromTab(tabID string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	titleHwnd := p.titleBarHwnd
	editHwnd := p.addressHwnd
	url := ""
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			url = p.tabs[i].currentURL
		}
	}
	p.mu.Unlock()

	if editHwnd != 0 && url != "" {
		if focus, _, _ := panelGetFocus.Call(); focus != editHwnd {
			setPanelWindowText(editHwnd, url)
		}
	}
	if titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
}

// layoutAddressEdit 标题栏尺寸变化后重摆地址栏 EDIT。
func (p *panelWindow) layoutAddressEdit(titleHwnd uintptr) {
	p.mu.Lock()
	editHwnd := p.addressHwnd
	p.mu.Unlock()
	if editHwnd == 0 {
		return
	}

	var client panelRECT
	panelGetClientRect.Call(titleHwnd, uintptr(unsafe.Pointer(&client)))
	layout := panelTitleBarLayoutFor(client.Right - client.Left)
	panelMoveWindow.Call(
		editHwnd,
		uintptr(layout.address.Left),
		uintptr(layout.address.Top),
		uintptr(layout.address.Right-layout.address.Left),
		uintptr(layout.address.Bottom-layout.address.Top),
		1,
	)
}

func addTitleBarWindow(hwnd uintptr, panel *panelWindow) {
	activePanelWindowsMutex.Lock()
	activeTitleBarWindows[hwnd] = panel
	activePanelWindowsMutex.Unlock()
}

func getTitleBarWindow(hwnd uintptr) *panelWindow {
	activePanelWindowsMutex.RLock()
	panel := activeTitleBarWindows[hwnd]
	activePanelWindowsMutex.RUnlock()
	return panel
}

func removeTitleBarWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	activePanelWindowsMutex.Lock()
	delete(activeTitleBarWindows, hwnd)
	activePanelWindowsMutex.Unlock()
}

// ─── 地址栏（EDIT 子窗口 + 子类化） ─────────────────────────────────────────

type panelAddressEdit struct {
	panel   *panelWindow
	oldProc uintptr
}

func createPanelAddressEdit(parent uintptr, p *panelWindow) (uintptr, error) {
	instance, err := panelModuleInstance()
	if err != nil {
		return 0, err
	}

	var client panelRECT
	panelGetClientRect.Call(parent, uintptr(unsafe.Pointer(&client)))
	layout := panelTitleBarLayoutFor(client.Right - client.Left)

	hwnd, _, createErr := panelCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(panelEditClassName)),
		0,
		win32WSChild|win32WSVisible|win32ESAutoHScroll,
		uintptr(layout.address.Left),
		uintptr(layout.address.Top),
		uintptr(layout.address.Right-layout.address.Left),
		uintptr(layout.address.Bottom-layout.address.Top),
		parent,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create panel address edit: %w", createErr)
	}

	panelSendMessage.Call(hwnd, win32WMSetFont, panelAddressFont, 1)
	// EM_SETMARGINS：左右各留 8px，文字不贴边。
	panelSendMessage.Call(hwnd, win32EMSetMargins, 3, 8|8<<16)

	old, _, _ := panelSetWindowLongPtr.Call(hwnd, win32GWLWndProc, panelAddressEditProcCb)
	if old == 0 {
		panelDestroyWindow.Call(hwnd)
		return 0, fmt.Errorf("subclass panel address edit failed")
	}
	addAddressEdit(hwnd, &panelAddressEdit{panel: p, oldProc: old})

	// 初始文本 = 活动标签 URL。
	p.mu.Lock()
	url := ""
	for i := range p.tabs {
		if p.tabs[i].tabID == p.activeTabID {
			url = p.tabs[i].currentURL
		}
	}
	p.mu.Unlock()
	if url != "" {
		setPanelWindowText(hwnd, url)
	}
	return hwnd, nil
}

// panelAddressEditWndProc 地址栏子类化过程：回车导航、Esc 还原、回车不蜂鸣。
func panelAddressEditWndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	ed := getAddressEdit(hwnd)
	if ed == nil {
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	}

	switch message {
	case win32WMChar:
		if wParam == win32VKReturn {
			return 0 // 吞掉回车字符，避免系统蜂鸣
		}
	case win32WMKeyDown:
		switch wParam {
		case win32VKReturn:
			ed.panel.navigateFromAddressBar(hwnd)
			return 0
		case win32VKEscape:
			ed.panel.resetAddressBar(hwnd)
			return 0
		}
	case win32WMNCDestroy:
		removeAddressEdit(hwnd)
	}
	result, _, _ := panelCallWindowProc.Call(ed.oldProc, hwnd, uintptr(message), wParam, lParam)
	return result
}

// navigateFromAddressBar 地址栏回车：导航活动标签到输入的地址（无协议头补 http://）。
func (p *panelWindow) navigateFromAddressBar(editHwnd uintptr) {
	url := strings.TrimSpace(panelWindowTextString(editHwnd))
	if url == "" {
		p.resetAddressBar(editHwnd)
		return
	}
	if !strings.Contains(url, "://") {
		url = "http://" + url
	}

	p.mu.Lock()
	var wv *panelWebView
	for i := range p.tabs {
		if p.tabs[i].tabID == p.activeTabID {
			wv = p.tabs[i].webview
			p.tabs[i].currentURL = url
		}
	}
	p.mu.Unlock()
	if wv == nil {
		return
	}
	if err := wv.navigate(url); err != nil {
		p.resetAddressBar(editHwnd)
		return
	}
	// 焦点还给窗口（键盘回到页面）。
	if hwnd := p.windowHandle(); hwnd != 0 {
		panelSetFocus.Call(hwnd)
	}
}

// resetAddressBar 把地址栏文本还原为活动标签的当前 URL。
func (p *panelWindow) resetAddressBar(editHwnd uintptr) {
	p.mu.Lock()
	url := ""
	for i := range p.tabs {
		if p.tabs[i].tabID == p.activeTabID {
			url = p.tabs[i].currentURL
		}
	}
	p.mu.Unlock()
	setPanelWindowText(editHwnd, url)
}

func panelWindowTextString(hwnd uintptr) string {
	n, _, _ := panelGetWindowTextLen.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	copied, _, _ := panelGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	if copied == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:copied])
}

func setPanelWindowText(hwnd uintptr, text string) {
	t, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	panelSetWindowText.Call(hwnd, uintptr(unsafe.Pointer(t)))
}

func addAddressEdit(hwnd uintptr, ed *panelAddressEdit) {
	activePanelWindowsMutex.Lock()
	activeAddressEdits[hwnd] = ed
	activePanelWindowsMutex.Unlock()
}

func getAddressEdit(hwnd uintptr) *panelAddressEdit {
	activePanelWindowsMutex.RLock()
	ed := activeAddressEdits[hwnd]
	activePanelWindowsMutex.RUnlock()
	return ed
}

func removeAddressEdit(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	activePanelWindowsMutex.Lock()
	delete(activeAddressEdits, hwnd)
	activePanelWindowsMutex.Unlock()
}

// ─── WebView2 导航方法与事件 ────────────────────────────────────────────────

type panelEventRegistrationToken struct {
	Value int64
}

func (w *panelWebView) source() string {
	var uri *uint16
	hr, _, _ := w.Vtbl.GetSource.Call(
		uintptr(unsafe.Pointer(w)),
		uintptr(unsafe.Pointer(&uri)),
	)
	if int32(hr) < 0 || uri == nil {
		return ""
	}
	defer panelCoTaskMemFree.Call(uintptr(unsafe.Pointer(uri)))
	return windows.UTF16PtrToString(uri)
}

func (w *panelWebView) canGoBack() bool {
	var can int32
	hr, _, _ := w.Vtbl.get_CanGoBack.Call(uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(&can)))
	return int32(hr) >= 0 && can != 0
}

func (w *panelWebView) canGoForward() bool {
	var can int32
	hr, _, _ := w.Vtbl.get_CanGoForward.Call(uintptr(unsafe.Pointer(w)), uintptr(unsafe.Pointer(&can)))
	return int32(hr) >= 0 && can != 0
}

func (w *panelWebView) goBack() {
	w.Vtbl.GoBack.Call(uintptr(unsafe.Pointer(w)))
}

func (w *panelWebView) goForward() {
	w.Vtbl.GoForward.Call(uintptr(unsafe.Pointer(w)))
}

func (w *panelWebView) reload() {
	w.Vtbl.Reload.Call(uintptr(unsafe.Pointer(w)))
}

func (w *panelWebView) stop() {
	w.Vtbl.Stop.Call(uintptr(unsafe.Pointer(w)))
}

func (w *panelWebView) addEvent(add panelCOMProc, handler *panelWebEventHandler) {
	var token panelEventRegistrationToken
	add.Call(
		uintptr(unsafe.Pointer(w)),
		uintptr(unsafe.Pointer(handler)),
		uintptr(unsafe.Pointer(&token)),
	)
}

// WebView2 事件种类（一个 handler 类型打天下，Invoke 签名都长一样）。
const (
	panelEvNavStarting = iota
	panelEvNavCompleted
	panelEvSourceChanged
	panelEvHistoryChanged
)

type panelWebEventHandlerVtbl struct {
	panelIUnknownVtbl
	Invoke panelCOMProc
}

type panelWebEventHandler struct {
	Vtbl  *panelWebEventHandlerVtbl
	panel *panelWindow
	tabID string
	kind  int
}

var panelWebEventHandlerVTable = &panelWebEventHandlerVtbl{
	panelIUnknownVtbl: panelIUnknownVtbl{
		QueryInterface: panelCOMProc(windows.NewCallback(panelQueryInterfaceIUnknown)),
		AddRef:         panelCOMProc(windows.NewCallback(panelWebEventHandlerAddRef)),
		Release:        panelCOMProc(windows.NewCallback(panelWebEventHandlerRelease)),
	},
	Invoke: panelCOMProc(windows.NewCallback(panelWebEventHandlerInvoke)),
}

func panelWebEventHandlerAddRef(uintptr) uintptr  { return 1 }
func panelWebEventHandlerRelease(uintptr) uintptr { return 1 }

// panelWebEventHandlerInvoke 事件统一入口（WebView2 在创建 controller 的 UI 线程上回调）。
func panelWebEventHandlerInvoke(h *panelWebEventHandler, sender *panelWebView, args uintptr) uintptr {
	switch h.kind {
	case panelEvNavStarting:
		h.panel.onTabNavigationStarting(h.tabID)
	case panelEvNavCompleted:
		h.panel.onTabNavigationCompleted(h.tabID, sender)
	case panelEvSourceChanged:
		h.panel.onTabSourceChanged(h.tabID, sender)
	case panelEvHistoryChanged:
		h.panel.onTabHistoryChanged(h.tabID, sender)
	}
	return 0
}

// registerTabEvents 给标签的 WebView2 挂上导航事件。
// handler 必须先存进 tabState 再注册：COM 侧的引用 Go GC 看不见，不存就可能被回收。
func (p *panelWindow) registerTabEvents(tabID string, w *panelWebView) {
	mk := func(kind int) *panelWebEventHandler {
		return &panelWebEventHandler{Vtbl: panelWebEventHandlerVTable, panel: p, tabID: tabID, kind: kind}
	}
	starting := mk(panelEvNavStarting)
	completed := mk(panelEvNavCompleted)
	source := mk(panelEvSourceChanged)
	history := mk(panelEvHistoryChanged)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			p.tabs[i].eventHandlers = append(p.tabs[i].eventHandlers, starting, completed, source, history)
		}
	}
	p.mu.Unlock()

	w.addEvent(w.Vtbl.add_NavigationStarting, starting)
	w.addEvent(w.Vtbl.add_NavigationCompleted, completed)
	w.addEvent(w.Vtbl.add_SourceChanged, source)
	w.addEvent(w.Vtbl.add_HistoryChanged, history)
}

func (p *panelWindow) onTabNavigationStarting(tabID string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			p.tabs[i].loading = true
		}
	}
	active := p.activeTabID == tabID
	titleHwnd := p.titleBarHwnd
	p.mu.Unlock()

	if active && titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
}

func (p *panelWindow) onTabNavigationCompleted(tabID string, sender *panelWebView) {
	url := ""
	canBack, canFwd := false, false
	if sender != nil {
		url = sender.source()
		canBack = sender.canGoBack()
		canFwd = sender.canGoForward()
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			p.tabs[i].loading = false
			if url != "" {
				p.tabs[i].currentURL = url
			}
			p.tabs[i].canGoBack = canBack
			p.tabs[i].canGoForward = canFwd
		}
	}
	active := p.activeTabID == tabID
	p.mu.Unlock()

	if active {
		p.syncTitleBarFromTab(tabID)
	}
	if sender != nil {
		p.requestIcons(tabID, sender, url)
	}
}

func (p *panelWindow) onTabSourceChanged(tabID string, sender *panelWebView) {
	if sender == nil {
		return
	}
	url := sender.source()
	if url == "" {
		return
	}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			p.tabs[i].currentURL = url
		}
	}
	active := p.activeTabID == tabID
	p.mu.Unlock()

	if active {
		p.syncTitleBarFromTab(tabID)
	}
}

func (p *panelWindow) onTabHistoryChanged(tabID string, sender *panelWebView) {
	if sender == nil {
		return
	}
	canBack := sender.canGoBack()
	canFwd := sender.canGoForward()

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			p.tabs[i].canGoBack = canBack
			p.tabs[i].canGoForward = canFwd
		}
	}
	active := p.activeTabID == tabID
	titleHwnd := p.titleBarHwnd
	p.mu.Unlock()

	if active && titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
}
