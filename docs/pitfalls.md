# 踩坑清单

**只记「编译、`go vet`、单测全都放行，却照样出错」的事。** 每条都是改某块代码前要扫一眼的东西。

行为契约在 `docs/behavior.md`，机制在 `docs/architecture.md` —— 这里不复述它们，只补充「哪一步会静默失败」和「为什么」。

---

## 面板窗口：无边框 + 自绘标题栏

- **`WM_NCCALCSIZE` 两条分支都要处理**。网上配方常只写 `if (wParam == TRUE) return 0;`，但**创建期发来的偏偏是 `wParam == FALSE`**。只判 TRUE → 系统标题栏保留、与自绘标题栏叠成两条。`wParam == FALSE` 时 `lParam` 是 `RECT*`（进：建议窗口矩形；出：客户区屏幕坐标），写 `GetWindowRect` 结果即可让客户区 = 窗口矩形。
- **`setupPanelFrameless` 里的 `SetWindowPos(SWP_FRAMECHANGED)` 不能省**：窗口管理器缓存创建期算出的边框，不重算上面那次判定可能一直不生效。同函数还得 `DwmExtendFrameIntoClientArea(1,1,1,1)` 找回 DWM 阴影、`DWMWA_WINDOW_CORNER_PREFERENCE = DWMWCP_DONOTROUND` 关 Win11 圆角（子窗口是直角）。
- **缩放边框靠「让出来」**：子窗口在还原状态下四边内缩 `win32FrameBorder`(8px)，让出的这圈归父窗口，`panelFrameHitTest` 在那里回 `HTLEFT/HTTOP/...`；顶部另有 `win32FrameTopStrip`(4px) 透明条给 `HTTOP`。最大化时内缩为 0（`frameInset()`）。
- **拖动 / 双击最大化 / 右键系统菜单靠 `HTTRANSPARENT`**：标题栏子窗口在按钮以外区域返回它，命中落到父窗口 → 父窗口在 `cy < titleBarHeight + tabBarHeight` 时回 `HTCAPTION`。**这条横带必须覆盖标题栏 + 标签栏两个高度**，只写标题栏的话标签右侧空白会被判 `HTCLIENT`，怎么拖都拖不动。
- **命中测试与点击必须共用同一份几何**：「是不是标签」用 `tabIndexAt`、「是不是配色按钮」用 `themeButtonHit`，两处公式各自只能有一份。标签的绘制上限与命中上限也是同一个函数（`panelTabBarTabLimit`）。
- `tabIndexAt` 必须显式挡掉 `x < tabBarPadding`：Go 整数除法向零截断，左侧 8px 会被算成 `-8/164 = 0` 而误判成 0 号标签。
- **标题栏子窗口必须带 `WS_CLIPCHILDREN`**：地址栏 EDIT 是它的子窗口，没这个样式时每次重绘都会把 EDIT 整条盖掉。
- **`WM_CTLCOLOREDIT` 由标题栏处理**（EDIT 的配色消息发给父窗口），颜色一律从 `p.chrome()` 取。
- 地址栏回车导航补协议头（无 `://` 则补 `http://`）；`Esc` 还原为当前 URL。

### 窗口状态持久化

**必须跳过最小化窗口**（`captureBounds`）：最小化时 `GetWindowRect` 返回哨兵矩形 `(-32000,-32000,160,28)`，写进配置就变成「下次打开缩成小方块、还落在屏幕外」。两道防线：`IsIconic` + `plausibleWindowRect`。

**记录必须排在 `DestroyWindow` 之前**（`destroyPanelWindow`）：`DestroyWindow` 同步触发 `WM_DESTROY` → `dispose`，那时已经读不到矩形。写在 `dispose` 里的记录是死代码 —— 它编译、vet、单测、E2E 全绿，却从未执行过。

读配置时走 `initialWindowRect` 做同一道校验，退回 `defaultPanelWindowWidth/Height` —— **必须与 `config.create` 用同一对常量**。

## 绘制与像素

