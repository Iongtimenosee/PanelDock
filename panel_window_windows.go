//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/go-webview2/webviewloader"
	"golang.org/x/sys/windows"
)

const (
	win32CWUseDefault       = uint32(0x80000000)
	win32IDCArrow           = 32512
	win32IDIApplication     = 32512
	win32SWRestore          = 9
	win32SWShow             = 5
	win32SWHide             = 0
	win32WMCLOSE            = 0x0010
	win32WMDESTROY          = 0x0002
	win32WMSIZE             = 0x0005
	win32WMEXITSIZEMOVE     = 0x0232
	win32WMAPP              = 0x8000
	win32WMLButtonDblClk    = 0x0203
	win32WMRButtonUp        = 0x0205
	win32WSOverlappedWindow = 0x00CF0000
	win32WSChild            = 0x40000000
	win32WSVisible          = 0x10000000
	win32WSClipChildren     = 0x02000000
	win32HWNDTopMost        = ^uintptr(0) // -1
	win32HWNDNotTopMost     = ^uintptr(1) // -2
	win32SWPNOSize          = 0x0001
	win32SWPNOMove          = 0x0002
	win32SWPNoZOrder        = 0x0004
	win32SWPNoActivate      = 0x0010
	win32SWPFrameChanged    = 0x0020
	win32NIMAdd             = 0
	win32NIMDelete          = 2
	win32NIFMessage         = 0x00000001
	win32NIFIcon            = 0x00000002
	win32NIFTIP             = 0x00000004
	win32MFString           = 0x00000000
	win32MFChecked          = 0x00000008
	win32MFUnchecked        = 0x00000000
	win32MFSeparator        = 0x00000800
	win32TPMRETURNCMD       = 0x00000100
	win32WMPaint            = 0x000F
	win32WMMouseMove        = 0x0200
	win32WMLButtonDown      = 0x0201
	win32WMMouseLeave       = 0x02A3 // win32WMLButtonUp 已在 tray_windows.go 声明
	win32WMNCDestroy        = 0x0082
	win32WMCtlColorEdit     = 0x0133
	win32WMKeyDown          = 0x0100
	win32WMChar             = 0x0102
	win32WMGetMinMaxInfo    = 0x0024
	win32WMNCCalcSize       = 0x0083
	win32WMNCHitTest        = 0x0084
	win32WMEraseBkgnd       = 0x0014
	win32WMSettingChange    = 0x001A
	win32TMELeave           = 0x00000002
	win32SWMaximize         = 3
	win32SWMinimize         = 6
	win32VKReturn           = 0x0D
	win32VKEscape           = 0x1B
	panelSOK                = uintptr(0)
	panelENoInterface       = uintptr(0x80004002)

	// WM_NCHITTEST 返回值（LRESULT）。
	win32HTTransparent = ^uintptr(0) // -1：穿透给父窗口
	win32HTClient      = 1
	win32HTCaption     = 2
	win32HTLeft        = 10
	win32HTRight       = 11
	win32HTTop         = 12
	win32HTTopLeft     = 13
	win32HTTopRight    = 14
	win32HTBottom      = 15
	win32HTBottomLeft  = 16
	win32HTBottomRight = 17

	// DrawTextW 格式标志。
	win32DTCenter      = 0x00000001
	win32DTVCenter     = 0x00000004
	win32DTSingleLine  = 0x00000020
	win32DTNoPrefix    = 0x00000800
	win32DTEndEllipsis = 0x00008000

	// 自绘标题栏高度（像素）。窗口是无边框的（WM_NCCALCSIZE 吃掉系统非客户区），
	// 顶部这块是自绘的「站点图标 + 导航按钮 + 地址栏 + 设置 + 窗口按钮」工具栏。
	titleBarHeight = 40
	// 标题栏左上角站点图标的绘制边长（像素）。与任务栏图标不同，这里不随 DPI 缩放 ——
	// titleBarHeight 本身是固定像素，放大图标只会把它撑变形。任务栏那份尺寸见
	// panelIconTargetsFor（按窗口 DPI 取 SM_CXICON）。
	titleBarIconSize = 16
	// 标签栏高度（像素），位于自绘标题栏之下、WebView2 内容区之上。
	tabBarHeight = 40
	// 窗口还原状态下左/右/下三边的缩放边框厚度（客户区内缩，父窗口背景充当边框）。
	// 子窗口（标题栏/标签栏/WebView2）会遮住命中测试，边框只能靠内缩让出来；
	// 上边框不占位——标题栏顶部 4px 的透明条交给父窗口判 HTTOP。
	win32FrameBorder = 8
	// 标题栏顶部留给「上边框缩放」的透明条高度。
	win32FrameTopStrip = 4
	// 标签栏内单个标签的几何参数（像素）。
	tabBarPadding  = 8
	tabBarTabWidth = 160
	tabBarTabGap   = 4
	// 标签栏右端的明暗配色切换按钮（贴右缘，正好落在标题栏关闭按钮的正下方）。
	// 高度与标签一致，宽度与标题栏那些图标按钮同量级。
	tabBarThemeBtnWidth  = 34
	tabBarThemeBtnHeight = 26
)

// 配色切换按钮的字形（Segoe MDL2 Assets）。字形表达**当前状态**，与置顶图钉同一约定
// （空心/实心图钉也是「状态」而不是「将要变成什么」）：太阳 = 当前浅色，月亮 = 当前深色。
const (
	tbGlyphThemeLight = "\uE706" // Brightness：太阳
	tbGlyphThemeDark  = "\uE708" // QuietHours：月亮
)

// win32WMDirectClose 是私有消息：直接销毁窗口，不触发关闭询问。
// 程序主动关闭（删除面板、标签变更重开、退出）与托盘菜单「关闭面板」走这条路径；
// 只有用户点标题栏 X 发出的 WM_CLOSE 才是「交互式关闭」，需要按设置询问。
const win32WMDirectClose = win32WMAPP + 2

// win32WMTitleBarRefresh 是私有消息：通知自绘标题栏整体重绘。
// 状态变化（导航、悬停、窗口尺寸）用它把标题栏刷上屏（GUI 绘制只能回 UI 线程）。
const win32WMTitleBarRefresh = win32WMAPP + 21

// win32WMIconsReady 是私有消息：图标已在后台解析完，请 UI 线程安装。
//
// 图标解析要下载网页、解图、建位图，绝不能占着 UI 线程做；解析结果挂在
// panelWindow.pendingIcons 上，用这条消息把控制权交回来（WM_SETICON 必须在窗口线程发）。
const win32WMIconsReady = win32WMAPP + 22

// win32WMRemoveTab 是私有消息：请求 UI 线程销毁某个标签的 WebView2 并把它从标签栏移除。
//
// WebView2 的 controller 是线程亲和的 COM 对象，销毁（Close/Release）必须在创建它的
// UI 线程上做；调用方（管理面板的绑定线程）把请求挂到 pendingTabRemoves 上投递回来。
// 请求带 done channel：清 profile 必须等浏览器进程先放手（controller.Close 之后目录
// 才删得掉），所以 removeTab 是**同步**等待 UI 线程处理完才返回的。
const win32WMRemoveTab = win32WMAPP + 23

// win32WMSetTheme 是私有消息：请求 UI 线程把本窗口切到 pendingTheme。
//
// 配色可能被任意 goroutine 改（管理面板的设置、系统深浅色变化），而 GDI 重绘与
// WebView2 controller 调用都只能在窗口线程做，所以用挂值 + 投消息把控制权交回来。
const win32WMSetTheme = win32WMAPP + 24

// 托盘图标与托盘菜单由 tray_windows.go 统一实现（承载在常驻 IPC 窗口上），
// 面板窗口不再各自注册图标——否则「一个面板都没开」时托盘里就没有任何入口。

var (
	panelUser32               = syscall.NewLazyDLL("user32.dll")
	panelKernel32             = syscall.NewLazyDLL("kernel32.dll")
	panelShell32              = syscall.NewLazyDLL("shell32.dll")
	panelGdi32                = syscall.NewLazyDLL("gdi32.dll")
	panelCreateWindowEx       = panelUser32.NewProc("CreateWindowExW")
	panelDefWindowProc        = panelUser32.NewProc("DefWindowProcW")
	panelDestroyWindow        = panelUser32.NewProc("DestroyWindow")
	panelDispatchMessage      = panelUser32.NewProc("DispatchMessageW")
	panelGetClientRect        = panelUser32.NewProc("GetClientRect")
	panelGetCursorPos         = panelUser32.NewProc("GetCursorPos")
	panelGetDC                = panelUser32.NewProc("GetDC")
	panelReleaseDC            = panelUser32.NewProc("ReleaseDC")
	panelGetMessage           = panelUser32.NewProc("GetMessageW")
	panelGetWindowRect        = panelUser32.NewProc("GetWindowRect")
	panelInvalidateRect       = panelUser32.NewProc("InvalidateRect")
	panelValidateRect         = panelUser32.NewProc("ValidateRect")
	panelLoadCursor           = panelUser32.NewProc("LoadCursorW")
	panelLoadIcon             = panelUser32.NewProc("LoadIconW")
	panelMoveWindow           = panelUser32.NewProc("MoveWindow")
	panelPostMessage          = panelUser32.NewProc("PostMessageW")
	panelPostQuitMessage      = panelUser32.NewProc("PostQuitMessage")
	panelRegisterClassEx      = panelUser32.NewProc("RegisterClassExW")
	panelSetForegroundWindow  = panelUser32.NewProc("SetForegroundWindow")
	panelSetWindowPos         = panelUser32.NewProc("SetWindowPos")
	panelShowWindow           = panelUser32.NewProc("ShowWindow")
	panelTrackMouseEvent      = panelUser32.NewProc("TrackMouseEvent")
	panelTranslateMessage     = panelUser32.NewProc("TranslateMessage")
	panelUpdateWindow         = panelUser32.NewProc("UpdateWindow")
	panelFillRect             = panelUser32.NewProc("FillRect")
	panelDrawTextW            = panelUser32.NewProc("DrawTextW")
	panelGetModuleHandle      = panelKernel32.NewProc("GetModuleHandleW")
	panelShellNotifyIcon      = panelShell32.NewProc("Shell_NotifyIconW")
	panelCreatePopupMenu      = panelUser32.NewProc("CreatePopupMenu")
	panelAppendMenuW          = panelUser32.NewProc("AppendMenuW")
	panelTrackPopupMenu       = panelUser32.NewProc("TrackPopupMenu")
	panelDestroyMenu          = panelUser32.NewProc("DestroyMenu")
	panelIsWindowVisible      = panelUser32.NewProc("IsWindowVisible")
	panelExtractIconExW       = panelShell32.NewProc("ExtractIconExW")
	panelDestroyIcon          = panelUser32.NewProc("DestroyIcon")
	panelCreateSolidBrush     = panelGdi32.NewProc("CreateSolidBrush")
	panelCreateFontW          = panelGdi32.NewProc("CreateFontW")
	panelGetStockObject       = panelGdi32.NewProc("GetStockObject")
	panelSetTextColor         = panelGdi32.NewProc("SetTextColor")
	panelSetBkMode            = panelGdi32.NewProc("SetBkMode")
	panelSetBkColor           = panelGdi32.NewProc("SetBkColor")
	panelSelectObject         = panelGdi32.NewProc("SelectObject")
	panelDeleteObject         = panelGdi32.NewProc("DeleteObject")
	panelCreateCompatDC       = panelGdi32.NewProc("CreateCompatibleDC")
	panelDeleteDC             = panelGdi32.NewProc("DeleteDC")
	panelCreateDIBSection     = panelGdi32.NewProc("CreateDIBSection")
	panelIsZoomed             = panelUser32.NewProc("IsZoomed")
	panelIsIconic             = panelUser32.NewProc("IsIconic")
	panelSetCapture           = panelUser32.NewProc("SetCapture")
	panelReleaseCapture       = panelUser32.NewProc("ReleaseCapture")
	panelGetCapture           = panelUser32.NewProc("GetCapture")
	panelGetFocus             = panelUser32.NewProc("GetFocus")
	panelSetFocus             = panelUser32.NewProc("SetFocus")
	panelGetWindowText        = panelUser32.NewProc("GetWindowTextW")
	panelGetWindowTextLen     = panelUser32.NewProc("GetWindowTextLengthW")
	panelSetWindowText        = panelUser32.NewProc("SetWindowTextW")
	panelSetWindowLongPtr     = panelUser32.NewProc("SetWindowLongPtrW")
	panelCallWindowProc       = panelUser32.NewProc("CallWindowProcW")
	panelSendMessage          = panelUser32.NewProc("SendMessageW")
	panelMonitorFromWindow    = panelUser32.NewProc("MonitorFromWindow")
	panelGetMonitorInfo       = panelUser32.NewProc("GetMonitorInfoW")
	panelMsimg32              = syscall.NewLazyDLL("msimg32.dll")
	panelDwmapi               = syscall.NewLazyDLL("dwmapi.dll")
	panelOle32                = syscall.NewLazyDLL("ole32.dll")
	panelAlphaBlend           = panelMsimg32.NewProc("AlphaBlend")
	panelDwmExtendFrame       = panelDwmapi.NewProc("DwmExtendFrameIntoClientArea")
	panelDwmSetAttribute      = panelDwmapi.NewProc("DwmSetWindowAttribute")
	panelCoTaskMemFree        = panelOle32.NewProc("CoTaskMemFree")
	panelWindowProcedure      = windows.NewCallback(panelWindowProc)
	panelTabBarProcedure      = windows.NewCallback(panelTabBarProc)
	panelTitleBarProcedure    = windows.NewCallback(panelTitleBarProc)
	panelAddressEditProcCb    = windows.NewCallback(panelAddressEditWndProc)
	panelWindowClassName, _   = windows.UTF16PtrFromString("PanelDock.IsolatedPanel")
	panelTabBarClassName, _   = windows.UTF16PtrFromString("PanelDock.PanelTabBar")
	panelTitleBarClassName, _ = windows.UTF16PtrFromString("PanelDock.PanelTitleBar")
	panelEditClassName, _     = windows.UTF16PtrFromString("EDIT")
	panelWindowClassOnce      sync.Once
	panelWindowClassError     error
	panelTabBarClassOnce      sync.Once
	panelTabBarClassError     error
	panelTitleBarClassOnce    sync.Once
	panelTitleBarClassError   error
	activePanelWindows        = make(map[uintptr]*panelWindow)
	activeTabBarWindows       = make(map[uintptr]*panelWindow)
	activeTitleBarWindows     = make(map[uintptr]*panelWindow)
	activeAddressEdits        = make(map[uintptr]*panelAddressEdit)
	activePanelWindowsMutex   sync.RWMutex

	// 标签栏 GDI 资源（进程级共享，创建后不释放）。
	panelTabBarGDIOnce sync.Once
	panelTabBarFont    uintptr
)

