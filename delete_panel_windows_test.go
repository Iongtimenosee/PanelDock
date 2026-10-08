//go:build windows

package main

// 「删除面板」的两条收尾行为锁定：
//
//  1. 桌面快捷方式**可选**联动清理（`removeShortcuts`）—— 必须是用户在确认对话框里
//     **显式勾选**的结果，没勾就不能动桌面上的 .lnk，后端只认布尔入参；
//  2. 浏览器数据**无条件**清空—— 不看面板的 sessionMode，
//     清不干净就报错并保留面板。
//
// 两者都必须真的走到磁盘，所以断言的都是文件系统状态，而不是「调用没报错」。

import (
	"os"
	"path/filepath"
	"testing"
)

// shortcutTestExeName 是测试里假装的「本程序文件名」。
// 生产代码用 os.Executable()（测试二进制名与 PanelDock.exe 不同），因此必须注入。
const shortcutTestExeName = "PanelDock.exe"

// stubShortcutSeams 把「桌面目录」与「本程序文件名」注入测试环境，返回还原函数。
// 真实桌面无法在测试里安全改写，因此整条扫描链路都靠这两个接缝重定向到临时目录。
func stubShortcutSeams(t *testing.T, desktop string) func() {
	t.Helper()

	originalDir, originalBase := shortcutDesktopDirectory, shortcutSelfExeBase
	shortcutDesktopDirectory = func() (string, error) { return desktop, nil }
	shortcutSelfExeBase = func() string { return shortcutTestExeName }
	return func() {
		shortcutDesktopDirectory = originalDir
		shortcutSelfExeBase = originalBase
	}
}

