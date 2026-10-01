//go:build windows

package main

// 面板窗口的明暗配色。
//
// 面板窗口的外壳（自绘标题栏 + 原生标签栏 + 缩放边框让出来的那一圈）全部由 GDI
// 绘制，用不上管理窗口那套 CSS token（frontend/src/style.css），所以这里给一份
// 语义**同名**的颜色表，明暗各一张 —— 底色 / 悬停 / 按下 / 活动 / 输入框 / 强调色，
// 两边对齐才不会出现「管理窗口偏蓝、面板窗口偏灰」。
//
// 作用范围要如实：**只改外壳**。面板里显示的是别人的页面，本程序不向远程页面注入
// 任何样式或脚本（项目的硬不变量），所以页面自身的深浅由站点决定，我们不碰。
// 能跟着主题走的只有两块：外壳的 GDI 颜色，以及 WebView2 的**默认底色**
//（页面自己没铺底的区域、以及首帧之前的空白都吃它，深色下不再白闪）。

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// panelChrome 是一套主题下外壳用到的全部颜色与画刷。
//
// 颜色一律写 0xRRGGBB（人类可读），要 COLORREF 时过 panelColorRef —— 别再写成
// 「0x00FAA560 // #60a5fa」那种必须靠注释才能读懂的常量，那是这套代码里最容易
// 抄错的地方（BGR 与 RGB 写反了编译、vet、单测全绿，只有肉眼看得出来）。
type panelChrome struct {
	bg            uint32 // 标题栏 / 标签栏底色，也是缩放边框那一圈
	text          uint32 // 图标按钮与普通标签的文字
	textDisabled  uint32 // 禁用按钮的文字
	hover         uint32 // 悬停底色（按钮、标签共用）
	pressed       uint32 // 按下底色
	activeTab     uint32 // 活动标签的底色
	activeTabText uint32 // 活动标签的文字
	field         uint32 // 地址栏底色
	fieldText     uint32 // 地址栏文字
	accent        uint32 // 「已置顶」图钉这类状态强调色

	// 画刷（GDI 对象，进程级共享、创建后不释放）。只给真当底色用的颜色建，
	// 前景色都是 SetTextColor 直接给值，不需要画刷。
	brBg        uintptr
	brHover     uintptr
	brPressed   uintptr
	brActiveTab uintptr
	brField     uintptr
}

var (
	panelChromeInitOnce sync.Once
	panelChromeLight    panelChrome
	panelChromeDark     panelChrome
)

// panelChromeInit 建好两套配色与画刷（进程级一次）。
//
// 明暗**两套都建**，不做「按需创建 + 缓存」：一次建完，之后切主题只是换指针，
// 不会在重绘路径上分配 GDI 对象（那是最容易漏释放的地方）。
func panelChromeInit() {
	panelChromeInitOnce.Do(func() {
		// 浅色：与 style.css 的 :root[data-theme='light'] 对齐。
		panelChromeLight = panelChrome{
			bg:            0xF1F5F9,
			text:          0x334155,
			textDisabled:  0x94A3B8,
			hover:         0xE2E8F0,
			pressed:       0xCBD5E1,
			activeTab:     0xFFFFFF,
			activeTabText: 0x0F172A,
			field:         0xFFFFFF,
			fieldText:     0x1F2937,
			accent:        0x2563EB,
		}
		// 深色：沿用面板窗口一直以来的观感（底色 #1e293b、强调 #60a5fa）。
		panelChromeDark = panelChrome{
			bg:            0x1E293B,
			text:          0xCBD5E1,
			textDisabled:  0x64748B,
			hover:         0x334155,
			pressed:       0x475569,
			activeTab:     0xF1F5F9,
			activeTabText: 0x1E293B,
			field:         0x334155,
			fieldText:     0xE2E8F0,
			accent:        0x60A5FA,
		}

		for _, chrome := range []*panelChrome{&panelChromeLight, &panelChromeDark} {
			chrome.brBg = panelSolidBrush(chrome.bg)
			chrome.brHover = panelSolidBrush(chrome.hover)
			chrome.brPressed = panelSolidBrush(chrome.pressed)
			chrome.brActiveTab = panelSolidBrush(chrome.activeTab)
			chrome.brField = panelSolidBrush(chrome.field)
		}
	})
}

// panelChromeFor 取生效主题对应的配色表。只认 light / dark，其余（含空串）按浅色 ——
// 面板窗口拿到的已经是解析过的**生效**主题，不该再出现 auto。
func panelChromeFor(effective string) *panelChrome {
	panelChromeInit()
	if effective == ThemeDark {
		return &panelChromeDark
	}
	return &panelChromeLight
}

