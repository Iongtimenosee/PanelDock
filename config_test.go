package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigStoreCRUD(t *testing.T) {
	dir := t.TempDir()
	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")
	if err := os.WriteFile(store.path, []byte(`{"schemaVersion":1,"panels":[]}`), 0o600); err != nil {
		t.Fatalf("seed empty config: %v", err)
	}

	if err := store.load(); err != nil {
		t.Fatalf("load empty config: %v", err)
	}
	if len(store.list()) != 0 {
		t.Fatalf("expected empty config, got %d panels", len(store.list()))
	}

	p1, err := store.create("路由管理", "http://192.168.1.1", true)
	if err != nil {
		t.Fatalf("create panel: %v", err)
	}
	if p1.ID == "" || p1.Name != "路由管理" {
		t.Fatalf("unexpected created panel: %+v", p1)
	}
	// 验证创建时自动生成 Tab
	if len(p1.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(p1.Tabs))
	}
	if p1.Tabs[0].URL != "http://192.168.1.1" || p1.Tabs[0].Name != "路由管理" {
		t.Fatalf("unexpected tab: %+v", p1.Tabs[0])
	}

	p2, err := store.create("NAS", "https://nas.lan", true)
	if err != nil {
		t.Fatalf("create second panel: %v", err)
	}
	if p1.ID == p2.ID {
		t.Fatalf("panel ids must be unique")
	}

	// 无效 URL 应被拒绝
	if _, err := store.create("坏地址", "not a url", true); err == nil {
		t.Fatalf("expected invalid url to be rejected")
	}

	// 更新
	updated, err := store.update(p1.ID, "路由-新", "http://192.168.1.2", false)
	if err != nil {
		t.Fatalf("update panel: %v", err)
	}
	if updated.Name != "路由-新" || updated.Enabled {
		t.Fatalf("unexpected updated panel: %+v", updated)
	}
	// 第一个 Tab 应同步更新
	if updated.Tabs[0].URL != "http://192.168.1.2" || updated.Tabs[0].Name != "路由-新" {
		t.Fatalf("tab not updated: %+v", updated.Tabs[0])
	}

	// 窗口状态记忆
	if err := store.updateWindowState(p1.ID, PanelWindowState{X: 10, Y: 20, Width: 800, Height: 600}); err != nil {
		t.Fatalf("update window state: %v", err)
	}
	got, ok := store.get(p1.ID)
	if !ok {
		t.Fatalf("panel not found after window state update")
	}
	if got.Window.X != 10 || got.Window.Width != 800 {
		t.Fatalf("window state not persisted: %+v", got.Window)
	}

	// 置顶开关
	if err := store.setAlwaysOnTop(p1.ID, true); err != nil {
		t.Fatalf("set always on top: %v", err)
	}
	got, _ = store.get(p1.ID)
	if !got.AlwaysOnTop {
		t.Fatalf("flags not persisted: %+v", got)
	}

	// 删除
	if err := store.delete(p1.ID); err != nil {
		t.Fatalf("delete panel: %v", err)
	}
	if len(store.list()) != 1 {
		t.Fatalf("expected 1 panel after delete, got %d", len(store.list()))
	}

	// 重新加载验证持久化
	store2 := newConfigStore()
	store2.path = filepath.Join(dir, "config.json")
	if err := store2.load(); err != nil {
		t.Fatalf("reload config: %v", err)
	}
	list := store2.list()
	if len(list) != 1 || list[0].ID != p2.ID {
		t.Fatalf("reloaded config mismatch: %+v", list)
	}
}

