# 架构与运行机制

**本文件讲「现在是怎么做的」**。行为契约见 `docs/behavior.md`，踩坑见 `docs/pitfalls.md`。

---

## 命令行与启动模式

- `PanelDock.exe --open <面板ID>`：**轻量模式** —— 主窗口以 `StartHidden` 启动、不显示，自动打开指定面板。传的是**面板 ID**（`panels[].id`），不是标签 ID。
- 面板不存在 / 没有标签 / 已被停用时行为各不相同（见 `behavior.md`），共同点是**不会留下看不见的进程**。
- 已有实例在运行时，本次启动只做 IPC 转发，不会叠出第二套窗口。
- 命令面只有 `--open` 一个参数。要「单标签直达」，建一个只含单标签的面板。

## 进程寿命

**判定只有一处**：`App.quitWhenNoWindows(remainingPanels)`。`shouldQuitAfterPanelClosed` 传运行表长度做实时判定，`panelCloseEndsProcess` 传 0 做预测。顺序：

1. `quitting` → false
2. 还有分组标签窗口 → false
3. 管理窗口还在（`mainShown`）→ false
4. 管理窗口藏在托盘里（`resident`）→ false
5. **轻量模式 + `lightweightQuitOnLastPanel` 开着 → true**
6. 否则**看托盘**：图标还开着就留在托盘，关掉了才退

用户看到的规则只有一条：**还有窗口（管理窗口 / 分组标签窗口）或托盘里还有图标，就不退出整个程序。**

| 场景 | 行为 |
|---|---|
| `--open` 启动 + 关闭最后一个面板 | 进程退出（「用完即走」，默认开启） |
| `--open` 启动但「用完即走」被关掉 → 关最后一个面板 | **不退出**：托盘图标开着就留在托盘 |
| 轻量模式 + 经托盘/转发呼出过管理窗口 → 再关面板 | **不退出**（`mainShown`） |
| 轻量模式 + 用户在托盘上操作过窗口 → 再关最后一个面板 | **不退出**（`trayUsed`） |
| 管理窗口藏起、托盘图标开着 → 关掉最后一个面板 | **不退出**：留在托盘等用户回来 |
| 点管理窗口 X，零面板且托盘图标也关着 | 进程退出（它已是最后一个窗口） |
| 托盘「退出 PanelDock」 | 无条件退出（`quitting` 放行 `OnBeforeClose`） |

`mainShown` 的写入口共三处，缺一不可：`startup`（常规启动一上来就是显示的）、`ShowMainWindow`、`hideManagerWindow`。

### 轻量模式的让位开关：`trayUsed`

轻量实例还要多答一个问题：**用户有没有真的用过托盘**。没用过之前（`trayUsed` 为假），
`App.closeSkipsTrayHide` 会把「最小化到托盘」这一步整个跳掉、直接关掉面板 —— 进程马上就
被「用完即走」收走，先藏进托盘是纯属多余的中间态，用户只会看到「明明选了最小化到托盘，程序却没了」。
想在托盘里留住，就必须先在托盘上操作一次（`markTrayToggleTarget` 经双击或菜单恢复置位）。

**这一步对每个面板都成立，不分是不是最后一个**。只看最后一个是不够的：藏进托盘的窗口仍然占着
运行表 `App.panels` 的位置，「还有分组标签窗口」这条判断就一路成立 —— 开着两个面板的用户会发现
第一个先被藏起来，第二个横竖轮不到「最后一个」，两个窗口一起赖在托盘里，进程再也不退出。
（判定原先挂在 `panelCloseEndsProcess` 上，实测正是这个下场。）

`trayUsed` 只决定这一步，**不参与退出判定** —— 进程寿命仍然由上面的六步单点说了算，
这样「用完即走」不会因为多了一个标记而出现第二条判定路径。

## 两条关闭路径

| 路径 | 触发 | 行为 |
|---|---|---|
| **交互式关闭** | 用户点标题栏 X → `WM_CLOSE` | `handleInteractiveClose()` → `App.decideClose(owner, note)`，按设置询问或直接执行；「直接关闭」只 `p.close()` 本窗口 |
| **主动关闭** | 私有消息 `win32WMDirectClose`（`WM_APP+2`），由 `panelWindow.close()` 发出 | 用于退出程序、删除面板、标签变更重开。**永不弹询问框** |

- 新增任何「程序内部关窗口」的代码一律走 `p.close()`。直接 `PostMessage(WM_CLOSE)` 会莫名弹出询问框。
- 标题栏关闭按钮**必须**走 `PostMessage(WM_CLOSE)` —— 用私有消息会绕过询问框。
- **不要从 Wails 绑定方法里弹原生模态框**（会在 WebView2 消息线程上自建 `GetMessage` 循环）。

## 管理窗口的关闭动作（固定，不询问）

`App.onBeforeClose` → `App.closeManagerWindow`：

