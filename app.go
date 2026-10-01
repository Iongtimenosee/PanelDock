package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// App 是 Wails 管理端的应用对象，向主窗口前端暴露面板管理方法。
// 面板窗口本身（panelWindow）不承载任何 Wails bridge，仅通过本结构体
// 的回调把窗口状态写回配置。
type App struct {
	mu sync.Mutex

	config      *configStore
	panels      map[string]*panelWindow
	autoOpenID  string // --open <面板ID>：启动后直接打开该面板
	lightweight bool   // --open 启动：隐藏主窗口，进程寿命跟随面板窗口
	ctx         context.Context
	quitting    bool // 已发起主动退出：此时 OnBeforeClose 必须放行
	// mainShown 报告「管理窗口当前是否是一个存在的窗口」（显示、最小化都算）。
	// 它参与「还有没有窗口」的判定，是决定进程要不要退出的输入之一，
	// 因此每次显示/隐藏管理窗口都必须同步。写入口只有三处：
	// startup（常规启动一上来就是显示的）、ShowMainWindow、两处 WindowHide。
	mainShown bool
	// resident 表示管理窗口被用户「最小化到托盘」藏了起来——即用户明确表达了
	// 「让程序留在托盘里」的意图。此时最后一个面板关闭也不再自动退出。
	resident bool
	// activePanelID 是托盘菜单「活动面板」的提示：最近打开/激活的面板。
	activePanelID string
}

func NewApp(autoOpenID string) *App {
	return &App{
		config:      newConfigStore(),
		panels:      make(map[string]*panelWindow),
		autoOpenID:  autoOpenID,
		lightweight: autoOpenID != "",
	}
}

func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	lightweight := a.lightweight
	// 常规启动时管理窗口一上来就是显示的（StartHidden 只在轻量模式下为 true）。
	// 不补这一笔，「还有没有窗口」的判定就会漏掉管理窗口本身 —— 那样在托盘图标
	// 关闭的情况下，关掉最后一个面板会把还开着的管理窗口一起带走。
	a.mainShown = !lightweight
	a.mu.Unlock()

	_ = a.config.load()

	// 窗口底色与主题对齐（main.go 在创建窗口时已按配置选过一次；这里再同步是为了
	// 覆盖「main 里的 load 失败、这里才成功」的边角，幂等无害）。
	a.syncWindowTheme()

	// 本进程是唯一实例：开启 IPC 监听，并用同一个常驻窗口承载托盘图标。
	// 托盘图标挂在这里（而不是面板窗口）——一个面板都没开时托盘里也必须有入口。
	ipcHWND, err := startIPCListener(a.handleIPCCommand)
	if err != nil {
		println("ipc listener:", err.Error())
	} else {
		trayAttach(a, ipcHWND)
		a.syncTray()
	}

	// 轻量模式下主窗口以 StartHidden 启动，这里只负责补开面板。
	if lightweight {
		id := a.autoOpenID
		go func() {
			// 等 Wails 主窗口先就绪，再开面板，避免与启动流程竞争。
			time.Sleep(500 * time.Millisecond)
			if err := a.openPanelFromExternalRequest(id); err != nil {
				println("auto open panel:", err.Error())
				if errors.Is(err, ErrPanelKeptDisabled) {
					// 用户刚刚在询问框里选择了「保持停用」：静默收场，
					// 不要再拿管理窗口盖上去 —— 他刚回答完，程序却弹个窗口，等于没听他说话。
					return
				}
				// 面板打不开（不存在 / 没有标签）时显示管理界面，避免留下看不见的进程。
				a.ShowMainWindow()
			}
		}()
	}
}

// ShowMainWindow 显示并激活 Wails 管理窗口（轻量模式下由托盘菜单/转发命令调用）。
func (a *App) ShowMainWindow() {
	a.mu.Lock()
	a.mainShown = true
	// 用户回到管理界面：不再是「藏在托盘里」的状态。
	a.resident = false
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	wailsRuntime.WindowShow(ctx)
	wailsRuntime.WindowUnminimise(ctx)
	wailsRuntime.WindowSetAlwaysOnTop(ctx, true)
	wailsRuntime.WindowSetAlwaysOnTop(ctx, false)
	a.syncTray()
}

// Quit 退出整个应用（同时关闭全部面板窗口）。
func (a *App) Quit() {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	go a.requestQuit(ctx)
}

// requestQuit 先置 quitting 标记再调用 Wails Quit。
// 必须先标记：Wails 的 Frontend.Quit 会先询问 OnBeforeClose，轻量模式下该钩子
// 默认阻止关闭，不标记就会把退出请求整个吞掉。
func (a *App) requestQuit(ctx context.Context) {
	a.mu.Lock()
	a.quitting = true
	a.mu.Unlock()
	wailsRuntime.Quit(ctx)
}

// onBeforeClose 拦截管理窗口的关闭，执行固定动作（没有设置项、也不询问）。
// 返回 true 表示阻止本次关闭（Wails 会保留窗口）。
//
// 为什么这里必须拦：管理窗口是 **Wails 的**窗口，它一被销毁，Wails 就认为「没有窗口了」
// 并结束进程；而分组标签窗口是自建的 Win32 窗口，不在 Wails 管辖范围内，救不了场。
// 所以「只关自己、别把分组标签带走」只能在这里守住 —— 还有面板窗口时把管理窗口**藏起来**
// （对用户而言等同于「只关掉自己」），别的一概不动。
func (a *App) onBeforeClose(ctx context.Context) bool {
	if a.isQuitting() {
		return false // 程序主动退出：放行
	}
	return a.closeManagerWindow()
}

// closeManagerWindow 执行「关闭管理面板」的固定动作，返回 true 表示拦下本次关闭。
//
// 判断与面板窗口的收工判定同源（见 quitWhenNoWindows）：
//   - 还有分组标签窗口，或托盘图标还开着 → **只关自己**：把管理窗口藏起来，进程继续运行
//     （藏起来而不销毁，是因为销毁就等于「没有窗口了」，Wails 会立刻结束进程并带走面板）；
//   - 两者都没有 → 放行。管理窗口一关就是「最后一个窗口关闭」，进程随之结束。
//
// 它不碰托盘设置：「在系统托盘显示图标」由用户自己的勾选决定，关个管理窗口不该动它。
func (a *App) closeManagerWindow() bool {
	if !a.panelsAlive() && !a.trayIconShown() {
		return false
	}
	if a.trayIconShown() {
		// 托盘图标开着：管理窗口藏进去后靠它找回，标记「藏在托盘里」让图标留着。
		a.mu.Lock()
		a.resident = true
		a.mu.Unlock()
	}
	a.hideManagerWindow()
	a.syncTray()
	return true
}