func TestConfigStoreDefaults(t *testing.T) {
	dir := t.TempDir()
	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")
	if err := os.WriteFile(store.path, []byte(`{"schemaVersion":1,"panels":[]}`), 0o600); err != nil {
		t.Fatalf("seed empty config: %v", err)
	}
	if err := store.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	p, err := store.create("默认", "http://127.0.0.1:8080", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !p.Enabled || p.AlwaysOnTop {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if p.Window.Width != 1120 || p.Window.Height != 760 {
		t.Fatalf("default window size should be 1120x760: %+v", p.Window)
	}
}

func TestConfigStoreCorrupt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte("{corrupt json"), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}

	store := newConfigStore()
	store.path = path
	if err := store.load(); err != nil {
		t.Fatalf("load should tolerate corrupt config: %v", err)
	}
	// 损坏配置应回退为默认配置（含演示面板）。
	if len(store.list()) != 1 {
		t.Fatalf("corrupt config should fall back to default panel list, got %d", len(store.list()))
	}
}

func TestConfigMigration(t *testing.T) {
	dir := t.TempDir()
	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")

	// 模拟旧版配置：使用顶层 URL 字段，无 Tabs。
	oldConfig := `{
		"schemaVersion": 1,
		"panels": [
			{
				"id": "test-id-1",
				"name": "旧面板",
				"url": "http://old.example.com",
				"enabled": true,
				"alwaysOnTop": false,
				"minimizeToTray": true
			}
		]
	}`
	if err := os.WriteFile(store.path, []byte(oldConfig), 0o600); err != nil {
		t.Fatalf("seed old config: %v", err)
	}

	if err := store.load(); err != nil {
		t.Fatalf("load old config: %v", err)
	}

	list := store.list()
	if len(list) != 1 {
		t.Fatalf("expected 1 panel, got %d", len(list))
	}

	panel := list[0]
	if len(panel.Tabs) != 1 {
		t.Fatalf("expected 1 tab after migration, got %d", len(panel.Tabs))
	}
	if panel.Tabs[0].URL != "http://old.example.com" {
		t.Fatalf("tab URL mismatch after migration: %s", panel.Tabs[0].URL)
	}
	if panel.Tabs[0].Name != "旧面板" {
		t.Fatalf("tab name mismatch after migration: %s", panel.Tabs[0].Name)
	}
	if panel.Tabs[0].ID == "" {
		t.Fatalf("tab ID should be generated during migration")
	}
	// 旧 URL 字段应被清空。
	if panel.DeprecatedURL != "" {
		t.Fatalf("old URL field should be cleared after migration: %s", panel.DeprecatedURL)
	}
	if panel.DeprecatedMinimizeToTray != nil {
		t.Fatalf("旧版面板级 minimizeToTray 应被清掉: %v", *panel.DeprecatedMinimizeToTray)
	}

	// 废弃的键不能留在配置文件里：功能已收归应用级设置，留着只会让人以为它还有用。
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("回读配置失败: %v", err)
	}
	if strings.Contains(string(data), "minimizeToTray") {
		t.Errorf("废弃的面板级 minimizeToTray 键应从配置文件里消失: %s", data)
	}
}

