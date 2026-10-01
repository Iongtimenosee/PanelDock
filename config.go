package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

const (
	configSchemaVersion = 1
	configDirName       = "PanelDock"
	configFileName      = "config.json"
)

// 关闭**面板窗口**（分组标签窗口）时的行为。
// 管理窗口没有这一项：它的关闭动作是固定的（见 App.closeManagerWindow）。
const (
	// CloseActionAsk 每次关闭都弹窗询问（默认）。
	CloseActionAsk = "ask"
	// CloseActionTray 直接最小化到系统托盘（窗口隐藏，程序继续运行）。
	CloseActionTray = "tray"
	// CloseActionClose 直接关闭、不再询问：只关闭该面板窗口，其他面板、管理窗口与
	// 托盘图标都不受影响；进程是否退出由「还有没有窗口与托盘」判定（见 App.quitWhenNoWindows）。
	CloseActionClose = "close"
	// legacyCloseActionQuit 是旧版取值「直接退出」：在面板窗口上也会结束整个程序，
	// 与用户预期不符，已废弃。仅用于读取老配置，加载时迁移为 CloseActionClose。
	legacyCloseActionQuit = "quit"
)

// 面板关闭后如何处理各标签的 WebView2 会话（浏览状态）。
const (
	// SessionModePersist 关闭面板后**保留**各标签的 WebView2 profile：
	// Cookie、登录态、缓存、本地存储都留在磁盘上，下次打开接着上次的会话。默认行为。
	SessionModePersist = "persist"
	// SessionModeFresh 关闭面板后**清空**该面板所有标签的 profile 目录；
	// 打开前还会再清一次兜底（上次若因崩溃/强杀没清干净，这里补上），
	// 因此每次打开都等同全新环境：未登录、无缓存、无本地存储。
	SessionModeFresh = "fresh"
)

// 分组级的「保存登录密码」开关取值，对应 WebView2 的 IsPasswordAutosaveEnabled。
//
// 用字符串枚举而不是 bool：这个开关**默认开启**，而 json.Unmarshal 分不清「键缺失」与
// 「显式 false」—— 老配置里没有这个键，读出来就是零值，等于把所有老用户的分组悄悄关掉。
// 空串代表「默认」是这套配置层已经用惯的写法（见 SessionMode）。
const (
	// PasswordAutosaveOn 启用：登录后由 WebView2 弹出「保存密码」提示，下次自动回填。
	// 默认值，也是老配置缺少该字段时的语义。
	PasswordAutosaveOn = "on"
	// PasswordAutosaveOff 不再保存新密码、也不再弹保存提示。
	//
	// ⚠️ WebView2 的既定语义：这个属性**只管「保存」**。关掉它之后，此前已经存下来的
	// 密码仍然会被建议、被回填（官方 Autofill.md：no new password data is saved and no
	// Save/Update Password prompts are displayed. However, if there was password data
	// already saved before disabling this setting, then that password information is
	// auto-populated, suggestions are shown...）。想要「一点不留」得靠面板的
	// SessionModeFresh（关闭即清空整个 profile），不是靠这个开关。
	PasswordAutosaveOff = "off"
)

// 界面语言（settings.language）。管**本程序自己画的界面文案**：管理窗口（前端词典）
// 与原生控件（询问框、托盘菜单，见 native_text_windows.go）。
//
// 够不着的地方要说清楚：WebView2 自己的右键菜单、系统输入框等跟随 Windows 显示
// 语言，由系统自己本地化，本设置管不到它们。
const (
	// LanguageAuto 跟随 Windows 显示语言（默认，也是老配置缺少该字段时的语义）：
	// 中文主语言 → zh-CN，其余 → en-US（见 systemLanguage）。
	LanguageAuto = "auto"
	// LanguageZhCN 简体中文。
	LanguageZhCN = "zh-CN"
	// LanguageEnUS 英语。
	LanguageEnUS = "en-US"
)

// 界面配色主题（settings.theme）。管**两个窗口的外壳**：
// 管理窗口走 CSS token（frontend/src/style.css），面板窗口走 GDI 配色表
// （theme_panel_windows.go），两边同名对齐。
//
// 够不着的地方要说清楚：面板里显示的是别人的页面，本程序不向远程页面注入任何样式或
// 脚本（项目硬不变量），所以页面的深浅由站点自己决定，只有外壳 + WebView2 的默认底色
// 跟着走。
const (
	// ThemeAuto 跟随 Windows 的深浅色偏好（默认，也是老配置缺少该字段时的语义）。
	ThemeAuto = "auto"
	// ThemeLight 固定浅色。
	ThemeLight = "light"
	// ThemeDark 固定深色。
	ThemeDark = "dark"
)

