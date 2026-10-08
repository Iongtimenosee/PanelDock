package main

import (
	"syscall"
	"unsafe"
)

// panelAppUserModelID 是本程序的显式 AppUserModelID（AUMID）。
//
// 为什么必须显式声明：Explorer 会拿窗口的 AUMID 去匹配「已知应用」条目（快捷方式、固定项），
// 匹配上就一律用那个条目的图标，窗口自己的 WM_SETICON 被无视。而本程序**自己创建的桌面
// 快捷方式**恰好就是这样一个条目 —— 它的 AUMID 等于 exe 路径，与不声明 AUMID 时的隐式
// AUMID 完全相同，于是每个面板窗口的任务栏按钮都被劫持成 exe 内嵌图标。
//
// 判别实验（可复现）：把 exe 复制改名成没人指向的名字 → 任务栏显示站点图标；给它造一个
// 快捷方式 → 立刻变回 exe 图标。声明一个不会被任何快捷方式携带的 AUMID 之后，
// 窗口图标才由 WM_SETICON 说了算。
//
// 已知边界：固定到任务栏后 Windows 用固定项的图标，那是静态引用，与站点无关。
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
