//go:build windows

package main

// 应用级托盘图标：由常驻 IPC 窗口承载。
//
// 为什么挂在这个窗口上：图标若挂在面板窗口上，一个面板都没开时托盘里就空空如也，
// 「最小化到托盘」等于让程序彻底失联。由常驻 IPC 窗口承载后，只要进程在运行且用户
// 没有关掉「在系统托盘显示图标」，托盘里就一定有入口 —— 图标与面板数量无关。
//
// 托盘菜单里的「活动面板」= 最近打开/激活、且当前仍打开着的面板（见 App.trayActivePanel）。

import (
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// 托盘回调消息：shell 通过它把鼠标事件投递给图标的宿主窗口（IPC 窗口）。
	trayCallbackMessage = win32WMAPP + 1
	// 应用级托盘图标的固定 ID：整个进程只有一个图标。
	trayIconUID = 1
	// 菜单里显示的活动面板名上限（按字符截断，避免菜单过宽）。
	trayMenuNameLimit = 16

	// 托盘菜单项 ID。
	trayMenuShowHide    = 1
	trayMenuTopMost     = 2
	trayMenuClose       = 3
	trayMenuOpenManager = 4
	trayMenuQuit        = 5

	// 菜单标志。
	win32MFGrayed = 0x00000001
	// 鼠标消息（托盘回调的 lParam）。
	win32WMLButtonUp = 0x0202
)

var (
	trayStateMu sync.Mutex
	trayHWND    uintptr // 承载托盘图标的窗口（IPC 窗口）
	trayAdded   bool
	trayIconH   uintptr

	trayAppMu sync.RWMutex
	trayApp   *App

	// trayTaskbarCreatedMessage 是 Explorer 重启时广播的注册消息。
	// 收到它必须重新注册图标，否则任务栏重启后托盘图标会消失。
	trayTaskbarCreatedMessage = registerTrayTaskbarCreatedMessage()
)

func registerTrayTaskbarCreatedMessage() uint32 {
	register := panelUser32.NewProc("RegisterWindowMessageW")
	name, err := windows.UTF16PtrFromString("TaskbarCreated")
	if err != nil {
		return 0
	}
	message, _, _ := register.Call(uintptr(unsafe.Pointer(name)))
	return uint32(message)
}

// trayAttach 把托盘图标绑定到承载窗口（IPC 窗口）并登记应用对象。
// 由 App.startup 在 IPC 窗口创建成功后调用。
func trayAttach(app *App, hwnd uintptr) {
	trayAppMu.Lock()
	trayApp = app
	trayAppMu.Unlock()

	trayStateMu.Lock()
	trayHWND = hwnd
	trayStateMu.Unlock()
}

func currentTrayApp() *App {
	trayAppMu.RLock()
	defer trayAppMu.RUnlock()
	return trayApp
}

// traySync 按「是否需要托盘图标」注册或移除图标。可反复调用，是幂等的。
func traySync(need bool) {
	trayStateMu.Lock()
	hwnd := trayHWND
	added := trayAdded
	trayStateMu.Unlock()
	if hwnd == 0 {
		return
	}

	switch {
	case need && !added:
		trayAddIcon(hwnd)

	case !need && added:
		var nid panelNOTIFYICONDATA
		nid.CbSize = uint32(unsafe.Sizeof(nid))
		nid.HWnd = hwnd
		nid.UID = trayIconUID
		panelShellNotifyIcon.Call(win32NIMDelete, uintptr(unsafe.Pointer(&nid)))

		trayStateMu.Lock()
		trayAdded = false
		icon := trayIconH
		trayIconH = 0
		trayStateMu.Unlock()
		if icon != 0 {
			panelDestroyIcon.Call(icon)
		}
	}
}

// trayAddIcon 在承载窗口上注册托盘图标。调用前应已确认当前没有图标。
func trayAddIcon(hwnd uintptr) {
	icon, err := panelAppIcon()
	if err != nil {
		return
	}

	// 悬浮提示按当前语言取（词典见 native_text_windows.go）；取不到 App 时用源语言兜底。
	tipText := nativeUITexts[nativeTextDefault].Tray.Tip
	if app := currentTrayApp(); app != nil {
		tipText = app.nativeText().Tray.Tip
	}

	var nid panelNOTIFYICONDATA
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = trayIconUID
	nid.UFlags = win32NIFMessage | win32NIFIcon | win32NIFTIP
	nid.UCallbackMessage = trayCallbackMessage
	nid.HIcon = icon
	tip, err := windows.UTF16FromString(tipText)
	if err != nil {
		panelDestroyIcon.Call(icon)
		return
	}
	copy(nid.SzTip[:], tip)

	if result, _, _ := panelShellNotifyIcon.Call(win32NIMAdd, uintptr(unsafe.Pointer(&nid))); result == 0 {
		panelDestroyIcon.Call(icon)
		return
	}

	trayStateMu.Lock()
	trayAdded = true
	trayIconH = icon
	trayStateMu.Unlock()
}

// trayReAdd 在 Explorer 重启后强制重新注册图标。
func trayReAdd() {
	trayStateMu.Lock()
	// 旧图标随任务栏一起消失了，句柄作废，重新提取即可。
	icon := trayIconH
	trayAdded = false
	trayIconH = 0
	trayStateMu.Unlock()
	if icon != 0 {
		panelDestroyIcon.Call(icon)
	}

	app := currentTrayApp()
	if app == nil {
		return
	}
	traySync(app.trayNeeded())
}

