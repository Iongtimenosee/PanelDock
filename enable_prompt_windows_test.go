//go:build windows

package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// stubEnableConfirm 替换「是否启用」的询问实现，返回「本次是否被询问」的探针。
// 单测绝不能真的弹原生询问框：那会把测试卡在等人的点击上（与 App.decideClose
// 的「已记住」分支同理 —— 只有那个不创建对话框的分支能在测试进程里断言）。
func stubEnableConfirm(t *testing.T, answer bool) *bool {
	t.Helper()

	orig := confirmEnableDisabledPanel
	t.Cleanup(func() { confirmEnableDisabledPanel = orig })

	asked := new(bool)
	confirmEnableDisabledPanel = func(string, enablePromptText) bool {
		*asked = true
		return answer
	}
	return asked
}

// TestEnablePromptSpec 验证「面板已停用」询问框的静态内容。
//
// 这些字段不是随便断言的：标题栏文案是端到端用例定位窗口的唯一依据；
// 按钮 ID 一旦与关闭询问框撞车，两个框的按钮就会互相串味（窗口过程按 ID 分发）；
// 默认按钮必须是「启用并打开」—— 用户刚双击了快捷方式，回车就该打开它。
func TestEnablePromptSpec(t *testing.T) {
	// 用 zh-CN（源语言）词典断言静态内容；en-US 只需结构与它同构（见 TestNativeUITextsComplete）。
	text := nativeUITexts[LanguageZhCN].EnablePrompt
	spec := enablePromptSpec("OpenClash 演示", text)

	if spec.Caption != text.Caption {
		t.Errorf("标题栏文案 = %q, want %q（端到端用例按它定位窗口）", spec.Caption, text.Caption)
	}
	if !strings.Contains(spec.Title, "OpenClash 演示") {
		t.Errorf("标题应带上面板名，实际 %q", spec.Title)
	}
	if !strings.Contains(spec.Body, "停用") {
		t.Errorf("正文应说明它处于停用状态，实际 %q", spec.Body)
	}

	// 正文写的行数要与留给它的高度匹配：STATIC 不会自动长高，行多了会被裁掉。
	if lines := 1 + strings.Count(spec.Body, "\r\n"); lines != 3 {
		t.Errorf("正文应为 3 行（对应 BodyHeight=%d），实际 %d 行", spec.BodyHeight, lines)
	}

	// 「启用」是一次性决定：绝不能出现「记住我的选择」，否则停用功能名存实亡。
	if spec.Check != nil {
		t.Errorf("「面板已停用」询问框不应有复选框，实际 %q", spec.Check.Text)
	}

	if len(spec.Buttons) != 2 {
		t.Fatalf("应有 2 个按钮，实际 %d 个", len(spec.Buttons))
	}
	var open, keep *promptModalButton
	for i := range spec.Buttons {
		switch spec.Buttons[i].ID {
		case enablePromptBtnOpen:
			open = &spec.Buttons[i]
		case enablePromptBtnKeep:
			keep = &spec.Buttons[i]
		}
	}
	if open == nil || keep == nil {
		t.Fatalf("按钮 ID 不完整: %+v", spec.Buttons)
	}
	if !open.Default {
		t.Error("「启用并打开」应为默认按钮（回车即选中）")
	}
	if keep.Default {
		t.Error("「保持停用」不应是默认按钮")
	}
	if !strings.Contains(open.Text, "启用") || !strings.Contains(keep.Text, "保持停用") {
		t.Errorf("按钮措辞不对: open=%q keep=%q", open.Text, keep.Text)
	}

	// ID 必须与关闭询问框的几个互不相同：窗口过程只按 ID 分发动作。
	closeIDs := map[uintptr]string{
		closePromptBtnTray:     "关闭询问·最小化到托盘",
		closePromptBtnClose:    "关闭询问·直接关闭",
		closePromptBtnCancel:   "关闭询问·取消",
		closePromptChkRemember: "关闭询问·记住我的选择",
	}
	for _, id := range []uintptr{enablePromptBtnOpen, enablePromptBtnKeep} {
		if name, ok := closeIDs[id]; ok {
			t.Errorf("按钮 ID %d 与 %s 撞车，两个询问框的按钮会互相串味", id, name)
		}
	}
}

// TestEnablePromptName 验证放进标题的面板名处理：过长要截断（标题只有一行高，超长会被裁掉），
// 且必须按**字符**截而不是字节 —— 按字节切会把中文切坏成乱码。
func TestEnablePromptName(t *testing.T) {
	fallback := nativeUITexts[LanguageZhCN].EnablePrompt.FallbackName
	if got := enablePromptName("  NAS 面板  ", fallback); got != "NAS 面板" {
		t.Errorf("应去掉首尾空白，实际 %q", got)
	}
	if got := enablePromptName("   ", fallback); got != fallback {
		t.Errorf("名字为空时应给兜底说法，实际 %q", got)
	}

	long := strings.Repeat("路", enablePromptNameMax+5)
	got := enablePromptName(long, fallback)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("超长名字应加省略号，实际 %q", got)
	}
	if runes := []rune(strings.TrimSuffix(got, "…")); len(runes) != enablePromptNameMax {
		t.Errorf("截断后应有 %d 个字符，实际 %d（%q）", enablePromptNameMax, len(runes), got)
	}
	if strings.Contains(got, "\uFFFD") {
		t.Errorf("截断把中文切坏了: %q", got)
	}
}

