//go:build windows

package main

// 原生控件的界面文案词典（i18n 的 Win32 侧）。
//
// 范围：本程序**自己画的**原生界面要跟语言设置走 ——
// 「关闭面板窗口」询问框、「面板已停用」询问框、托盘菜单、托盘悬浮提示、
// 面板询问框的补充说明。WebView2 自带的右键菜单、系统输入框等跟随 Windows
// 显示语言，由系统自己本地化，这里管不到。
//
// 词典编译进程序（map 字面量），不做外挂语言文件：桌面工具没有「不发版加语言」
// 的需求，外挂文件只多一个「文件丢失/版本不齐」的失败路径。
//
// 取词规则（唯一入口 App.nativeText）：每次弹框 / 建菜单时**现读配置现解析，不缓存**。
// 语言切换对原生侧因此天然即时生效 —— 下一个弹出的询问框、下一次右键托盘就是新语言，
// 不需要任何推送或刷新机制；代价是每次读一次内存里的配置，纳秒级。
//
// 与前端词典（frontend/src/i18n/）刻意**不共享**：两边文案集合几乎不相交
// （web 设置界面 vs 原生弹框/托盘），共享一份源文件需要引入生成步骤，
// 复杂度远超省下的那几条重复串。

import (
	"golang.org/x/sys/windows"
)

// closePromptText 是关闭询问框的全部界面文案。
// Caption 同时用于原标题栏显示与端到端测试定位（见 waitForClosePrompt），
// 因此必须是精确标题。
type closePromptText struct {
	Caption  string
	Title    string
	Body     string
	Tray     string
	Close    string
	Remember string
	Cancel   string
	// Note 是「关掉它程序就收工」的补充说明（panelWindow.closePromptNote 用）。
	Note string
}

// enablePromptText 是「面板已停用」询问框的全部界面文案。
type enablePromptText struct {
	Caption string
	// TitleFmt 里的 {name} 会被替换为（已截断的）面板名。
	TitleFmt string
	Body     string
	Open     string
	Keep     string
	// FallbackName 是面板名为空时的兜底说法（避免出现「「」已停用」这种残缺标题）。
	FallbackName string
}

// trayText 是托盘图标相关的界面文案（悬浮提示 + 右键菜单）。
//
// 菜单里的「已隐藏的窗口」项**直接用分组名**，不带任何前缀后缀：
// 用户的用途是「一眼认出并点回来」，套一层「显示 xxx」反而把名字挤到后面去了。
type trayText struct {
	Tip string
	// NoHidden 是一个都没有时的占位项（置灰），避免菜单空得让人以为程序坏了。
	NoHidden    string
	OpenManager string
	Quit        string
	// CloseHidden 是「彻底关闭」子菜单的标题：里面列的是同一批窗口，点了就真的关掉。
	// 关闭和「取回」分成两处入口，是因为它们挨在一起时手滑一次就丢了一个窗口 ——
	// 丢东西这件事该多走一步。子菜单里的项同样只写分组名。
	CloseHidden string
}

// configText 是配置导出/导入两个原生文件对话框的标题。
type configText struct {
	ExportTitle string
	ImportTitle string
}

// nativeUIText 是一种语言的原生界面文案全集。
type nativeUIText struct {
	ClosePrompt  closePromptText
	EnablePrompt enablePromptText
	Tray         trayText
	Config       configText
}

