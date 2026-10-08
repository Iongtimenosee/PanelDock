//go:build windows

package main

// 手工搭建的「模态询问框」公共设施。
//
// 用途：Win32 的 MessageBoxW 不支持复选框，因此用标准控件（STATIC / BUTTON / 复选框）
// 手工搭建一个模态小窗口 —— 与项目其余部分保持一致：手工声明 Win32 API、零第三方依赖。
// 目前两个询问框共用它：关闭询问（close_prompt_windows.go）与「面板已停用，是否启用」
// （enable_prompt_windows.go）。
//
// 所有询问框**共用同一个窗口类与窗口过程**，靠标题栏文案（spec.Caption）区分是谁；
// 按钮靠控件 ID 分发（见 promptProc）。端到端用例按类名找「询问框还在不在」、按标题找
// 「是哪一个框」，因此新增询问框时**不要**另立窗口类 —— 那会让探测器漏掉新的框。
//
// 设计要点：
//   - 布局数值（正文高度、复选框 Y、客户区高度）由每个询问框自己给（promptModalSpec）：
//     STATIC 控件**不会自动长高**，正文行数多了必须手动加高，否则多出来的行会被裁掉。
//     这些取值都是按正文行数实测调出来的，不要为了「统一」去改。
//   - 控件在 CreateWindowExW 返回之后创建（而非 WM_CREATE 内），这样「窗口 → 状态」映射
//     在控件创建前就已就绪，窗口过程里不需要解析 CREATESTRUCT 这个易错的大结构体；
//   - 用 IsDialogMessageW 承担 Tab 导航、回车触发默认按钮、Esc 取消，避免自造键盘逻辑；
//   - GetDpiForWindow 可能不存在（Win10 1607 之前），先 Find() 探测再调用，
//     否则 syscall.NewLazyDLL 的惰性解析会直接 panic。

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// promptIDCancel 是 IDCANCEL：IsDialogMessageW 响应 Esc 时发出。
	promptIDCancel = 2

	// 关闭询问框的控件 ID（close_prompt_windows.go）。
	closePromptBtnTray     = 1001
	closePromptBtnClose    = 1002
	closePromptBtnCancel   = 1003
	closePromptChkRemember = 1100

	// 「面板已停用」询问框的控件 ID（enable_prompt_windows.go）。
	// 必须与上面几个互不相同：窗口过程按 ID 分发，撞车会让两个询问框的按钮互相串味。
	enablePromptBtnOpen = 1201 // 启用并打开（默认按钮）
	enablePromptBtnKeep = 1202 // 保持停用

	// 新增的 Win32 常量（其余复用 panel_window_windows.go 中的声明）。
	win32WMCreate         = 0x0001
	win32WMCommand        = 0x0111
	win32WMSetFont        = 0x0030
	win32WMCTLColorStatic = 0x0138
	win32BMGetCheck       = 0x00F0
	win32BSTChecked       = 1
	win32BSAutoCheckBox   = 0x00000003
	win32BSPushButton     = 0x00000000
	win32BSDefPushButton  = 0x00000001
	win32SSLeft           = 0x00000000
	win32WSTabStop        = 0x00010000
	win32WSPopup          = 0x80000000
	win32WSCaption        = 0x00C00000
	win32WSSysMenu        = 0x00080000
	win32WSExDlgModalFrm  = 0x00000001
	win32SMCXScreen       = 0
	win32SMCYScreen       = 1
	win32ColorBtnFace     = 15
	win32BkTransparent    = 1
	win32RGBBlack         = 0x00000000
)

const (
	promptFontFace     = "Microsoft YaHei UI"
	promptDefaultDPI   = 96
	promptMaxSaneDPI   = 480
	promptDefaultGUIFn = 17 // GetStockObject(DEFAULT_GUI_FONT)
)

// promptWindowClassName 是所有手工询问框共用的窗口类名；允许测试注入，避免误连到其他对话框。
// 端到端用例按它定位，因此保留不改。
var promptWindowClassName = "PanelDock.ClosePrompt"

var (
	promptUser32 = syscall.NewLazyDLL("user32.dll")
	promptGdi32  = syscall.NewLazyDLL("gdi32.dll")

	promptAdjustWindowRectEx = promptUser32.NewProc("AdjustWindowRectEx")
	promptEnableWindow       = promptUser32.NewProc("EnableWindow")
	promptGetSystemMetrics   = promptUser32.NewProc("GetSystemMetrics")
	promptGetSysColorBrush   = promptUser32.NewProc("GetSysColorBrush")
	promptIsChild            = promptUser32.NewProc("IsChild")
	promptIsDialogMessage    = promptUser32.NewProc("IsDialogMessageW")
	promptIsWindowVisible    = promptUser32.NewProc("IsWindowVisible")
	promptSendMessage        = promptUser32.NewProc("SendMessageW")
	promptSetFocus           = promptUser32.NewProc("SetFocus")
	promptGetDpiForWindow    = promptUser32.NewProc("GetDpiForWindow")
	promptDeleteObject       = promptGdi32.NewProc("DeleteObject")

	promptProcedure = windows.NewCallback(promptProc)

	promptClassOnce  sync.Once
	promptClassError error

	promptStatesMu sync.Mutex
	promptStates   = map[uintptr]*promptState{}
)