// AppSettings 是跨面板的应用级设置，持久化在 config.json 的 settings 节点。
type AppSettings struct {
	// ShowTrayIcon 控制是否在系统托盘注册图标（默认开启）。
	// 关闭后托盘菜单不可用，「从托盘呼出管理面板」的入口随之消失。
	ShowTrayIcon bool `json:"showTrayIcon"`

	// Theme 是界面明暗配色（ThemeAuto / ThemeLight / ThemeDark），管理窗口与面板窗口共用。
	// 用字符串枚举而不是 bool：默认值是「跟随系统」而不是固定某一侧，bool 表达不了；
	// 且空串（老配置缺键）天然落到 ThemeAuto，不需要 settingsHasKey 那套按键补默认值。
	// 读取路径统一过 normalizeTheme，不要直接比较这个字段。
	//
	// 面板窗口那个配色按钮写的就是这一项（App.togglePanelTheme）：它是**面板侧**改设置，
	// 必须 notifySettingsChanged 广播，否则管理界面开着时会一直显示旧配色。
	Theme string `json:"theme,omitempty"`

	// Language 是界面语言（LanguageAuto / LanguageZhCN / LanguageEnUS）。
	// 与 Theme 同一套「字符串枚举 + 零值即默认」的写法：空串（老配置缺键）天然落到
	// LanguageAuto，不需要 settingsHasKey 补默认值。读取路径统一过 normalizeLanguage。
	//
	// 切换由前端发起、成功后 location.reload() 重新渲染，因此 SetLanguage 不需要
	// 广播 settings-changed（与 SetTheme 同一论证：改设置的唯一入口就是发起方自己）。
	Language string `json:"language,omitempty"`

	// PanelCloseAction 为关闭**面板窗口**（分组标签窗口）时的行为：ask / tray / close。
	// CloseActionClose 在这里表示「只关闭这个面板窗口」，其他窗口与托盘图标都不受影响。
	PanelCloseAction string `json:"panelCloseAction"`

	// LightweightQuitOnLastPanel 决定**轻量模式**（`--open` 启动、管理窗口从未打开）
	// 下关掉最后一个面板后是否直接退出程序，默认开启。
	//
	// 开启 = 「用完即走」：双击快捷方式进来干一件事，关掉就走，不在托盘里留进程。
	// 关闭 = 与常规启动完全一致：只要托盘图标还开着就留在托盘里等用户回来。
	// 常规启动不受它影响（管理窗口关掉后进程本来就由「有没有窗口与托盘」判定）。
	LightweightQuitOnLastPanel bool `json:"lightweightQuitOnLastPanel"`

	// DeprecatedCloseAction 是旧版「两类窗口共用一个关闭行为」的单一字段。
	// 加载时写入 PanelCloseAction（旧值 quit 归一化为 close），随后清空。
	DeprecatedCloseAction string `json:"closeAction,omitempty"`

	// DeprecatedManagerCloseAction 是旧版的「关闭管理面板时」设置。
	// 管理窗口的关闭动作现已固定（有其它窗口或托盘图标就只关自己，否则退出程序），
	// 这一项无处可迁移，加载时直接清掉，配置文件里不再保留它。
	DeprecatedManagerCloseAction *string `json:"managerCloseAction,omitempty"`
}

// defaultAppSettings 返回应用级设置的默认值。
// 托盘图标默认开启：轻量模式下主窗口是隐藏的，托盘菜单是呼回管理界面最方便的入口。
// 轻量模式「用完即走」默认开启：快捷方式直达的实例关掉最后一个面板就收工。
func defaultAppSettings() AppSettings {
	return AppSettings{
		ShowTrayIcon:               true,
		PanelCloseAction:           CloseActionAsk,
		LightweightQuitOnLastPanel: true,
	}
}

// minimizeToTrayConfigured 报告面板窗口是否被设置为「关闭时最小化到托盘」。
//
// 这是「托盘图标必须开着」的设置侧依据：选了该行为的面板一关闭就藏进托盘，
// 而托盘图标是把它找回来的唯一入口 —— 没有图标，那个窗口就等于丢了。
func (s AppSettings) minimizeToTrayConfigured() bool {
	return normalizeCloseAction(s.PanelCloseAction) == CloseActionTray
}

// ErrTrayIconRequired 表示「取消在系统托盘显示图标」被拒绝，原因是有窗口的关闭行为
// 被设置为「最小化到托盘」。文案直接面向用户（由前端提示），说清「做不到」和「怎么解」。
var ErrTrayIconRequired = errors.New(
	"「关闭面板窗口时」被设置为「最小化到托盘（程序在后台继续运行）」，托盘图标必须保留 —— " +
		"面板窗口关闭后会藏进托盘，图标是找回它的唯一入口。" +
		"请先把「关闭面板窗口时」改成别的，再取消托盘图标")

// closeAction 返回已记住的面板窗口关闭行为。
func (s AppSettings) closeAction() string {
	return normalizeCloseAction(s.PanelCloseAction)
}

// isValidCloseAction 判断关闭行为取值是否可写入。
// 旧版 quit 只允许读、不允许写（写入它已被 normalizeCloseAction 取代）。
func isValidCloseAction(action string) bool {
	return action == CloseActionAsk || action == CloseActionTray || action == CloseActionClose
}

// normalizeCloseAction 把配置里的关闭行为规整为当前合法取值：
// 旧版 quit → close；其余非法值 → ask。读取路径上统一走这里，
// 保证「面板窗口不再直接退出整个程序」这一修正在老配置上同样生效。
func normalizeCloseAction(action string) string {
	switch action {
	case CloseActionAsk, CloseActionTray, CloseActionClose:
		return action
	case legacyCloseActionQuit:
		return CloseActionClose
	default:
		return CloseActionAsk
	}
}

