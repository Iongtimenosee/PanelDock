//go:build windows

package main

// 原生「面板已停用」询问框。
//
// 场景：面板（分组）被停用后，桌面快捷方式 / 任务栏固定的图标仍然指向它（`--open <面板ID>`）。
// 若什么都不做（只在日志里留一行），用户双击快捷方式看起来像没反应 ——
// 他无从判断是自己停用了它、还是程序坏了、还是地址变了。
//
// 因此这里弹一个原生询问框，说清「它被停用了」，并给出两条路：
//   - 启用并打开：把它设为启用（落盘）并立即打开；
//   - 保持停用：什么都不做，回到原来的静默状态。
//
// 搭建机制复用 prompt_modal_windows.go（所有询问框共用一个窗口类，靠标题栏文案区分）。
// 与「关闭询问框」的关键区别：**这里没有「记住我的选择」** ——
// 「启用并打开」是一次明确的一次性决定，把它记成「以后自动启用」等于悄悄绕过用户的停用意图，
// 那样停用功能本身就名存实亡了。

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// enablePromptNameMax 是放进标题的面板名长度上限。
	// 标题 STATIC 只有一行的高度（26 单位），超长会直接被裁掉 —— 与其让用户看到半截名字，
	// 不如明确加省略号；完整名字在管理界面的卡片上随时能看到。
	enablePromptNameMax = 16
)

// promptActionEnable 是「启用并打开」按钮对应的动作字符串（由 promptProc 写入询问结果）。
const promptActionEnable = "enable"

// enablePromptSpec 构造「面板已停用」询问框的内容。
// text 是界面文案，由调用方按当前语言解析（App.nativeText），本函数不关心语言。
//
// 单独抽成函数是为了可测：端到端用例按 Caption 找窗口、按按钮 ID 点击，单测在这里断言
// 「标题就是所选语言的 Caption」「默认按钮是启用并打开」「按钮 ID 不与关闭询问框撞车」。
//
// 布局数值：正文 3 行 → 84 单位（与关闭询问的管理角色同档，都留了余量），
// 客户区 210 单位，按钮那一行落在正文下方 24 单位处。
// 注意：英文正文如果重写，行数变了必须同步 BodyHeight（STATIC 不会自动长高）。
func enablePromptSpec(panelName string, text enablePromptText) promptModalSpec {
	return promptModalSpec{
		Caption:      text.Caption,
		Title:        strings.ReplaceAll(text.TitleFmt, "{name}", enablePromptName(panelName, text.FallbackName)),
		Body:         text.Body,
		BodyHeight:   84,
		ClientWidth:  470,
		ClientHeight: 210,
		// 从右到左：保持停用在最右。
		Buttons: []promptModalButton{
			{ID: enablePromptBtnKeep, Text: text.Keep, Width: 110},
			// 用户刚双击了快捷方式，意图就是打开它 —— 默认按钮给「启用并打开」（回车即选中）。
			{ID: enablePromptBtnOpen, Text: text.Open, Width: 130, Default: true},
		},
	}
}

// enablePromptName 取用于标题的面板名：去掉首尾空白，过长则截断加省略号。
// fallback 是名字为空时的兜底说法（语言相关），避免出现「「」已停用」这种残缺标题。
func enablePromptName(name, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	runes := []rune(name)
	if len(runes) <= enablePromptNameMax {
		return name
	}
	return string(runes[:enablePromptNameMax]) + "…"
}

// promptEnableDisabledPanel 询问用户是否启用该面板，阻塞到用户做出选择。
// 返回 true 表示用户选择「启用并打开」；false 表示用户选择「保持停用」、
// 按 Esc / 关掉询问框，或询问框压根没能创建 —— 三种情况调用方都保持现状。
func promptEnableDisabledPanel(panelName string, text enablePromptText) bool {
	result, ok := showPromptModal(enablePromptOwner(), enablePromptSpec(panelName, text))
	return ok && result.Action == promptActionEnable
}

// enablePromptOwner 返回询问框该挂在哪个窗口上（用于居中，并把它置灰形成模态）。
//
// 管理窗口**可见时**才用它：FindWindowW 连隐藏窗口也能找到，而轻量模式 / 窗口已藏进托盘时
// 主窗口是隐藏的 —— 拿它当 owner 会让询问框居中到一个用户看不见的位置（可能还在屏幕外）。
// 这种情况返回 0，询问框居中到主屏幕。
func enablePromptOwner() uintptr {
	hwnd := mainWindowHandle()
	if !promptWindowVisible(hwnd) {
		return 0
	}
	return hwnd
}

// mainWindowHandle 返回 Wails 管理窗口句柄；找不到时返回 0（询问框退化为屏幕居中）。
// Wails v2 的 runtime 不暴露 HWND，这里按窗口标题精确查找：面板窗口标题带
// 「 · 面板名」后缀，不会与管理窗口标题冲突。
func mainWindowHandle() uintptr {
	title, err := windows.UTF16PtrFromString(appMainWindowTitle)
	if err != nil {
		return 0
	}
	hwnd, _, _ := ipcFindWindowW.Call(0, uintptr(unsafe.Pointer(title)))
	return hwnd
}
