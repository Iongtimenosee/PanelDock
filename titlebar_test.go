//go:build windows

package main

import "testing"

// TestTitleBarLayoutHasPinButton 锁定「置顶开关」的几何。
//
// 布局是绘制与命中测试**共用**的一份几何（`paintTitleBar` 与 `buttonAt` 都调
// `panelTitleBarLayoutFor`），所以这里能钉住的东西很具体：按钮存在、互不重叠、
// 中心点能被正确识别。写错了不会编译报错，只会「画出来了但点不准」。
func TestTitleBarLayoutHasPinButton(t *testing.T) {
	const width = int32(1120)
	l := panelTitleBarLayoutFor(width)

	if l.pin.Right > l.settings.Left {
		t.Errorf("置顶开关与设置按钮重叠：pin=%+v settings=%+v", l.pin, l.settings)
	}
	if l.pin.Left < l.address.Left {
		t.Errorf("置顶开关压到了地址栏：pin=%+v address=%+v", l.pin, l.address)
	}
	if w := l.address.Right - l.address.Left; w <= 0 {
		t.Errorf("地址栏宽度被按钮挤成 %d（加了置顶开关后放不下了）", w)
	}

	pinX := (l.pin.Left + l.pin.Right) / 2
	pinY := (l.pin.Top + l.pin.Bottom) / 2
	if got := l.buttonAt(pinX, pinY); got != tbBtnPin {
		t.Errorf("置顶开关中心命中 %d，期望 tbBtnPin=%d", got, tbBtnPin)
	}
	// 设置按钮紧挨着置顶开关，是最容易串味的一个 —— 单独钉一下。
	setX := (l.settings.Left + l.settings.Right) / 2
	if got := l.buttonAt(setX, pinY); got != tbBtnSettings {
		t.Errorf("设置按钮中心命中 %d，期望 tbBtnSettings=%d", got, tbBtnSettings)
	}
	if got := l.buttonRect(tbBtnPin); got != l.pin {
		t.Errorf("buttonRect(tbBtnPin)=%+v，应与布局里的 pin 一致（%+v）", got, l.pin)
	}
}

// TestTitleBarPinGlyphs 钉住置顶开关的两个字形。
//
// 码位是 2026-09-30 在本机 `C:\Windows\Fonts\segmdl2.ttf` 的 cmap 里逐个确认存在的
// （E718 Pin / E840 Pinned），别凭记忆改：字体里没有的码位不会报错，只会渲染成豆腐块，
// 而「渲染成豆腐块」编译、vet、单测、E2E 全都发现不了。
//
// 两个字形必须不同，否则「未置顶」与「已置顶」在界面上完全一样，开关就成了盲操作。
func TestTitleBarPinGlyphs(t *testing.T) {
	cases := []struct {
		name  string
		glyph string
		want  rune
	}{
		{"未置顶", tbGlyphPin, 0xE718},
		{"已置顶", tbGlyphPinned, 0xE840},
	}
	for _, c := range cases {
		runes := []rune(c.glyph)
		if len(runes) != 1 {
			t.Errorf("%s：字形应是单个码位，实际 %d 个（%q）", c.name, len(runes), c.glyph)
			continue
		}
		if runes[0] != c.want {
			t.Errorf("%s：码位应为 U+%04X，实际 U+%04X", c.name, c.want, runes[0])
		}
	}
	if tbGlyphPin == tbGlyphPinned {
		t.Error("「未置顶」与「已置顶」用了同一个字形，用户分辨不出状态")
	}
}