// normalizeTheme 把配置里的配色取值规整为合法值：light / dark 原样通过，
// 空串（老配置缺键）与非法值一律落到「跟随系统」。
// 与 normalizeSessionMode 同一套「零值即默认」的写法；load() 不为它写回，
// 否则每次启动都会给配置添上这个字段，破坏「跑完 E2E 配置零污染」的检查。
func normalizeTheme(theme string) string {
	if theme == ThemeLight || theme == ThemeDark {
		return theme
	}
	return ThemeAuto
}

// isValidTheme 判断配色取值是否可写入（跟随系统也是显式合法值，用户在下拉里选它）。
func isValidTheme(theme string) bool {
	return theme == ThemeAuto || theme == ThemeLight || theme == ThemeDark
}

// normalizeLanguage 把配置里的语言取值规整为合法值：zh-CN / en-US 原样通过，
// 空串（老配置缺键）与非法值一律落到「跟随系统」。与 normalizeTheme 同一套
// 「零值即默认」的写法；load() 不为它写回，保持「跑完 E2E 配置零污染」。
func normalizeLanguage(language string) string {
	if language == LanguageZhCN || language == LanguageEnUS {
		return language
	}
	return LanguageAuto
}

// isValidLanguage 判断语言取值是否可写入（跟随系统也是显式合法值，用户在下拉里选它）。
func isValidLanguage(language string) bool {
	return language == LanguageAuto || language == LanguageZhCN || language == LanguageEnUS
}

// 面板窗口的默认尺寸：新建面板用它，从配置里读到不可信窗口状态时（见 panel_window_windows.go
// 的 initialWindowRect）也退回它。两处必须是同一个值，否则同一个面板会因为历史脏数据
// 拿到与新建面板不同的尺寸。
const (
	defaultPanelWindowWidth  = 1120
	defaultPanelWindowHeight = 760
)

// PanelWindowState 记录面板窗口的位置与大小（屏幕坐标，含非客户区）。
// 位置与大小在面板窗口关闭或移动结束时写回配置，下次打开时恢复。
type PanelWindowState struct {
	X         int  `json:"x"`
	Y         int  `json:"y"`
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximized bool `json:"maximized,omitempty"`
}

// PanelTab 描述面板内的一个标签页。
// 每个标签页拥有独立的 WebView2 profile，登录会话互不干扰。
type PanelTab struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// PanelConfig 描述一个分组（面板）的配置。
//
// 注意：这个结构里没有任何密码字段。面板登录过的密码由 WebView2 自己加密后存在各标签的
// profile 目录里，本工具不读取也不导出；PasswordAutosave 只是「要不要让 WebView2 保存」
// 这个开关的值。
type PanelConfig struct {
	ID              string           `json:"id"`
	Name            string           `json:"name"`
	Tabs            []PanelTab       `json:"tabs,omitempty"`
	Enabled         bool             `json:"enabled"`
	AlwaysOnTop     bool             `json:"alwaysOnTop"`
	Window          PanelWindowState `json:"window"`
	DefaultTabIndex int              `json:"defaultTabIndex,omitempty"`

	// DeprecatedMinimizeToTray 是旧版的面板级「最小化到托盘」开关。
	// 该功能已收回：托盘相关行为统一由应用级设置（两个「关闭行为」下拉）决定，
	// 面板上不再有这一项。加载时若发现这个键就清掉并写回，配置文件里不再保留它。
	DeprecatedMinimizeToTray *bool `json:"minimizeToTray,omitempty"`

	// SessionMode 决定关闭面板后如何处理各标签的 WebView2 profile：
	//   - SessionModePersist（默认，也是老配置缺少该字段时的语义）：保留全部状态；
	//   - SessionModeFresh：关闭后清空，每次打开都是全新环境。
	// 读取路径统一过 normalizeSessionMode，因此**不要**直接比较这个字段，
	// 用 PanelConfig.clearsSessionOnClose()。
	SessionMode string `json:"sessionMode,omitempty"`

	// PasswordAutosave 决定这个分组的标签是否启用「保存登录密码」
	// （WebView2 原生的密码保存，取值 on / off；空串与非法值一律按 on 处理）。
	// 同样**不要**直接比较这个字段，用 PanelConfig.savesPasswords()。
	//
	// 密码由 WebView2 加密后存在该标签自己的 profile 目录里，本工具不读取也不导出；
	// 因此「关闭即清空」的分组一关闭，密码就随 profile 一起被清掉。
	PasswordAutosave string `json:"passwordAutosave,omitempty"`

	// Shortcut 记录由本工具为该面板创建的桌面快捷方式（.lnk 完整路径），**至多一个**。
	// 删除面板时据此清理；同时还会扫描桌面兜底（见 listPanelShortcuts）。
	//
	// 为什么不是数组（2026-09-30 用户要求「一个分组桌面只能创建一个快捷方式」）：
	// 桌面是给人看的。同一个分组点几次「桌面快捷方式」就堆出「名字.lnk / 名字 (2).lnk /
	// 名字 (3).lnk」，用户分不清哪个是哪个，删面板时还要一口气清一堆。
	// 现在要刷新就**覆盖桌面那一份**（见 ensurePanelDesktopShortcut），不再新增。
	Shortcut string `json:"shortcut,omitempty"`

	// DeprecatedShortcuts 仅用于向后兼容旧版配置（旧版允许一个分组记录多个快捷方式）。
	// 载入时收敛成第一个仍然存在的路径写进 Shortcut，然后清空。
	DeprecatedShortcuts []string `json:"shortcuts,omitempty"`

	// DeprecatedURL 仅用于向后兼容旧版配置（旧版面板使用顶层 url 字段）。
	// JSON 反序列化后迁移到 Tabs 中，然后清空。
	DeprecatedURL string `json:"url,omitempty"`
}

