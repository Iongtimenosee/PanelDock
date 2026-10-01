# AGENTS.md

## 项目定位

PanelDock 是一个 Windows/Wails 桌面工具：用隔离的 WebView2 会话打开局域网管理页面（OpenClash、路由器、NAS 等）。主窗口（Wails）负责面板与标签配置管理；面板窗口是无任何桥接的原生 Win32 + WebView2 窗口。

## 当前架构

- `main.go`：Wails 入口，绑定 App；启动时先做单实例判定与请求转发。
- `app.go`：主窗口桥接 API（面板/标签 CRUD、打开/关闭、置顶/托盘、**重置数据**）。标签变更**默认不再整窗重开**：`UpdatePanelTabs` 只对「新增了标签」才走 `restartPanel`，删标签用 `panelWindow.removeTab` 热移除、改名/改址用 `updateTabInfos` 热更新（见「删除标签的数据清理」）。
- `ipc_windows.go`：单实例命名互斥体 + 隐藏 IPC 窗口（WM_COPYDATA）转发；该窗口同时**承载应用级托盘图标**（`startIPCListener` 返回 hwnd 供 `trayAttach` 使用）。
- `tray_windows.go`：应用级托盘图标与托盘菜单（`Shell_NotifyIconW`，挂在常驻 IPC 窗口上），以及 `App.trayNeeded` / `App.syncTray`。
- `prompt_modal_windows.go`：手工搭建的**原生模态询问框公共设施**（窗口类 + 窗口过程 + 控件/字体/DPI/居中/消息循环，`promptModalSpec` 驱动）。所有询问框共用它，靠**标题栏文案**区分是谁、靠控件 ID 分发动作。
- `close_prompt_windows.go`：原生「关闭面板窗口」询问框（`promptCloseDialog`，文案从 `App.nativeText().ClosePrompt` 取，含「记住我的选择」复选框）。**只有面板窗口会问** —— 管理窗口的关闭动作是固定的，不弹框。
- `enable_prompt_windows.go`：原生「面板已停用」询问框（`enablePromptSpec` / `promptEnableDisabledPanel`），用于快捷方式打开停用面板时询问是否启用；`mainWindowHandle()`（Wails 管理窗口句柄查找）也在这里。
- `native_text_windows.go`：**原生界面文案词典（i18n 的 Win32 侧）** —— 关闭/启用询问框、托盘菜单与悬浮提示、面板关闭询问的补充说明，zh-CN / en-US 两份；`App.nativeText()` 每次现读配置解析（不缓存，语言切换即时生效），auto 用 `GetUserDefaultUILanguage` 判定。见「界面语言（i18n）」一节。
- `apperror.go`：**面向用户的错误码**（`appError` + `errCode/errCodeDetail/errCodeWrap`）。到达用户眼前的后端错误一律返回码（如 `panel.nameRequired`），前端词典翻译；`Unwrap` 保住哨兵错误的 `errors.Is` 判定。见「界面语言（i18n）」一节。
- `config.go`：`%APPDATA%\PanelDock\config.json` 的加载/原子写入/损坏回退/旧版单 URL 迁移；便携模式路径解析；应用级设置（`settings` 节点）；面板的快捷方式路径记录（`shortcut`，**至多一个** —— 旧版的 `shortcuts` 数组在载入时收敛成第一个仍然存在的路径并把数组抹掉，见 `TestConfigMigrationShortcutsArrayToOne`）。
- `shortcut_windows.go`：桌面快捷方式（IShellLinkW + IPersistFile 手工 COM），含读回既有 .lnk 与联动清理。
- `panel_window_windows.go`：手工 COM 子集的独立面板窗口（**无边框**，系统非客户区被 `WM_NCCALCSIZE` 吃掉），含原生标签栏（最右端是**明暗配色切换按钮**）、窗口状态记忆、外壳的 `WM_ERASEBKGND` 自绘。托盘图标不在这里（由常驻 IPC 窗口承载）。
- `titlebar_windows.go`：面板窗口的**自绘标题栏**——无边框框架（命中测试/最大化约束/DWM 阴影与直角）、`PanelDock.PanelTitleBar` 子窗口（favicon + 后退/前进/刷新/停止 + 地址栏 EDIT + 置顶开关 + 打开管理面板 + 最小化/最大化/关闭）、WebView2 导航事件接入。图标**怎么拿到**不在这里（见 `icons_windows.go`），这里只管把 16px 位图**画上去**。标题栏与标签栏的图标按钮共用 `paintChromeIconButton`（配色从 `p.chrome()` 取）。
- `icons_windows.go`：面板窗口**图标从哪来、要哪几个尺寸**。多来源候选收集（Web App Manifest `icons[]` / `apple-touch-icon` / `link[rel~=icon]` / 根目录 `/favicon.ico` 兜底）→ 下载解码（ICO 多帧全取）→ 按目标边长分别择优 → 产出标题栏 16px 预乘 DIB 与任务栏小/大两个 `HICON`；一个都拿不到时自绘**首字母色块**（monogram）。解析出的候选同时登记到进程内样本表（见 `iconcache_windows.go`）。
- `iconcache_windows.go`：面板图标的**落盘缓存**与会话级的两阶段桥接 API。把站点图标编码成多帧 `.ico` 存到 `config.json` 旁边的 `icons\<面板ID>.ico`，供**快捷方式与任务栏固定项**使用（那些是 shell 保管的静态图标引用，认不了内存里的 HICON）；`App.InspectPanelIcon` 只做预告，`App.ApplyPanelIcon` 才落盘并改写 `.lnk`（桌面没有快捷方式时**顺手创建一个**，不是只存图标），删面板时 `dropPanelIconCache` 负责收尾。见「刷新图标」一节。
- `aumid_windows.go`：`applyAppUserModelID()` 在 `main()` 最早处声明进程的显式 AppUserModelID（`PanelDock.Desktop.IsolatedPanels`）。**不声明的话面板窗口的任务栏图标会被自己创建的快捷方式劫持**（见「图标链路」里那条）。
- `password_autosave_windows.go`：把分组级的「保存登录密码」开关送到 WebView2（`get_Settings` → QI `ICoreWebView2Settings4` → `put_IsPasswordAutosaveEnabled`）。见「保存登录密码」一节。
- `theme_windows.go`：**界面配色（明暗主题）**的后端侧 —— 读 Windows 深浅色偏好（注册表 `AppsUseLightTheme`）、把三态设置解析成生效主题、管理窗口的原生底色与主题联动（防启动白闪）。见「界面配色（明暗主题）」一节。
- `theme_panel_windows.go`：**面板窗口外壳的配色表**（明暗两套 `panelChrome` + 画刷、`panelColorRef` 的 RGB→BGR 换位、`ICoreWebView2Controller2` 的默认底色、跨线程的 `requestTheme`/`applyPendingTheme`、配色按钮的动作 `togglePanelTheme`、`WM_SETTINGCHANGE` 的过滤）。见「界面配色 · 面板窗口侧」一节。
- `frontend/src/main.js`：主窗口 UI（原生 JS，无框架），全部文案经 `t()` 取自词典。
- `frontend/src/i18n/`：前端词典两份（`zh-CN.js` 源语言 / `en-US.js`）+ 查表模块（`t` / `tErr` / `resolveLocale`），随 vite 编译进产物。见「界面语言（i18n）」一节。

### 关键设计决定

- **轻量模式（`--open` 启动）**：主窗口以 `options.App.StartHidden` 启动，只显示面板窗口；进程寿命由 `App.quitWhenNoWindows` 判定（见「进程寿命」一节）。关闭管理窗口一律走 `OnBeforeClose` → `App.closeManagerWindow()`（固定动作，不询问）；关闭面板窗口走 `panelWindow.handleInteractiveClose()` → `App.decideClose()`，按设置询问或直接执行。
- **单实例调度**：命名互斥体 `Local\PanelDock.SingleInstance.Mutex`（`panelDockMutexName` 可注入测试）。非首个实例通过隐藏 IPC 窗口把命令 `open:<面板ID>`（或 `show`）用 `WM_COPYDATA` 转发给已运行实例，然后自行退出。
  - IPC 窗口类名 `PanelDock.IPC`（`ipcWindowClassName` 可注入测试）。**必须是普通顶层窗口而非 `HWND_MESSAGE`**——消息专用窗口无法被 `FindWindow` 找到。
  - 转发用 `SendMessageTimeoutW` + `SMTO_ABORTIFHUNG`（3s），避免目标线程挂起时永久阻塞；找不到窗口时重试 20×50ms，覆盖"上一个实例仍在启动"的竞态。
  - 转发失败（极旧实例/窗口未就绪）退化为独立实例继续运行，不静默退出。
- **应用级设置**（`config.json` 的 `settings` 节点，管理界面「应用设置」区块可改）：
  - `showTrayIcon`（默认 true）：**是否在系统托盘显示图标**。图标由**常驻 IPC 窗口**承载（`tray_windows.go`，固定 uid=1），因此**与打开几个面板无关**——一个面板都没开时托盘里也有入口。
  - **托盘图标与「最小化到托盘」的绑定（不变量）**：面板窗口的关闭行为是 `tray` 时，`showTrayIcon` **自动且必须**为 true。理由：选了这个行为的窗口一关闭就藏进托盘，图标是把它找回来的唯一入口。四个落点缺一不可：`load()` 纠正矛盾配置并写回、`settingsLocked()` 读取时归一化（`trayNeeded` / `quitWhenNoWindows` 都读它，绝不能看到矛盾组合）、`configStore.setCloseAction` 选中 `tray` 时连带打开、`setShowTrayIcon(false)` 直接拒绝（`ErrTrayIconRequired`，文案面向用户）。前端 `applySettings` 据下拉值显示 `#st-tray-forced` 提示（`alert` 只兜底）；`App.setCloseAction` 在图标被连带打开时 `syncTray()` + 广播。
  - **没有面板级 `minimizeToTray`**：托盘行为统一由 `showTrayIcon` + 关闭行为决定，面板卡片上没有「最小化到托盘」勾选，也没有对应的桥接方法；旧字段 `DeprecatedMinimizeToTray *bool` 只在 `load()` 里读一次用于清除，配置文件里不再保留该键。**面板窗口的最小化按钮就是普通最小化** —— 把窗口送进托盘只发生在「关闭」时（用户选 tray，或设置里预设 tray）。`panelWindow.hiddenInTray` 是记录「窗口正藏在托盘里」的唯一字段，也是 `trayNeeded` 的兜底输入。
  - `panelCloseAction`：关闭**面板窗口**时的行为，取值 `ask`（默认，每次询问）/ `tray`（最小化到托盘）/ `close`（直接关闭该面板，不再询问）。选「询问」时弹原生询问框，勾选「记住我的选择」会写回这一项。
  - `lightweightQuitOnLastPanel`（默认 true）：**轻量模式**（`--open` 启动、管理窗口从未打开）下关掉最后一个面板后是否直接退出程序。开启 = 「用完即走」，不在托盘里留进程；关闭 = 与常规启动完全一致（托盘图标还开着就留在托盘里）。常规启动不受它影响。
  - `theme`（默认 `auto`）：管理界面的明暗配色，取值 `auto`（跟随 Windows 深浅色）/ `light` / `dark`。用字符串枚举而不是 bool：默认值是「跟随系统」bool 表达不了，且空串（老配置缺键）天然落到 `auto`，不需要 `settingsHasKey` 那套补默认值迁移。见「界面配色（明暗主题）」一节。
  - **管理窗口没有关闭行为设置**：它的动作是固定的（见「管理窗口的关闭动作」一节），曾经存在的 `managerCloseAction` 已废弃 —— `load()` 清掉该键并写回。
  - 旧版单一字段 `closeAction` 已废弃：`load()` 把它落到 `panelCloseAction`（旧值 `quit` 归一化为 `close`）后**清空旧字段**并写回文件。**测试/脚本改配置时必须一并清掉它**，否则加载时的迁移会覆盖新值（见 `e2eForceCloseActionAsk`）。
  - 老配置无 `settings` 节点时，`load()` 补默认值并写回（用 `*AppSettings` 指针区分「节点缺失」与「显式 false」）。
  - **给 `settings` 新增「默认开启」的布尔项时，必须按「键在不在」补默认值**（`settingsHasKey`）：`json.Unmarshal` 分不清「键缺失」与「显式 false」，老配置里没有的键读出来就是零值 `false`。不补的后果是**它还会被固化**——程序随后写回配置，把这个错误默认值落到文件里，之后再没人能看出它本来是默认值。`lightweightQuitOnLastPanel` 上线时实测踩到：老配置一律被当成「关闭用完即走」，轻量模式关掉最后一个面板后赖在托盘里，E2E 连挂三条「关闭最后一个面板后进程未退出」。补默认值的判据只能是原始 JSON 里有没有这个键（`settingsHasKey`），不能看字段的值。
- **配置导出 / 导入**（设置区两个按钮，`App.ExportConfig` / `App.ImportConfig`，文件对话框在后端用 Wails 原生对话框弹）：**只管 config.json 的内容**（面板列表 + 应用设置）—— 图标缓存可由「刷新图标」重建、WebView2 登录态本来就不该跨机器搬，都不在范围内。导入的关键语义（`configStore.replaceWith`）：
  - **校验失败时现有配置一个字节不动**。预检（JSON 可解析 + schema 版本一致）必须在 `load()` 之前做 —— `load()` 对坏 JSON 的语义是「回退默认配置并写回」，直接 load 用户的文件会把垃圾悄悄变成默认配置；通过预检后复制到临时副本（`config.json.importing`）走完整 `load()`，迁移与托盘不变量纠正都发生在副本上，**不碰用户的源文件**。
  - 成功前把当前配置备份为 `config.json.bak`（同目录）；导入后 `syncTray` + 主题双窗口 + settings/panels 双广播收尾，**已打开的面板窗口不关**（新配置里没有它，只是下次不再打开）。
  - 用户取消对话框返回 `false`，不算错误（前端据此区分取消与失败）。
- **关闭面板窗口**（`close_prompt_windows.go`，只有这一种询问框）：
  - 文案：「最小化到托盘」/ **「直接关闭」**（只关这一个面板，其他面板与管理窗口不受影响）/ 取消，外加「记住我的选择（以后关闭面板窗口不再询问）」。
  - 标题栏 `PanelDock · 关闭面板窗口`（`closePromptLabels.Caption`）—— 端到端用例据此定位。
  - 询问框的复选框语义只有一种：「以后关闭面板窗口不再询问」，写回 `panelCloseAction`（`App.commitCloseChoice`）。
  - 在「关掉它就是最后一个面板、且此后没有任何窗口与托盘」时会多出一行提示（`panelWindow.closePromptNote` → `App.panelCloseEndsProcess`），如实告知「选直接关闭后程序会退出」。提示存在时对话框自动加高（正文 > 2 行 → 高版面），避免正文被静态控件裁掉。
