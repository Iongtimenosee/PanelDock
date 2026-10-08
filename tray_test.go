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

// TestTrayHiddenQueue 覆盖托盘菜单「已隐藏的窗口」队列的四条规则：
// 按顺序记录、重复隐藏移到队尾（末位即最近）、窗口恢复后立即出队、窗口消失后不能留下幻影项。
// 队列是菜单的唯一数据源，漏sticks一条就会列一个点了没反应的名字。
func TestTrayHiddenQueue(t *testing.T) {
	app := newTestApp(t)
	p1 := addStuckPanel(app, "p1")
	p2 := addStuckPanel(app, "p2")

	p1.setHiddenInTray(true)
	p2.setHiddenInTray(true)
	assertTrayOrder(t, app, "p1", "p2")

	// 重复隐藏同一个窗口：不能重复入队，且它成为「最近一次」，双击优先轮到它。
	p1.setHiddenInTray(true)
	assertTrayOrder(t, app, "p2", "p1")

	if got := app.lastTrayHidden(); got != p1 {
		t.Errorf("双击应作用于最近一次隐藏的窗口 p1，实际 %v", got)
	}

	// 恢复显示 → 从菜单里消失。
	p1.setHiddenInTray(false)
	assertTrayOrder(t, app, "p2")

	// 窗口已经不存在（dispose 后）但队列还没清：列出时必须被过滤掉，
	// 否则菜单里会出现一条点了没反应的名字。
	app.mu.Lock()
	delete(app.panels, "p2")
	app.mu.Unlock()
	if got := app.trayHiddenPanels(); len(got) != 0 {
		t.Errorf("窗口已销毁后不应再出现在菜单里，实际剩 %d 项", len(got))
	}

	// 面板被删除 / 关闭的清理入口会把队列一并摘干净（idempotent）。
	app.forgetTrayHidden("p2")
	assertTrayOrder(t, app)
}

// TestTrayDoubleClickTogglesBetweenStates 覆盖双击托盘图标的来回开关语义：
// 同一个窗口被反复「拿回来 ⇄ 收回去」，不必先把它放到桌面上点 X 才能再双击收起。
//
// 这正是上一版缺的那一段 —— 队列里只装藏着的窗口，一经恢复就再也轮不到它了，
// 于是第二次双击无事可做（实测现象：必须关掉窗口再双击才能重新拿到它）。
func TestTrayDoubleClickTogglesBetweenStates(t *testing.T) {
	app := newTestApp(t)
	p1 := addStuckPanel(app, "p1")
	p2 := addStuckPanel(app, "p2")

	// 两个都先藏起来：队列末位（p2）是双击的第一个目标。
	p1.setHiddenInTray(true)
	p2.setHiddenInTray(true)

	// 第 1 次双击：拿回 p2，菜单里只剩 p1。
	app.trayActivateLastHidden()
	assertTrayHiddenState(t, p2, false)
	assertTrayOrder(t, app, "p1")

	// 第 2 次双击：把同一个窗口收回去（而不是去动队列里的 p1）。
	app.trayActivateLastHidden()
	assertTrayHiddenState(t, p2, true)
	assertTrayOrder(t, app, "p1", "p2")

	// 第 3 次双击：还能再拿回来 —— 这才是「不停双击不停来回」。
	app.trayActivateLastHidden()
	assertTrayHiddenState(t, p2, false)
	assertTrayOrder(t, app, "p1")

	// 目标始终锁在同一个窗口上，不随队列漂移。
	if got := app.trayToggleTarget(); got != p2 {
		t.Errorf("双击目标应锁在 p2 上，实际 %v", got)
	}

	// 目标被销毁（这是真实删除路径会走的清理）：双击换到队列里下一个，
	// 不能对着一个已经死掉的对象反复空转。
	markPanelClosed(p2)
	if app.trayToggleTarget() != nil {
		t.Error("目标窗口已销毁：trayToggleTarget 应失效")
	}
	app.trayDropToggleTarget("p2")
	app.trayActivateLastHidden()
	assertTrayHiddenState(t, p1, false)
	assertTrayOrder(t, app)
}