// ActiveURL 返回面板的有效 URL。
func (c PanelConfig) ActiveURL() string {
	if len(c.Tabs) > 0 {
		return c.Tabs[0].URL
	}
	return ""
}

// clearsSessionOnClose 报告该面板是否「关闭后清空浏览器状态」。
// 判断收敛在这里：配置里的取值可能是空串（老配置）、大小写变体或非法值，
// 逐个调用点自己比较字符串迟早会漏掉某一条路径。
func (c PanelConfig) clearsSessionOnClose() bool {
	return normalizeSessionMode(c.SessionMode) == SessionModeFresh
}

// savesPasswords 报告该分组是否启用「保存登录密码」。
// 同 clearsSessionOnClose：把「空串/非法值算默认」这条规则收在一处，避免逐个调用点自己比较。
func (c PanelConfig) savesPasswords() bool {
	return normalizePasswordAutosave(c.PasswordAutosave) != PasswordAutosaveOff
}

// normalizePasswordAutosave 把配置里的「保存登录密码」取值规整为合法值：
// 只有显式 PasswordAutosaveOff 才算关闭，其余（空串、老配置、非法值）一律按开启 ——
// 与 normalizeSessionMode 同一套「零值即默认」的写法。
func normalizePasswordAutosave(mode string) string {
	if mode == PasswordAutosaveOff {
		return PasswordAutosaveOff
	}
	return PasswordAutosaveOn
}

// isValidPasswordAutosave 判断「保存登录密码」取值是否可写入。
func isValidPasswordAutosave(mode string) bool {
	return mode == PasswordAutosaveOn || mode == PasswordAutosaveOff
}

// normalizeSessionMode 把配置里的会话处理方式规整为合法取值：
// 只有显式 SessionModeFresh 才算「清空」，其余（含老配置的空串、非法值）一律「保留」。
func normalizeSessionMode(mode string) string {
	if mode == SessionModeFresh {
		return SessionModeFresh
	}
	return SessionModePersist
}

// isValidSessionMode 判断会话处理方式取值是否可写入。
func isValidSessionMode(mode string) bool {
	return mode == SessionModePersist || mode == SessionModeFresh
}

// AppConfig 是持久化到 %APPDATA%\PanelDock\config.json 的根结构。
// Settings 用指针是为了区分「节点缺失」（老配置 → 套用默认值并写回）
// 与「显式关闭」（showTrayIcon: false 必须被尊重）。
type AppConfig struct {
	SchemaVersion int           `json:"schemaVersion"`
	Settings      *AppSettings  `json:"settings,omitempty"`
	Panels        []PanelConfig `json:"panels"`
}

// configStore 负责配置的加载、原子写入与 CRUD。
type configStore struct {
	mu     sync.Mutex
	path   string
	config AppConfig
}

func newConfigStore() *configStore {
	return &configStore{path: defaultConfigPath()}
}

// resolvePortableRoot 检测便携模式；测试可注入替换。
// exe 同目录存在 data 文件夹时启用便携模式：配置与 WebView2 会话全部存放于
// <exe目录>\data\ 下（config.json + WebViewProfiles\<tabID>），整体拷走即可迁移
// （U 盘 / 其他机器）。无 data 目录时维持系统目录（%APPDATA% / %LOCALAPPDATA%）。
var resolvePortableRoot = defaultPortableRoot

func defaultPortableRoot() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	root := filepath.Join(filepath.Dir(exe), "data")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root
	}
	return ""
}