- **托盘菜单**（`tray_windows.go`）：显示/隐藏面板、窗口置顶、关闭面板（以上三项对「活动面板」生效，无面板打开时置灰）、打开管理面板、退出 PanelDock。「活动面板」= `App.activePanelID`，由 `OpenPanel` / `markActivePanel` 维护；它已关闭时退化为配置顺序里第一个仍打开的面板。轻量模式下靠「打开管理面板」呼出被隐藏的主窗口（`App.ShowMainWindow`）。
- **托盘图标「必须保留」的三条理由**（`App.trayNeeded`，任一成立即保留图标，即使 `showTrayIcon=false`）：全局开关开启 / 管理窗口正藏在托盘（`App.resident`）/ 某个面板窗口正藏在托盘（`panelWindow.hiddenInTray`）。窗口从托盘恢复时清掉对应标记；`App.syncTray()` 重新计算并幂等地增删图标。**例外**：`App.quitting` 时无条件返回 false，不再维护图标。第 2、3 条优先于「用户取消了托盘勾选」——藏起来的窗口必须有图标才能找回。
- **Explorer 重启要重注册图标**：`tray_windows.go` 注册 `TaskbarCreated` 消息，`ipcWindowProc` 收到后调 `trayReAdd()`——否则任务栏重启后托盘图标会消失。
- **两条关闭路径必须分清**（重要）：
  - **交互式关闭** = 用户点标题栏 X 发出的 `WM_CLOSE`。走 `panelWindow.handleInteractiveClose()` → `App.decideClose(owner, note)`，按设置询问或直接执行；「直接关闭」只 `p.close()` 本窗口。
  - **主动关闭** = 私有消息 `win32WMDirectClose`（`WM_APP+2`），由 `panelWindow.close()` 发出，用于退出程序、删除面板、标签变更重开，以及托盘菜单「关闭面板」。**永不弹询问框**。
  - 新增任何"程序内部关窗口"的代码，一律走 `p.close()`，不要直接 `PostMessage(WM_CLOSE)`，否则会莫名弹出询问框。
- **管理窗口的关闭动作是固定的，不询问**（`App.onBeforeClose` → `App.closeManagerWindow`）：
  - 规则：**还有分组标签窗口、或托盘图标还开着 → 只关自己**（把管理窗口藏起来，进程继续运行）；**两者都没有 → 放行**，管理窗口一关就是「最后一个窗口关闭」，进程随之结束。这与面板窗口的收工判定同源（`quitWhenNoWindows`）—— 用户看到的只有一条规则。
  - 为什么藏而不销毁：**管理窗口是 Wails 的窗口，一销毁就等于「没有窗口了」**，Wails 立刻结束进程并把分组标签一起带走；面板窗口是自建 Win32 窗口、救不了场。所以「只关自己」只能靠 `OnBeforeClose` 拦住。
  - **不要写「顺手把其它窗口关掉」或「顺手撤掉托盘图标」这类代码**：前者会破坏「还有窗口在就不退出」，后者会让行为与设置里那个勾选框对不上。托盘设置只由「在系统托盘显示图标」管，关闭管理窗口一个字节都不改。
  - 「藏起来」时若托盘图标开着，顺手置 `resident = true`（语义：管理窗口正藏在托盘里），图标才能留下把窗口找回来；图标本来就关着时不置位，免得凭空给它变出一个图标来。
  - 管理窗口藏起来之后，想回管理界面**再启动一次程序**即可（单实例 IPC 转发 → `ShowMainWindow`）。
  - `hideManagerWindow()` 用 `App.ctx`（startup 钩子给的）而不是 `OnBeforeClose` 传进来的那个：Wails runtime 只接受生命周期钩子的 ctx，其它 context 会直接 `log.Fatal`；单测里 `ctx == nil` 时只更新状态、不碰窗口。
- **退出判定的唯一来源是 `App.quitWhenNoWindows(remainingPanels)`**（`shouldQuitAfterPanelClosed` / `panelCloseEndsProcess` 都委托它）：
  - 有分组标签窗口 → 不退；管理窗口还在（`mainShown`）→ 不退；管理窗口正藏在托盘里（`resident`）→ 不退；正在主动退出（`quitting`）→ 不退。
  - 都没有时：轻量模式**且**开着「用完即走」（`lightweightQuitOnLastPanel`）→ 退；否则 **看托盘** —— 托盘图标还开着就留在托盘里等用户回来，关掉了才退。
  - `remainingPanels` 是「假设此刻还剩几个面板窗口」：实时判定传运行表长度，预测（问「关掉这个面板后程序会退出吗」）传 0。
  - `mainShown` 是「还有没有窗口」的输入之一，必须准：常规启动在 `startup` 里置 `!lightweight`，显示/隐藏只有 `ShowMainWindow` 与 `hideManagerWindow` 两个入口。
