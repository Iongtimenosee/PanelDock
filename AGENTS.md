# AGENTS.md

> 你正在改一个几乎是**手工 Win32 + 手工 COM** 的 Windows 桌面应用，外面套了一层 Wails 壳。
> 本文件讲**现在是怎么做的**和**哪些规则不能破**；`docs/pitfalls.md` 讲**为什么会这样**和**哪些地方会静默失败**。
>
> 动手前先读一遍「一天就够上手的心智模型」。看着奇怪的写法，八成是踩坑后的产物 —— 改之前去 `docs/pitfalls.md` 查对应小节。

## 一天就够上手的心智模型

1. **两套彼此隔离的窗口体系**。主窗口是 Wails/WebView2（前端 JS + 桥接方法）；面板窗口是自建 Win32 子窗口 + WebView2，**没有任何桥接**。这两者行为的差异（谁能弹原生框、谁被 Wails 的生命周期管着）是大部分奇怪约定的来源。
2. **窗口的创建方式决定了它的死法**。管理窗口是 Wails 的窗口，**一销毁 = 「没有窗口了」= Wails 立刻结束进程**并顺手带走所有面板窗口。所以管理窗口的关闭只能是「藏起来」。面板窗口是自建的，可以随便销毁。
3. **PID 级别的身份隔离**：每个标签一个独立 WebView2 Environment + UserDataFolder（`WebViewProfiles\<tabID>`）。换 id 就是换浏览器身份，登录会话全丢。
4. **界面里有两份事实源**：`config.json` 是事实源，localStorage 只是防首帧闪烁的读透缓存。前端没有轮询，所有后端自行改的设置必须广播。
5. **Windows 的很多 API 会静默失败**：S_OK 不代表做对了，返回 NULL 时 GetLastError 可能是 0。这套代码里有大量「做完必须验证」的写法，别嫌啰嗦。

## 项目定位

PanelDock：Windows/Wails 桌面工具，用**隔离的 WebView2 会话**打开局域网管理页面（OpenClash、路由器、NAS 等）。主窗口（Wails）负责面板与标签的配置管理。

四条硬边界，任何时候都不许越：

- **不向远程页面注入任何 DOM / 脚本 / WebMessage** —— 远程页面与宿主之间零通信通道。
- **面板窗口不暴露任何桥接**：无 `window.external`、无 HostObject、不处理 WebMessage。
- **配置文件里没有任何密码字段**。密码由 WebView2 自己加密存进各标签 profile 目录，本工具不读取也不导出。`PanelConfig.PasswordAutosave` 只是「让不让 WebView2 保存」这个开关的值。
- **只支持 http/https 地址**。

## 架构地图