- **32bpp DIB 的字节序是 BGRA，`image.RGBA.Pix` 是 RGBA —— 必须逐通道换位，不能用 `copy`**。`copy` 会把 R 写进 B 的位置（纯红 favicon 显示成纯蓝），而这个 bug 编译 / vet / 单测 / E2E 全绿（E2E 只断言 `WM_GETICON` 非零），**只有逐像素采样标题栏才能发现**。
- **两处绘制的 alpha 约定不同**：标题栏走 `AlphaBlend`，要**预乘** DIB（`bitmapFromImage`，`image.Image.RGBA()` 返回的正好是预乘值）；图标走 PNG，要**直通** alpha（`panelHIconFromImage` 内 `png.Encode` 会把预乘还原）。搞反的症状是半透明边缘「一圈发黑」，16×16 上肉眼几乎看不出来。
- **颜色一律写 `0xRRGGBB`，要 COLORREF 时过 `panelColorRef`**。别写 `0x00FAA560 // #60a5fa` 那种靠注释才读得懂的常量 —— BGR 与 RGB 写反了编译 / vet / 单测全绿，只有肉眼看得出来。
- **`paintTabBar` / `paintTitleBar` / `WM_CTLCOLOREDIT` 一律从 `p.chrome()` 取色**，不许出现裸色值。
- **窗口类的背景刷注册时就定死、改不了**，所以三个窗口过程都要自己处理 `WM_ERASEBKGND`；窗口类里那个 `Background:` 只是收到主题前的兜底（= 深色）。
- **换主题是跨线程的**：`requestTheme` 只挂 `pendingTheme` + `PostMessage(win32WMSetTheme)`，真干活的是 UI 线程上的 `applyPendingTheme`。配色没变就直接返回 —— auto 模式下 `WM_SETTINGCHANGE` 会因别的原因频繁到来。
- **`WM_SETTINGCHANGE` 要过滤**：只有 lParam 指向 `"ImmersiveColorSet"` 才与主题有关（`isImmersiveColorSet`），区域 / 字体 / 辅助功能都会发同一条消息。

> **由此得一条通用规则：改了绘制代码就必须做一次像素级复验**，别只看「有没有东西画出来」。

## 图标链路（`icons_windows.go`）

- **「多来源」的理由**：任务栏要大图标（32×32 起，高 DPI 48），标题栏只要 16×16。只认第一个 `<link rel=icon>` 会得到一张被拉糊的任务栏图标。优先级：manifest `icons[]` → `apple-touch-icon` → `link[rel~=icon]` → `/favicon.ico` 兜底（**页面已声明图标时不再猜**）。一次最多下 8 个。
- **`panelPickIcon` 是「够大的里挑最小的」**，不是挑最大的：512 缩到 16 会糊，原生 16 才清晰；都不够大时才挑最大的。
- **ICO 是容器，每一帧都要解出来**（`decodeICOAll`，内嵌 PNG 与 BMP+AND 掩码都支持）—— 一张 favicon.ico 塞 16/32/48 三帧是常态。
- **HTML 里的图标要在页面里解析、在 Go 里下载**：`ExecuteScript` 一次性把 manifest 地址 / apple-touch-icon / link icon / `document.baseURI` 摊平成 JSON 返回，相对地址在页面里转成绝对地址（Go 侧拿不到 `document.baseURI`）。manifest 只回传地址、内容由 Go 取（它是独立文件，页面里读不到）。
- **解析失败别静默**，也**不要交一个空 payload 冒充成功** —— 空 payload 会让下游画「首字母色块」，以假乱真。取不到图标就画首字母色块（monogram）。

### `ExecuteScript` 的返回值形态

返回的是**「脚本完成值的 JSON 编码」**，不是字符串内容。脚本写 `return out;`（对象）→ raw 是 `{"manifest":...}`；写 `return JSON.stringify(out);` → 拿到的是 `"{\"manifest\":...}"`，**外面多一层引号并转义**，直接 `json.Unmarshal` 必失败。`panelDecodeIconPayload` 两种形态都收。

失败路径是**静默 return 0**，症状是「favicon 永远不出现、日志一片干净」。

> **favicon 不显示 ≠ favicon 逻辑错。** 先怀疑 `ICoreWebView2` vtable 错位 —— 脚本根本没执行，自然什么都取不到。

### 造 HICON 走 PNG，不要走 `CreateIconIndirect`

`panelHIconFromImage`：重采样 → `png.Encode` → `CreateIconFromResourceEx`。旧实现用 `CreateIconIndirect`，**稳定返回 NULL 且 `GetLastError` 恒为 0**，真实原因是 `panelICONINFO` 漏了 `xHotspot`/`yHotspot`（结构应为 32 字节，两个位图句柄全部错位）。