- **后端改设置要广播**（`App.notifySettingsChanged` → Wails 事件 `paneldock:settings-changed`，前端 `EventsOn` 收到后重读 `GetSettings`）：否则设置区显示的勾选状态会与配置文件不一致 —— **选中「最小化到托盘」会连带把托盘图标打开**（`App.setCloseAction`，见上节不变量）就是这种情形。前端没有轮询，新增任何「后端自行改设置」的路径都要广播。
- **托盘图标兜底**：`hideToTray()` 与最小化到托盘前会 `setHiddenInTray(true)`，确保窗口隐藏后必然有图标可找回（即使全局关闭了托盘图标）；`showFromTray()` / `activate()` 恢复时清掉该标记。
- **删除面板时的快捷方式清理是「显式勾选」的结果**（`App.DeletePanel(id, removeShortcuts)`）：清理只在 `removeShortcuts=true` 时发生，由前端确认对话框里「同时删除快捷方式」可选项的勾选状态决定；没勾就一个 `.lnk` 都不动。要清理时的做法：先读配置里记录的 `panels[].shortcut`（**只有一个**），再扫描桌面目录，用 `IShellLinkW.GetPath/GetArguments` 读回既有 .lnk，匹配「指向本程序（按文件名比对）+ 参数为 `--open <面板ID>`」——后者覆盖用户改名/移动过、以及本功能上线前手工创建的快捷方式。**清理必须发生在 `config.delete` 之前**：`listPanelShortcuts` 要读面板配置里记录的路径，配置一删就查不到（只剩桌面扫描，改名/移出桌面目录的就漏了）。`deleteShortcutFiles` **只删 `.lnk` 后缀**，配置被改坏也不会误删普通文件。
- **前端删除确认用应用内 `<dialog>`（`showModal`），不是 `window.confirm`**：浏览器确认框放不下复选框，而「清理快捷方式」恰恰需要复选框。删除前先 `App.ListPanelShortcuts(id)` 拿到清单，用于列出路径并决定可选项是否可用（清单为空 → 禁用并置灰，不给无从执行的承诺）；可选项默认勾选（保持既有「删除面板即清理快捷方式」语义）。对话框内聚焦「取消」——删除不可撤销，回车不该直接把面板删掉。面板窗口的关闭询问框是另一套东西（原生 Win32，见 `close_prompt_windows.go`）：**不要**从 Wails 绑定方法里弹原生模态框，那会在 WebView2 消息处理线程上自建 `GetMessage` 循环。
- **便携模式（绿色版）**：`exe` 同目录存在 `data` 文件夹时自动启用（约定同 VSCode 的 `data` 目录）。此时配置写 `data\config.json`，会话写 `data\WebViewProfiles\<tabID>`；整个「exe + data」文件夹拷走即完成迁移（U 盘 / 其他机器），登录会话随行。无 `data` 目录时回退系统目录（`%APPDATA%` / `%LOCALAPPDATA%`），已有用户无感。检测函数 `resolvePortableRoot`（包级变量，测试可注入）。
- **前端字段名必须用 json tag 的小写驼峰**（`panel.id`、`panel.tabs`、`tab.url`），不能用 Go 字段名大写（`panel.ID`）。Wails 线上数据遵循 `encoding/json` 序列化结果；Wails 生成的 `frontend/wailsjs/go/models.ts` 是字段名的权威参考。
- **新增 App 桥接方法后必须核对 `frontend/wailsjs/go/main/App.js` 里真的有它**（踩过，2026-10-01）：本项目路径含中文，`wails build` / `wails generate module` 的绑定生成会**静默失败**——日志正常结束、时间戳不变，但新方法不进 `App.js`/`App.d.ts`，前端 import 它时 vite 直接报 "not exported"。修法是按既有格式手工补 `App.js`（`window['go']['main']['App']['方法名']`，运行时按方法名动态解析，真身在 `main.go` 的 Bind）与 `App.d.ts` 的条目；`go.mod` 升级或挪到纯 ASCII 路径后可再试生成器。同理：**改了前端必须去掉 `wails build -s` 的 `-s`**（它是"跳过前端构建"，留着则 dist 里是旧产物）。
- **面板标签栏是原生 Win32 子窗口**（`PanelDock.PanelTabBar` 类，GDI 绘制，字体 Microsoft YaHei UI）。不向远程页面注入任何 DOM/脚本/WebMessage——远程页面与宿主之间没有任何通信通道。
- **面板窗口是无边框窗口 + 自绘标题栏**（`titlebar_windows.go`，窗口类 `PanelDock.PanelTitleBar`）：系统标题栏与边框整个去掉，顶部换成一条 40px 的自绘工具栏，从左到右依次为
  favicon → 后退/前进/刷新/停止（Segoe MDL2 字形按钮）→ 地址栏（原生 `EDIT` 子窗口）→ **置顶开关**（图钉图标，未置顶=空心，已置顶=实心+高亮蓝）→ 打开管理面板（齿轮）→ 最小化/最大化/关闭。
  标签栏 `PanelDock.PanelTabBar` 紧贴其下（`y = titleBarHeight`），WebView2 内容区从 `titleBarHeight + tabBarHeight` 开始。标签栏**最右端有一个明暗配色切换按钮**（位置正好落在标题栏「关闭」正下方）。
  - **窗口布局自上而下：自绘标题栏 → 原生标签栏 → WebView2**，三段都靠 `panelWindow.resize()` 统一摆位，`contentBounds()` 是唯一的内容区分界来源。
  - **`WM_NCCALCSIZE` 的两条分支都必须处理**（重要，踩过）：文档上的无边框配方通常只写 `if (wParam == TRUE) return 0;`，但**窗口创建期系统发来的偏偏是 `wParam == FALSE`**。只判 `TRUE` 的写法会让系统标题栏原样保留、和自绘标题栏叠成两条（实测：客户区 1104x721 vs 窗口 1120x760）。`wParam == FALSE` 时 `lParam` 是 `RECT*`，语义是「进：建议窗口矩形；出：客户区的屏幕坐标」，把它写成 `GetWindowRect` 的结果即可让客户区等于窗口矩形。
  - **`setupPanelFrameless` 里的 `SetWindowPos(SWP_FRAMECHANGED)` 不能省**：窗口管理器会缓存创建期算出的边框，不重算的话上面那一次判定可能一直不生效。同函数里再 `DwmExtendFrameIntoClientArea(1,1,1,1)` 找回 DWM 阴影（客户区被子窗口盖满，这 1px 不可见）、`DWMWA_WINDOW_CORNER_PREFERENCE = DWMWCP_DONOTROUND` 关掉 Win11 圆角（子窗口是直角，圆角会被它们的角戳穿）。
  - **缩放边框靠「让出来」**：客户区=窗口矩形之后，四边没有非客户区可供拖拽。做法是子窗口在还原状态下四边内缩 `win32FrameBorder`(8px)，让出的这一圈归父窗口，父窗口的 `WM_NCHITTEST`（`panelFrameHitTest`）在那里回 `HTLEFT/HTTOP/...`。顶部另有 `win32FrameTopStrip`(4px) 的透明条留给 `HTTOP`。最大化时内缩为 0（`frameInset()`）。
  - **拖动/双击最大化/右键系统菜单靠 `HTTRANSPARENT`**：标题栏子窗口在**按钮以外**的区域（含地址栏以外的空白）返回 `HTTRANSPARENT`，命中测试落到父窗口 → 父窗口在 `cy < titleBarHeight + tabBarHeight` 时回 `HTCAPTION`，于是这些交互全是系统原生行为，一行代码都不用写。地址栏 `EDIT` 是独立子窗口，命中的是它自己，不受影响。
  - **标签栏右侧的空白同样是拖拽区**（`tabBarHitTest`）：标签本身与右端的配色按钮回 `HTCLIENT` 自己收点击，`x` 超出标签的总宽度且不在按钮上就回 `HTTRANSPARENT` 交给父窗口判 `HTCAPTION`。父窗口那条 `HTCAPTION` 横带因此必须**同时覆盖两个子窗口的高度**（`titleBarHeight + tabBarHeight`）——只写标题栏高度的话，标签右侧那片空白会被判 `HTCLIENT`，鼠标在上面怎么拖都拖不动窗口（用户报的问题就是这个）。
  - **命中测试与点击必须共用同一份几何**：`tabBarHitTest` 判「是不是标签」用的就是 `tabIndexAt`、判「是不是配色按钮」用的就是 `themeButtonHit`，两处公式各自只能有一份。另外 `tabIndexAt` 必须显式挡掉 `x < tabBarPadding`：Go 的整数除法向零截断，左侧 8px 内边距会被算成 `-8/164 = 0` 而误判成 0 号标签（会吃掉那 8px 的拖拽区，点击也会莫名切到 0 号）。
  - **标签的绘制上限与命中上限是同一个函数**（`panelTabBarTabLimit`）：标签只画到配色按钮之前，超出上限的标签既不画也不可点（不做滚动/溢出折叠 —— 面板的标签数量按设计就是个位数）。两边算得不一样就是「看得见点不到」或「点得到看不见」。
  - **标题栏子窗口必须带 `WS_CLIPCHILDREN`**：地址栏 `EDIT` 是它的子窗口，没有这个样式时父窗口每次重绘都会把 `EDIT` 整条盖掉（实测地址栏直接消失）。
  - **标题栏按钮点击不可直接调 `p.close()`**：关闭按钮要走 `PostMessage(WM_CLOSE)`，也就是「交互式关闭」那条路径（按设置询问 / 直接执行）；用私有消息 `win32WMDirectClose` 会绕过关闭询问框。见「两条关闭路径必须分清」。
  - **`WM_CTLCOLOREDIT` 由标题栏处理**：地址栏的配色在 `panelTitleBarProc` 里返回画刷 + `SetTextColor/SetBkColor`（`EDIT` 的配色消息发给父窗口）。**颜色从 `p.chrome()` 取**，深色下是深底浅字、浅色下是白底深字 —— 别写死。
  - **地址栏回车导航会补协议头**（`navigateFromAddressBar`，无 `://` 则补 `http://`）；`Esc` 还原为当前 URL。
  - **图标链路（`icons_windows.go`）：一次页面扫描 → 多候选下载 → 按尺寸分别择优 → 标题栏与任务栏各取所需。**
    - **为什么要「多来源」**：锁到任务栏时系统要的是**大图标**（32×32 起，高 DPI 下 48），而标题栏左上角只有 16×16。只认第一个 `<link rel=icon>`（通常就一张 16 或 32）会得到一个被拉糊的任务栏图标。来源优先级：manifest `icons[]`（常带 192/512）→ `apple-touch-icon`（常 180）→ `link[rel~=icon]` → 根目录 `/favicon.ico` 兜底。**页面已声明图标时不再去猜 `/favicon.ico`**，白花一次请求。一次最多下 8 个（`panelIconMaxFetch`）。
    - **`panelPickIcon` 的规则是「够大的里挑最小的」**，不是「挑最大的」：把 512 缩到 16 会糊，原生 16 才清晰；都不够大时才退而求其次挑最大的。同尺寸并列按来源优先级决胜。
    - **ICO 是容器，必须把每一帧都解出来**（`decodeICOAll`，内嵌 PNG 与 BMP+AND 掩码两种帧都支持）。一张 `favicon.ico` 里塞 16/32/48 三帧是常态，只挑一张就等于放弃另外两个尺寸。
    - **HTML 里的图标要在页面里解析、在 Go 里下载**：`ExecuteScript` 一次性把 manifest 地址 / apple-touch-icon / link icon / `document.baseURI` 摊平成 JSON 返回，相对地址在页面里就转成绝对地址（Go 侧拿不到 `document.baseURI`）。manifest 只回传地址、内容由 Go 取 —— 它是独立文件，页面里读不到（要跟 CSP 与同源策略斗）。
    - **`ExecuteScript` 返回的是「脚本完成值的 JSON 编码」，不是字符串内容**（重要，踩过）：脚本写 `return out;`（对象）时拿到的 raw 就是 `{"manifest":...}`；写 `return JSON.stringify(out);`（字符串）时拿到的是 `"{\"manifest\":...}"` —— **外面多一层引号并转义**，直接 `json.Unmarshal` 到结构体必然失败。2026-09-30 就栽在第二种写法上，而失败路径是静默 `return 0`，症状是「favicon 永远不出现、日志一片干净」，排查方向很容易被带偏去怀疑 vtable 槽位。现在 `panelDecodeIconPayload` 两种形态都收，并有 `TestPanelDecodeIconPayloadHandlesBothShapes` 看守。
    - **解析失败别静默**：任何 `ExecuteScript` 回调用不上时都要留下痕迹，否则「什么都没发生」比报错更难查。同理，解析不出来时返回 false 保持默认图标，**不要交一个空 payload 冒充成功** —— 空 payload 会让下游画出「首字母色块」，以假乱真。
    - **favicon 不显示 ≠ favicon 逻辑错**：先怀疑 `ICoreWebView2` vtable 错位（见上一条），`ExecuteScript` 落到错误槽位上时脚本根本没执行，自然什么都取不到。
    - **取不到图标就画首字母色块**（monogram，`panelMonogramImage`）：圆角方底色由主机名 FNV 哈希稳定选取（同一站点每次同色），字形用 GDI 画到黑底 DIB 上**只取灰度当覆盖率**再在 Go 侧合成。为什么绕这一道：**GDI 往 32bpp DIB 上画字不写 alpha 通道**，直接当图标用会得到一整块透明。字号用 ANTIALIASED 而非 ClearType —— 后者次像素渲染会在 RGB 三通道写出不同值，取灰度会带彩边。
    - **两处绘制的 alpha 约定不同，别搞混**：标题栏走 `AlphaBlend`，要 **预乘** DIB（`bitmapFromImage`；`image.Image.RGBA()` 返回的正好是预乘值）；图标走 PNG，要 **直通** alpha（`panelHIconFromImage` 内部 `png.Encode` 会把预乘还原成直通，不用手写反预乘）。premultiplied 搞反的症状是半透明边缘「一圈发黑」，16×16 上肉眼几乎看不出来。
    - **32bpp DIB 的字节序是 BGRA，而 `image.RGBA.Pix` 是 RGBA —— 必须逐通道换位，不能用 `copy`**（2026-09-30 栽过）：`copy` 会把 R 写进 B 的位置，**纯红的 favicon 显示成纯蓝**。这个 bug 的可怕之处在于**编译、`go vet`、单测、E2E 全部照常通过**（E2E 只断言 `WM_GETICON` 非零，跟画出来的颜色无关），只有**逐像素采样标题栏**才发现 —— 是重构时把这段从 `titlebar_windows.go` 搬进本文件、图省事写成 `copy` 引入的。回归用例 `TestBitmapFromImageWritesBGR`（用纯红取样，`GetDIBits` 读回，断言 BGRA 为 `[0 0 255 255]`）。
    - 由此得一条通用规则：**「改了绘制代码」就必须做一次像素级复验**，别只看「有没有东西画出来」。同类错误还有窗口/控件的**位置偏移**（标题栏内容整体右移 8px 是 `win32FrameBorder` 的内容内缩，属设计值 —— 判定前先确认是设计还是缺陷）。
  - **造 `HICON` 走 PNG，不要走 `CreateIconIndirect` + 手工 DIB/掩码**（`panelHIconFromImage`）：重采样到目标边长 → `png.Encode` → `CreateIconFromResourceEx`。一次调用、不用造掩码、不用管 DIB 方向，且 PNG 的语义就是直通 alpha。
    - **教训（2026-09-30，值得记住）**：旧实现用 `CreateIconIndirect`，它**稳定返回 NULL 且 `GetLastError` 恒为 0**，当时判断是「USER32 惰性初始化」并加了个重试三次的补丁 —— **完全是错的**。真实原因是 `panelICONINFO` 结构体漏了 `xHotspot`/`yHotspot`：整个结构只有 24 字节、`hbmMask` 落在偏移 8（应 16）、`hbmColor` 落在 16（应 24），系统读到的两个位图句柄全是错位的。补上两个字段后**第一次调用就成功**。
    - 推广规则：**任何 Win32 结构体都要用 `unsafe.Offsetof` 打出偏移量核一遍**，别凭字段名猜（该结构在 x64 上是 4+4+4+4填充+8+8 = 32 字节）。**调用返回 NULL 而 `GetLastError` 为 0 时，优先怀疑参数结构体，而不是系统的脾气**；「加重试」是在掩盖自己的 bug。
  - **任务栏按钮的图标不一定是你设的那个：Explorer 会按窗口的 AppUserModelID 去匹配「已知应用」条目（快捷方式 / 固定项），匹配上就一律用那个条目的图标，窗口自己的 `WM_SETICON` 被无视**（2026-09-30 实测踩到，症状是「标题栏 favicon 正常、任务栏永远是 exe 内嵌的默认 W 图标」——W 是当时的 Wails 默认图标，2026-10-01 已换成 PanelDock 的 P，下面实录里的 W 一律读作「exe 内嵌图标」）。
    - 作祟的正是**本程序自己创建的桌面快捷方式**（`shortcut_windows.go` 会 `SetIconLocation` 指回 exe）：它的 AUMID 就是 exe 路径，与**不声明 AUMID 时进程的隐式 AUMID 完全相同**，于是每个面板窗口都被匹配走。
    - 判别实验（同一台机、同一面板、同一份 `data`，逐像素看任务栏）：`PanelDock.exe`（桌面有快捷方式指向它）→ 任务栏是 W，而 `WM_GETICON(ICON_BIG)` 拿到的确实是站点图标；把它复制改名为 `PD_probe.exe`（没有任何快捷方式指向它）→ 任务栏立刻显示站点图标；**再给 `PD_probe.exe` 造一个快捷方式，它又被劫持回 W**。根治办法是 `applyAppUserModelID()`（`aumid_windows.go`）声明一个不会被任何快捷方式携带的 AUMID —— 加完之后同一个 exe、同一个快捷方式启动，任务栏恢复站点图标。
    - **只有「运行中且未固定」的窗口按钮会跟着站点走。**固定项那一个图标是静态的：固定到任务栏之后按钮走固定项（.lnk）的图标，只有 exe 图标可用。要让固定项也显示站点图标，得把站点图标缓存成 .ico 并改写那份 .lnk 的 `SetIconLocation` + 通知 shell —— **已实现，走分组卡片上的「刷新图标」按钮**（见「刷新图标」一节），它是用户显式点出来的动作，不是自动改写。
    - 这条也解释了为什么「不该用真的任务栏去断言」：它依赖桌面快捷方式、固定项、任务栏可见性，做不成稳定的回归。守它的是两层——`TestApplyAppUserModelIDSetsExplicitID`（设一次再读回来，专防 `shell32` 导出名写错导致**静默 no-op**、任务栏悄悄退回旧行为）+ 上面那套逐像素判别实验（人工）。
  - 回归用例见 `TestE2ETitlebarFollowsRealNavigation`（三条断言：地址栏跟随真实跳转、只有我方 UA 去取图标、`WM_GETICON(ICON_BIG)` 非零）与 `TestPanelHIconRoundTrip`（16/32/48 三个尺寸的图标往返：位深、尺寸、颜色、透明区）、`TestBitmapFromImageWritesBGR`（标题栏 DIB 的通道序）、`TestApplyAppUserModelIDSetsExplicitID`（AUMID 真的设上了）。
  - **WebView2 事件回调挂在创建 controller 的 UI 线程上**：`add_NavigationStarting/SourceChanged/HistoryChanged/NavigationCompleted` 驱动地址栏文本、前进后退可用态、刷新/停止切换、favicon 拉取。handler 对象必须存进 `tabState.eventHandlers` —— COM 侧的引用 Go GC 看不见，不存就可能被回收（回调里 `AddRef/Release` 都返回 1，永不释放）。
  - **事件 vtable 槽位已从 `GetSettings` 扩展到 `Stop`（槽位 43）**，槽位号必须与 WebView2 ABI **逐条对齐**：`GetSource=4`、`Navigate=5`、**`NavigateToString=6`**、`add_NavigationStarting=7`、`add_SourceChanged=11`、`add_HistoryChanged=13`、`add_NavigationCompleted=15`、`ExecuteScript=29`、`Reload=31`、`get_CanGoBack=38`、`get_CanGoForward=39`、`GoBack=40`、`GoForward=41`、`GetDevToolsProtocolEventReceiver=42`、`Stop=43`。
    **千万别漏 `NavigateToString`（`Navigate` 之后那一个）** —— 漏掉它，其后所有槽位整体错位一格，而**编译、`go vet`、单测全都不报错**，症状极具迷惑性：`add_*` 实际打在 `remove_*` 上（返回 `S_OK` 却**永不回调**）、`ExecuteScript` 实际是 `RemoveScriptToExecuteOnDocumentCreated`（脚本根本没跑，favicon 永远不出现）、`get_CanGoBack` 实际是 `get_BrowserProcessId`（返回 PID → **恒为 true**）。核对基准现成可用：依赖里 `github.com/wailsapp/go-webview2` 的 `pkg/webview2/ICoreWebView2.go` 就是同一份 ABI 的生成物，逐行对齐即可。定位这类问题的铁律：**别信「返回 S_OK」，要信「事件到没到」**。
  - **别用「NULL 探针」自证槽位**：`add_NavigationStarting(this, NULL, &token)` 返回 `E_INVALIDARG` 看着像验证通过，但 `NavigateToString(this, NULL)` 同样返回 `E_INVALIDARG` —— 参数校验相似的方法互相冒充，结论完全反了。
  - **手工 COM 回调对象的 `QueryInterface` 统一走 `panelQueryInterfaceIUnknown`**（只承认 `IID_IUnknown`，其余 `E_NOINTERFACE`）。**不要写成「任何 IID 都返回 S_OK」**：万一运行时 QI 的是别的接口（如 `IMarshal`），它随后会按那个接口的槽位调我们的方法，而我们的 vtable 只有 `IUnknown + Invoke`，后面是野指针。也不必担心「不承认就收不到回调」——WebView2 这几个句柄回调都在本进程内被直接调用、不走跨进程封送（实测：controller 完成回调在「一律 E_NOINTERFACE」下照样收到）。
  - 回归用例分两层，**骨架用例抓不到链路故障**：
    - `TestE2EPanelWindowIsFramelessWithCustomTitleBar`：只查窗口骨架（客户区尺寸==窗口尺寸、三段顶部分界、地址栏 `EDIT` 存在），不比对像素。**用例自带面板与窗口尺寸**（`e2eAddFramelessPanel`）：它拿窗口宽度核对地址栏几何，而地址栏宽度是「窗口宽度减去两侧固定按钮」算出来的 —— 窗口一窄就合法地算成 0。若拿用户配置里的面板做样本，用户那条窗口状态就变成了用例的输入（2026-09-30 实测：那个面板的窗口状态被历史脏数据写成 x=-32000 / 160x28，用例报「地址栏 EDIT 尺寸异常 0x26」，看着像产品坏了）。
    - `TestE2ETitlebarFollowsRealNavigation`：查**事件与图标真的活过来了**。存在理由是骨架用例对 2026-09-30 那次 vtable 错位事故**完全无感**（窗口骨架一切正常）。三条断言都是刻意挑的「只有我方代码真的跑起来才可能成立」的观测量：
      1. 配置指向 `/start`、`/start` 用 302 跳到 `/final`，断言地址栏最终为 `/final` —— 地址栏文本由 `tabState.currentURL` 驱动，而 `currentURL` **在标签创建时就被配置 URL 种了值**，所以「配置 URL == 最终 URL」的写法是空的（地址栏从一开始就对）。必须让两者不同。
      2. `/icon.png` 只认 **`PanelDock/` 前缀的 User-Agent** —— **Chromium 自己会抓 favicon**，只断言「有人来取过」的话，即使 `ExecuteScript` 落在错误槽位上、脚本根本没执行，服务器照样收到请求。这条判别器在 `panelFetchBytes` 里，改那里请连带改用例。
      3. `WM_GETICON(ICON_BIG)` 非零 —— 守的是「图标装到窗口上」那一段（PNG/HICON/WM_SETICON 任一步断了都是 0）。`WM_GETICON` 只返回**显式设置过**的图标，系统给无图标窗口的 exe 兜底不走它，所以不会假阳性。
    - 新写的用例**必须做反向验证**：把修复撤掉重建、确认用例变红（第一版这两条断言就是空的，靠反向验证才发现）。
- 每个标签（PanelTab）使用独立 WebView2 Environment + UserDataFolder（`%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>`），登录会话互不干扰。
- 面板窗口不暴露 Wails 桥接（无 `window.external`、HostObject、WebMessage 处理）。
- 配置文件里没有任何密码字段。分组登录过的密码由 WebView2 自己加密后存在各标签的 profile 目录里（`Login Data` + `Local State` 的 DPAPI 主密钥），本工具不读取也不导出；`PanelConfig.PasswordAutosave` 只是「让不让 WebView2 保存」这个开关的值。仅支持 http/https 地址。
- 编辑面板保存走 `UpdatePanelTabs` 整体替换标签列表，不要用"UpdatePanel + 循环 AddTab"（会删不掉旧标签）。
- **删除标签 = 关那个标签 + 清它的数据**（2026-09-30 用户明确要求）：被移除标签的 WebView2 经 `panelWindow.removeTab`（私有消息 `win32WMRemoveTab` 投递回 UI 线程、同步等待）单独销毁，**其余标签的页面不动、窗口不重开**；然后 `clearTabProfile` 删它的 profile 目录（Cookie、登录态、已保存的密码一并清掉）。改清理失败时的取舍与 `DeletePanel` 不同：WebView2 销毁不可逆，配置**必须**照常更新（否则标签下次又回来、数据永远没人清），错误如实报给用户、由「重置数据」兜底。`UpdatePanelTabs` 只有**新增标签**时才 `restartPanel` —— 热创建一个新 WebView2 要走完整的 environment 异步链，不值得。

## 关键实现约束