type panelWNDCLASSEX struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  uintptr
}

type panelMSG struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	PointX   int32
	PointY   int32
	LPrivate uint32
}

type panelRECT struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type panelPOINT struct {
	X int32
	Y int32
}

// panelNOTIFYICONDATA 与 Windows NOTIFYICONDATAW 内存布局一致（x64）。
type panelNOTIFYICONDATA struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         windows.GUID
	HBalloonIcon     uintptr
}

type panelCOMProc uintptr

//go:uintptrescapes
func (p panelCOMProc) Call(args ...uintptr) (uintptr, uintptr, error) {
	return syscall.SyscallN(uintptr(p), args...)
}

// ─── IUnknown ──────────────────────────────────────────────────────────────────

type panelIUnknownVtbl struct {
	QueryInterface panelCOMProc
	AddRef         panelCOMProc
	Release        panelCOMProc
}

type panelIUnknown struct {
	Vtbl *panelIUnknownVtbl
}

// panelIIDUnknown 是 IID_IUnknown（{00000000-0000-0000-C000-000000000046}）。
var panelIIDUnknown = windows.GUID{Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}

// panelQueryInterfaceIUnknown 是手工 COM 回调对象共用的 QueryInterface：只承认 IID_IUnknown。
//
// 别写成「任何 IID 都返回 S_OK」：万一运行时 QI 的是别的接口（例如 IMarshal），它随后会按那个
// 接口的槽位调我们的方法，而我们的 vtable 只有 IUnknown + Invoke —— 后面的槽位是野指针。
// 如实报告才安全，而且不会弄丢回调：WebView2 的这几个回调句柄都在本进程内被直接调用，
// 不走跨进程封送（已实测：controller 完成回调在「一律 E_NOINTERFACE」下照样能收到）。
func panelQueryInterfaceIUnknown(self uintptr, riid *windows.GUID, object *uintptr) uintptr {
	if object != nil {
		*object = 0
	}
	if riid == nil || *riid != panelIIDUnknown {
		return panelENoInterface
	}
	if object != nil {
		*object = self
	}
	return panelSOK
}

// ─── ICoreWebView2Environment ──────────────────────────────────────────────────

type panelEnvironmentVtbl struct {
	panelIUnknownVtbl
	CreateController panelCOMProc
}

type panelEnvironment struct {
	Vtbl *panelEnvironmentVtbl
}

// ─── ICoreWebView2Controller ───────────────────────────────────────────────────

type panelControllerVtbl struct {
	panelIUnknownVtbl
	GetIsVisible                      panelCOMProc
	PutIsVisible                      panelCOMProc
	GetBounds                         panelCOMProc
	PutBounds                         panelCOMProc
	GetZoomFactor                     panelCOMProc
	PutZoomFactor                     panelCOMProc
	AddZoomFactorChanged              panelCOMProc
	RemoveZoomFactorChanged           panelCOMProc
	SetBoundsAndZoomFactor            panelCOMProc
	MoveFocus                         panelCOMProc
	AddMoveFocusRequested             panelCOMProc
	RemoveMoveFocusRequested          panelCOMProc
	AddGotFocus                       panelCOMProc
	RemoveGotFocus                    panelCOMProc
	AddLostFocus                      panelCOMProc
	RemoveLostFocus                   panelCOMProc
	AddAcceleratorKeyPressed          panelCOMProc
	RemoveAcceleratorKeyPressed       panelCOMProc
	GetParentWindow                   panelCOMProc
	PutParentWindow                   panelCOMProc
	NotifyParentWindowPositionChanged panelCOMProc
	Close                             panelCOMProc
	GetCoreWebView2                   panelCOMProc
}

type panelController struct {
	Vtbl *panelControllerVtbl
}

// ─── ICoreWebView2（完整 vtable 至 ExecuteScript + add_WebMessageReceived）─────

type panelWebViewVtbl struct {
	panelIUnknownVtbl
	// ICoreWebView2 方法。槽位号 = IUnknown(0–2) 之后的**连续位置**，必须与 WebView2 ABI
	// 逐条对齐 —— 少写一个方法，其后所有槽位整体错位一格，而且**编译、vet、单测全不报错**：
	// 调用会落到隔壁方法上（add_* 打到 remove_*，返回 S_OK 却永不回调；get_CanGoBack 打到
	// get_BrowserProcessId，于是恒为 true）。核对基准：github.com/wailsapp/go-webview2
	// 的 pkg/webview2/ICoreWebView2.go（同一份 ABI 的生成物）。
	// 注意 NavigateToString 在 Navigate 之后，最容易漏。
	GetSettings                            panelCOMProc // 3
	GetSource                              panelCOMProc // 4
	Navigate                               panelCOMProc // 5
	NavigateToString                       panelCOMProc // 6
	add_NavigationStarting                 panelCOMProc // 7
	remove_NavigationStarting              panelCOMProc // 8
	add_ContentLoading                     panelCOMProc // 9
	remove_ContentLoading                  panelCOMProc // 10
	add_SourceChanged                      panelCOMProc // 11
	remove_SourceChanged                   panelCOMProc // 12
	add_HistoryChanged                     panelCOMProc // 13
	remove_HistoryChanged                  panelCOMProc // 14
	add_NavigationCompleted                panelCOMProc // 15
	remove_NavigationCompleted             panelCOMProc // 16
	add_FrameNavigationStarting            panelCOMProc // 17
	remove_FrameNavigationStarting         panelCOMProc // 18
	add_FrameNavigationCompleted           panelCOMProc // 19
	remove_FrameNavigationCompleted        panelCOMProc // 20
	add_ScriptDialogOpening                panelCOMProc // 21
	remove_ScriptDialogOpening             panelCOMProc // 22
	add_PermissionRequested                panelCOMProc // 23
	remove_PermissionRequested             panelCOMProc // 24
	add_ProcessFailed                      panelCOMProc // 25
	remove_ProcessFailed                   panelCOMProc // 26
	AddScriptToExecuteOnDocumentCreated    panelCOMProc // 27
	RemoveScriptToExecuteOnDocumentCreated panelCOMProc // 28
	ExecuteScript                          panelCOMProc // 29
	CapturePreview                         panelCOMProc // 30
	Reload                                 panelCOMProc // 31
	PostWebMessageAsJSON                   panelCOMProc // 32
	PostWebMessageAsString                 panelCOMProc // 33
	add_WebMessageReceived                 panelCOMProc // 34
	remove_WebMessageReceived              panelCOMProc // 35
	CallDevToolsProtocolMethod             panelCOMProc // 36
	get_BrowserProcessId                   panelCOMProc // 37
	get_CanGoBack                          panelCOMProc // 38
	get_CanGoForward                       panelCOMProc // 39
	GoBack                                 panelCOMProc // 40
	GoForward                              panelCOMProc // 41
	// ICoreWebView2 在 GoForward 之后还有若干方法，本结构只用到 Stop：
	GetDevToolsProtocolEventReceiver panelCOMProc // 42
	Stop                             panelCOMProc // 43
}

type panelWebView struct {
	Vtbl *panelWebViewVtbl
}

// ─── Controller creation handler ───────────────────────────────────────────────

type panelControllerHandlerVtbl struct {
	panelIUnknownVtbl
	Invoke panelCOMProc
}

type panelControllerHandler struct {
	Vtbl  *panelControllerHandlerVtbl
	panel *panelWindow
	tabID string
}

var panelControllerHandlerVTable = &panelControllerHandlerVtbl{
	panelIUnknownVtbl: panelIUnknownVtbl{
		QueryInterface: panelCOMProc(windows.NewCallback(panelQueryInterfaceIUnknown)),
		AddRef:         panelCOMProc(windows.NewCallback(panelControllerHandlerAddRef)),
		Release:        panelCOMProc(windows.NewCallback(panelControllerHandlerRelease)),
	},
	Invoke: panelCOMProc(windows.NewCallback(panelControllerHandlerInvoke)),
}

func panelControllerHandlerAddRef(uintptr) uintptr  { return 1 }
func panelControllerHandlerRelease(uintptr) uintptr { return 1 }
func panelControllerHandlerInvoke(h *panelControllerHandler, errorCode uintptr, controller *panelController) uintptr {
	return h.panel.controllerCompleted(h.tabID, errorCode, controller)
}

// ─── Environment creation handler ──────────────────────────────────────────────

type panelEnvironmentHandler struct {
	panel *panelWindow
	tabID string
}