> 推广规则：① **任何 Win32 结构体都用 `unsafe.Offsetof` 核一遍偏移量**，别凭字段名猜。② **返回 NULL 而 `GetLastError` 为 0 时，先怀疑自己的参数结构体**，不要靠「加重试」掩盖 bug。

### 任务栏图标会被自己创建的快捷方式劫持

**Explorer 按窗口的 AppUserModelID 匹配「已知应用」条目（快捷方式 / 固定项），匹配上就一律用那个条目的图标，窗口自己的 `WM_SETICON` 被无视。** 作祟的正是本程序自己创建的桌面快捷方式（`shortcut_windows.go` 会 `SetIconLocation` 指回 exe）：它的 AUMID 就是 exe 路径，与**不声明 AUMID 时进程的隐式 AUMID 完全相同**。

- 判别实验：有快捷方式指向 → 任务栏是 exe 图标；改名成没人指向的新文件名 → 立刻显示站点图标；再给它造一个快捷方式 → 又被劫持回去。根治是 `applyAppUserModelID()`。
- **只有「运行中且未固定」的窗口按钮会跟着站点走**，固定项那份是静态的（走 .lnk 图标）。
- **别用真的任务栏做断言**：它依赖快捷方式、固定项、任务栏可见性，做不成稳定回归。守它的是 `TestApplyAppUserModelIDSetsExplicitID`（防 `shell32` 导出名写错导致静默 no-op）+ 人工逐像素判别。

## WebView2 手工 COM：vtable 槽位必须逐条对齐

面板窗口是手工 COM 子集 —— `pkg/webview2` 的自动生成回调在 Go 1.25 下会 `panic: compileCallback: argument size is larger than uintptr`。

**不能折叠 `IUnknown` 到目标方法之间的槽位，也不能漏掉中间任何一个方法** —— 漏一个 = 其后全部错位，且**编译、vet、单测全不报错**。

调用到 `Stop`（槽位 43）：

```
GetSource=4  Navigate=5  NavigateToString=6  add_NavigationStarting=7
add_SourceChanged=11  add_HistoryChanged=13  add_NavigationCompleted=15
ExecuteScript=29  Reload=31  get_CanGoBack=38  get_CanGoForward=39
GoBack=40  GoForward=41  GetDevToolsProtocolEventReceiver=42  Stop=43
```

**千万别漏 `NavigateToString`** —— 漏掉它其后整体错位一格，症状极具迷惑性：`add_*` 打在 `remove_*` 上（返回 `S_OK` 却永不回调）、`ExecuteScript` 实际是 `RemoveScriptToExecuteOnDocumentCreated`、`get_CanGoBack` 实际是 `get_BrowserProcessId`（返回 PID → **恒为 true**）。

核对基准现成可用：依赖里 `go-webview2/pkg/webview2/ICoreWebView2.go` 就是同一份 ABI 的生成物，逐行对齐即可。

> 铁律：**别信「返回 S_OK」，要信「事件到没到」。**
> **别用「NULL 探针」自证槽位**：`add_NavigationStarting(this, NULL, &token)` 返回 `E_INVALIDARG` 看着像验证通过，但 `NavigateToString(this, NULL)` 同样返回 `E_INVALIDARG` —— 参数校验相似的方法互相冒充，结论完全反了。

### 其余 COM 约定

- **事件回调挂在创建 controller 的 UI 线程上**；handler 对象**必须存进 `tabState.eventHandlers`** —— COM 侧引用 Go GC 看不见，不存就可能被回收。
- **手工 COM 回调对象的 `QueryInterface` 统一走 `panelQueryInterfaceIUnknown`**（只承认 `IID_IUnknown`）。**不要写成「任何 IID 都返回 S_OK」**：万一 QI 的是别的接口（如 `IMarshal`），它随后会按那个接口的槽位调我们，而我们的 vtable 只有 `IUnknown + Invoke`，后面是野指针。
- **`ICoreWebView2Settings4` 必须对 Settings 对象 QI**，对 `ICoreWebView2` 直接 QI 必然失败。槽位（IUnknown 0–2 之后按官方 IDL 数）：Settings1 3–20、Settings2 21–22、Settings3 23–24、Settings4 25–28，`put_IsPasswordAutosaveEnabled` = **26**。
  **依赖里 `ICoreWebView2Settings{,2,3,4}.go` 不能当基准**：它把每个 SettingsN 当独立接口、只嵌 `IUnknownVtbl`，与真实 ABI 对不上。