- **`ICoreWebView2Settings4`（密码保存）必须对 Settings 对象 QI**：`get_Settings` 拿到的是 `ICoreWebView2Settings`，`QueryInterface(ICoreWebView2Settings4)` 要打在**它**身上；对 `ICoreWebView2` 直接 QI 必然失败（Settings4 是由 Settings 对象实现的）。槽位（IUnknown 0–2 之后，按官方 IDL 逐条数）：Settings1 3–20（18 项）、Settings2 21–22（UserAgent）、Settings3 23–24（AreBrowserAcceleratorKeysEnabled）、Settings4 25–28，其中 `put_IsPasswordAutosaveEnabled` 是 **26**。**依赖里 `go-webview2/pkg/webview2/ICoreWebView2Settings{,2,3,4}.go` 不能当基准**：它把每个 SettingsN 当成独立接口、只嵌 `IUnknownVtbl`，槽位与真实 ABI 对不上。`password_autosave_windows.go` 里按完整继承链手写的 vtable 已真机验证（写进去后 `get_` 读回是 1）。
- `panel_window_windows.go` 中手工声明的 COM vtable 字段顺序必须与 WebView2 ABI 一致，不能为了"简化"折叠 `IUnknown` 到目标方法之间的槽位，也**不能漏掉中间任何一个方法**（漏一个 = 其后全部错位，且编译/vet/单测都不报错）。ICoreWebView2 vtable 目前调用到 `Stop`（槽位 43，清单见「面板窗口是无边框窗口 + 自绘标题栏」一节）；改动必须对照 ABI，首选基准是依赖里 `go-webview2/pkg/webview2` 的生成物。
- 手写 Win32 结构体（`panelMSG`、`panelNOTIFYICONDATA`、`panelTRACKMOUSEEVENT`）已按 x64 ABI 核对过内存布局，改动需重新核对对齐。
- **Win32 API 所属 DLL 必须核对**：GDI 绘制函数（`SetBkMode`、`SetTextColor`、`SelectObject`、`CreateSolidBrush`、`CreateFontW`、`GetStockObject`）属 `gdi32.dll`；`FillRect`、`DrawTextW` 属 `user32.dll`。`syscall.NewLazyDLL` 惰性解析导致声错 DLL 在编译/vet/test 全不报错，首次绘制才 panic。新增 API 声明先查 MSDN 的 Header/DLL 标注。
- **Wails 的 `runtime.Quit` 会先询问 `OnBeforeClose`**：若该钩子返回 true（阻止关闭），退出请求会被整个吞掉（`Frontend.Quit` 直接 return）。轻量模式下必须先置 `App.quitting` 再调用退出（见 `requestQuit`），否则「关闭最后一个面板即退出」永远失效。
- **`go vet` 的 unsafeptr 检查**拒绝 `unsafe.Pointer(uintptr)` 转换。处理 `WM_COPYDATA` 时的两个合规写法：`panelCOPYDATASTRUCT.LpData` 直接声明为 `*uint16`（布局同 LPVOID），WndProc 的 `lParam` 声明为 `unsafe.Pointer`（`windows.NewCallback` 支持该参数类型）。
- **COM 初始化必须 `runtime.LockOSThread`**：COM 单元是「每线程」状态，若 goroutine 在 `CoInitializeEx` 与 `CoUninitialize` 之间迁移到别的线程，会留下「初始化在某线程、反初始化在另一线程」的错配——那根线程永久停在 STA，后续落到它的调用拿到 `S_FALSE` 而报 `CoInitializeEx: Incorrect function`（间歇性、难以复现）。`shortcutWithCOM` 已加锁线程。
- **`CoInitializeEx` 的 `S_FALSE` 要单独识别**：`golang.org/x/sys/windows` 的包装把任何非 0 HRESULT 都转成 `error`，`S_FALSE`(1) 于是变成 `ERROR_INVALID_FUNCTION`，与「未知失败」无法区分。`shortcutSFalse` 常量显式处理它（仍配对一次 `CoUninitialize`）。
- **`syscall.NewLazyDLL` 调用不存在的导出会 panic**：对 Windows 版本相关的 API（如 `GetDpiForWindow`，Win10 1607+）必须先 `proc.Find()` 探测再 `Call`，否则在老系统上直接崩。
- **询问框不解析 `CREATESTRUCT`**：`prompt_modal_windows.go` 在 `CreateWindowExW` 返回之后才创建子控件（而非 `WM_CREATE` 内），从而避免手工声明这个易错的大结构体；窗口过程在映射表里查不到状态时就交回 `DefWindowProcW`。
- **持久化窗口状态必须跳过最小化窗口**（`panelWindow.captureBounds`）：最小化时 `GetWindowRect` 返回的是 Windows 的哨兵矩形 `(-32000,-32000,160,28)`（「图标位置」），写进配置就等于「下次打开这个面板缩成一个小方块、还落在屏幕外」。2026-09-30 在用户便携配置里实测到过一次 —— 面板最小化后从托盘关闭（`dispose` 里那次记录）就写下了 x=-32000 / 160x28。两道防线：`IsIconic` 挡最小化；`plausibleWindowRect`（纯函数，`window_state_test.go` 钉着）再挡哨兵坐标与退化矩形。读配置时走 `initialWindowRect` 做同一道校验，退回 `defaultPanelWindowWidth/Height` —— **必须与 `config.create` 用同一对常量**，否则同一个面板会因为历史脏数据拿到与新建面板不同的尺寸。
- **新增询问框 = 加一份 `promptModalSpec`**，不要复制窗口类/消息循环：类名、窗口过程、字体、DPI、居中、`IsDialogMessageW` 导航全在 `prompt_modal_windows.go`。三件必须核对的事：① 按钮 ID 全局唯一（窗口过程按 ID 分发，撞车会让两个询问框的按钮互相串味，单测 `TestEnablePromptSpec` 就在守这条）；② 正文行数要和 `BodyHeight` 匹配（STATIC 不会自动长高，行多了会被裁掉）；③ `Caption` 全局唯一——端到端用例靠标题栏文案定位是哪一个框。窗口类**共用**，所以 `findWindowByClass` 只能回答「有没有询问框」，回答不了「是哪个」。
- 对话框的 Tab 导航 / 回车触发默认按钮 / Esc 取消交给 `IsDialogMessageW`；但它只应处理「对话框自身或其子窗口」的消息（父窗口与对话框同线程，用 `IsChild` 过滤）。
- **管理界面的面板卡片头部固定两行**（`main.js` 卡片模板 + `app.css` 的 `.panel-card-head` / `.panel-title-row` / `.panel-name` / `.panel-card-actions`）：第一行是 `.panel-title-row` —— 「面板名 + 启用 / 停用徽章」同行（2026-09-30 用户要求把徽章从原来的第二行移到名称旁边）；第二行是 `.panel-card-actions`（3 个勾选框 + 操作按钮，左对齐、按需折行）。名称用 `overflow-wrap: anywhere` 折行且 `min-width: 0` 允许收缩、徽章 `flex: none` 不被压变形，因此超长名称/URL 不会撑破卡片。**操作按钮不要塞回名称那一行**——名称一长就把按钮挤到下一行，同一种卡片在不同面板上高度与列位就不一致了。
- **界面不展示面板运行时状态**（2026-09-30 用户要求移除）：卡片上只留「启用 / 停用」徽章，右上角的桥接徽章（`bridge-badge` + `setBridge`）也一并删掉。随之删除 `App.PanelRuntimeStatus` / `App.PrototypeStatus` / `PanelRuntimeInfo` / `panelWindow.getActiveTabID`（`isVisible()` **保留** —— 托盘菜单的显示/隐藏切换要用）。卡片主按钮固定写「打开」（`title` 里说明「已经打开时会切到前台」），不再按运行状态在「打开 / 切换」之间变字：那需要主界面持续向后端要状态，而前端只有事件驱动、**没有轮询**（见上面那条「后端改设置要广播」）。**不要**为了让按钮文字更聪明把运行时状态查询加回来。错误反馈仍在列表区（`refresh()` 失败时把原因写进面板列表）。
- 面板窗口必须是手工 COM 子集实现：`pkg/webview2` 的自动生成回调在 Go 1.25 下会 `panic: compileCallback: argument size is larger than uintptr`。

## 构建与验证

- 本机工具链：Go 1.27 / Wails CLI v2.16 / Node v24 / UCRT64 gcc 16.2 / WebView2 Runtime 153。
- 经 Git Bash 启动时找不到 `npm`，构建请用 `cmd /c "wails build"`（受限环境可直接 `wails build`，CLI 在 `~/go/bin`）。
- 回归基线（全部必须通过）：`go vet ./...`、`go test ./...`、`wails build`。
- 运行时验证：`build/bin/PanelDock.exe --open <面板ID>`，用 `tasklist /V /FI "IMAGENAME eq PanelDock.exe"` 查窗口标题（应为 `PanelDock · <面板名>`），并检查 `%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>\EBWebView` 目录 mtime 是否被本次运行更新。GUI 进程会随工具调用的进程树被沙箱回收，属正常现象。
- 端到端验证（默认跳过，需 `PANELDOCK_E2E=1`）：`PANELDOCK_E2E=1 go test -run TestE2E -v .`
  - `TestE2ELightweightSingleInstanceAndQuit`：真实启动实例，`FindWindowW` 断言主窗口不可见/面板可见、二次启动转发后退出，最后发 `win32WMDirectClose` 验证进程干净退出。（「面板可见」必须用 `waitForCondition` 等一等：窗口对象在 `ShowWindow` 之前就已存在，按类名找到它 ≠ 已可见，直接断言会偶发失败。）
  - `TestE2EClosePromptPanelOnlyAffectsItself`：同一实例开两个面板（第二个实例 `--open` 转发），第一个点 X 先选「最小化到托盘」（断言该面板隐藏、另一个面板可见、进程存活），再点 X 选「**直接关闭**」——断言**只销毁被关的那个面板**、另一个面板仍可见、进程仍在；最后关掉剩余面板断言进程收工。**这是「关闭只影响本面板」的核心回归**。
  - `TestE2ECloseRememberIsPersisted`：「记住我的选择」落盘的回归。在面板窗口询问框里用 `BM_SETCHECK` 勾选「记住我的选择」再选「直接关闭」，等进程退出后读配置断言 `panelCloseAction=close`（跨进程改控件状态要按控件 ID `GetDlgItem`，用 `BM_SETCHECK` 而非 `BM_CLICK` 更确定）。
  - `TestE2EManagerCloseOnlyClosesItself`（核心回归）：普通启动 + 转发打开一个面板，管理窗口点 X（**不弹询问框**）——断言**分组标签窗口照旧可见、进程存活**、管理窗口消失，且**托盘图标仍在、`showTrayIcon` 仍为 true**（探针含 uid=99 反向自检）；最后关掉那个面板，断言托盘图标还开着时进程**留在托盘里**（不退出）。用例收尾要显式 `Kill` + `waitForNoInstance` —— 它结束时进程还活着，会占着单实例锁。
  - `TestE2ETrayIconRegistered` / `TestE2ETrayIconDisabled`：用 `Shell_NotifyIconW(NIM_MODIFY)` **探测托盘图标是否真的注册**（图标不存在时返回 FALSE；用例内含 uid=99 的反向自检，避免探针本身失效）。两者都**不带 `--open` 启动**——即一个面板都不开，验证托盘图标照样在（这是「无面板时最小化到托盘不会退化为退出」的前提）。探测目标是**常驻 IPC 窗口**（`findWindowByClass(ipcWindowClass)`）+ `trayIconUID`。
  - `TestE2EManagerCloseKeepsTrayWhenNoPanels` / `TestE2EManagerCloseQuitsWhenNoTrayAndNoPanels`：管理窗口固定关闭动作的两半。前者：零面板 + 托盘图标开着时点 X → 窗口隐藏、**进程存活**、图标仍在（收尾要显式 `Kill` + `waitForNoInstance`）；后者：零面板 + 托盘图标关着时点 X → 进程结束（此时它就是最后一个窗口）。
  - `TestE2ETrayIconForcedByMinimizeToTray`：把配置写成矛盾组合（`showTrayIcon=false` + `panelCloseAction=tray`）后启动，断言图标照样注册、且纠正结果被写回文件。`TestE2ETrayIconDisabled` 的前置条件必须把关闭行为摆成非 tray —— 否则上面那条不变量会让它必然失败。
  - `e2eSkipUnlessReady` 在「配置里没有启用面板」时**不再静默跳过**：它自己临时启用第一个面板（`e2eEnableFirstPanel`，`t.Cleanup` 按原字节还原）。跳过 ≠ 通过——本机配置里唯一的面板是停用的，实测整套用例会因此全体静默跳过而显示 ok。
  - `TestE2EFreshSessionClearedOnClose`：**「关闭后清空浏览器状态」的回归**。用例在便携配置里临时加一个 `sessionMode=fresh` 的测试面板（用完按原字节还原，全程只动这个临时面板的目录），并给它配一个用例自己起的本地 HTTP 服务（页面写 localStorage + 设一个名字带随机后缀的 Cookie）；先埋一份「上次没清干净」的残留并断言它在打开前被抹掉，再断言会话数据**真的写进了磁盘**（搜得到 token，否则 `t.Fatal`，避免清空断言形同虚设），最后主动关闭面板，断言这些 token 在 profile 目录里**再也搜不到**（并在 2 秒后复查一遍）。**不要**改成断言「目录不存在」—— 浏览器退出时会重建目录，见「会话状态」一节。
  - `TestE2EDisabledPanelAsksToEnable`：**停用面板的打开请求**的回归（轻量启动路径）。用例在便携配置里临时加一个 `enabled=false` 的面板，`--open` 启动后先断言**弹出「面板已停用」询问框**，然后分两轮验证：① 选「保持停用」→ 询问框关闭、面板窗口不出现、**管理窗口也不出现**、配置仍是停用（静默）；② 选「启用并打开」→ 面板窗口真的出现、`enabled` 已落盘。轮次之间必须 `Process.Kill()` + `waitForNoInstance(t)`：第一轮没有面板窗口，轻量进程只会留在托盘里，**它持有的单实例锁会把第二轮的 `--open` 转发过去**（那样第二轮永远等不到自己的询问框）。
  - `TestE2EForwardedOpenOnDisabledPanel`：同一件事的**转发路径**（真实场景：程序已在运行，双击快捷方式）——普通启动让管理窗口可见，再用第二个实例 `--open` 停用面板，断言询问框在**已在运行的实例**里弹出（这条覆盖 `enablePromptOwner()` 取到可见管理窗口的分支），选「启用并打开」后面板打开、管理窗口与进程都还在。
  - `TestE2EDirtyWindowStateFallsBackToDefault`：**窗口状态兜底的回归**（可反向验证）。把临时面板的 `window` 写成哨兵值（`x=-32000 / 160x28`）后启动，断言面板窗口真的开成默认尺寸、且**落回可见屏幕**（真去问 `GetWindowRect`，不是只比配置）。撤掉 `initialWindowRect` 里的校验它立刻变红。
  - `TestE2EMinimizedPanelKeepsLastWindowState`：最小化 → 主动关闭 → 读**配置里的值**，断言窗口状态没被写成哨兵矩形。断言前先确认「最小化确实让 `GetWindowRect` 变成哨兵矩形」这个前提成立（否则用例可能因为「窗口已销毁导致记录失败」而恒绿）。**诚实说明：这条目前无法反向验证** —— 把 `captureBounds` 的两道检查全撤掉它照样通过，因为 `dispose` 记录时窗口已销毁、`GetWindowRect` 失败、`lastRect` 保持构造值；留下它是为了钉住这条真实用户路径的现状（将来谁改了 dispose 时序让记录重新读到哨兵矩形，它会立刻变红）。
  - `TestE2ETabBarThemeButtonSwitchesPanelChrome`：**标签栏最右端配色按钮的回归**。往标签栏子窗口发 `WM_LBUTTONDOWN/UP`（**客户区坐标**），断言配置 `theme` 真的变、且**标签栏的实际像素**（`GetPixel`）跟着换 —— 只看配置的话，「按钮能改设置但窗口不重绘」照样通过。两个方向都断言。前置把 `theme` 钉成**显式 dark**（不用 auto，否则断言会变成「看这台机器当时什么色」）；期望色号从配色表算（`panelColorRef(panelChromeFor(...).bg)`），不另抄一份。只能读**自绘的 GDI 内容**，WebView2 的内容区是别的进程合成的、读不到。
  - 询问框依靠**标题栏文案**定位（`waitForClosePrompt(t)` 内部查 `closePromptLabels.Caption`；「面板已停用」询问框用 `waitForWindowByTitle(t, enablePromptCaption)`），窗口句柄靠 `waitForWindowByTitle(t, panelWindowTitle(name))`——多个面板同时打开时不能用「按类名找第一个」。所有询问框**共用窗口类**，所以「询问框还在不在」用 `findWindowByClass(promptWindowClassName)`。现在只有面板窗口会弹关闭询问框（管理窗口的关闭不询问），因此不再按角色区分。
  - 点击询问框按钮统一走 `clickPromptButton(t, hwnd, buttonID)`（跨进程 `WM_COMMAND` + `SendMessageW` 同步等待），关闭询问与停用询问都用它。
  - 用例通过 `e2ePatchSettings` / `e2eReadConfig` 读写便携配置并在结束时按原字节还原；检测到已有真实实例在运行时自动跳过（**串跑时会因此跳过若干用例，需要单独重跑**）。
  - **两个改配置的坑（务必遵守）**：
    1. 还原必须**无条件**：即使补丁没有任何实际改动也要写回原始字节。被测程序自己会写配置（把关闭行为设成「最小化到托盘」会连带打开托盘图标、勾「记住我的选择」会写入关闭行为），跳过还原就会把便携包改坏、让后续用例静默跳过（跳过 ≠ 通过，串跑时没人会发现）。
    2. 补丁要**清掉已废弃的字段**（旧 `closeAction` / `managerCloseAction`）：加载时的迁移/清理会重写它们，可能覆盖刚写入的值。
    3. 「关闭最后一个面板 → 进程退出」是用例的常见前提，它现在取决于 `lightweightQuitOnLastPanel`：需要这一前提的用例必须自己打开它（`e2eForceLightweightQuit`；顺带要「每次询问」的用 `e2eForceCloseActionAsk`），否则用户配置里关掉它那些用例全挂。
  - 跑完 E2E 建议 `diff` 一次 `build/bin/data/config.json`，确认用户配置没被改动。
