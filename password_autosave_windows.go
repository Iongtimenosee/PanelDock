//go:build windows

package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── 分组的「保存登录密码」（WebView2 原生自动填充里的密码部分）─────────────────
//
// 开关本身是分组级的（PanelConfig.PasswordAutosave，默认开启），这里只负责把它送到
// WebView2：每个标签创建 WebView2、以及用户在卡片上改这个开关时，各调一次。
//
// 密码由 WebView2 自己加密后存在该标签的 profile 目录里（`Login Data` + `Local State`
// 的 DPAPI 主密钥），本工具既不读取也不导出 —— 所以「关闭即清空」的分组一关闭，
// 密码就随 profile 一起没了，这正是用户要的那条语义。
//
// ⚠️ 这个属性只管「保存」：关掉之后不再存新密码、不再弹保存提示，但**此前已经存下来的
// 密码仍会被建议与回填**（官方 specs/Autofill.md 明写）。想彻底不留，用「关闭即清空」。

// panelIIDSettings4 是 IID_ICoreWebView2Settings4（{cb56846c-4168-4d53-b04f-03b6d6796ff2}）。
var panelIIDSettings4 = windows.GUID{
	Data1: 0xcb56846c,
	Data2: 0x4168,
	Data3: 0x4d53,
	Data4: [8]byte{0xb0, 0x4f, 0x03, 0xb6, 0xd6, 0x79, 0x6f, 0xf2},
}

// panelWebViewSettingsVtbl 是**继承链完整**的 ICoreWebView2Settings4 vtable。
//
// 槽位号 = IUnknown(0–2) 之后的**连续位置**，按官方 IDL 逐条数：
// Settings1 3–20（18 项）、Settings2 21–22（UserAgent）、Settings3 23–24
// （AreBrowserAcceleratorKeysEnabled）、Settings4 25–28（IsPasswordAutosaveEnabled 与
// IsGeneralAutofillEnabled 各 get/put）。
//
// ⚠️ 不要拿 go-webview2 的 pkg/webview2/ICoreWebView2Settings{,2,3,4}.go 当基准：它把每个
// SettingsN 当成**独立接口**、只嵌 IUnknownVtbl，槽位与真实 ABI 对不上；而且它的
// GetICoreWebView2SettingsN() 是直接从 ICoreWebView2 QI —— Settings4 由 Settings 对象
// 实现，那条路必然失败。它是核对 ICoreWebView2 槽位的基准，Settings 系列只能按 IDL 数。
//
// 少写一个方法，其后所有槽位整体错位一格，且**编译、vet、单测全不报错**（详见
// panel_window_windows.go 的 panelWebViewVtbl 注释）。本结构只用到 26，前面的是为对齐槽位。
type panelWebViewSettingsVtbl struct {
	panelIUnknownVtbl
	GetIsScriptEnabled                  panelCOMProc // 3
	PutIsScriptEnabled                  panelCOMProc // 4
	GetIsWebMessageEnabled              panelCOMProc // 5
	PutIsWebMessageEnabled              panelCOMProc // 6
	GetAreDefaultScriptDialogsEnabled   panelCOMProc // 7
	PutAreDefaultScriptDialogsEnabled   panelCOMProc // 8
	GetIsStatusBarEnabled               panelCOMProc // 9
	PutIsStatusBarEnabled               panelCOMProc // 10
	GetAreDevToolsEnabled               panelCOMProc // 11
	PutAreDevToolsEnabled               panelCOMProc // 12
	GetAreDefaultContextMenusEnabled    panelCOMProc // 13
	PutAreDefaultContextMenusEnabled    panelCOMProc // 14
	GetAreHostObjectsAllowed            panelCOMProc // 15
	PutAreHostObjectsAllowed            panelCOMProc // 16
	GetIsZoomControlEnabled             panelCOMProc // 17
	PutIsZoomControlEnabled             panelCOMProc // 18
	GetIsBuiltInErrorPageEnabled        panelCOMProc // 19
	PutIsBuiltInErrorPageEnabled        panelCOMProc // 20
	GetUserAgent                        panelCOMProc // 21
	PutUserAgent                        panelCOMProc // 22
	GetAreBrowserAcceleratorKeysEnabled panelCOMProc // 23
	PutAreBrowserAcceleratorKeysEnabled panelCOMProc // 24
	GetIsPasswordAutosaveEnabled        panelCOMProc // 25
	PutIsPasswordAutosaveEnabled        panelCOMProc // 26
	GetIsGeneralAutofillEnabled         panelCOMProc // 27
	PutIsGeneralAutofillEnabled         panelCOMProc // 28
}

type panelWebViewSettings struct {
	Vtbl *panelWebViewSettingsVtbl
}

// applyPasswordAutosave 把 enabled 写进这个 WebView2 的密码保存属性。
//
// 失败一律静默：只有 1.0.1108 之前的运行时（没有 ICoreWebView2Settings4）才会失败，
// 那属于「这台机器没有这个能力」，不是错误 —— 为此让面板打不开、或弹一个用户看不懂的
// 错误框，都比默默按默认值（关）继续更糟。
func applyPasswordAutosave(webview *panelWebView, enabled bool) {
	if webview == nil {
		return
	}

	var settings *panelWebViewSettings
	hr, _, _ := webview.Vtbl.GetSettings.Call(
		uintptr(unsafe.Pointer(webview)),
		uintptr(unsafe.Pointer(&settings)),
	)
	if int32(hr) < 0 || settings == nil {
		return
	}
	defer panelCOMRelease(settings)

	// Settings4 由 Settings 对象实现，所以 QI 要打在 settings 上，不能打在 ICoreWebView2 上。
	var settings4 *panelWebViewSettings
	hr, _, _ = settings.Vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(settings)),
		uintptr(unsafe.Pointer(&panelIIDSettings4)),
		uintptr(unsafe.Pointer(&settings4)),
	)
	if int32(hr) < 0 || settings4 == nil {
		return
	}
	// 与 settings 是同一个对象，QI 只用来确认能力；多出来的那个引用就地还掉。
	panelCOMRelease(settings4)

	// BOOL 按值传 32 位，x64 下读的正是低 32 位，直接传 0/1 即可（与 putIsVisible 同一套路）。
	flag := uintptr(0)
	if enabled {
		flag = 1
	}
	settings.Vtbl.PutIsPasswordAutosaveEnabled.Call(uintptr(unsafe.Pointer(settings)), flag)
}