// trayIconShown 报告系统托盘里此刻是否有本程序的图标（见 trayNeeded）。
func (a *App) trayIconShown() bool {
	return a.trayNeeded()
}

// hideManagerWindow 隐藏管理窗口，并把它从「还存在的窗口」里划掉。
//
// 这里用的是 App 自己保存的 ctx（startup 钩子给的那个），而不是 OnBeforeClose 传进来的：
// Wails 的 runtime 只接受生命周期钩子给的 ctx（其它 context 会直接 log.Fatal），
// 而单测里没有真实 Wails 环境、ctx 为空 —— 那就只更新状态、不碰窗口。
func (a *App) hideManagerWindow() {
	a.mu.Lock()
	ctx := a.ctx
	a.mainShown = false
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	wailsRuntime.WindowHide(ctx)
}

// panelsAlive 报告是否还有分组标签窗口没关掉。
func (a *App) panelsAlive() bool {
	return a.panelCount() > 0
}

// panelCount 返回当前打开的分组标签窗口数量。
func (a *App) panelCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.panels)
}

// notifySettingsChanged 通知管理界面重新读取设置。
// 后端自行改配置时必须广播，否则设置区显示的勾选状态会与配置文件不一致
// （例：把某处关闭行为改成「最小化到托盘」会连带打开托盘图标勾选，但界面还显示未勾选）。
func (a *App) notifySettingsChanged() {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(ctx, settingsChangedEvent)
}

// settingsChangedEvent 是「后端改了设置，界面请刷新」的事件名，与前端 main.js 约定一致。
const settingsChangedEvent = "paneldock:settings-changed"

// panelsChangedEvent 是「面板列表变了，界面请刷新」的事件名，与前端 main.js 约定一致。
const panelsChangedEvent = "paneldock:panels-changed"

// notifyPanelsChanged 通知管理界面重新读取面板列表。
//
// 外部打开请求（快捷方式）把停用面板改成启用就是这种情形：改动是后端自己做的，
// 界面不会知道 —— 不广播的话卡片上会一直挂着过期的「停用」标记（前端没有轮询）。
func (a *App) notifyPanelsChanged() {
	a.mu.Lock()
	ctx := a.ctx
	a.mu.Unlock()
	if ctx == nil {
		return
	}
	wailsRuntime.EventsEmit(ctx, panelsChangedEvent)
}

// closeDecision 是一次用户关闭请求的最终决定。
type closeDecision struct {
	// Action 为 CloseActionTray / CloseActionClose；空串表示取消（什么都不做）。
	// CloseActionClose 表示「只关闭这个面板窗口」，其他窗口与托盘图标都不受影响。
	Action string
	// Remember 为 true 表示把该行为写入设置，以后不再询问。
	Remember bool
}

// decideClose 决定关闭面板窗口时该怎么做。
// 已记住的行为直接返回；设置为「每次询问」时弹出原生询问框（会自动记忆本次的复选框状态）。
// note 是追加在正文末行的可选补充说明。
func (a *App) decideClose(owner uintptr, note string) closeDecision {
	settings := a.config.settings()
	if action := settings.closeAction(); action != CloseActionAsk {
		return closeDecision{Action: action}
	}

	result, ok := promptCloseDialog(owner, note, a.nativeText().ClosePrompt)
	if !ok {
		return closeDecision{}
	}
	return closeDecision{Action: result.Action, Remember: result.Remember}
}

// panelCloseEndsProcess 预测「关掉这个面板窗口」之后进程是否也会退出，
// 用于在面板询问框里如实告知后果，而不是让用户按了「直接关闭」才发现程序没了。
// 与 shouldQuitAfterPanelClosed 同源，区别只在于这里假设的是「本次关闭之后」的状态。
func (a *App) panelCloseEndsProcess() bool {
	if a.panelCount() > 1 {
		return false // 还有别的分组标签窗口：关掉这个不影响进程
	}
	return a.quitWhenNoWindows(0)
}

// commitCloseChoice 处理「记住我的选择」：勾选时把本次行为写入面板窗口的关闭行为设置。
func (a *App) commitCloseChoice(decision closeDecision) {
	if decision.Remember && decision.Action != "" {
		_ = a.config.setCloseAction(decision.Action)
	}
}

// isQuitting 报告程序是否正在主动退出（此时所有关闭请求都直接放行，不再询问）。
func (a *App) isQuitting() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.quitting
}

func (a *App) shutdown(_ context.Context) {
	a.mu.Lock()
	panels := make([]*panelWindow, 0, len(a.panels))
	for _, p := range a.panels {
		panels = append(panels, p)
	}
	a.mu.Unlock()

	for _, p := range panels {
		p.close()
	}
	for _, p := range panels {
		p.wait()
	}
}

// ListPanels 返回全部面板配置（按配置顺序）。
func (a *App) ListPanels() []PanelConfig {
	return a.config.list()
}

// CreatePanel 新建一个面板配置。
func (a *App) CreatePanel(name, url string, enabled bool) (PanelConfig, error) {
	return a.config.create(name, url, enabled)
}

// UpdatePanel 更新面板配置（名称 / 地址 / 启用状态）。
func (a *App) UpdatePanel(id, name, url string, enabled bool) (PanelConfig, error) {
	return a.config.update(id, name, url, enabled)
}

