//go:build windows

package main

import (
	"os"
	"testing"
)

// newResetTestApp 起一个只跑配置层 + 文件层的 App（不碰 Win32），并把便携根目录指到临时目录。
//
// 用 useTempPortableRoot 而不是 newTestApp：前者顺带把 profileClearDelay 压到 1ms，
// 清理路径里的重试预算才不会让测试白等几百毫秒。
func newResetTestApp(t *testing.T) *App {
	t.Helper()

	useTempPortableRoot(t)
	app := NewApp("")
	if err := app.config.load(); err != nil {
		t.Fatalf("加载配置: %v", err)
	}
	return app
}

// createResettablePanel 造一个带两个标签的面板，并把它两个标签的 profile 目录都填上数据。
// 返回面板配置与两个 profile 目录。
func createResettablePanel(t *testing.T, app *App) (PanelConfig, []string) {
	t.Helper()

	panel, err := app.config.create("重置测试", "http://a.lan", true)
	if err != nil {
		t.Fatalf("创建面板: %v", err)
	}

	tabs := []PanelTab{
		{ID: panel.Tabs[0].ID, Name: "标签一", URL: "http://a.lan"},
		{ID: "tab-reset-second", Name: "标签二", URL: "http://b.lan"},
	}
	if err := app.config.updateTabs(panel.ID, tabs); err != nil {
		t.Fatalf("写入标签: %v", err)
	}

	// seedProfile 造的是「WebView2 真用过」的目录（带 EBWebView\Default\Cookies），
	// 用来验证删的是整棵树而不是只删最外层。
	return panel, []string{seedProfile(t, tabs[0].ID), seedProfile(t, tabs[1].ID)}
}

// TestResetPanelDataClearsAllTabProfiles 验证「重置数据」把该分组**所有**标签的浏览器数据
// 从磁盘上抹掉，且**不动面板本身**。
//
// 断言的是磁盘状态而不是「调用没报错」：这个功能的全部价值就在数据真的没了，
// 少清一个标签 = 那个标签的登录态还在，用户以为什么都没留。
func TestResetPanelDataClearsAllTabProfiles(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)

	// 重置不写配置，配置文件应当一个字节都不动 —— 顺手锁住这条（E2E 的「零污染」同理）。
	before, err := os.ReadFile(app.config.path)
	if err != nil {
		t.Fatalf("读取配置: %v", err)
	}

	if err := app.ResetPanelData(panel.ID); err != nil {
		t.Fatalf("ResetPanelData: %v", err)
	}

	for i, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("第 %d 个标签的 profile 目录应被整棵删除 (err=%v)", i+1, err)
		}
	}

	// 面板本身必须完好：重置的是数据，不是面板。
	cfg, ok := app.config.get(panel.ID)
	if !ok {
		t.Fatal("重置不应删除面板配置")
	}
	if len(cfg.Tabs) != 2 {
		t.Errorf("重置后标签数应为 2，实际 %d", len(cfg.Tabs))
	}
	if cfg.Name != panel.Name {
		t.Errorf("重置不应改面板名：%q → %q", panel.Name, cfg.Name)
	}

	after, err := os.ReadFile(app.config.path)
	if err != nil {
		t.Fatalf("读取配置: %v", err)
	}
	if string(before) != string(after) {
		t.Error("重置只该动浏览器数据，配置文件的字节不应有任何变化")
	}
}

// TestResetPanelDataRejectsUnknownPanel 验证面板 ID 不存在时报错而不是静默成功 ——
// 静默成功会让用户以为「已经清空了」，实际什么都没发生。
func TestResetPanelDataRejectsUnknownPanel(t *testing.T) {
	app := newResetTestApp(t)

	if err := app.ResetPanelData("no-such-panel"); err == nil {
		t.Error("未知面板 ID 应返回错误")
	}
}

// TestResetPanelDataClosesRunningPanelFirst 锁定「先关窗口、再清目录」这个顺序。
//
// 顺序反了**不会报错**，只会清不干净：目录被面板的浏览器进程占着，RemoveAll 只删掉一部分，
// 剩下的登录态照旧留在盘上 —— 而用户看到的是「重置成功」。这类静默失效只能靠顺序断言守住。
//
// 这里用注入的假 closer 顶替真实实现：真的 close/wait 要等窗口线程 close(done)，
// 测试进程里没有窗口线程，会直接挂死。
func TestResetPanelDataClosesRunningPanelFirst(t *testing.T) {
	app := newResetTestApp(t)
	panel, dirs := createResettablePanel(t, app)

	dataPresentWhenClosed := false
	original := resetPanelCloser
	resetPanelCloser = func(*panelWindow) {
		_, err := os.Stat(dirs[0])
		dataPresentWhenClosed = !os.IsNotExist(err)
	}
	t.Cleanup(func() { resetPanelCloser = original })

	// 假装这个面板正开着（真实路径下这一步由 OpenPanel 完成）。
	app.mu.Lock()
	app.panels[panel.ID] = &panelWindow{id: panel.ID}
	app.mu.Unlock()

	if err := app.ResetPanelData(panel.ID); err != nil {
		t.Fatalf("ResetPanelData: %v", err)
	}

	if !dataPresentWhenClosed {
		t.Error("必须先关掉正在运行的面板窗口、再清空它的数据；否则目录被浏览器进程占着，清不干净且不报错")
	}
	for i, dir := range dirs {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("第 %d 个标签的 profile 目录应被删除 (err=%v)", i+1, err)
		}
	}
}
