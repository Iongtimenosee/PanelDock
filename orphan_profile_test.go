//go:build windows

package main

import (
	"os"
	"testing"
)

// 孤儿 profile 扫描/清理：盘上有目录、配置里无对应标签 → 孤儿；
// 配置里标签的目录一个不能动。
func TestListAndCleanOrphanProfiles(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app) // 两个标签 + 各自 profile

	// 一个孤儿目录（模拟早期版本删标签留下的残留）。
	orphan := seedProfile(t, "tab-orphan-legacy")

	orphans, err := app.ListOrphanProfiles()
	if err != nil {
		t.Fatalf("ListOrphanProfiles: %v", err)
	}
	if len(orphans) != 1 || orphans[0] != "tab-orphan-legacy" {
		t.Fatalf("应只发现孤儿目录 tab-orphan-legacy，实际 %v", orphans)
	}

	n, err := app.CleanOrphanProfiles()
	if err != nil {
		t.Fatalf("CleanOrphanProfiles: %v", err)
	}
	if n != 1 {
		t.Errorf("应报告清理 1 个，实际 %d", n)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Errorf("孤儿目录应被删除 (err=%v)", err)
	}
	for i, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("第 %d 个在用标签的 profile 不应被动到: %v", i+1, err)
		}
	}

	// 清完再扫：空。
	if orphans, err = app.ListOrphanProfiles(); err != nil || len(orphans) != 0 {
		t.Errorf("清理后不应再检出孤儿，实际 %v (err=%v)", orphans, err)
	}

	// 面板配置不受影响。
	if _, ok := app.config.get(panel.ID); !ok {
		t.Error("清理孤儿不应删除任何面板配置")
	}
}

// 根目录不存在（从未开过任何标签）时应安静返回空，而不是报错。
func TestListOrphanProfilesMissingRoot(t *testing.T) {
	app := newResetTestApp(t) // 临时根目录，尚无任何 profile

	orphans, err := app.ListOrphanProfiles()
	if err != nil {
		t.Fatalf("根目录不存在不应报错: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("应返回空列表，实际 %v", orphans)
	}
}