// DeletePanel 删除面板配置、关闭其已打开的面板窗口，并清空它的全部浏览器数据。
//
// removeShortcuts 是用户在确认对话框里对「同时删除快捷方式」可选项的勾选结果：
//   - true：联动清理该面板的桌面快捷方式（配置里记录的 + 扫描桌面兜底匹配到的）；
//   - false：桌面快捷方式原样保留。面板已不存在，留下的 .lnk 双击后会因面板 ID 查不到
//     而落到「显示管理界面」（main.go → App.ShowMainWindow），不会指向别的面板，
//     但要由用户自己决定是否留着 —— 所以清理必须是**显式勾选**的结果，不是默认动作。
//
// **WebView2 profile 无条件清掉**（2026-09-30 用户明确要求）：Cookie、登录态、缓存、
// 本地存储，以及 WebView2 保存的登录密码。理由是这个分组从此不存在 —— 那些目录只有
// 它自己的标签 ID 能访问，面板一删就再也读不回来，留着只会白占空间、并且把密码继续留在
// 盘上。想「只清数据、保留面板」是另一个动作（ResetPanelData），卡片上就有。
//
// 顺序是刻意的：**关窗口 → 清数据 → 删快捷方式 → 删配置**。
//   - 清数据必须在关窗口之后：目录被浏览器进程占着，不关就删不干净（删一半 = 登录态还在）；
//   - 清数据失败就**整件事中止、返回错误、面板保留** —— 不允许出现「面板没了但数据还在
//     盘上」这种半完成状态，那正是用户以为已经清干净、实际密码还在的情形；
//   - 快捷方式清单要在配置删除之前读（listPanelShortcuts 依赖配置里记录的 .lnk 路径），
//     但排在清数据之后：数据都没清干净，不该先去动用户的桌面。
func (a *App) DeletePanel(id string, removeShortcuts bool) error {
	cfg, ok := a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}

	a.mu.Lock()
	p := a.panels[id]
	if p != nil {
		delete(a.panels, id)
	}
	if a.activePanelID == id {
		a.activePanelID = ""
	}
	a.mu.Unlock()

	if p != nil {
		p.close()
		p.wait()
	}

	// 面板没打开时上面的 close/wait 不会发生，dispose 的清空也就没跑过，这里补一次；
	// 面板打开过的话 dispose 已经清干净，这次是一记空操作。
	// 用 clearUntilStable 而不是 clearOnce：刚退出的浏览器进程会在几百毫秒里把目录重建回来
	// （实测，见 session.go），只删一次等于没清干净；此刻窗口已销毁，这段等待用户看不到。
	if err := clearPanelProfiles(cfg.Tabs, clearUntilStable); err != nil {
		return errCodeWrap(errDeleteClearFailed, err)
	}

	if removeShortcuts {
		// 先取配置里记录的路径，再扫描桌面兜底（覆盖被改名/移动过、
		// 以及本功能上线前手工创建的 .lnk）。
		deleteShortcutFiles(listPanelShortcuts(cfg.ID, cfg.Shortcut))
	}

	// 图标缓存跟着面板一起走：还指着它的快捷方式先改回 exe 自带图标，再删 .ico。
	// 放在删快捷方式之后 —— 已经被删掉的那些就不在扫描结果里了，不会白改一遍。
	a.dropPanelIconCache(cfg)

	return a.config.delete(id)
}

// ListPanelShortcuts 返回该面板当前在桌面/配置中登记的快捷方式路径。
// 供前端在删除面板前明确告知「哪些 .lnk 可被一并清理」，并决定「同时删除快捷方式」
// 可选项是否可用（一个都没有时禁用该选项，避免给出无效承诺）。
func (a *App) ListPanelShortcuts(id string) ([]string, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return nil, errCode(errPanelNotFound)
	}
	return listPanelShortcuts(cfg.ID, cfg.Shortcut), nil
}

// PanelShortcutPreview 是「桌面快捷方式」按钮按下后的预检结果。
//
// 只回答现状，不写任何文件：桌面已经摆着这个分组的一份时，前端要先问一句
// 「是否覆盖它」，用户点头才动手（2026-09-30 用户要求）。
type PanelShortcutPreview struct {
	// Exists 为真表示桌面已经有该分组的快捷方式 —— 继续做就是覆盖它。
	Exists bool `json:"exists"`
	// Shortcuts 是桌面上该分组的全部快捷方式路径（正常至多一份，多出来的是历史遗留）。
	Shortcuts []string `json:"shortcuts"`
	// Extra 是历史遗留的多余项，继续操作时会被收敛掉。
	Extra []string `json:"extra"`
	// IconPath 是已经缓存好的站点图标路径（没缓存过则为空）。非空时覆盖后的快捷方式会用它当图标。
	IconPath string `json:"iconPath"`
}

// InspectPanelShortcut 预检桌面快捷方式的现状，供前端在覆盖前问一句。
func (a *App) InspectPanelShortcut(id string) (PanelShortcutPreview, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return PanelShortcutPreview{}, errCode(errPanelNotFound)
	}
	found := listPanelShortcuts(cfg.ID, cfg.Shortcut)
	preview := PanelShortcutPreview{
		Shortcuts: found,
		IconPath:  a.cachedPanelIconPath(cfg.ID),
	}
	if len(found) == 0 {
		return preview, nil
	}
	preview.Exists = true

	keep := found[0]
	for _, path := range found {
		if strings.EqualFold(path, cfg.Shortcut) {
			keep = path
			break
		}
	}
	for _, path := range found {
		if !strings.EqualFold(path, keep) {
			preview.Extra = append(preview.Extra, path)
		}
	}
	return preview, nil
}

// PanelShortcutResult 是一次「桌面快捷方式」操作的结果。
type PanelShortcutResult struct {
	// Path 是本次操作的 .lnk 完整路径。
	Path string `json:"path"`
	// Created 为真表示这次新建了文件；为假表示覆盖了桌面原有的那一份。
	Created bool `json:"created"`
	// Removed 是顺带收敛掉的、这个分组此前遗留在桌面上的多余快捷方式。
	Removed []string `json:"removed"`
}

// CreatePanelShortcut 确保桌面有该面板的快捷方式（`--open <面板ID>` 直达启动）。
//
// 桌面已经有这个分组的一份时**覆盖它**，不再新增：一个分组桌面只能有一个
// （见 createDesktopShortcut）。把用户自己起的文件名保留下来 —— 他可能已经把那份
// 快捷方式改了名摆在顺手的位置。
//
// 面板改名后快捷方式仍有效（ID 不可变）。路径会记入面板配置，删除面板时据此清理。
func (a *App) CreatePanelShortcut(id string) (PanelShortcutResult, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return PanelShortcutResult{}, errCode(errPanelNotFound)
	}
	exePath, err := os.Executable()
	if err != nil {
		return PanelShortcutResult{}, errCodeWrap(errExecPathFailed, err)
	}
	path, created, err := createDesktopShortcut(exePath, cfg.ID, cfg.Name, a.cachedPanelIconPath(cfg.ID), cfg.Shortcut)
	if err != nil {
		return PanelShortcutResult{}, err
	}
	// 顺手收敛：桌面万一还留着这个分组从前的多份快捷方式，只留刚写的那一份。
	removed := a.collapseExtraShortcuts(cfg, path)
	_ = a.config.setShortcut(cfg.ID, path)
	return PanelShortcutResult{Path: path, Created: created, Removed: removed}, nil
}

// collapseExtraShortcuts 收掉桌面上该分组多余的快捷方式（一个分组只留一份）。
func (a *App) collapseExtraShortcuts(cfg PanelConfig, keep string) []string {
	desktop, err := shortcutDesktopDirectory()
	if err != nil {
		return nil
	}
	return collapsePanelDesktopShortcuts(desktop, cfg.ID, keep)
}