- **`panelController2Vtbl`**：`GetDefaultBackgroundColor`=26、`PutDefaultBackgroundColor`=27，**是这张表末尾**。加方法前先核基线。
- **WebView2 默认底色**（`Controller2::put_DefaultBackgroundColor`）：深色下不设它，每次开面板 / 切页都先闪一记白。**所有标签都要设一遍**。`COREWEBVIEW2_COLOR` 是 `{A,R,G,B}` 4 字节，**按值**传（压成 uintptr）。拿不到 Controller2 时静默跳过。

## Win32 / COM 通用

- **API 所属 DLL 必须核对**：`SetBkMode`、`SetTextColor`、`SelectObject`、`CreateSolidBrush`、`CreateFontW`、`GetStockObject` 属 `gdi32.dll`；`FillRect`、`DrawTextW` 属 `user32.dll`。`syscall.NewLazyDLL` 惰性解析导致写错 DLL 在编译 / vet / test 全不报错，**首次绘制才 panic**。
- **`syscall.NewLazyDLL` 调用不存在的导出会 panic**：版本相关 API（如 `GetDpiForWindow`）必须先 `proc.Find()` 再 `Call`。
- **`CoInitializeEx` 的 `S_FALSE` 要单独识别**：`golang.org/x/sys/windows` 把任何非 0 HRESULT 都转成 error，`S_FALSE`(1) 于是变成 `ERROR_INVALID_FUNCTION`。`shortcutSFalse` 显式处理它（仍配对一次 `CoUninitialize`）。
- **COM 初始化必须 `runtime.LockOSThread`**：goroutine 若在 `CoInitializeEx` 与 `CoUninitialize` 之间迁移线程，那根线程会永久停在 STA，后续落到它的调用拿到 `S_FALSE`（**间歇性、难复现**）。`shortcutWithCOM` 已加锁线程。
- **`go vet` 的 unsafeptr 检查**拒绝 `unsafe.Pointer(uintptr)`。两个合规写法：`panelCOPYDATASTRUCT.LpData` 直接声明为 `*uint16`；WndProc 的 `lParam` 声明为 `unsafe.Pointer`。
- 手写 Win32 结构体（`panelMSG`、`panelNOTIFYICONDATA`、`panelTRACKMOUSEEVENT`）已按 x64 ABI 核对过，改动需重新核对对齐。

## Wails 集成

- **`runtime.Quit` 会先询问 `OnBeforeClose`**：钩子返回 true 就把退出请求整个吞掉。轻量模式必须先置 `App.quitting` 再退出（见 `requestQuit`）。
- **前端字段名必须用 json tag 的小写驼峰**（`panel.id`、`panel.tabs`），不能用 Go 字段名大写。`frontend/wailsjs/go/models.ts` 是权威参考。
- **⚠️ 新增 App 桥接方法后必须核对 `frontend/wailsjs/go/main/App.js` 里真的有它**。本项目路径含中文，`wails build` / `wails generate module` 的绑定生成会**静默失败**（日志正常、时间戳不变，但新方法不进 `App.js`/`App.d.ts`，前端 import 时 vite 报 "not exported"）。修法是按既有格式**手工补** `App.js`（`window['go']['main']['App']['方法名']`）与 `App.d.ts`。挪到纯 ASCII 路径后可再用生成器。
- **改了前端必须去掉 `wails build -s` 的 `-s`**（它是跳过前端构建，留着则 dist 里是旧产物）。
- **窗口原生底色必须联动，否则启动白闪**（`theme_windows.go`）：三道防线 —— `main.go` 在 `wails.Run` **之前**读配置算初始底色（startup 里再改已晚）；`startup` 里 `syncWindowTheme()` 幂等对齐；前端每次 `applyTheme` 调 `App.ApplyWindowTheme`。**两侧 `--bg` token 与 `windowBackgroundLight/Dark` 必须同值**。
- **防闪脚本在 `index.html` 的 `<head>`**：Wails 绑定首帧前不可用，所以用 localStorage 镜像设置值（键 `paneldock.theme`）。**config.json 才是事实源**，localStorage 只是读透缓存。
- **`SetTheme` / `SetLanguage` 不广播 settings-changed**（与其他 setter 的刻意差异）：发起方自己 await 成功后就地生效，广播只多一次无意义重读。但 `SetTheme`**要**把生效配色推给已打开的面板（`applyThemeToPanels`）；反过来**面板窗口那个配色按钮必须广播**。
- **`data-theme` 挂 `<html>` 而不是 `#app`**：`main.js` 是整块 `innerHTML` 重渲染的。CSS 全部走语义 token，`app.css` 里不允许写裸色值；`color-scheme` 必须跟着 `data-theme` 一起设，否则原生 `<select>` / 滚动条 / `<dialog>` 的 `::backdrop` 在深色下穿帮。