- 桌面快捷方式集成测试（默认跳过）：`PANELDOCK_DESKTOP_TEST=1 go test -run TestCreateDesktopShortcutIntegration -v .`
- 快捷方式扫描的真实桌面验证（默认跳过、只读）：
  `PANELDOCK_DESKTOP_TEST=1 PANELDOCK_DESKTOP_PANEL_ID=<面板ID> go test -run TestListPanelShortcutsRealDesktop -v .`
  —— 会用真实桌面目录 + 注入的 `PanelDock.exe` 文件名扫描，打印会被判为该面板的 .lnk 清单。

## 运行时数据位置

- 便携模式（`<exe目录>\data\` 存在）：
  - 配置：`<exe目录>\data\config.json`
  - 会话/profile：`<exe目录>\data\WebViewProfiles\<tabID>`
  - 图标缓存：`<exe目录>\data\icons\<面板ID>.ico`
- 非便携模式：
  - 配置：`%APPDATA%\PanelDock\config.json`
  - 会话/profile：`%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>`
  - 图标缓存：`%APPDATA%\PanelDock\icons\<面板ID>.ico`
- 当前开发实例为便携模式：`build\bin\PanelDock.exe` + `build\bin\data\`（数据已从系统目录迁入）。`wails build` 的 `Clean Bin Dir` 为 false，data 不会被清掉。

## 命令行直达启动

- `PanelDock.exe --open <面板ID>`：**轻量启动**——主窗口隐藏，500ms 后自动打开指定面板（`main.go` 的 `parseAutoOpenArg` + `app.go` 的 `startup` 异步调用 `openPanelFromExternalRequest`）。面板不存在 / 没有标签时自动显示管理窗口，避免留下看不见的进程；**面板被停用时先询问是否启用**（见下节）。
- 已有实例在运行时，本次启动只做转发（见「关键设计决定·单实例调度」），不会叠出第二套窗口。
- 注意：`--open` 传的是**面板 ID**（panels[].id），不是标签 ID。**`--tag-id` / `--tag` 标签级直达已取消**（《方案讨论记录.md》已同步修订）：快捷方式从面板处创建，想要「单标签直达」建一个只含单标签的面板即可；命令面只保留 `--open` 一个参数。

## 停用面板的打开请求：询问是否启用

面板被停用后，桌面快捷方式与任务栏固定的图标**仍然指向它**（`--open <面板ID>`）。直接报错会让用户双击后看起来像没反应，分不清是被停用、程序没起来还是地址坏了 —— 因此改为弹一个原生询问框（`enable_prompt_windows.go`）：

- **启用并打开**（默认按钮，回车即选中）：`configStore.setEnabled(id, true)` 落盘启用，然后照常打开；
- **保持停用**：返回 `App.ErrPanelKeptDisabled`，两条外部入口都**静默收场** —— 不弹管理窗口、不报错。用户刚刚回答过那个问题，再补一个窗口弹出来等于没听他说话；
- **没有「记住我的选择」**：这是刻意的。把「启用」记成「以后自动启用」等于悄悄绕过用户的停用意图，停用功能本身会名存实亡。
- **入口只有外部请求**：`App.openPanelFromExternalRequest` = `ensurePanelEnabledForExternalOpen` + `OpenPanel`，两个调用点是 `startup` 的 `--open` 异步补开与 `handleIPCCommand`（转发命令）。**管理界面里的「打开」按钮不走这里** —— 停用卡片上那个按钮本来就是灰的；而 Wails 绑定方法**绝不能**弹原生模态框（会在 WebView2 消息处理线程上自建 `GetMessage` 循环）。因此 `App.OpenPanel` 保持纯粹的「按现有配置打开，停用则报错」。
- `App.OpenPanel` 仍是 Wails 桥接方法（前端在用）；`openPanelFromExternalRequest` **不要**加 `Bind` —— 它是给程序内部用的。
- `confirmEnableDisabledPanel` 是包级**注入缝**：单测把它换成假实现，就不必真的弹框、也不会卡在等人的点击上（同 `decideClose` 只测「已记住」分支的道理）。
- **连续两次请求不会叠出两个询问框**：`enablePromptMu` 把「是否启用」串行化，后到的请求等前一个做出选择后**重新读配置** —— 那时面板往往已经启用，于是直接打开，不再问第二遍。
- **询问框挂哪个 owner 要看可见性**：`enablePromptOwner()` 只在管理窗口**可见**时用它（居中 + 置灰形成模态）；轻量模式 / 窗口已藏进托盘时主窗口是隐藏的，而 `FindWindowW` 连隐藏窗口也能找到 —— 拿它当 owner 会让询问框居中到用户看不见的位置（甚至屏幕外）。这种情况返回 0，居中到主屏幕。
  - 代价要心里有数：转发路径（`handleIPCCommand` 在自己的 goroutine 里）拿到的是**别的线程**（Wails 主线程）的窗口，`EnableWindow(owner, FALSE)` 因此是一次**跨线程同步消息**，会等到对方处理 WM_ENABLE —— 实测询问框可能晚几百毫秒才显示出来（对方正在初始化 WebView2 时最明显）。不会挂死：对方线程若真卡住，程序本来就已失去响应。端到端用例对此有防护：**等询问框「可见」再去点**（`waitForVisiblePrompt`），否则会撞上「已创建但尚未显示」这一瞬。
- **面板名放进标题前要截断**（`enablePromptName`，16 个字符 + 省略号，按**字符**截而不是字节）：标题 STATIC 只有一行高度（26 单位），超长会被直接裁掉；按字节切会把中文切成乱码。
- 副作用要知道：选择「保持停用」时，轻量启动的进程**留在托盘里** —— 这是「静默」的字面结果，同时也是运行中 PanelDock 的常态；托盘图标（若开着）仍能找回管理界面。
- 面板启用状态是后端自己改的：改完必须 `notifyPanelsChanged()`（Wails 事件 `paneldock:panels-changed`），否则管理界面卡片上会一直挂着过期的「停用」标记（前端没有轮询）。

## 面板标签：增删与排序

- 不做独立的「单标签 / 分组」形态选择器：**标签数量自然决定形态**，1 个标签即单标签面板，多个即分组。编辑表单里就是一个标签列表，可增、删、改名、**排序**。原「单标签 / 分组」形态选项与原 `--tag-id` / `--tag` 方案一并取消（快捷方式一律从面板创建，要单标签直达就建一个只含单标签的面板）。
- 顺序即 `panels[].tabs[]` 的数组顺序，**没有也不需要有单独的排序字段**。`newPanelWindow` 顺序遍历 `cfg.Tabs` 建标签栏，所以「按排序打开」只是既有行为的直接结果 —— 前端排好序落盘即可，后端不需要为排序加任何代码。
- 前端两条排序通道（`frontend/src/main.js`：`moveTab` + 挂在 `#pf-tabs` 上的 `dragover`/`drop`）：拖拽把手、↑ ↓ 按钮。**只有把手是 `draggable`** —— 把整个 `.pf-tab-item` 设为 draggable 会让里面的输入框没法用鼠标框选文本。拖拽过程中**只画落点提示线、不重排 DOM**：重排会重建这些节点，浏览器会当场中止拖拽；真正的移动只发生在 `drop` 那一刻。落点判定挂在容器上（不是每一项上），这样标签之间的缝隙也是有效落点。
- **排序必须搬整个标签对象（含 `tab.id`）**：id 决定 WebView2 profile 目录 `WebViewProfiles\<tabID>`，换了 id 等于换了浏览器身份，登录会话全丢。`formTabs` 里存的就是带 id 的对象，`moveTab` 用 splice 搬它们，提交时 id 原样带回；排到第一个的标签也不会被 `UpdatePanel` 改名成面板名（`UpdatePanelTabs` 紧随其后覆盖回载荷里的名字）。
- **`configStore.updateTabs` 会按标签 ID 重映射 `DefaultTabIndex`**：该字段是「上次激活的标签」的**下标**，只在旧顺序里有意义。重排后同一个下标指向的是另一个标签（标签变少时甚至越界），不修正就会出现「顺序排好了，打开激活的却是别人」。做法是先记住旧默认标签的 ID，替换后在新顺序里找它的新下标；默认标签本身被删掉时才回落 0。增、删、改名、排序全都走这一个写入口，所以一处修正即覆盖全部路径。
- `updateTabs` 会把传入切片**复制**一份再存（`AddTab` 复用旧切片容量，不复制会让调用方后续写入污染已保存配置）。
- 单测（`tab_order_test.go`）：`TestReorderTabsKeepsDefaultTabByID`、`TestReorderTabsKeepsTabIdentity`（重排不得改动任何 `tab.id`）、`TestDeleteDefaultTabFallsBackToFirst`、`TestUpdatePanelTabsReorderThroughApp`（走前端真正调用的桥接方法，并验证配置不与调用方共享底层数组）。

## 会话状态：关闭后保留 / 清空

面板级开关 `panels[].sessionMode`：`persist`（默认，保留浏览器状态）/ `fresh`（关闭后清空，每次打开都是新环境）。

- **实现方式：整个 profile 目录删掉**（`session.go` 的 `removeProfileDir` → `os.RemoveAll`）。profile 目录（`WebViewProfiles\<tabID>`）就是这个标签的浏览器身份本身 —— Cookie、Local Storage、IndexedDB、Service Worker、缓存、站点权限授权全在里面，目录结构还随 WebView2 版本变。逐类挑文件清理既会漏（漏一样就等于登录态还在）又要跟着上游改，**不要**改成那种实现。
- **两种删除策略**（`sessionClearPolicy`），因为两个挂接点面对的处境完全不同：
  - `clearOnce`：删掉即走。用于**打开面板前**的兜底 —— 那一刻该 profile 还没有任何浏览器进程，不存在「删了又被重建」的问题，不必白等静默期。
  - `clearUntilStable`：删到目录**连续 `profileClearQuietChecks`(3) 次检查都不存在**为止（每次间隔 `profileClearDelay` 250ms，即 500ms 静默期）。用于**关闭面板后**：浏览器进程还在退出，会把目录重建回来（下面「实测」一条），只删一次等于没清干净 —— 而「不留残留」正是这个功能的全部价值。确认之间必须真的等待，否则连续两次瞬时检查什么也证明不了。
- **两条挂接点，缺一不可**：
  - **关闭后清空**：`panelWindow.dispose()` 里**同步**执行（在 `doneOnce` 之前），用 `clearUntilStable`。为什么不能丢给 goroutine：① `restartPanel` 是「关掉 → 等 `done` → 立刻重开」，异步清理会与新会话抢同一个目录；② 关闭最后一个面板时进程 200ms 后就退出，异步清理很可能被砍掉。窗口此刻已销毁，静默期+重试的几百毫秒用户看不到。
  - **打开前兜底**：`newPanelWindow` 走 `preparePanelProfile(tabID, fresh)`，fresh 时**先清空（`clearOnce`）再 MkdirAll**。这是「每次新开都是新环境」的**保证**所在：上次崩溃 / 强杀 / 关机没清干净的残留在这里被补掉。清空失败时**让本次打开失败并报错** —— 宁可让用户看到「面板打不开 + 原因」，也不能悄悄带着上次的登录会话打开，那正是这个开关要避免的事。
- **删除后目录会被重建（E2E 实测）**：`os.RemoveAll` 成功之后，目录会**又冒出来**，带着一整棵空的 `EBWebView` 树（Preferences / History / Login Data… 都是新初始化的）。那是尚未退干净的浏览器进程在退出时重建的，不是会话残留。两条规则：① 只删一次不可靠，必须删到稳定；② **别用「目录是否存在」当判据** —— 目录在不在是时序问题，「会话数据在不在」才是承诺本身（E2E 因此改为在 profile 目录里**搜本次会话真实写下**的 Cookie 名与 localStorage token）。
- **删除面板时无条件清数据**（2026-09-30 用户明确要求，`App.DeletePanel`）：所有标签的 profile 目录整棵删掉，**不再看面板的 `sessionMode`**（历史实现只对 `fresh` 面板清、默认面板保留，已废弃 —— 想「只清数据、保留面板」是 `ResetPanelData` 的职责）。理由：分组都没了，那些目录只有它自己的标签 ID 能访问，留着只是白占空间、还把已保存的密码继续留在盘上。
- **删除单个标签同样清数据**（`App.DeleteTab` / `App.UpdatePanelTabs` 的差集路径）：同一个理由 —— 标签的 ID 没了，它的 profile 目录就成了谁也访问不到的孤儿。历史实现只改配置不删目录，是真实泄漏（见 `tab_delete_test.go`）。
  - 用 `clearUntilStable` 而非 `clearOnce`：面板没打开时 `close/wait` 不发生、dispose 的清空也没跑过；打开过的话 dispose 刚清过，这次是空操作 —— 但一律走稳定确认，省得区分两种情况（此刻窗口已销毁，这段等待用户看不到）。
  - 顺序是**关窗口 → 清数据 → 删快捷方式 → 删配置**：清数据失败就**整件事中止、面板保留**，绝不允许「面板没了、数据还在盘上」这种半完成状态（那正是用户以为清干净了的情形）；快捷方式清单必须在配置删除前读（`listPanelShortcuts` 依赖配置里记录的 .lnk 路径）。
  - 单测 `TestDeletePanelClearsBrowserData`（默认 persist 面板也必须清干净 —— 最容易的回归就是把 `clearsSessionOnClose()` 分支写回来）、`TestDeletePanelKeepsPanelWhenDataCannotBeCleared`（注入 `profileRemove` 失败，断言报错且面板保留）。
  - 前端 `askDeletePanel`：数据被清这条**单独一行红字加粗**（`#dd-warning` / `.confirm-warning`），不许混进 `.confirm-message` 的普通说明里 —— 它不可撤销。脚注只讲两件次要的事：想「只清数据、保留面板」该走「重置数据」入口、任务栏固定项不归本工具管。卡片上的「删除」按钮 title 同样要写明会连数据一起删。