func defaultConfigPath() string {
	if root := resolvePortableRoot(); root != "" {
		return filepath.Join(root, configFileName)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	return filepath.Join(base, configDirName, configFileName)
}

func defaultAppConfig() AppConfig {
	settings := defaultAppSettings()
	return AppConfig{
		SchemaVersion: configSchemaVersion,
		Settings:      &settings,
		Panels: []PanelConfig{
			{
				ID:   uuid.NewString(),
				Name: "OpenClash 演示",
				Tabs: []PanelTab{
					{ID: uuid.NewString(), Name: "Zashboard", URL: "http://immortalwrt.lan:9090/ui/zashboard/#/proxies"},
				},
				Enabled: true,
			},
		},
	}
}

// settingsKeyLightweightQuit 是「轻量模式用完即走」在 settings 节点里的键名。
// 单独提出来是因为加载期需要按「原始 JSON 里有没有这个键」补默认值（见 settingsHasKey）。
const settingsKeyLightweightQuit = "lightweightQuitOnLastPanel"

// settingsHasKey 报告配置文件里的 settings 节点是否**真的写了**这个键。
//
// json.Unmarshal 分不清「键缺失」和「显式 false」：老配置里没有的键读出来就是零值 false，
// 而默认值是按「键在不在」定的 —— 不回到原始 JSON 上确认，给配置新增一个默认开启的
// 布尔项，就等于替所有老用户把它悄悄关掉。
func settingsHasKey(data []byte, key string) bool {
	var probe struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	_, ok := probe.Settings[key]
	return ok
}

// load 读取配置；文件不存在时创建默认配置。
func (s *configStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.config = defaultAppConfig()
			return s.saveLocked()
		}
		return fmt.Errorf("read config %s: %w", s.path, err)
	}

	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		// 配置损坏时回退默认配置并重写文件，避免启动后无法使用。
		s.config = defaultAppConfig()
		return s.saveLocked()
	}
	if cfg.SchemaVersion != configSchemaVersion {
		return fmt.Errorf("unsupported config schema version %d", cfg.SchemaVersion)
	}

	// 向后兼容：老配置没有 settings 节点时补上默认值并写回，
	// 让「托盘图标 / 关闭行为」在管理界面里可见可改。
	// 关闭行为同时做取值归一化（旧版 quit → close），并把旧版「两类窗口共用一项」的
	// closeAction 落到面板窗口那一项；管理窗口的关闭行为已改为固定动作，
	// 旧版 managerCloseAction 无处可迁移，直接清掉。
	migrated := false
	if cfg.Settings == nil {
		settings := defaultAppSettings()
		cfg.Settings = &settings
		migrated = true
	} else {
		panel := normalizeCloseAction(cfg.Settings.PanelCloseAction)
		if legacy := cfg.Settings.DeprecatedCloseAction; legacy != "" {
			panel = normalizeCloseAction(legacy)
		}
		if panel != cfg.Settings.PanelCloseAction || cfg.Settings.DeprecatedCloseAction != "" ||
			cfg.Settings.DeprecatedManagerCloseAction != nil {
			cfg.Settings.PanelCloseAction = panel
			cfg.Settings.DeprecatedCloseAction = ""
			cfg.Settings.DeprecatedManagerCloseAction = nil
			migrated = true
		}
		// 老配置的 settings 节点里没有后续版本新增的键，反序列化读到的是零值 false。
		// 凡是「默认开启」的项都必须按「键在不在」补上默认值，否则升级用户会被悄悄改掉行为
		// （新增 lightweightQuitOnLastPanel 时实测踩到：老配置一律被当成关闭「用完即走」，
		// 轻量模式关掉最后一个面板后赖在托盘里不退出）。
		if !settingsHasKey(data, settingsKeyLightweightQuit) {
			cfg.Settings.LightweightQuitOnLastPanel = defaultAppSettings().LightweightQuitOnLastPanel
			migrated = true
		}
	}

	// 不变量：只要面板窗口被设置为「关闭时最小化到托盘」，托盘图标就必须开着。
	// 配置里可能出现这种矛盾组合（旧版本留下的、或手工改过配置），加载时直接纠正并写回 ——
	// 否则那些窗口一关闭就藏进托盘，而托盘里没有图标，它们再也回不来。
	if cfg.Settings != nil && cfg.Settings.minimizeToTrayConfigured() && !cfg.Settings.ShowTrayIcon {
		cfg.Settings.ShowTrayIcon = true
		migrated = true
	}

	// 向后兼容：将旧版单 URL 面板迁移为 Tabs 数组；顺带清掉已废弃的面板级
	// minimizeToTray 键（该功能已收归应用级设置，配置文件里不再保留它）。
	for i := range cfg.Panels {
		if len(cfg.Panels[i].Tabs) == 0 && cfg.Panels[i].DeprecatedURL != "" {
			cfg.Panels[i].Tabs = []PanelTab{
				{ID: newTabID(), Name: cfg.Panels[i].Name, URL: cfg.Panels[i].DeprecatedURL},
			}
			cfg.Panels[i].DeprecatedURL = ""
			migrated = true
		}
		if cfg.Panels[i].DeprecatedMinimizeToTray != nil {
			cfg.Panels[i].DeprecatedMinimizeToTray = nil
			migrated = true
		}
		// 旧版一个分组可以记录多个桌面快捷方式（每次点「桌面快捷方式」都新建一份）。
		// 收敛成第一个仍然存在的：配置里只留一个，桌面上的多余 .lnk 由「刷新图标」
		// 或下次「桌面快捷方式」时按需清理 —— 载入配置就动手删用户的文件太越界。
		if len(cfg.Panels[i].DeprecatedShortcuts) > 0 {
			if cfg.Panels[i].Shortcut == "" {
				for _, path := range cfg.Panels[i].DeprecatedShortcuts {
					if path == "" {
						continue
					}
					if info, err := os.Stat(path); err == nil && !info.IsDir() {
						cfg.Panels[i].Shortcut = path
						break
					}
				}
			}
			cfg.Panels[i].DeprecatedShortcuts = nil
			migrated = true
		}
	}
	if migrated {
		s.config = cfg
		return s.saveLocked()
	}

	s.config = cfg
	return nil
}