// PinTaskbarResult 是一次「固定到任务栏」的结果，供前端决定怎么说话。
//
// Windows 不允许程序自己把图标固定到任务栏（见 taskbar_windows.go 的文件头），
// 所以这里没有「已固定成功」这种字段：能确认的只有「本来就在」和「快捷方式已经备好了，
// 剩下两下得你来点」。
type PinTaskbarResult struct {
	// Shortcut 是本次操作对应的 .lnk 完整路径 —— 用户最后要固定的就是它。
	Shortcut string `json:"shortcut"`
	// AlreadyPinned 为真表示回读任务栏固定目录发现该面板已经在任务栏上，无需再做任何事。
	AlreadyPinned bool `json:"alreadyPinned"`
}

// PinPanelToTaskbar 为「固定到任务栏」做好准备。
//
// 实现分两步（为什么不能一步到位，见 taskbar_windows.go）：
//  1. 没有现成的 .lnk 就先建一个 —— 任务栏固定的本质就是这个 .lnk 被复制进固定目录；
//  2. 回读固定目录，已经在任务栏上了就直接返回 AlreadyPinned。
//
// 刻意**不尝试**调用 shell 的 `taskbarpin` 动词：实测它在 Windows 11 上不报错，
// 而是退化成 `open` 把面板真的打开一次，副作用大于收益。
//
// 也刻意**不在这里打开资源管理器**（2026-09-30 调整）：那是用户还来不及读说明就被程序
// 抢走的动作，属于「擅作主张」。选中快捷方式改由用户读完引导后自己点「选中快捷方式」触发
// （App.RevealPanelShortcut），什么时候切过去由用户决定。
func (a *App) PinPanelToTaskbar(id string) (PinTaskbarResult, error) {
	cfg, ok := a.config.get(id)
	if !ok {
		return PinTaskbarResult{}, errCode(errPanelNotFound)
	}

	// 复用已有快捷方式：为了固定再往桌面上撒一个 .lnk 对用户没有意义。
	// 确实没有才新建（走 CreatePanelShortcut，路径会记进配置，删面板时能一并清理）。
	shortcuts := listPanelShortcuts(cfg.ID, cfg.Shortcut)
	var lnk string
	if len(shortcuts) > 0 {
		lnk = shortcuts[0]
	} else {
		created, err := a.CreatePanelShortcut(id)
		if err != nil {
			return PinTaskbarResult{}, err
		}
		lnk = created.Path
	}

	result := PinTaskbarResult{Shortcut: lnk, AlreadyPinned: isPanelPinnedToTaskbar(cfg.ID)}
	return result, nil
}

// RevealPanelShortcut 在资源管理器中选中该面板的快捷方式。
//
// 供前端「固定到任务栏」引导对话框里的「选中快捷方式」按钮使用 —— 由用户读完说明后
// 主动触发，而不是在弹框时替用户把资源管理器拉起来（用户可能还没看完就被切走）。
func (a *App) RevealPanelShortcut(id string) error {
	cfg, ok := a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}
	shortcuts := listPanelShortcuts(cfg.ID, cfg.Shortcut)
	if len(shortcuts) == 0 {
		return errCode(errShortcutMissing)
	}
	return revealShortcutInExplorer(shortcuts[0])
}

// markActivePanel 记录最近打开/激活的面板，供托盘菜单定位「活动面板」。
func (a *App) markActivePanel(id string) {
	a.mu.Lock()
	a.activePanelID = id
	a.mu.Unlock()
}

// OpenPanel 打开指定面板；若窗口已存在则激活之。
//
// 它只做「按现有配置打开」，**不处理停用面板的询问** —— 那件事只属于外部打开请求
// （见 openPanelFromExternalRequest）。管理界面里的「打开」按钮在停用的卡片上本来就是灰的，
// 而 Wails 绑定方法绝不能弹原生模态框：那会在 WebView2 的消息处理线程上自建 GetMessage 循环。
func (a *App) OpenPanel(id string) error {
	cfg, ok := a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}
	if !cfg.Enabled {
		return errCode(errPanelDisabled)
	}
	if len(cfg.Tabs) == 0 {
		return errCode(errPanelNoTabs)
	}

	a.mu.Lock()
	if p, ok := a.panels[id]; ok {
		a.mu.Unlock()
		p.activate()
		a.markActivePanel(id)
		return nil
	}
	a.mu.Unlock()

	panel, err := newPanelWindow(a, cfg)
	if err != nil {
		return err
	}

	// 插入运行表前再次检查，避免并发调用创建出两个同 ID 窗口。
	a.mu.Lock()
	if existing, ok := a.panels[id]; ok {
		a.mu.Unlock()
		existing.activate()
		a.markActivePanel(id)
		return nil
	}
	a.panels[id] = panel
	a.mu.Unlock()

	a.markActivePanel(id)

	if err := panel.open(); err != nil {
		a.mu.Lock()
		if a.panels[id] == panel {
			delete(a.panels, id)
		}
		a.mu.Unlock()
		return err
	}
	return nil
}

// ErrPanelKeptDisabled 表示用户在「面板已停用」询问框里选择了保持停用。
//
// 调用方据此**静默收场**：不弹管理界面、也不报错 —— 用户刚刚明确回答过那个问题，
// 这时再补一个窗口弹出来只会显得程序没听见他说话。
var ErrPanelKeptDisabled = errors.New("面板已停用，用户选择保持停用")

// confirmEnableDisabledPanel 是「是否启用这个停用的面板」的注入缝。
// 单测把它换成一个假实现，就不必真的弹出原生询问框（也避免测试卡在等人的点击上）。
// 文案由调用方（App）按当前语言解析后传入，询问函数本身不再关心语言。
var confirmEnableDisabledPanel = promptEnableDisabledPanel

// enablePromptMu 把「询问是否启用」串行化：连点两下快捷方式不会叠出两个同样的询问框。
// 后到的请求会等前一个做出选择，然后**重新读配置** —— 那时面板往往已经启用，于是直接打开。
// 询问框是模态的，锁的持有时间就是用户思考的时间，不影响其他命令（IPC 处理本就在独立 goroutine 里）。
var enablePromptMu sync.Mutex

