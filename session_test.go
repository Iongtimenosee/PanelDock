package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// useTempPortableRoot 把会话目录解析到临时目录，返回 WebViewProfiles 根路径。
// 会话数据会真的写到磁盘，因此必须走注入缝 —— 否则测试就往用户的
// %LOCALAPPDATA%\PanelDock\WebViewProfiles 里写东西了（同类教训见 shortcut_windows.go 的注入缝）。
//
// 同时把删除重试的间隔压到 1ms：间隔只影响等待时长，不影响「删到不再出现」这套逻辑，
// 但用真实预算会让每条涉及清理的用例白等几百毫秒。
func useTempPortableRoot(t *testing.T) string {
	t.Helper()

	orig := resolvePortableRoot
	origDelay := profileClearDelay
	t.Cleanup(func() {
		resolvePortableRoot = orig
		profileClearDelay = origDelay
	})

	root := filepath.Join(t.TempDir(), "data")
	resolvePortableRoot = func() string { return root }
	profileClearDelay = time.Millisecond
	return filepath.Join(root, "WebViewProfiles")
}

// seedProfile 造一个「WebView2 用过」的标签目录：带一层 EBWebView\Default 子目录与文件。
// 用来验证清理是真的把整棵目录树删掉，而不是只删最外层。
func seedProfile(t *testing.T, tabID string) string {
	t.Helper()

	dir, err := tabProfileDir(tabID)
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	nested := filepath.Join(dir, "EBWebView", "Default")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("创建会话目录失败: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "Cookies"), []byte("session=abc"), 0o600); err != nil {
		t.Fatalf("写入会话文件失败: %v", err)
	}
	return dir
}

// TestPanelSessionModeDefaultsToPersist 验证默认是「保留浏览器状态」，
// 且老配置（没有 sessionMode 字段）读出来就是「保留」、**不触发任何回写**。
//
// 「不回写」是刻意设计：SessionMode 不做加载期迁移，因此启动程序不会给便携配置
// 添上这个字段 —— 否则每次启动都改一次 config.json，「跑完 E2E 配置零污染」这条验证就废了。
func TestPanelSessionModeDefaultsToPersist(t *testing.T) {
	store := newTestStore(t)

	created, err := store.create("路由器", "http://router.lan", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.SessionMode != SessionModePersist {
		t.Errorf("新建面板默认应为 %q，实际 %q", SessionModePersist, created.SessionMode)
	}
	if created.clearsSessionOnClose() {
		t.Error("新建面板不应是「关闭后清空」")
	}

	// 老配置：面板里没有 sessionMode；settings 节点必须**写全**。
	//
	// 这条用例断言的是「读取老配置一个字都不改」，而任何「默认开启」的键只要缺失，
	// 加载期就会补默认值并回写文件 —— 于是这份字面量成了隐性耦合：
	// 新增一个设置项而忘了同步这里，失败信息会指向 sessionMode（南辕北辙）。
	// 所以 settings 由默认值现取，不再手写。
	settingsJSON, err := json.Marshal(defaultAppSettings())
	if err != nil {
		t.Fatalf("编码默认设置失败: %v", err)
	}
	legacyPath := filepath.Join(t.TempDir(), "legacy.json")
	original := []byte(`{
  "schemaVersion": 1,
  "settings": ` + string(settingsJSON) + `,
  "panels": [
    {
      "id": "p1",
      "name": "旧面板",
      "tabs": [{"id": "t1", "name": "标签", "url": "http://old.lan"}],
      "enabled": true,
      "alwaysOnTop": false,
      "window": {"x": 0, "y": 0, "width": 800, "height": 600}
    }
  ]
}`)
	if err := os.WriteFile(legacyPath, original, 0o600); err != nil {
		t.Fatalf("写入老配置失败: %v", err)
	}

	legacy := newConfigStore()
	legacy.path = legacyPath
	if err := legacy.load(); err != nil {
		t.Fatalf("载入老配置失败: %v", err)
	}

	panel, ok := legacy.get("p1")
	if !ok {
		t.Fatal("面板丢失")
	}
	if panel.SessionMode != SessionModePersist || panel.clearsSessionOnClose() {
		t.Errorf("老配置应视为「保留」，实际 %q", panel.SessionMode)
	}
	// list() 也要归一化：前端拿到的永远是 persist / fresh 之一，不需要自己再猜默认值。
	if got := legacy.list()[0].SessionMode; got != SessionModePersist {
		t.Errorf("list() 应归一化为 %q，实际 %q", SessionModePersist, got)
	}

	after, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatalf("回读配置失败: %v", err)
	}
	if string(after) != string(original) {
		t.Errorf("读取老配置不应改写文件，实际写成了:\n%s", after)
	}
}

