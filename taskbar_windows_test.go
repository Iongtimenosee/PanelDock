//go:build windows

package main

// 「固定到任务栏」的行为锁定。
//
// 这个功能**不是**「一键固定」：Windows 10/11 不允许程序自己往任务栏上钉图标
// （详见 taskbar_windows.go 的文件头）。后端能做的是两件事，测试逐条锁住：
//   1. 该面板还没有快捷方式 → 先建一个（并记进配置，删面板时能一起清）；
//   2. 回读任务栏固定目录，已经在上面 → 如实报告，不做多余动作。
//
// **准备快捷方式时不许动资源管理器**（2026-09-30 调整）：弹框的同时抢走前台，
// 用户还没读完说明就被切走了。选中快捷方式只能由用户点按钮触发（RevealPanelShortcut）。
//
// 「已在任务栏上」的判定必须靠回读固定目录，且不能把别的面板/别的程序的固定项算进来。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubTaskbarSeams 把桌面目录、任务栏固定目录、本程序文件名、以及「在资源管理器中显示」
// 全部重定向到测试环境，返回还原函数与「被 reveal 过的路径」记录器。
func stubTaskbarSeams(t *testing.T, desktop, pinned string) (restore func(), revealed *[]string) {
	t.Helper()

	restoreShortcut := stubShortcutSeams(t, desktop)
	originalPinnedDir := shortcutTaskbarPinnedDir
	originalReveal := shortcutRevealInExplorer

	shortcutTaskbarPinnedDir = func() (string, error) { return pinned, nil }

	calls := new([]string)
	shortcutRevealInExplorer = func(path string) error {
		*calls = append(*calls, path)
		return nil
	}

	return func() {
		restoreShortcut()
		shortcutTaskbarPinnedDir = originalPinnedDir
		shortcutRevealInExplorer = originalReveal
	}, calls
}

func countLnk(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录 %s: %v", dir, err)
	}
	n := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".lnk") {
			n++
		}
	}
	return n
}

// TestPinPanelToTaskbarDetectsPinned 面板已经在任务栏上时如实报告，且不做多余动作
// （不 reveal、不再造一个快捷方式）。
func TestPinPanelToTaskbarDetectsPinned(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, revealed := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "已固定的面板")
	lnk := writePanelShortcut(t, desktop, "已固定的面板.lnk", panel.ID)
	// Windows 固定时会把 .lnk 复制进固定目录 —— 这里直接模拟那一步的结果。
	writePanelShortcut(t, pinned, "已固定的面板.lnk", panel.ID)

	result, err := app.PinPanelToTaskbar(panel.ID)
	if err != nil {
		t.Fatalf("PinPanelToTaskbar: %v", err)
	}
	if !result.AlreadyPinned {
		t.Error("面板已固定在任务栏上，应报告 AlreadyPinned")
	}
	if result.Shortcut != lnk {
		t.Errorf("应复用已有快捷方式 %q，实际 %q", lnk, result.Shortcut)
	}
	if len(*revealed) != 0 {
		t.Errorf("已固定时不应再弹资源管理器，实际 reveal: %v", *revealed)
	}
	if n := countLnk(t, desktop); n != 1 {
		t.Errorf("不应重复创建快捷方式，桌面 .lnk 数量 = %d", n)
	}
}

// TestPinPanelToTaskbarDoesNotReveal 没固定过时只把现成的快捷方式报回来，
// **绝不替用户打开资源管理器** —— 弹框时用户还在读说明，这时抢走前台就是擅作主张。
func TestPinPanelToTaskbarDoesNotReveal(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, revealed := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "待固定的面板")
	lnk := writePanelShortcut(t, desktop, "待固定的面板.lnk", panel.ID)

	result, err := app.PinPanelToTaskbar(panel.ID)
	if err != nil {
		t.Fatalf("PinPanelToTaskbar: %v", err)
	}
	if result.AlreadyPinned {
		t.Error("固定目录里没有这个面板，不应报告 AlreadyPinned")
	}
	if result.Shortcut != lnk {
		t.Errorf("应复用已有快捷方式 %q，实际 %q", lnk, result.Shortcut)
	}
	if len(*revealed) != 0 {
		t.Fatalf("准备快捷方式时不应打开资源管理器（用户还没读完说明），实际 reveal: %v", *revealed)
	}
}