// ensurePanelEnabledForExternalOpen 处理外部打开请求里「面板处于停用状态」这一种情况：
// 询问用户是否启用；用户同意则落盘启用并返回 nil（调用方继续打开），
// 用户选择保持停用则返回 ErrPanelKeptDisabled。
//
// 为什么只有外部请求走这里（快捷方式 / 任务栏固定图标 / `--open` 转发）：
//   - 管理界面里停用的卡片上，「打开」按钮本来就是灰的，不存在「点了没反应」；
//   - Wails 绑定方法绝不能弹原生模态框（那会在 WebView2 的消息处理线程上自建 GetMessage 循环）。
func (a *App) ensurePanelEnabledForExternalOpen(id string) error {
	cfg, ok := a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}
	if cfg.Enabled {
		return nil
	}

	enablePromptMu.Lock()
	defer enablePromptMu.Unlock()

	// 等锁期间可能已经被另一个请求启用了（连点两下快捷方式）：重新读一次，
	// 别对着已经启用的面板再问一遍「是否启用」。
	cfg, ok = a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}
	if cfg.Enabled {
		return nil
	}

	if !confirmEnableDisabledPanel(cfg.Name, a.nativeText().EnablePrompt) {
		return ErrPanelKeptDisabled
	}

	if err := a.config.setEnabled(id, true); err != nil {
		return err
	}
	// 面板的启用状态是后端自己改的：管理界面若正开着，让它重新读一遍，
	// 否则卡片上会一直挂着「停用」标记（前端没有轮询）。
	a.notifyPanelsChanged()
	return nil
}

// openPanelFromExternalRequest 处理来自程序外部的打开请求：桌面快捷方式、任务栏固定图标、
// 以及 `--open <面板ID>` 被转发过来的命令（见 main.go / ipc_windows.go）。
//
// 与 OpenPanel 的区别只有一处：停用的面板不会被直接拒绝，而是先询问用户是否启用
// （见 ensurePanelEnabledForExternalOpen）——「双击快捷方式毫无反应」是最难自查的一种失败：
// 用户分不清是自己停用了它、程序没起来、还是面板地址坏了。
func (a *App) openPanelFromExternalRequest(id string) error {
	if err := a.ensurePanelEnabledForExternalOpen(id); err != nil {
		return err
	}
	return a.OpenPanel(id)
}

// restartPanel 关闭面板窗口并以最新配置重新打开（异步重开，不阻塞调用方）。
// 面板未打开时是空操作。
func (a *App) restartPanel(panelID string) {
	a.mu.Lock()
	p := a.panels[panelID]
	a.mu.Unlock()
	if p == nil {
		return
	}

	p.close()
	p.wait()

	newCfg, ok := a.config.get(panelID)
	if !ok {
		return
	}
	go func() {
		pw, err := newPanelWindow(a, newCfg)
		if err != nil {
			// 重开失败（如「关闭后清空」时残留的浏览器进程占着 profile 目录）不能一声不吭：
			// 用户刚保存完面板就看到窗口消失，日志是唯一的线索。
			println("restart panel:", err.Error())
			return
		}
		a.mu.Lock()
		a.panels[panelID] = pw
		a.mu.Unlock()
		_ = pw.open()
	}()
}

// ClosePanel 关闭指定面板窗口（保留配置与 profile）。
func (a *App) ClosePanel(id string) error {
	a.mu.Lock()
	p := a.panels[id]
	a.mu.Unlock()
	if p == nil {
		return nil
	}
	p.close()
	return nil
}

// SetPanelAlwaysOnTop 切换指定面板的置顶状态（持久化 + 应用到已打开窗口）。
func (a *App) SetPanelAlwaysOnTop(id string, on bool) error {
	if err := a.config.setAlwaysOnTop(id, on); err != nil {
		return err
	}
	a.mu.Lock()
	p := a.panels[id]
	a.mu.Unlock()
	if p != nil {
		p.setAlwaysOnTop(on)
	}
	return nil
}

// SetPanelSessionMode 设置面板关闭后的会话状态处理方式：
//   - SessionModePersist：保留（Cookie、登录态、缓存都留着，下次打开接着上次）；
//   - SessionModeFresh：关闭后清空，下次打开等同全新环境（打开前还会再清一次兜底）。
//
// 只写配置、不重开窗口：语义本身就是「下次关闭时按新设置处理」，
// 当前已打开的窗口不受影响，也没必要为了改一个开关把用户的页面重载一遍。
func (a *App) SetPanelSessionMode(id, mode string) error {
	return a.config.setSessionMode(id, mode)
}

// SetPanelPasswordAutosave 设置该分组是否保存登录密码（PasswordAutosaveOn / PasswordAutosaveOff）。
//
// 与会话处理方式不同，这一项**当场生效**：它就是 WebView2 上的一个设置属性，改完立刻重设
// 到已打开的标签上。用户刚在卡片上取消勾选、却要等下次打开才起作用的话，只会以为没生效。
//
// 注意它只管「保存」：关掉之后此前已存的密码仍会被回填（WebView2 的既定语义，见
// PasswordAutosaveOff 的注释），清掉既有密码靠的是面板的「关闭即清空」。
func (a *App) SetPanelPasswordAutosave(id, mode string) error {
	if err := a.config.setPasswordAutosave(id, mode); err != nil {
		return err
	}

	a.mu.Lock()
	p := a.panels[id]
	a.mu.Unlock()
	if p != nil {
		p.setPasswordAutosave(normalizePasswordAutosave(mode) == PasswordAutosaveOn)
	}
	return nil
}

// resetPanelCloser 是「重置数据前先关掉正在运行的面板窗口」这一步的注入缝。
//
// 真实实现必须**等窗口完全销毁**（`wait`）：profile 目录被它的浏览器进程占着，
// 不等就等于删一个正在被人写字的文件夹 —— 恰好是「清空」这条承诺最不能出问题的地方。
// 单测把它换成假实现，就不必在测试进程里真的去开一个窗口（也就不会卡在窗口线程上）。
var resetPanelCloser = func(p *panelWindow) {
	p.close()
	p.wait()
}

// ResetPanelData 清空该面板所有标签的 WebView2 数据 —— profile 目录整棵删掉。
//
// 与「关闭后清空」的区别是**立刻执行**：那个是关闭时的自动行为，这个由用户在卡片上手动触发，
// 用来把已经攒下来的登录态、缓存、本地存储以及 WebView2 保存的登录密码一次抹干净。
// 面板配置（地址、标签、快捷方式、窗口状态、会话设置）一个字节都不动 —— 重置的是数据，不是面板。
//
// 面板正在运行时先关掉它的窗口再清：
//   - 目录被浏览器进程占着，不关就删不干净（删一半 = 数据还在，比不删更糟）；
//   - 窗口内存里还留着上一次的会话，不关掉的话"清空"只是口头承诺。
//
// 关掉之后**不自动重开**：重置的语义是「抹掉数据」，重新访问由用户自己决定（卡片上就有「打开」）。
// 这也顺带避开一个坑 —— 立刻重开会让新会话与清空动作抢同一个目录。
func (a *App) ResetPanelData(id string) error {
	cfg, ok := a.config.get(id)
	if !ok {
		return errCode(errPanelNotFound)
	}
	if len(cfg.Tabs) == 0 {
		return nil
	}

	a.mu.Lock()
	p := a.panels[id]
	a.mu.Unlock()
	if p != nil {
		resetPanelCloser(p)
	}

	// clearUntilStable：刚退出的浏览器进程会在几百毫秒里把目录重建回来（实测，见 session.go），
	// 单次删除等于没清。此刻窗口已销毁，重试预算里的开销用户看不到。
	if err := clearPanelProfiles(cfg.Tabs, clearUntilStable); err != nil {
		return errCodeWrap(errResetClearFailed, err)
	}
	return nil
}