## 询问框

- **不解析 `CREATESTRUCT`**：在 `CreateWindowExW` 返回之后才创建子控件（而非 `WM_CREATE` 内）；窗口过程在映射表查不到状态就交回 `DefWindowProcW`。
- Tab 导航 / 回车 / Esc 交给 `IsDialogMessageW`，但它只应处理「对话框自身或其子窗口」的消息（同线程，用 `IsChild` 过滤）。
- **询问框挂哪个 owner 要看可见性**：`enablePromptOwner()` 只在管理窗口**可见**时用它 —— `FindWindowW` 连隐藏窗口也能找到，拿它当 owner 会让询问框居中到看不见的位置。
  代价：转发路径拿到的是**别的线程**（Wails 主线程）的窗口，`EnableWindow(owner, FALSE)` 是跨线程同步消息，询问框可能晚几百毫秒才显示。不会挂死。E2E 因此要**等询问框「可见」再去点**。
- **面板名放进标题前按字符截断**（16 字符 + 省略号）：按字节切会把中文切成乱码。
- **连续两次请求不会叠出两个询问框**：`enablePromptMu` 串行化，后到的请求等前一个选择后**重新读配置**（那时往往已启用，于是直接打开）。

## 会话 / 密码 / 删除的实际坑

- **⚠️ 删掉 profile 目录后它会被重建（实测）**：`os.RemoveAll` 成功之后目录会又冒出来，带着一棵空的 `EBWebView` 树 —— 那是尚未退干净的浏览器进程重建的，不是会话残留。两条规则：① 只删一次不可靠，必须 `clearUntilStable`（连续 3 次检查都不存在，每次间隔 250ms）；② **别用「目录是否存在」当判据** —— 目录在不在是时序问题，「会话数据在不在」才是承诺本身（E2E 因此在 profile 目录里搜本次会话真实写下的 Cookie 名与 localStorage token）。
- **关闭后清空必须同步做**（`dispose()` 里、`doneOnce` 之前）：① `restartPanel` 是「关掉 → 等 done → 立刻重开」，异步清理会与新会话抢同一目录；② 关闭最后一个面板时进程 200ms 后就退出，异步清理很可能被砍掉。
- **打开前清空失败要让本次打开失败并报错**：宁可让用户看到「面板打不开 + 原因」，也不能悄悄带着上次的登录会话打开。
- **`sessionMode` 不做加载期迁移**（缺字段 / 空串 / 非法值一律按 `persist`，且 `load()` **不写回**）—— 否则每次启动都给便携配置添字段，「跑完 E2E 配置零污染」这条检查就废了。对比 `closeAction` 那种必须写回的迁移，取舍不同。
- **`panelWindow.clearOnClose` 是构造时快照**，不在关闭时回查配置。
- **前端 `submitForm` 里 `SetPanelSessionMode` 与 `SetPanelPasswordAutosave` 必须排在 `UpdatePanelTabs` 之前** —— 后者会重开运行中的面板，重开时读的是那一刻的配置。
- **`setPasswordAutosave` 里 COM 调用放在 `p.mu` 之外**（与 `setAlwaysOnTop` 同因：拿着锁调外部对象，对方回调进本窗口就自锁）。
- **`DeleteTab` 与 `DeletePanel` 的取舍相反**：WebView2 销毁不可逆，所以删标签时清理失败**必须照常更新配置**（否则标签下次又回来、数据永远没人清），错误如实报给用户、由「重置数据」兜底；而删面板是「清不掉就整件事中止」。
- **重置数据：先关窗口再清**。顺序反了**不会报错**只会清不干净，而用户看到的是「重置成功」。

## 桌面快捷方式 / 固定到任务栏 / 图标缓存