- **还有分组标签窗口、或托盘图标还开着 → 只关自己**（窗口藏起来，进程继续）；**两者都没有 → 放行**（它已是最后一个窗口，进程随之结束）。
- 藏而不销毁的原因：管理窗口是 Wails 的窗口，一销毁就是「没有窗口了」。
- **不要写「顺手关掉其它窗口」或「顺手撤掉托盘图标」**：前者破坏「还有窗口在就不退出」，后者让行为与设置里的勾选框对不上。托盘设置只由「在系统托盘显示图标」管。
- 「藏起来」时若托盘图标开着，顺手置 `resident = true`；图标本来就关着时不置位，免得凭空变出图标。
- 藏起来之后想回管理界面，**再启动一次程序**即可（单实例 IPC 转发 → `ShowMainWindow`）。

## 单实例调度

- 命名互斥体 `Local\PanelDock.SingleInstance.Mutex`；IPC 窗口类名 `PanelDock.IPC`。
- 非首个实例把命令 `open:<面板ID>`（或 `show`）用 `WM_COPYDATA` 转发给已运行实例后自行退出。
- 转发用 `SendMessageTimeoutW` + `SMTO_ABORTIFHUNG`（3s）；找不到窗口时重试 20×50ms，覆盖「上一个实例仍在启动」的竞态。
- 转发失败退化为独立实例继续运行，不静默退出。

## 应用级设置（`config.json` 的 `settings` 节点）

| 键 | 取值 | 含义 |
|---|---|---|
| `showTrayIcon` | bool，默认 true | 系统托盘图标。由**常驻 IPC 窗口**承载，**与打开几个面板无关** |
| `panelCloseAction` | `ask`（默认）/ `tray` / `close` | 关闭**面板窗口**时的行为。询问框勾「记住我的选择」写回这一项 |
| `lightweightQuitOnLastPanel` | bool，默认 true | 轻量模式下关掉最后一个面板是否直接退出。常规启动不受影响 |
| `theme` | `auto`（默认）/ `light` / `dark` | 明暗配色 |
| `language` | `auto`（默认）/ `zh-CN` / `en-US` | 界面语言 |

### 不变量：托盘图标与「最小化到托盘」绑定

面板关闭行为是 `tray` 时，`showTrayIcon` **自动且必须为 true** —— 窗口一关闭就藏进托盘，图标是把它找回来的唯一入口。四个落点缺一不可：

- `load()` 纠正矛盾配置并写回
- `settingsLocked()` 读取时归一化（`trayNeeded` / `quitWhenNoWindows` 都读它，绝不能看到矛盾组合）
- `configStore.setCloseAction` 选中 `tray` 时连带打开
- `setShowTrayIcon(false)` 直接拒绝（`ErrTrayIconRequired`）

**没有面板级 `minimizeToTray`**：面板的最小化按钮就是普通最小化，进托盘只发生在「关闭」时。

### 三态设置一律用字符串枚举

`theme` / `language` / `sessionMode` / `passwordAutosave` 都是**字符串枚举 + 空串即默认值**。理由：默认值是「跟随系统 / 默认开启」，bool 表达不了，且老配置缺键时零值 `false` 会被当成「用户显式关掉了」。

> 给 `settings` 新增「默认开启」的布尔项时，必须按「键在不在」补默认值（`settingsHasKey`）。

### 已废弃的旧字段

`load()` 读配置时会清掉并写回。**写测试或脚本改配置时必须一并清掉这些键**，否则加载时的迁移会覆盖新值：

- 旧 `closeAction` → 归一化进 `panelCloseAction`（旧值 `quit` 归一为 `close`）
- `managerCloseAction` → 直接清掉（管理窗口的关闭动作是固定的）
- 面板级 `minimizeToTray` → 清掉（功能已收归应用级设置）
- 旧 `shortcuts` 数组 → 收敛成单个 `shortcut`

### 配置导出 / 导入

`App.ExportConfig` / `App.ImportConfig`，只管 config.json 的内容（图标缓存可重建、登录态不该跨机器搬）。

- **校验失败时现有配置一个字节不动**。预检必须在 `load()` 之前做 —— `load()` 对坏 JSON 的语义是「回退默认并写回」，直接 load 用户文件会把垃圾悄悄变成默认配置。
- 成功前备份为 `config.json.bak`；导入后 `syncTray` + 主题双窗口 + 双广播收尾。**已打开的面板窗口不关**。
- 用户取消对话框返回 `false`，不算错误。

### 后端改设置必须广播

`App.notifySettingsChanged` → Wails 事件 `paneldock:settings-changed`。**前端没有轮询**，新增任何「后端自行改设置」的路径都要广播。典型情形：选中「最小化到托盘」会连带把托盘图标打开。

## 运行时数据位置与便携模式

便携模式（`<exe目录>\data\` 存在时自动启用，约定同 VSCode 的 `data` 目录）：

| | 便携模式 | 非便携 |
|---|---|---|
| 配置 | `<exe目录>\data\config.json` | `%APPDATA%\PanelDock\config.json` |
| 会话/profile | `<exe目录>\data\WebViewProfiles\<tabID>` | `%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>` |
| 图标缓存 | `<exe目录>\data\icons\<面板ID>.ico` | `%APPDATA%\PanelDock\icons\<面板ID>.ico` |

整个「exe + data」文件夹拷走即完成迁移，登录会话随行。检测函数 `resolvePortableRoot`（包级变量，测试可注入）。

`build/bin/PanelDock.exe` + `build/bin/data/` 是本地开发实例的数据所在。`wails build` 的 `Clean Bin Dir` 为 false，data 不会被清掉。