| 文件 | 职责 |
|---|---|
| `main.go` | Wails 入口，绑定 App；最先做单实例判定与请求转发 |
| `app.go` | 主窗口桥接 API：面板/标签 CRUD、打开/关闭、置顶/托盘、重置数据、导出导入配置 |
| `config.go` | `%APPDATA%\PanelDock\config.json` 的加载/原子写入/损坏回退/旧版迁移；便携模式路径解析；应用级设置 `settings` 节点 |
| `ipc_windows.go` | 单实例命名互斥体 + 隐藏 IPC 窗口（`WM_COPYDATA` 转发）；**该窗口同时承载应用级托盘图标** |
| `tray_windows.go` | 应用级托盘图标与菜单（`Shell_NotifyIconW`，挂在常驻 IPC 窗口上）；`App.trayNeeded` / `App.syncTray` |
| `prompt_modal_windows.go` | **原生模态询问框公共设施**（窗口类 + 窗口过程 + 控件/字体/DPI/居中/消息循环，`promptModalSpec` 驱动） |
| `close_prompt_windows.go` | 「关闭面板窗口」询问框。**只有面板窗口会问**，管理窗口的关闭动作是固定的 |
| `enable_prompt_windows.go` | 「面板已停用」询问框；`mainWindowHandle()` 也在这里 |
| `native_text_windows.go` | 原生界面文案词典（i18n 的 Win32 侧），zh-CN / en-US 两份 |
| `apperror.go` | 面向用户的错误码（`appError` + `errCode/errCodeDetail/errCodeWrap`） |
| `panel_window_windows.go` | 手工 COM 子集的独立面板窗口（**无边框**），原生标签栏、窗口状态记忆、外壳 `WM_ERASEBKGND` 自绘 |
| `titlebar_windows.go` | 面板窗口自绘标题栏：无边框框架、命中测试、地址栏 EDIT、导航按钮、WebView2 导航事件接入 |
| `icons_windows.go` | 站点图标**从哪来、要哪几个尺寸**：多来源候选 → 下载解码 → 按边长择优 → 标题栏 DIB + 任务栏 HICON |
| `iconcache_windows.go` | 图标**落盘缓存**与 `InspectPanelIcon` / `ApplyPanelIcon` 两阶段桥接 |
| `aumid_windows.go` | `main()` 最早处声明显式 AppUserModelID（**不声明会被自己创建的快捷方式劫持任务栏图标**） |
| `password_autosave_windows.go` | 把分组级「保存登录密码」开关送到 WebView2（`Settings4::put_IsPasswordAutosaveEnabled`） |
| `shortcut_windows.go` | 桌面快捷方式（IShellLinkW + IPersistFile 手工 COM），含读回既有 .lnk 与联动清理 |
| `taskbar_windows.go` | 「固定到任务栏」的准备与引导；固定状态回读判定 |
| `session.go` | 会话清理策略：删整个 profile 目录 |
| `theme_windows.go` | 明暗主题后端侧：读 Windows 深浅色偏好、解析生效主题、管理窗口原生底色联动 |
| `theme_panel_windows.go` | 面板窗口外壳的配色表与换肤（跨线程 `requestTheme` / `applyPendingTheme`） |
| `frontend/src/main.js` | 主窗口 UI（原生 JS，无框架），全部文案经 `t()` 取自词典 |
| `frontend/src/i18n/` | 前端词典两份（zh-CN 源语言 / en-US）+ 查表模块，随 vite 编译进产物 |

## 命令行与启动模式

- `PanelDock.exe --open <面板ID>`：**轻量模式** —— 主窗口以 `StartHidden` 启动、不显示，自动打开指定面板。传的是**面板 ID**（`panels[].id`），不是标签 ID。
- 面板不存在 / 没有标签 / 已被停用时行为各不相同（见「停用面板」），共同点是**不会留下看不见的进程**。
- 已有实例在运行时，本次启动只做 IPC 转发，不会叠出第二套窗口。
- 命令面只有 `--open` 一个参数，没有标签级直达。要「单标签直达」，建一个只含单标签的面板。

## 进程寿命：什么时候才真正退出

**判定只有一处**：`App.quitWhenNoWindows(remainingPanels)`。`shouldQuitAfterPanelClosed` 传运行表长度做实时判定，`panelCloseEndsProcess` 传 0 做预测（问「关掉这个面板后程序会退出吗」）。顺序：

1. `quitting` → false
2. 还有分组标签窗口 → false
3. 管理窗口还在（`mainShown`）→ false
4. 管理窗口藏在托盘里（`resident`）→ false
5. **轻量模式 + `lightweightQuitOnLastPanel` 开着 → true**
6. 否则**看托盘**：托盘图标还开着就留在托盘，关掉了才退

用户看到的规则只有一条：**还有窗口（管理窗口 / 分组标签窗口）或托盘里还有图标，就不退出整个程序**。

关闭面板窗口**只影响那个面板**（`p.close()`）；关闭管理窗口**只关它自己**。由此：

| 场景 | 行为 |
|---|---|
| `--open` 启动 + 关闭最后一个面板 | 进程退出（「用完即走」，默认开启） |
| `--open` 启动但「用完即走」被关掉 → 关最后一个面板 | **不退出**：托盘图标开着就留在托盘 |
| 轻量模式 + 经托盘/转发呼出过管理窗口 → 再关面板 | **不退出**（`mainShown`） |
| 管理窗口藏起、托盘图标开着 → 关掉最后一个面板 | **不退出**：留在托盘等用户回来 |
| 点管理窗口 X，零面板且托盘图标也关着 | 进程退出（它已是最后一个窗口） |
| 托盘「退出 PanelDock」 | 无条件退出（`quitting` 放行 `OnBeforeClose`，并停用托盘图标维护） |

`mainShown` 只有 `ShowMainWindow` 与 `hideManagerWindow` 两个入口能改，必须准。

## 两条关闭路径必须分清