- **配置层不做加载期迁移**：`sessionMode` 缺字段 / 空串 / 非法值一律按 `persist` 处理（`normalizeSessionMode`），且 `load()` **不写回** —— 否则每次启动都给便携配置添上这个字段，「跑完 E2E 配置零污染」这条检查就废了（对比 `closeAction` 迁移是必须写回的，两者取舍不同）。判断一律用 `PanelConfig.clearsSessionOnClose()`，不要自己比较字符串。
- `list()` / `get()` 在副本上归一化后返回，前端拿到的永远是 `persist` / `fresh` 之一，不需要自己再猜默认值（归一化不动磁盘上的原始取值）。
- `panelWindow.clearOnClose` 是**构造时快照**，不在关闭时回查配置：否则「打开时是保留、关闭前被改成清空」会让同一次关闭的后果不可预测。
- 前端两处入口：编辑表单的「关闭后的浏览器状态」单选组（`input[name="pf-session"]`）+ 面板卡片的「关闭即清空」勾选框。**`submitForm` 里 `SetPanelSessionMode` 必须排在 `UpdatePanelTabs` 之前** —— 后者会重开正在运行的面板，重开时读的是那一刻的配置，顺序反了新窗口就带着旧的处理方式。
- 副作用（要在文案里讲清）：清空后这些页面需要重新登录，页面缓存与站点偏好也没了。UI 文案与卡片 title 都写明了。
- 单测（`session_test.go` / `session_windows_test.go`）：默认 persist、老配置读出来是 persist 且**文件字节不变**、设置往返与非法值拒收、清理只删目标面板的目录、`preparePanelProfile` 在 fresh 下抹残留 / 在 persist 下原样保留、缺失目录视为成功、`removeProfileDir` 两种策略（删到稳定 / 删一次即走 / 用满预算后如实报错 / 确认之间真的有等待）、`newPanelWindow` 按配置置位并清残留。删除动作与「是否已消失」是包级注入缝（`profileRemove` / `profileGone`），用来编排「删掉 → 又被建回来 → 再删掉 → 稳定」的时序（真实的文件占用制造不出来：Go 打开文件默认带 `FILE_SHARE_DELETE`，删得掉）。
- 端到端：`TestE2EFreshSessionClearedOnClose` —— 用例**自己在便携配置里造一个 fresh 测试面板**（不碰用户真实面板：配置能按字节还原，会话目录还原不了），并给它配一个用例自己起的本地 HTTP 服务（页面写 localStorage、响应头设一个名字带随机后缀的 Cookie）。三步断言：① 打开前埋的残留被抹掉（打开前兜底）；② 页面真的把会话数据写到了磁盘（搜得到 token，否则直接 `t.Fatal` —— 不然后面的清空断言形同虚设）；③ 关闭面板后**再也搜不到**这些 token，且等 2 秒后再查一遍（浏览器进程是陆续退出的，一次「没搜到」可能只是它还没写完）。用例还注入 `resolvePortableRoot` 指向便携包 —— 测试二进制的 exe 在临时目录里，不注入就会算到 `%LOCALAPPDATA%`，断言全部落空。

## 保存登录密码（分组级，默认开启）

面板级开关 `panels[].passwordAutosave`：`on`（默认）/ `off`。开启时该分组的每个标签在 WebView2 创建后把 `IsPasswordAutosaveEnabled` 设为 true —— 登录页于是会像 Edge 一样弹「保存密码」提示，下次打开自动回填。用的是 WebView2 自带的密码管理（即 Edge 那套），**本工具不读取、不导出任何密码**。

- 实现：`password_autosave_windows.go` 的 `applyPasswordAutosave`。调用点在 `panelWindow.controllerCompleted`，**必须在 `navigate` 之前**（登录页首屏加载时开关要已经生效，否则第一次不触发保存提示）。QI 失败（1.0.1108 之前的运行时没有这个接口）静默跳过，不让面板打不开、也不弹用户看不懂的错。
- **`IsPasswordAutosaveEnabled` 只管「保存」**（官方 `specs/Autofill.md` 明写）：设成 false 只是不再保存新密码、不再弹保存提示，**此前已经存下来的密码仍然会被建议与回填**。所以 UI 文案一律写「不再保存新密码」，**不要**写成「关闭密码功能」；想彻底不留密码只能靠会话状态的 `fresh`（关闭即删整个 profile 目录，密码随之消失）。
- 默认开启又必须容忍「键缺失」，所以它是**字符串枚举**（`on` / `off`，空串 = 默认）而不是 bool —— 与 `sessionMode` 同一套路。用 bool 会让老配置读成 false，等于把所有老用户的分组悄悄关掉（和 `lightweightQuitOnLastPanel` 那次同一个坑，但这里连 `settingsHasKey` 都借不上力：面板是数组，逐项回到原始 JSON 查键不划算）。
- **当场生效**：`App.SetPanelPasswordAutosave` → `panelWindow.setPasswordAutosave` 立刻重设到该窗口所有已创建的标签。与会话处理方式的「只写配置、下次关闭时才起作用」刻意不同 —— 这就是一个 WebView2 属性，改完不生效用户只会以为开关坏了。重设是就地改属性，不重开窗口、不动用户当前页面。
- `panelWindow.passwordAutosave` 既是构造时快照、也可被改写；`setPasswordAutosave` 里 **COM 调用放在 `p.mu` 之外**（与 `setAlwaysOnTop` 同一考虑：拿着锁去调外部对象，对方回调进本窗口就自锁）。
- 前端两处入口：编辑表单「账号密码」区块的勾选框（`#pf-password`，**新建默认勾选**，老配置按开启渲染）+ 面板卡片的「保存密码」勾选框（`data-action="password"`）。`submitForm` 里 `SetPanelPasswordAutosave` 必须排在 `UpdatePanelTabs` **之前**（后者会重开正在运行的面板，重开时读的是那一刻的配置）；新建时后端 `create()` 已写默认值，只有用户取消勾选才多写一次。
- 单测：`TestPanelPasswordAutosaveDefaults`（默认与归一化）、`TestPanelPasswordAutosavePersist`（往返 + 非法值拒收 + 面板不存在）、`TestPanelPasswordAutosaveLegacyConfig`（老配置读出来是开启）、`TestNewPanelWindowAppliesPasswordAutosave`（窗口构造带上快照）。最后一条**已做反向验证**：把构造函数改成 `passwordAutosave: false`，三条「应为 true」立刻变红，改回即绿。
- 它与「关闭即清空」是同一条链路的两端，文案必须成对讲清：开了密码保存的分组若同时是 `fresh`，一关闭密码就跟着 profile 一起被清掉（那正是「彻底不留密码」的办法）。

## 界面配色（明暗主题）

管理窗口与面板窗口都支持浅色 / 深色两套配色，**同一个设置项** `settings.theme`：`auto`（默认，跟随 Windows 深浅色）/ `light` / `dark`。**两个入口写同一个字段**：管理窗口右上角 ◐ 按钮，以及**面板窗口标签栏最右端的配色按钮**（位置正好在标题栏「关闭」的正下方）；「应用设置 · 常规」的「界面配色」下拉也是它。两处都是「切到哪边就固定成哪边」，不会停在 `auto`。

**配色只管外壳，不管页面**：面板里显示的是别人的页面，本程序不向远程页面注入样式或脚本（项目硬不变量），所以页面深浅由站点自己决定。面板侧能跟着走的只有两块：GDI 外壳的颜色，以及 WebView2 的默认底色。

- **面板窗口的外壳配色在 `theme_panel_windows.go`**（`panelChrome` 表 + 明暗两套）。面板外壳全是 GDI 画的，用不上管理窗口那套 CSS token，所以另给一份**语义同名**的颜色表（`bg` / `text` / `hover` / `pressed` / `activeTab` / `field` / `accent`…），两边对齐才不会出现「管理窗口偏蓝、面板窗口偏灰」。

- **CSS 全部走语义 token**（`style.css`）：明暗两套映射挂在 `<html data-theme>`（约 40 个 token），`app.css` 里**不允许再写裸色值** —— 新增颜色先在 `style.css` 给两个主题各配一份。`color-scheme: light/dark` 必须跟着 `data-theme` 一起设，否则原生 `<select>`、滚动条、`<dialog>` 的 `::backdrop` 在深色下仍是浅色样式，一眼穿帮。
- **`data-theme` 挂 `<html>` 而不是 `#app`**：`main.js` 是整块 `#app.innerHTML` 重渲染的，挂在它身上或里面都会被下一次 render 抹掉；`<html>` 永远不被波及。
- **窗口原生底色必须联动，否则启动白闪**（`theme_windows.go`）：`options.App.BackgroundColour` 在 WebView 首帧之前、窗口缩放空隙里露出，深色主题下若仍是创建时的浅色就是一记白闪。三道防线：`main.go` 在 `wails.Run` **之前**读配置算出初始底色（startup 里再改已经晚了）；`startup` 里 `syncWindowTheme()` 再对齐一次（幂等）；前端每次 `applyTheme` 调 `App.ApplyWindowTheme` 同步（auto 模式下系统切换深浅色时，只有前端自己知道要换）。**两侧 `--bg` token 与 `windowBackgroundLight/Dark` 必须同值**，对不上的症状是窗口边缘一圈异色。
- **防闪脚本在 `index.html` <head>**：Wails 绑定首帧前不可用，拿不到后端配置，所以用 localStorage 镜像「设置值」（键 `paneldock.theme`，每次 `applyTheme` 同步写入；只存三态设置，不存生效值），head 内联脚本在 CSS 生效前解析出 `data-theme`。**config.json 才是事实源**，localStorage 只是读透缓存：`applySettings` 每次都以后端归一化值重放 `applyTheme`（幂等），两者永远只差一个启动瞬间，缓存损坏的代价是首帧跟一次系统偏好。
- **`SetTheme` 不广播 settings-changed**（与其他 setter 的刻意差异）：前端那个入口 await 成功后立刻 `applyTheme`，广播只会多一次无意义重读。但它**要**把生效配色推给已打开的面板窗口（`applyThemeToPanels`），否则管理窗口切了、面板窗口还是老配色。窗口原生底色联动走独立的 `ApplyWindowTheme`（只认 light/dark、不写配置），因为 auto 模式下系统深浅色切换不经过后端 —— `ApplyWindowTheme` 同样会推面板。
- **反过来，面板窗口那个按钮必须广播**（`App.togglePanelTheme`）：这次是面板侧改的设置，管理界面开着的话得知道，否则它会一直按旧配色渲染。这就是「后端自行改设置要广播」那条规矩的又一例。
- **auto 跟系统靠两条同源的判定**：后端 `systemThemePreference`（可注入，单测别依赖跑测试这台机器的深浅色设置）读注册表 `HKCU\...\Themes\Personalize\AppsUseLightTheme`；前端监听 `matchMedia('(prefers-color-scheme: dark)')` 的 change 事件即时跟进（事件驱动，无轮询）。显式 light/dark 压过系统偏好 —— 这正是「跟随系统」与「固定」的差别。
- 配置层不做加载期迁移：`normalizeTheme` 把空串 / 非法值归一到 `auto`，`load()` **不写回**，否则每次启动都给便携配置添字段，「跑完 E2E 配置零污染」就废了（同 `sessionMode`，与 `closeAction` 那种必须写回的迁移对比）。`settingsLocked()` 归一化后返回，前端永远拿到三值之一。
- 单测（`theme_test.go`）：取值规整（大小写敏感，不认的写法按默认处理不硬猜）、桥接往返 + 非法值拒收、**老配置读出 auto 且文件字节不变**（注意：测试夹具里 settings 节点要写全其余默认开启的键，缺 `lightweightQuitOnLastPanel` 会触发无关的 settingsHasKey 迁移、让失败信息失真到 theme 头上）、生效主题解析、`ApplyWindowTheme` 拒收非法值、初始窗口底色跟随生效主题。

### 面板窗口侧（`theme_panel_windows.go`）

- **颜色一律写 `0xRRGGBB`，要 COLORREF 时过 `panelColorRef`**。别再写成 `0x00FAA560 // #60a5fa` 那种必须靠注释才读得懂的常量 —— BGR 与 RGB 写反了编译、vet、单测全绿，只有肉眼看得出来，这是这套代码里最容易抄错的地方。`TestPanelColorRefConvertsRGBToBGR` 钉住换位（已做反向验证）。
- **窗口类的背景刷是注册那一刻定死的，改不了**，所以三个窗口过程都要自己处理 `WM_ERASEBKGND`（用当前 chrome 的 `brBg` 填），窗口类里那个 `Background:` 只是「还没收到主题之前」的兜底（取 `panelDefaultChrome()` = 深色，与既有观感一致，用哪套都不影响最终观感）。
- **`paintTabBar` / `paintTitleBar` / `WM_CTLCOLOREDIT` 一律从 `p.chrome()` 取色**，不许再出现裸色值。新增颜色先加到 `panelChrome`。
- **标签栏与标题栏的绘制范围必须同源**：`panelTabBarTabLimit(width)` 同时被绘制（`paintTabBar` 的 `visible`）和命中测试（`tabIndexAt`）使用，两边算得不一样就是「看得见点不到」或「点得到看不见」。它由 `panelTabBarThemeRect` 反推 —— 标签只画到配色按钮之前。`TestPanelTabBarTabLimitNeverOverlapsThemeButton` 扫描宽度 120→2200 守住（已做反向验证：`+1` 改成 `+2` 立即大量报红）。
- **配色按钮的命中矩形只有一份实现**（`panelTabBarThemeRect`），E2E 也用它算坐标（`clickTabBarTheme`），不各写一份。它贴右缘内边距、垂直居中，正好落在标题栏「关闭」按钮正下方。字形表达**当前状态**：太阳（浅色）/ 月亮（深色）。
- **换主题是跨线程的**：调用方可能是任意 goroutine（管理面板改设置、系统深浅色变了），而重绘只能发生在窗口自己的 UI 线程，所以 `requestTheme` 只挂 `pendingTheme` + `PostMessage(win32WMSetTheme)`，真正干活的是 UI 线程上的 `applyPendingTheme`。配色没变就直接返回 —— auto 模式下 `WM_SETTINGCHANGE` 会因为别的原因频繁到来。
- **`WM_SETTINGCHANGE` 要过滤**：lParam 指向变了的那一项设置名，只有 `"ImmersiveColorSet"` 与主题有关（`isImmersiveColorSet`）。区域、字体、辅助功能都会发同一条消息，不过滤就会跟着瞎重绘。
- **WebView2 的默认底色**（`ICoreWebView2Controller2::put_DefaultBackgroundColor`）：页面自己没铺底的地方、以及首帧画出来之前露出的空白都吃它，深色下不设它每次开面板/切页都会先闪一记白。**所有标签都要设一遍**，不只是当前那个（其余标签随后被切出来时同样会先露底）。`COREWEBVIEW2_COLOR` 是 `{A,R,G,B}` 的 4 字节结构体，**按值**传（压成 uintptr，不能传指针）。拿不到 Controller2（运行时太老）时静默跳过 —— 这只是观感，不值得报错打扰用户。
  - `panelController2Vtbl` 的槽位：`GetDefaultBackgroundColor`=26、`PutDefaultBackgroundColor`=27，**26/27 是这张表的末尾**。多写或少写一个方法就会越界取到野指针，加方法前先核基线（`ICoreWebView2Controller` 的顺序已与 `wailsapp/go-webview2` 逐条核对）。
- **单测（`theme_panel_windows_test.go`，14 项）**：`panelColorRef` 换位与往返、深色表保持既有观感（改动配色会绊住这条）、`panelChromeFor` 解析、画刷确实建出来了、配色按钮矩形贴右缘、标签上限不压按钮（扫描全宽度区间）、命中与矩形同源、`panelTheme` 兜底深色、无窗口时 `requestTheme` 不 panic、`applyPendingTheme` 配色没变就不重绘、`togglePanelTheme` 切换并落盘、`isImmersiveColorSet` 过滤别的设置项、`SetTheme` 推给已打开面板。注意 `app.panels` 是 `map[string]*panelWindow`。
- **E2E（`TestE2ETabBarThemeButtonSwitchesPanelChrome`）**：真机点那个按钮，读**标签栏的实际像素**（`GetPixel`）而不只看配置 —— 按钮能改设置而窗口不重绘照样会把设置写对、界面不动。两个方向都断言（切过去、切回来）。用例把设置钉成**显式 dark**，不用 auto：auto 跟随本机系统深浅色，断言会变成「看这台机器当时什么色」。期望色号从配色表算出来（`panelColorRef(panelChromeFor(...).bg)`），不另抄一份 —— 调整色号是观感，不该让用例变红。
  - 采样点是「第一个标签右侧、配色按钮左侧」那片纯底色（标签只占 `[tabBarPadding, tabBarPadding+tabBarTabWidth]`，按钮贴右缘）。
  - 只能读**自绘的 GDI 内容**：WebView2 的内容区是另一个进程合成的，`GetPixel` 读不到，别拿它去断言页面。
