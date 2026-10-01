//go:build windows

package main

// 应用级托盘与进程寿命判定的单元测试。
//
// 这两块是「托盘图标迁到常驻 IPC 窗口」这轮改动的核心逻辑：
// 托盘需求判定决定图标是否存在，进程寿命判定决定最后一个面板关闭后是否退出。

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTrayEllipsize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"openwrt", "openwrt"},
		{"  留白  ", "留白"},
		{"路由器", "路由器"},
	}
	for _, c := range cases {
		if got := trayEllipsize(c.in); got != c.want {
			t.Errorf("trayEllipsize(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("名", trayMenuNameLimit+5)
	got := trayEllipsize(long)
	if runes := []rune(got); len(runes) != trayMenuNameLimit+1 {
		t.Errorf("超长名称应截断为 %d 字符 + 省略号，实际 %q", trayMenuNameLimit, got)
	}
}

// newTestApp 创建一个配置落在临时目录的应用对象，避免影响真实配置。
func newTestApp(t *testing.T) *App {
	t.Helper()

	original := resolvePortableRoot
	resolvePortableRoot = func() string { return t.TempDir() }
	t.Cleanup(func() { resolvePortableRoot = original })

	app := NewApp("")
	if err := app.config.load(); err != nil {
		t.Fatalf("加载默认配置: %v", err)
	}
	return app
}

// TestAppTrayNeeded 覆盖「是否需要托盘图标」的三种理由。
func TestAppTrayNeeded(t *testing.T) {
	app := newTestApp(t)

	if !app.trayNeeded() {
		t.Error("默认 showTrayIcon=true：应需要托盘图标")
	}

	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if app.trayNeeded() {
		t.Error("关闭托盘图标且没有窗口藏在托盘里时，不应需要图标")
	}

	// 管理窗口被「最小化到托盘」：即使全局关闭也必须保留图标，
	// 否则窗口藏起来就再也找不回来（本次修复的边界）。
	app.mu.Lock()
	app.resident = true
	app.mu.Unlock()
	if !app.trayNeeded() {
		t.Error("管理窗口藏在托盘里时必须保留图标")
	}

	// 某个面板窗口藏在托盘里同理。
	app.mu.Lock()
	app.resident = false
	app.panels["p1"] = &panelWindow{app: app, id: "p1", hiddenInTray: true, done: make(chan struct{})}
	app.mu.Unlock()
	if !app.trayNeeded() {
		t.Error("面板窗口藏在托盘里时必须保留图标")
	}

	// 全局关闭托盘图标、管理窗口也没藏在托盘里，此时只剩「面板窗口藏在托盘里」
	// 这一条保留理由，图标仍必须留着（「藏起来的窗口必须有图标才能找回」优先于
	// 用户的关闭意愿）。
	app.mu.Lock()
	app.resident = false
	app.mu.Unlock()
	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if !app.trayNeeded() {
		t.Error("面板窗口藏在托盘里时，即使取消勾选也要保留图标，否则窗口再也找不回来")
	}

	// 程序正在退出：同理，不再维护图标。
	app.mu.Lock()
	app.quitting = true
	app.mu.Unlock()
	if app.trayNeeded() {
		t.Error("程序正在退出时不应需要托盘图标")
	}

	// 复原，确认退出标记撤掉后判定回到原样。
	app.mu.Lock()
	app.quitting = false
	app.mu.Unlock()
	if !app.trayNeeded() {
		t.Error("复原后仍有面板藏在托盘里，应重新需要图标")
	}
}

// TestSetCloseActionForcesTrayIcon 验证桥接层把「选中最小化到托盘 → 托盘图标自动打开」
// 收尾干净：配置改了、图标真的算作需要、取消勾选会被明确拒绝；改回去之后又允许取消。
func TestSetCloseActionForcesTrayIcon(t *testing.T) {
	app := newTestApp(t)

	if err := app.SetShowTrayIcon(false); err != nil {
		t.Fatalf("关闭面板窗口的行为是 ask，取消托盘图标应被允许: %v", err)
	}
	if app.trayNeeded() {
		t.Fatal("取消勾选后不应需要托盘图标")
	}

	if err := app.SetPanelCloseAction(CloseActionTray); err != nil {
		t.Fatalf("SetPanelCloseAction(tray): %v", err)
	}
	if !app.config.settings().ShowTrayIcon {
		t.Error("选中「最小化到托盘」后设置里应自动打开托盘图标")
	}
	if !app.trayNeeded() {
		t.Error("选中「最小化到托盘」后应重新需要托盘图标")
	}

	// 此时取消勾选：必须报错（错误码包着哨兵，errors.Is 仍可判定），并且图标照旧。
	if err := app.SetShowTrayIcon(false); !errors.Is(err, ErrTrayIconRequired) {
		t.Fatalf("应拒绝取消托盘图标，实际 err = %v", err)
	}
	if !app.trayNeeded() {
		t.Error("被拒绝后托盘图标必须仍然保留")
	}

	// 改回去：取消勾选重新可行。
	if err := app.SetPanelCloseAction(CloseActionAsk); err != nil {
		t.Fatalf("SetPanelCloseAction(ask): %v", err)
	}
	if err := app.SetShowTrayIcon(false); err != nil {
		t.Fatalf("此时取消托盘图标应被允许: %v", err)
	}
}

// TestQuitWhenNoWindows 覆盖「没有任何窗口剩下时进程是否退出」这条唯一判定。
// 不变量：还有窗口、或者托盘里还有图标，就不退出整个程序；两者都没有才收工。
// 唯一例外是轻量模式（`--open` 用完即走），且它可以在设置里关掉。
func TestQuitWhenNoWindows(t *testing.T) {
	app := newTestApp(t)

	// 常规启动：管理窗口一开始就是显示的（startup 里补的这一笔）。
	app.mu.Lock()
	app.mainShown = !app.lightweight
	app.mu.Unlock()
	if app.shouldQuitAfterPanelClosed() {
		t.Error("管理窗口还开着，不应退出")
	}

	// 管理窗口关掉（此时它已藏进托盘）但没有面板：托盘图标还开着，
	// 留在托盘里，不能悄悄退出。
	app.mu.Lock()
	app.mainShown = false
	app.mu.Unlock()
	if app.shouldQuitAfterPanelClosed() {
		t.Error("没有窗口但托盘图标还在时不应退出（托盘是找回界面的入口）")
	}

	// 托盘也被关掉了：此时才是真正「没有任何窗口和托盘」，可以退出。
	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if !app.shouldQuitAfterPanelClosed() {
		t.Error("没有任何窗口、也没有托盘图标时：应退出")
	}

	// 只要还有一个分组标签窗口在，就绝不退出整个程序。
	app.mu.Lock()
	app.panels["p1"] = &panelWindow{app: app, id: "p1", done: make(chan struct{})}
	app.mu.Unlock()
	if app.shouldQuitAfterPanelClosed() {
		t.Error("还有分组标签窗口时绝不能退出整个程序")
	}

	// 轻量模式（用完即走）：最后一个面板关闭即收工，不为托盘图标多留
	//（此时即使托盘图标还开着也要退出）。
	app.mu.Lock()
	delete(app.panels, "p1")
	app.lightweight = true
	app.mu.Unlock()
	if err := app.config.setShowTrayIcon(true); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if !app.shouldQuitAfterPanelClosed() {
		t.Error("轻量模式「用完即走」开启时：关掉最后一个面板应退出")
	}

	// 把它关掉之后，轻量实例与常规实例一致：托盘图标还开着就留在托盘里。
	if err := app.config.setLightweightQuitOnLastPanel(false); err != nil {
		t.Fatalf("setLightweightQuitOnLastPanel: %v", err)
	}
	if app.shouldQuitAfterPanelClosed() {
		t.Error("关掉「用完即走」后，托盘图标还开着就不应退出")
	}
	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon(false): %v", err)
	}
	if !app.shouldQuitAfterPanelClosed() {
		t.Error("既没有窗口也没有托盘图标时（轻量模式）应退出")
	}
	if err := app.config.setLightweightQuitOnLastPanel(true); err != nil {
		t.Fatalf("setLightweightQuitOnLastPanel(true): %v", err)
	}
	if err := app.config.setShowTrayIcon(true); err != nil {
		t.Fatalf("setShowTrayIcon(true): %v", err)
	}

	// 用户选择过留在托盘：程序留在托盘，不退出。
	app.mu.Lock()
	app.resident = true
	app.mu.Unlock()
	if app.shouldQuitAfterPanelClosed() {
		t.Error("用户选择过留在托盘后不应自动退出")
	}

	// 正在主动退出：不再补第二次退出请求（收尾交给 Wails 的 OnShutdown）。
	app.mu.Lock()
	app.resident = false
	app.quitting = true
	app.mu.Unlock()
	if app.shouldQuitAfterPanelClosed() {
		t.Error("程序正在退出时不应再触发自动退出")
	}
}

// TestManagerCloseHidesWhilePanelsAlive 覆盖管理窗口的固定关闭动作（其一半）：
// 还有分组标签窗口时**只关自己** —— 不碰任何面板窗口，也不动托盘设置；
// 管理窗口藏起来（Wails 的主窗口一销毁进程就结束，只能藏），进程继续运行。
func TestManagerCloseHidesWhilePanelsAlive(t *testing.T) {
	app := newTestApp(t)
	panel := addStuckPanel(app, "keep")
	app.mu.Lock()
	app.mainShown = true
	app.mu.Unlock()

	if !app.onBeforeClose(context.Background()) {
		t.Fatal("还有分组标签窗口时应拦下本次关闭")
	}
	if panelCloseRequested(panel) {
		t.Error("关闭管理窗口不应向分组标签窗口发出任何关闭请求")
	}
	if !app.panelsAlive() {
		t.Error("分组标签窗口应继续运行")
	}
	if !app.config.settings().ShowTrayIcon {
		t.Error("关闭管理窗口不应改动「在系统托盘显示图标」")
	}
	if !app.trayNeeded() {
		t.Error("托盘设置没被动过，图标应照旧保留")
	}
	if app.shouldQuitAfterPanelClosed() {
		t.Error("还有分组标签窗口时不应退出")
	}
	if app.isQuitting() {
		t.Error("被拦下时不应进入退出流程")
	}
}

// TestManagerCloseHidesWhenTrayIconShown 覆盖另一半：一个面板都没有，但托盘图标开着时，
// 仍然是「只关自己」—— 窗口藏进托盘，靠图标就能找回来，程序留在托盘里。
func TestManagerCloseHidesWhenTrayIconShown(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.mainShown = true
	app.mu.Unlock()

	if !app.onBeforeClose(context.Background()) {
		t.Fatal("托盘图标还开着时应拦下本次关闭（窗口藏进托盘）")
	}
	app.mu.Lock()
	resident := app.resident
	app.mu.Unlock()
	if !resident {
		t.Error("管理窗口藏进托盘后应置 resident，否则图标会被撤掉、窗口再也找不回来")
	}
	if !app.trayNeeded() {
		t.Error("窗口藏在托盘里时必须保留托盘图标")
	}
}

// TestManagerCloseReleasesWhenNothingLeft 覆盖固定关闭动作的出口：
// 既没有分组标签窗口、也没有托盘图标时，本次关闭必须放行 —— 放行后管理窗口关闭即
// 「最后一个窗口关闭」，进程随之结束。
func TestManagerCloseReleasesWhenNothingLeft(t *testing.T) {
	app := newTestApp(t)
	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	app.mu.Lock()
	app.mainShown = true
	app.mu.Unlock()

	if app.onBeforeClose(context.Background()) {
		t.Error("没有任何窗口与托盘图标时应放行本次关闭")
	}
	if app.config.settings().ShowTrayIcon {
		t.Error("关闭管理窗口不应改动「在系统托盘显示图标」")
	}
}

// addStuckPanel 注入一个「关不掉」的分组标签窗口并返回它。
// hwnd 为 0 时 close() 只会置 closePending、不会真的拆除窗口，于是它一直留在
// app.panels 里 —— 这正是「面板窗口还在」的最小复现。
func addStuckPanel(app *App, id string) *panelWindow {
	p := &panelWindow{app: app, id: id, done: make(chan struct{})}
	app.mu.Lock()
	app.panels[id] = p
	app.mu.Unlock()
	return p
}

// panelCloseRequested 报告该面板窗口是否收到过关闭请求（主动关闭路径的痕迹）。
func panelCloseRequested(p *panelWindow) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed || p.closePending
}