// ─── tabState：单个标签的运行时状态 ────────────────────────────────────────────

type tabState struct {
	tabID       string
	name        string
	url         string
	profilePath string
	environment *panelEnvironment
	controller  *panelController
	webview     *panelWebView

	// 自绘标题栏所需的每标签导航状态（由 WebView2 事件维护，全部在 UI 线程读写）。
	currentURL   string
	canGoBack    bool
	canGoForward bool
	loading      bool // 导航进行中：「停止」按钮的启用依据

	// 站点图标。同一份图标要服务两个完全不同的场景，所以解析出三份产物
	// （来源与尺寸决策都在 icons_windows.go）：
	//   - iconTitleDIB   标题栏左上角绘制用（**预乘** alpha 的 16×16 DIB，供 AlphaBlend）；
	//   - iconSmallHIcon WM_SETICON(ICON_SMALL) 用；
	//   - iconBigHIcon   WM_SETICON(ICON_BIG) 用，也就是任务栏按钮上那个。
	// 全为 0 表示还没解析出来：标题栏画默认地球字形、窗口用系统默认图标。
	// 三者由 UI 线程写入（见 applyPendingIcons），后台解析线程只写 pendingIcons。
	iconTitleDIB   uintptr
	iconSmallHIcon uintptr
	iconBigHIcon   uintptr
	iconMonogram   bool   // 图标是自绘的首字母色块（站点没给出可用图标）
	iconPageURL    string // 这批图标是按哪个页面解析的；同页重复导航不再重复下载

	eventHandlers []any // 已注册的 COM 事件 handler，强引用防止 GC 回收回调对象
}

// ─── panelWindow ───────────────────────────────────────────────────────────────

type panelWindow struct {
	mu sync.Mutex

	app           *App
	id            string
	name          string
	tabs          []tabState
	activeTabID   string
	hwnd          uintptr
	tabBarHwnd    uintptr // 原生标签栏子窗口（自绘标题栏之下）
	titleBarHwnd  uintptr // 自绘标题栏子窗口（无边框窗口的「标题栏」，置顶）
	addressHwnd   uintptr // 标题栏内的地址栏 EDIT 子窗口
	hoverTab      int     // 标签栏悬停高亮的标签索引，-1 表示无
	hoverTracking bool    // 是否已注册 TrackMouseEvent
	hoverThemeBtn bool    // 标签栏右端配色按钮是否处于悬停态
	pressThemeBtn bool    // 配色按钮是否处于按下态（捕获鼠标期间）
	// theme 是本窗口外壳当前生效的明暗侧（light / dark）。
	// 它是**生效值**不是设置里的三态：auto 在构造时就已经解析成具体一侧，
	// 之后靠 WM_SETTINGCHANGE 跟随系统变化。空串表示还没定过，按深色处理。
	theme string
	// pendingTheme 是别的线程请求切换、等着 UI 线程消费的配色（见 requestTheme）。
	pendingTheme  string
	hoverTitleBtn int  // 标题栏悬停高亮的按钮 ID，-1 表示无
	pressTitleBtn int  // 标题栏按下的按钮 ID（捕获鼠标期间），-1 表示无
	titleTracking bool // 标题栏是否已注册 TrackMouseEvent
	closed        bool
	closePending  bool
	alwaysOnTop   bool
	// hiddenInTray 表示窗口当前被藏在托盘里（关闭时选「最小化到托盘」，或托盘菜单里「隐藏」）。
	// 托盘图标本身由 tray_windows.go 统一管理，这里只是它的一个「必须保留图标」输入。
	// 注意：标题栏的最小化按钮**不**走这条路 —— 它就是普通最小化，托盘行为只在关闭时发生
	// （面板级「最小化到托盘」开关已收回，统一由应用级「关闭行为」设置决定）。
	hiddenInTray bool
	// clearOnClose 是「关闭后清空浏览器状态」的构造时快照（PanelConfig.SessionMode==fresh）。
	// 存快照而不是关闭时回查配置：窗口销毁时只该依赖自己创建时的那份配置，
	// 否则「打开时是保留、关闭前被改成清空」会让一次次关闭的后果变得不可预测。
	clearOnClose bool
	// passwordAutosave 是分组级「保存登录密码」的当前值：构造时取自 PanelConfig，
	// 之后可以被 App.SetPanelPasswordAutosave 当场改掉。读写都过 p.mu。
	passwordAutosave bool
	lastRect         panelRECT

	// pendingIcons 是后台解析完成、等着 UI 线程安装的图标（见 icons_windows.go）。
	// 用队列而不是「一个字段」：多个标签可能几乎同时解析完，后来的不能把前一个挤掉。
	pendingIcons []*panelResolvedIcons

	// pendingTabRemoves 是等着 UI 线程执行的「移除单个标签」请求（见 removeTab）。
	// 调用方在别的线程上入队并投递 win32WMRemoveTab；窗口线程消费完逐个 close(done)。
	pendingTabRemoves []panelTabRemoveRequest

	done     chan struct{}
	doneOnce sync.Once
}

// newPanelWindow 基于面板配置创建面板窗口对象（窗口尚未创建）。
func newPanelWindow(app *App, cfg PanelConfig) (*panelWindow, error) {
	if len(cfg.Tabs) == 0 {
		return nil, errCode(errPanelNoTabs)
	}

	tabs := make([]tabState, 0, len(cfg.Tabs))
	// 「关闭后清空」的面板在每次打开前先清一遍会话数据（兜底，见 preparePanelProfile）。
	fresh := cfg.clearsSessionOnClose()
	for _, t := range cfg.Tabs {
		profilePath, err := preparePanelProfile(t.ID, fresh)
		if err != nil {
			return nil, err
		}
		tabs = append(tabs, tabState{
			tabID:       t.ID,
			name:        t.Name,
			url:         t.URL,
			profilePath: profilePath,
			currentURL:  t.URL,
		})
	}

	defaultIdx := cfg.DefaultTabIndex
	if defaultIdx < 0 || defaultIdx >= len(tabs) {
		defaultIdx = 0
	}

	// 托盘相关行为全部由应用级设置决定（关闭行为 + 托盘图标开关，见 tray_windows.go），
	// 面板自身不再持有任何托盘开关。
	return &panelWindow{
		app:           app,
		id:            cfg.ID,
		name:          cfg.Name,
		tabs:          tabs,
		activeTabID:   tabs[defaultIdx].tabID,
		hoverTab:      -1,
		hoverTitleBtn: -1,
		pressTitleBtn: -1,
		alwaysOnTop:   cfg.AlwaysOnTop,
		// 外壳配色在构造时定一次：此后由 WM_SETTINGCHANGE（auto 模式跟系统）
		// 与管理面板的设置变更推送（requestTheme）维护。
		theme:        panelInitialTheme(app),
		clearOnClose: fresh,
		// 密码保存开关同样是构造时快照：它决定本窗口每个标签创建 WebView2 时送进去的值。
		passwordAutosave: cfg.savesPasswords(),
		// 配置里的窗口状态可能已经不可信（历史版本把最小化时的哨兵矩形写了进去），
		// 过一道校验再当初始矩形用（见 initialWindowRect）。
		lastRect: initialWindowRect(cfg.Window),
		done:     make(chan struct{}),
	}, nil
}

// panelWindowTitle 由面板名生成窗口标题；空名回退为应用名。
// 标题形如 "面板名 · PanelDock"：**面板名在前**——任务栏按钮、Alt+Tab、任务管理器的应用列表
// 都是从左往右截断显示的，把面板名放前面才能在窄空间里第一眼认出是哪个面板。
// 与 Wails 管理窗口标题（appMainWindowTitle）不冲突：关闭询问框靠标题精确查找（FindWindowW
// 是整串相等比较），端到端测试也靠标题区分同时打开的多个面板。
func panelWindowTitle(name string) string {
	if name == "" {
		return appMainWindowTitle
	}
	return name + " · " + appMainWindowTitle
}

func (p *panelWindow) windowTitle() string {
	return panelWindowTitle(p.name)
}

func (p *panelWindow) open() error {
	ready := make(chan error, 1)
	go p.run(ready)
	return <-ready
}

func (p *panelWindow) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}

	if p.hwnd == 0 {
		p.closePending = true
		p.mu.Unlock()
		return
	}

	hwnd := p.hwnd
	p.mu.Unlock()
	// 直接关闭：不弹「最小化到托盘 / 直接退出」询问框（那是给用户点 X 用的）。
	panelPostMessage.Call(hwnd, win32WMDirectClose, 0, 0)
}

func (p *panelWindow) wait() {
	<-p.done
}

func (p *panelWindow) activate() {
	p.mu.Lock()
	hwnd := p.hwnd
	closed := p.closed
	p.mu.Unlock()
	if closed || hwnd == 0 {
		return
	}

	p.setHiddenInTray(false)
	panelShowWindow.Call(hwnd, win32SWRestore)
	panelSetForegroundWindow.Call(hwnd)
}

func (p *panelWindow) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// S_FALSE 要单独识别（同 shortcutWithCOM）：本线程此前已被别的组件按 STA
	// 初始化过（Wails/WebView2 加载器干的好事，goroutine 落到那根线程上就撞上），
	// 此时 S_FALSE 也算初始化成功，且规范要求同样配对一次 CoUninitialize。
	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	switch {
	case err == nil || errors.Is(err, shortcutSFalse):
		defer windows.CoUninitialize()
	default:
		p.dispose()
		ready <- fmt.Errorf("initialize panel COM apartment: %w", err)
		return
	}

	if err := registerPanelWindowClass(); err != nil {
		p.dispose()
		ready <- err
		return
	}

	hwnd, err := createPanelWindow(p.windowTitle(), p.lastRect)
	if err != nil {
		p.dispose()
		ready <- err
		return
	}

	p.mu.Lock()
	p.hwnd = hwnd
	closePending := p.closePending
	p.mu.Unlock()

	addPanelWindow(hwnd, p)

	// 无边框化：吃掉系统非客户区后，用 DWM 找回阴影、并关掉 Win11 圆角
	// （子窗口是直角，圆角会被标题栏/WebView2 的角戳穿）。
	setupPanelFrameless(hwnd)

	// 自绘标题栏（favicon + 导航按钮 + 地址栏 + 设置 + 窗口按钮），位于最顶部。
	if err := registerPanelTitleBarClass(); err != nil {
		panelDestroyWindow.Call(hwnd)
		p.dispose()
		ready <- err
		return
	}
	titleHwnd, err := createPanelTitleBar(hwnd)
	if err != nil {
		panelDestroyWindow.Call(hwnd)
		p.dispose()
		ready <- err
		return
	}
	p.mu.Lock()
	p.titleBarHwnd = titleHwnd
	p.mu.Unlock()
	addTitleBarWindow(titleHwnd, p)

	// 地址栏（EDIT 子窗口 + 子类化接管回车导航）。
	editHwnd, err := createPanelAddressEdit(titleHwnd, p)
	if err != nil {
		panelDestroyWindow.Call(hwnd)
		p.dispose()
		ready <- err
		return
	}
	p.mu.Lock()
	p.addressHwnd = editHwnd
	p.mu.Unlock()

	// 顶部原生标签栏：不向远程页面注入任何 DOM/脚本。
	if err := registerPanelTabBarClass(); err != nil {
		panelDestroyWindow.Call(hwnd)
		p.dispose()
		ready <- err
		return
	}
	barHwnd, err := createPanelTabBar(hwnd)
	if err != nil {
		panelDestroyWindow.Call(hwnd)
		p.dispose()
		ready <- err
		return
	}
	p.mu.Lock()
	p.tabBarHwnd = barHwnd
	p.mu.Unlock()
	addTabBarWindow(barHwnd, p)

	// 显式摆一次布局：创建期间的 WM_SIZE 早于子窗口创建，这里的内缩边框摆位没人做过。
	p.resize()

	if p.alwaysOnTop {
		p.setAlwaysOnTop(true)
	}

	panelShowWindow.Call(hwnd, win32SWShow)
	panelUpdateWindow.Call(hwnd)

	if closePending {
		panelDestroyWindow.Call(hwnd)
		ready <- nil
		return
	}

	// 为每个标签创建独立的 WebView2 环境。
	for i := range p.tabs {
		tab := &p.tabs[i]
		envHandler := &panelEnvironmentHandler{panel: p, tabID: tab.tabID}
		if err := webviewloader.CreateCoreWebView2EnvironmentWithOptions(
			envHandler,
			webviewloader.WithUserDataFolder(tab.profilePath),
		); err != nil {
			// 某个标签创建失败，不阻断其他标签（该标签显示为空白，可改用其他标签）。
			_ = err
		}
	}

	ready <- nil
	p.messageLoop()
	p.dispose()
}

