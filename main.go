package main

import (
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// appMainWindowTitle 是 Wails 管理窗口标题。
// 关闭询问框需要把它当作 owner 居中显示，而 Wails v2 的 runtime 不暴露 HWND，
// 因此按标题精确查找主窗口（面板窗口标题为 "PanelDock · 面板名"，不会误匹配）。
const appMainWindowTitle = "PanelDock"

// parseAutoOpenArg 解析 --open <面板ID> 启动参数（直达面板，供快捷方式使用）。
func parseAutoOpenArg(args []string) string {
	for i := 1; i < len(args); i++ {
		if args[i] == "--open" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func main() {
	// 必须在创建任何窗口之前声明 AUMID：否则面板窗口的任务栏图标会被
	// 指向本 exe 的快捷方式劫持（详见 aumid_windows.go）。
	applyAppUserModelID()

	autoOpenID := parseAutoOpenArg(os.Args)

	// 单实例调度：已有实例时把请求转发过去后退出，避免叠出多套窗口。
	// 转发失败（极旧的实例、IPC 窗口尚未就绪）时退化为独立实例继续运行。
	if !acquireSingleInstance() {
		if forwardIPCCommand(ipcCommandFor(autoOpenID)) {
			return
		}
	}

	// Create an instance of the app structure
	app := NewApp(autoOpenID)

	// 窗口创建前先读一次配置：深色主题下窗口底色必须从一开始就是深色，
	// 否则「窗口显示 → WebView 首帧」之间的空隙会白闪一下（startup 里再改已经晚了）。
	// startup 会再 load 一次，这里不改加载时序语义，只是提前读。
	_ = app.config.load()
	background := app.initialWindowBackground()

	// Create application with options
	err := wails.Run(&options.App{
		Title:  appMainWindowTitle,
		Width:  1024,
		Height: 768,
		// 轻量模式（--open）：主窗口不显示，只出面板窗口。
		StartHidden: app.lightweight,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &background,
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.onBeforeClose,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