// TestSetPanelSessionModePersistsAndValidates 验证设置项的写入、持久化与取值校验。
func TestSetPanelSessionModePersistsAndValidates(t *testing.T) {
	store := newTestStore(t)
	panel, err := store.create("路由器", "http://router.lan", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := store.setSessionMode(panel.ID, SessionModeFresh); err != nil {
		t.Fatalf("setSessionMode(fresh): %v", err)
	}
	got, _ := store.get(panel.ID)
	if got.SessionMode != SessionModeFresh || !got.clearsSessionOnClose() {
		t.Errorf("应写入 %q，实际 %q", SessionModeFresh, got.SessionMode)
	}

	reloaded := newConfigStore()
	reloaded.path = store.path
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again, _ := reloaded.get(panel.ID); again.SessionMode != SessionModeFresh {
		t.Errorf("会话处理方式未持久化，实际 %q", again.SessionMode)
	}

	// 改回「保留」也要能落盘（两个方向都要能表达）。
	if err := store.setSessionMode(panel.ID, SessionModePersist); err != nil {
		t.Fatalf("setSessionMode(persist): %v", err)
	}
	got, _ = store.get(panel.ID)
	if got.SessionMode != SessionModePersist || got.clearsSessionOnClose() {
		t.Errorf("应回到 %q，实际 %q", SessionModePersist, got.SessionMode)
	}

	// 非法值与空值一律拒收：空值写进去就没法表达「保留」了。
	for _, bad := range []string{"", "delete", "FRESH", "fresh ", "clean"} {
		if err := store.setSessionMode(panel.ID, bad); err == nil {
			t.Errorf("非法取值 %q 不应被接受", bad)
		}
	}
	if got, _ := store.get(panel.ID); got.SessionMode != SessionModePersist {
		t.Errorf("非法取值不应改动配置，实际 %q", got.SessionMode)
	}

	if err := store.setSessionMode("不存在", SessionModeFresh); err == nil {
		t.Error("面板不存在时应报错")
	}
}

// TestClearPanelProfilesRemovesEveryTab 验证清空是「整棵目录树」且不越界：
// 目标面板所有标签的目录都没了，其他面板的目录与 WebViewProfiles 根目录原封不动。
func TestClearPanelProfilesRemovesEveryTab(t *testing.T) {
	profilesRoot := useTempPortableRoot(t)

	dirA := seedProfile(t, "tab-a")
	dirB := seedProfile(t, "tab-b")
	other := seedProfile(t, "tab-other")

	tabs := []PanelTab{
		{ID: "tab-a", Name: "标签A"},
		{ID: "tab-b", Name: "标签B"},
	}
	if err := clearPanelProfiles(tabs, clearUntilStable); err != nil {
		t.Fatalf("clearPanelProfiles: %v", err)
	}

	for _, dir := range []string{dirA, dirB} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("标签目录应被整个删除: %s (err=%v)", dir, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("其他面板的会话目录不应被波及: %v", err)
	}
	if _, err := os.Stat(profilesRoot); err != nil {
		t.Errorf("WebViewProfiles 根目录不应被删除: %v", err)
	}
}

// TestPreparePanelProfileRespectsSessionMode 验证打开面板前的 profile 准备：
// 「保留」原样沿用已有数据，「关闭后清空」在打开前抹掉残留并重建空目录。
func TestPreparePanelProfileRespectsSessionMode(t *testing.T) {
	useTempPortableRoot(t)

	kept := seedProfile(t, "tab-keep")
	dir, err := preparePanelProfile("tab-keep", false)
	if err != nil {
		t.Fatalf("preparePanelProfile(persist): %v", err)
	}
	if dir != kept {
		t.Errorf("保留模式应沿用同一目录，实际 %q，期望 %q", dir, kept)
	}
	if _, err := os.Stat(filepath.Join(kept, "EBWebView", "Default", "Cookies")); err != nil {
		t.Errorf("保留模式不应清空已有会话数据: %v", err)
	}

	dirty := seedProfile(t, "tab-fresh")
	dir, err = preparePanelProfile("tab-fresh", true)
	if err != nil {
		t.Fatalf("preparePanelProfile(fresh): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirty, "EBWebView", "Default", "Cookies")); !os.IsNotExist(err) {
		t.Errorf("清空模式应在打开前抹掉残留会话数据 (err=%v)", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("清空后应重建出空的 profile 目录: %v", err)
	}
}

// TestRemoveProfileDirTreatsMissingAsSuccess 验证「目录本来就不存在」不算失败 ——
// 关闭时清理与打开前兜底会前后各清一次，第二次必然面对一个不存在的目录。
func TestRemoveProfileDirTreatsMissingAsSuccess(t *testing.T) {
	useTempPortableRoot(t)

	dir, err := tabProfileDir("never-existed")
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	if err := removeProfileDir(dir, clearUntilStable); err != nil {
		t.Errorf("目录不存在应视为成功: %v", err)
	}
	if err := removeProfileDir("", clearUntilStable); err != nil {
		t.Errorf("空路径应直接返回成功: %v", err)
	}
}

// stubProfileClear 注入删除动作与「是否已消失」的判定，返回还原函数。
// 用它可以精确编排「删掉 → 又被建回来 → 再删掉 → 稳定」的时序，
// 不必真的去制造文件占用（也制造不出来：Go 打开文件默认带 FILE_SHARE_DELETE）。
func stubProfileClear(t *testing.T, remove func(string) error, gone func(string) bool) {
	t.Helper()

	origRemove, origGone := profileRemove, profileGone
	t.Cleanup(func() {
		profileRemove = origRemove
		profileGone = origGone
	})
	profileRemove = remove
	profileGone = gone
}

// TestRemoveProfileDirDeletesUntilItStaysGone 验证核心行为：
// 目录被删掉之后**又出现**（实测中尚未退干净的 WebView2 浏览器进程会重建它）时必须继续删，
// 只有连续若干次检查都确认不存在才算清干净。
//
// 这条回归的来历：最初的实现「删一次就撒手」，端到端用例当场发现目录在关闭后带着一整棵
// EBWebView 树重新出现 —— 相当于会话可能留下残留，正是这个功能要避免的事。
func TestRemoveProfileDirDeletesUntilItStaysGone(t *testing.T) {
	useTempPortableRoot(t)

	dir, err := tabProfileDir("tab-racy")
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}

	// 前两次检查都报告「目录还在」（模拟浏览器重建两次），之后才稳定消失。
	removals := 0
	checks := 0
	stubProfileClear(t,
		func(string) error { removals++; return nil },
		func(string) bool {
			checks++
			return checks > 2
		},
	)

	if err := removeProfileDir(dir, clearUntilStable); err != nil {
		t.Fatalf("反复重建后最终稳定时应返回成功: %v", err)
	}
	// 两次「重建」各要再删一次，收尾还要连续 profileClearQuietChecks 次确认才算干净 ——
	// 不能一删完就宣布搞定，也不能只在最后确认一次。
	if want := 2 + profileClearQuietChecks; removals != want {
		t.Errorf("删除次数应为 %d（重建 2 次 + 确认 %d 次），实际 %d",
			want, profileClearQuietChecks, removals)
	}
	if checks != removals {
		t.Errorf("每次删除后都应确认一次：删除 %d 次、确认 %d 次", removals, checks)
	}

	// clearOnce（打开前兜底用的策略）不等待：那一刻没有浏览器进程，删一次就返回。
	removals = 0
	stubProfileClear(t,
		func(string) error { removals++; return nil },
		func(string) bool { return true },
	)
	if err := removeProfileDir(dir, clearOnce); err != nil {
		t.Fatalf("clearOnce 应直接成功: %v", err)
	}
	if removals != 1 {
		t.Errorf("clearOnce 应只删一次、不等待静默期，实际删了 %d 次", removals)
	}
}

// TestRemoveProfileDirReportsFailure 验证一直删不掉时如实返回错误：
// 「清空」这条承诺不能默默落空 —— 调用方要靠它决定是记日志还是拒绝打开面板。
func TestRemoveProfileDirReportsFailure(t *testing.T) {
	useTempPortableRoot(t)

	dir, err := tabProfileDir("tab-locked")
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}

	removals := 0
	stubProfileClear(t,
		func(string) error { removals++; return errors.New("sharing violation") },
		func(string) bool { return false },
	)

	if err := removeProfileDir(dir, clearUntilStable); err == nil {
		t.Error("一直删不掉时应返回错误")
	}
	if removals != profileClearAttempts {
		t.Errorf("应把预算用满（%d 次），实际尝试 %d 次", profileClearAttempts, removals)
	}

	// 另一种失败形态：删除动作报告成功，但目录始终还在。
	removals = 0
	stubProfileClear(t,
		func(string) error { removals++; return nil },
		func(string) bool { return false },
	)
	if err := removeProfileDir(dir, clearUntilStable); err == nil {
		t.Error("目录删完就出现（始终未消失）时应返回错误")
	}

	// 「连续确认」不是连查几次，而是真的静默一段时间：两次确认之间必须等待，
	// 否则连续两次瞬时检查什么也证明不了。
	delay := profileClearDelay
	profileClearDelay = 20 * time.Millisecond
	t.Cleanup(func() { profileClearDelay = delay })

	removals = 0
	start := time.Now()
	stubProfileClear(t,
		func(string) error { removals++; return nil },
		func(string) bool { return true },
	)
	if err := removeProfileDir(dir, clearUntilStable); err != nil {
		t.Fatalf("目录已不存在时应直接成功: %v", err)
	}
	if removals != profileClearQuietChecks {
		t.Errorf("确认清干净需要 %d 次删除调用，实际 %d 次", profileClearQuietChecks, removals)
	}
	if want := time.Duration(profileClearQuietChecks-1) * profileClearDelay; time.Since(start) < want {
		t.Errorf("确认之间应有静默等待（期望 ≥ %v），实际 %v", want, time.Since(start))
	}
}