// TestConfigMigrationShortcutsArrayToOne 锁定「一个分组桌面只留一个快捷方式」的旧配置迁移。
//
// 旧版每点一次「桌面快捷方式」就新建一份 .lnk，配置里于是攒成一串路径
// （2026-09-30 用户在自己的 config.json 里看到的就是这个）。载入时收敛成第一个
// 仍然存在的那个，并把 shortcuts 数组从配置文件里抹掉 —— 留着它下次保存又写回去了。
func TestConfigMigrationShortcutsArrayToOne(t *testing.T) {
	dir := t.TempDir()
	desktop := filepath.Join(dir, "桌面")
	if err := os.MkdirAll(desktop, 0o755); err != nil {
		t.Fatal(err)
	}
	// 第一个路径还在，第二个已经被用户删了 —— 迁移必须挑存在的那个，不能挑到空气。
	alive := filepath.Join(desktop, "路由器.lnk")
	if err := os.WriteFile(alive, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(desktop, "路由器 (2).lnk")

	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")
	oldConfig := fmt.Sprintf(`{
		"schemaVersion": 1,
		"panels": [
			{
				"id": "panel-old-shortcuts",
				"name": "旧快捷方式",
				"enabled": true,
				"alwaysOnTop": false,
				"tabs": [{"id":"tab-1","name":"旧快捷方式","url":"http://nas.lan/"}],
				"shortcuts": [%q, %q, ""]
			}
		]
	}`, alive, gone)
	if err := os.WriteFile(store.path, []byte(oldConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	panel, ok := store.get("panel-old-shortcuts")
	if !ok {
		t.Fatal("面板不该消失")
	}
	if panel.Shortcut != alive {
		t.Errorf("收敛后的快捷方式 = %q，期望仍然存在的那个 %q", panel.Shortcut, alive)
	}
	if panel.DeprecatedShortcuts != nil {
		t.Errorf("旧数组应被清空: %v", panel.DeprecatedShortcuts)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"shortcuts"`) {
		t.Errorf("旧的 shortcuts 数组不该留在配置文件里: %s", data)
	}
	if !strings.Contains(string(data), `"shortcut":`) {
		t.Errorf("收敛结果应写进配置文件: %s", data)
	}
}

// TestPortableMode 验证便携模式（exe 同目录存在 data 文件夹）下配置与会话目录的解析。
func TestPortableMode(t *testing.T) {
	orig := resolvePortableRoot
	defer func() { resolvePortableRoot = orig }()

	root := filepath.Join(t.TempDir(), "data")
	resolvePortableRoot = func() string { return root }

	if got, want := defaultConfigPath(), filepath.Join(root, "config.json"); got != want {
		t.Errorf("便携模式配置路径 = %q, want %q", got, want)
	}
	profileDir, err := tabProfileDir("tab-abc")
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	if want := filepath.Join(root, "WebViewProfiles", "tab-abc"); profileDir != want {
		t.Errorf("便携模式会话目录 = %q, want %q", profileDir, want)
	}
}

// TestNonPortableMode 验证无 data 文件夹时回退系统目录且不再依赖 exe 位置。
func TestNonPortableMode(t *testing.T) {
	orig := resolvePortableRoot
	defer func() { resolvePortableRoot = orig }()
	resolvePortableRoot = func() string { return "" }

	cfgPath := defaultConfigPath()
	base, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("UserConfigDir 不可用: %v", err)
	}
	if want := filepath.Join(base, "PanelDock", "config.json"); cfgPath != want {
		t.Errorf("非便携配置路径 = %q, want %q", cfgPath, want)
	}

	profileDir, err := tabProfileDir("tab-abc")
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("UserCacheDir 不可用: %v", err)
	}
	if want := filepath.Join(cache, "PanelDock", "WebViewProfiles", "tab-abc"); profileDir != want {
		t.Errorf("非便携会话目录 = %q, want %q", profileDir, want)
	}
}

// newTestStore 创建一个指向临时目录、已载入空配置的 configStore。
func newTestStore(t *testing.T) *configStore {
	t.Helper()

	store := newConfigStore()
	store.path = filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(store.path, []byte(`{"schemaVersion":1,"panels":[]}`), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	if err := store.load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	return store
}

// TestAppSettingsDefaults 验证老配置（无 settings 节点）套用默认值并写回文件。
func TestAppSettingsDefaults(t *testing.T) {
	store := newTestStore(t)

	got := store.settings()
	if !got.ShowTrayIcon {
		t.Error("默认应显示托盘图标")
	}
	if got.PanelCloseAction != CloseActionAsk {
		t.Errorf("默认关闭行为应为 %q，实际 %q", CloseActionAsk, got.PanelCloseAction)
	}
	if !got.LightweightQuitOnLastPanel {
		t.Error("默认应开启「轻量模式下关掉最后一个面板后退出程序」（用完即走）")
	}

	// 默认值应写回文件，让「托盘图标 / 关闭行为」在管理界面里可见可改。
	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	if !strings.Contains(string(data), `"showTrayIcon": true`) {
		t.Errorf("默认设置未写回配置: %s", data)
	}
	if !strings.Contains(string(data), `"panelCloseAction": "ask"`) ||
		!strings.Contains(string(data), `"lightweightQuitOnLastPanel": true`) {
		t.Errorf("默认设置未写回配置: %s", data)
	}
	// 管理窗口的关闭行为是固定动作，配置里不该再有这一项。
	if strings.Contains(string(data), "managerCloseAction") {
		t.Errorf("配置里不应再出现 managerCloseAction: %s", data)
	}
}

// TestAppSettingsPersist 验证应用级设置持久化。
// 注意：关闭行为不能是 tray —— 那会触发「托盘图标必须开着」的不变量，
// 单独由 TestTrayIconRequiredByMinimizeToTray 覆盖。
func TestAppSettingsPersist(t *testing.T) {
	store := newTestStore(t)

	if err := store.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if err := store.setCloseAction(CloseActionClose); err != nil {
		t.Fatalf("setCloseAction: %v", err)
	}
	if err := store.setLightweightQuitOnLastPanel(false); err != nil {
		t.Fatalf("setLightweightQuitOnLastPanel: %v", err)
	}

	reloaded := newConfigStore()
	reloaded.path = store.path
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	got := reloaded.settings()
	if got.ShowTrayIcon {
		t.Error("showTrayIcon=false 未持久化")
	}
	if got.PanelCloseAction != CloseActionClose {
		t.Errorf("panelCloseAction = %q, want %q", got.PanelCloseAction, CloseActionClose)
	}
	if got.LightweightQuitOnLastPanel {
		t.Error("lightweightQuitOnLastPanel=false 未持久化")
	}
}

// TestTrayIconRequiredByMinimizeToTray 验证「面板窗口会最小化到托盘 → 托盘图标必须开着」。
//
// 这条例外的由来：用户配置里真实出现过 showTrayIcon=false 与「最小化到托盘」的矛盾组合 ——
// 窗口一关闭就藏进托盘，而托盘里没有图标，那个窗口再也找不回来。
// 因此：选了「最小化到托盘」图标自动打开，此时取消勾选必须被明确拒绝。
func TestTrayIconRequiredByMinimizeToTray(t *testing.T) {
	store := newTestStore(t)

	// 还是 ask：取消托盘图标是允许的（不变量只由「最小化到托盘」触发）。
	if err := store.setShowTrayIcon(false); err != nil {
		t.Fatalf("没有窗口会最小化到托盘时，取消托盘图标应被允许: %v", err)
	}

	if err := store.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction: %v", err)
	}
	if !store.settings().ShowTrayIcon {
		t.Fatal("选中「最小化到托盘」后托盘图标应自动打开")
	}

	// 取消勾选必须被拒绝，并且拒绝不留下任何改动。
	if err := store.setShowTrayIcon(false); err != ErrTrayIconRequired {
		t.Fatalf("应拒绝取消托盘图标，实际 err = %v", err)
	}
	if got := store.settings(); !got.ShowTrayIcon || got.closeAction() != CloseActionTray {
		t.Fatalf("拒绝后设置不应被改动: %+v", got)
	}

	// 改掉那个关闭行为之后，取消勾选重新变得可行。
	if err := store.setCloseAction(CloseActionAsk); err != nil {
		t.Fatalf("setCloseAction(ask): %v", err)
	}
	if err := store.setShowTrayIcon(false); err != nil {
		t.Fatalf("没有窗口会最小化到托盘时应允许取消: %v", err)
	}
	if store.settings().ShowTrayIcon {
		t.Fatal("取消勾选未生效")
	}

	// 写入路径的不变量在重启后依然成立（落盘的是 true）。
	if err := store.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}
	reloaded := newConfigStore()
	reloaded.path = store.path
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.settings().ShowTrayIcon {
		t.Error("自动打开的托盘图标未落盘")
	}
}

// TestConfigLoadHealsContradictoryTrayState 验证矛盾组合在加载时被纠正并写回文件。
// 手工改过配置、或用过旧版本的用户都可能留下 showTrayIcon=false + tray 的组合。
func TestConfigLoadHealsContradictoryTrayState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	seed := `{"schemaVersion":1,"settings":{"showTrayIcon":false,"panelCloseAction":"tray"},"panels":[]}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("写入矛盾配置失败: %v", err)
	}

	store := newConfigStore()
	store.path = path
	if err := store.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	if !store.settings().ShowTrayIcon {
		t.Error("加载后托盘图标必须为开启状态")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("回读配置失败: %v", err)
	}
	if !strings.Contains(string(data), `"showTrayIcon": true`) {
		t.Errorf("纠正结果未写回配置: %s", data)
	}

	// 反向确认：关闭行为不是 tray 时，关闭状态必须被尊重（不能顺手改成 true）。
	path = filepath.Join(t.TempDir(), "config.json")
	seed = `{"schemaVersion":1,"settings":{"showTrayIcon":false,"panelCloseAction":"close"},"panels":[]}`
	if err := os.WriteFile(path, []byte(seed), 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	store = newConfigStore()
	store.path = path
	if err := store.load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if store.settings().ShowTrayIcon {
		t.Error("没有窗口会最小化到托盘时，showTrayIcon=false 必须被尊重")
	}
}

// TestAppSettingsMigratesLegacyFields 验证两个旧字段的迁移：
//   - 旧版单一 closeAction（两类窗口共用一项）落到面板窗口那一项，
//     quit（面板窗口也会退出整个程序）归一化为 close；
//   - 旧版 managerCloseAction 已无对应功能，直接清掉。
//
// 两者都写回文件并清空旧字段，下次启动无需再迁移。
func TestAppSettingsMigratesLegacyFields(t *testing.T) {
	store := newTestStore(t)

	if err := os.WriteFile(
		store.path,
		[]byte(`{"schemaVersion":1,"settings":{"showTrayIcon":true,"closeAction":"quit","managerCloseAction":"tray"},"panels":[]}`),
		0o600,
	); err != nil {
		t.Fatalf("write legacy config: %v", err)
	}

	legacy := newConfigStore()
	legacy.path = store.path
	if err := legacy.load(); err != nil {
		t.Fatalf("load legacy config: %v", err)
	}
	got := legacy.settings()
	if got.PanelCloseAction != CloseActionClose {
		t.Errorf("旧版 quit 应迁移为 %q，实际 %q", CloseActionClose, got.PanelCloseAction)
	}
	// 旧 managerCloseAction=tray 不能被当成面板设置（那会强行打开托盘图标）。
	if !got.ShowTrayIcon {
		t.Error("迁移不应改动 showTrayIcon")
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	if !strings.Contains(string(data), `"panelCloseAction": "close"`) {
		t.Errorf("迁移结果未写回配置: %s", data)
	}
	if strings.Contains(string(data), `"closeAction"`) || strings.Contains(string(data), "managerCloseAction") {
		t.Errorf("旧字段迁移后应被清空: %s", data)
	}

	// 旧值不允许再被写入。
	if err := legacy.setCloseAction(legacyCloseActionQuit); err == nil {
		t.Error("旧版 quit 不应允许写入")
	}
}

// TestAppSettingsDropsManagerCloseAction 验证旧版「关闭管理面板时」设置被清掉，
// 且**不清空面板窗口自己的那一项** —— 两者曾经并排存在，迁移不能顺手改坏另一项。
func TestAppSettingsDropsManagerCloseAction(t *testing.T) {
	store := newTestStore(t)

	if err := os.WriteFile(
		store.path,
		[]byte(`{"schemaVersion":1,"settings":{"showTrayIcon":true,"panelCloseAction":"close","managerCloseAction":"tray"},"panels":[]}`),
		0o600,
	); err != nil {
		t.Fatalf("write split config: %v", err)
	}

	split := newConfigStore()
	split.path = store.path
	if err := split.load(); err != nil {
		t.Fatalf("load split config: %v", err)
	}
	if got := split.settings(); got.PanelCloseAction != CloseActionClose {
		t.Errorf("面板窗口的关闭行为不应被改动，实际 %q", got.PanelCloseAction)
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	if strings.Contains(string(data), "managerCloseAction") {
		t.Errorf("旧字段应从配置里消失: %s", data)
	}
}

// TestAppSettingsBackfillsNewKeys 验证「settings 节点存在、但缺少后续版本新增的键」时，
// 默认开启的项按默认值补齐并写回。
//
// 反序列化分不清「键缺失」与「显式 false」，缺键读出来就是零值 false —— 直接留用会替老用户
// 把功能悄悄关掉（老配置下的轻量模式关掉最后一个面板后不再退出进程，E2E 实测踩到）。
func TestAppSettingsBackfillsNewKeys(t *testing.T) {
	store := newTestStore(t)

	// settings 节点在（走不到「整节点缺失 → 套默认值」那条路），但没有 lightweightQuitOnLastPanel。
	if err := os.WriteFile(
		store.path,
		[]byte(`{"schemaVersion":1,"settings":{"showTrayIcon":true,"panelCloseAction":"ask"},"panels":[]}`),
		0o600,
	); err != nil {
		t.Fatalf("write config without new key: %v", err)
	}

	fresh := newConfigStore()
	fresh.path = store.path
	if err := fresh.load(); err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !fresh.settings().LightweightQuitOnLastPanel {
		t.Error("缺少 lightweightQuitOnLastPanel 键的老配置应按默认值补齐（开启）")
	}

	data, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatalf("read back config: %v", err)
	}
	if !strings.Contains(string(data), `"lightweightQuitOnLastPanel": true`) {
		t.Errorf("补上的默认值未写回配置: %s", data)
	}

	// 用户显式关掉之后，重新加载必须尊重这个 false：补默认值只能补「缺的键」，
	// 不能反过来把用户的选择覆盖掉。
	if err := fresh.setLightweightQuitOnLastPanel(false); err != nil {
		t.Fatalf("setLightweightQuitOnLastPanel: %v", err)
	}
	again := newConfigStore()
	again.path = store.path
	if err := again.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if again.settings().LightweightQuitOnLastPanel {
		t.Error("显式 false 应被尊重，不能被默认值覆盖")
	}
}

// TestAppSettingsRejectsInvalidCloseAction 验证非法关闭行为既写不进、读出来也会回退。
func TestAppSettingsRejectsInvalidCloseAction(t *testing.T) {
	store := newTestStore(t)

	if err := store.setCloseAction("explode"); err == nil {
		t.Error("非法关闭行为应返回错误")
	}
	if got := store.settings().PanelCloseAction; got != CloseActionAsk {
		t.Errorf("非法值不应写入，实际 %q", got)
	}

	// 手工塞入非法值：读取时应回退为「每次询问」。
	if err := os.WriteFile(
		store.path,
		[]byte(`{"schemaVersion":1,"settings":{"showTrayIcon":true,"closeAction":"bogus"},"panels":[]}`),
		0o600,
	); err != nil {
		t.Fatalf("write bogus config: %v", err)
	}

	bad := newConfigStore()
	bad.path = store.path
	if err := bad.load(); err != nil {
		t.Fatalf("load bogus config: %v", err)
	}
	if got := bad.settings(); got.PanelCloseAction != CloseActionAsk {
		t.Errorf("非法关闭行为应回退 ask，实际 %q", got.PanelCloseAction)
	}
}

// TestDecideCloseUsesRememberedAction 验证已记住的关闭行为直接生效、不再弹窗。
// 该分支不创建对话框，因此可安全地在测试进程内断言。
func TestDecideCloseUsesRememberedAction(t *testing.T) {
	store := newTestStore(t)
	app := &App{config: store}

	if err := store.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction: %v", err)
	}
	if got := app.decideClose(0, ""); got.Action != CloseActionTray || got.Remember {
		t.Errorf("应取已记住的 tray，实际 %+v", got)
	}
}

// TestCommitCloseChoice 验证「记住我的选择」只在勾选时写回设置。
func TestCommitCloseChoice(t *testing.T) {
	store := newTestStore(t)
	app := &App{config: store}

	app.commitCloseChoice(closeDecision{Action: CloseActionTray, Remember: false})
	if got := store.settings(); got.PanelCloseAction != CloseActionAsk {
		t.Errorf("未勾选「记住」时不应改设置，实际 %q", got.PanelCloseAction)
	}

	app.commitCloseChoice(closeDecision{Action: CloseActionTray, Remember: true})
	got := store.settings()
	if got.PanelCloseAction != CloseActionTray {
		t.Errorf("勾选「记住」后应写入 panelCloseAction=tray，实际 %q", got.PanelCloseAction)
	}

	// 取消（Action 为空）即使勾选了「记住」也不应写设置。
	app.commitCloseChoice(closeDecision{Action: "", Remember: true})
	if got := store.settings(); got.PanelCloseAction != CloseActionTray {
		t.Errorf("取消不应改设置，实际 %q", got.PanelCloseAction)
	}
}

// TestPanelPasswordAutosaveDefaults 验证「保存登录密码」的默认与归一化规则。
//
// 这个开关是**默认开启**的字符串枚举，不是 bool：老配置里没有 `passwordAutosave` 这个键，
// 若用 bool 就会读成 false 把所有老用户的分组悄悄关掉。用字符串则「空串 = 默认」自然成立。
func TestPanelPasswordAutosaveDefaults(t *testing.T) {
	store := newTestStore(t)

	panel, err := store.create("路由后台", "https://router.lan", true)
	if err != nil {
		t.Fatalf("create panel: %v", err)
	}
	if panel.PasswordAutosave != PasswordAutosaveOn {
		t.Errorf("新建分组应显式写入 %q，实际 %q", PasswordAutosaveOn, panel.PasswordAutosave)
	}
	if !panel.savesPasswords() {
		t.Error("新建分组应默认保存密码")
	}

	cases := []struct {
		stored string
		want   bool
	}{
		{"", true},                   // 老配置缺这个键
		{PasswordAutosaveOn, true},   // 显式开启
		{PasswordAutosaveOff, false}, // 显式关闭
		{"ON", true},                 // 大小写变体不是合法写入值，但读出来按默认处理
		{"whatever", true},           // 非法值同样按默认
	}
	for _, c := range cases {
		cfg := PanelConfig{PasswordAutosave: c.stored}
		if got := cfg.savesPasswords(); got != c.want {
			t.Errorf("passwordAutosave=%q 时应 savesPasswords()==%v，实际 %v", c.stored, c.want, got)
		}

		want := PasswordAutosaveOn
		if !c.want {
			want = PasswordAutosaveOff
		}
		if got := normalizePasswordAutosave(c.stored); got != want {
			t.Errorf("normalizePasswordAutosave(%q) = %q，期望 %q", c.stored, got, want)
		}
	}
}

// TestPanelPasswordAutosavePersist 验证开关能写盘、读得回来，且列表/详情都会归一化。
func TestPanelPasswordAutosavePersist(t *testing.T) {
	store := newTestStore(t)

	panel, err := store.create("NAS", "https://nas.lan", true)
	if err != nil {
		t.Fatalf("create panel: %v", err)
	}

	if err := store.setPasswordAutosave(panel.ID, PasswordAutosaveOff); err != nil {
		t.Fatalf("setPasswordAutosave(off): %v", err)
	}
	// 重新加载，确认真的落到了磁盘上而不只是内存里。
	reloaded := newConfigStore()
	reloaded.path = store.path
	if err := reloaded.load(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := reloaded.get(panel.ID)
	if !ok {
		t.Fatal("重新加载后面板丢失")
	}
	if got.savesPasswords() {
		t.Errorf("关掉后应不再保存密码，实际 %q", got.PasswordAutosave)
	}

	if err := reloaded.setPasswordAutosave(panel.ID, PasswordAutosaveOn); err != nil {
		t.Fatalf("setPasswordAutosave(on): %v", err)
	}
	if !reloaded.list()[0].savesPasswords() {
		t.Error("重新打开后 list() 应报告保存密码已启用")
	}

	if err := reloaded.setPasswordAutosave(panel.ID, "maybe"); err == nil {
		t.Error("非法取值应被拒绝")
	}
	if err := reloaded.setPasswordAutosave("不存在", PasswordAutosaveOn); err == nil {
		t.Error("面板不存在应报错")
	}
}

// TestPanelPasswordAutosaveLegacyConfig 验证老配置（面板里没有 passwordAutosave 键）
// 读出来就是「开启」，而且 list() 会把归一化后的值交给前端 —— 前端拿到空串就得自己猜默认。
func TestPanelPasswordAutosaveLegacyConfig(t *testing.T) {
	dir := t.TempDir()
	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")

	legacy := `{"schemaVersion":1,"panels":[{"id":"p1","name":"老面板","enabled":true,` +
		`"window":{"width":800,"height":600},"tabs":[{"id":"t1","name":"标签","url":"https://a.lan"}]}]}`
	if err := os.WriteFile(store.path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy config: %v", err)
	}
	if err := store.load(); err != nil {
		t.Fatalf("load legacy config: %v", err)
	}

	panels := store.list()
	if len(panels) != 1 {
		t.Fatalf("期望 1 个面板，实际 %d", len(panels))
	}
	if panels[0].PasswordAutosave != PasswordAutosaveOn {
		t.Errorf("老配置应归一化为 %q，实际 %q", PasswordAutosaveOn, panels[0].PasswordAutosave)
	}
	if detail, ok := store.get("p1"); !ok || !detail.savesPasswords() {
		t.Errorf("get() 也应报告老配置默认保存密码，实际 %+v", detail)
	}
}

func TestTabManagement(t *testing.T) {
	dir := t.TempDir()
	store := newConfigStore()
	store.path = filepath.Join(dir, "config.json")
	if err := os.WriteFile(store.path, []byte(`{"schemaVersion":1,"panels":[]}`), 0o600); err != nil {
		t.Fatalf("seed empty config: %v", err)
	}
	if err := store.load(); err != nil {
		t.Fatalf("load: %v", err)
	}

	// 创建面板。
	p, err := store.create("测试面板", "http://tab1.example.com", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(p.Tabs) != 1 {
		t.Fatalf("expected 1 tab, got %d", len(p.Tabs))
	}

	// 添加标签。
	newTabs := append(p.Tabs, PanelTab{
		ID:   newTabID(),
		Name: "标签二",
		URL:  "http://tab2.example.com",
	})
	if err := store.updateTabs(p.ID, newTabs); err != nil {
		t.Fatalf("update tabs: %v", err)
	}

	got, _ := store.get(p.ID)
	if len(got.Tabs) != 2 {
		t.Fatalf("expected 2 tabs, got %d", len(got.Tabs))
	}
	if got.Tabs[1].URL != "http://tab2.example.com" {
		t.Fatalf("second tab URL mismatch: %s", got.Tabs[1].URL)
	}

	// 设置默认标签索引。
	if err := store.setDefaultTabIndex(p.ID, 1); err != nil {
		t.Fatalf("set default tab index: %v", err)
	}
	got, _ = store.get(p.ID)
	if got.DefaultTabIndex != 1 {
		t.Fatalf("default tab index not set: %d", got.DefaultTabIndex)
	}

	// 删除一个标签。
	remaining := []PanelTab{got.Tabs[1]}
	if err := store.updateTabs(p.ID, remaining); err != nil {
		t.Fatalf("remove tab: %v", err)
	}
	got, _ = store.get(p.ID)
	if len(got.Tabs) != 1 {
		t.Fatalf("expected 1 tab after removal, got %d", len(got.Tabs))
	}
	if got.Tabs[0].URL != "http://tab2.example.com" {
		t.Fatalf("remaining tab should be tab2: %s", got.Tabs[0].URL)
	}
}