- **交互式关闭** = 用户点标题栏 X 发出的 `WM_CLOSE`。走 `panelWindow.handleInteractiveClose()` → `App.decideClose(owner, note)`，按设置询问或直接执行；「直接关闭」只 `p.close()` 本窗口。
- **主动关闭** = 私有消息 `win32WMDirectClose`（`WM_APP+2`），由 `panelWindow.close()` 发出，用于退出程序、删除面板、标签变更重开、托盘菜单「关闭面板」。**永不弹询问框**。

**新增任何「程序内部关窗口」的代码，一律走 `p.close()`**，不要直接 `PostMessage(WM_CLOSE)`，否则会莫名弹出询问框。反过来，标题栏那个关闭按钮**必须**走 `PostMessage(WM_CLOSE)` —— 用私有消息会绕过询问框。

另外：**不要从 Wails 绑定方法里弹原生模态框**，那会在 WebView2 的消息处理线程上自建 `GetMessage` 循环。所有原生询问框只能由 Win32 侧路径触发。

## 管理窗口的关闭动作（固定，不询问）

`App.onBeforeClose` → `App.closeManagerWindow`：

- **还有分组标签窗口、或托盘图标还开着 → 只关自己**（把窗口藏起来，进程继续运行）；**两者都没有 → 放行**，管理窗口一关就是「最后一个窗口关闭」，进程随之结束。
- 为什么藏而不销毁：管理窗口是 Wails 的窗口，**一销毁就等于「没有窗口了」**，Wails 立刻结束进程并顺手带走所有面板窗口。所以「只关自己」只能靠 `OnBeforeClose` 拦住。
- **不要写「顺手把其它窗口关掉」或「顺手撤掉托盘图标」这类代码**：前者破坏「还有窗口在就不退出」，后者让行为与设置里的勾选框对不上。托盘设置只由「在系统托盘显示图标」管，关管理窗口一个字节都不改。
- 「藏起来」时若托盘图标开着，顺手置 `resident = true`（语义：管理窗口正藏在托盘里），图标才能留下把窗口找回来；图标本来就关着时不置位，免得凭空变出一个图标。
- 管理窗口藏起来之后，想回管理界面**再启动一次程序**即可（单实例 IPC 转发 → `ShowMainWindow`）。

## 单实例调度

- 命名互斥体 `Local\PanelDock.SingleInstance.Mutex`。非首个实例把命令 `open:<面板ID>`（或 `show`）用 `WM_COPYDATA` 转发给已运行实例，然后自行退出。IPC 窗口类名 `PanelDock.IPC`。
- 转发用 `SendMessageTimeoutW` + `SMTO_ABORTIFHUNG`（3s），避免目标线程挂起时永久阻塞；找不到窗口时重试 20×50ms，覆盖「上一个实例仍在启动」的竞态。转发失败退化为独立实例继续运行，不静默退出。

## 应用级设置（`config.json` 的 `settings` 节点）

| 键 | 取值 | 含义 |
|---|---|---|
| `showTrayIcon` | bool，默认 true | 是否在系统托盘显示图标。图标由**常驻 IPC 窗口**承载，**与打开几个面板无关** |
| `panelCloseAction` | `ask`（默认）/ `tray` / `close` | 关闭**面板窗口**时的行为。询问框里勾「记住我的选择」写回这一项 |
| `lightweightQuitOnLastPanel` | bool，默认 true | 轻量模式下关掉最后一个面板是否直接退出。常规启动不受影响 |
| `theme` | `auto`（默认）/ `light` / `dark` | 明暗配色 |
| `language` | `auto`（默认）/ `zh-CN` / `en-US` | 界面语言 |

### 不变量：托盘图标与「最小化到托盘」绑定

面板窗口的关闭行为是 `tray` 时，`showTrayIcon` **自动且必须为 true** —— 选了这个行为的窗口一关闭就藏进托盘，图标是把它找回来的唯一入口。四个落点缺一不可：

- `load()` 纠正矛盾配置并写回
- `settingsLocked()` 读取时归一化（`trayNeeded` / `quitWhenNoWindows` 都读它，绝不能看到矛盾组合）
- `configStore.setCloseAction` 选中 `tray` 时连带打开
- `setShowTrayIcon(false)` 直接拒绝（`ErrTrayIconRequired`）