// panelDefaultChrome 是窗口类注册时用的兜底配色。
//
// 窗口类的背景刷是注册那一刻定死的，改不了；主题切换靠各窗口自己处理 WM_ERASEBKGND
// 覆盖掉它（见三个窗口过程），所以这里给哪一套都不影响最终观感，取深色是保持
// 「还没收到主题之前」与既有行为一致。
func panelDefaultChrome() *panelChrome {
	return panelChromeFor(ThemeDark)
}

// panelSolidBrush 按 0xRRGGBB 建一把实心画刷。
func panelSolidBrush(rgb uint32) uintptr {
	brush, _, _ := panelCreateSolidBrush.Call(uintptr(panelColorRef(rgb)))
	return brush
}

// panelColorRef 把 0xRRGGBB 转成 COLORREF（0x00BBGGRR）。
func panelColorRef(rgb uint32) uint32 {
	return (rgb&0x0000FF)<<16 | (rgb & 0x00FF00) | (rgb>>16)&0x0000FF
}

// ─── WebView2 的默认底色 ──────────────────────────────────────────────────────

// IID_ICoreWebView2Controller2 = {c979903e-d4ca-4228-92eb-47ee3fa96eab}。
var panelIIDController2 = windows.GUID{
	Data1: 0xC979903E, Data2: 0xD4CA, Data3: 0x4228,
	Data4: [8]byte{0x92, 0xEB, 0x47, 0xEE, 0x3F, 0xA9, 0x6E, 0xAB},
}

// panelController2Vtbl 在 ICoreWebView2Controller 的 vtable 之后续两个方法。
//
// 槽位：IUnknown 0–2；ICoreWebView2Controller 的 23 个方法 3–25（panelControllerVtbl
// 已逐条核对过 github.com/wailsapp/go-webview2 的 ICoreWebView2Controller.go，顺序一致）；
// 于是 GetDefaultBackgroundColor = 26、PutDefaultBackgroundColor = 27。
// **26/27 是这张表的末尾**，多写或少写一个方法就会越界取到野指针，加方法前先核基线。
type panelController2Vtbl struct {
	panelControllerVtbl
	GetDefaultBackgroundColor panelCOMProc // 26
	PutDefaultBackgroundColor panelCOMProc // 27
}

type panelController2 struct {
	Vtbl *panelController2Vtbl
}

// setDefaultBackgroundColor 把 WebView2 的默认底色设为主题底色。
//
// 这个颜色决定两件事：页面自己没铺底的地方，以及页面首帧画出来之前露出的那片空白。
// 深色主题下不设它，每次开面板/切页都会先闪一记白。
//
// 拿不到 ICoreWebView2Controller2（WebView2 运行时太老）时**静默跳过**：这只是观感，
// 不值得为它报错打扰用户。
func (c *panelController) setDefaultBackgroundColor(rgb uint32) {
	if c == nil {
		return
	}

	var controller2 *panelController2
	hr, _, _ := c.Vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&panelIIDController2)),
		uintptr(unsafe.Pointer(&controller2)),
	)
	if hr != panelSOK || controller2 == nil {
		return
	}
	defer panelCOMRelease(controller2)

	// COREWEBVIEW2_COLOR 是 {A, R, G, B} 的 4 字节结构体，**按值**传（小结构体走寄存器），
	// 所以按内存布局压成一个 uintptr，不能传指针。
	value := uintptr(0xFF) |
		uintptr(rgb>>16&0xFF)<<8 | // R
		uintptr(rgb>>8&0xFF)<<16 | // G
		uintptr(rgb&0xFF)<<24 // B
	controller2.Vtbl.PutDefaultBackgroundColor.Call(uintptr(unsafe.Pointer(controller2)), value)
}

// ─── 主题变更的应用 ──────────────────────────────────────────────────────────

// panelTheme 返回本窗口当前生效的配色。窗口还没定过主题时回落到深色（既有观感）。
func (p *panelWindow) panelTheme() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.theme == "" {
		return ThemeDark
	}
	return p.theme
}

// chrome 返回本窗口当前该用的配色表。
func (p *panelWindow) chrome() *panelChrome {
	return panelChromeFor(p.panelTheme())
}