func (p *panelWindow) messageLoop() {
	var message panelMSG
	for {
		result, _, _ := panelGetMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if result == 0 || result == ^uintptr(0) {
			return
		}

		panelTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		panelDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

// frameInset 返回还原状态下左/右/下三边的缩放边框厚度（最大化时为 0）。
// 无边框窗口的非客户区已被吃掉，缩放边框靠子窗口内缩让出来（见 win32FrameBorder）。
func (p *panelWindow) frameInset() int32 {
	if p.hwnd == 0 {
		return 0
	}
	if zoomed, _, _ := panelIsZoomed.Call(p.hwnd); zoomed != 0 {
		return 0
	}
	return win32FrameBorder
}

func (p *panelWindow) contentBounds() panelRECT {
	var bounds panelRECT
	if p.hwnd != 0 {
		panelGetClientRect.Call(p.hwnd, uintptr(unsafe.Pointer(&bounds)))
	}
	inset := p.frameInset()
	bounds.Left += inset
	bounds.Right -= inset
	bounds.Top += titleBarHeight + tabBarHeight
	bounds.Bottom -= inset
	if bounds.Bottom < bounds.Top {
		bounds.Bottom = bounds.Top
	}
	if bounds.Right < bounds.Left {
		bounds.Right = bounds.Left
	}
	return bounds
}

func (p *panelWindow) resize() {
	p.mu.Lock()
	hwnd := p.hwnd
	barHwnd := p.tabBarHwnd
	titleHwnd := p.titleBarHwnd
	closed := p.closed
	tabs := make([]tabState, len(p.tabs))
	copy(tabs, p.tabs)
	p.mu.Unlock()
	if closed || hwnd == 0 {
		return
	}

	// 布局（自上而下）：自绘标题栏 → 原生标签栏 → WebView2 内容区。
	// 还原状态下左/右内缩 win32FrameBorder 给缩放边框；最大化时铺满。
	var client panelRECT
	if rectOK, _, _ := panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); rectOK != 0 {
		inset := p.frameInset()
		width := uintptr(client.Right - client.Left - 2*inset)
		if titleHwnd != 0 {
			panelMoveWindow.Call(titleHwnd, uintptr(inset), 0, width, titleBarHeight, 1)
		}
		if barHwnd != 0 {
			panelMoveWindow.Call(barHwnd, uintptr(inset), titleBarHeight, width, tabBarHeight, 1)
		}
	}

	bounds := p.contentBounds()
	for i := range tabs {
		if tabs[i].controller != nil {
			_ = tabs[i].controller.putBounds(bounds)
		}
	}
}

func (p *panelWindow) fail(err error) {
	_ = err

	p.mu.Lock()
	hwnd := p.hwnd
	closed := p.closed
	p.mu.Unlock()
	if closed || hwnd == 0 {
		return
	}

	panelDestroyWindow.Call(hwnd)
}

// destroyPanelWindow 销毁面板窗口，并在销毁**之前**记下最后一次窗口位置。
//
// 顺序不能反：DestroyWindow 同步触发 WM_DESTROY → dispose，那时已读不到矩形
//（这段逻辑一度写在 dispose 里，因此常年是死代码）。最小化由 captureBounds 跳过。
func destroyPanelWindow(panel *panelWindow, hwnd uintptr) {
	if panel != nil {
		panel.recordBounds()
	}
	panelDestroyWindow.Call(hwnd)
}

// recordBounds 记录窗口当前屏幕矩形（含非客户区），用于持久化窗口状态。
func (p *panelWindow) recordBounds() {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd == 0 {
		return
	}

	rect, ok := p.captureBounds(hwnd)
	if !ok {
		return
	}
	p.mu.Lock()
	p.lastRect = rect
	p.mu.Unlock()
}

// captureBounds 读取窗口的屏幕矩形供持久化；返回 false 表示这次不该记录。
//
// **最小化的窗口必须跳过**：此时 GetWindowRect 返回的是 Windows 的哨兵值
// (-32000,-32000,160,28)（「图标位置」，标题栏最小化按钮走的正是 SW_MINIMIZE），
// 存进配置就变成「下次打开这个面板缩成一个小方块、还落在屏幕外」。
// 实测到过：面板最小化后从托盘关闭，配置里记下了
// x=-32000 / 160x28。最小化期间保留上一次的正常矩形即可 —— 用户还原窗口时会再记一次。
func (p *panelWindow) captureBounds(hwnd uintptr) (panelRECT, bool) {
	if iconic, _, _ := panelIsIconic.Call(hwnd); iconic != 0 {
		return panelRECT{}, false
	}

	var rect panelRECT
	if result, _, _ := panelGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); result == 0 {
		return panelRECT{}, false
	}
	if !plausibleWindowRect(rect) {
		return panelRECT{}, false
	}
	return rect, true
}

// 窗口状态的可信下限。阈值取得很松：只挡「明显不是一个面板窗口」的值
// （哨兵坐标、被压成零头的矩形），用户真把窗口拖成细长条也照样记下来。
const (
	minPlausibleWindowWidth  = 320
	minPlausibleWindowHeight = 240
)

// plausibleWindowRect 判断一个窗口矩形是否值得写进（或从配置里读出来使用）。
//
// 第二道防线（第一道是 IsIconic）：配置里存了坏值不是「显示得难看」，而是
// 「下次打开找不到窗口」—— 宁可不记录，也不要写坏。
func plausibleWindowRect(rect panelRECT) bool {
	if rect.Left <= -30000 || rect.Top <= -30000 {
		return false
	}
	return rect.Right-rect.Left >= minPlausibleWindowWidth && rect.Bottom-rect.Top >= minPlausibleWindowHeight
}

// plausibleWindowState 是 plausibleWindowRect 的配置侧入口，用于**写入配置前**的最后一道检查。
//
// 为什么出口也要查：坏值不是「显示得奇怪」而是「下次打开找不到窗口」，代价极不对称 ——
// 宁可这次不更新（窗口下次回到上一次的位置），也不要写进去。入口（captureBounds）挡的是
// 已知路径，这里挡的是「将来某条新路径把队形打乱」。
func plausibleWindowState(state PanelWindowState) bool {
	return plausibleWindowRect(panelRECT{
		Left:   int32(state.X),
		Top:    int32(state.Y),
		Right:  int32(state.X + state.Width),
		Bottom: int32(state.Y + state.Height),
	})
}

// initialWindowRect 把配置里的窗口状态转成创建窗口用的初始矩形。
//
// 配置里可能已经存着不可信的值（历史版本把最小化时的哨兵矩形写了进去），照单全收会让面板
// 「打开即缩在屏幕外」。这时退回与新建面板一致的默认尺寸（见 config.go 的 create）。
func initialWindowRect(state PanelWindowState) panelRECT {
	rect := panelRECT{
		Left:   int32(state.X),
		Top:    int32(state.Y),
		Right:  int32(state.X + state.Width),
		Bottom: int32(state.Y + state.Height),
	}
	if !plausibleWindowRect(rect) {
		return panelRECT{Right: defaultPanelWindowWidth, Bottom: defaultPanelWindowHeight}
	}
	return rect
}

// lastWindowState 导出最近一次窗口位置与大小。
func (p *panelWindow) lastWindowState() PanelWindowState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return PanelWindowState{
		X:      int(p.lastRect.Left),
		Y:      int(p.lastRect.Top),
		Width:  int(p.lastRect.Right - p.lastRect.Left),
		Height: int(p.lastRect.Bottom - p.lastRect.Top),
	}
}

// passwordAutosaveEnabled 读当前的「保存登录密码」开关。
func (p *panelWindow) passwordAutosaveEnabled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.passwordAutosave
}

// setPasswordAutosave 更新「保存登录密码」开关，并立刻重设到该窗口所有已创建的 WebView2 上。
//
// 为什么不像会话处理方式那样「只写配置、下次打开才生效」：这个开关就是 WebView2 上的一个
// 属性，用户刚在卡片上取消勾选、界面却还照旧弹保存提示，只会以为开关坏了。重设是就地改属性，
// 不需要重开窗口、也不会动用户当前页面。
//
// COM 调用放在锁外：与 setAlwaysOnTop 同一个考虑 —— 拿着 p.mu 去调外部对象，
// 对方万一回调进本窗口就自锁了。
func (p *panelWindow) setPasswordAutosave(on bool) {
	p.mu.Lock()
	p.passwordAutosave = on
	webviews := make([]*panelWebView, 0, len(p.tabs))
	for i := range p.tabs {
		if p.tabs[i].webview != nil {
			webviews = append(webviews, p.tabs[i].webview)
		}
	}
	p.mu.Unlock()

	for _, webview := range webviews {
		applyPasswordAutosave(webview, on)
	}
}

func (p *panelWindow) setAlwaysOnTop(on bool) {
	p.mu.Lock()
	hwnd := p.hwnd
	titleHwnd := p.titleBarHwnd
	p.alwaysOnTop = on
	p.mu.Unlock()

	if hwnd != 0 {
		insertAfter := win32HWNDNotTopMost
		if on {
			insertAfter = win32HWNDTopMost
		}
		panelSetWindowPos.Call(hwnd, insertAfter, 0, 0, 0, 0, win32SWPNOMove|win32SWPNOSize)
	}
	// 标题栏的置顶开关要跟着变：点它自己、点托盘菜单、改管理面板三处都能置顶，
	// 不刷新的话图标会一直停在旧状态（只靠调用方记得刷新一定会漏）。
	if titleHwnd != 0 {
		panelInvalidateRect.Call(titleHwnd, 0, 1)
	}
	if p.app != nil {
		_ = p.app.config.setAlwaysOnTop(p.id, on)
		// 管理面板卡片上的「窗口置顶」勾选框读的是面板列表，同进程改完不广播就会挂在旧值上。
		p.app.notifyPanelsChanged()
	}
}

// setHiddenInTray 标记/解除「窗口正藏在托盘里」。
// 这是托盘图标的一个「必须保留」输入：即使全局关闭了托盘图标，只要还有窗口藏在
// 托盘里就必须留着图标，否则窗口再也叫不回来。
func (p *panelWindow) setHiddenInTray(on bool) {
	p.mu.Lock()
	changed := p.hiddenInTray != on
	p.hiddenInTray = on
	p.mu.Unlock()

	if p.app != nil {
		// 托盘菜单的「已隐藏的窗口」队列跟着同一笔状态走：藏起来 → 进队尾（于是它就是
		//「最近一次关闭到托盘」的那个，双击托盘恢复的正是它）；恢复 → 出队。
		// 少同步任何一边，菜单里就会出现点了没反应的幻影项。
		switch {
		case on:
			// 重复隐藏也要重新入队尾：「最近一次」说的是最近那次动作，
			// 不是首次藏起来的时刻（changed 为 false 时同样要刷新顺序）。
			p.app.noteTrayHidden(p.id)
		case changed:
			p.app.forgetTrayHidden(p.id)
		}
		if changed {
			p.app.syncTray()
		}
	}
}

