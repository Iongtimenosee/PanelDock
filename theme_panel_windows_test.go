//go:build windows

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestPanelColorRefConvertsRGBToBGR 钉住 0xRRGGBB → COLORREF 的换位。
//
// 这条最该有：GDI 的 COLORREF 是 0x00BBGGRR，而配色表按人类习惯写 RGB，
// 换位写错（比如少移一位）**编译、vet、单测全绿**，只有肉眼看窗口才发现变蓝。
// 断言右侧那几个字面量是本次重构前写死在代码里的值 —— 换位对了，
// 深色主题才会与改动前**逐个像素一致**。
func TestPanelColorRefConvertsRGBToBGR(t *testing.T) {
	cases := []struct {
		rgb  uint32
		want uint32
	}{
		{0x1E293B, 0x003B291E}, // 标签栏底色
		{0xF1F5F9, 0x00F9F5F1}, // 活动标签底色
		{0xCBD5E1, 0x00E1D5CB}, // 常规文字
		{0x64748B, 0x008B7464}, // 禁用文字
		{0x60A5FA, 0x00FAA560}, // 已置顶图钉
		{0xE2E8F0, 0x00F0E8E2}, // 地址栏文字
	}
	for _, c := range cases {
		if got := panelColorRef(c.rgb); got != c.want {
			t.Errorf("panelColorRef(%#06X) = %#08X，期望 %#08X", c.rgb, got, c.want)
		}
	}
}

// TestPanelColorRefRoundTrip 反向验证：换位两次应当回到原值。
// 只验单向的话，一个「恒定返回 0」的实现也能过上面那张表里的个别项。
func TestPanelColorRefRoundTrip(t *testing.T) {
	for _, rgb := range []uint32{0x000000, 0xFFFFFF, 0x1E293B, 0xF1F5F9, 0x60A5FA, 0x123456} {
		got := panelColorRef(panelColorRef(rgb) & 0xFFFFFF)
		if got != rgb {
			t.Errorf("%#06X 换位两次得到 %#06X", rgb, got)
		}
	}
}

// TestPanelChromeDarkKeepsLegacyColors 锁住深色主题 = 面板窗口一直以来的观感。
//
// 新增「浅色」是在深色之上**加一套**，不是把深色顺手改了。这个用例就是那条界线：
// 谁把深色的某个值动了（哪怕只是调一点点），这里立刻红。
func TestPanelChromeDarkKeepsLegacyColors(t *testing.T) {
	dark := panelChromeFor(ThemeDark)
	cases := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"底色", dark.bg, 0x1E293B},
		{"文字", dark.text, 0xCBD5E1},
		{"禁用文字", dark.textDisabled, 0x64748B},
		{"悬停", dark.hover, 0x334155},
		{"按下", dark.pressed, 0x475569},
		{"活动标签底色", dark.activeTab, 0xF1F5F9},
		{"活动标签文字", dark.activeTabText, 0x1E293B},
		{"地址栏底色", dark.field, 0x334155},
		{"地址栏文字", dark.fieldText, 0xE2E8F0},
		{"强调色", dark.accent, 0x60A5FA},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("[深色] %s = %#06X，期望 %#06X（深色是既有观感，不该被改动）", c.name, c.got, c.want)
		}
	}
}

// TestPanelChromeForResolvesTheme 钉住主题取值到配色表的映射。
// 面板拿到的是**生效**主题（已解析过 auto），所以非 light 一律按浅色兜底。
func TestPanelChromeForResolvesTheme(t *testing.T) {
	if panelChromeFor(ThemeDark) != &panelChromeDark {
		t.Error("dark 应取深色表")
	}
	for _, value := range []string{ThemeLight, "", ThemeAuto, "blue"} {
		if panelChromeFor(value) != &panelChromeLight {
			t.Errorf("%q 应按浅色兜底", value)
		}
	}
}

// TestPanelChromeBrushesAreCreated 两套配色都必须真的建出画刷。
// 漏建的症状是「窗口某些区域不重绘/全黑」，而单测与 vet 都不会报错。
func TestPanelChromeBrushesAreCreated(t *testing.T) {
	for _, effective := range []string{ThemeLight, ThemeDark} {
		chrome := panelChromeFor(effective)
		brushes := map[string]uintptr{
			"brBg":        chrome.brBg,
			"brHover":     chrome.brHover,
			"brPressed":   chrome.brPressed,
			"brActiveTab": chrome.brActiveTab,
			"brField":     chrome.brField,
		}
		for name, brush := range brushes {
			if brush == 0 {
				t.Errorf("[%s] %s 未创建", effective, name)
			}
		}
	}
}