// ListOrphanProfiles 扫描 WebViewProfiles 根目录，返回不属于任何标签的子目录名。
//
// 孤儿的来源：早期版本删标签/删分组只改配置不删目录（2026-09-30 已改为连带清理），
// 那之前留下的目录从此没有任何配置项指向它们 —— 占着磁盘，里面可能还存着密码，
// 而且用户没有入口发现它们。目录名就是 tabID，对用户没意义，只报个数和用途说明。
func (a *App) ListOrphanProfiles() ([]string, error) {
	root, err := profileRootDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 从未开过任何标签，目录还不存在
		}
		return nil, fmt.Errorf("扫描 profile 目录: %w", err)
	}

	used := make(map[string]bool)
	for _, panel := range a.config.list() {
		for _, t := range panel.Tabs {
			used[t.ID] = true
		}
	}

	var orphans []string
	for _, e := range entries {
		if e.IsDir() && !used[e.Name()] {
			orphans = append(orphans, e.Name())
		}
	}
	return orphans, nil
}

// CleanOrphanProfiles 删除全部孤儿 profile 目录，返回删除的个数。
//
// 孤儿目录不被任何窗口占用（正开着的标签都在配置里、都在 used 集合中），
// 所以 clearOnce 足够，不需要 clearUntilStable 的重试预算。
// 删前重新扫一遍而不是让前端把名单传回来：名单经手前端就有被篡改的可能，
// 服务端自己算的才是「真的没有配置项指向它」。
func (a *App) CleanOrphanProfiles() (int, error) {
	orphans, err := a.ListOrphanProfiles()
	if err != nil {
		return 0, err
	}
	for _, id := range orphans {
		if err := clearTabProfile(id, clearOnce); err != nil {
			return 0, errCodeWrap(errCleanFailed, err)
		}
	}
	return len(orphans), nil
}

// GetSettings 返回应用级设置（托盘图标开关、关闭行为）。
func (a *App) GetSettings() AppSettings {
	return a.config.settings()
}

// SetShowTrayIcon 切换「在系统托盘显示图标」。
// 托盘图标由常驻 IPC 窗口承载，因此这里只需重新计算一次「是否需要图标」。
//
// 取消勾选可能被拒绝（config.ErrTrayIconRequired）：有窗口的关闭行为是「最小化到托盘」时
// 图标必须留着，否则那些窗口一关闭就藏进托盘、再也找不回来。拒绝原因用错误码
// （tray.iconRequired）回给前端翻译 —— 中文裸串在英文界面里没法看；哨兵错误包在
// appError 的 Unwrap 链里，errors.Is(err, ErrTrayIconRequired) 依然成立。
func (a *App) SetShowTrayIcon(on bool) error {
	if err := a.config.setShowTrayIcon(on); err != nil {
		if errors.Is(err, ErrTrayIconRequired) {
			// 哨兵错误包进 Unwrap 链：单测与调用方仍可用 errors.Is 判定。
			return errCodeWrap(errTrayIconRequired, ErrTrayIconRequired)
		}
		return err
	}
	a.syncTray()
	return nil
}

// SetPanelCloseAction 设置关闭**面板窗口**时的行为：ask（每次询问）/ tray（最小化到托盘）/ close（直接关闭该面板）。
// 管理窗口没有对应设置：它的关闭动作是固定的，见 closeManagerWindow。
func (a *App) SetPanelCloseAction(action string) error {
	return a.setCloseAction(action)
}

// SetLightweightQuitOnLastPanel 设置轻量模式（快捷方式直达）下关掉最后一个面板后是否直接退出程序。
func (a *App) SetLightweightQuitOnLastPanel(on bool) error {
	return a.config.setLightweightQuitOnLastPanel(on)
}

// SetTheme 设置界面配色（ThemeAuto / ThemeLight / ThemeDark）。
//
// 与其他 setter 不同，这里**不广播** settings-changed：管理面板与面板窗口的两个
// 切换按钮都在前端/面板侧，改完立刻自己应用，广播只会多一次无意义的重读。
//
// 要联动的是**两个窗口**：管理窗口的原生底色（深色下避免白闪）在这里顺手同步，
// 已打开的面板窗口（自绘外壳 + WebView2 默认底色）推一遍。
// 但**系统深浅色在 auto 模式下变化**不经过这里（没人改设置），走的是
// ApplyWindowTheme（前端 matchMedia）与面板窗口自己的 WM_SETTINGCHANGE 两条路。
func (a *App) SetTheme(theme string) error {
	if err := a.config.setTheme(theme); err != nil {
		return err
	}
	a.syncWindowTheme()
	a.applyThemeToPanels(a.resolveTheme())
	return nil
}

// SetLanguage 设置界面语言（LanguageAuto / LanguageZhCN / LanguageEnUS）。
//
// 与 SetTheme 同一论证不广播 settings-changed：改语言的唯一入口就是前端自己，
// await 成功后 location.reload() 重新渲染（事件监听是渲染后一次性绑死的，原地换语言
// 等于要求整个渲染流程可重入，不值得）。原生侧（询问框 / 托盘菜单）每次显示时现读配置
// （见 native_text_windows.go），语言切换对它们天然即时生效。
func (a *App) SetLanguage(language string) error {
	return a.config.setLanguage(language)
}

// ExportConfig 把当前配置导出到用户选择的位置（原生另存为对话框）。
// 只含 config.json 的内容（面板列表 + 应用设置）；图标缓存与 WebView2 会话不在
// 导出范围 —— 前者可由「刷新图标」重建，后者本来就不该搬（见「保存登录密码」）。
// 用户取消对话框返回 false，不算错误。
func (a *App) ExportConfig() (bool, error) {
	dest, err := wailsRuntime.SaveFileDialog(a.ctx, wailsRuntime.SaveDialogOptions{
		Title:           a.nativeText().Config.ExportTitle,
		DefaultFilename: "PanelDock-config.json",
	})
	if err != nil {
		return false, errCodeWrap(errConfigExportFailed, err)
	}
	if dest == "" {
		return false, nil
	}
	if err := a.config.exportTo(dest); err != nil {
		return false, err
	}
	return true, nil
}