// saveLocked 以「临时文件 + 原子重命名」方式写回配置，避免断电损坏。
// 调用方必须已持有 s.mu。
func (s *configStore) saveLocked() error {
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := json.MarshalIndent(&s.config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// exportTo 把当前配置序列化写到 dest（与 saveLocked 同一格式，以内存态为准）。
// 供「导出配置」用：导出的就是 config.json 的完整内容 —— 面板列表 + 应用设置。
func (s *configStore) exportTo(dest string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(&s.config, "", "  ")
	if err != nil {
		return errCodeWrap(errConfigExportFailed, err)
	}
	if err := os.WriteFile(dest, data, 0o600); err != nil {
		return errCodeWrap(errConfigExportFailed, err)
	}
	return nil
}

// configBackupSuffix 是导入前备份当前配置的后缀（写在配置同目录，config.json.bak）。
const configBackupSuffix = ".bak"

// replaceWith 用 srcPath 的内容整体替换当前配置（供「导入配置」用）。
//
// 校验分两层，失败时**现有配置一个字节都不动**：
//  1. 预检：JSON 能解析、schema 版本一致 —— 必须在 load 之前做，因为 load 对坏 JSON
//     的语义是「回退默认配置并写回」，直接 load 用户的文件会把垃圾文件悄悄变成默认配置；
//  2. 走完整的 load()（在临时副本上）：旧格式迁移、托盘不变量纠正等都在副本上完成，
//     用户的源文件不被改写。
//
// 通过后：先把当前配置备份为 <配置路径>.bak（导入是整体覆盖，留一条退路），
// 再把迁移后的配置作为内存态与磁盘态一次性落地。
func (s *configStore) replaceWith(srcPath string) error {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return errCodeWrap(errConfigImportFailed, err)
	}
	var probe AppConfig
	if err := json.Unmarshal(data, &probe); err != nil {
		return errCode(errConfigImportInvalid)
	}
	if probe.SchemaVersion != configSchemaVersion {
		return errCodeDetail(errConfigImportInvalid, fmt.Sprintf("schema %d", probe.SchemaVersion))
	}

	// 临时副本走完整加载：迁移与不变量纠正发生在副本上。
	tmp := s.path + ".importing"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return errCodeWrap(errConfigImportFailed, err)
	}
	staged := &configStore{path: tmp}
	if err := staged.load(); err != nil {
		_ = os.Remove(tmp)
		return errCode(errConfigImportInvalid)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if cur, err := os.ReadFile(s.path); err == nil {
		_ = os.WriteFile(s.path+configBackupSuffix, cur, 0o600) // 备份失败不拦导入
	}
	s.config = staged.config
	if err := s.saveLocked(); err != nil {
		_ = os.Remove(tmp)
		return errCodeWrap(errConfigImportFailed, err)
	}
	_ = os.Remove(tmp)
	return nil
}

// settings 返回应用级设置；节点缺失或取值非法时回退默认值。
func (s *configStore) settings() AppSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settingsLocked()
}

// settingsLocked 同 settings，但要求调用方已持有 s.mu。
func (s *configStore) settingsLocked() AppSettings {
	if s.config.Settings == nil {
		return defaultAppSettings()
	}
	out := *s.config.Settings
	out.PanelCloseAction = normalizeCloseAction(out.PanelCloseAction)
	// 配色取值同样在读取路径归一化：前端永远拿到 auto / light / dark 之一，不必自己猜默认值。
	out.Theme = normalizeTheme(out.Theme)
	// 语言取值同理：前端与原生控件都拿到 auto / zh-CN / en-US 之一。
	out.Language = normalizeLanguage(out.Language)
	// 读取路径上同样守住「面板窗口会最小化到托盘 → 图标必须开着」这条不变量：
	// 判定托盘需求（App.trayNeeded）与进程寿命都读这里，绝不能让它们看到矛盾组合。
	if out.minimizeToTrayConfigured() {
		out.ShowTrayIcon = true
	}
	return out
}

// ensureSettingsLocked 返回可写的设置节点，缺失时就地补默认值。调用方必须已持有 s.mu。
func (s *configStore) ensureSettingsLocked() *AppSettings {
	if s.config.Settings == nil {
		settings := defaultAppSettings()
		s.config.Settings = &settings
	}
	return s.config.Settings
}

// setShowTrayIcon 切换「在系统托盘显示图标」。
//
// 关闭请求会被拒绝（返回 ErrTrayIconRequired）当且仅当面板窗口的关闭行为是
// 「最小化到托盘」—— 那种窗口关闭后藏进托盘，图标是找回它的唯一入口，
// 不能一边让窗口藏进去、一边把入口撤掉。开启请求永远允许。
func (s *configStore) setShowTrayIcon(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	settings := s.ensureSettingsLocked()
	if !on && settings.minimizeToTrayConfigured() {
		return ErrTrayIconRequired
	}
	settings.ShowTrayIcon = on
	return s.saveLocked()
}

