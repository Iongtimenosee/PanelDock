# AGENTS.md

PanelDock：Windows/Wails 桌面工具，用**隔离的 WebView2 会话**打开局域网管理页面（路由器、OpenClash、NAS…）。主窗口是 Wails/WebView2，面板窗口是**手工 Win32 + 手工 COM**自建的无边框窗口。

本文件是**唯一入口**，只放「改代码前必须先知道的事」。细节分层放在 `docs/`，按需再读。

## 硬边界（任何时候都不得越）

1. **不向远程页面注入任何 DOM / 脚本 / WebMessage** —— 远程页面与宿主零通信通道。
2. **面板窗口不暴露任何桥接**：无 `window.external`、无 HostObject、不处理 WebMessage。
3. **配置文件里没有任何密码字段**。密码由 WebView2 自己加密存进各标签 profile；`PanelConfig.PasswordAutosave` 只是「让不让 WebView2 保存」的开关值。
4. **只支持 http/https 地址**。

## 心智模型（五条，看懂就不容易改错）

1. **两套隔离的窗口体系**：主窗口走 Wails（前端 JS + 桥接），面板窗口走自建 Win32 子窗口（**无桥接**）。谁能弹原生框、谁受 Wails 生命周期管，差异都源于此。
2. **创建方式决定死法**：管理窗口是 Wails 的窗口，**一销毁 = 「没有窗口了」= Wails 立刻结束进程**并带走所有面板窗口。所以管理窗口的关闭只能是「藏起来」。面板窗口是自建的，可随意销毁。
3. **标签 = 浏览器身份**：每个标签一个独立 WebView2 Environment + UserDataFolder（`WebViewProfiles\<tabID>`）。换 id 就是换身份，登录会话全丢。
4. **两份事实源**：`config.json` 是事实源，localStorage 只是防首帧闪烁的读透缓存。前端**没有轮询**，后端自行改的设置与面板必须广播。
5. **Windows API 会静默失败**：S_OK 不代表做对了，返回 NULL 时 `GetLastError` 可能是 0。这套代码里大量「做完必须验证」的写法不是啰嗦。
6. **轻量模式会被「用过托盘」让位**：`trayUsed` 为假时它跳过「藏到托盘」直接关窗口；置位之后轻量实例与常规实例一致。它只管这一步，**不参与**进程寿命判定（那仍归 `quitWhenNoWindows` 单点）。

## 文档索引

| 文件 | 何时读 |
|---|---|
| `docs/architecture.md` | 改窗口生命周期、单实例、配置层、设置项、数据路径之前 |
| `docs/behavior.md` | 改任何用户可见行为之前（行为契约速查，一条一段结论） |
| `docs/pitfalls.md` | **改绘制 / Win32 / COM / WebView2 vtable / 图标编码 / E2E 之前必扫** |
| `docs/doc-policy.md` | 写文档或注释之前（含硬上限与演进规则） |
| `README.md` / `README.zh-CN.md` | 面向最终用户，改动功能时同步（英文为默认落地页） |

## 文件地图