// ImportConfig 从用户选择的文件导入配置（原生打开对话框），整体替换当前配置。
// 校验在 configStore.replaceWith（失败时现有配置不动；成功前自动备份
// config.json.bak）。返回 false = 用户取消对话框。
//
// 导入成功后的收尾：托盘图标、两窗口配色、管理界面全部按新配置刷新（广播
// settings-changed / panels-changed，管理界面自己重读）。**已打开的面板窗口不关**：
// 它们对应的面板若已不在新配置里，只是下次启动不再打开；运行中的窗口关掉是
// 导入之外的动作，不该由导入顺手做掉。
func (a *App) ImportConfig() (bool, error) {
	src, err := wailsRuntime.OpenFileDialog(a.ctx, wailsRuntime.OpenDialogOptions{
		Title: a.nativeText().Config.ImportTitle,
		Filters: []wailsRuntime.FileFilter{
			{DisplayName: "JSON (*.json)", Pattern: "*.json"},
		},
	})
	if err != nil {
		return false, errCodeWrap(errConfigImportFailed, err)
	}
	if src == "" {
		return false, nil
	}
	if err := a.config.replaceWith(src); err != nil {
		return false, err
	}
	a.syncTray()
	a.syncWindowTheme()
	a.applyThemeToPanels(a.resolveTheme())
	a.notifySettingsChanged()
	a.notifyPanelsChanged()
	return true, nil
}

// setCloseAction 写关闭行为，并把「它可能顺带改了托盘图标开关」这件事收尾干净。
//
// 选中「最小化到托盘」会连带把托盘图标打开（配置层的不变量，见 ErrTrayIconRequired）——
// 那是后端自己改的设置，必须 syncTray 把图标真的加上，并广播让界面重新读取，
// 否则界面上的勾选状态会与配置文件不一致。
func (a *App) setCloseAction(action string) error {
	before := a.config.settings()
	if err := a.config.setCloseAction(action); err != nil {
		return err
	}
	if after := a.config.settings(); after.ShowTrayIcon != before.ShowTrayIcon {
		a.syncTray()
		a.notifySettingsChanged()
	}
	return nil
}

// panelWindows 返回当前已打开面板窗口的快照。
func (a *App) panelWindows() []*panelWindow {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]*panelWindow, 0, len(a.panels))
	for _, p := range a.panels {
		out = append(out, p)
	}
	return out
}

// SetActiveTab 切换指定面板的活动标签（运行时切换 + 持久化默认标签）。
func (a *App) SetActiveTab(panelID, tabID string) error {
	a.mu.Lock()
	p := a.panels[panelID]
	a.mu.Unlock()
	if p == nil {
		return errCode(errPanelNotOpen)
	}

	index := p.switchTab(tabID)
	if index >= 0 {
		_ = a.config.setDefaultTabIndex(panelID, index)
	}
	return nil
}

// DeleteTab 删除指定面板中的某个标签（至少保留一个），并**清空它的浏览器数据**。
//
// 窗口开着时只销毁这一个标签的 WebView2（其余标签的页面原样保留，不重开窗口），
// 然后删除它的 profile 目录 —— Cookie、登录态、缓存、已保存的密码一并清掉。
// 这与删除分组的数据清理语义一致：标签从此不存在，留着它的数据只是白占盘、
// 把密码继续留在盘上，而且配置里已经没有它的 ID，之后想清也找不到入口。
//
// 清理失败的取舍与 DeletePanel 不同：标签的 WebView2 一旦销毁就装不回去，
// 配置必须照常更新（否则下次打开标签又回来了），只把失败如实报给用户，
// 让「重置数据」做兜底。
func (a *App) DeleteTab(panelID, tabID string) error {
	cfg, ok := a.config.get(panelID)
	if !ok {
		return errCode(errPanelNotFound)
	}

	// 读取当前标签列表，过滤掉要删除的标签。
	newTabs := make([]PanelTab, 0, len(cfg.Tabs))
	found := false
	for _, t := range cfg.Tabs {
		if t.ID == tabID {
			found = true
			continue
		}
		newTabs = append(newTabs, t)
	}
	if !found {
		return errCode(errTabNotFound)
	}
	if len(newTabs) == 0 {
		return errCode(errTabMinOne)
	}

	// 窗口开着：先销毁该标签的 WebView2（浏览器进程放手后目录才删得掉）。
	// removeTab 内部会同步等 UI 线程执行完。
	a.mu.Lock()
	p := a.panels[panelID]
	a.mu.Unlock()
	if p != nil {
		p.removeTab(tabID)
	}

	if err := clearTabProfile(tabID, clearUntilStable); err != nil {
		// 标签已经从窗口上摘掉了，配置必须跟上，否则它下次又被打开、
		// 数据也永远没人清。失败只能如实报告，交给「重置数据」兜底。
		_ = a.config.updateTabs(panelID, newTabs)
		return errCodeWrap(errTabRemoveUnclean, err)
	}

	return a.config.updateTabs(panelID, newTabs)
}

// AddTab 向指定面板添加一个新标签。
func (a *App) AddTab(panelID, name, rawURL string) (PanelTab, error) {
	if err := validatePanelInput(name, rawURL); err != nil {
		return PanelTab{}, err
	}

	cfg, ok := a.config.get(panelID)
	if !ok {
		return PanelTab{}, errCode(errPanelNotFound)
	}

	tab := PanelTab{
		ID:   newTabID(),
		Name: name,
		URL:  rawURL,
	}
	newTabs := make([]PanelTab, len(cfg.Tabs))
	copy(newTabs, cfg.Tabs)
	newTabs = append(newTabs, tab)

	if err := a.config.updateTabs(panelID, newTabs); err != nil {
		return PanelTab{}, err
	}
	return tab, nil
}

// GetPanelTabs 返回指定面板的标签列表。
func (a *App) GetPanelTabs(panelID string) ([]PanelTab, error) {
	cfg, ok := a.config.get(panelID)
	if !ok {
		return nil, errCode(errPanelNotFound)
	}
	return cfg.Tabs, nil
}