- **动手前先看的真机验证法**（本机无自动化框架时）：Python + ctypes 从窗口 DC `BitBlt` 一小条出来存 PNG（`.workbuddy/tmp/shot_panel.py` 是当时的临时工具），配合 `SendMessageW(bar, WM_LBUTTONDOWN, ..., (y<<16)|x)` 模拟点击。**坐标必须是客户区坐标** —— 误传屏幕坐标时点击静默失效，`SendMessage` 照样返回 1，看着像成功（踩过）。

## 界面语言（i18n）

`settings.language`：`auto`（默认，跟随 Windows 显示语言）/ `zh-CN` / `en-US`，字符串枚举 + 空串即 auto（与 `theme` 同一套论证，不需要 settingsHasKey）。**词典编译进程序，没有外挂语言文件** —— 桌面工具没有「不发版加语言」的需求，外挂只多一个文件丢失/版本不齐的失败路径。加第三种语言 = 再写一份词典 + 重新编译。

**翻译范围（与用户商定的边界）**：管理窗口的 web UI、后端返回的用户可见错误、以及本程序**自己画的**原生界面（关闭/启用询问框、托盘菜单、托盘悬浮提示、面板关闭询问的补充说明）。**不翻译**：WebView2 右键菜单、系统输入框等系统组件（跟随 Windows 显示语言，微软的事）；面板窗口的标题栏/标签栏本来就没有文字（纯图标按钮）。

- **两份词典、刻意不共享**：前端（`frontend/src/i18n/*.js`，vite 打包进产物）与 Win32 侧（`native_text_windows.go` 的 `nativeUITexts` map）。两边文案集合几乎不相交，共享一份源文件需要引入生成步骤，复杂度远超省下的那几条重复串。zh-CN 都是源语言：前端缺 key 回落 zh-CN 再露 key 本身；Go 侧 `nativeText()` 取不到语言回落 zh-CN。
- **前端切换 = 落盘 + 镜像 + `location.reload()`**：事件监听是 innerHTML 渲染后一次性绑死的，「原地换语言」等于要求整个渲染流程可重入；reload 干净可靠。`SetLanguage` 因此**不广播** settings-changed（与 `SetTheme` 同一论证：改设置的唯一入口就是发起方自己）。
- **首帧语言靠 localStorage 镜像**（键 `paneldock.language`）：`main.js` 的模板在模块加载时就渲染，Wails 绑定（`GetSettings`）那时不可用 —— 不镜像的话英文用户每次启动先看到一屏中文再闪成英文（与主题防闪同一套路）。`applySettings` 每次以配置为准重放镜像；配置与镜像解析出的语言不一致（手改 config.json）就 reload 一次对齐。
- **原生侧不缓存、每次现取**（`App.nativeText()` → 现读配置 → `resolveLanguage`）：托盘菜单本来就是每次右键现建的，询问框是每次弹时构造的 —— 语言切完，下一个弹出的框、下一次右键托盘就是新语言，**不需要任何推送机制**。代价是每次读一次内存里的配置，纳秒级。
- **auto 的判定两侧同语义**：Go 侧 `systemLanguage()`（`GetUserDefaultUILanguage`，LANGID 主语言 0x04=中文 → zh-CN，其余 en-US）；前端 `resolveLocale`（`navigator.language` zh 开头 → zh-CN）。两处都**不硬猜前缀/大小写**：`zh`、`ZH-CN`、`fr-FR` 一律按 auto/拒绝处理。
- **后端错误码化**（`apperror.go`）：到达用户的错误返回 `"panel.nameRequired"` 这类码（带技术细节时为 `"码: 细节"`），前端 `tErr` 按码查词典、查不到原样显示（内部技术错误、os 错误宁露细节不硬编）。哨兵错误用 `errCodeWrap` 包装，`errors.Is(err, ErrTrayIconRequired)` 仍成立。**改码名 = 改前端词典键（`err.` + 码），两边必须同步**；`PanelIconPreview.Reason` 也走同一套（`icon.reason.*`，四种「刷不了图标」的业务结果）。
- **面板名、标签名是用户数据，永远不翻译**；词典只管 UI chrome。
- 单测：`native_text_windows_test.go`（词典完整性 —— map 字面量编译器帮不上忙，少填一个键运行时才以「菜单少一项」暴露；`normalizeLanguage`；`SetLanguage` 往返）。E2E 在 `e2ePatchSettings` 里把语言钉死 zh-CN —— 用例按中文标题找询问框窗口，不钉的话英文 Windows 上 auto 解析成 en-US、全部找不到。
- **本机坑**：中文路径下 `wails build` 的 bindings 生成会静默失败（日志照说 Done、`frontend/wailsjs` 文件不更新）—— 新增桥接方法后要核对 `App.js` 里真的有它，没有就手工补（格式机械：`window['go']['main']['App']['方法名']`），运行时按方法名动态解析、绑定的真身在 `main.go`。

## 重置分组数据（卡片上的「重置数据」）

`App.ResetPanelData(id)`：立刻删掉该面板**所有**标签的 profile 目录 —— 对「关闭后清空（`fresh`）」的即时补充，不用等关闭、也不改任何设置。UI 入口只有面板卡片的「重置数据」（`.warn`，警示色；刻意不用删除那个红 —— 会丢数据但面板还在，两者不该看起来一样）。

与「删除面板」的分工（文案与文档都要讲清，否则用户会拿删除代替重置）：**重置保留面板、只清数据**；**删除连面板带数据一起清**（`App.DeletePanel`，无条件）。

- **先关窗口再清**：面板在运行表里就先 `close()` + `wait()`（真实实现在 `resetPanelCloser` 注入缝后面）。顺序反了**不会报错**只会清不干净：目录被浏览器进程占着，`RemoveAll` 只删掉一部分，剩下的登录态照旧留在盘上，而用户看到的是「重置成功」。这条顺序由 `TestResetPanelDataClosesRunningPanelFirst` 守着（已做反向验证：把清空挪到关闭之前，用例立刻报红）。
- **用 `clearUntilStable` 而不是 `clearOnce`**：刚退出的浏览器进程会在几百毫秒里把目录重建回来（同 `dispose`，E2E 实测过）。此刻窗口已销毁，重试预算的开销用户看不到。
- **不自动重开**：重置的语义是「抹掉数据」，重新访问由用户自己点「打开」。这也避开一个坑 —— 立刻重开会让新会话与清空动作抢同一个目录（`restartPanel` 之所以能这么做，是因为它等的是 `dispose` 里同步完成的清理）。
- **只动数据，不动配置**：不写配置、不删面板、不改快捷方式与窗口状态。单测直接比对配置文件**字节**未变（`TestResetPanelDataClearsAllTabProfiles`）。
- 清空失败**如实报错**（提示可能有浏览器进程占着、稍后重试），不假装成功 —— 静默失败会让用户以为「已经清干净了」。
- 前端确认对话框 `#reset-dialog`（`askResetPanel`）必须同时讲清两件事：**含 WebView2 保存的登录密码**（否则用户以为只是清缓存）和**配置不受影响**（否则以为把面板删了）；面板正开着时还要说明会先关掉它的窗口。默认聚焦「取消」（数据清掉回不来，回车不该直接执行）。
- 单测 `reset_panel_test.go`：多标签全清、配置字节不变、未知面板报错、先关后清的顺序。测试 App 用 `useTempPortableRoot`（它顺带把 `profileClearDelay` 压到 1ms，清理重试预算才不会让测试白等）。

## 进程寿命：什么时候才真正退出（重要）

判定只有一处：`App.quitWhenNoWindows(remainingPanels)`（`shouldQuitAfterPanelClosed` 传运行表长度，`panelCloseEndsProcess` 传 0 预测「关掉这个面板之后」）。顺序：
`quitting` → false；有分组标签窗口 → false；管理窗口还在（`mainShown`）→ false；管理窗口藏在托盘里（`resident`）→ false；**轻量模式 + 「用完即走」开着 → true**；否则看托盘 —— 托盘图标还开着就留在托盘，关掉了才退。

核心规则只有一条，两类窗口一致：**还有窗口（管理窗口 / 分组标签窗口）或托盘里还有图标，就不退出整个程序**；两者都没有才收工。由此推出：

- 关闭面板窗口**只影响那个面板**（`p.close()`），进程去留交给上面这条判定；
- 关闭管理窗口**只关它自己**（藏起来），分组标签与托盘一律不动；
- 轻量模式（`--open` 启动、管理窗口从未打开）默认「用完即走」——关掉最后一个面板就退出，不为托盘图标多留一个进程；这一项可以在设置里关掉（`lightweightQuitOnLastPanel`），关掉后轻量实例与常规实例行为完全一致。

| 场景 | 行为 |
|---|---|
| `--open` 启动 + 关闭最后一个面板（主动关闭路径） | 进程退出（轻量模式「用完即走」，默认开启） |
| `--open` 启动，但「用完即走」被关掉 → 关掉最后一个面板 | **不退出**：托盘图标还开着，留在托盘里（与常规启动一致） |
| 轻量模式 + 经托盘/转发呼出管理窗口后再关面板 | **不退出**，因为管理窗口还开着（`mainShown`） |
| 点面板 X（`panelCloseAction=ask`） | 弹**关闭询问框**：最小化到托盘 / 直接关闭 / 取消 |
| 在询问框勾选「记住我的选择」 | 写回 `panelCloseAction`，以后关闭面板窗口不再询问 |
| 面板 X 选「最小化到托盘」 | 该面板隐藏、其他面板与管理窗口不受影响、进程继续，托盘图标保证存在 |
| 面板 X 选「**直接关闭**」 | **只关闭该面板**（`p.close()`）。其他窗口/托盘还在就继续运行；都没有才走 `quitWhenNoWindows` 退出 |
| 点管理窗口 X（**不弹框**，固定动作） | 还有分组标签窗口或托盘图标还开着 → **只关自己**：窗口藏起来、分组标签与托盘设置一字不改、进程继续 |
| 点管理窗口 X，且一个分组标签都没有、托盘图标也关着 | 放行本次关闭 → 管理窗口关闭即最后一个窗口 → **进程退出** |
| 管理窗口已藏起、托盘图标还开着 → 关掉最后一个面板 | **不退出**：留在托盘里等用户回来（托盘是找回界面的入口） |
| 面板关闭行为被设为「最小化到托盘」 | **托盘图标自动且必须打开**（`showTrayIcon` 连带写 true + 落盘）；此后取消勾选会被拒绝并提示原因 |
| 关闭行为不是 tray 时取消托盘图标 | 允许（且 `load()` 不会把它改成 true） |
| 旧配置里 `closeAction="quit"` | 读取时归一化为 `close` 写入 `panelCloseAction`，清空旧字段后写回 |
| 旧配置里 `managerCloseAction` | `load()` 直接清掉并写回（该功能已取消：管理窗口的关闭动作是固定的） |
| 旧配置里的面板级 `minimizeToTray` 键 | `load()` 清掉并写回（功能已收归应用级设置）；面板的最小化按钮即普通最小化 |
| 托盘「关闭面板」/ 删除面板 / 标签变更重开 | 直接关闭该窗口，不询问，不影响其他窗口 |
| 托盘「退出 PanelDock」 | 无条件退出（`quitting` 标记放行 `OnBeforeClose`，并停用托盘图标维护） |

## 桌面快捷方式

- `shortcut_windows.go`：手工 COM（IShellLinkW + IPersistFile，风格同 `panel_window_windows.go`）。桥接方法 `App.CreatePanelShortcut(panelID)`（返回 `{path, created, removed}`）与 `App.InspectPanelShortcut(panelID)`（只读预检：桌面有没有、有没有历史遗留的多余项），前端面板卡片有「桌面快捷方式」「固定任务栏」等按钮（后者见下节）。
- 快捷方式参数固定 `--open <面板ID>`，面板改名后仍有效；名称默认面板名；非法字符由 `sanitizeShortcutName` 清理。
- **一个分组桌面只留一份**（2026-09-30 用户要求「一个分组桌面只能创建一个快捷方式」）：`createDesktopShortcut` 先找桌面上已有的那一份，找到就**覆盖它、并保留用户起的文件名**，没有才新建。
  `findPanelDesktopShortcut` 有两条来源，缺一不可：① 按「目标=本程序（文件名比对）+ 参数含 `--open <面板ID>`」扫桌面（用户改过名也认得出）；② 配置里记录的那一份在桌面上且存在时也算 —— 这条兜底覆盖「程序改过名之后旧 `.lnk` 的目标名匹配不上」的情形，否则会另外堆一份新的出来。
  旧行为用 `uniqueShortcutPath`（重名加序号），点几次就堆出「名.lnk / 名 (2).lnk / 名 (3).lnk」，配置里也攒成一串 —— 2026-09-30 用户在自己的 `config.json` 里看到的就是这个。
- **历史遗留的重复项会被收敛**（`collapsePanelDesktopShortcuts`）：只删「本程序为该分组创建的」那些，别的分组、别的程序的 `.lnk` 一个不动；`keep` 为空时**一个都不删**（拿不准就别动手）。范围**只在桌面**，不碰任务栏固定目录。前端「桌面快捷方式」按钮在有既存项时先用 `window.confirm` 问「是否覆盖」，用户不点头就什么都不做。
- **坑 1**：`SHGetFolderPathW` 与 IShellLink 都要求 COM 已初始化，`shortcutWithCOM` 包住全部步骤，不要先取桌面路径再进 COM（会 E_FAIL）。
- **坑 2**：本机桌面重定向到 E 盘，`CSIDL_DESKTOPDIRECTORY`(0x0a) 返回 E_FAIL，须用 `CSIDL_DESKTOP`(0x00)（有 `%USERPROFILE%\Desktop` 兜底）。
- **坑 3**：`shortcutWithCOM` 必须 `runtime.LockOSThread`，且要识别 `S_FALSE`——详见「关键实现约束」。
- 读取既有 .lnk：`readShortcutTargetInCOM`（`IPersistFile.Load` + `IShellLinkW.GetPath/GetArguments`，`STGM_READ`），供删除面板的联动清理使用。
- **删除面板时的清理是可选项**：`App.DeletePanel(id, removeShortcuts bool)` + 前端删除确认对话框里的「同时删除快捷方式」复选框。勾了才清理，没勾就保留桌面上的 .lnk（面板已不存在，那些 .lnk 双击只会打开管理界面，保留是用户的明确选择）。对话框用 `<dialog>` 实现，同时列出将被清理的 `.lnk` 清单。
- 可注入的测试缝：`shortcutSelfExeBase`（本程序文件名）与 `shortcutDesktopDirectory`（桌面目录）都是包级变量，便于在临时目录里做完整扫描测试（`stubShortcutSeams` 是统一的注入助手，`shortcutTestExeName` 是测试用的程序名常量）。
- 单测：`TestDeletePanelShortcutCleanupIsOptIn`（勾选/不勾选两种收尾，含「别的面板的 .lnk 不能被误删」）、`TestListPanelShortcutsForDeleteDialog`（对话框依赖的只读查询，且证明它不删任何东西）。
- 集成测试 `TestCreateDesktopShortcutIntegration` / `TestListPanelShortcutsRealDesktop`（默认 skip，设 `PANELDOCK_DESKTOP_TEST=1` 在真实桌面验证）。

## 固定到任务栏

**结论先说：Windows 10/11 不允许程序自己把图标固定到任务栏，本功能只能做「准备好 + 引导」。**
任何「一键固定」的实现都是假的，别去试。