// TestPanelTabBarThemeRectSitsAtRightEdge 配色按钮贴右缘、垂直居中，
// 宽度与高度都在标签栏之内。
func TestPanelTabBarThemeRectSitsAtRightEdge(t *testing.T) {
	const width = 1000
	rect := panelTabBarThemeRect(width)

	if rect.Right != width-tabBarPadding {
		t.Errorf("右边界 = %d，期望 %d（贴右缘内边距）", rect.Right, width-tabBarPadding)
	}
	if rect.Left != rect.Right-tabBarThemeBtnWidth {
		t.Errorf("宽度 = %d，期望 %d", rect.Right-rect.Left, tabBarThemeBtnWidth)
	}
	if rect.Top != (tabBarHeight-tabBarThemeBtnHeight)/2 {
		t.Errorf("Top = %d，未垂直居中", rect.Top)
	}
	if rect.Bottom > tabBarHeight {
		t.Errorf("Bottom = %d 超出标签栏高度 %d", rect.Bottom, tabBarHeight)
	}
}

// TestPanelTabBarTabLimitNeverOverlapsThemeButton 是标签栏最要紧的一条不变量：
// 画出来的最后一个标签不能压到配色按钮，且上限必须**刚好**卡在临界点上
// （松一格就会白白少显示一个标签，紧一格就会盖住按钮）。
func TestPanelTabBarTabLimitNeverOverlapsThemeButton(t *testing.T) {
	for width := int32(120); width <= 2200; width += 17 {
		limit := panelTabBarTabLimit(width)
		buttonLeft := panelTabBarThemeRect(width).Left
		if limit <= 0 {
			continue
		}

		lastRight := int32(tabBarPadding + (limit-1)*(tabBarTabWidth+tabBarTabGap) + tabBarTabWidth)
		if lastRight > buttonLeft {
			t.Errorf("宽度 %d：第 %d 个标签右边界 %d 压到了配色按钮（左边界 %d）", width, limit, lastRight, buttonLeft)
		}

		nextRight := lastRight + tabBarTabGap + tabBarTabWidth
		if nextRight <= buttonLeft {
			t.Errorf("宽度 %d：本可以再放下一个标签（%d <= %d），上限算松了", width, nextRight, buttonLeft)
		}
	}
}

// TestThemeButtonHitMatchesRect 命中测试与绘制用同一份几何（不共用就会出现
// 「看得见点不到」或「点得到看不见」）。
func TestThemeButtonHitMatchesRect(t *testing.T) {
	const width = 800
	rect := panelTabBarThemeRect(width)

	for _, point := range []struct {
		x, y int
		want bool
	}{
		{int(rect.Left), int(rect.Top), true},
		{int(rect.Right) - 1, int(rect.Bottom) - 1, true},
		{int(rect.Left) - 1, int(rect.Top), false},
		{int(rect.Right), int(rect.Top), false},
		{int(rect.Left), int(rect.Top) - 1, false},
		{int(rect.Left), int(rect.Bottom), false},
	} {
		if got := themeButtonHit(width, point.x, point.y); got != point.want {
			t.Errorf("themeButtonHit(%d, %d) = %v，期望 %v", point.x, point.y, got, point.want)
		}
	}

	if themeButtonHit(0, 0, 0) {
		t.Error("宽度为 0 时不该命中任何按钮")
	}
}

// TestPanelThemeFallsBackToDark 窗口还没定过主题（空串）时按深色 —— 与窗口类
// 背景刷、panelInitialTheme(nil) 的兜底一致。
func TestPanelThemeFallsBackToDark(t *testing.T) {
	panel := &panelWindow{}
	if got := panel.panelTheme(); got != ThemeDark {
		t.Errorf("空主题应回落深色，实际 %q", got)
	}
	if panel.chrome() != &panelChromeDark {
		t.Error("空主题应取深色配色表")
	}
}

// TestRequestThemeWithoutWindowIsNoop 没有窗口（还没建好 / 已销毁）时不能 panic，
// 也不该留下待处理值。
func TestRequestThemeWithoutWindowIsNoop(t *testing.T) {
	panel := &panelWindow{}
	panel.requestTheme(ThemeDark)
	if panel.pendingTheme != "" {
		t.Errorf("窗口未就绪时不该挂待处理主题，实际 %q", panel.pendingTheme)
	}

	closed := &panelWindow{hwnd: 12345, closed: true}
	closed.requestTheme(ThemeDark)
	if closed.pendingTheme != "" {
		t.Error("已关闭的窗口不该挂待处理主题")
	}
}

