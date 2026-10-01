package main

import (
	"github.com/wailsapp/wails/v2/pkg/options"
	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/sys/windows/registry"
)

// 主题只影响 Wails 管理窗口的 WebView 内容（CSS 变量），但窗口本身还有一个
// **原生底色**（options.App.BackgroundColour）：它在 WebView 首帧绘制之前、
// 以及窗口尺寸变化尚未重绘的空隙里露出来。深色主题下它若仍是创建时的浅色，
// 用户每次启动都会先看到一记白闪。本文件负责让这个底色与主题保持一致。
//
// 明暗两侧的取值必须与前端 CSS 的 --bg token 一致（frontend/src/style.css），
// 否则窗口边缘露出的颜色与页面背景对不上。

// 管理窗口的原生底色（与 CSS --bg token 同值）。
var (
	windowBackgroundLight = options.RGBA{R: 246, G: 247, B: 249, A: 1} // #f6f7f9
	windowBackgroundDark  = options.RGBA{R: 15, G: 23, B: 42, A: 1}    // #0f172a
)

// systemThemePreference 返回 Windows 当前的应用深浅色偏好。
// 读注册表 HKCU\...\Themes\Personalize 的 AppsUseLightTheme（0 = 深色），
// 这与 WebView2 里 matchMedia('(prefers-color-scheme: dark)') 的判定同源。
// 读不到（老系统 / 键被删）按浅色处理，与前端 head 防闪脚本的兜底一致。
var systemThemePreference = defaultSystemThemePreference

func defaultSystemThemePreference() string {
	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`, registry.QUERY_VALUE)
	if err != nil {
		return ThemeLight
	}
	defer key.Close()

	value, _, err := key.GetIntegerValue("AppsUseLightTheme")
	if err != nil || value == 0 {
		return ThemeDark
	}
	return ThemeLight
}

// effectiveTheme 把设置里的配色取值解析为实际生效的明暗侧：
// 显式 light / dark 优先，auto 跟随系统偏好。
func effectiveTheme(setting string) string {
	if normalized := normalizeTheme(setting); normalized != ThemeAuto {
		return normalized
	}
	if systemThemePreference() == ThemeDark {
		return ThemeDark
	}
	return ThemeLight
}

// initialWindowBackground 返回管理窗口创建时应有的底色（main.go 的 BackgroundColour）。
// 必须在 wails.Run 之前算好：窗口显示与 WebView 首帧之间的空隙露的就是它，
// 等 startup 再改就已经晚了。
func (a *App) initialWindowBackground() options.RGBA {
	return windowBackgroundFor(a.resolveTheme())
}

// syncWindowTheme 把管理窗口的原生底色同步为当前生效主题。
// ctx 未就绪（单测 / 早期调用）时静默跳过，只是不改窗口而已。
func (a *App) syncWindowTheme() {
	a.mu.Lock()
	ctx := a.ctx
	theme := effectiveTheme(a.config.settings().Theme)
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	col := windowBackgroundFor(theme)
	wailsRuntime.WindowSetBackgroundColour(ctx, col.R, col.G, col.B, col.A)
}

// ApplyWindowTheme 由前端在**生效主题变化**时调用（含 auto 模式下系统偏好切换、
// 启动时前端解析结果与后端注册表读取不一致的兜底）。只改窗口底色，不写配置 ——
// 配置里的 theme 记的是用户的选择（可能是 auto），生效侧的解析由前端持有更及时。
//
// 面板窗口跟着一起换。这条是「系统深浅色切换」的三条路之一（另外两条：面板窗口
// 自己收 WM_SETTINGCHANGE、管理窗口前端跑 matchMedia），三者幂等，谁先到都行。
func (a *App) ApplyWindowTheme(effective string) error {
	if effective != ThemeLight && effective != ThemeDark {
		return errCodeDetail(errThemeInvalid, effective)
	}
	a.applyThemeToPanels(effective)

	col := windowBackgroundFor(effective)
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return nil
	}
	wailsRuntime.WindowSetBackgroundColour(ctx, col.R, col.G, col.B, col.A)
	return nil
}

func windowBackgroundFor(effective string) options.RGBA {
	if effective == ThemeDark {
		return windowBackgroundDark
	}
	return windowBackgroundLight
}