- 微软从 Windows 10 起明确关闭了程序化固定，官方答复是「固定到任务栏属于用户偏好，程序不应代为决定，只有用户本人可以固定」。Windows 11 又封掉仅剩的两条旁路：往 `%APPDATA%\Microsoft\Internet Explorer\Quick Launch\User Pinned\TaskBar` 拷 .lnk 不再生效；`shell:::{4234d49b-0245-4df3-b780-3893943456e1}` 命名空间的 pin 动词被隐藏（该命名空间现在只能取消固定）。
- **`taskbarpin` 动词必须整条删掉，不能「试一下不行再回退」**：shell 认不出这个动词时**静默退化成 `open`**（返回成功、`err=nil`，且目标程序真的被启动）。留着它，用户点「固定任务栏」的副作用就是白开一个面板窗口，而固定照样不会发生。
- 仍能无人值守写入任务栏的只剩 LayoutModification.xml + 杀掉并重启 `explorer.exe`。代价是任务栏整条消失再重建、所有托盘图标重载，写错还会覆盖用户已经排好的布局 —— 为一个图标不值得，本项目不碰。
- 因此实现只有两步（`app.go` 的 `App.PinPanelToTaskbar` + `taskbar_windows.go`）：
  1. 复用该面板已有的 .lnk（`listPanelShortcuts`），没有才调 `CreatePanelShortcut` 新建一个（路径记进配置，删面板时能一并清理）；
  2. `isPanelPinnedToTaskbar(panelID)` 回读任务栏固定目录判断是否已经固定 → `AlreadyPinned`。
- **选中快捷方式不许自动发生**：`revealShortcutInExplorer`（`ShellExecuteExW` + `explorer.exe /select,"<path>"`）只能由用户点引导对话框里的「选中快捷方式」（`App.RevealPanelShortcut`）触发。随弹框一起抢前台等于把说明文字盖掉，属于擅作主张。`PinTaskbarResult` 里没有「已经帮你选中了」这种状态。
- **固定状态的判定只能靠回读目录**，Windows 没有「查询固定状态」的 API，而那个目录就是事实本身。判定逻辑与桌面扫描同源：指向本程序（按文件名比对）+ 参数为 `--open <面板ID>`。Windows 自己生成的固定项能被 `IShellLinkW` 正常读出，**参数被完整保留**（连 `-taskbar-tab <uuid>` 这类参数都在），所以按参数匹配是可靠的；`File Explorer.lnk` 这类无目标的特殊项读不出目标，会被自然跳过。
- 前端面板卡片按钮：「桌面快捷方式」（原「快捷方式」；调 `App.InspectPanelShortcut` 预检，桌面已有这个分组的一份时先 `window.confirm` 问「是否覆盖」，用户点头才调 `App.CreatePanelShortcut`，返回 `{path, created, removed}` 用来把「新建 / 覆盖 / 顺带清理了几个」讲清楚）、「固定任务栏」、「刷新图标」（见「刷新图标」一节）、「编辑」等。后者若返回 `AlreadyPinned` 就直接告知「已经在任务栏上了」；否则弹 `<dialog>`（`showPinDialog`）说明 Windows 的限制 + 三步操作 + 快捷方式路径，**只讲不抢前台**：弹框时不打开资源管理器，由用户读完点主按钮「选中快捷方式」（走 `App.RevealPanelShortcut`）。这条路径失败时把错误写进脚注（`.confirm-footnote.is-warn` 转告警色），而不是 alert 弹窗打断、也不是假装帮上了忙。
- **固定项按钮的图标是可以变成站点图标的**：固定之后任务栏按钮走那一份固定项 `.lnk` 的图标，那是个**静态引用** —— 但用分组卡片上的「刷新图标」把站点图标缓存成 `.ico` 并改写它的 `SetIconLocation`（`listPanelShortcutsForIcon` 会同时扫桌面与固定目录）之后，固定项也能显示站点图标。运行中**未**固定的窗口按钮则自动跟着站点走（见「图标链路」里 AUMID 那条）。
- 删除面板**不会**删除任务栏上已固定的项：那份 .lnk 是 Windows 自己复制进固定目录的，不是本工具创建的，取消固定仍是用户自己的事（前端删除对话框脚注里写明了这一点）。但如果它正用着被删面板的图标缓存，**会把它的图标改回 exe 自带图标** —— 见「刷新图标」里的收尾顺序。
- 单测（`taskbar_windows_test.go`）：已固定时如实报告且不重复造 .lnk、未固定时**只报回快捷方式路径而不打开资源管理器**、没有 .lnk 时新建并记进配置（同样不打开）、固定目录里别人的项（别的面板/别的程序/读不出来的损坏项）不算数、`RevealPanelShortcut` 选中配置里记录的那份 .lnk / 拉不起资源管理器时返回错误 / 没有快捷方式时报错、面板不存在时报错。（`stubTaskbarSeams` 返回的 reveal 记录器现在主要用于断言「没被调用」。）
- **任何「会写用户目录」的路径都必须走注入缝**（`shortcutDesktopDirectory` / `resolvePortableRoot` 这类包级变量），不要直接调真实函数 —— 否则测试会往**用户真实桌面**写 .lnk、往真实 profile 目录写会话数据。

## 刷新图标（分组卡片上的按钮）

把站点图标缓存成本地 `.ico`，让**桌面快捷方式与任务栏固定项**都用上它。入口是分组卡片上的「刷新图标」（与「桌面快捷方式」「固定任务栏」并列），实现全在 `iconcache_windows.go`。

- **为什么非要落盘一个文件**：标题栏与任务栏按钮的图标是我们自己 `WM_SETICON` 上去的，进程活着就在；但 `.lnk` 与任务栏固定项的图标是 **shell 保管的一份静态引用**（`IShellLinkW::SetIconLocation` 指向某个 `.ico` 或 exe），它不认内存里的 `HICON`、更不认网页。所以必须有磁盘上的一个真实文件。
- **缓存位置**：`config.json` 旁边的 `icons\<面板ID>.ico`（`panelIconCacheDirFor` 由 `configStore.path` 推出，不重新推导便携/`%APPDATA%`，否则配置文件被指定到别处时图标会写到另一个地方）。用面板 ID 命名，与改名无关。
- **交互刻意分两步**，不是「点一下全做完」：
  - `App.InspectPanelIcon(id)` → `PanelIconPreview`：能不能刷、图标预览（data URL）、会动到哪几个 `.lnk`、写到哪个路径、**桌面那份是要覆盖还是要新建**（`shortcutPath` / `willCreateShortcut`）、有哪些重复项会被清掉（`duplicates`）。**不写任何文件**；
  - `App.ApplyPanelIcon(id)` → `PanelIconApplyResult`：写 `.ico` → 桌面那份**创建或覆盖** → 逐个改写任务栏固定项 → 收敛桌面上多余的 → 通知 shell。`Updated` / `Failed` **分开报**：完全可能「桌面那个改了、固定项没改成」，一句「成功」会让人以为全都好了。
  - **桌面没有快捷方式时必须顺手创建一个**（2026-09-30 用户原话：「当桌面没有快捷方式时，刷新图标后只是下载保存动作，这没啥意义」）。所以前端主按钮在那时叫**「创建快捷方式并应用」**，有既存项时叫「应用」。这条同时要求 `ApplyPanelIcon` 把路径 `setShortcut` 记进配置 —— 不记的话删面板时这份 `.lnk` 会漏在桌面上没人管。
  - 分开的理由：改的是**用户自己创建的东西**（桌面快捷方式、任务栏固定项）。这类动作必须先摆出「会改成什么」让人点头。图标只有一份、快捷方式也只有一份，覆盖意味着替换掉既有的东西，更要说清楚（对话框逐条列出「会覆盖图标 / 会覆盖快捷方式 / 会清理几个重复项」）。
- **「有没有图标可用」只能看面板窗口留下的样本**（`panelIconRegistry`，`registerPanelIconSample` / `unregisterPanelIconSamples`）。三种情况提示完全不同，不能混：
  - 面板窗口没打开过（或页面还在加载）→ 「还没打开过，请先打开它」；
  - 窗口开过但站点没给图标（样本在、候选为空）→ 「当前地址没有图标可用，只能用默认图标」；
  - 有候选 → 可刷。
  **窗口销毁时必须注销样本**（`panelWindow.dispose` 里调），否则窗口都关了按钮还会说「有图标可用」，然后写一份过期图标出去。样本里只记**站点真实给出**的图标，不含自绘的首字母色块 —— 色块是「没有图标」时的显示替代品，不该被缓存成快捷方式图标。
- **多标签一律取第一个标签的图标**（`cfg.Tabs[0]`）。缓存是分组级的：一个分组一份 `.ico`，快捷方式也指向整个分组。若跟着「当前显示的标签」走，同一个快捷方式两次刷新会给出不同图标，没法解释。
- **`.ico` 里要有多档尺寸**：16（列表/详细）、32（任务栏/小图标）、48（中图标）、64/128、256（大图标）。只塞一帧的后果是**某些尺寸下糊**（shell 把 16 拉大成 256）。≤48 用 **DIB 帧**（兼容性最好），>48 用 **PNG 帧**（体积小一个数量级：256 的 DIB 光像素就 256KB）。
- **源图不够大的档位直接跳过，不做放大**：站点只给 32 时硬拉出 256 只会得到一张模糊的图，还不如让 shell 缩放 32 那一帧。例外是 16 —— 列表视图的最低要求，实在没有就用最大的源缩下去。
- **两个必须亲自踩过才知道的坑**：
  - **DIB 帧的 `biHeight` 必须写两倍高度**（ICO 把 XOR 位图与 AND 掩码描述成一张上下拼接的位图），写一倍高度会让后半截被当成像素；像素是**自底向上**存的（第一行是图像最后一行）。
  - **帧里的 alpha 是直通（straight），不是预乘** —— 而 `panelResample` 产出的是预乘值，所以出帧前必须过 `panelToNRGBA` 反预乘（`png.Encode` 也要给 NRGBA，它对 RGBA 会再乘一遍 alpha）。搞反的症状是半透明边缘发亮，不放大看不出来。
- **改写 `.lnk` 必须「读回来再整份重写」**（`setShortcutIcon`）：`IShellLinkW` 只有整份 `Save`，新建出来的对象是空的 —— 不先把 `GetPath`/`GetArguments`/`GetDescription` 读回来就 `SetPath`，会得到一个双击没反应的空壳，而返回值全是 `S_OK`。传空 `iconPath` 表示回退到目标程序的内嵌图标。
- **扫描范围**：`listPanelShortcutsForIcon` 扫**桌面 + 任务栏固定目录**（固定时 Windows 把 `.lnk` **复制**过去，是两份独立文件，只改桌面那份任务栏不会有任何变化）。
  **不要**把固定目录并进 `listPanelShortcuts` —— 那个函数是**删面板**时的清理范围，扩大会让「删面板」顺手删掉用户亲手固定到任务栏的项目。
- **删面板时的收尾顺序不能反**（`dropPanelIconCache`）：先把还指着这份缓存的 `.lnk` 的图标改回 exe 自带图标，**再**删 `.ico`。反过来的话那些 `.lnk` 会指向不存在的图标，桌面上变成空白方块，而用户完全不知道是自己刚删了个面板。另外只动**图标确实等于我们那份缓存**的 `.lnk`（`readShortcutIconPath` 比对）—— 用户自己换过的图标不碰。
- **新建/覆盖快捷方式会自动用上缓存**（`App.CreatePanelShortcut` 与 `ApplyPanelIcon` 都传 `cachedPanelIconPath`）：缓存过就直接用，不必再点一次「刷新图标」。
- **改完必须通知 shell**（`SHChangeNotify`，`SHCNE_UPDATEITEM` + `SHCNF_FLUSH`；改过固定项目录里的东西再补一次 `SHCNE_UPDATEDIR`）。不通知的症状是「改了但看不见」：`.lnk` 里已经是新值，桌面上还是旧图标，用户以为功能没生效。
- 单测（`iconcache_windows_test.go`）：多帧 ICO 往返（尺寸 + 颜色 + 透明区）、**DIB 帧的方向与 BGRA 通道序**（上红下蓝的图一次测两件事）、源图不够大不放大、`LoadImageW` 让 **Windows 自己认这份 .ico**（唯一一条不靠自家解码器的验证）、缓存路径的路径穿越、`setShortcutIcon` 不丢目标与参数、传空串回退 exe 图标、扫描范围含固定目录而删除范围不含、预检三态、只取第一个标签、应用后两个 `.lnk` 都改到、重复刷新可覆盖、删面板后图标回退且文件消失、用户自己换过的图标不被碰、样本登记与注销。
  另有三条守「一个分组只留一份快捷方式」这一版新增的行为：`TestApplyPanelIconCreatesShortcutWhenDesktopHasNone`（桌面没快捷方式时顺手创建 + 记进配置 + 图标与参数都对）、`TestPlanPanelIconCollapsesDuplicateDesktopShortcuts`（预检报告重复项、且**不**把马上要删的那几份列进「会改到图标」）、`TestCreatePanelShortcutOverwritesInsteadOfStacking` + `TestCollapsePanelDesktopShortcuts`（`shortcut_test.go`）、`TestConfigMigrationShortcutsArrayToOne`（`config_test.go`，旧 `shortcuts` 数组收敛）。
- 这组用例**全部做过反向验证**（改一处源码 → 对应用例必须变红）：DIB 方向、DIB 通道序、放大小图、不读回目标、扫描退回只扫桌面、取最后一个标签、删面板不动快捷方式；以及本版新增的 5 项 —— 覆盖既有快捷方式、桌面没快捷方式时顺手创建、重复项收敛、旧配置数组迁移、预检的「本次会创建」 —— 合计 12 项全部有效。

## 托盘图标

- 托盘图标由**常驻 IPC 窗口**承载（`tray_windows.go`，固定 uid=1），因此**与打开几个面板无关** —— 一个面板都没开时托盘里也入口（这也是「零面板时关闭管理窗口不退出」的前提）。全局开关是 `showTrayIcon`（默认开启）。
- 保留图标的三条理由（`App.trayNeeded`，任一成立即保留，即使 `showTrayIcon=false`）：全局开关开启 / 管理窗口正藏在托盘（`App.resident`）/ 某个面板窗口正藏在托盘（`panelWindow.hiddenInTray`）。窗口从托盘恢复时清掉对应标记；`App.syncTray()` 幂等地增删图标。**例外**：`App.quitting` 时无条件不维护。
- **图标与「最小化到托盘」绑定**：面板窗口的关闭行为是 `tray` 时，`showTrayIcon` 就自动且必须为 true（见「应用级设置」的不变量）。
- **Explorer 重启要重注册图标**：注册 `TaskbarCreated` 消息，收到后调 `trayReAdd()` —— 否则任务栏重启后托盘图标会消失。
- Windows 11 默认把新图标折叠进任务栏的「隐藏的图标」浮出层（点 `^`），首次可能看不到，需手动拖到任务栏常驻——这不是程序问题。
- 托盘悬浮提示固定为 `PanelDock · Web管理面板启动器`（应用级，不带面板名）；活动面板名显示在菜单项文字里（超过 `trayMenuNameLimit` 字符截断）。

## 已知未实现（对应《方案讨论记录.md》的后续阶段）

- 配置导入/导出与备份。
- **按地址（标签级）**的登录数据策略：目前策略在**面板（标签分组）级**（`sessionMode`，见「会话状态」一节），一个面板下的所有标签同进同出。要「这个面板里 A 标签保留、B 标签清空」还得把 `sessionMode` 下放到 `PanelTab`（清理逻辑 `preparePanelProfile` / `clearTabProfile` 已经是按 tabID 做的，改造量主要在配置层与表单）。
- 对**非本地** HTTP 地址展示登录风险提示（局域网 / 内网地址不提示）。