- `SHGetFolderPathW` 与 IShellLink 都要求 COM 已初始化，`shortcutWithCOM` 包住全部步骤，不要先取桌面路径再进 COM（会 E_FAIL）；它必须 `runtime.LockOSThread` 且识别 `S_FALSE`。
- **本机桌面重定向到 E 盘时 `CSIDL_DESKTOPDIRECTORY`(0x0a) 返回 E_FAIL**，须用 `CSIDL_DESKTOP`(0x00)。
- **`taskbarpin` 动词必须整条删掉，不能「试一下不行再回退」**：shell 认不出这个动词时**静默退化成 `open`**（返回成功、`err=nil`，目标程序真被启动）。留着它的副作用就是用户点了「固定任务栏」却白开一个面板窗口。
- **改写 .lnk 必须「读回来再整份重写」**（`setShortcutIcon`）：`IShellLinkW` 只有整份 `Save`，新建的对象是空的 —— 不先 `GetPath`/`GetArguments`/`GetDescription` 就 `SetPath`，会得到**双击没反应的空壳，而返回值全是 `S_OK`**。
- **DIB 帧的 `biHeight` 必须写两倍高度**（ICO 把 XOR 位图与 AND 掩码描述成上下拼接的一张图）；像素**自底向上**存。
- **帧里的 alpha 是直通，不是预乘** —— `panelResample` 产出预乘值，出帧前必须过 `panelToNRGBA` 反预乘（`png.Encode` 也要 NRGBA，它对 RGBA 会再乘一遍）。搞反的症状是半透明边缘发亮。
- **`listPanelShortcutsForIcon` 扫桌面 + 任务栏固定目录**（Windows 固定时把 .lnk **复制**过去，是两份独立文件），但**不要把固定目录并进 `listPanelShortcuts`** —— 那是删面板时的清理范围，扩大会顺手删掉用户亲手固定的项目。
- **删面板的收尾顺序不能反**（`dropPanelIconCache`）：先把还指着这份缓存的 .lnk 图标改回 exe 自带图标，**再**删 .ico；只动图标确实等于我们那份缓存的 .lnk（`readShortcutIconPath` 比对）。
- **改完必须通知 shell**（`SHChangeNotify` + `SHCNE_UPDATEITEM` + `SHCNF_FLUSH`；动过固定项目录再补 `SHCNE_UPDATEDIR`）。不通知的症状是「改了但看不见」。
- **.ico 要多档尺寸**（16/32/48/64/128/256）：只塞一帧 → shell 把 16 拉大成 256，某些尺寸下糊。≤48 用 DIB 帧，>48 用 PNG 帧。源图不够大的档位直接跳过，不做放大（例外是 16，列表视图的最低要求）。

## 托盘

- **菜单项 ID：动态恢复项从 `trayMenuRestoreBase`(100) 起递增，固定项用 1..3。占位项不能复用 ID 0** —— `TPM_RETURNCMD` 用 0 表示「菜单被取消」，撞上就把「没点」当成「点了某项」。菜单建好时把窗口对象快照进 slice（而不是只存 ID 回头再查）。
- **销毁侧必须兜底清队列**：窗口销毁是直接改字段的（`dispose` 里 `p.hiddenInTray = false`），不走 `setHiddenInTray`，所以 `DeletePanel` 与 `onPanelClosed` 里各清一次。取出时还要复核「窗口还在 + `hiddenInTray` 仍为真」，否则会列一条点了没反应的幻影项。
- **`a.mu` 不可重入**：任何自查并加锁的 helper（`trayDropToggleTarget`）都不能在**已经持锁**的段落里调用。`DeletePanel` / `onPanelClosed` 是典型的持锁区（先取引用、再一口气改完几份状态），在那里调加锁版会直接死锁 —— 单跑托盘用例是绿的，只有 `TestDeletePanel*` 会挂住（实测撞过 600s 超时）。这类 helper 一律配对提供 `xxxLocked()` 版本。
- **双击必须有独立于队列的记忆**：队列里只有**藏着**的窗口，一经恢复就出队了，所以「第二次双击收回去」寻址不到目标 —— 靠 `trayToggleID` 记住上次碰过的那个，队列只负责第一次取目标。
- **藏进托盘不等于关掉**：窗口只是隐藏，`App.panels` 里还留着它。任何用 `panelCount()` 判「还有没有窗口」的逻辑都会把它算进去 —— `closeSkipsTrayHide` 早先挂在「这是最后一个面板」上，结果开两个面板时第一个先被藏起来、第二个永远轮不上「最后一个」，两个窗口一起赖在托盘里，进程再也不退出（用户实测就是这个现象）。判「轻量模式要不要跳过托盘」只能看模式本身与 `trayUsed`，不要碰面板计数。
- **菜单里的「取回」和「彻底关闭」必须分层**：原生菜单一行只有一个命令 ID，想在行内塞第二个可点位置就得自绘，代价不成比例；把关闭并列成第二组又会让「丢掉一个窗口」与「取回一个窗口」同样容易。用 `MF_POPUP` 挂子菜单（`trayAppendCloseSubmenu`），多一步抵达。子菜单由父菜单级联销毁，**不能自己再 `DestroyMenu`**。
- Windows 11 默认把新图标折叠进「隐藏的图标」浮出层（点 `^`），首次可能看不到 —— 这不是程序问题。