// setCloseAction 设置面板窗口关闭时的行为（CloseActionAsk / CloseActionTray / CloseActionClose）。
func (s *configStore) setCloseAction(action string) error {
	if !isValidCloseAction(action) {
		return errCodeDetail(errCloseActionInvalid, action)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	settings := s.ensureSettingsLocked()
	settings.PanelCloseAction = action
	// 选中「最小化到托盘」时把托盘图标一并打开：这是自动且必须的（见 ErrTrayIconRequired）。
	// 反过来选别的不会自动关掉图标 —— 用户自己关过就是关过，不该被他处设置改回来。
	if settings.minimizeToTrayConfigured() {
		settings.ShowTrayIcon = true
	}
	return s.saveLocked()
}

// setLightweightQuitOnLastPanel 设置轻量模式下关掉最后一个面板是否直接退出程序。
func (s *configStore) setLightweightQuitOnLastPanel(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureSettingsLocked().LightweightQuitOnLastPanel = on
	return s.saveLocked()
}

// setTheme 设置管理界面的配色主题（ThemeAuto / ThemeLight / ThemeDark）。
func (s *configStore) setTheme(theme string) error {
	if !isValidTheme(theme) {
		return errCodeDetail(errThemeInvalid, theme)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureSettingsLocked().Theme = theme
	return s.saveLocked()
}

// setLanguage 设置界面语言（LanguageAuto / LanguageZhCN / LanguageEnUS）。
func (s *configStore) setLanguage(language string) error {
	if !isValidLanguage(language) {
		return errCodeDetail(errLanguageInvalid, language)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureSettingsLocked().Language = language
	return s.saveLocked()
}

func (s *configStore) list() []PanelConfig {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]PanelConfig, len(s.config.Panels))
	copy(out, s.config.Panels)
	// 返回前归一化会话处理方式与密码保存开关：老配置里缺这两个字段（空串），前端若拿到
	// 空串就得自己再猜一次默认值。归一化只在副本上进行，不动磁盘上的原始取值。
	for i := range out {
		out[i].SessionMode = normalizeSessionMode(out[i].SessionMode)
		out[i].PasswordAutosave = normalizePasswordAutosave(out[i].PasswordAutosave)
	}
	return out
}

func (s *configStore) get(id string) (PanelConfig, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			panel := s.config.Panels[i]
			panel.SessionMode = normalizeSessionMode(panel.SessionMode)
			panel.PasswordAutosave = normalizePasswordAutosave(panel.PasswordAutosave)
			return panel, true
		}
	}
	return PanelConfig{}, false
}

