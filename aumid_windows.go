package main

import (
	"syscall"
	"unsafe"
)

// panelAppUserModelID 是本程序的显式 AppUserModelID（AUMID）。
//
// 为什么必须显式声明：任务栏按钮的图标**不是只看窗口图标**。Explorer 会拿窗口的
// AUMID 去匹配「已知应用」条目（快捷方式、固定项），匹配上就一律用那个条目的图标，
// 窗口自己的 WM_SETICON 图标被无视。
//
// 而本程序**自己创建的桌面快捷方式**恰好就是这样一个条目：它的 AUMID 等于 exe 路径，
// 与不声明 AUMID 时进程的隐式 AUMID（同样是 exe 路径）完全一致 —— 于是每个面板窗口
// 的任务栏按钮都被快捷方式劫持，永远显示 exe 内嵌的图标，站点图标白取了。
// （下文实录里那个「W 图标」是当时 exe 内嵌的 Wails 默认图标；2026-10-01 已换成
// PanelDock 自己的 P 图标 —— 换的只是图样，「任务栏显示 exe 图标而非站点图标」这个
// 判断依据没变，把 W 读成「exe 内嵌图标」即可。）
//
// 实测证据（2026-09-30，同一台机器、同一个面板、同一份 data）：
//   - build\bin\PD_probe.exe（把 PanelDock.exe 复制改名，没有任何快捷方式指向它）
//     → 任务栏显示站点图标（粉色 zashboard logo）；
//   - build\bin\PanelDock.exe（桌面有三个快捷方式指向它）
//     → 任务栏显示 exe 默认 W 图标，而 WM_GETICON(ICON_BIG) 拿到的确实是站点图标；
//   - 补一刀对照：刚给 PD_probe.exe 造了一个快捷方式，它的任务栏图标**立刻**变成 W。
//
// 声明一个不会被任何快捷方式携带的 AUMID 之后，窗口图标才由 WM_SETICON 说了算。
//
// 已知边界：用户把程序**固定到任务栏**之后，Windows 用固定项（快捷方式）的图标，
// 而这仍然只可能是 exe 的图标 —— 固定项那一个图标是静态的，与站点无关。
const panelAppUserModelID = "PanelDock.Desktop.IsolatedPanels"

// applyAppUserModelID 在启动最早处调用一次。失败不致命：退回隐式 AUMID
// （即旧行为：任务栏图标会被快捷方式劫持），程序照常运行。
func applyAppUserModelID() {
	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	if err := proc.Find(); err != nil {
		return
	}
	id, err := syscall.UTF16PtrFromString(panelAppUserModelID)
	if err != nil {
		return
	}
	_, _, _ = proc.Call(uintptr(unsafe.Pointer(id)))
}