// promptResult 是用户对询问框的选择。Action 为空串表示取消（保持现状）。
type promptResult struct {
	Action   string
	Remember bool
}

// promptState 保存对话框的运行时状态；只在对话框所在线程访问。
type promptState struct {
	hwnd     uintptr
	check    uintptr
	action   string
	remember bool
}

func promptRegister(hwnd uintptr, state *promptState) {
	promptStatesMu.Lock()
	promptStates[hwnd] = state
	promptStatesMu.Unlock()
}

func promptLookup(hwnd uintptr) *promptState {
	promptStatesMu.Lock()
	defer promptStatesMu.Unlock()
	return promptStates[hwnd]
}

func promptUnregister(hwnd uintptr) {
	promptStatesMu.Lock()
	delete(promptStates, hwnd)
	promptStatesMu.Unlock()
}

// finish 记录用户选择并销毁对话框；随后 GetMessage 会收到 WM_QUIT 结束模态循环。
func (s *promptState) finish(action string) {
	if s == nil || s.hwnd == 0 {
		return
	}
	hwnd := s.hwnd
	s.hwnd = 0
	s.action = action
	// 没有复选框的询问框（如「面板已停用」）state.check 为 0：它本来就没有「记住我的选择」
	// 这一项，Remember 保持 false。
	if action != "" && s.check != 0 {
		checked, _, _ := promptSendMessage.Call(s.check, win32BMGetCheck, 0, 0)
		s.remember = checked == win32BSTChecked
	}
	panelDestroyWindow.Call(hwnd)
}

// promptModalButton 是询问框上的一个按钮。
type promptModalButton struct {
	ID      uintptr
	Text    string
	Width   int  // 单位值（96 DPI 下的像素），显示前按窗口 DPI 缩放
	Default bool // 默认按钮：回车即选中，并获得初始焦点
}

// promptModalCheck 描述一个可选复选框（「记住我的选择」这类）。
type promptModalCheck struct {
	ID   uintptr
	Text string
	Y    int // 单位值；由询问框自己给（正文高度决定它该落在哪儿）
}

// promptModalSpec 是一个手工询问框的全部静态内容。
//
// 所有长度与位置都是**单位值**（96 DPI 下的像素），显示前统一按窗口 DPI 缩放。
// BodyHeight / ClientHeight 必须由调用方给足：正文写了几行就给几行的高度，
// 还要为可能自动折行的长行留余量（实测过的取值见各询问框的构造处）。
type promptModalSpec struct {
	Caption      string
	Title        string
	Body         string
	BodyHeight   int
	ClientWidth  int
	ClientHeight int
	Check        *promptModalCheck
	// Buttons 从右到左摆放：Buttons[0] 贴右边（「取消」类按钮放最右是 Windows 的惯例）。
	Buttons []promptModalButton
}

func registerPromptClass() error {
	promptClassOnce.Do(func() {
		instance, err := panelModuleInstance()
		if err != nil {
			promptClassError = err
			return
		}
		className, err := windows.UTF16PtrFromString(promptWindowClassName)
		if err != nil {
			promptClassError = err
			return
		}

		cursor, _, _ := panelLoadCursor.Call(0, win32IDCArrow)
		windowClass := panelWNDCLASSEX{
			Size:      uint32(unsafe.Sizeof(panelWNDCLASSEX{})),
			WndProc:   promptProcedure,
			Instance:  instance,
			Cursor:    cursor,
			ClassName: className,
			// (HBRUSH)(COLOR_BTNFACE+1)：与系统对话框同色的背景。
			Background: win32ColorBtnFace + 1,
		}
		atom, _, registerErr := panelRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
		if atom == 0 && registerErr != syscall.Errno(1410) { // 1410 = 类已注册
			promptClassError = fmt.Errorf("register prompt window class: %w", registerErr)
		}
	})
	return promptClassError
}