// isHiddenInTray 报告窗口当前是否藏在托盘里。
func (p *panelWindow) isHiddenInTray() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hiddenInTray
}

// isAlwaysOnTop 报告窗口当前的置顶状态（供托盘菜单勾选显示）。
func (p *panelWindow) isAlwaysOnTop() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.alwaysOnTop
}

// hideToTray 把窗口隐藏到系统托盘（最小化到托盘 / 关闭询问选「最小化到托盘」时使用）。
// 隐藏前标记 hiddenInTray，确保托盘图标不会随之消失。
//
// 标记排在 ShowWindow 之前、也排在 hwnd 判断之外：状态与 Win32 调用解耦后，句柄为零时
// （窗口尚在创建、或单测里的替身对象）这笔状态照样成立，不会整个被吞掉。
func (p *panelWindow) hideToTray() {
	p.setHiddenInTray(true)
	if hwnd := p.windowHandle(); hwnd != 0 {
		panelShowWindow.Call(hwnd, win32SWHide)
	}
}

// isClosed 报告窗口是否已经开始销毁（销毁完成后 App.panels 里也不会再有它）。
func (p *panelWindow) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// windowHandle 返回面板窗口句柄（0 表示窗口尚未创建或已销毁）。
func (p *panelWindow) windowHandle() uintptr {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hwnd
}

// handleInteractiveClose 处理用户主动关闭（标题栏 X）。
// 按设置询问或直接执行：隐藏到托盘 / 直接关闭本面板 / 取消（窗口保持不动）。
//
// 「直接关闭」只作用于本面板：其他面板窗口与管理窗口都不受影响。
func (p *panelWindow) handleInteractiveClose() {
	if p.app == nil {
		return
	}
	decision := p.app.decideClose(p.windowHandle(), p.closePromptNote())
	switch decision.Action {
	case CloseActionTray:
		// 记住的选择照原样落盘（那是用户跨会话的全局意图），本次只是不执行它。
		p.app.commitCloseChoice(decision)
		if p.app.closeSkipsTrayHide() {
			p.close()
			return
		}
		p.hideToTray()
	case CloseActionClose:
		p.app.commitCloseChoice(decision)
		// 只关闭本窗口。托盘图标由常驻 IPC 窗口承载，本窗口消失不影响它；
		// 进程寿命由 App.quitWhenNoWindows 判定：没有窗口、也没有托盘图标时才收工。
		p.close()
	}
}

// closePromptNote 生成面板询问框的补充说明。
// 只有「关掉它程序就收工」这一种情况需要额外说明——按钮写着「直接关闭」，
// 而后果比字面更大时必须讲清楚，否则就回到了「按了才发现在退程序」的老问题。
// 文案按当前语言取（App.nativeText），语言切换对下一个弹出的询问框即时生效。
func (p *panelWindow) closePromptNote() string {
	if p.app == nil || !p.app.panelCloseEndsProcess() {
		return ""
	}
	return p.app.nativeText().ClosePrompt.Note
}

func (p *panelWindow) isVisible() bool {
	p.mu.Lock()
	hwnd := p.hwnd
	p.mu.Unlock()
	if hwnd == 0 {
		return false
	}

	result, _, _ := panelIsWindowVisible.Call(hwnd)
	return result != 0
}

// switchTab 切换到指定标签，返回标签索引（供配置持久化使用）。
func (p *panelWindow) switchTab(tabID string) int {
	p.mu.Lock()
	foundIdx := -1
	for i := range p.tabs {
		isActive := p.tabs[i].tabID == tabID
		if isActive {
			foundIdx = i
		}
		if p.tabs[i].controller != nil {
			visible := uintptr(0)
			if isActive {
				visible = 1
			}
			p.tabs[i].controller.putIsVisible(visible)
		}
	}
	if foundIdx >= 0 {
		p.activeTabID = tabID
	}
	barHwnd := p.tabBarHwnd
	p.mu.Unlock()

	if foundIdx >= 0 && barHwnd != 0 {
		panelInvalidateRect.Call(barHwnd, 0, 0)
	}
	if foundIdx >= 0 {
		// 标题栏展示的是「活动标签」的图标/地址/导航状态，切标签必须同步。
		p.syncTitleBarFromTab(tabID)
		// 任务栏按钮上那个图标也跟着走 —— 否则在 A 面板的按钮上看到 B 站点的图标。
		p.applyActiveTabIcons(tabID)
	}
	return foundIdx
}

// panelTabRemoveRequest 是一条「移除单个标签」请求：tabID 指明目标，
// done 在 UI 线程执行完后关闭，removeTab 靠它同步等待。
type panelTabRemoveRequest struct {
	tabID string
	done  chan struct{}
}

// removeTab 请求销毁指定标签的 WebView2 并把它从标签栏移除，**只动这一个标签**，
// 其余标签的页面原样保留（用户删一个标签不该把整窗都关了重开）。
//
// 返回 false 表示没做任何事：窗口没开、已关闭或本来就没有这个标签 ——
// 调用方据此决定是否还需要清 profile（窗口没开时目录没被占用，直接清就行）。
//
// COM 销毁投递给 UI 线程执行（线程亲和，见 win32WMRemoveTab），这里同步等待：
// 清 profile 的前提是浏览器进程放手，不等完就回去清只会白清。
// 等待有 5 秒上限兜底：UI 线程卡死（理论不会发生）时不能把管理面板一起挂住，
// 此时返回 true 让调用方继续 —— clearUntilStable 的重试预算还能再兜一层。
func (p *panelWindow) removeTab(tabID string) bool {
	req := panelTabRemoveRequest{tabID: tabID, done: make(chan struct{})}

	p.mu.Lock()
	if p.closed || p.hwnd == 0 {
		p.mu.Unlock()
		return false
	}
	found := false
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			found = true
			break
		}
	}
	if !found {
		p.mu.Unlock()
		return false
	}
	p.pendingTabRemoves = append(p.pendingTabRemoves, req)
	hwnd := p.hwnd
	p.mu.Unlock()

	panelPostMessage.Call(hwnd, win32WMRemoveTab, 0, 0)

	select {
	case <-req.done:
	case <-time.After(5 * time.Second):
		println("remove tab: UI thread did not respond in 5s:", tabID)
	}
	return true
}

// consumeTabRemoves 在 UI 线程上执行积压的移除请求。
func (p *panelWindow) consumeTabRemoves() {
	p.mu.Lock()
	reqs := p.pendingTabRemoves
	p.pendingTabRemoves = nil
	p.mu.Unlock()
	for _, req := range reqs {
		p.removeTabNow(req.tabID)
		close(req.done)
	}
}

// removeTabNow 在 UI 线程上销毁单个标签的全部资源（与 dispose 的单标签版本同一套顺序）。
func (p *panelWindow) removeTabNow(tabID string) {
	p.mu.Lock()
	idx := -1
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			idx = i
			break
		}
	}
	if idx < 0 {
		p.mu.Unlock()
		return
	}
	wasActive := p.activeTabID == tabID
	tab := p.tabs[idx]
	p.tabs = append(p.tabs[:idx], p.tabs[idx+1:]...)

	// 删的是活动标签就切到相邻的（原位置的下一个，越界取末尾）。
	nextActive := p.activeTabID
	if wasActive {
		if len(p.tabs) == 0 {
			nextActive = ""
		} else {
			j := idx
			if j >= len(p.tabs) {
				j = len(p.tabs) - 1
			}
			nextActive = p.tabs[j].tabID
		}
		p.activeTabID = nextActive
	}

	// 路上还没安装的图标就地丢弃，不然句柄随解析对象一起消失。
	var staleIcons []*panelResolvedIcons
	kept := p.pendingIcons[:0]
	for _, r := range p.pendingIcons {
		if r.tabID == tabID {
			staleIcons = append(staleIcons, r)
		} else {
			kept = append(kept, r)
		}
	}
	p.pendingIcons = kept
	p.mu.Unlock()

	// 管理窗口「刷新图标」的样本跟着注销；图标句柄删除必须在 UI 线程（与绘制同线程）。
	unregisterPanelIconSamples([]string{tabID})
	if tab.iconTitleDIB != 0 {
		panelDeleteObject.Call(tab.iconTitleDIB)
	}
	if tab.iconSmallHIcon != 0 {
		panelDestroyIcon.Call(tab.iconSmallHIcon)
	}
	if tab.iconBigHIcon != 0 {
		panelDestroyIcon.Call(tab.iconBigHIcon)
	}
	for _, r := range staleIcons {
		r.dispose()
	}

	// COM 释放放锁外（与 dispose 同一考虑：拿着 p.mu 调外部对象可能自锁）。
	if tab.controller != nil {
		_ = tab.controller.close()
	}
	panelCOMRelease(tab.webview)
	panelCOMRelease(tab.controller)
	panelCOMRelease(tab.environment)

	if len(p.tabs) == 0 {
		// 防御路径：App 层保证删标签至少留一个，真到这里说明状态已经不正常，
		// 按整个面板关闭处理，别留一个空壳窗口。
		p.close()
		return
	}
	p.switchTab(nextActive)
}

// updateTabInfos 把编辑后的名称/地址热更新到窗口里**已存在**的标签上，不重开窗口。
//
// 名称变了重画标签栏；地址变了让对应 WebView2 重新导航。controller.Close 之外的
// WebView2 调用在本项目里已有跨线程先例（setPasswordAutosave 直接改 Settings 属性），
// navigate 同样可行 —— 换来的是纯改名/改址/删标签的保存不再闪一遍整窗重开。
func (p *panelWindow) updateTabInfos(tabs []PanelTab) {
	type renavigate struct {
		webview *panelWebView
		url     string
	}
	var renavigates []renavigate
	nameChanged := false

	p.mu.Lock()
	for _, t := range tabs {
		for i := range p.tabs {
			if p.tabs[i].tabID != t.ID {
				continue
			}
			if p.tabs[i].name != t.Name {
				p.tabs[i].name = t.Name
				nameChanged = true
			}
			if p.tabs[i].url != t.URL && p.tabs[i].webview != nil {
				p.tabs[i].url = t.URL
				renavigates = append(renavigates, renavigate{webview: p.tabs[i].webview, url: t.URL})
			}
			break
		}
	}
	barHwnd := p.tabBarHwnd
	p.mu.Unlock()

	if nameChanged && barHwnd != 0 {
		panelInvalidateRect.Call(barHwnd, 0, 0)
	}
	for _, r := range renavigates {
		_ = r.webview.navigate(r.url)
	}
}

// ─── Environment / Controller 回调 ─────────────────────────────────────────────

func (h *panelEnvironmentHandler) EnvironmentCompleted(errorCode webviewloader.HRESULT, environment *webviewloader.ICoreWebView2Environment) webviewloader.HRESULT {
	if errorCode != 0 || environment == nil {
		h.panel.fail(fmt.Errorf("create WebView2 environment for tab %s: %#x", h.tabID, uint32(errorCode)))
		return 0
	}

	h.panel.mu.Lock()
	if h.panel.closed {
		h.panel.mu.Unlock()
		return 0
	}

	nativeEnvironment := (*panelEnvironment)(unsafe.Pointer(environment))
	panelCOMAddRef(nativeEnvironment)

	// 找到对应的 tabState。
	var tab *tabState
	for i := range h.panel.tabs {
		if h.panel.tabs[i].tabID == h.tabID {
			tab = &h.panel.tabs[i]
			break
		}
	}
	if tab == nil {
		h.panel.mu.Unlock()
		return 0
	}

	tab.environment = nativeEnvironment
	controllerHandler := &panelControllerHandler{
		Vtbl:  panelControllerHandlerVTable,
		panel: h.panel,
		tabID: h.tabID,
	}
	hwnd := h.panel.hwnd
	h.panel.mu.Unlock()

	if err := nativeEnvironment.createController(hwnd, controllerHandler); err != nil {
		h.panel.fail(fmt.Errorf("create WebView2 controller for tab %s: %w", h.tabID, err))
	}
	return 0
}