// trayHandleEvent 处理托盘回调消息（在 IPC 窗口的消息线程上执行）。
func trayHandleEvent(wParam, lParam uintptr) {
	if uint32(wParam) != trayIconUID {
		return
	}
	app := currentTrayApp()
	if app == nil {
		return
	}

	switch uint32(lParam) {
	case win32WMLButtonDblClk:
		app.trayActivateActivePanel()
	case win32WMRButtonUp:
		app.trayShowMenu()
	}
}

// ─── App 侧：托盘需求判定与菜单 ────────────────────────────────────────────────

// trayNeeded 判断当前是否需要托盘图标。
// 三种情形需要：全局开启托盘图标 / 管理窗口正藏在托盘 / 某个面板窗口正藏在托盘。
// 例外：程序正在退出时一律不需要（此时不再维护图标）。
func (a *App) trayNeeded() bool {
	a.mu.Lock()
	quitting := a.quitting
	resident := a.resident
	a.mu.Unlock()
	if quitting {
		return false
	}

	if a.config.settings().ShowTrayIcon || resident {
		return true
	}

	for _, p := range a.panelWindows() {
		if p.isHiddenInTray() {
			return true
		}
	}
	return false
}

// syncTray 重新计算并应用托盘图标状态。
func (a *App) syncTray() {
	traySync(a.trayNeeded())
}

// trayActivePanel 返回托盘菜单要操作的面板：优先最近打开/激活的面板，
// 若它已关闭则退化为配置顺序里第一个仍打开的面板；全都没有时返回 nil（菜单项置灰）。
func (a *App) trayActivePanel() *panelWindow {
	a.mu.Lock()
	if p := a.panels[a.activePanelID]; p != nil {
		a.mu.Unlock()
		return p
	}
	a.mu.Unlock()

	for _, cfg := range a.config.list() {
		a.mu.Lock()
		p := a.panels[cfg.ID]
		if p != nil {
			a.activePanelID = cfg.ID
		}
		a.mu.Unlock()
		if p != nil {
			return p
		}
	}
	return nil
}

// trayActivateActivePanel 双击托盘图标时激活活动面板；没有面板则呼出管理界面。
func (a *App) trayActivateActivePanel() {
	if p := a.trayActivePanel(); p != nil {
		p.activate()
		return
	}
	a.ShowMainWindow()
}

// trayShowMenu 弹出并处理托盘右键菜单。
// 菜单在 IPC 窗口线程上执行（弹菜单期间 IPC 转发会短暂阻塞，但菜单是模态的，影响可忽略）。
func (a *App) trayShowMenu() {
	trayStateMu.Lock()
	hwnd := trayHWND
	trayStateMu.Unlock()
	if hwnd == 0 {
		return
	}

	active := a.trayActivePanel()
	label := ""
	if active != nil {
		label = strings.ReplaceAll(a.nativeText().Tray.ActiveSuffixFmt, "{name}", trayEllipsize(active.name))
	}

	// 菜单文案每次右键现取：语言切换对托盘菜单即时生效（无需重启或推送）。
	text := a.nativeText().Tray

	menu, _, _ := panelCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer panelDestroyMenu.Call(menu)

	// 面板相关项在没有已打开面板时置灰，避免点了没反应。
	panelFlags := uintptr(win32MFString)
	if active == nil {
		panelFlags |= win32MFGrayed
	}

	trayAppendItem(menu, panelFlags, trayMenuShowHide, text.ShowHide+label)

	topFlags := panelFlags
	if active != nil && active.isAlwaysOnTop() {
		topFlags |= win32MFChecked
	}
	trayAppendItem(menu, topFlags, trayMenuTopMost, text.TopMost+label)

	trayAppendItem(menu, panelFlags, trayMenuClose, text.ClosePanel+label)

	panelAppendMenuW.Call(menu, win32MFSeparator, 0, 0)
	trayAppendItem(menu, win32MFString, trayMenuOpenManager, text.OpenManager)
	panelAppendMenuW.Call(menu, win32MFSeparator, 0, 0)
	trayAppendItem(menu, win32MFString, trayMenuQuit, text.Quit)

	var point panelPOINT
	panelGetCursorPos.Call(uintptr(unsafe.Pointer(&point)))
	panelSetForegroundWindow.Call(hwnd)
	command, _, _ := panelTrackPopupMenu.Call(
		menu,
		win32TPMRETURNCMD,
		uintptr(point.X),
		uintptr(point.Y),
		0,
		hwnd,
		0,
	)

	switch command {
	case trayMenuShowHide, trayMenuTopMost, trayMenuClose:
		// 菜单弹出期间面板可能已被关闭，这里重新取一次。
		target := a.trayActivePanel()
		if target == nil {
			return
		}
		switch command {
		case trayMenuShowHide:
			target.toggleVisibleFromTray()
		case trayMenuTopMost:
			target.setAlwaysOnTop(!target.isAlwaysOnTop())
		case trayMenuClose:
			target.close()
		}
	case trayMenuOpenManager:
		a.ShowMainWindow()
	case trayMenuQuit:
		a.Quit()
	}
}

// trayAppendItem 向菜单追加一个字符串项。
func trayAppendItem(menu uintptr, flags, id uintptr, label string) {
	text, err := windows.UTF16PtrFromString(label)
	if err != nil {
		return
	}
	panelAppendMenuW.Call(menu, flags, id, uintptr(unsafe.Pointer(text)))
}

// trayEllipsize 按字符截断过长面板名，保持菜单宽度可控。
func trayEllipsize(name string) string {
	chars := []rune(strings.TrimSpace(name))
	if len(chars) <= trayMenuNameLimit {
		return string(chars)
	}
	return string(chars[:trayMenuNameLimit]) + "…"
}