## 测试与验证

### 两条铁律

1. **新写的用例必须做反向验证**：把修复撤掉重建、确认用例变红。第一版 E2E 有两条断言写成恒真的空断言（配置 URL 与最终 URL 相同、只断言「有人来取过图标」），全靠反向验证才发现。
2. **任何「会写用户目录」的路径都必须走注入缝**（`shortcutDesktopDirectory` / `resolvePortableRoot` 这类包级变量），否则测试会往**用户真实桌面**写 .lnk、往真实 profile 目录写会话数据。

### 改配置的三个坑

1. **还原必须无条件**：即使补丁没有实际改动也要写回原始字节。被测程序自己会写配置（关闭行为设成 tray 会连带打开托盘图标、勾「记住我的选择」会写入关闭行为），跳过还原会把便携包改坏、让后续用例静默跳过。
2. **补丁要清掉已废弃字段**（旧 `closeAction` / `managerCloseAction`）：加载时的迁移会重写它们，可能覆盖刚写入的值。
3. 「关闭最后一个面板 → 进程退出」取决于 `lightweightQuitOnLastPanel`：需要这一前提的用例必须自己打开它（`e2eForceLightweightQuit`；顺带要「每次询问」的用 `e2eForceCloseActionAsk`）。

`e2ePatchSettings` 是把整个 settings 节点换掉，不是在原始 JSON 上做小改动。

### E2E 清单

只写「守什么」和「写这条时容易踩的变量」；用例本体在 `e2e_windows_test.go`。

| 用例 | 守什么 / 坑 |
|---|---|
| `TestE2ELightweightSingleInstanceAndQuit` | 轻量启动 + 转发 + 干净退出。「面板可见」必须 `waitForCondition` —— 窗口对象在 `ShowWindow` 之前就已存在 |
| `TestE2EClosePromptPanelOnlyAffectsItself` | **「关闭只影响本面板」核心回归** |
| `TestE2ECloseRememberIsPersisted` | 跨进程改控件用 `BM_SETCHECK` 而非 `BM_CLICK` |
| `TestE2EManagerCloseOnlyClosesItself` | **收尾必须 `Kill` + `waitForNoInstance`**（结束时进程还活着，会占着单实例锁） |
| `TestE2ETrayIconRegistered` / `…Disabled` | 用 `Shell_NotifyIconW(NIM_MODIFY)` 探测（内含 uid=99 反向自检）。都不带 `--open` |
| `TestE2EManagerCloseKeepsTrayWhenNoPanels` / `…QuitsWhenNoTrayAndNoPanels` | 管理窗口固定关闭动作的两半 |
| `TestE2ETrayIconForcedByMinimizeToTray` | 故意写矛盾配置，断言被纠正并写回。`…Disabled` 的前置必须把关闭行为摆成非 tray |
| `TestE2EFreshSessionClearedOnClose` | **不要断言「目录不存在」**；必须先断言会话数据真的落盘 |
| `TestE2EDisabledPanelAsksToEnable` / `TestE2EForwardedOpenOnDisabledPanel` | **轮次之间必须 `Process.Kill()` + `waitForNoInstance`**：第一轮只留在托盘，它持有的单实例锁会把第二轮转发过去 |
| `TestE2EDirtyWindowStateFallsBackToDefault` | 哨兵值 `(-32000, 160x28)` 的兜底（可反向验证） |
| `TestE2ECloseRemembersLastWindowPosition` | 关闭面板时最后位置真的落盘。**可反向验证**：撤掉 `destroyPanelWindow` 里销毁前的 `recordBounds` 即红。刻意**不**补发 `WM_EXITSIZEMOVE` —— 否则记录不只依赖关闭前那一次 |
| `TestE2EMinimizedPanelKeepsLastWindowState` | **现状钉子，单点不可反向验证**：最小化时 `captureBounds` 直接跳过，配置保留上一次正常位置。要变红须同时撤掉三道防线（`IsIconic` + `plausibleWindowRect` + `plausibleWindowState`），撤任一道都被下一道兜住。判据本身由 `window_state_test.go` 的纯函数单测守着 |
| `TestE2EFramelessWindowHasCustomTitleBar` | 只查骨架，不比像素。**用例自带窗口尺寸**，别拿用户配置里的面板做样本 |
| `TestE2ETitlebarFollowsRealNavigation` | 查**事件与图标真的活过来了**（骨架用例对 vtable 错位完全无感）：① 配置 URL 与最终 URL **必须不同**；② `/icon.png` 只认 `PanelDock/` UA 前缀（判别器在 `panelFetchBytes`）；③ `WM_GETICON(ICON_BIG)` 非零（只返回显式设置过的图标，不会假阳性） |
| `TestE2ETabBarThemeButtonSwitchesPanelChrome` | 断言配置真的变**且像素跟着换**。坐标必须是客户区坐标；前置把 `theme` 钉成显式 dark |