// showPromptModal 按 spec 显示询问框并阻塞到用户做出选择。
//
// owner 为发起询问的窗口（可为 0），对话框在其中心显示并禁用 owner 形成模态。
// ok 为 false 表示用户取消 / 保持现状，或对话框无法创建 —— 两种情况调用方都应中止后续动作。
//
// 它在**调用方所在线程**上跑自己的消息循环，因此绝不能从 Wails 绑定方法里调用：
// 那会在 WebView2 的消息处理线程上自建 GetMessage 循环（见 AGENTS.md#三条最容易踩的规则）。
// 合法调用点都是自建窗口的消息线程或专用 goroutine（关闭询问 = 面板窗口线程；启用询问 = IPC 处理 goroutine）。
func showPromptModal(owner uintptr, spec promptModalSpec) (promptResult, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := registerPromptClass(); err != nil {
		return promptResult{}, false
	}
	instance, err := panelModuleInstance()
	if err != nil {
		return promptResult{}, false
	}

	dpi := promptDPI(owner)
	scaled := func(v int) int { return v * dpi / promptDefaultDPI }

	titleFont := promptCreateFont(scaled(16), 600)
	textFont := promptCreateFont(scaled(13), 400)
	defer func() {
		if titleFont != 0 {
			promptDeleteObject.Call(titleFont)
		}
		if textFont != 0 {
			promptDeleteObject.Call(textFont)
		}
	}()
	if textFont == 0 {
		textFont, _, _ = panelGetStockObject.Call(promptDefaultGUIFn)
	}
	if titleFont == 0 {
		titleFont = textFont
	}

	style := uint32(win32WSPopup | win32WSCaption | win32WSSysMenu)
	exStyle := uint32(win32WSExDlgModalFrm)

	clientWidth := scaled(spec.ClientWidth)
	clientHeight := scaled(spec.ClientHeight)
	frame := panelRECT{Right: int32(clientWidth), Bottom: int32(clientHeight)}
	promptAdjustWindowRectEx.Call(
		uintptr(unsafe.Pointer(&frame)),
		uintptr(style),
		0,
		uintptr(exStyle),
	)
	windowWidth := int(frame.Right - frame.Left)
	windowHeight := int(frame.Bottom - frame.Top)
	x, y := promptCenter(owner, windowWidth, windowHeight)

	className, err := windows.UTF16PtrFromString(promptWindowClassName)
	if err != nil {
		return promptResult{}, false
	}
	// 标题栏直接用询问框的 Caption：用户一眼能看出自己正在回答什么，
	// 端到端用例也据此区分不同的询问框。
	windowTitle, err := windows.UTF16PtrFromString(spec.Caption)
	if err != nil {
		return promptResult{}, false
	}

	hwnd, _, createErr := panelCreateWindowEx.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowTitle)),
		uintptr(style),
		uintptr(x),
		uintptr(y),
		uintptr(windowWidth),
		uintptr(windowHeight),
		owner,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		_ = createErr
		return promptResult{}, false
	}

	state := &promptState{hwnd: hwnd}
	promptRegister(hwnd, state)

	margin := scaled(20)
	contentWidth := clientWidth - margin*2
	promptCreateControl(hwnd, "STATIC", spec.Title,
		win32WSChild|win32WSVisible|win32SSLeft,
		margin, scaled(18), contentWidth, scaled(26), 0, instance, titleFont)
	promptCreateControl(hwnd, "STATIC", spec.Body,
		win32WSChild|win32WSVisible|win32SSLeft,
		margin, scaled(50), contentWidth, scaled(spec.BodyHeight), 0, instance, textFont)

	if spec.Check != nil {
		state.check = promptCreateControl(hwnd, "BUTTON", spec.Check.Text,
			win32WSChild|win32WSVisible|win32WSTabStop|win32BSAutoCheckBox,
			margin, scaled(spec.Check.Y), contentWidth, scaled(24), spec.Check.ID, instance, textFont)
	}

	buttonHeight := scaled(32)
	buttonY := clientHeight - margin - buttonHeight
	gap := scaled(8)
	var defaultButton uintptr
	rightEdge := clientWidth - margin
	for _, button := range spec.Buttons {
		width := scaled(button.Width)
		buttonX := rightEdge - width
		buttonStyle := uint32(win32WSChild | win32WSVisible | win32WSTabStop | win32BSPushButton)
		if button.Default {
			buttonStyle = uint32(win32WSChild | win32WSVisible | win32WSTabStop | win32BSDefPushButton)
		}
		created := promptCreateControl(hwnd, "BUTTON", button.Text,
			buttonStyle, buttonX, buttonY, width, buttonHeight, button.ID, instance, textFont)
		if button.Default {
			defaultButton = created
		}
		rightEdge = buttonX - gap
	}

	if owner != 0 {
		promptEnableWindow.Call(owner, 0)
	}
	panelShowWindow.Call(hwnd, win32SWShow)
	panelSetForegroundWindow.Call(hwnd)
	if defaultButton != 0 {
		promptSetFocus.Call(defaultButton)
	}

	var message panelMSG
	for {
		result, _, _ := panelGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 { // 0 = WM_QUIT，-1 = 错误
			break
		}
		if message.Hwnd == hwnd || promptIsChildOf(hwnd, message.Hwnd) {
			if handled, _, _ := promptIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&message))); handled != 0 {
				continue
			}
		}
		panelTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		panelDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}

	if owner != 0 {
		promptEnableWindow.Call(owner, 1)
		panelSetForegroundWindow.Call(owner)
	}
	promptUnregister(hwnd)

	if state.action == "" {
		return promptResult{}, false
	}
	return promptResult{Action: state.action, Remember: state.remember}, true
}