func (s *configStore) create(name, rawURL string, enabled bool) (PanelConfig, error) {
	if err := validatePanelInput(name, rawURL); err != nil {
		return PanelConfig{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	trimmedName := strings.TrimSpace(name)
	panel := PanelConfig{
		ID:   uuid.NewString(),
		Name: trimmedName,
		Tabs: []PanelTab{
			{ID: uuid.NewString(), Name: trimmedName, URL: strings.TrimSpace(rawURL)},
		},
		Enabled: enabled,
		// 新建面板默认保留会话：这是按钮/开关之外最不意外的一档（用户没明说要清空就别清）。
		SessionMode: SessionModePersist,
		// 新建面板默认保存登录密码（用户明确要求的默认值）：写显式值而不是留空，
		// 让配置文件自己说清楚这个分组是开着还是关着。
		PasswordAutosave: PasswordAutosaveOn,
		Window: PanelWindowState{
			Width:  defaultPanelWindowWidth,
			Height: defaultPanelWindowHeight,
		},
	}
	s.config.Panels = append(s.config.Panels, panel)
	if err := s.saveLocked(); err != nil {
		return PanelConfig{}, err
	}
	return panel, nil
}

func (s *configStore) update(id, name, rawURL string, enabled bool) (PanelConfig, error) {
	if err := validatePanelInput(name, rawURL); err != nil {
		return PanelConfig{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			trimmedName := strings.TrimSpace(name)
			s.config.Panels[i].Name = trimmedName
			s.config.Panels[i].Enabled = enabled
			// 同步更新第一个 Tab 的名称和地址。
			if len(s.config.Panels[i].Tabs) > 0 {
				s.config.Panels[i].Tabs[0].Name = trimmedName
				s.config.Panels[i].Tabs[0].URL = strings.TrimSpace(rawURL)
			} else {
				s.config.Panels[i].Tabs = []PanelTab{
					{ID: uuid.NewString(), Name: trimmedName, URL: strings.TrimSpace(rawURL)},
				}
			}
			if err := s.saveLocked(); err != nil {
				return PanelConfig{}, err
			}
			return s.config.Panels[i], nil
		}
	}
	return PanelConfig{}, errCode(errPanelNotFound)
}

func (s *configStore) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels = append(s.config.Panels[:i], s.config.Panels[i+1:]...)
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

func (s *configStore) updateWindowState(id string, state PanelWindowState) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels[i].Window = state
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

// setAlwaysOnTop 切换指定面板的置顶状态。
func (s *configStore) setAlwaysOnTop(id string, on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels[i].AlwaysOnTop = on
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

// setEnabled 只改面板的启用 / 停用状态，不碰名称、地址与标签。
//
// 存在的理由：外部打开请求（快捷方式 / 任务栏图标）遇到停用面板时，用户可以在询问框里
// 选择「启用并打开」——那条路径只该改这一个字段。走 update() 的话要把名称与地址重新拼一遍，
// 还会顺手覆盖第一个标签的名字，属于「为了省一个函数去改一堆不该动的数据」。
func (s *configStore) setEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID != id {
			continue
		}
		if s.config.Panels[i].Enabled == enabled {
			return nil // 已经是目标状态：不写盘，避免无谓的文件改动
		}
		s.config.Panels[i].Enabled = enabled
		return s.saveLocked()
	}
	return errCode(errPanelNotFound)
}

// setSessionMode 设置面板关闭后的会话处理方式（SessionModePersist / SessionModeFresh）。
// 只写配置、不触碰已打开的窗口：语义本身就是「下次关闭时按新设置处理」。
func (s *configStore) setSessionMode(id, mode string) error {
	if !isValidSessionMode(mode) {
		return errCodeDetail(errSessionModeInvalid, mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels[i].SessionMode = normalizeSessionMode(mode)
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

// setPasswordAutosave 设置该分组是否启用「保存登录密码」（PasswordAutosaveOn / PasswordAutosaveOff）。
//
// 只写配置，不由这里去动窗口：调用方（App.SetPanelPasswordAutosave）还要把新值应用到
// 正开着的窗口上，两件事分开，配置层不反向依赖窗口层。
func (s *configStore) setPasswordAutosave(id, mode string) error {
	if !isValidPasswordAutosave(mode) {
		return errCodeDetail(errPasswordInvalid, mode)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels[i].PasswordAutosave = normalizePasswordAutosave(mode)
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

// setShortcut 记录该面板的桌面快捷方式路径，供删除面板时联动清理。
//
// 覆盖式而不是追加式：一个分组只保留一个（见 PanelConfig.Shortcut 的说明）。
// path 传空串表示「这个分组现在没有桌面快捷方式了」，同样要落盘。
func (s *configStore) setShortcut(id, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID != id {
			continue
		}
		if s.config.Panels[i].Shortcut == path {
			return nil
		}
		s.config.Panels[i].Shortcut = path
		return s.saveLocked()
	}
	return errCode(errPanelNotFound)
}

// updateTabs 整体替换面板的标签列表（增、删、改名、**排序**都走这里）。
//
// DefaultTabIndex 是「上次激活的标签」的下标，替换标签后必须**按标签 ID 重新定位**：
// 下标只在旧顺序里有意义，重排之后同一个下标指向的是另一个标签，甚至可能越界
// （标签变少时）。不修正的话，用户把标签排好序、打开面板，激活的却是别人。
// 默认标签本身被删掉时才回落到第一个标签。
func (s *configStore) updateTabs(id string, tabs []PanelTab) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID != id {
			continue
		}
		panel := &s.config.Panels[i]

		defaultTabID := ""
		if panel.DefaultTabIndex >= 0 && panel.DefaultTabIndex < len(panel.Tabs) {
			defaultTabID = panel.Tabs[panel.DefaultTabIndex].ID
		}

		// 复制一份，避免与调用方的切片共享底层数组（AddTab 会复用旧切片的容量）。
		replaced := make([]PanelTab, len(tabs))
		copy(replaced, tabs)
		panel.Tabs = replaced

		panel.DefaultTabIndex = 0
		for j := range replaced {
			if defaultTabID != "" && replaced[j].ID == defaultTabID {
				panel.DefaultTabIndex = j
				break
			}
		}
		return s.saveLocked()
	}
	return errCode(errPanelNotFound)
}

func (s *configStore) setDefaultTabIndex(id string, index int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.config.Panels {
		if s.config.Panels[i].ID == id {
			s.config.Panels[i].DefaultTabIndex = index
			return s.saveLocked()
		}
	}
	return errCode(errPanelNotFound)
}

// newTabID 生成新的标签唯一标识。
func newTabID() string {
	return uuid.NewString()
}

// profileRootDir 返回 WebView2 用户数据目录的根（其下每个子目录是一个标签的 profile）。
// tabProfileDir 与孤儿扫描（App.ListOrphanProfiles）共用这一份定位逻辑。
func profileRootDir() (string, error) {
	if root := resolvePortableRoot(); root != "" {
		return filepath.Join(root, "WebViewProfiles"), nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("locate local application data directory: %w", err)
	}
	return filepath.Join(cache, configDirName, "WebViewProfiles"), nil
}

// tabProfileDir 返回标签专属的 WebView2 用户数据目录。
// 每个标签使用独立 profile，登录 Cookie 与会话互不干扰。
// 便携模式下位于 <exe目录>\data\WebViewProfiles\，随 data 目录整体迁移。
func tabProfileDir(tabID string) (string, error) {
	root, err := profileRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, tabID), nil
}

func validatePanelInput(name, rawURL string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errCode(errPanelNameRequired)
	}
	if len([]rune(name)) > 64 {
		return errCode(errPanelNameTooLong)
	}

	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return errCode(errPanelURLRequired)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return errCodeDetail(errPanelURLInvalid, err.Error())
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errCode(errPanelURLScheme)
	}
	if parsed.Host == "" {
		return errCode(errPanelURLHost)
	}
	return nil
}