### E2E 通用约定

- **窗口定位按标题找**（`waitForWindowByTitle(t, panelWindowTitle(name))`）—— 多面板同时打开时不能用「按类名找第一个」。询问框靠标题栏文案定位；「有没有询问框」用 `findWindowByClass(promptWindowClassName)`。
- **点按钮**统一走 `clickPromptButton(t, hwnd, buttonID)`（跨进程 `WM_COMMAND`）。
- 用例读写便携配置并在结束时按原字节还原；检测到已有真实实例在运行时自动跳过（**串跑会因此跳过若干用例，需单独重跑**）。
- `e2eSkipUnlessReady` 在「配置里没有启用面板」时**不再静默跳过**：它自己临时启用第一个面板。跳过 ≠ 通过。

### 测试地图

| 文件 | 覆盖什么 |
|---|---|
| `config_test.go` | 加载/原子写/损坏回退/旧字段迁移（含 `shortcuts` 收敛） |
| `session_test.go` / `session_windows_test.go` | 会话模式归一化、两种清理策略、`preparePanelProfile` |
| `shortcut_test.go` | 快捷方式创建/覆盖/收敛/删除联动 |
| `taskbar_windows_test.go` | 固定状态回读、不抢前台、别的项不算数 |
| `iconcache_windows_test.go` / `icons_windows_test.go` | 多帧 ICO 往返、DIB 方向与通道序、按边长择优、`LoadImageW` 认这份 .ico |
| `theme_test.go` / `theme_panel_windows_test.go` | 配色 token、`panelColorRef` 换位、标签上限不压按钮、命中与矩形同源 |
| `tab_order_test.go` / `tab_delete_test.go` | 重排保 id、`DefaultTabIndex` 重映射、删标签清数据 |
| `reset_panel_test.go` / `delete_panel_windows_test.go` | 重置不改配置、先关后清顺序、清数据失败即中止 |
| `tray_test.go` / `window_state_test.go` / `titlebar_test.go` | 托盘不变量、窗口状态哨兵值、标题栏骨架 |
| `native_text_windows_test.go` | 词典完整性（map 字面量编译器帮不上忙，少填一个键运行时才以「菜单少一项」暴露）、`normalizeLanguage`、往返 |
| `enable_prompt_windows_test.go` | 停用询问框 spec 与按钮 ID 唯一性 |
| `orphan_profile_test.go` / `aumid_windows_test.go` | 孤儿 profile 目录、AUMID 真的设上了 |
| `docs_test.go` | 文档行数上限与中英 README 结构一致（见 `docs/doc-policy.md`） |

### 没有自动化框架时的真机验证法

Python + ctypes 从窗口 DC `BitBlt` 一小条出来存 PNG，配合 `SendMessageW(hwnd, WM_LBUTTONDOWN, ..., (y<<16)|x)` 模拟点击。**坐标必须是客户区坐标** —— 误传屏幕坐标时点击静默失效，`SendMessage` 照样返回 1，看着像成功。