// requestTheme 请求把本窗口切到 effective 配色。
//
// 调用方可能是任意 goroutine（管理面板改了设置、系统深浅色变了），而重绘只能发生在
// 窗口自己的 UI 线程上，所以这里只挂值 + 投消息，真正干活的是 applyPendingTheme。
func (p *panelWindow) requestTheme(effective string) {
	p.mu.Lock()
	if p.closed || p.hwnd == 0 {
		p.mu.Unlock()
		return
	}
	p.pendingTheme = effective
	hwnd := p.hwnd
	p.mu.Unlock()
	panelPostMessage.Call(hwnd, win32WMSetTheme, 0, 0)
}

// applyPendingTheme 在 UI 线程上应用待处理的配色：换外壳颜色 + 重设 WebView2 默认底色 + 全重绘。
//
// 配色没变就直接返回：auto 模式下的 WM_SETTINGCHANGE 会因为别的原因频繁到来
// （不止深浅色会发这条消息），不该每次都重刷三个窗口。
func (p *panelWindow) applyPendingTheme() {
	p.mu.Lock()
	pending := p.pendingTheme
	p.pendingTheme = ""
	changed := pending != "" && pending != p.theme
	if changed {
		p.theme = pending
	}
	hwnd, titleHwnd, barHwnd := p.hwnd, p.titleBarHwnd, p.tabBarHwnd
	controllers := make([]*panelController, 0, len(p.tabs))
	for i := range p.tabs {
		controllers = append(controllers, p.tabs[i].controller)
	}
	p.mu.Unlock()

	if !changed || p.closed {
		return
	}

	// WebView2 默认底色要把**所有**标签都设一遍，不只是当前那个 —— 其余标签随后
	// 被切出来时同样会先露一下底。
	chrome := panelChromeFor(pending)
	for _, controller := range controllers {
		controller.setDefaultBackgroundColor(chrome.bg)
	}

	panelInvalidateRect.Call(hwnd, 0, 1)
	if titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
	if barHwnd != 0 {
		panelInvalidateRect.Call(barHwnd, 0, 1)
	}
}

// panelSettingTheme 返回配置里**设置值**的三态（auto / light / dark）。
func (a *App) panelSettingTheme() string {
	if a == nil || a.config == nil {
		return ThemeAuto
	}
	return a.config.settings().Theme
}

// resolveTheme 把设置值解析成生效的明暗侧（auto 跟随 Windows 深浅色偏好）。
func (a *App) resolveTheme() string {
	return effectiveTheme(a.panelSettingTheme())
}

// panelInitialTheme 是面板窗口构造时的初始配色。app 为 nil（单测里直接搓 panelWindow）
// 时回落到深色，与窗口「还没定过主题」的兜底一致。
func panelInitialTheme(app *App) string {
	if app == nil {
		return ThemeDark
	}
	return app.resolveTheme()
}

// applyThemeToPanels 把生效配色推给所有已打开的面板窗口。
func (a *App) applyThemeToPanels(effective string) {
	for _, panel := range a.panelWindows() {
		panel.requestTheme(effective)
	}
}

// togglePanelTheme 是面板窗口配色按钮的动作：切到另一侧并**固定**下来。
//
// 与 SetTheme 的行为刻意一致（管理窗口右上角那个按钮也是「切到哪边就固定成哪边」，
// 不会停在 auto），但多一步广播：这次是**面板**改的设置，管理界面得知道，
// 否则管理窗口开着时会一直显示旧配色。
//
// from 是面板**当前生效**的明暗侧（不是设置里的三态），所以设置是 auto 时按一次
// 也会落盘成显式的 light / dark —— 这正是「切到哪边就固定成哪边」的字面含义。
func (a *App) togglePanelTheme(from string) error {
	next := ThemeDark
	if from == ThemeDark {
		next = ThemeLight
	}
	if err := a.config.setTheme(next); err != nil {
		return err
	}
	a.syncWindowTheme()
	a.applyThemeToPanels(next)
	a.notifySettingsChanged()
	return nil
}

// isImmersiveColorSet 判断 WM_SETTINGCHANGE 是不是「深浅色偏好变了」——
// lParam 指向具体变了的那项设置名，只有 "ImmersiveColorSet" 与主题有关。
// 其余（区域、字体、辅助功能…）都会发同一条消息，不过滤就会跟着瞎重绘。
func isImmersiveColorSet(lParam uintptr) bool {
	if lParam == 0 {
		return false
	}
	// lParam 是系统给的指针，走 uintptrToPtr 还原（直接 unsafe.Pointer(uintptr)
	// 会被 go vet 的 unsafeptr 检查拦下）。
	return windows.UTF16PtrToString((*uint16)(uintptrToPtr(lParam))) == "ImmersiveColorSet"
}