// writePanelShortcut 在指定「桌面」目录写下指向某个面板的 .lnk（真实 COM 落盘）。
func writePanelShortcut(t *testing.T, desktop, name, panelID string) string {
	t.Helper()

	path := filepath.Join(desktop, name)
	err := shortcutWithCOM(func() error {
		return writeShortcutLnk(path, `C:\Tools\`+shortcutTestExeName, "--open "+panelID, "PanelDock · 测试", "")
	})
	if err != nil {
		t.Fatalf("写入 %s: %v", name, err)
	}
	return path
}

// createTestPanel 在测试应用里新建一个面板配置并返回它。
func createTestPanel(t *testing.T, app *App, name string) PanelConfig {
	t.Helper()

	panel, err := app.config.create(name, "http://example.lan", true)
	if err != nil {
		t.Fatalf("创建面板 %q: %v", name, err)
	}
	return panel
}

// TestDeletePanelShortcutCleanupIsOptIn 覆盖删除面板时的两种收尾：
//   - removeShortcuts=false：桌面 .lnk 一个都不动，面板配置照常删除；
//   - removeShortcuts=true：配置里记录的、以及只靠扫描桌面匹配到的 .lnk 都被清理，
//     而且只清理本面板的（别的面板的 .lnk 必须毫发无伤）。
func TestDeletePanelShortcutCleanupIsOptIn(t *testing.T) {
	desktop := t.TempDir()
	restore := stubShortcutSeams(t, desktop)
	defer restore()

	app := newTestApp(t)

	// ── 1. 不勾选「同时删除快捷方式」：.lnk 必须留下 ────────────────────────────
	keepPanel := createTestPanel(t, app, "保留快捷方式的面板")
	keepLnk := writePanelShortcut(t, desktop, "保留.lnk", keepPanel.ID)
	if err := app.config.setShortcut(keepPanel.ID, keepLnk); err != nil {
		t.Fatalf("记录快捷方式: %v", err)
	}

	if err := app.DeletePanel(keepPanel.ID, false); err != nil {
		t.Fatalf("DeletePanel(removeShortcuts=false): %v", err)
	}
	if _, err := os.Stat(keepLnk); err != nil {
		t.Errorf("没勾选「同时删除快捷方式」时桌面 .lnk 必须保留: %v", err)
	}
	if _, ok := app.config.get(keepPanel.ID); ok {
		t.Error("面板配置应已删除")
	}

	// ── 2. 勾选：连扫描匹配到的 .lnk 一起清理 ─────────────────────────────────
	removePanel := createTestPanel(t, app, "清理快捷方式的面板")
	// 故意不写进配置：模拟用户改过名或手工创建，只能靠扫描桌面上的目标与参数匹配。
	scanLnk := writePanelShortcut(t, desktop, "扫描命中.lnk", removePanel.ID)
	otherLnk := writePanelShortcut(t, desktop, "别的面板.lnk", "00000000-0000-0000-0000-000000000000")

	if err := app.DeletePanel(removePanel.ID, true); err != nil {
		t.Fatalf("DeletePanel(removeShortcuts=true): %v", err)
	}
	if _, err := os.Stat(scanLnk); !os.IsNotExist(err) {
		t.Error("勾选后，扫描匹配到的 .lnk 应被清理")
	}
	if _, err := os.Stat(otherLnk); err != nil {
		t.Errorf("其他面板的 .lnk 不应被误删: %v", err)
	}
	if _, ok := app.config.get(removePanel.ID); ok {
		t.Error("面板配置应已删除")
	}

	// ── 3. 上一步保留下来的 .lnk 不受后续删除影响 ──────────────────────────────
	if _, err := os.Stat(keepLnk); err != nil {
		t.Errorf("第 1 步保留下来的 .lnk 不应被后来的删除波及: %v", err)
	}
}

// TestDeletePanelClearsBrowserData 验证删除面板时**无条件**清掉它所有标签的 WebView2 profile
// 目录。
//
// 断言磁盘状态而不是「调用没报错」：这个动作的全部价值就是数据真的没了 —— 少清一个标签，
// 那个标签的 Cookie 与 WebView2 保存的密码就还躺在磁盘上，而用户以为已经删干净。
//
// 同时锁住「默认的『保留状态』面板也一样清」：历史上这里是按 sessionMode 分支的
// （只有「关闭后清空」的面板才顺带清），改成无条件清理后，最容易的回归就是把分支写回来。
func TestDeletePanelClearsBrowserData(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)

	// 这个面板是默认的「保留浏览器状态」，正是历史上会被跳过的那个分支。
	if cfg, ok := app.config.get(panel.ID); !ok || cfg.clearsSessionOnClose() {
		t.Fatal("用例前提：该面板应为默认的「保留浏览器状态」")
	}

	if err := app.DeletePanel(panel.ID, false); err != nil {
		t.Fatalf("DeletePanel: %v", err)
	}

	if _, ok := app.config.get(panel.ID); ok {
		t.Error("面板配置应已删除")
	}
	for i, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("删除面板后，第 %d 个标签的 profile 目录应被整棵删除 (err=%v)", i+1, err)
		}
	}
}

// TestDeletePanelKeepsPanelWhenDataCannotBeCleared 锁定「清不干净就不删面板」这条：
// 宁可让用户看到「删不掉 + 原因」，也不允许出现「面板没了、登录数据还留在盘上」——
// 后者是静默的半完成状态，用户会以为已经清干净了。
func TestDeletePanelKeepsPanelWhenDataCannotBeCleared(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)

	originalRemove := profileRemove
	profileRemove = func(string) error { return os.ErrPermission }
	t.Cleanup(func() { profileRemove = originalRemove })

	if err := app.DeletePanel(panel.ID, false); err == nil {
		t.Error("数据清不掉时应返回错误，而不是照常把面板删掉")
	}
	if _, ok := app.config.get(panel.ID); !ok {
		t.Error("清空失败时面板必须保留 —— 不允许「面板没了但数据还在」")
	}
	for i, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("失败路径不应破坏第 %d 个标签的现有数据: %v", i+1, err)
		}
	}
}

// TestListPanelShortcutsForDeleteDialog 锁定确认对话框依赖的那条只读查询：
// 删除面板前用它列出可清理的 .lnk（决定可选项是否可用），它本身绝不能删任何东西。
func TestListPanelShortcutsForDeleteDialog(t *testing.T) {
	desktop := t.TempDir()
	restore := stubShortcutSeams(t, desktop)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "对话框用的面板")
	lnk := writePanelShortcut(t, desktop, "对话框.lnk", panel.ID)

	got, err := app.ListPanelShortcuts(panel.ID)
	if err != nil {
		t.Fatalf("ListPanelShortcuts: %v", err)
	}
	if len(got) != 1 || got[0] != lnk {
		t.Fatalf("应列出该面板的 .lnk，实际: %v", got)
	}
	// 只读：查完文件必须还在（对话框阶段用户可能点「取消」）。
	if _, err := os.Stat(lnk); err != nil {
		t.Errorf("ListPanelShortcuts 不应删除任何文件: %v", err)
	}

	if _, err := app.ListPanelShortcuts("不存在的面板"); err == nil {
		t.Error("面板不存在时应返回错误，而不是空清单")
	}
}