// TestLightweightCloseSkipsTrayHideUntilTrayUsed 轻量模式（快捷方式直达）下，
// 用户还没在托盘上操作过任何窗口时，「最小化到托盘」这一步会被跳过、直接关掉面板 ——
// 进程马上就要收工，藏起来的窗口再也没机会被拿出来。
// 一旦他真的在托盘上操作过一次，就让位于他的意图，之后照旧藏起来。
func TestLightweightCloseSkipsTrayHideUntilTrayUsed(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.lightweight = true
	app.mu.Unlock()
	if err := app.config.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}
	panel := addStuckPanel(app, "p1")

	if !app.closeSkipsTrayHide() {
		t.Fatal("轻量模式 + 未用过托盘 + 最后一个面板：应跳过「藏到托盘」")
	}

	// 走真实关闭路径（设置已记住 tray，不会弹询问框）：必须去关窗口，而不是藏起来。
	panel.handleInteractiveClose()
	if panel.isHiddenInTray() {
		t.Error("跳过时不应把窗口藏到托盘 —— 进程马上收工，藏了也拿不回来")
	}
	if !panelCloseRequested(panel) {
		t.Error("跳过时应当直接关掉这个面板窗口")
	}

	// 关键是**开着第二个面板时同样要跳过**。只放过"最后一个"是不够的：那样第一个窗口
	// 会被藏进托盘，但它仍占着运行表的位置，于是第二个也永远轮不上"最后一个"，
	// 两个窗口一起赖在托盘里，进程再也不退出 —— 用户实测到的正是这个。
	second := addStuckPanel(app, "p2")
	if !app.closeSkipsTrayHide() {
		t.Error("还有其它面板时同样应跳过「藏到托盘」")
	}
	second.handleInteractiveClose()
	if second.isHiddenInTray() {
		t.Error("多面板时也不该把窗口藏到托盘：藏了它就一直是运行表里的「还没关」")
	}
	if !panelCloseRequested(second) {
		t.Error("多面板时同样应当直接关掉这个面板窗口")
	}

	// 用户在托盘上操作过一次窗口：他开始依赖托盘了，此后轻量模式让位于他的意图。
	app.markTrayToggleTarget(second)
	app.mu.Lock()
	delete(app.panels, "p2")
	app.mu.Unlock()

	third := addStuckPanel(app, "p3")
	if app.closeSkipsTrayHide() {
		t.Error("用过托盘之后：即使只剩最后一个面板也应照常藏到托盘")
	}
	third.handleInteractiveClose()
	assertTrayHiddenState(t, third, true)
	if panelCloseRequested(third) {
		t.Error("用过托盘之后不应再去关窗口")
	}
}

// TestTrayMenuCommandClosesPanelForGood 托盘菜单里的「彻底关闭」子菜单：
// 选中它就真的把这个窗口关掉，而不是藏回托盘（藏回去等于什么都没发生）。
//
// 这条 API 的来历：菜单里最早有一项「关闭面板」，在把菜单改成「已隐藏的窗口列表」时被删了，
// 于是最小化到托盘的窗口再也没有"在这里丢掉它"的办法 —— 只能先取回再点 X，而点 X 又会被
// 记住的「最小化到托盘」再送回来。
func TestTrayMenuCommandClosesPanelForGood(t *testing.T) {
	app := newTestApp(t)
	if err := app.config.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}

	first := addStuckPanel(app, "p1")
	second := addStuckPanel(app, "p2")
	first.hideToTray()
	second.hideToTray()
	targets := app.trayHiddenPanels()

	// 主列表那组的语义不变：点名字是「取回」。
	app.trayHandleMenuCommand(targets, trayMenuRestoreBase+1)
	assertTrayHiddenState(t, second, false)

	// 「彻底关闭」那一组：换成真关。
	app.trayHandleMenuCommand(targets, trayMenuCloseHiddenBase)
	if !panelCloseRequested(first) {
		t.Error("选中「彻底关闭」应当真的关掉那个面板窗口")
	}
	// 替身窗口收不到真正的销毁流程，hiddenInTray 不会被清掉，所以这里能验的恰恰是
	//「没有走取回那条路」：showFromTray 会把它置为 false。
	if !first.isHiddenInTray() {
		t.Error("彻底关闭不是「取回」：不该把窗口从托盘里拿到桌面上")
	}

	// 菜单被取消（TPM_RETURNCMD 返回 0）：什么都不动。
	if app.trayHandleMenuCommand(targets, 0); panelCloseRequested(second) {
		t.Error("取消菜单不应带来任何动作")
	}

	// 下标越界（窗口在菜单弹出期间就消失了）：不能崩，也不能误伤别人。
	app.trayHandleMenuCommand(targets, trayMenuCloseHiddenBase+uintptr(len(targets)+5))
}