func (p *panelWindow) controllerCompleted(tabID string, errorCode uintptr, controller *panelController) uintptr {
	if int32(errorCode) < 0 || controller == nil {
		p.fail(fmt.Errorf("create WebView2 controller for tab %s: %#x", tabID, uint32(errorCode)))
		return 0
	}

	panelCOMAddRef(controller)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		panelCOMRelease(controller)
		return 0
	}

	var tab *tabState
	for i := range p.tabs {
		if p.tabs[i].tabID == tabID {
			tab = &p.tabs[i]
			break
		}
	}
	if tab == nil {
		p.mu.Unlock()
		panelCOMRelease(controller)
		return 0
	}

	tab.controller = controller
	p.mu.Unlock()

	// WebView2 默认底色跟随外壳主题：页面首帧之前、以及页面自己没铺底的地方
	// 露出的就是它（深色主题下不设会先闪一记白）。见 theme_panel_windows.go。
	controller.setDefaultBackgroundColor(p.chrome().bg)

	webview, err := controller.getWebView()
	if err != nil {
		p.fail(fmt.Errorf("get WebView2 for tab %s: %w", tabID, err))
		return 0
	}
	panelCOMAddRef(webview)

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		panelCOMRelease(webview)
		return 0
	}
	tab.webview = webview
	p.mu.Unlock()

	// 按分组的「保存登录密码」开关设置这个标签（见 password_autosave_windows.go）。
	// 放在 navigate 之前：登录页首屏加载时开关必须已经生效，否则第一次不会触发保存提示。
	applyPasswordAutosave(webview, p.passwordAutosaveEnabled())

	// 注册导航事件：自绘标题栏的地址栏/前进后退/刷新停止/favicon 全靠它们驱动。
	p.registerTabEvents(tabID, webview)

	// 设置 WebView2 控制器边界（顶部留给自绘标题栏 + 原生标签栏）。
	bounds := p.contentBounds()
	_ = controller.putBounds(bounds)

	// 导航到目标 URL。
	if err := webview.navigate(tab.url); err != nil {
		p.fail(fmt.Errorf("navigate tab %s: %w", tabID, err))
	}

	// 设置初始可见性。
	p.mu.Lock()
	isActive := (tabID == p.activeTabID)
	p.mu.Unlock()
	if isActive {
		controller.putIsVisible(1)
	} else {
		controller.putIsVisible(0)
	}

	return 0
}

// onNavigationCompleted 与标签栏 JS 注入机制已移除：
// 标签栏改为原生 Win32 子窗口（见 panelTabBarProc），不再向远程页面注入任何 DOM/脚本，
// 远程页面与宿主之间也不存在 WebMessage 通道。

// ─── 从托盘恢复显示 ────────────────────────────────────────────────────────────

// showFromTray 从托盘恢复窗口显示，并解除「藏在托盘里」的标记
// （面板窗口自身不再注册托盘图标，图标统一由 tray_windows.go 管理）。
//
// 它同时是托盘菜单里「已隐藏的窗口」项的实现：setHiddenInTray(false) 顺带把本窗口
// 从托盘菜单队列里摘掉 —— 用户看到的正是「点一次名字，窗口回来，菜单里那条消失」。
// 之后要再把它藏回托盘，双击托盘图标即可（它已成双击的目标），菜单里重新出现。
func (p *panelWindow) showFromTray() {
	p.setHiddenInTray(false)
	hwnd := p.windowHandle()
	if hwnd == 0 {
		return
	}
	panelShowWindow.Call(hwnd, win32SWRestore)
	panelSetForegroundWindow.Call(hwnd)
}

// ─── 销毁 ──────────────────────────────────────────────────────────────────────

func (p *panelWindow) dispose() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}

	p.closed = true
	hwnd := p.hwnd
	p.mu.Unlock()

	// 窗口位置不在这里记录 —— 见 destroyPanelWindow：WM_DESTROY 时已读不到矩形。

	// 清理所有标签的 COM 资源。
	p.mu.Lock()
	p.hwnd = 0
	barHwnd := p.tabBarHwnd
	p.tabBarHwnd = 0
	titleHwnd := p.titleBarHwnd
	p.titleBarHwnd = 0
	editHwnd := p.addressHwnd
	p.addressHwnd = 0
	p.hoverTab = -1
	p.hoverTracking = false
	p.hoverTitleBtn = -1
	p.pressTitleBtn = -1
	p.titleTracking = false
	p.hiddenInTray = false

	tabs := make([]tabState, len(p.tabs))
	copy(tabs, p.tabs)
	for i := range p.tabs {
		p.tabs[i].environment = nil
		p.tabs[i].controller = nil
		p.tabs[i].webview = nil
		p.tabs[i].eventHandlers = nil
		p.tabs[i].iconTitleDIB = 0
		p.tabs[i].iconSmallHIcon = 0
		p.tabs[i].iconBigHIcon = 0
	}
	// 窗口已经关了，还在路上/已到货但没安装的图标就地释放，否则句柄随对象一起消失。
	pending := p.pendingIcons
	p.pendingIcons = nil
	// 同理：还没执行的移除请求也要放行，removeTab 的等待方不该干等到超时。
	// 标签已经随窗口一起销毁，什么都不用再做，只关 done。
	pendingRemoves := p.pendingTabRemoves
	p.pendingTabRemoves = nil
	p.mu.Unlock()

	for _, req := range pendingRemoves {
		close(req.done)
	}

	removePanelWindow(hwnd)
	removeTabBarWindow(barHwnd)
	removeTitleBarWindow(titleHwnd)
	removeAddressEdit(editHwnd)

	// 窗口没了，页面也就没了：把留给管理窗口「刷新图标」按钮的图标样本一并注销。
	// 不注销的后果很具体 —— 面板关掉之后，按钮仍认为「网页窗口那边加载好了图标」，
	// 于是给用户写一份早已过期的图标出去。
	panelClosedTabIDs := make([]string, 0, len(tabs))
	for i := range tabs {
		if tabs[i].tabID != "" {
			panelClosedTabIDs = append(panelClosedTabIDs, tabs[i].tabID)
		}
	}
	unregisterPanelIconSamples(panelClosedTabIDs)

	for i := range tabs {
		if tabs[i].iconTitleDIB != 0 {
			panelDeleteObject.Call(tabs[i].iconTitleDIB)
		}
		if tabs[i].iconSmallHIcon != 0 {
			panelDestroyIcon.Call(tabs[i].iconSmallHIcon)
		}
		if tabs[i].iconBigHIcon != 0 {
			panelDestroyIcon.Call(tabs[i].iconBigHIcon)
		}
	}
	for _, r := range pending {
		r.dispose()
	}

	for i := range tabs {
		if tabs[i].controller != nil {
			_ = tabs[i].controller.close()
		}
		panelCOMRelease(tabs[i].webview)
		panelCOMRelease(tabs[i].controller)
		panelCOMRelease(tabs[i].environment)
	}

	// 「关闭后清空」在这里**同步**执行，不能丢给 goroutine：
	//   - restartPanel 是「关掉 → 等 done → 立刻重开」，异步清理会与新会话抢同一个目录；
	//   - 关闭最后一个面板时进程 200ms 后就退出，异步清理很可能被直接砍掉。
	// 窗口此刻已销毁，重试预算里的几百毫秒用户看不到（正常情况下第一次删除就成功）。
	// 清不掉不阻断关闭：下次打开前还有一次兜底清空（preparePanelProfile），
	// 这里只留一条线索，便于排查「为什么关闭时没清干净」。
	if p.clearOnClose {
		if err := clearPanelProfiles(p.profileTabs(), clearUntilStable); err != nil {
			println("clear panel session on close:", err.Error())
		}
	}

	if p.app != nil {
		p.app.onPanelClosed(p.id, p)
	}
	p.doneOnce.Do(func() { close(p.done) })
}

// profileTabs 返回面板各标签的 ID 与名称，供清理会话数据使用。
// tabState 里的这两个字段在窗口销毁后依然保留（dispose 只清 COM 资源），因此随时可调。
func (p *panelWindow) profileTabs() []PanelTab {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]PanelTab, 0, len(p.tabs))
	for i := range p.tabs {
		out = append(out, PanelTab{ID: p.tabs[i].tabID, Name: p.tabs[i].name})
	}
	return out
}

// ─── 原生标签栏（Win32 子窗口，GDI 绘制） ─────────────────────────────────────

// panelTRACKMOUSEEVENT 与 Windows TRACKMOUSEEVENT 内存布局一致（x64）。
type panelTRACKMOUSEEVENT struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   uintptr
	DwHoverTime uint32
	_           uint32
}

// panelTabBarInitGDI 创建标签栏与外壳共享的字体（进程级，一次即可）。
//
// 颜色不再是「一套写死的画刷」——面板外壳跟随明暗主题，取色一律走 panelChromeFor
// （见 theme_panel_windows.go）。这里顺带把两套配色与画刷建好。
func panelTabBarInitGDI() {
	panelChromeInit()
	panelTabBarGDIOnce.Do(func() {
		face, _ := windows.UTF16PtrFromString("Microsoft YaHei UI")
		fontHeight := -13
		font, _, _ := panelCreateFontW.Call(
			uintptr(fontHeight), // 字高（负值表示字符高度）
			0, 0, 0,
			600, // FW_SEMIBOLD
			0, 0, 0,
			1, // DEFAULT_CHARSET
			0, // OUT_DEFAULT_PRECIS
			0, // CLIP_DEFAULT_PRECIS
			5, // CLEARTYPE_QUALITY
			0, // DEFAULT_PITCH
			uintptr(unsafe.Pointer(face)),
		)
		if font == 0 {
			// 兜底：默认 GUI 字体。
			font, _, _ = panelGetStockObject.Call(17) // DEFAULT_GUI_FONT
		}
		panelTabBarFont = font
	})
}

// registerPanelTabBarClass 注册标签栏子窗口类（含背景刷，减少重绘闪烁）。
func registerPanelTabBarClass() error {
	panelTabBarClassOnce.Do(func() {
		instance, err := panelModuleInstance()
		if err != nil {
			panelTabBarClassError = err
			return
		}

		panelTabBarInitGDI()
		windowClass := panelWNDCLASSEX{
			Size:       uint32(unsafe.Sizeof(panelWNDCLASSEX{})),
			WndProc:    panelTabBarProcedure,
			Instance:   instance,
			Background: panelDefaultChrome().brBg,
			ClassName:  panelTabBarClassName,
		}
		atom, _, registerErr := panelRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
		if atom == 0 && registerErr != syscall.Errno(1410) {
			panelTabBarClassError = fmt.Errorf("register tab bar window class: %w", registerErr)
		}
	})
	return panelTabBarClassError
}

