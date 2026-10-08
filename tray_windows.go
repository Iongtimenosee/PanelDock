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
	// 菜单里显示的分组名上限（按字符截断，避免菜单过宽）。
	trayMenuNameLimit = 16

	// 托盘菜单项 ID。
	// 「已隐藏的窗口」是动态项：每个藏在托盘里的分组一个，ID 从 trayMenuRestoreBase
	// 起按本次菜单的罗列顺序递增；固定项用 1..3，两段不冲突。
	// trayMenuCloseHiddenBase 同理，是「彻底关闭」子菜单里同一批窗口的 ID 段。
	// 两段之间留出 900 个空位：面板多到 100 个以上时，上面那段也不会顶到这一段。
	trayMenuRestoreBase     = 100
	trayMenuCloseHiddenBase = 1000

	trayMenuOpenManager = 1
	trayMenuQuit        = 2
	// trayMenuNoop 是「没有已隐藏的窗口」占位项：置灰，点了什么都不做。
	// 不能拿 0 当 ID —— TPM_RETURNCMD 用 0 表示「菜单被取消」，撞上就分不清点没点。
	trayMenuNoop = 3

	// 菜单标志。
	win32MFGrayed = 0x00000001
	// win32MFPopup 追加一个带子菜单的项：此时第三个参数不是命令 ID 而是子菜单句柄。
	win32MFPopup = 0x00000010
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
		app.trayActivateLastHidden()
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

// trayActivateActivePanel 双击托盘图标的最后兜底：既没有双击目标、队列也是空的时，
// 激活最近打开/激活的面板；连面板都没有就呼出管理界面。
func (a *App) trayActivateActivePanel() {
	if p := a.trayActivePanel(); p != nil {
		p.activate()
		return
	}
	a.ShowMainWindow()
}

// trayActivePanel 返回最近打开/激活、且当前仍打开着的面板；全都没有时返回 nil。
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

// lastTrayHidden 返回队列末位——最近一次「关闭到托盘」的那个分组窗口；没有则 nil。
func (a *App) lastTrayHidden() *panelWindow {
	hidden := a.trayHiddenPanels()
	if len(hidden) == 0 {
		return nil
	}
	return hidden[len(hidden)-1]
}

// trayActivateLastHidden 双击托盘图标：固定作用在同一个窗口上，在「拿回来 ⇄ 收回去」
// 之间来回翻转，于是双击是一个纯粹的开关 —— 不必先把窗口放到桌面上点 X 才能再双击收起。
//
// 目标优先取「上次双击碰过的那个」（还活着就用它），否则退回队列末位（最近一次关闭到托盘的）；
// 两者都没有时才走兜底：激活活动面板，连面板都没有就呼出管理界面。
func (a *App) trayActivateLastHidden() {
	if p := a.trayToggleTarget(); p != nil {
		a.togglePanelFromTray(p)
		return
	}
	if p := a.lastTrayHidden(); p != nil {
		a.togglePanelFromTray(p)
		return
	}
	a.trayActivateActivePanel()
}

// togglePanelFromTray 翻转一个窗口的托盘状态，并把它记成下次双击的目标。
// 翻转而不是「一律恢复」，是让双击能对同一窗口反复起作用的唯一办法 ——
// 队列里只有藏着的窗口，恢复过的目标一旦出队就再也对不上号了。
func (a *App) togglePanelFromTray(p *panelWindow) {
	a.markTrayToggleTarget(p)
	if p.isHiddenInTray() {
		p.showFromTray()
		return
	}
	p.hideToTray()
}

