//go:build windows

package main

import (
	"errors"
	"strings"
	"testing"
)

// TestNativeUITextsComplete 保证每种内置语言的原生文案都填全了。
// 词典是 map 字面量，编译器帮不上忙 —— 少填一个键或留空一个字段，运行时才会以
// 「托盘菜单少一项 / 询问框按钮空白」的形式暴露。这里把结构性检查钉住。
func TestNativeUITextsComplete(t *testing.T) {
	if len(nativeUITexts) < 2 {
		t.Fatalf("内置语言应至少两种，实际 %d", len(nativeUITexts))
	}
	for lang, text := range nativeUITexts {
		c := text.ClosePrompt
		for name, v := range map[string]string{
			"ClosePrompt.Caption":  c.Caption,
			"ClosePrompt.Title":    c.Title,
			"ClosePrompt.Body":     c.Body,
			"ClosePrompt.Tray":     c.Tray,
			"ClosePrompt.Close":    c.Close,
			"ClosePrompt.Remember": c.Remember,
			"ClosePrompt.Cancel":   c.Cancel,
			"ClosePrompt.Note":     c.Note,
		} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s 的 %s 为空", lang, name)
			}
		}

		e := text.EnablePrompt
		for name, v := range map[string]string{
			"EnablePrompt.Caption":      e.Caption,
			"EnablePrompt.TitleFmt":     e.TitleFmt,
			"EnablePrompt.Body":         e.Body,
			"EnablePrompt.Open":         e.Open,
			"EnablePrompt.Keep":         e.Keep,
			"EnablePrompt.FallbackName": e.FallbackName,
		} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s 的 %s 为空", lang, name)
			}
		}
		if !strings.Contains(e.TitleFmt, "{name}") {
			t.Errorf("%s 的 EnablePrompt.TitleFmt 应含 {name} 占位符，实际 %q", lang, e.TitleFmt)
		}

		tr := text.Tray
		for name, v := range map[string]string{
			"Tray.Tip":         tr.Tip,
			"Tray.ShowHide":    tr.ShowHide,
			"Tray.TopMost":     tr.TopMost,
			"Tray.ClosePanel":  tr.ClosePanel,
			"Tray.OpenManager": tr.OpenManager,
			"Tray.Quit":        tr.Quit,
		} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s 的 %s 为空", lang, name)
			}
		}
		if !strings.Contains(tr.ActiveSuffixFmt, "{name}") {
			t.Errorf("%s 的 Tray.ActiveSuffixFmt 应含 {name} 占位符，实际 %q", lang, tr.ActiveSuffixFmt)
		}
	}
}

// TestNormalizeLanguage 覆盖语言取值规整：合法值原样通过，空串与非法值落到「跟随系统」。
func TestNormalizeLanguage(t *testing.T) {
	cases := map[string]string{
		"":        LanguageAuto,
		"auto":    LanguageAuto,
		"zh-CN":   LanguageZhCN,
		"en-US":   LanguageEnUS,
		"zh":      LanguageAuto, // 不硬猜前缀
		"ZH-CN":   LanguageAuto, // 大小写敏感
		"english": LanguageAuto,
	}
	for in, want := range cases {
		if got := normalizeLanguage(in); got != want {
			t.Errorf("normalizeLanguage(%q) = %q, want %q", in, got, want)
		}
	}
	if !isValidLanguage(LanguageAuto) || !isValidLanguage(LanguageZhCN) || !isValidLanguage(LanguageEnUS) {
		t.Error("三个合法取值都应可写入")
	}
	if isValidLanguage("zh") {
		t.Error("非法取值不应可写入")
	}
}

// TestSetLanguagePersistsAndNormalizes 验证 SetLanguage 落盘且非法值被拒。
func TestSetLanguagePersistsAndNormalizes(t *testing.T) {
	store := newTestStore(t)
	app := &App{config: store}

	if err := app.SetLanguage(LanguageEnUS); err != nil {
		t.Fatalf("SetLanguage(en-US): %v", err)
	}
	if got := store.settings().Language; got != LanguageEnUS {
		t.Errorf("设置应落盘为 en-US，实际 %q", got)
	}
	// 写 auto（显式跟随系统）也是合法值。
	if err := app.SetLanguage(LanguageAuto); err != nil {
		t.Fatalf("SetLanguage(auto): %v", err)
	}
	err := app.SetLanguage("fr-FR")
	if err == nil {
		t.Fatal("未内置的语言应被拒绝")
	}
	var ae *appError
	if !errors.As(err, &ae) || ae.code != errLanguageInvalid {
		t.Errorf("应返回 settings.languageInvalid 错误码，实际 %v", err)
	}
}
