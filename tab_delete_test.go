//go:build windows

package main

import (
	"os"
	"testing"
)

// 删标签的数据清理语义测试：被移除标签的 profile 目录必须从盘上抹掉，
// 保留的标签一个字节都不能动。与 reset_panel_test 同一套注入方式
//（临时便携根目录 + 真实文件系统，不碰 Win32 —— 窗口没开时
// removeTab 直接返回 false，走的就是纯清理路径）。

// TestUpdatePanelTabsRemovesRemovedTabProfiles 验证编辑保存时被移除的标签：
// 它的浏览器数据被删、保留标签的数据原样、配置只剩保留的标签。
func TestUpdatePanelTabsRemovesRemovedTabProfiles(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)
	cfg, _ := app.config.get(panel.ID)

	// 保存时只保留第一个标签（第二个被移除）。
	kept := []PanelTab{cfg.Tabs[0]}
	if err := app.UpdatePanelTabs(panel.ID, kept); err != nil {
		t.Fatalf("UpdatePanelTabs: %v", err)
	}

	if _, err := os.Stat(dirs[1]); !os.IsNotExist(err) {
		t.Errorf("被移除标签的 profile 目录应被删除 (err=%v)", err)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		t.Errorf("保留标签的 profile 目录不应被删除: %v", err)
	}

	after, ok := app.config.get(panel.ID)
	if !ok {
		t.Fatal("面板不应被删除")
	}
	if len(after.Tabs) != 1 || after.Tabs[0].ID != cfg.Tabs[0].ID {
		t.Errorf("配置应只剩保留的标签，实际 %+v", after.Tabs)
	}
}

// TestUpdatePanelTabsKeepsProfilesWhenOnlyRenamed 验证纯改名保存不清任何数据。
func TestUpdatePanelTabsKeepsProfilesWhenOnlyRenamed(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)
	cfg, _ := app.config.get(panel.ID)

	renamed := []PanelTab{
		{ID: cfg.Tabs[0].ID, Name: "新名字一", URL: cfg.Tabs[0].URL},
		{ID: cfg.Tabs[1].ID, Name: "新名字二", URL: cfg.Tabs[1].URL},
	}
	if err := app.UpdatePanelTabs(panel.ID, renamed); err != nil {
		t.Fatalf("UpdatePanelTabs: %v", err)
	}

	for i, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("第 %d 个标签只是改名，profile 不应被动到: %v", i+1, err)
		}
	}
}

// TestDeleteTabClearsTabProfile 验证 DeleteTab：标签从配置消失，profile 整棵删除。
func TestDeleteTabClearsTabProfile(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)
	cfg, _ := app.config.get(panel.ID)

	if err := app.DeleteTab(panel.ID, cfg.Tabs[1].ID); err != nil {
		t.Fatalf("DeleteTab: %v", err)
	}

	if _, err := os.Stat(dirs[1]); !os.IsNotExist(err) {
		t.Errorf("被删标签的 profile 目录应被删除 (err=%v)", err)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		t.Errorf("未删标签的 profile 不应受影响: %v", err)
	}

	after, ok := app.config.get(panel.ID)
	if !ok {
		t.Fatal("面板不应被删除")
	}
	if len(after.Tabs) != 1 || after.Tabs[0].ID != cfg.Tabs[0].ID {
		t.Errorf("配置应只剩未删的标签，实际 %+v", after.Tabs)
	}
}