// trayShowMenu 弹出并处理托盘右键菜单。
// 菜单在 IPC 窗口线程上执行（弹菜单期间 IPC 转发会短暂阻塞，但菜单是模态的，影响可忽略）。
//
// 菜单内容：先罗列所有「关闭到托盘」的分组（一项就是它的名字，点了就恢复），
// 再放「打开管理面板」与「退出」。面板级的显示/隐藏、置顶、关闭都不在这里提供 ——
// 那些状态面板窗口自己有对应控件，塞进托盘只会让「找回窗口」这件主要的事变难找。
func (a *App) trayShowMenu() {
	trayStateMu.Lock()
	hwnd := trayHWND
	trayStateMu.Unlock()
	if hwnd == 0 {
		return
	}

	// 菜单文案每次右键现取：语言切换对托盘菜单即时生效（无需重启或推送）。
	text := a.nativeText().Tray

	// 建菜单时就地快照窗口对象（而不是存 ID 回头再查）：用户看到的是这份列表，
	// 点哪一项都必须作用在它当时看到的那条上，否则中间有人关了窗口就会张冠李戴。
	targets := a.trayHiddenPanels()

	menu, _, _ := panelCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer panelDestroyMenu.Call(menu)

	if len(targets) == 0 {
		// 空列表也要有一行：菜单里什么都沒有会让人以为程序坏了。
		trayAppendItem(menu, win32MFString|win32MFGrayed, trayMenuNoop, text.NoHidden)
	} else {
		for i, p := range targets {
			trayAppendItem(menu, win32MFString, trayMenuRestoreBase+uintptr(i), trayEllipsize(p.name))
		}
		trayAppendCloseSubmenu(menu, targets, text.CloseHidden)
	}

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

	a.trayHandleMenuCommand(targets, command)
}

// trayHandleMenuCommand 把托盘菜单的选中项翻译成对应动作。
// targets 必须与建菜单时用的那份一致：用户点的是他看到的那一条，命令带着的是下标。
//
// 分派从这里单独拆出来，是因为 Win32 菜单的创建与跟踪没法在单测里拦截，而「选了哪一项
// 就该发生什么」才是真正要守住的部分。
//
// command 为 0 表示菜单被取消（TPM_RETURNCMD 的约定）：什么都不做。
func (a *App) trayHandleMenuCommand(targets []*panelWindow, command uintptr) {
	if command == 0 {
		return
	}
	if command >= trayMenuCloseHiddenBase {
		idx := int(command - trayMenuCloseHiddenBase)
		if idx < len(targets) {
			// 真的关掉它：走窗口自己的关闭路径，不再问一次，也不该再藏回托盘。
			// 后续（运行表清理、托盘图标重算、是否收工）都由 onPanelClosed 接手。
			targets[idx].close()
		}
		return
	}

	if command >= trayMenuRestoreBase {
		idx := int(command - trayMenuRestoreBase)
		if idx < len(targets) {
			// 走同一个翻转入口：从菜单拿回来的窗口立刻成为双击的目标，
			// 于是「菜单恢复 → 双击收回」是连贯的一套动作。
			a.togglePanelFromTray(targets[idx])
		}
		return
	}

	switch command {
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

// trayAppendCloseSubmenu 追加「彻底关闭」子菜单，里面罗列同一批已隐藏窗口。
//
// 为什么不直接给每行配一个关闭热区：原生菜单的一行只支持一个命令 ID，要在一行里塞两个
// 可点的位置就得自绘（owner-draw），代价远大于收益。而把「关闭」并列成主菜单的第二组，
// 又会让它在视觉上和「取回」平起平坐 —— 丢一个窗口和取回一个窗口不该是同样容易的事。
// 放进子菜单：多一步抵达，且握这个菜单的人意图本来就明确。
func trayAppendCloseSubmenu(menu uintptr, targets []*panelWindow, label string) {
	submenu, _, _ := panelCreatePopupMenu.Call()
	if submenu == 0 {
		return
	}
	// 子菜单由父菜单级联销毁，这里不能再 DestroyMenu 它（会把整个菜单一起拆掉）。
	for i, p := range targets {
		trayAppendItem(submenu, win32MFString, trayMenuCloseHiddenBase+uintptr(i), trayEllipsize(p.name))
	}

	text, err := windows.UTF16PtrFromString(label)
	if err != nil {
		panelDestroyMenu.Call(submenu)
		return
	}
	panelAppendMenuW.Call(menu, win32MFPopup, submenu, uintptr(unsafe.Pointer(text)))
}

// trayEllipsize 按字符截断过长面板名，保持菜单宽度可控。
func trayEllipsize(name string) string {
	chars := []rune(strings.TrimSpace(name))
	if len(chars) <= trayMenuNameLimit {
		return string(chars)
	}
	return string(chars[:trayMenuNameLimit]) + "…"
}