**没有面板级 `minimizeToTray`**：面板的最小化按钮就是普通最小化，把窗口送进托盘只发生在「关闭」时。

### 三态设置一律用字符串枚举，不用 bool

`theme` / `language` / `sessionMode` / `passwordAutosave` 都是**字符串枚举 + 空串即默认值**。理由：默认值是「跟随系统 / 默认开启」，bool 表达不了，且老配置缺键时零值 `false` 会被当成「用户显式关掉了」。

> ⚠️ 给 `settings` 新增「默认开启」的布尔项时，必须按「键在不在」补默认值（`settingsHasKey`）—— 详见 `docs/pitfalls.md`。

### 已废弃的旧字段

`load()` 读配置时会清掉并写回。**写测试或脚本改配置时必须一并清掉这些键**，否则加载时的迁移会覆盖新值：

- 旧 `closeAction` → 归一化进 `panelCloseAction`（旧值 `quit` 归一为 `close`），清空旧字段
- `managerCloseAction` → 直接清掉（管理窗口的关闭动作是固定的）
- 面板级 `minimizeToTray` → 清掉（功能已收归应用级设置）
- 旧 `shortcuts` 数组 → 收敛成单个 `shortcut`

### 配置导出 / 导入

`App.ExportConfig` / `App.ImportConfig`，只管 config.json 的内容。图标缓存可由「刷新图标」重建、WebView2 登录态不该跨机器搬，都不在范围内。

- **校验失败时现有配置一个字节不动**。预检必须在 `load()` 之前做 —— `load()` 对坏 JSON 的语义是「回退默认配置并写回」，直接 load 用户的文件会把垃圾悄悄变成默认配置。
- 成功前备份为 `config.json.bak`；导入后 `syncTray` + 主题双窗口 + 双广播收尾。**已打开的面板窗口不关**。
- 用户取消对话框返回 `false`，不算错误。

### 后端改设置必须广播

`App.notifySettingsChanged` → Wails 事件 `paneldock:settings-changed`。**前端没有轮询**，新增任何「后端自行改设置」的路径都要广播，否则设置区显示的勾选状态会与配置文件不一致。典型情形：选中「最小化到托盘」会连带把托盘图标打开。

## 功能行为速查

每条只给结论，背后的坑见 `docs/pitfalls.md` 同名小节。

**面板窗口布局**：自上而下三段 —— 自绘标题栏（40px）→ 原生标签栏 → WebView2，`panelWindow.resize()` 统一摆位，`contentBounds()` 是唯一的内容区分界来源。标签栏最右端是明暗配色切换按钮。

**会话状态**（`panels[].sessionMode`：`persist` 默认 / `fresh`）：实现方式是**整个 profile 目录删掉**（ `session.go`），不是逐类挑文件。两个挂接点缺一不可 —— `dispose()` 里同步执行 `clearUntilStable`（关闭后），`newPanelWindow` 前执行 `clearOnce`（打开前兜底）。

**保存登录密码**（`panels[].passwordAutosave`：`on` 默认 / `off`）：走 WebView2 自带的密码管理。`applyPasswordAutosave` 必须在 `navigate` 之前调用。改完**当场生效**。UI 文案只能写「不再保存新密码」—— 已存下来的密码仍会被回填，想彻底不留只能靠 `fresh`。

**界面配色**：同一份 `settings.theme` 驱动两个窗口（管理窗口右上角按钮 / 面板标签栏最右端）。**配色只管外壳，不管页面**（不注入远程页面）。

**界面语言**：词典编译进程序，没有外挂语言文件。两份词典（前端 JS + Win32 侧 Go map）**刻意不共享**。Win32 侧 `nativeText()` 每次现读配置、不缓存。错误码化。

**询问框**：新增就加一份 `promptModalSpec`，别复制窗口类。三件必须核对：按钮 ID 全局唯一、`BodyHeight` 与正文行数匹配、`Caption` 全局唯一（E2E 靠它定位）。

**停用面板**：外部 `--open` 请求到一个停用面板时弹原生询问框。「启用并打开」落盘后照常打开；「保持停用」返回 `App.ErrPanelKeptDisabled` 并**静默收场**（不弹管理窗口、不报错）。**没有「记住我的选择」** —— 记成「以后自动启用」等于悄悄绕过用户的停用意图。