// TestPanelCloseEndsProcess 覆盖「面板询问框要不要提示后果」的判定：
// 关掉这个面板后如果没有任何窗口、也没有托盘图标，程序就会收工，此时必须提示。
func TestPanelCloseEndsProcess(t *testing.T) {
	app := newTestApp(t)

	if app.panelCloseEndsProcess() {
		t.Error("普通模式下托盘图标开着（关掉面板后程序留在托盘），无需提示")
	}

	app.mu.Lock()
	app.lightweight = true
	app.mu.Unlock()
	if !app.panelCloseEndsProcess() {
		t.Error("轻量模式 + 管理窗口未显示 + 无其他面板：应提示「关闭后程序退出」")
	}

	// 还有别的面板：关掉一个不会让进程收工，无需提示。
	app.mu.Lock()
	app.panels["p1"] = &panelWindow{app: app, id: "p1", done: make(chan struct{})}
	app.panels["p2"] = &panelWindow{app: app, id: "p2", done: make(chan struct{})}
	app.mu.Unlock()
	if app.panelCloseEndsProcess() {
		t.Error("仍有其他面板打开时不应提示程序退出")
	}

	// 只剩一个面板：关掉它就真收工了，需要提示。
	app.mu.Lock()
	delete(app.panels, "p2")
	app.mu.Unlock()
	if !app.panelCloseEndsProcess() {
		t.Error("只剩一个面板时应提示「关闭后程序退出」")
	}

	// 管理窗口正在显示：关掉面板后进程继续。
	app.mu.Lock()
	app.mainShown = true
	app.mu.Unlock()
	if app.panelCloseEndsProcess() {
		t.Error("管理窗口正在显示时不应提示程序退出")
	}

	// 管理窗口藏进托盘（resident）：用户明确要求留在托盘，同样不提示。
	app.mu.Lock()
	app.mainShown = false
	app.resident = true
	app.mu.Unlock()
	if app.panelCloseEndsProcess() {
		t.Error("用户选择过「最小化到托盘」后不应提示程序退出")
	}

	// 常规模式、管理窗口已关闭（例如选了「关闭管理面板」）且托盘图标也关了：
	// 关掉这个面板就真的没有任何窗口与托盘了，必须提示。
	app.mu.Lock()
	app.resident = false
	app.lightweight = false
	app.mu.Unlock()
	if err := app.config.setShowTrayIcon(false); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}
	if !app.panelCloseEndsProcess() {
		t.Error("没有窗口也没有托盘时，关掉最后一个面板应收工，需要提示")
	}
}