// TestPinPanelToTaskbarCreatesShortcutWhenMissing 面板一个快捷方式都没有时先补一个，
// 而且必须记进配置 —— 否则删除面板时这份 .lnk 会漏在桌面上没人清理。
func TestPinPanelToTaskbarCreatesShortcutWhenMissing(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, revealed := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "没有快捷方式的面板")

	result, err := app.PinPanelToTaskbar(panel.ID)
	if err != nil {
		t.Fatalf("PinPanelToTaskbar: %v", err)
	}
	if _, err := os.Stat(result.Shortcut); err != nil {
		t.Fatalf("应自动创建快捷方式: %v", err)
	}
	if n := countLnk(t, desktop); n != 1 {
		t.Errorf("桌面应只有一个新建的 .lnk，实际 %d", n)
	}
	if len(*revealed) != 0 {
		t.Errorf("新建快捷方式时不应打开资源管理器，实际 reveal: %v", *revealed)
	}

	cfg, _ := app.config.get(panel.ID)
	if cfg.Shortcut != result.Shortcut {
		t.Fatalf("新建的快捷方式必须记进配置（供删除面板时清理），实际: %q", cfg.Shortcut)
	}
}

// TestIsPanelPinnedToTaskbarIgnoresForeignItems 固定目录里别人的项一律不算数：
// 别的面板的固定项、别的程序的固定项、以及损坏/无目标的固定项（File Explorer 那类）。
func TestIsPanelPinnedToTaskbarIgnoresForeignItems(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, _ := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	const mine = "11111111-2222-3333-4444-555555555555"
	const other = "99999999-8888-7777-6666-555555555555"

	writePanelShortcut(t, pinned, "别的面板.lnk", other)
	// 别的程序：参数完全一样，只是目标不是本程序。
	foreignLnk := filepath.Join(pinned, "别的程序.lnk")
	if err := shortcutWithCOM(func() error {
		return writeShortcutLnk(foreignLnk, `C:\Tools\Other.exe`, "--open "+mine, "别的程序", "")
	}); err != nil {
		t.Fatalf("写入别的程序快捷方式: %v", err)
	}
	// 读不出来的项 + 非 .lnk 文件：必须只是跳过，不影响判定。
	if err := os.WriteFile(filepath.Join(pinned, "坏掉的.lnk"), []byte("not a lnk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pinned, "desktop.ini"), []byte("[.ShellClassInfo]"), 0o600); err != nil {
		t.Fatal(err)
	}

	if isPanelPinnedToTaskbar(mine) {
		t.Fatal("别的面板/别的程序的固定项不应被算成本面板已固定")
	}

	// 换上真正属于本面板的固定项，才应判定为已固定。
	writePanelShortcut(t, pinned, "本面板.lnk", mine)
	if !isPanelPinnedToTaskbar(mine) {
		t.Error("固定目录里有指向本面板的 .lnk 时应判定为已固定")
	}
}

// TestRevealPanelShortcutSelectsRecordedShortcut 用户点「选中快捷方式」时，
// 才在资源管理器中选中配置里记录的那份 .lnk。
func TestRevealPanelShortcutSelectsRecordedShortcut(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, revealed := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "要选中的面板")
	lnk := writePanelShortcut(t, desktop, "要选中的面板.lnk", panel.ID)

	if err := app.RevealPanelShortcut(panel.ID); err != nil {
		t.Fatalf("RevealPanelShortcut: %v", err)
	}
	if len(*revealed) != 1 || (*revealed)[0] != lnk {
		t.Fatalf("应在资源管理器中选中 %q，实际: %v", lnk, *revealed)
	}
}

// TestRevealPanelShortcutReportsFailure 连资源管理器都拉不起来时把错误交回前端，
// 让对话框能说实话（脚注转告警色）而不是假装已经打开。
func TestRevealPanelShortcutReportsFailure(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, _ := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	shortcutRevealInExplorer = func(string) error { return fmt.Errorf("explorer 起不来") }

	app := newTestApp(t)
	panel := createTestPanel(t, app, "拉不起资源管理器的面板")
	writePanelShortcut(t, desktop, "x.lnk", panel.ID)

	if err := app.RevealPanelShortcut(panel.ID); err == nil {
		t.Error("reveal 失败时应把错误交回前端")
	}
}

// TestPinPanelToTaskbarUnknownPanel 面板不存在时返回错误，而不是静默成功。
func TestPinPanelToTaskbarUnknownPanel(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, revealed := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	if _, err := app.PinPanelToTaskbar("不存在的面板"); err == nil {
		t.Error("面板不存在时应返回错误")
	}
	if len(*revealed) != 0 {
		t.Errorf("出错时不应触发任何窗口操作，实际: %v", *revealed)
	}
}

// TestRevealPanelShortcutWithoutShortcut 没有快捷方式时明确报错，不静默什么都不做。
func TestRevealPanelShortcutWithoutShortcut(t *testing.T) {
	desktop, pinned := t.TempDir(), t.TempDir()
	restore, _ := stubTaskbarSeams(t, desktop, pinned)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "没有快捷方式的面板")

	if err := app.RevealPanelShortcut(panel.ID); err == nil {
		t.Error("没有可显示的快捷方式时应返回错误")
	}
}