// UpdatePanelTabs 整体替换指定面板的标签列表。
//
// 被移除的标签与 DeleteTab 同一套语义：销毁其 WebView2、清空其浏览器数据
// （Cookie、登录态、已保存的密码）。窗口开着时**只销毁被移除标签的 WebView2**，
// 其余标签不重开窗口 —— 改名/改地址就地热更新（updateTabInfos）。
// 只有本次保存**新增了标签**时才退回整窗重开：热创建一个新 WebView2 要走完整的
// environment 异步创建链，为少见场景把它抽出来不值得。
func (a *App) UpdatePanelTabs(panelID string, tabs []PanelTab) error {
	if len(tabs) == 0 {
		return errCode(errTabMinOne)
	}
	for _, t := range tabs {
		if err := validatePanelInput(t.Name, t.URL); err != nil {
			// 码原样透传（前端按码翻译），标签名放在细节里 —— 用户得知道是哪个标签没填对。
			var ae *appError
			if errors.As(err, &ae) {
				return errCodeDetail(ae.code, t.Name)
			}
			return err
		}
	}

	cfg, ok := a.config.get(panelID)
	if !ok {
		return errCode(errPanelNotFound)
	}

	// 确保每个标签都有 ID。
	for i := range tabs {
		if tabs[i].ID == "" {
			tabs[i].ID = newTabID()
		}
	}

	// 差集：配置里有、新列表里没有的标签，就是本次被移除的。
	inNew := make(map[string]bool, len(tabs))
	for _, t := range tabs {
		inNew[t.ID] = true
	}
	var removed []PanelTab
	for _, t := range cfg.Tabs {
		if !inNew[t.ID] {
			removed = append(removed, t)
		}
	}
	// 反过来：新列表里出现配置里没有的 ID，说明这次加了新标签。
	inOld := make(map[string]bool, len(cfg.Tabs))
	for _, t := range cfg.Tabs {
		inOld[t.ID] = true
	}
	hasNew := false
	for _, t := range tabs {
		if !inOld[t.ID] {
			hasNew = true
			break
		}
	}

	// 窗口开着：先销毁被移除标签的 WebView2（目录被它占着就删不掉）。
	a.mu.Lock()
	p := a.panels[panelID]
	a.mu.Unlock()
	if p != nil {
		for _, t := range removed {
			p.removeTab(t.ID)
		}
	}

	if len(removed) > 0 {
		if err := clearPanelProfiles(removed, clearUntilStable); err != nil {
			// 与 DeleteTab 同一取舍：窗口上的标签已经摘掉了，配置必须跟上；
			// 没清干净的数据如实报告，交给「重置数据」兜底。
			_ = a.config.updateTabs(panelID, tabs)
			return errCodeWrap(errTabsRemoveUnclean, err)
		}
	}

	if err := a.config.updateTabs(panelID, tabs); err != nil {
		return err
	}

	if p == nil {
		return nil
	}
	if hasNew {
		// 新标签要创建新的 WebView2，走整窗重开（见函数头注释）。
		a.restartPanel(panelID)
		return nil
	}
	// 纯删除/改名/改址：热更新窗口里的标签，不打断其余页面。
	p.updateTabInfos(tabs)
	return nil
}

// onPanelClosed 由面板窗口的 dispose 流程回调：清理运行表并保存最后窗口状态。
// 是否收工交给 quitWhenNoWindows 判定：没有窗口、也没有托盘图标时进程才干净退出。
// 注意：关闭单个面板不会影响其他面板与管理窗口——「直接关闭」只作用于本面板。
func (a *App) onPanelClosed(id string, p *panelWindow) {
	a.mu.Lock()
	delete(a.panels, id)
	ctx := a.ctx
	a.mu.Unlock()

	// 写配置前最后过一道校验：坏值（最小化的哨兵矩形、退化成零头的尺寸）一旦落盘就是
	// 「下次打开这个面板找不到窗口」，代价远大于「这次没记住位置」—— 不可信就保留原值。
	// 详见 panel_window_windows.go 的 captureBounds / plausibleWindowState。
	if state := p.lastWindowState(); plausibleWindowState(state) {
		_ = a.config.updateWindowState(id, state)
	}

	// 面板数量变化可能让托盘图标不再必需（例如此前唯一的保留理由就是「某个面板藏在托盘里」）。
	a.syncTray()

	if a.shouldQuitAfterPanelClosed() && ctx != nil {
		// 稍延时退出，让面板窗口的 WebView2 拆除先完成，避免拆到一半被杀。
		go func() {
			time.Sleep(200 * time.Millisecond)
			a.requestQuit(ctx)
		}()
	}
}

// shouldQuitAfterPanelClosed 判断「最后一个分组标签窗口刚被关闭」时进程是否应自动退出。
func (a *App) shouldQuitAfterPanelClosed() bool {
	return a.quitWhenNoWindows(a.panelCount())
}

// quitWhenNoWindows 是「没有任何窗口剩下时进程是否退出」的**唯一**判定，两个场景共用：
//   - 最后一个分组标签窗口刚被关掉（onPanelClosed）；
//   - 用户正要关掉最后一个分组标签窗口，提前如实告知后果（panelCloseEndsProcess）。
//
// 判断只有一条：**还有窗口、或者托盘里还有图标，就不退出整个程序** ——
// 托盘图标是重新找回界面的入口，撤掉它等于把程序藏进了看不见的地方。
// 窗口与托盘都没了才收工。轻量模式（`--open` 启动、用完即走）是唯一的例外，
// 且这个例外本身可以在设置里关掉（LightweightQuitOnLastPanel）：
// 关掉后轻量实例与常规实例完全一致 —— 只要托盘图标还开着就留在托盘里。
//
// remainingPanels 是「假设此刻还剩几个分组标签窗口」：实时判定传运行表长度，预测传 0。
func (a *App) quitWhenNoWindows(remainingPanels int) bool {
	a.mu.Lock()
	stopping := a.quitting
	lightweight := a.lightweight
	resident := a.resident
	managerShown := a.mainShown
	settings := a.config.settings()
	a.mu.Unlock()
	ephemeral := lightweight && settings.LightweightQuitOnLastPanel

	if stopping {
		return false // 正在主动退出：由 Wails 的 OnShutdown 统一收尾，不再补刀
	}
	if remainingPanels > 0 {
		return false // 还有分组标签窗口：程序留在运行状态
	}
	if managerShown {
		return false // 管理窗口本身就是那个「还存在的窗口」
	}
	if resident {
		return false // 管理窗口正藏在托盘里：图标在那儿等着用户回来
	}
	if ephemeral {
		return true // 轻量模式用完即走：不为托盘图标多留一个进程
	}
	return !settings.ShowTrayIcon // 没有窗口了：托盘里还有图标就继续留着，否则退出
}

// handleIPCCommand 处理由其他实例转发过来的命令（见 ipc_windows.go）。
func (a *App) handleIPCCommand(cmd string) {
	if panelID := ipcPanelIDFromCommand(cmd); panelID != "" {
		if err := a.openPanelFromExternalRequest(panelID); err != nil {
			println("ipc open panel:", err.Error())
			if errors.Is(err, ErrPanelKeptDisabled) {
				// 用户刚在询问框里选了「保持停用」：回到静默状态，不弹管理界面。
				return
			}
			// 面板打不开时给出可见反馈，而不是静默失败。
			a.ShowMainWindow()
		}
		return
	}
	// show 或未知命令：呼出管理界面。
	a.ShowMainWindow()
}