// createPanelTabBar 在面板客户区顶部创建标签栏子窗口。
func createPanelTabBar(parent uintptr) (uintptr, error) {
	instance, err := panelModuleInstance()
	if err != nil {
		return 0, err
	}

	var client panelRECT
	panelGetClientRect.Call(parent, uintptr(unsafe.Pointer(&client)))

	hwnd, _, createErr := panelCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(panelTabBarClassName)),
		0,
		win32WSChild|win32WSVisible,
		0,
		titleBarHeight, // 位于自绘标题栏之下（精确内缩摆位由 panelWindow.resize 完成）
		uintptr(client.Right-client.Left),
		tabBarHeight,
		parent,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create panel tab bar: %w", createErr)
	}
	return hwnd, nil
}

// panelTabBarProc 标签栏窗口过程：绘制、悬停高亮、点击切换标签、右端配色切换。
func panelTabBarProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	panel := getTabBarWindow(hwnd)
	if panel == nil {
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	}

	switch message {
	case win32WMPaint:
		panel.paintTabBar(hwnd)
		return 0
	case win32WMEraseBkgnd:
		// 窗口类的背景刷是注册时定死的，跟不上主题；自己擦，免得浅色主题下
		// 每次重绘先闪一记深色底。
		panel.eraseBackground(hwnd, wParam)
		return 1
	case win32WMNCHitTest:
		return panel.tabBarHitTest(hwnd, lParam)
	case win32WMMouseMove:
		x := int(int16(lParam & 0xFFFF))
		y := int(int16((lParam >> 16) & 0xFFFF))
		panel.updateTabHover(hwnd, x, y)
		return 0
	case win32WMMouseLeave:
		panel.mu.Lock()
		panel.hoverTab = -1
		panel.hoverThemeBtn = false
		panel.hoverTracking = false
		panel.mu.Unlock()
		panelInvalidateRect.Call(hwnd, 0, 0)
		return 0
	case win32WMLButtonDown:
		x := int(int16(lParam & 0xFFFF))
		y := int(int16((lParam >> 16) & 0xFFFF))
		panel.pressTabBarButton(hwnd, x, y)
		return 0
	case win32WMLButtonUp:
		x := int(int16(lParam & 0xFFFF))
		y := int(int16((lParam >> 16) & 0xFFFF))
		panel.releaseTabBarButton(hwnd, x, y)
		return 0
	}
	result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
	return result
}

// pressTabBarButton 处理标签栏上的按下：配色按钮记录下来（等抬起才算数），
// 标签则立即切换（与改动前一致，标签不需要按下-抬起语义）。
func (p *panelWindow) pressTabBarButton(hwnd uintptr, x, y int) {
	width := tabBarWidth(hwnd)
	if themeButtonHit(width, x, y) {
		p.mu.Lock()
		p.pressThemeBtn = true
		p.mu.Unlock()
		// 捕获鼠标：按下后拖出按钮再松开应当取消，不能落在别处。
		panelSetCapture.Call(hwnd)
		panelInvalidateRect.Call(hwnd, 0, 0)
		return
	}

	idx := p.tabIndexAt(width, x)
	if idx < 0 {
		return
	}
	p.mu.Lock()
	var tabID string
	if idx < len(p.tabs) {
		tabID = p.tabs[idx].tabID
	}
	p.mu.Unlock()
	if tabID != "" {
		p.switchTab(tabID)
	}
}

// releaseTabBarButton 处理抬起：只有「按下与抬起都在配色按钮内」才算一次点击。
func (p *panelWindow) releaseTabBarButton(hwnd uintptr, x, y int) {
	p.mu.Lock()
	pressed := p.pressThemeBtn
	p.pressThemeBtn = false
	closed := p.closed
	app := p.app
	theme := p.theme
	if theme == "" {
		theme = ThemeDark
	}
	p.mu.Unlock()

	if captured, _, _ := panelGetCapture.Call(); captured == hwnd {
		panelReleaseCapture.Call()
	}
	if !pressed {
		return
	}
	panelInvalidateRect.Call(hwnd, 0, 0)

	if closed || app == nil || !themeButtonHit(tabBarWidth(hwnd), x, y) {
		return
	}
	// 切到另一侧并固定下来；实际的配色推送由 App 统一做（所有面板 + 管理窗口一起换）。
	if err := app.togglePanelTheme(theme); err != nil {
		return
	}
}

// eraseBackground 用当前配色擦除客户区。
//
// 三个窗口（面板本体、标题栏、标签栏）都自己擦：窗口类背景刷是注册时定死的，
// 主题一切它就过时了，落给 DefWindowProc 会在浅色主题下闪深色。
func (p *panelWindow) eraseBackground(hwnd uintptr, hdc uintptr) {
	var client panelRECT
	if ok, _, _ := panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok == 0 {
		return
	}
	chrome := p.chrome()
	panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&client)), chrome.brBg)
}

// tabBarHitTest 标签栏的命中分工（与标题栏 titleBarHitTest 同款）：
//   - 配色按钮 → HTCLIENT，自己收点击（它不能被当成拖拽区，否则点不动）；
//   - 标签区（含标签之间的间隙）→ HTCLIENT，自己收点击；
//   - 标签右侧那片没有标签的空白 → HTTRANSPARENT，穿透给父窗口判 HTCAPTION，
//     于是「拖住标签后面的空白横向/斜着甩」就是原生拖窗口。
//
// 判据必须与 tabIndexAt / themeButtonHit 同源：点击用什么几何算命中，这里就用什么，
// 否则会出现「点得到但拖不动」或者「看着是空白却按下了某个标签」。
func (p *panelWindow) tabBarHitTest(hwnd uintptr, lParam uintptr) uintptr {
	var rect panelRECT
	if ok, _, _ := panelGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return win32HTClient
	}
	// 标签栏是 WS_CHILD（无 WS_BORDER/滚动条），窗口矩形宽度 == 客户区宽度，
	// 所以这里可以直接用窗口矩形；paintTabBar / pressTabBarButton 用的是客户区宽度，
	// 两者等价（tabBarWidth 也取客户区）。
	width := int(rect.Right - rect.Left)
	x := int(int32(int16(lParam&0xFFFF))) - int(rect.Left)
	y := int(int32(int16((lParam>>16)&0xFFFF))) - int(rect.Top)
	if themeButtonHit(width, x, y) {
		return win32HTClient
	}
	if p.tabIndexAt(width, x) >= 0 {
		return win32HTClient
	}
	return win32HTTransparent
}

// panelTabBarThemeRect 返回配色切换按钮在标签栏中的矩形。
// 贴右缘内边距，垂直居中 —— 正好落在标题栏「关闭」按钮的正下方。
func panelTabBarThemeRect(width int32) panelRECT {
	top := int32((tabBarHeight - tabBarThemeBtnHeight) / 2)
	return panelRECT{
		Left:   width - tabBarPadding - tabBarThemeBtnWidth,
		Top:    top,
		Right:  width - tabBarPadding,
		Bottom: top + tabBarThemeBtnHeight,
	}
}

// themeButtonHit 报告坐标是否落在配色按钮上。
func themeButtonHit(width, x, y int) bool {
	if width <= 0 {
		return false
	}
	return rectContains(panelTabBarThemeRect(int32(width)), int32(x), int32(y))
}

// panelTabBarTabLimit 返回标签栏在不压到配色按钮的前提下最多能画几个标签。
//
// **绘制与命中测试共用它**，两边算得不一样就会出现「看得见点不到」或「点得到看不见」。
// 标签暂时不做滚动/溢出折叠：超出上限的标签既不画也不可点（面板的标签数量按设计
// 就是个位数，为它加一套滚动机制不划算）。
func panelTabBarTabLimit(width int32) int {
	avail := int(panelTabBarThemeRect(width).Left) - tabBarPadding - tabBarTabWidth
	if avail < 0 {
		return 0
	}
	return avail/(tabBarTabWidth+tabBarTabGap) + 1
}

// tabIndexAt 把标签栏客户区 X 坐标换算为标签索引，未命中返回 -1。
//
// x < tabBarPadding 必须先挡掉：Go 的整数除法向零截断，左侧那 8px 内边距会被
// 算成「-8/164 = 0」而误判成 0 号标签（点击切到 0 号、命中测试也不肯放行拖拽）。
func (p *panelWindow) tabIndexAt(width, x int) int {
	if x < tabBarPadding {
		return -1
	}
	p.mu.Lock()
	count := len(p.tabs)
	p.mu.Unlock()

	if limit := panelTabBarTabLimit(int32(width)); count > limit {
		count = limit
	}
	idx := (x - tabBarPadding) / (tabBarTabWidth + tabBarTabGap)
	if idx < 0 || idx >= count {
		return -1
	}
	return idx
}

// updateTabHover 更新悬停高亮并在首次进入时注册 WM_MOUSELEAVE 跟踪。
func (p *panelWindow) updateTabHover(hwnd uintptr, x, y int) {
	width := tabBarWidth(hwnd)
	idx := p.tabIndexAt(width, x)
	onThemeBtn := themeButtonHit(width, x, y)

	p.mu.Lock()
	changed := idx != p.hoverTab || onThemeBtn != p.hoverThemeBtn
	needTrack := !p.hoverTracking
	p.hoverTracking = true
	p.hoverTab = idx
	p.hoverThemeBtn = onThemeBtn
	p.mu.Unlock()

	if needTrack {
		var tme panelTRACKMOUSEEVENT
		tme.CbSize = uint32(unsafe.Sizeof(tme))
		tme.DwFlags = win32TMELeave
		tme.HwndTrack = hwnd
		panelTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
	}
	if changed {
		panelInvalidateRect.Call(hwnd, 0, 0)
	}
}

// tabBarWidth 返回标签栏客户区宽度（标签栏是无边框子窗口，宽度即窗口宽度）。
func tabBarWidth(hwnd uintptr) int {
	var client panelRECT
	if ok, _, _ := panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok == 0 {
		return 0
	}
	return int(client.Right - client.Left)
}

// paintTabBar 绘制标签栏背景与全部标签（活动页浅色、悬停中灰）。
func (p *panelWindow) paintTabBar(hwnd uintptr) {
	panelTabBarInitGDI()

	hdc, _, _ := panelGetDC.Call(hwnd)
	if hdc == 0 {
		return
	}
	defer panelReleaseDC.Call(hwnd, hdc)

	p.mu.Lock()
	activeID := p.activeTabID
	hoverIdx := p.hoverTab
	hoverTheme := p.hoverThemeBtn
	pressTheme := p.pressThemeBtn
	tabs := make([]tabState, len(p.tabs))
	copy(tabs, p.tabs)
	p.mu.Unlock()
	chrome := p.chrome()

	var client panelRECT
	panelGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client)))

	bgRect := panelRECT{Right: client.Right, Bottom: client.Bottom}
	panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&bgRect)), chrome.brBg)

	// 标签只画到配色按钮之前（tabIndexAt 用同一个上限，见 panelTabBarTabLimit）。
	visible := panelTabBarTabLimit(client.Right)
	if visible > len(tabs) {
		visible = len(tabs)
	}
	for i := 0; i < visible; i++ {
		left := int32(tabBarPadding + i*(tabBarTabWidth+tabBarTabGap))
		rect := panelRECT{
			Left:   left,
			Top:    4,
			Right:  left + tabBarTabWidth,
			Bottom: tabBarHeight - 4,
		}

		isActive := tabs[i].tabID == activeID
		brush := uintptr(0)
		textColor := uintptr(panelColorRef(chrome.text))
		switch {
		case isActive:
			brush = chrome.brActiveTab
			textColor = uintptr(panelColorRef(chrome.activeTabText))
		case i == hoverIdx:
			brush = chrome.brHover
		}
		if brush != 0 {
			panelFillRect.Call(hdc, uintptr(unsafe.Pointer(&rect)), brush)
		}

		name, err := windows.UTF16PtrFromString(tabs[i].name)
		if err != nil {
			continue
		}
		panelSetBkMode.Call(hdc, 1) // TRANSPARENT
		panelSetTextColor.Call(hdc, textColor)
		oldFont, _, _ := panelSelectObject.Call(hdc, panelTabBarFont)
		panelDrawTextW.Call(
			hdc,
			uintptr(unsafe.Pointer(name)),
			^uintptr(0), // -1
			uintptr(unsafe.Pointer(&rect)),
			win32DTCenter|win32DTVCenter|win32DTSingleLine|win32DTNoPrefix|win32DTEndEllipsis,
		)
		panelSelectObject.Call(hdc, oldFont)
	}

	// 右端配色切换按钮：字形表达当前状态（太阳 = 浅色，月亮 = 深色）。
	glyph := tbGlyphThemeLight
	if p.panelTheme() == ThemeDark {
		glyph = tbGlyphThemeDark
	}
	paintChromeIconButton(hdc, panelTabBarThemeRect(client.Right), glyph, chrome, hoverTheme, pressTheme)

	panelValidateRect.Call(hwnd, 0)
}