**面板标签**：数量自然决定形态（1 个 = 单标签，多个 = 分组），没有形态选择器。顺序即 `tabs[]` 数组顺序，没有单独的排序字段。编辑保存走 `UpdatePanelTabs` 整体替换。

**删除面板**（`App.DeletePanel(id, removeShortcuts)`）：**无条件清数据**，不看 `sessionMode`。顺序固定 —— 关窗口 → 清数据 → 删快捷方式 → 删配置，清数据失败就整件事中止。快捷方式清理是「显式勾选」的结果。

**重置数据**（`App.ResetPanelData(id)`）：保留面板、只清数据，不改任何配置。先关窗口再清，不自动重开。

**桌面快捷方式**：参数固定 `--open <面板ID>`。**一个分组桌面只留一份**，有既存项就覆盖（保留用户起的文件名）。历史遗留的重复项会被收敛，但只删本程序为该分组创建的。

**固定到任务栏**：**Windows 不允许程序自己固定，只能做「准备好 + 引导」。任何「一键固定」的实现都是假的。** 实现只有两步：复用/新建该面板的 .lnk，回读任务栏固定目录判断是否已固定。选中快捷方式只能由用户点出来。

**刷新图标**：缓存 .ico 到 `config.json` 旁边的 `icons\<面板ID>.ico`，让桌面快捷方式与任务栏固定项都用上站点图标。交互刻意分两步（预检 / 应用），因为改的是用户自己创建的东西。桌面没有快捷方式时必须**顺手创建一个**。

**托盘图标**：由常驻 IPC 窗口承载，与打开几个面板无关。`trayNeeded` 三条理由任一成立即保留（全局开关 / 管理窗口藏在托盘 / 面板藏在托盘），后两条优先于用户取消勾选。**Explorer 重启要重注册**（`TaskbarCreated` → `trayReAdd()`）。

**管理界面 UI**：卡片头部固定两行（名称+徽章 / 操作区），**操作按钮不要塞回名称那一行**。**界面不展示面板运行时状态**，主按钮固定写「打开」。破坏性确认对话框一律用应用内 `<dialog>`，默认聚焦「取消」。

## 运行时数据位置与便携模式

便携模式（`<exe目录>\data\` 存在时自动启用，约定同 VSCode 的 `data` 目录）：

| | 便携模式 | 非便携 |
|---|---|---|
| 配置 | `<exe目录>\data\config.json` | `%APPDATA%\PanelDock\config.json` |
| 会话/profile | `<exe目录>\data\WebViewProfiles\<tabID>` | `%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>` |
| 图标缓存 | `<exe目录>\data\icons\<面板ID>.ico` | `%APPDATA%\PanelDock\icons\<面板ID>.ico` |

整个「exe + data」文件夹拷走即完成迁移，登录会话随行。检测函数 `resolvePortableRoot`（包级变量，测试可注入）。

`build/bin/PanelDock.exe` + `build/bin/data/` 是本地开发实例的数据所在。`wails build` 的 `Clean Bin Dir` 为 false，data 不会被清掉。

## 构建与验证

本机工具链：Go 1.27 / Wails CLI v2.16 / Node v24 / UCRT64 gcc 16.2 / WebView2 Runtime 153。

- 经 Git Bash 启动时找不到 `npm`，构建请用 `cmd /c "wails build"`（CLI 在 `~/go/bin`）。
- **回归基线（全部必须通过）**：`go vet ./...`、`go test ./...`、`wails build`。别用 `-s`（会跳过前端构建，dist 里留旧产物）。
- 端到端验证（默认跳过）：`PANELDOCK_E2E=1 go test -run TestE2E -v .`
- 快捷方式集成测试（默认跳过）：`PANELDOCK_DESKTOP_TEST=1 go test -run TestCreateDesktopShortcutIntegration -v .`
- 跑完 E2E 建议 `diff` 一次 `build/bin/data/config.json`，确认用户配置没被改动。

**两条不可妥协的测试规矩**（详见 `docs/pitfalls.md`）：

1. **新写的用例必须做反向验证**：把修复撤掉重建、确认用例变红。否则很容易写出恒真的空断言。
2. **任何「会写用户目录」的路径都必须走注入缝**（`shortcutDesktopDirectory` / `resolvePortableRoot` 这类包级变量）。否则测试会往**用户真实桌面**写 .lnk、往真实 profile 目录写会话数据。