// TestLightweightQuitsAfterEveryPanelClosed 是用户实测出的回归：轻量模式下同时开着两个
// 面板，逐个关掉之后进程必须退出。
//
// 藏进托盘的窗口**仍然占着运行表的位置**，所以「还有别的面板」这条判断会一路成立到最后 ——
// 跳过托盘必须对每个面板都生效，只放过"最后一个"的话，第一个就先被藏起来了。
func TestLightweightQuitsAfterEveryPanelClosed(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.lightweight = true
	app.mu.Unlock()
	if err := app.config.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}
	if err := app.config.setShowTrayIcon(true); err != nil {
		t.Fatalf("setShowTrayIcon: %v", err)
	}

	first := addStuckPanel(app, "p1")
	second := addStuckPanel(app, "p2")

	// 第一个不是"最后一个"，但同样得真关：一旦它被藏进托盘，"还有窗口"就永远成立。
	first.handleInteractiveClose()
	if first.isHiddenInTray() {
		t.Fatal("第一个面板不该被藏进托盘")
	}
	disposePanelFromRuntime(app, first)
	if app.shouldQuitAfterPanelClosed() {
		t.Error("还有一个面板没关时不应收工")
	}

	second.handleInteractiveClose()
	if second.isHiddenInTray() {
		t.Fatal("最后一个面板同样不该被藏进托盘")
	}
	disposePanelFromRuntime(app, second)
	if !app.shouldQuitAfterPanelClosed() {
		t.Error("轻量模式下关掉全部面板后应收工：开几个面板都不能破例")
	}
}

// TestLightweightStaysWhenTrayUsed 与上面同一场景，但用户先在托盘上操作过窗口：
// 此后轻量模式让位于他，最后一个面板关掉后进程留在托盘里。
func TestLightweightStaysWhenTrayUsed(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.lightweight = true
	app.mu.Unlock()
	if err := app.config.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}

	first := addStuckPanel(app, "p1")
	second := addStuckPanel(app, "p2")

	// 用户在托盘上切换过窗口：他正在用托盘，「用完即走」就此让位。
	app.markTrayToggleTarget(first)

	first.handleInteractiveClose()
	if !first.isHiddenInTray() {
		t.Fatal("用过托盘之后应照旧藏到托盘")
	}
	disposePanelFromRuntime(app, first)
	if app.shouldQuitAfterPanelClosed() {
		t.Error("还有第二个面板时不该收工")
	}

	second.handleInteractiveClose()
	if !second.isHiddenInTray() {
		t.Fatal("用过托盘之后最后一个面板也应藏到托盘")
	}
}

// disposePanelFromRuntime 模拟窗口真的关闭：替身窗口走不到 dispose → onPanelClosed，
// 这里手工执行其中的运行表清理部分（也就是进程寿命判定真正依赖的那几笔）。
func disposePanelFromRuntime(app *App, p *panelWindow) {
	app.mu.Lock()
	defer app.mu.Unlock()
	delete(app.panels, p.id)
	app.trayHiddenOrder = removeFromOrder(app.trayHiddenOrder, p.id)
	app.trayDropToggleTargetLocked(p.id)
}

// TestNormalModeKeepsTrayHide 常规启动不受「跳过藏托盘」影响：
// 那里没有「用完即走」，管理窗口还开着，把面板藏起来本来就是用户的意思。
func TestNormalModeKeepsTrayHide(t *testing.T) {
	app := newTestApp(t)
	app.mu.Lock()
	app.mainShown = true
	app.mu.Unlock()
	if err := app.config.setCloseAction(CloseActionTray); err != nil {
		t.Fatalf("setCloseAction(tray): %v", err)
	}
	panel := addStuckPanel(app, "p1")

	if app.closeSkipsTrayHide() {
		t.Fatal("常规模式不应跳过「藏到托盘」")
	}
	panel.handleInteractiveClose()
	assertTrayHiddenState(t, panel, true)
	if panelCloseRequested(panel) {
		t.Error("常规模式应照常把窗口藏到托盘，不去关它")
	}
}

// assertTrayHiddenState 校验某个面板窗口此刻是否藏在托盘里。
func assertTrayHiddenState(t *testing.T, p *panelWindow, want bool) {
	t.Helper()
	if got := p.isHiddenInTray(); got != want {
		t.Errorf("窗口 %s 的托盘隐藏状态 = %v，期望 %v", p.id, got, want)
	}
}

// markPanelClosed 模拟窗口已经销毁（dispose 已跑过）。
func markPanelClosed(p *panelWindow) {
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
}
func assertTrayOrder(t *testing.T, app *App, want ...string) {
	t.Helper()
	app.mu.Lock()
	got := append([]string(nil), app.trayHiddenOrder...)
	app.mu.Unlock()
	if len(got) != len(want) {
		t.Fatalf("队列长度 = %d（%v），期望 %d（%v）", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("队列第 %d 项 = %q，期望 %q（完整：%v）", i, got[i], want[i], got)
		}
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