func addTabBarWindow(hwnd uintptr, panel *panelWindow) {
	activePanelWindowsMutex.Lock()
	activeTabBarWindows[hwnd] = panel
	activePanelWindowsMutex.Unlock()
}

func getTabBarWindow(hwnd uintptr) *panelWindow {
	activePanelWindowsMutex.RLock()
	panel := activeTabBarWindows[hwnd]
	activePanelWindowsMutex.RUnlock()
	return panel
}

func removeTabBarWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	activePanelWindowsMutex.Lock()
	delete(activeTabBarWindows, hwnd)
	activePanelWindowsMutex.Unlock()
}

// ─── COM 方法封装 ──────────────────────────────────────────────────────────────

func (e *panelEnvironment) createController(hwnd uintptr, handler *panelControllerHandler) error {
	hr, _, _ := e.Vtbl.CreateController.Call(
		uintptr(unsafe.Pointer(e)),
		hwnd,
		uintptr(unsafe.Pointer(handler)),
	)
	return panelHRESULTError("create WebView2 controller", hr)
}

func (c *panelController) putBounds(bounds panelRECT) error {
	hr, _, _ := c.Vtbl.PutBounds.Call(
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&bounds)),
	)
	return panelHRESULTError("resize WebView2 controller", hr)
}

func (c *panelController) putIsVisible(visible uintptr) error {
	hr, _, _ := c.Vtbl.PutIsVisible.Call(
		uintptr(unsafe.Pointer(c)),
		visible,
	)
	return panelHRESULTError("set WebView2 visibility", hr)
}

func (c *panelController) close() error {
	hr, _, _ := c.Vtbl.Close.Call(uintptr(unsafe.Pointer(c)))
	return panelHRESULTError("close WebView2 controller", hr)
}

func (c *panelController) getWebView() (*panelWebView, error) {
	var webview *panelWebView
	hr, _, _ := c.Vtbl.GetCoreWebView2.Call(
		uintptr(unsafe.Pointer(c)),
		uintptr(unsafe.Pointer(&webview)),
	)
	if err := panelHRESULTError("get WebView2 instance", hr); err != nil {
		return nil, err
	}
	return webview, nil
}

func (w *panelWebView) navigate(url string) error {
	uri, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}

	hr, _, _ := w.Vtbl.Navigate.Call(
		uintptr(unsafe.Pointer(w)),
		uintptr(unsafe.Pointer(uri)),
	)
	return panelHRESULTError("navigate to panel", hr)
}

// ─── 辅助函数 ──────────────────────────────────────────────────────────────────

func panelHRESULTError(operation string, hr uintptr) error {
	if int32(hr) >= 0 {
		return nil
	}
	return fmt.Errorf("%s: %#x", operation, uint32(hr))
}

func panelCOMAddRef[T any](object *T) {
	if object == nil {
		return
	}

	unknown := (*panelIUnknown)(unsafe.Pointer(object))
	unknown.Vtbl.AddRef.Call(uintptr(unsafe.Pointer(object)))
}

func panelCOMRelease[T any](object *T) {
	if object == nil {
		return
	}

	unknown := (*panelIUnknown)(unsafe.Pointer(object))
	unknown.Vtbl.Release.Call(uintptr(unsafe.Pointer(object)))
}

func registerPanelWindowClass() error {
	panelWindowClassOnce.Do(func() {
		instance, err := panelModuleInstance()
		if err != nil {
			panelWindowClassError = err
			return
		}

		// 类背景刷负责填充「缩放边框让出来的那一圈」与重绘闪烁区。
		panelTabBarInitGDI()

		cursor, _, _ := panelLoadCursor.Call(0, win32IDCArrow)
		windowClass := panelWNDCLASSEX{
			Size:       uint32(unsafe.Sizeof(panelWNDCLASSEX{})),
			WndProc:    panelWindowProcedure,
			Instance:   instance,
			Cursor:     cursor,
			Background: panelDefaultChrome().brBg,
			ClassName:  panelWindowClassName,
		}
		atom, _, registerErr := panelRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
		if atom == 0 && registerErr != syscall.Errno(1410) {
			panelWindowClassError = fmt.Errorf("register panel window class: %w", registerErr)
		}
	})
	return panelWindowClassError
}

func panelModuleInstance() (uintptr, error) {
	instance, _, err := panelGetModuleHandle.Call(0)
	if instance == 0 {
		return 0, fmt.Errorf("get executable module handle: %w", err)
	}
	return instance, nil
}

func createPanelWindow(title string, rect panelRECT) (uintptr, error) {
	instance, err := panelModuleInstance()
	if err != nil {
		return 0, err
	}

	titlePtr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}

	x, y := int(rect.Left), int(rect.Top)
	width, height := int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
	if width <= 0 || height <= 0 {
		x, y = int(win32CWUseDefault), int(win32CWUseDefault)
		width, height = defaultPanelWindowWidth, defaultPanelWindowHeight
	}

	hwnd, _, createErr := panelCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(panelWindowClassName)),
		uintptr(unsafe.Pointer(titlePtr)),
		win32WSOverlappedWindow,
		uintptr(x),
		uintptr(y),
		uintptr(width),
		uintptr(height),
		0,
		0,
		instance,
		0,
	)
	if hwnd == 0 {
		return 0, fmt.Errorf("create panel window: %w", createErr)
	}
	return hwnd, nil
}

func panelWindowProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	panel := getPanelWindow(hwnd)
	switch message {
	case win32WMNCCalcSize:
		// 无边框：客户区覆盖整个窗口矩形，系统标题栏与边框由此消失。
		// 缩放边框/标题区拖拽由 WM_NCHITTEST 手工判定（见 panelFrameHitTest）。
		//
		// **两条分支都必须管**，只处理 wParam=TRUE 是错的：
		//   - wParam=TRUE：lParam 是 NCCALCSIZE_PARAMS，rgrc[0] 进来就是「建议窗口矩形」，
		//     不改直接返回 0 即「客户区 = 窗口矩形」；
		//   - wParam=FALSE：lParam 是 RECT，文档规定「进：建议窗口矩形；出：客户区屏幕坐标」，
		//     必须自己把它写成窗口矩形，否则落给 DefWindowProc 会算出标准边框。
		// 实测：窗口创建期走的正是 wParam=FALSE 这一支 —— 只判 TRUE 的写法会让
		// 系统标题栏原地保留（客户区 1104x721 vs 窗口 1120x760 的铁证）。
		if wParam == 0 {
			var window panelRECT
			if ok, _, _ := panelGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&window))); ok != 0 {
				*(*panelRECT)(uintptrToPtr(lParam)) = window
			}
		}
		return 0
	case win32WMGetMinMaxInfo:
		// 客户区=窗口矩形之后，最大化会连任务栏一起盖住，这里把最大化矩形约束回工作区。
		panelAdjustMaximizedBounds(hwnd, lParam)
		return 0
	case win32WMNCHitTest:
		// 与 panel 对象无关（创建早期也会收到），只按几何判定。
		return panelFrameHitTest(hwnd, lParam)
	case win32WMSIZE:
		if panel != nil {
			panel.resize()
		}
		return 0
	case win32WMEraseBkgnd:
		// 缩放边框让出来的那一圈由本窗口的客户区露出来，颜色要跟主题走
		//（窗口类背景刷是注册时定死的，跟不上换主题）。
		if panel != nil {
			panel.eraseBackground(hwnd, wParam)
			return 1
		}
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	case win32WMSettingChange:
		// Windows 的深浅色偏好变了：auto 模式下要跟上。固定 light/dark 时
		// 重算出来还是同一个值，applyPendingTheme 会因为「没变」直接返回。
		// 顶层窗口本来就会收到这条广播，不需要另找人转发。
		if panel != nil && panel.app != nil && isImmersiveColorSet(lParam) {
			panel.requestTheme(panel.app.resolveTheme())
		}
		return 0
	case win32WMSetTheme:
		// 别的线程请求换配色，回 UI 线程落地（见 theme_panel_windows.go）。
		if panel != nil {
			panel.applyPendingTheme()
		}
		return 0
	case win32WMEXITSIZEMOVE:
		if panel != nil {
			panel.recordBounds()
		}
		return 0
	case win32WMCLOSE:
		// 交互式关闭（标题栏 X）：程序正在退出时直接放行，否则按设置询问/执行。
		if panel != nil && panel.app != nil && !panel.app.isQuitting() {
			panel.handleInteractiveClose()
			return 0
		}
		destroyPanelWindow(panel, hwnd)
		return 0
	case win32WMDirectClose:
		destroyPanelWindow(panel, hwnd)
		return 0
	case win32WMIconsReady:
		// 图标在后台线程解析完了，回这里安装（WM_SETICON 只能在窗口线程发）。
		if panel != nil {
			panel.applyPendingIcons()
		}
		return 0
	case win32WMRemoveTab:
		// 管理面板那边要删某个标签：销毁它的 WebView2、从标签栏摘掉（见 removeTab）。
		if panel != nil {
			panel.consumeTabRemoves()
		}
		return 0
	case win32WMDESTROY:
		if panel != nil {
			panel.dispose()
		}
		panelPostQuitMessage.Call(0)
		return 0
	default:
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, lParam)
		return result
	}
}

func addPanelWindow(hwnd uintptr, panel *panelWindow) {
	activePanelWindowsMutex.Lock()
	activePanelWindows[hwnd] = panel
	activePanelWindowsMutex.Unlock()
}

func getPanelWindow(hwnd uintptr) *panelWindow {
	activePanelWindowsMutex.RLock()
	panel := activePanelWindows[hwnd]
	activePanelWindowsMutex.RUnlock()
	return panel
}

func removePanelWindow(hwnd uintptr) {
	if hwnd == 0 {
		return
	}
	activePanelWindowsMutex.Lock()
	delete(activePanelWindows, hwnd)
	activePanelWindowsMutex.Unlock()
}

// panelAppIcon 从可执行文件提取图标用于托盘。
func panelAppIcon() (uintptr, error) {
	exe, err := os.Executable()
	if err == nil {
		exePath, err := windows.UTF16PtrFromString(exe)
		if err == nil {
			var large, small uintptr
			panelExtractIconExW.Call(
				uintptr(unsafe.Pointer(exePath)),
				0,
				uintptr(unsafe.Pointer(&large)),
				uintptr(unsafe.Pointer(&small)),
				1,
			)
			if small != 0 {
				return small, nil
			}
			if large != 0 {
				return large, nil
			}
		}
	}

	icon, _, _ := panelLoadIcon.Call(0, win32IDIApplication)
	if icon != 0 {
		return icon, nil
	}
	return 0, errors.New("no icon available")
}