// TestSetEnabledKeepsOtherFields 验证 setEnabled 只动启用状态：
// 外部请求里的「启用并打开」不该顺手改掉名称、地址、标签或其它面板。
func TestSetEnabledKeepsOtherFields(t *testing.T) {
	store := newTestStore(t)

	panel, err := store.create("路由器", "http://192.168.1.1", false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	other, err := store.create("NAS", "https://nas.lan", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.setShortcut(panel.ID, `C:\Users\me\Desktop\路由器.lnk`); err != nil {
		t.Fatalf("setShortcut: %v", err)
	}
	before, _ := store.get(panel.ID)

	if err := store.setEnabled(panel.ID, true); err != nil {
		t.Fatalf("setEnabled: %v", err)
	}

	after, ok := store.get(panel.ID)
	if !ok {
		t.Fatal("面板不应消失")
	}
	if !after.Enabled {
		t.Error("启用状态未写入")
	}
	if after.Name != before.Name || after.ActiveURL() != before.ActiveURL() ||
		len(after.Tabs) != len(before.Tabs) || after.Tabs[0].ID != before.Tabs[0].ID {
		t.Errorf("除启用状态外的字段不应被改动:\nbefore=%+v\nafter=%+v", before, after)
	}
	if after.Shortcut != before.Shortcut {
		t.Errorf("快捷方式记录不应被改动: %q", after.Shortcut)
	}
	if got, _ := store.get(other.ID); !got.Enabled {
		t.Error("不该动到别的面板")
	}

	// 落盘：重开一次存储仍是启用状态。
	reloaded := newConfigStore()
	reloaded.path = store.path
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got, _ := reloaded.get(panel.ID); !got.Enabled {
		t.Error("启用状态未持久化")
	}

	// 面板不存在时如实报错，而不是静默成功。
	if err := store.setEnabled("no-such-panel", true); err == nil {
		t.Error("面板不存在时应返回错误")
	}
}

// TestEnsurePanelEnabledForExternalOpen 验证外部打开请求遇到停用面板时的四条路径。
//
// 这里只覆盖「是否询问 / 是否落盘」这一段：真正打开面板要起 WebView2，
// 属于端到端用例的职责（TestE2EDisabledPanelAsksToEnable）。
func TestEnsurePanelEnabledForExternalOpen(t *testing.T) {
	// ── 面板不存在：不询问，直接报错（用户没得选，问了也没用）──────────────
	store := newTestStore(t)
	app := &App{config: store}
	asked := stubEnableConfirm(t, true)
	if err := app.ensurePanelEnabledForExternalOpen("no-such-panel"); err == nil {
		t.Error("面板不存在时应返回错误")
	}
	if *asked {
		t.Error("面板不存在时不应弹询问框")
	}

	// ── 已启用：不询问，直接放行 ──────────────────────────────────────────
	enabled, err := store.create("已启用", "http://192.168.1.1", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	*asked = false
	if err := app.ensurePanelEnabledForExternalOpen(enabled.ID); err != nil {
		t.Fatalf("已启用的面板应直接放行: %v", err)
	}
	if *asked {
		t.Error("已启用的面板不应弹询问框")
	}

	// ── 停用 + 用户选「保持停用」：静默收场，配置一个字节都不改 ─────────────
	disabled, err := store.create("停用面板", "http://192.168.1.2", false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	asked = stubEnableConfirm(t, false)
	if err := app.ensurePanelEnabledForExternalOpen(disabled.ID); !errors.Is(err, ErrPanelKeptDisabled) {
		t.Errorf("用户选择保持停用时应返回 ErrPanelKeptDisabled，实际 %v", err)
	}
	if !*asked {
		t.Error("停用的面板应先询问用户")
	}
	if got, _ := store.get(disabled.ID); got.Enabled {
		t.Error("用户选择保持停用时不应启用面板")
	}

	// ── 停用 + 用户选「启用并打开」：落盘启用，返回 nil 让调用方继续打开 ─────
	asked = stubEnableConfirm(t, true)
	if err := app.ensurePanelEnabledForExternalOpen(disabled.ID); err != nil {
		t.Fatalf("用户同意启用后应放行: %v", err)
	}
	if !*asked {
		t.Error("停用的面板应先询问用户")
	}
	if got, _ := store.get(disabled.ID); !got.Enabled {
		t.Error("用户同意后应把面板设为启用")
	}

	// 开启后再次请求（用户又点了一次快捷方式）：不该再问第二遍。
	asked = stubEnableConfirm(t, false)
	if err := app.ensurePanelEnabledForExternalOpen(disabled.ID); err != nil {
		t.Fatalf("已启用的面板应直接放行: %v", err)
	}
	if *asked {
		t.Error("面板已启用，不应再问一遍")
	}

	// 落盘确认：启用状态必须写进文件，而不是只活在内存里。
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("回读配置失败: %v", err)
	}
	if !strings.Contains(string(data), `"enabled": true`) {
		t.Errorf("启用状态未落盘: %s", data)
	}
}
