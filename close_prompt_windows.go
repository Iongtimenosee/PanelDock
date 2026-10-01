//go:build windows

package main

// 原生「关闭面板窗口」询问对话框。
//
// 需求：用户关闭面板窗口（分组标签窗口）时询问「最小化到托盘」还是「直接关闭」，
// 并可勾选「记住我的选择」。Win32 的 MessageBoxW 不支持复选框，因此用标准控件
// 手工搭建一个模态小窗口 —— 搭建机制见 prompt_modal_windows.go
// （与「面板已停用」询问框共用），本文件只负责**关闭询问自己的内容**：文案与布局数值。
//
// 只有面板窗口会问：管理窗口的关闭动作是固定的（有其它窗口或托盘图标就只关自己，
// 否则退出程序），没有可选项，因此不弹框 —— 见 App.closeManagerWindow。

import "strings"

// promptCloseDialog 显示关闭询问框并阻塞到用户做出选择。
// owner 为被关闭的窗口（可为 0），对话框在其中心显示并禁用 owner 形成模态。
// note 是可选的补充说明（追加在正文末行），用于如实告知本次关闭的额外后果
// （如「这是最后一个面板，关闭后程序会退出」）；为空时不显示。
// text 是界面文案，由调用方按当前语言解析（App.nativeText），本函数不关心语言。
// ok 为 false 表示用户取消或对话框无法创建 —— 两种情况调用方都应中止关闭。
func promptCloseDialog(owner uintptr, note string, text closePromptText) (promptResult, bool) {
	body := text.Body
	if note != "" {
		body += "\r\n" + note
	}

	// 正文区高度按正文行数给：STATIC 控件**不会自动长高**，行数多了必须手动加高，
	// 否则多出来的行会被裁掉。基准是 2 行 → 62px；带补充说明时会多一行，
	// 而说明本身还会自动折行，正文合计可达 5 行 → 104px。
	bodyLines := 1 + strings.Count(body, "\r\n")
	bodyHeight := 62
	checkY := 118
	clientHeightUnits := 220
	if bodyLines > 2 {
		bodyHeight = 104
		checkY = 160
		clientHeightUnits = 254
	}

	return showPromptModal(owner, promptModalSpec{
		Caption:      text.Caption,
		Title:        text.Title,
		Body:         body,
		BodyHeight:   bodyHeight,
		ClientWidth:  470,
		ClientHeight: clientHeightUnits,
		Check: &promptModalCheck{
			ID:   closePromptChkRemember,
			Text: text.Remember,
			Y:    checkY,
		},
		// 从右到左：取消在最右，「最小化到托盘」在最左。
		// 「最小化到托盘」是不丢数据的选项，作为默认按钮（回车即选中）。
		Buttons: []promptModalButton{
			{ID: closePromptBtnCancel, Text: text.Cancel, Width: 90},
			{ID: closePromptBtnClose, Text: text.Close, Width: 110},
			{ID: closePromptBtnTray, Text: text.Tray, Width: 130, Default: true},
		},
	})
}