// TestApplyPendingThemeIgnoresUnchangedTheme 配色没变时不重绘。
//
// 这不是省事：auto 模式下 WM_SETTINGCHANGE 会因为区域/字体/辅助功能等**各种**原因
// 到来，也不过滤就重刷三个窗口，会明显看到没必要的闪动。
func TestApplyPendingThemeIgnoresUnchangedTheme(t *testing.T) {
	panel := &panelWindow{theme: ThemeDark}
	panel.pendingTheme = ThemeDark
	panel.applyPendingTheme()

	if panel.theme != ThemeDark {
		t.Errorf("主题应保持 %q，实际 %q", ThemeDark, panel.theme)
	}
	if panel.pendingTheme != "" {
		t.Error("待处理值应被消费掉")
	}
}

// TestTogglePanelThemeSwitchesAndPersists 面板上的配色按钮：切到另一侧并**落盘**。
//
// 与设置里「跟随系统」的关系是本次刻意选定的语义：按钮只认当前生效的明暗侧，
// 按一次就把它固定下来（auto → 显式），这与管理窗口右上角那个按钮完全一致。
func TestTogglePanelThemeSwitchesAndPersists(t *testing.T) {
	app := newTestApp(t)

	cases := []struct {
		from     string // 面板当前生效的明暗侧
		wantNext string // 期望落盘的值
	}{
		{ThemeLight, ThemeDark},
		{ThemeDark, ThemeLight},
	}
	for _, c := range cases {
		if err := app.togglePanelTheme(c.from); err != nil {
			t.Fatalf("togglePanelTheme(%q): %v", c.from, err)
		}
		if got := app.GetSettings().Theme; got != c.wantNext {
			t.Errorf("从 %q 切换后设置应为 %q，实际 %q", c.from, c.wantNext, got)
		}
	}

	// auto 状态下按一次也变成显式值（不是弹回 auto）—— 这正是「切到哪边就固定成哪边」。
	if err := app.SetTheme(ThemeAuto); err != nil {
		t.Fatalf("SetTheme(auto): %v", err)
	}
	if err := app.togglePanelTheme(ThemeLight); err != nil {
		t.Fatalf("togglePanelTheme(light): %v", err)
	}
	if got := app.GetSettings().Theme; got != ThemeDark {
		t.Errorf("auto 下从浅色切换后应固定为 dark，实际 %q", got)
	}
}

// TestIsImmersiveColorSetFiltersOtherSettings 只有「深浅色偏好变了」才该重算主题。
// WM_SETTINGCHANGE 是条大杂烩消息，不过滤就会跟着瞎重绘。
func TestIsImmersiveColorSetFiltersOtherSettings(t *testing.T) {
	ptr := func(s string) uintptr {
		p, err := windows.UTF16PtrFromString(s)
		if err != nil {
			t.Fatalf("构造字符串指针失败: %v", err)
		}
		return uintptr(unsafe.Pointer(p))
	}

	if !isImmersiveColorSet(ptr("ImmersiveColorSet")) {
		t.Error("ImmersiveColorSet 应被判为深浅色变化")
	}
	for _, other := range []string{"WindowMetrics", "Environment", "Policy", ""} {
		if isImmersiveColorSet(ptr(other)) {
			t.Errorf("%q 不应触发主题重算", other)
		}
	}
	if isImmersiveColorSet(0) {
		t.Error("lParam 为 0 时不该触发（有些来源不填这个字段）")
	}
}

// TestSetThemePushesToOpenPanels 管理面板改配色时，已打开的面板窗口要收到推送。
//
// 这里用一个没有真实窗口的 panelWindow：requestTheme 会因为没有 hwnd 而静默跳过，
// 但**待处理值必须挂上** —— 这条断言守的是「推送被漏掉」这种最难发现的情况
// （界面上只是某些面板没换色，看起来像偶发）。
func TestSetThemePushesToOpenPanels(t *testing.T) {
	app := newTestApp(t)

	panel := &panelWindow{app: app, hwnd: 1, theme: ThemeDark}
	app.mu.Lock()
	app.panels = map[string]*panelWindow{"p1": panel}
	app.mu.Unlock()

	if err := app.SetTheme(ThemeLight); err != nil {
		t.Fatalf("SetTheme(light): %v", err)
	}
	if panel.pendingTheme != ThemeLight {
		t.Errorf("面板应收到 pendingTheme=%q，实际 %q", ThemeLight, panel.pendingTheme)
	}
}