// promptProc 是所有询问框共用的窗口过程：按控件 ID 把点击翻译成动作字符串。
// 新增询问框时给按钮一个**唯一**的 ID，并在下面补一个分支。
func promptProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	state := promptLookup(hwnd)
	switch message {
	case win32WMCommand:
		switch uint32(wParam & 0xFFFF) {
		case closePromptBtnTray:
			state.finish(CloseActionTray)
			return 0
		case closePromptBtnClose:
			// 后果由调用方在正文里写明（见 close_prompt_windows.go）。
			state.finish(CloseActionClose)
			return 0
		case closePromptBtnCancel, promptIDCancel:
			state.finish("")
			return 0
		case enablePromptBtnOpen:
			state.finish(promptActionEnable)
			return 0
		case enablePromptBtnKeep:
			state.finish("")
			return 0
		}
	case win32WMCTLColorStatic:
		// 静态文本与对话框背景同色，避免出现白底文字块。
		panelSetBkMode.Call(wParam, win32BkTransparent)
		panelSetTextColor.Call(wParam, win32RGBBlack)
		brush, _, _ := promptGetSysColorBrush.Call(win32ColorBtnFace)
		return brush
	case win32WMCLOSE:
		state.finish("")
		return 0
	case win32WMDESTROY:
		promptUnregister(hwnd)
		panelPostQuitMessage.Call(0)
		return 0
	}

	result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

// promptCreateControl 创建一个子控件并套用字体。
func promptCreateControl(parent uintptr, class, text string, style uint32, x, y, width, height int, id, instance, font uintptr) uintptr {
	className, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	content, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return 0
	}

	hwnd, _, _ := panelCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(content)),
		uintptr(style),
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		parent,
		id,
		instance,
		0,
	)
	if hwnd != 0 && font != 0 {
		promptSendMessage.Call(hwnd, win32WMSetFont, font, 1)
	}
	return hwnd
}

// promptCreateFont 创建对话框字体；height 为正数，内部转成 Win32 要求的负字高。
func promptCreateFont(height, weight int) uintptr {
	face, err := windows.UTF16PtrFromString(promptFontFace)
	if err != nil {
		return 0
	}

	negativeHeight := -height
	font, _, _ := panelCreateFontW.Call(
		uintptr(negativeHeight),
		0, 0, 0,
		uintptr(weight),
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, // OUT_DEFAULT_PRECIS
		0, // CLIP_DEFAULT_PRECIS
		5, // CLEARTYPE_QUALITY
		0, // DEFAULT_PITCH
		uintptr(unsafe.Pointer(face)),
	)
	return font
}

// promptDPI 返回窗口所在显示器的 DPI；API 不可用或取值异常时回退 96。
func promptDPI(hwnd uintptr) int {
	if err := promptGetDpiForWindow.Find(); err != nil {
		return promptDefaultDPI
	}
	dpi, _, _ := promptGetDpiForWindow.Call(hwnd)
	if dpi < promptDefaultDPI || dpi > promptMaxSaneDPI {
		return promptDefaultDPI
	}
	return int(dpi)
}

// promptCenter 把对话框居中到 owner；owner 无效时居中到主屏。
func promptCenter(owner uintptr, width, height int) (int, int) {
	var rect panelRECT
	found := uintptr(0)
	if owner != 0 {
		found, _, _ = panelGetWindowRect.Call(owner, uintptr(unsafe.Pointer(&rect)))
	}
	if owner == 0 || found == 0 {
		screenW, _, _ := promptGetSystemMetrics.Call(win32SMCXScreen)
		screenH, _, _ := promptGetSystemMetrics.Call(win32SMCYScreen)
		return (int(screenW) - width) / 2, (int(screenH) - height) / 3
	}
	return int(rect.Left) + (int(rect.Right-rect.Left)-width)/2,
		int(rect.Top) + (int(rect.Bottom-rect.Top)-height)/2
}

func promptIsChildOf(parent, child uintptr) bool {
	if parent == 0 || child == 0 {
		return false
	}
	result, _, _ := promptIsChild.Call(parent, child)
	return result != 0
}

// promptWindowVisible 报告窗口是否存在且可见；句柄为 0 时返回 false。
func promptWindowVisible(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	visible, _, _ := promptIsWindowVisible.Call(hwnd)
	return visible != 0
}