// nativeUITexts 是全部内置语言的词典。zh-CN 是源语言（与前端词典同约定）。
var nativeUITexts = map[string]nativeUIText{
	LanguageZhCN: {
		ClosePrompt: closePromptText{
			Caption: "PanelDock · 关闭面板窗口",
			Title:   "关闭这个面板窗口？",
			Body: "最小化到托盘：隐藏窗口，程序在后台继续运行，可从托盘图标重新打开。\r\n" +
				"直接关闭：只关闭这个面板窗口，其他面板与管理面板不受影响。",
			Tray:     "最小化到托盘",
			Close:    "直接关闭",
			Remember: "记住我的选择（以后关闭面板窗口不再询问）",
			Cancel:   "取消",
			Note:     "提示：这是当前唯一打开的面板，且管理面板未打开；选「直接关闭」后程序会随之退出（托盘图标一并消失）。",
		},
		EnablePrompt: enablePromptText{
			Caption:      "PanelDock · 面板已停用",
			TitleFmt:     "「{name}」已停用",
			Body:         "停用状态下，双击快捷方式、点击任务栏图标都不会打开它。\r\n「启用并打开」：立即启用并打开这个面板。\r\n「保持停用」：什么都不做（和以前一样）。",
			Open:         "启用并打开",
			Keep:         "保持停用",
			FallbackName: "这个面板",
		},
		Tray: trayText{
			Tip:         "PanelDock · Web管理面板启动器",
			NoHidden:    "没有已隐藏的窗口",
			OpenManager: "打开管理面板",
			Quit:        "退出 PanelDock",
			CloseHidden: "彻底关闭窗口",
		},
		Config: configText{
			ExportTitle: "导出配置",
			ImportTitle: "导入配置",
		},
	},
	LanguageEnUS: {
		ClosePrompt: closePromptText{
			Caption: "PanelDock · Close panel window",
			Title:   "Close this panel window?",
			Body: "Minimize to tray: hides the window; the app keeps running and you can reopen\r\n" +
				"it from the tray icon. Close: closes only this panel window; other panels and the\r\n" +
				"manager window are not affected.",
			Tray:     "Minimize to tray",
			Close:    "Close",
			Remember: "Remember my choice (don't ask again when closing panel windows)",
			Cancel:   "Cancel",
			Note:     "Note: this is the only open panel and the manager window is not shown; choosing Close will also exit the app (the tray icon goes away too).",
		},
		EnablePrompt: enablePromptText{
			Caption:      "PanelDock · Panel disabled",
			TitleFmt:     "\"{name}\" is disabled",
			Body:         "While disabled, double-clicking its shortcut or clicking its taskbar icon will not open it.\r\n\"Enable and open\": enables this panel and opens it right away.\r\n\"Keep disabled\": do nothing (same as before).",
			Open:         "Enable and open",
			Keep:         "Keep disabled",
			FallbackName: "this panel",
		},
		Tray: trayText{
			Tip:         "PanelDock · Web panel launcher",
			NoHidden:    "No hidden windows",
			OpenManager: "Open manager",
			Quit:        "Quit PanelDock",
			CloseHidden: "Close a window for good",
		},
		Config: configText{
			ExportTitle: "Export configuration",
			ImportTitle: "Import configuration",
		},
	},
}

// nativeTextDefault 是取不到词典时的兜底语言（源语言）。
const nativeTextDefault = LanguageZhCN

// panelGetUserDefaultUILanguage 取 Windows 当前显示语言的 LANGID。
var panelGetUserDefaultUILanguage = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// systemLanguage 把 Windows 显示语言映射到内置语言之一：
// 中文主语言（LANG_CHINESE，不管简繁区域）→ zh-CN，其余 → en-US。
// 与前端的 navigator.language 判定（zh 开头 → zh-CN）语义一致。
func systemLanguage() string {
	langID, _, _ := panelGetUserDefaultUILanguage.Call()
	if uintptr(uint16(langID))&0x3FF == 0x04 { // LANG_CHINESE
		return LanguageZhCN
	}
	return LanguageEnUS
}

// resolveLanguage 把语言设置解析成具体语言：显式 zh-CN / en-US 原样返回，
// auto（默认）跟随 Windows 显示语言。前端（navigator.language）与原生侧共用同一语义。
func (a *App) resolveLanguage() string {
	setting := normalizeLanguage(a.config.settings().Language)
	if setting != LanguageAuto {
		return setting
	}
	return systemLanguage()
}

// nativeText 返回当前语言的界面文案。每次调用现读配置（不缓存），
// 语言切换对询问框与托盘菜单即时生效，见文件头注释。
func (a *App) nativeText() nativeUIText {
	if t, ok := nativeUITexts[a.resolveLanguage()]; ok {
		return t
	}
	return nativeUITexts[nativeTextDefault]
}
