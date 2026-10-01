//go:build windows

package main

import "testing"

// TestPlausibleWindowRect 锁定「一个窗口矩形值不值得写进配置」的判断。
//
// 背景是一个真实事故（2026-09-30）：面板最小化后从托盘关闭，dispose 里记下的
// GetWindowRect 是 (-32000,-32000,160,28) —— Windows 表示「图标位置」的哨兵值 ——
// 配置里于是存下了 x=-32000 / 160x28，下次打开那个面板缩成一个小方块还落在屏幕外。
// 判断本身是纯函数，所以逐条钉住最省事。
func TestPlausibleWindowRect(t *testing.T) {
	cases := []struct {
		name string
		rect panelRECT
		want bool
	}{
		{"正常窗口", panelRECT{Left: 100, Top: 80, Right: 1220, Bottom: 840}, true},
		{"贴着屏幕左上角", panelRECT{Left: 0, Top: 0, Right: 1120, Bottom: 760}, true},
		{"负坐标但仍在桌面（副屏在主屏左侧）", panelRECT{Left: -1600, Top: 100, Right: -480, Bottom: 860}, true},
		{"最小化哨兵矩形", panelRECT{Left: -32000, Top: -32000, Right: -31840, Bottom: -31972}, false},
		{"宽度被压成零头", panelRECT{Left: 100, Top: 100, Right: 180, Bottom: 700}, false},
		{"高度被压成零头", panelRECT{Left: 100, Top: 100, Right: 900, Bottom: 160}, false},
		{"退化成一个点", panelRECT{Left: 300, Top: 300, Right: 300, Bottom: 300}, false},
	}
	for _, c := range cases {
		if got := plausibleWindowRect(c.rect); got != c.want {
			t.Errorf("%s: plausibleWindowRect=%v，期望 %v（rect=%+v）", c.name, got, c.want, c.rect)
		}
	}
}

// TestPlausibleWindowState 守「写进配置前」那道检查（与 Rect 版同一判据的配置侧入口）。
//
// 同一条事故的出口防线：即使某条新路径把 lastRect 弄脏，也不能把坏值写进配置。
func TestPlausibleWindowState(t *testing.T) {
	cases := []struct {
		name  string
		state PanelWindowState
		want  bool
	}{
		{"正常", PanelWindowState{X: 818, Y: 299, Width: 1120, Height: 760}, true},
		{"尺寸未设置（老配置）", PanelWindowState{}, false},
		{"最小化哨兵值", PanelWindowState{X: -32000, Y: -32000, Width: 160, Height: 28}, false},
		{"高度被压成零头", PanelWindowState{X: 100, Y: 100, Width: 900, Height: 60}, false},
	}
	for _, c := range cases {
		if got := plausibleWindowState(c.state); got != c.want {
			t.Errorf("%s: plausibleWindowState=%v，期望 %v（state=%+v）", c.name, got, c.want, c.state)
		}
	}
}

// TestInitialWindowRectFallsBackOnDirtyState 验证配置里存着坏值时退回默认尺寸，
// 而不是「照单全收地打开一个小窗口」。修复只防将来，存量脏数据靠这条兜底。
func TestInitialWindowRectFallsBackOnDirtyState(t *testing.T) {
	good := PanelWindowState{X: 605, Y: 418, Width: 1120, Height: 760}
	got := initialWindowRect(good)
	if got.Left != 605 || got.Top != 418 || got.Right != 1725 || got.Bottom != 1178 {
		t.Errorf("正常窗口状态应原样转成矩形，实际 %+v", got)
	}

	dirty := []struct {
		name  string
		state PanelWindowState
	}{
		{"最小化时写下的哨兵值", PanelWindowState{X: -32000, Y: -32000, Width: 160, Height: 28}},
		{"尺寸小到不像窗口", PanelWindowState{Width: 160, Height: 28}},
		{"老配置里什么都没有", PanelWindowState{}},
	}
	for _, c := range dirty {
		got := initialWindowRect(c.state)
		if w, h := got.Right-got.Left, got.Bottom-got.Top; w != defaultPanelWindowWidth || h != defaultPanelWindowHeight {
			t.Errorf("%s：应退回默认尺寸 %dx%d，实际 %dx%d",
				c.name, defaultPanelWindowWidth, defaultPanelWindowHeight, w, h)
		}
	}
}