| 文件 | 职责 |
|---|---|
| `main.go` | Wails 入口，绑定 App；最先做单实例判定与请求转发 |
| `app.go` | 主窗口桥接 API：面板/标签 CRUD、打开/关闭、置顶/托盘、重置数据、导出导入配置 |
| `config.go` | `config.json` 的加载/原子写入/损坏回退/旧版迁移；便携模式路径；应用级 `settings` 节点 |
| `ipc_windows.go` | 单实例互斥体 + 隐藏 IPC 窗口（`WM_COPYDATA`）；**该窗口同时承载应用级托盘图标** |
| `tray_windows.go` | 应用级托盘图标与菜单（`Shell_NotifyIconW`，挂在常驻 IPC 窗口上）；`App.trayNeeded` / `App.syncTray` |
| `prompt_modal_windows.go` | **原生模态询问框公共设施**（窗口类 + 窗口过程 + 控件/字体/DPI/居中/消息循环，`promptModalSpec` 驱动） |
| `close_prompt_windows.go` | 「关闭面板窗口」询问框。**只有面板窗口会问**，管理窗口的关闭动作是固定的 |
| `enable_prompt_windows.go` | 「面板已停用」询问框；`mainWindowHandle()` 也在这里 |
| `native_text_windows.go` | 原生界面文案词典（i18n 的 Win32 侧），zh-CN / en-US 两份 |
| `apperror.go` | 面向用户的错误码（`appError` + `errCode/errCodeDetail/errCodeWrap`） |
| `panel_window_windows.go` | 独立面板窗口（**无边框**）：原生标签栏、窗口状态记忆、外壳 `WM_ERASEBKGND` 自绘 |
| `titlebar_windows.go` | 面板窗口自绘标题栏：命中测试、地址栏 EDIT、导航按钮、导航事件接入 |
| `icons_windows.go` | 站点图标**从哪来、要哪几个尺寸**：多来源候选 → 下载解码 → 按边长择优 → 标题栏 DIB + 任务栏 HICON |
| `iconcache_windows.go` | 图标**落盘缓存**与 `InspectPanelIcon` / `ApplyPanelIcon` 两阶段桥接 |
| `aumid_windows.go` | `main()` 最早处声明显式 AppUserModelID（**不声明会被自己创建的快捷方式劫持任务栏图标**） |
| `password_autosave_windows.go` | 把分组级「保存登录密码」开关送到 WebView2（`Settings4::put_IsPasswordAutosaveEnabled`） |
| `shortcut_windows.go` | 桌面快捷方式（IShellLinkW + IPersistFile），含读回既有 .lnk 与联动清理 |
| `taskbar_windows.go` | 「固定到任务栏」的准备与引导；固定状态回读判定 |
| `session.go` | 会话清理策略：删整个 profile 目录 |
| `theme_windows.go` | 明暗主题后端侧：读 Windows 深浅色偏好、解析生效主题、管理窗口原生底色联动 |
| `theme_panel_windows.go` | 面板窗口外壳配色表与换肤（跨线程 `requestTheme` / `applyPendingTheme`） |
| `frontend/src/main.js` | 主窗口 UI（原生 JS，无框架），全部文案经 `t()` 取自词典 |
| `frontend/src/i18n/` | 前端词典两份（zh-CN 源语言 / en-US），随 vite 编译进产物 |

## 三条最容易踩的规则

1. **两条关闭路径必须分清**：用户点 X 是 `WM_CLOSE`（交互式，按设置询问）；程序内部关窗口走 `win32WMDirectClose`（`WM_APP+2`，永不询问）。新增任何「程序内部关窗口」的代码一律走 `p.close()`，别直接 `PostMessage(WM_CLOSE)`；反过来标题栏关闭按钮**必须**走 `WM_CLOSE`。
2. **不要从 Wails 绑定方法里弹原生模态框** —— 那会在 WebView2 的消息处理线程上自建 `GetMessage` 循环。原生询问框只能由 Win32 侧路径触发。
3. **后端自行改设置必须广播**（`notifySettingsChanged` → `paneldock:settings-changed`）。前端没有轮询，不广播就会显示过期状态。

其余规则见 `docs/architecture.md`（机制）与 `docs/behavior.md`（行为）。踩过的坑在 `docs/pitfalls.md`。

## 构建与验证

- **工具链不写死**：工程目录由两台机器同步共享，Go / Wails / Node / gcc 的路径与版本因机而异。契约只有 `go.mod`：**Go 1.25.0+**，wails 对齐 `v2.16.0`。开工前先 `go version` / `wails version` / `node -v` 自检。
- `wails build` 需要 npm 在 PATH。缺了就修 PATH，别固化成某种 shell 调用方式（不同机器上 `cmd /c` 可能可用、可能被拦截）。
- **回归基线（全部必须通过）**：`go vet ./...`、`go test ./...`、`wails build`。别用 `-s`（跳过前端构建，dist 里留旧产物）。
- 端到端（默认跳过）：`PANELDOCK_E2E=1 go test -run TestE2E -v .`
- 快捷方式集成（默认跳过）：`PANELDOCK_DESKTOP_TEST=1 go test -run TestCreateDesktopShortcutIntegration -v .`
- 跑完 E2E 建议 `diff` 一次 `build/bin/data/config.json`，确认用户配置没被改动。

**两条不可妥协的测试规矩**（理由见 `docs/pitfalls.md`）：

1. **新用例必须反向验证**：撤掉修复重建、确认用例变红。否则极易写出恒真的空断言。
2. **任何「会写用户目录」的路径必须走注入缝**（`shortcutDesktopDirectory` / `resolvePortableRoot` 这类包级变量）。否则测试会往用户真实桌面写 .lnk、往真实 profile 目录写会话数据。

## 写文档 / 注释前

读 `docs/doc-policy.md`。摘要：文档共 5 份、各有硬上限；注释只写「为什么」，禁止复述本文档里已有的规则（写指针即可）。
