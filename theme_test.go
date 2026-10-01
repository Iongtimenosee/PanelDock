package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestThemeNormalization 钉住取值规整规则：light / dark 原样通过，
// 空串（老配置缺键）与非法值一律落到「跟随系统」。
func TestThemeNormalization(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ThemeAuto},
		{"auto", ThemeAuto},
		{ThemeLight, ThemeLight},
		{ThemeDark, ThemeDark},
		{"blue", ThemeAuto},
		{"DARK", ThemeAuto}, // 大小写敏感：不认的写法按默认处理，不猜
	}
	for _, c := range cases {
		if got := normalizeTheme(c.in); got != c.want {
			t.Errorf("normalizeTheme(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	for _, valid := range []string{ThemeAuto, ThemeLight, ThemeDark} {
		if !isValidTheme(valid) {
			t.Errorf("isValidTheme(%q) 应为 true", valid)
		}
	}
	for _, invalid := range []string{"", "blue", "Light"} {
		if isValidTheme(invalid) {
			t.Errorf("isValidTheme(%q) 应为 false", invalid)
		}
	}
}

// TestThemePersistRoundTrip 走前端真正调用的桥接方法：设置 → 读回 → 非法值拒收。
func TestThemePersistRoundTrip(t *testing.T) {
	app := newTestApp(t)

	for _, theme := range []string{ThemeDark, ThemeLight, ThemeAuto} {
		if err := app.SetTheme(theme); err != nil {
			t.Fatalf("SetTheme(%q): %v", theme, err)
		}
		if got := app.GetSettings().Theme; got != theme {
			t.Errorf("设置 %q 后读回 %q", theme, got)
		}
	}

	if err := app.SetTheme("blue"); err == nil {
		t.Error("非法取值应被拒绝")
	}
	// 拒收之后原值保持不变（不是「写坏再发现」）。
	if got := app.GetSettings().Theme; got != ThemeAuto {
		t.Errorf("拒收后取值应保持 %q，实际 %q", ThemeAuto, got)
	}
}

// TestThemeLegacyConfigUnchanged 验证老配置（没有 theme 字段）读出来是「跟随系统」，
// 且加载**一个字节都不改**。这是「跑完 E2E 配置零污染」检查的前提：
// theme 与 sessionMode 一样不做加载期迁移，缺键的语义由读取路径的归一化兜住。
func TestThemeLegacyConfigUnchanged(t *testing.T) {
	settingsJSON, err := json.Marshal(defaultAppSettings())
	if err != nil {
		t.Fatalf("编码默认设置失败: %v", err)
	}
	// defaultAppSettings 不含 Theme（空串 + omitempty），这份字面量天然就是「老配置」。
	if _, exists := func() (map[string]json.RawMessage, bool) {
		var probe map[string]json.RawMessage
		_ = json.Unmarshal(settingsJSON, &probe)
		_, ok := probe["theme"]
		return probe, ok
	}(); exists {
		t.Fatal("默认设置不应包含 theme 键，请检查 omitempty 是否还在")
	}

	for name, settings := range map[string]string{
		"缺键": string(settingsJSON),
		// 注意：这份「非法值」夹具必须写全其余默认开启的键 —— settingsHasKey 的迁移
		// 只对缺失键补默认值并回写（与本功能无关），夹具缺键会让断言失真到 theme 头上。
		"非法值": `{"showTrayIcon":true,"panelCloseAction":"ask","lightweightQuitOnLastPanel":true,"theme":"blue"}`,
	} {
		original := []byte(`{
  "schemaVersion": 1,
  "settings": ` + settings + `,
  "panels": []
}`)
		path := filepath.Join(t.TempDir(), "legacy.json")
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatalf("写入老配置失败: %v", err)
		}

		store := newConfigStore()
		store.path = path
		if err := store.load(); err != nil {
			t.Fatalf("载入老配置失败: %v", err)
		}

		if got := store.settings().Theme; got != ThemeAuto {
			t.Errorf("[%s] 老配置读出的配色应为 %q，实际 %q", name, ThemeAuto, got)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读回配置失败: %v", err)
		}
		if string(after) != string(original) {
			t.Errorf("[%s] 加载不应写回配置文件\n修改前: %s\n修改后: %s", name, original, after)
		}
	}
}

// TestEffectiveThemeResolvesAuto 验证生效主题的解析：显式 light / dark 优先，
// auto 跟随系统偏好（注入 systemThemePreference 缝，别依赖跑测试的这台机器）。
func TestEffectiveThemeResolvesAuto(t *testing.T) {
	orig := systemThemePreference
	t.Cleanup(func() { systemThemePreference = orig })

	for _, pref := range []string{ThemeLight, ThemeDark} {
		systemThemePreference = func() string { return pref }
		if got := effectiveTheme(ThemeAuto); got != pref {
			t.Errorf("auto + 系统 %q 应解析为 %q，实际 %q", pref, pref, got)
		}
		// 显式取值压过系统偏好：这正是「跟随系统」与「固定」的差别。
		systemThemePreference = func() string { return ThemeDark }
		if got := effectiveTheme(ThemeLight); got != ThemeLight {
			t.Errorf("显式 light 不应被系统偏好覆盖，实际 %q", got)
		}
		systemThemePreference = func() string { return ThemeLight }
		if got := effectiveTheme(ThemeDark); got != ThemeDark {
			t.Errorf("显式 dark 不应被系统偏好覆盖，实际 %q", got)
		}
	}

	// 非法 / 空取值按 auto 解析。
	systemThemePreference = func() string { return ThemeDark }
	if got := effectiveTheme(""); got != ThemeDark {
		t.Errorf("空取值应按 auto 解析为 dark，实际 %q", got)
	}
}

// TestApplyWindowThemeRejectsInvalid 验证前端回传的生效主题只认 light / dark。
// ctx 为 nil（单测环境）时合法取值静默成功、不 panic。
func TestApplyWindowThemeRejectsInvalid(t *testing.T) {
	app := newTestApp(t)

	if err := app.ApplyWindowTheme("blue"); err == nil {
		t.Error("非法取值应被拒绝")
	}
	if err := app.ApplyWindowTheme(ThemeDark); err != nil {
		t.Errorf("dark 应被接受: %v", err)
	}
}

// TestInitialWindowBackground 验证窗口创建时的底色跟随生效主题
// （深色主题下窗口从第一帧起就是深底，见 theme_windows.go 的说明）。
func TestInitialWindowBackground(t *testing.T) {
	app := newTestApp(t)
	orig := systemThemePreference
	t.Cleanup(func() { systemThemePreference = orig })

	systemThemePreference = func() string { return ThemeLight }
	if got := app.initialWindowBackground(); got != windowBackgroundLight {
		t.Errorf("浅色生效时窗口底色应为浅色，实际 %+v", got)
	}

	systemThemePreference = func() string { return ThemeDark }
	if got := app.initialWindowBackground(); got != windowBackgroundDark {
		t.Errorf("auto + 系统深色时窗口底色应为深色，实际 %+v", got)
	}

	if err := app.SetTheme(ThemeLight); err != nil {
		t.Fatalf("SetTheme(light): %v", err)
	}
	systemThemePreference = func() string { return ThemeDark }
	if got := app.initialWindowBackground(); got != windowBackgroundLight {
		t.Errorf("显式 light 应压过系统深色偏好，实际 %+v", got)
	}
}
