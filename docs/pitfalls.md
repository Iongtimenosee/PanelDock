# PanelDock 踩坑清单

> 这是 AGENTS.md 的配套参考。**改代码之前，先扫一眼对应小节** —— 下面每一条都是编译、`go vet`、甚至单测全都放行的坑，只能靠人记住。
> AGENTS.md 讲「现在是怎么做的 / 有哪些硬规则」，本文件讲「为什么会这样 / 哪一步会静默失败」。两者不重叠，别把这里的内容搬回去。

## 面板窗口：无边框 + 自绘标题栏

布局自上而下三段：**自绘标题栏（40px）→ 原生标签栏 → WebView2**。三段都靠 `panelWindow.resize()` 统一摆位，`contentBounds()` 是唯一的内容区分界来源。

标题栏从左到右：favicon → 后退/前进/刷新/停止（Segoe MDL2 字形按钮）→ 地址栏（原生 `EDIT` 子窗口）→ 置顶开关 → 打开管理面板（齿轮）→ 最小化/最大化/关闭。标签栏最右端是**明暗配色切换按钮**（正好落在标题栏「关闭」正下方）。

### 坑：无边框与拖拽

- **`WM_NCCALCSIZE` 的两条分支都必须处理**。网上流传的无边框配方通常只写 `if (wParam == TRUE) return 0;`，但**窗口创建期系统发来的偏偏是 `wParam == FALSE`**。只判 `TRUE` 会让系统标题栏原样保留、与自绘标题栏叠成两条（实测客户区 1104x721 vs 窗口 1120x760）。`wParam == FALSE` 时 `lParam` 是 `RECT*`，语义是「进：建议窗口矩形；出：客户区的屏幕坐标」，把它写成 `GetWindowRect` 的结果即可让客户区等于窗口矩形。
- **`setupPanelFrameless` 里的 `SetWindowPos(SWP_FRAMECHANGED)` 不能省**：窗口管理器会缓存创建期算出的边框，不重算的话上面那次判定可能一直不生效。同函数里再 `DwmExtendFrameIntoClientArea(1,1,1,1)` 找回 DWM 阴影（客户区被子窗口盖满，这 1px 不可见）、`DWMWA_WINDOW_CORNER_PREFERENCE = DWMWCP_DONOTROUND` 关掉 Win11 圆角（子窗口是直角，圆角会被它们的角戳穿）。
- **缩放边框靠「让出来」**：客户区 = 窗口矩形之后四边没有非客户区可拖。做法是子窗口在还原状态下四边内缩 `win32FrameBorder`(8px)，让出的这一圈归父窗口，父窗口的 `WM_NCHITTEST`（`panelFrameHitTest`）在那里回 `HTLEFT/HTTOP/...`。顶部另有 `win32FrameTopStrip`(4px) 透明条留给 `HTTOP`。最大化时内缩为 0（`frameInset()`）。
- **拖动 / 双击最大化 / 右键系统菜单靠 `HTTRANSPARENT`**：标题栏子窗口在按钮以外的区域返回 `HTTRANSPARENT`，命中测试落到父窗口 → 父窗口在 `cy < titleBarHeight + tabBarHeight` 时回 `HTCAPTION`，于是这些交互全是系统原生行为。地址栏 `EDIT` 是独立子窗口，命中的是它自己。
- **父窗口那条 `HTCAPTION` 横带必须同时覆盖两个子窗口的高度**（标题栏 + 标签栏）。只写标题栏高度的话，标签右侧那片空白会被判 `HTCLIENT`，鼠标在上面怎么拖都拖不动窗口。标签栏的 `tabBarHitTest` 同理：标签本身与配色按钮回 `HTCLIENT`，其余回 `HTTRANSPARENT`。
- **命中测试与点击必须共用同一份几何**：`tabBarHitTest` 判「是不是标签」就用 `tabIndexAt`、判「是不是配色按钮」就用 `themeButtonHit`，两处公式各自只能有一份。
- `tabIndexAt` 必须显式挡掉 `x < tabBarPadding`：Go 的整数除法向零截断，左侧 8px 内边距会被算成 `-8/164 = 0` 而误判成 0 号标签（吃掉那 8px 的拖拽区，点击也会莫名切到 0 号）。
- **标签的绘制上限与命中上限是同一个函数**（`panelTabBarTabLimit`）：标签只画到配色按钮之前，超出上限的既不画也不可点（面板标签数按设计就是个位数，不做滚动/溢出折叠）。两边算得不一样就是「看得见点不到」或「点得到看不见」。
- **标题栏子窗口必须带 `WS_CLIPCHILDREN`**：地址栏 `EDIT` 是它的子窗口，没有这个样式时父窗口每次重绘都会把 `EDIT` 整条盖掉。
- **`WM_CTLCOLOREDIT` 由标题栏处理**（EDIT 的配色消息发给父窗口）：返回画刷 + `SetTextColor`/`SetBkColor`，颜色一律从 `p.chrome()` 取，别写死。
- 地址栏回车导航会补协议头（`navigateFromAddressBar`，无 `://` 则补 `http://`）；`Esc` 还原为当前 URL。

### 坑：窗口状态持久化

**必须跳过最小化窗口**（`panelWindow.captureBounds`）：最小化时 `GetWindowRect` 返回的是 Windows 的哨兵矩形 `(-32000,-32000,160,28)`（「图标位置」），写进配置就等于「下次打开这个面板缩成一个小方块、还落在屏幕外」。两道防线：`IsIconic` 挡最小化；`plausibleWindowRect`（纯函数）再挡哨兵坐标与退化矩形。

读配置时走 `initialWindowRect` 做同一道校验，退回 `defaultPanelWindowWidth/Height` —— **必须与 `config.create` 用同一对常量**，否则同一个面板会因为历史脏数据拿到与新建面板不同的尺寸。

## 图标链路（`icons_windows.go`）

一次页面扫描 → 多候选下载 → 按尺寸分别择优 → 标题栏与任务栏各取所需。

- **为什么要「多来源」**：锁到任务栏时系统要的是**大图标**（32×32 起，高 DPI 下 48），标题栏左上角只有 16×16。只认第一个 `<link rel=icon>` 会得到一张被拉糊的任务栏图标。来源优先级：manifest `icons[]`（常带 192/512）→ `apple-touch-icon`（常 180）→ `link[rel~=icon]` → 根目录 `/favicon.ico` 兜底。**页面已声明图标时不再去猜 `/favicon.ico`**。一次最多下 8 个（`panelIconMaxFetch`）。
- **`panelPickIcon` 的规则是「够大的里挑最小的」**，不是「挑最大的」：把 512 缩到 16 会糊，原生 16 才清晰；都不够大时才挑最大的。同尺寸并列按来源优先级决胜。
- **ICO 是容器，必须把每一帧都解出来**（`decodeICOAll`，内嵌 PNG 与 BMP+AND 掩码两种帧都支持）。一张 favicon.ico 里塞 16/32/48 三帧是常态，只挑一张等于放弃另外两个尺寸。
- **HTML 里的图标要在页面里解析、在 Go 里下载**：`ExecuteScript` 一次性把 manifest 地址 / apple-touch-icon / link icon / `document.baseURI` 摊平成 JSON 返回，相对地址在页面里就转成绝对地址（Go 侧拿不到 `document.baseURI`）。manifest 只回传地址、内容由 Go 取 —— 它是独立文件，页面里读不到（要跟 CSP 与同源策略斗）。
- **解析失败别静默**：任何回调用不上时都要留下痕迹，否则「什么都没发生」比报错更难查。同理，解析不出来时返回 false 保持默认图标，**不要交一个空 payload 冒充成功** —— 空 payload 会让下游画出「首字母色块」，以假乱真。
- 取不到图标就画**首字母色块**（monogram，`panelMonogramImage`）：圆角方底色由主机名 FNV 哈希稳定选取，字形用 GDI 画到黑底 DIB 上**只取灰度当覆盖率**再在 Go 侧合成。

### 坑：`ExecuteScript` 的返回值形态

`ExecuteScript` 返回的是**「脚本完成值的 JSON 编码」**，不是字符串内容。脚本写 `return out;`（对象）时 raw 就是 `{"manifest":...}`；写 `return JSON.stringify(out);`（字符串）时拿到的是 `"{\"manifest\":...}"` —— **外面多一层引号并转义**，直接 `json.Unmarshal` 到结构体必然失败。`panelDecodeIconPayload` 两种形态都收。

失败路径是**静默 return 0**，症状是「favicon 永远不出现、日志一片干净」，排查方向很容易被带偏去怀疑 vtable 槽位。所以有这条通用规则：

> **favicon 不显示 ≠ favicon 逻辑错。** 先怀疑 `ICoreWebView2` vtable 错位（见下一条）—— `ExecuteScript` 落到错误槽位上时脚本根本没执行，自然什么都取不到。

### 坑：像素格式（两处约定不同，别搞混）

- **32bpp DIB 的字节序是 BGRA，而 `image.RGBA.Pix` 是 RGBA —— 必须逐通道换位，不能用 `copy`**。`copy` 会把 R 写进 B 的位置，**纯红的 favicon 显示成纯蓝**。这个 bug 编译、`go vet`、单测、E2E 全部照常通过（E2E 只断言 `WM_GETICON` 非零，跟颜色无关），只有**逐像素采样标题栏**才能发现。
- **两处绘制的 alpha 约定不同**：标题栏走 `AlphaBlend`，要**预乘** DIB（`bitmapFromImage`；`image.Image.RGBA()` 返回的正好是预乘值）；图标走 PNG，要**直通** alpha（`panelHIconFromImage` 内部 `png.Encode` 会把预乘还原成直通，不用手写反预乘）。premultiplied 搞反的症状是半透明边缘「一圈发黑」，16×16 上肉眼几乎看不出来。

> 由此得一条通用规则：**改了绘制代码就必须做一次像素级复验**，别只看「有没有东西画出来」。

### 坑：造 HICON 走 PNG，不要走 `CreateIconIndirect`

`panelHIconFromImage`：重采样到目标边长 → `png.Encode` → `CreateIconFromResourceEx`。一次调用、不用造掩码、不用管 DIB 方向，且 PNG 的语义就是直通 alpha。

旧实现用 `CreateIconIndirect`，它**稳定返回 NULL 且 `GetLastError` 恒为 0**。真实原因是 `panelICONINFO` 结构体漏了 `xHotspot`/`yHotspot`：整个结构只有 24 字节、`hbmMask` 落在偏移 8（应 16）、`hbmColor` 落在 16（应 24），系统读到的两个位图句柄全是错位的（该结构在 x64 上是 4+4+4+4填充+8+8 = 32 字节）。

> 推广规则：**任何 Win32 结构体都要用 `unsafe.Offsetof` 打出偏移量核一遍**，别凭字段名猜。
> **调用返回 NULL 而 `GetLastError` 为 0 时，优先怀疑参数结构体，而不是系统的脾气；「加重试」是在掩盖自己的 bug。**

### 坑：任务栏图标会被自己创建的快捷方式劫持

**Explorer 会按窗口的 AppUserModelID 去匹配「已知应用」条目（快捷方式 / 固定项），匹配上就一律用那个条目的图标，窗口自己的 `WM_SETICON` 被无视。** 症状：「标题栏 favicon 正常、任务栏永远是 exe 内嵌图标」。

作祟的正是**本程序自己创建的桌面快捷方式**（`shortcut_windows.go` 会 `SetIconLocation` 指回 exe）：它的 AUMID 就是 exe 路径，与**不声明 AUMID 时进程的隐式 AUMID 完全相同**，于是每个面板窗口都被匹配走。

判别实验：同一个面板同一个 exe，有快捷方式指向它 → 任务栏是 exe 图标；复制改名为没人指向的新文件名 → 立刻显示站点图标；**再给这个新名字造一个快捷方式，它又被劫持回去**。根治办法是 `applyAppUserModelID()`（`aumid_windows.go`）声明一个不会被任何快捷方式携带的 AUMID，加完之后同一个 exe、同一个快捷方式启动，任务栏恢复站点图标。

- **只有「运行中且未固定」的窗口按钮会跟着站点走。** 固定项那一个图标是静态的（走 .lnk 的图标）。要让固定项也显示站点图标，得把站点图标缓存成 .ico 并改写那份 .lnk 的 `SetIconLocation` + 通知 shell —— 「刷新图标」按钮就是干这个的，它是用户显式点出来的动作，不是自动改写。
- 这条也解释了为什么**不该用真的任务栏去做断言**：它依赖桌面快捷方式、固定项、任务栏可见性，做不成稳定的回归。守它的是两层 —— `TestApplyAppUserModelIDSetsExplicitID`（防 `shell32` 导出名写错导致静默 no-op）+ 逐像素判别实验（人工）。

## WebView2 手工 COM：vtable 槽位必须逐条对齐

面板窗口是手工 COM 子集实现 —— `pkg/webview2` 的自动生成回调在 Go 1.25 下会 `panic: compileCallback: argument size is larger than uintptr`。

**不能折叠 `IUnknown` 到目标方法之间的槽位，也不能漏掉中间任何一个方法** —— 漏一个 = 其后全部错位，且**编译、`go vet`、单测全都不报错**。

当前调用到 `Stop`（槽位 43），其余：

```
GetSource=4  Navigate=5  NavigateToString=6  add_NavigationStarting=7
add_SourceChanged=11  add_HistoryChanged=13  add_NavigationCompleted=15
ExecuteScript=29  Reload=31  get_CanGoBack=38  get_CanGoForward=39
GoBack=40  GoForward=41  GetDevToolsProtocolEventReceiver=42  Stop=43
```

**千万别漏 `NavigateToString`（`Navigate` 之后那一个）** —— 漏掉它，其后所有槽位整体错位一格，症状极具迷惑性：`add_*` 实际打在 `remove_*` 上（返回 `S_OK` 却**永不回调**）、`ExecuteScript` 实际是 `RemoveScriptToExecuteOnDocumentCreated`（脚本根本没跑）、`get_CanGoBack` 实际是 `get_BrowserProcessId`（返回 PID → **恒为 true**）。

核对基准现成可用：依赖里 `github.com/wailsapp/go-webview2` 的 `pkg/webview2/ICoreWebView2.go` 就是同一份 ABI 的生成物，逐行对齐即可。

> 铁律：**别信「返回 S_OK」，要信「事件到没到」。**
> **别用「NULL 探针」自证槽位**：`add_NavigationStarting(this, NULL, &token)` 返回 `E_INVALIDARG` 看着像验证通过，但 `NavigateToString(this, NULL)` 同样返回 `E_INVALIDARG` —— 参数校验相似的方法互相冒充，结论完全反了。

### 其余 COM 约定

- **事件回调挂在创建 controller 的 UI 线程上**：`add_NavigationStarting/SourceChanged/HistoryChanged/NavigationCompleted` 驱动地址栏文本、前进后退可用态、刷新/停止切换、favicon 拉取。handler 对象**必须存进 `tabState.eventHandlers`** —— COM 侧的引用 Go GC 看不见，不存就可能被回收（回调里 `AddRef/Release` 都返回 1，永不释放）。
- **手工 COM 回调对象的 `QueryInterface` 统一走 `panelQueryInterfaceIUnknown`**（只承认 `IID_IUnknown`，其余 `E_NOINTERFACE`）。**不要写成「任何 IID 都返回 S_OK」**：万一运行时 QI 的是别的接口（如 `IMarshal`），它随后会按那个接口的槽位调我们的方法，而我们的 vtable 只有 `IUnknown + Invoke`，后面是野指针。不必担心「不承认就收不到回调」—— 这几个回调都在本进程内被直接调用、不走跨进程封送。
- **`ICoreWebView2Settings4`（密码保存）必须对 Settings 对象 QI**：`get_Settings` 拿到的是 `ICoreWebView2Settings`，`QueryInterface(ICoreWebView2Settings4)` 要打在**它**身上；对 `ICoreWebView2` 直接 QI 必然失败。槽位（IUnknown 0–2 之后，按官方 IDL 逐条数）：Settings1 3–20、Settings2 21–22、Settings3 23–24、Settings4 25–28，其中 `put_IsPasswordAutosaveEnabled` 是 **26**。
  **依赖里 `go-webview2/pkg/webview2/ICoreWebView2Settings{,2,3,4}.go` 不能当基准**：它把每个 SettingsN 当成独立接口、只嵌 `IUnknownVtbl`，槽位与真实 ABI 对不上。`password_autosave_windows.go` 里按完整继承链手写的 vtable 已真机验证。
- **`panelController2Vtbl` 的槽位**：`GetDefaultBackgroundColor`=26、`PutDefaultBackgroundColor`=27，**是这张表的末尾**。多写或少写一个方法就会越界取到野指针，加方法前先核基线。

## Win32 / COM 通用陷阱

- **Win32 API 所属 DLL 必须核对**：GDI 绘制函数（`SetBkMode`、`SetTextColor`、`SelectObject`、`CreateSolidBrush`、`CreateFontW`、`GetStockObject`）属 `gdi32.dll`；`FillRect`、`DrawTextW` 属 `user32.dll`。`syscall.NewLazyDLL` 惰性解析导致写错 DLL 在编译/vet/test 全不报错，**首次绘制才 panic**。新增 API 声明先查 MSDN 的 Header/DLL 标注。
- **`syscall.NewLazyDLL` 调用不存在的导出会 panic**：对 Windows 版本相关的 API（如 `GetDpiForWindow`，Win10 1607+）必须先 `proc.Find()` 探测再 `Call`。
- **`CoInitializeEx` 的 `S_FALSE` 要单独识别**：`golang.org/x/sys/windows` 的包装把任何非 0 HRESULT 都转成 `error`，`S_FALSE`(1) 于是变成 `ERROR_INVALID_FUNCTION`，与「未知失败」无法区分。`shortcutSFalse` 常量显式处理它（仍配对一次 `CoUninitialize`）。
- **COM 初始化必须 `runtime.LockOSThread`**：COM 单元是「每线程」状态，goroutine 若在 `CoInitializeEx` 与 `CoUninitialize` 之间迁移线程，会留下错配 —— 那根线程永久停在 STA，后续落到它的调用拿到 `S_FALSE` 而报 `CoInitializeEx: Incorrect function`（**间歇性、难以复现**）。`shortcutWithCOM` 已加锁线程。
- **`go vet` 的 unsafeptr 检查**拒绝 `unsafe.Pointer(uintptr)` 转换。处理 `WM_COPYDATA` 时两个合规写法：`panelCOPYDATASTRUCT.LpData` 直接声明为 `*uint16`（布局同 LPVOID）；WndProc 的 `lParam` 声明为 `unsafe.Pointer`（`windows.NewCallback` 支持该参数类型）。
- 手写 Win32 结构体（`panelMSG`、`panelNOTIFYICONDATA`、`panelTRACKMOUSEEVENT`）已按 x64 ABI 核对过内存布局，改动需重新核对对齐。

## Wails 集成陷阱

- **Wails 的 `runtime.Quit` 会先询问 `OnBeforeClose`**：若该钩子返回 true（阻止关闭），退出请求会被整个吞掉（`Frontend.Quit` 直接 return）。轻量模式下必须先置 `App.quitting` 再调用退出（见 `requestQuit`），否则「关闭最后一个面板即退出」永远失效。
- **前端字段名必须用 json tag 的小写驼峰**（`panel.id`、`panel.tabs`、`tab.url`），不能用 Go 字段名大写（`panel.ID`）。Wails 线上数据遵循 `encoding/json` 序列化结果；`frontend/wailsjs/go/models.ts` 是字段名的权威参考。
- **⚠️ 新增 App 桥接方法后必须核对 `frontend/wailsjs/go/main/App.js` 里真的有它**。本项目路径含中文，`wails build` / `wails generate module` 的绑定生成会**静默失败** —— 日志正常结束、时间戳不变，但新方法不进 `App.js`/`App.d.ts`，前端 import 它时 vite 直接报 "not exported"。
  修法是按既有格式**手工补** `App.js`（`window['go']['main']['App']['方法名']`，运行时按方法名动态解析，真身在 `main.go` 的 Bind）与 `App.d.ts` 的条目。挪到纯 ASCII 路径后可再用生成器。
- **改了前端必须去掉 `wails build -s` 的 `-s`**（它是「跳过前端构建」，留着则 dist 里是旧产物）。

## 界面配色（明暗主题）

管理窗口与面板窗口共用同一个设置项 `settings.theme`：`auto`（默认，跟随 Windows 深浅色）/ `light` / `dark`。两个入口写同一个字段：管理窗口右上角 ◐ 按钮、面板窗口标签栏最右端的配色按钮。应用设置里那个下拉也是它。两处都是「切到哪边就固定成哪边」，不会停在 `auto`。

**配色只管外壳，不管页面**：面板里显示的是别人的页面，本程序不注入样式，页面深浅由站点自己决定。面板侧能跟着走的只有 GDI 外壳的颜色和 WebView2 的默认底色。

### 前端侧

- **CSS 全部走语义 token**（`style.css`）：明暗两套映射挂在 `<html data-theme>`（约 40 个 token），`app.css` 里**不允许再写裸色值** —— 新增颜色先在 `style.css` 给两个主题各配一份。`color-scheme: light/dark` 必须跟着 `data-theme` 一起设，否则原生 `<select>`、滚动条、`<dialog>` 的 `::backdrop` 在深色下仍是浅色样式，一眼穿帮。
- **`data-theme` 挂 `<html>` 而不是 `#app`**：`main.js` 是整块 `#app.innerHTML` 重渲染的，挂在它身上或里面都会被下一次 render 抹掉。
- **窗口原生底色必须联动，否则启动白闪**（`theme_windows.go`）：`options.App.BackgroundColour` 在 WebView 首帧之前、窗口缩放空隙里露出。三道防线：`main.go` 在 `wails.Run` **之前**读配置算出初始底色（startup 里再改已经晚了）；`startup` 里 `syncWindowTheme()` 再对齐一次（幂等）；前端每次 `applyTheme` 调 `App.ApplyWindowTheme` 同步（auto 模式下系统切换深浅色时，只有前端自己知道要换）。**两侧 `--bg` token 与 `windowBackgroundLight/Dark` 必须同值**，对不上的症状是窗口边缘一圈异色。
- **防闪脚本在 `index.html` 的 `<head>`**：Wails 绑定首帧前不可用，拿不到后端配置，所以用 localStorage 镜像「设置值」（键 `paneldock.theme`，每次 `applyTheme` 写入；只存三态设置，不存生效值），head 内联脚本在 CSS 生效前解析出 `data-theme`。**config.json 才是事实源**，localStorage 只是读透缓存：`applySettings` 每次都以后端归一化值重放 `applyTheme`（幂等），两者永远只差一个启动瞬间。
- **`SetTheme` 不广播 settings-changed**（与其他 setter 的刻意差异）：前端那个入口 await 成功后立刻 `applyTheme`，广播只会多一次无意义重读。但它**要**把生效配色推给已打开的面板窗口（`applyThemeToPanels`），否则管理窗口切了、面板窗口还是老配色。反过来，**面板窗口那个按钮必须广播**（`App.togglePanelTheme`）。
- **auto 跟系统靠两条同源的判定**：后端 `systemThemePreference`（可注入，单测别依赖跑测试这台机器的深浅色设置）读注册表 `HKCU\...\Themes\Personalize\AppsUseLightTheme`；前端监听 `matchMedia('(prefers-color-scheme: dark)')` 的 change 事件即时跟进。显式值压过系统偏好。

### 面板窗口侧（`theme_panel_windows.go`）

- **颜色一律写 `0xRRGGBB`，要 COLORREF 时过 `panelColorRef`**。别写 `0x00FAA560 // #60a5fa` 那种必须靠注释才读得懂的常量 —— **BGR 与 RGB 写反了编译、vet、单测全绿，只有肉眼看得出来**，这是这套代码里最容易抄错的地方。
- **`paintTabBar` / `paintTitleBar` / `WM_CTLCOLOREDIT` 一律从 `p.chrome()` 取色**，不许出现裸色值。新增颜色先加到 `panelChrome`。面板外壳全是 GDI 画的，用不上 CSS token，所以另给一份**语义同名**的颜色表（`bg`/`text`/`hover`/`pressed`/`activeTab`/`field`/`accent`…），两边对齐才不会出现「管理窗口偏蓝、面板窗口偏灰」。
- **窗口类的背景刷是注册那一刻定死的，改不了**，所以三个窗口过程都要自己处理 `WM_ERASEBKGND`（用当前 chrome 的 `brBg` 填）。窗口类里那个 `Background:` 只是「还没收到主题之前」的兜底（取 `panelDefaultChrome()` = 深色）。
- **标签栏与标题栏的绘制范围必须同源**：`panelTabBarTabLimit(width)` 同时被绘制和命中测试使用。它由 `panelTabBarThemeRect` 反推 —— 标签只画到配色按钮之前。配色按钮的命中矩形也**只有这一份实现**，E2E 也用它算坐标。
- **换主题是跨线程的**：调用方可能是任意 goroutine，而重绘只能发生在窗口自己的 UI 线程，所以 `requestTheme` 只挂 `pendingTheme` + `PostMessage(win32WMSetTheme)`，真正干活的是 UI 线程上的 `applyPendingTheme`。配色没变就直接返回 —— auto 模式下 `WM_SETTINGCHANGE` 会因为别的原因频繁到来。
- **`WM_SETTINGCHANGE` 要过滤**：lParam 指向变了的那一项设置名，只有 `"ImmersiveColorSet"` 与主题有关（`isImmersiveColorSet`）。区域、字体、辅助功能都会发同一条消息，不过滤就会跟着瞎重绘。
- **WebView2 的默认底色**（`ICoreWebView2Controller2::put_DefaultBackgroundColor`）：页面自己没铺底的地方、以及首帧画出来之前露出的空白都吃它，深色下不设它每次开面板/切页都会先闪一记白。**所有标签都要设一遍**，不只是当前那个。`COREWEBVIEW2_COLOR` 是 `{A,R,G,B}` 的 4 字节结构体，**按值**传（压成 uintptr，不能传指针）。拿不到 Controller2（运行时太老）时静默跳过 —— 这只是观感，不值得报错打扰用户。

## 界面语言（i18n）

`settings.language`：`auto`（默认，跟随 Windows 显示语言）/ `zh-CN` / `en-US`。**词典编译进程序，没有外挂语言文件** —— 桌面工具没有「不发版加语言」的需求，外挂只多一个文件丢失/版本不齐的失败路径。加第三种语言 = 再写一份词典 + 重新编译。

翻译范围：管理窗口的 web UI、后端返回的用户可见错误、以及本程序**自己画的**原生界面（关闭/启用询问框、托盘菜单、托盘悬浮提示）。**不翻译**：WebView2 右键菜单、系统输入框等系统组件（跟随 Windows 显示语言）；面板窗口的标题栏/标签栏本来就没有文字。

- **两份词典、刻意不共享**：前端（`frontend/src/i18n/*.js`，vite 打包进产物）与 Win32 侧（`native_text_windows.go` 的 `nativeUITexts` map）。两边文案集合几乎不相交，共享一份源文件需要引入生成步骤，复杂度远超省下的那几条重复串。zh-CN 是源语言：前端缺 key 回落 zh-CN 再露 key 本身；Go 侧 `nativeText()` 取不到语言回落 zh-CN。
- **`App.nativeText()` 每次现读配置解析，不缓存** —— 托盘菜单本来就是每次右键现建的，询问框是每次弹时构造的，语言切完下一个弹出的框就是新语言，**不需要任何推送机制**。代价是每次读一次内存里的配置，纳秒级。
- **前端切换 = 落盘 + 镜像 + `location.reload()`**：事件监听是 innerHTML 渲染后一次性绑死的，「原地换语言」等于要求整个渲染流程可重入。首帧语言靠 localStorage 镜像（键 `paneldock.language`），套路同主题防闪。`SetLanguage` 因此**不广播**。
- **auto 的判定两侧同语义**：Go 侧 `systemLanguage()`（`GetUserDefaultUILanguage`，LANGID 主语言 0x04=中文 → zh-CN，其余 en-US）；前端 `resolveLocale`（`navigator.language` zh 开头 → zh-CN）。两处都**不硬猜前缀/大小写**：`zh`、`ZH-CN`、`fr-FR` 一律按 auto/拒绝处理。
- **面板名、标签名是用户数据，永远不翻译**；词典只管 UI chrome。
- **后端错误码化**（`apperror.go`）：到达用户的错误返回 `"panel.nameRequired"` 这类码（带技术细节时为 `"码: 细节"`），前端 `tErr` 按码查词典、查不到原样显示（内部技术错误、os 错误宁露细节不硬编）。哨兵错误用 `errCodeWrap` 包装，`errors.Is(err, ErrTrayIconRequired)` 仍成立。**改码名 = 改前端词典键（`err.` + 码），两边必须同步**。

## 询问框（`prompt_modal_windows.go`）

新增询问框 = **加一份 `promptModalSpec`**，不要复制窗口类/消息循环：类名、窗口过程、字体、DPI、居中、`IsDialogMessageW` 导航全在公共设施里。三件必须核对的事：

1. **按钮 ID 全局唯一**（窗口过程按 ID 分发，撞车会让两个询问框的按钮互相串味）
2. **正文行数要和 `BodyHeight` 匹配**（STATIC 不会自动长高，行多了会被裁掉）
3. **`Caption` 全局唯一** —— 端到端用例靠标题栏文案定位；窗口类**共用**，所以 `findWindowByClass` 只能回答「有没有询问框」，回答不了「是哪个」

其余约定：

- **不解析 `CREATESTRUCT`**：在 `CreateWindowExW` 返回之后才创建子控件（而非 `WM_CREATE` 内），避免手工声明这个易错的大结构体；窗口过程在映射表里查不到状态时就交回 `DefWindowProcW`。
- Tab 导航 / 回车触发默认按钮 / Esc 取消交给 `IsDialogMessageW`；但它只应处理「对话框自身或其子窗口」的消息（父窗口与对话框同线程，用 `IsChild` 过滤）。

### 关闭面板窗口的询问框

文案：「最小化到托盘」/「直接关闭」（只关这一个面板）/ 取消，外加「记住我的选择（以后关闭面板窗口不再询问）」。复选框语义只有这一种，写回 `panelCloseAction`（`App.commitCloseChoice`）。

标题栏 `PanelDock · 关闭面板窗口`（`closePromptLabels.Caption`）—— 端到端用例据此定位。文案从 `App.nativeText().ClosePrompt` 取。

在「关掉它就是最后一个面板、且此后没有任何窗口与托盘」时会多出一行提示（`panelWindow.closePromptNote` → `App.panelCloseEndsProcess`），如实告知「选直接关闭后程序会退出」。**提示存在时对话框自动加高**（正文 > 2 行 → 高版面），避免正文被静态控件裁掉。

## 停用面板的打开请求：询问是否启用

面板被停用后，桌面快捷方式与任务栏固定项**仍然指向它**（`--open <面板ID>`）。直接报错会让用户双击后看起来像没反应，分不清是被停用、程序没起来还是地址坏了 —— 因此改为弹原生询问框（`enable_prompt_windows.go`）：

- **启用并打开**（默认按钮，回车即选中）：落盘启用，然后照常打开。
- **保持停用**：返回 `App.ErrPanelKeptDisabled`，**静默收场** —— 不弹管理窗口、不报错。用户刚回答过那个问题，再补一个窗口弹出来等于没听他说话。
- **没有「记住我的选择」**：把它记成「以后自动启用」等于悄悄绕过用户的停用意图，停用功能本身就名存实亡了。

实现约定：

- **入口只有外部请求**：`App.openPanelFromExternalRequest` = `ensurePanelEnabledForExternalOpen` + `OpenPanel`，两个调用点是 `startup` 的 `--open` 异步补开与 `handleIPCCommand`（转发命令）。**管理界面里的「打开」按钮不走这里** —— 停用卡片上那个按钮本来就是灰的，而 Wails 绑定方法绝不能弹原生模态框。`App.OpenPanel` 保持纯粹的「按现有配置打开，停用则报错」。`openPanelFromExternalRequest` **不要**加 `Bind`。
- `confirmEnableDisabledPanel` 是包级**注入缝**：单测把它换成假实现，就不必真的弹框。
- **连续两次请求不会叠出两个询问框**：`enablePromptMu` 把「是否启用」串行化，后到的请求等前一个做出选择后**重新读配置** —— 那时面板往往已经启用，于是直接打开，不再问第二遍。
- **询问框挂哪个 owner 要看可见性**：`enablePromptOwner()` 只在管理窗口**可见**时用它。轻量模式 / 窗口已藏进托盘时主窗口是隐藏的，而 `FindWindowW` 连隐藏窗口也能找到 —— 拿它当 owner 会让询问框居中到用户看不见的位置（甚至屏幕外）。这种情况返回 0，居中到主屏幕。
  代价要心里有数：转发路径拿到的是**别的线程**（Wails 主线程）的窗口，`EnableWindow(owner, FALSE)` 因此是一次**跨线程同步消息**，会等到对方处理 WM_ENABLE —— 实测询问框可能晚几百毫秒才显示（对方正在初始化 WebView2 时最明显）。不会挂死：对方线程若真卡住，程序本来就已失去响应。E2E 对此有防护：**等询问框「可见」再去点**，否则会撞上「已创建但尚未显示」这一瞬。
- **面板名放进标题前要截断**（16 个字符 + 省略号，按**字符**截而不是字节）：标题 STATIC 只有一行高度，按字节切会把中文切成乱码。
- 面板启用状态是后端自己改的：改完必须 `notifyPanelsChanged()`，否则管理界面卡片上会一直挂着过期的「停用」标记（前端没有轮询）。

## 面板标签：增删与排序

- 不做独立的「单标签 / 分组」形态选择器：**标签数量自然决定形态**，1 个标签即单标签面板，多个即分组。
- 顺序即 `panels[].tabs[]` 的数组顺序，**没有也不需要有单独的排序字段**。`newPanelWindow` 顺序遍历 `cfg.Tabs` 建标签栏，所以「按排序打开」只是既有行为的直接结果 —— 前端排好序落盘即可，后端不需要为排序加任何代码。
- 编辑保存走 `UpdatePanelTabs` 整体替换标签列表，不要用「UpdatePanel + 循环 AddTab」（会删不掉旧标签）。
- 前端两条排序通道（`moveTab` + 挂在 `#pf-tabs` 上的 `dragover`/`drop`）：拖拽把手、↑ ↓ 按钮。**只有把手是 `draggable`** —— 把整个 `.pf-tab-item` 设为 draggable 会让里面的输入框没法用鼠标框选文本。拖拽过程中**只画落点提示线、不重排 DOM**：重排会重建节点，浏览器会当场中止拖拽；真正的移动只发生在 `drop` 那一刻。落点判定挂在容器上（不是每一项上），这样标签之间的缝隙也是有效落点。
- **排序必须搬整个标签对象（含 `tab.id`）**：id 决定 WebView2 profile 目录 `WebViewProfiles\<tabID>`，换了 id 等于换了浏览器身份，登录会话全丢。
- **`configStore.updateTabs` 会按标签 ID 重映射 `DefaultTabIndex`**：该字段是**下标**，只在旧顺序里有意义。重排后同一个下标指向另一个标签（标签变少时甚至越界），不修正就会出现「顺序排好了，打开激活的却是别人」。增、删、改名、排序全都走这一个写入口，所以一处修正即覆盖全部路径。
- `updateTabs` 会把传入切片**复制**一份再存（`AddTab` 复用旧切片容量，不复制会让调用方后续写入污染已保存配置）。
- **标签变更默认不整窗重开**：`UpdatePanelTabs` 只对「新增了标签」才 `restartPanel`（热创建一个新 WebView2 要走完整的 environment 异步链，不值得），删标签用 `panelWindow.removeTab` 热移除、改名/改址用 `updateTabInfos` 热更新。

## 会话状态：关闭后保留 / 清空

面板级开关 `panels[].sessionMode`：`persist`（默认，保留浏览器状态）/ `fresh`（关闭后清空，每次打开都是新环境）。

- **实现方式：整个 profile 目录删掉**（`session.go` 的 `removeProfileDir`）。profile 目录就是这个标签的浏览器身份本身 —— Cookie、Local Storage、IndexedDB、Service Worker、缓存、站点权限授权全在里面，目录结构还随 WebView2 版本变。逐类挑文件清理既会漏（漏一样就等于登录态还在）又要跟着上游改，**不要**改成那种实现。
- **两种删除策略**（`sessionClearPolicy`）：
  - `clearOnce`：删掉即走。用于**打开面板前**的兜底 —— 那一刻还没有浏览器进程，不存在「删了又被重建」。
  - `clearUntilStable`：删到目录**连续 3 次检查都不存在**为止（每次间隔 250ms）。用于**关闭面板后**：浏览器进程还在退出，会把目录重建回来，只删一次等于没清干净 —— 而「不留残留」正是这个功能的全部价值。确认之间必须真的等待，否则连续两次瞬时检查什么也证明不了。
- **两条挂接点，缺一不可**：
  - **关闭后清空**：`panelWindow.dispose()` 里**同步**执行（`doneOnce` 之前），用 `clearUntilStable`。为什么不能丢给 goroutine：① `restartPanel` 是「关掉 → 等 `done` → 立刻重开」，异步清理会与新会话抢同一个目录；② 关闭最后一个面板时进程 200ms 后就退出，异步清理很可能被砍掉。窗口此刻已销毁，几百毫秒等待用户看不到。
  - **打开前兜底**：`newPanelWindow` 走 `preparePanelProfile(tabID, fresh)`，fresh 时**先清空（`clearOnce`）再 MkdirAll**。这是「每次新开都是新环境」的**保证**所在：上次崩溃 / 强杀 / 关机没清干净的残留在这里被补掉。清空失败时**让本次打开失败并报错** —— 宁可让用户看到「面板打不开 + 原因」，也不能悄悄带着上次的登录会话打开。
- **⚠️ 删除后目录会被重建（实测）**：`os.RemoveAll` 成功之后目录会**又冒出来**，带着一整棵空的 `EBWebView` 树（Preferences / History / Login Data… 都是新初始化的）。那是尚未退干净的浏览器进程在退出时重建的，不是会话残留。两条规则：① 只删一次不可靠，必须删到稳定；② **别用「目录是否存在」当判据** —— 目录在不在是时序问题，「会话数据在不在」才是承诺本身（E2E 因此在 profile 目录里**搜本次会话真实写下**的 Cookie 名与 localStorage token）。
- **配置层不做加载期迁移**：`sessionMode` 缺字段 / 空串 / 非法值一律按 `persist` 处理（`normalizeSessionMode`），且 `load()` **不写回** —— 否则每次启动都给便携配置添上这个字段，「跑完 E2E 配置零污染」这条检查就废了（对比 `closeAction` 那种必须写回的迁移，两者取舍不同）。判断一律用 `PanelConfig.clearsSessionOnClose()`，不要自己比较字符串。
- `panelWindow.clearOnClose` 是**构造时快照**，不在关闭时回查配置：否则「打开时是保留、关闭前被改成清空」会让同一次关闭的后果不可预测。
- 前端两处入口：编辑表单的「关闭后的浏览器状态」单选组 + 面板卡片的「关闭即清空」勾选框。**`submitForm` 里 `SetPanelSessionMode` 必须排在 `UpdatePanelTabs` 之前** —— 后者会重开正在运行的面板，重开时读的是那一刻的配置，顺序反了新窗口就带着旧的处理方式。

## 保存登录密码（分组级，默认开启）

面板级开关 `panels[].passwordAutosave`：`on`（默认）/ `off`。开启时该分组的每个标签在 WebView2 创建后把 `IsPasswordAutosaveEnabled` 设为 true —— 登录页于是像 Edge 一样弹「保存密码」提示。用的是 WebView2 自带的密码管理，**本工具不读取、不导出任何密码**。

- 实现：`password_autosave_windows.go` 的 `applyPasswordAutosave`。调用点在 `panelWindow.controllerCompleted`，**必须在 `navigate` 之前**（登录页首屏加载时开关要已经生效，否则第一次不触发保存提示）。QI 失败（1.0.1108 之前的运行时没这个接口）静默跳过。
- **`IsPasswordAutosaveEnabled` 只管「保存」**（官方 `specs/Autofill.md` 明写）：设成 false 只是不再保存新密码、不再弹保存提示，**此前已经存下来的密码仍然会被建议与回填**。UI 文案一律写「不再保存新密码」，**不要**写成「关闭密码功能」；想彻底不留密码只能靠会话状态的 `fresh`。
- 默认开启又必须容忍「键缺失」，所以是**字符串枚举**（`on`/`off`，空串 = 默认）而不是 bool —— 与 `sessionMode` 同一套路。这里连 `settingsHasKey` 都借不上力：面板是数组，逐项回到原始 JSON 查键不划算。
- **当场生效**：`App.SetPanelPasswordAutosave` → `panelWindow.setPasswordAutosave` 立刻重设到该窗口所有已创建的标签。与会话处理方式的「只写配置、下次关闭时才起作用」刻意不同 —— 这就是一个 WebView2 属性，改完不生效用户只会以为开关坏了。重设是就地改属性，不重开窗口、不动用户当前页面。
- `setPasswordAutosave` 里 **COM 调用放在 `p.mu` 之外**（与 `setAlwaysOnTop` 同一考虑：拿着锁去调外部对象，对方回调进本窗口就自锁）。
- 前端 `submitForm` 里 `SetPanelPasswordAutosave` 同样必须排在 `UpdatePanelTabs` **之前**。
- 它与「关闭即清空」是同一条链路的两端，文案必须成对讲清：开了密码保存的分组若同时是 `fresh`，一关闭密码就跟着 profile 一起被清掉（那正是「彻底不留密码」的办法）。
- 前端 `submitForm` 里 `SetPanelPasswordAutosave` 同样必须排在 `UpdatePanelTabs` **之前**。

## 删除面板 / 删除标签

两者的共同点：**数据必须一起清**。profile 目录的身份就是标签 ID，面板或标签一删，那些目录就成了谁也访问不到的孤儿 —— 留着只是白占空间、还把已保存的密码继续留在盘上。

**`App.DeletePanel(id, removeShortcuts bool)`**

- **无条件清数据**，不再看面板的 `sessionMode`（默认 persist 的也要清），策略用 `clearUntilStable`（清理策略见「会话状态」一节）。
- **顺序是：关窗口 → 清数据 → 删快捷方式 → 删配置**。清数据失败就**整件事中止、面板保留**，绝不允许「面板没了、数据还在盘上」；快捷方式清单必须在配置删除前读（`listPanelShortcuts` 依赖配置里记录的 .lnk 路径）。
- **快捷方式清理是「显式勾选」的结果**（前端确认对话框里的可选项），没勾就一个 .lnk 都不动。也要先处理图标缓存的下游引用（见「刷新图标」）。
- **不会删除任务栏上已固定的项**：那份 .lnk 是 Windows 自己复制进固定目录的，取消固定是用户自己的事。

**快捷方式的匹配方法**：先读配置里记录的 `panels[].shortcut`（**只有一个**），再扫描桌面目录，用 `IShellLinkW.GetPath/GetArguments` 读回既有 .lnk，匹配「指向本程序（按文件名比对）+ 参数为 `--open <面板ID>`」—— 后者覆盖用户改名/移动过、以及本功能上线前手工创建的快捷方式。`deleteShortcutFiles` **只删 `.lnk` 后缀**，配置被改坏也不会误删普通文件。

**`App.DeleteTab` / `UpdatePanelTabs` 的差集路径**：该标签的 WebView2 经 `panelWindow.removeTab`（私有消息 `win32WMRemoveTab` 投递回 UI 线程、同步等待）单独销毁，**其余标签的页面不动、窗口不重开**；然后 `clearTabProfile` 删它的 profile 目录。**与 `DeletePanel` 的取舍相反**：WebView2 销毁不可逆，所以清理失败时配置**必须**照常更新（否则标签下次又回来、数据永远没人清），错误如实报给用户、由「重置数据」兜底。

## 重置分组数据（卡片上的「重置数据」）

`App.ResetPanelData(id)`：立刻删掉该面板**所有**标签的 profile 目录 —— 对「关闭后清空（`fresh`）」的即时补充，不用等关闭、也不改任何设置。与「删除面板」的分工要讲清：**重置保留面板、只清数据**；**删除连面板带数据一起清**。

- **先关窗口再清**：面板在运行表里就先 `close()` + `wait()`。顺序反了**不会报错**只会清不干净，而用户看到的是「重置成功」。
- 用 `clearUntilStable`；此刻窗口已销毁，重试预算的开销用户看不到。
- **不自动重开**：立刻重开会让新会话与清空动作抢同一个目录。
- **只动数据，不动配置**：不写配置、不删面板、不改快捷方式与窗口状态。清空失败**如实报错**，不假装成功。

## 桌面快捷方式（`shortcut_windows.go`）

手工 COM（IShellLinkW + IPersistFile，风格同 `panel_window_windows.go`）。桥接方法 `App.CreatePanelShortcut(panelID)`（返回 `{path, created, removed}`）与 `App.InspectPanelShortcut(panelID)`（只读预检）。

- 参数固定 `--open <面板ID>`，面板改名后仍有效；名称默认面板名；非法字符由 `sanitizeShortcutName` 清理。
- **一个分组桌面只留一份**：`createDesktopShortcut` 先找桌面上已有的那一份，找到就**覆盖它、并保留用户起的文件名**，没有才新建。`findPanelDesktopShortcut` 有两条来源，缺一不可：① 按「目标=本程序（文件名比对）+ 参数含 `--open <面板ID>`」扫桌面（用户改过名也认得出）；② 配置里记录的那份在桌面上且存在时也算 —— 这条兜底覆盖「程序改过名之后旧 .lnk 的目标名匹配不上」的情形。
- **历史遗留的重复项会被收敛**（`collapsePanelDesktopShortcuts`）：只删「本程序为该分组创建的」那些，别的分组、别的程序的 .lnk 一个不动；`keep` 为空时**一个都不删**（拿不准就别动手）。范围**只在桌面**，不碰任务栏固定目录。
- 前端「桌面快捷方式」按钮在有既存项时先 `window.confirm` 问「是否覆盖」，用户不点头就什么都不做。
- 读取既有 .lnk：`readShortcutTargetInCOM`（`IPersistFile.Load` + `IShellLinkW.GetPath/GetArguments`，`STGM_READ`），供删除面板的联动清理与固定状态判定使用。

### 坑

1. `SHGetFolderPathW` 与 IShellLink 都要求 COM 已初始化，`shortcutWithCOM` 包住全部步骤，不要先取桌面路径再进 COM（会 E_FAIL）。
2. 本机桌面重定向到 E 盘，`CSIDL_DESKTOPDIRECTORY`(0x0a) 返回 E_FAIL，须用 `CSIDL_DESKTOP`(0x00)（有 `%USERPROFILE%\Desktop` 兜底）。
3. `shortcutWithCOM` 必须 `runtime.LockOSThread`，且要识别 `S_FALSE`。

测试缝：`shortcutSelfExeBase`（本程序文件名）与 `shortcutDesktopDirectory`（桌面目录）都是包级变量，便于在临时目录里做完整扫描测试（`stubShortcutSeams` 是统一的注入助手）。

## 固定到任务栏

**结论先说：Windows 10/11 不允许程序自己把图标固定到任务栏，本功能只能做「准备好 + 引导」。任何「一键固定」的实现都是假的，别去试。**

微软从 Windows 10 起明确关闭了程序化固定（官方答复：固定到任务栏属于用户偏好，程序不应代为决定）。Windows 11 又封掉仅剩的两条旁路：往 `%APPDATA%\Microsoft\Internet Explorer\Quick Launch\User Pinned\TaskBar` 拷 .lnk 不再生效；`shell:::{4234d49b-0245-4df3-b780-3893943456e1}` 命名空间的 pin 动词被隐藏。

- **`taskbarpin` 动词必须整条删掉，不能「试一下不行再回退」**：shell 认不出这个动词时**静默退化成 `open`**（返回成功、`err=nil`，且目标程序真的被启动）。留着它，用户点「固定任务栏」的副作用就是白开一个面板窗口，而固定照样不会发生。
- 仍能无人值守写入任务栏的只剩 LayoutModification.xml + 杀掉重启 `explorer.exe`。代价是任务栏整条消失再重建、所有托盘图标重载，写错还会覆盖用户已排好的布局 —— 为一个图标不值得。
- 因此实现只有两步（`app.go` 的 `App.PinPanelToTaskbar` + `taskbar_windows.go`）：① 复用该面板已有的 .lnk，没有才新建一个（路径记进配置）；② `isPanelPinnedToTaskbar(panelID)` 回读任务栏固定目录判断是否已固定 → `AlreadyPinned`。
- **选中快捷方式不许自动发生**：`revealShortcutInExplorer` 只能由用户点引导对话框里的「选中快捷方式」（`App.RevealPanelShortcut`）触发。随弹框一起抢前台等于把说明文字盖掉。`PinTaskbarResult` 里没有「已经帮你选中了」这种状态。
- **固定状态的判定只能靠回读目录**，Windows 没有「查询固定状态」的 API。判定逻辑与桌面扫描同源：指向本程序（文件名比对）+ 参数为 `--open <面板ID>`。**Windows 自己生成的固定项能被 `IShellLinkW` 正常读出、参数被完整保留**（连 `-taskbar-tab <uuid>` 这类都在），所以按参数匹配可靠；`File Explorer.lnk` 这类无目标的特殊项读不出目标，会被自然跳过。

**固定项按钮的图标是可以变成站点图标的**：固定之后任务栏按钮走那份固定项 .lnk 的图标，那是个**静态引用** —— 用「刷新图标」把站点图标缓存成 .ico 并改写它的 `SetIconLocation` 之后，固定项也能显示站点图标。

## 刷新图标（分组卡片上的按钮）

把站点图标缓存成本地 .ico，让**桌面快捷方式与任务栏固定项**都用上它。实现全在 `iconcache_windows.go`。

- **为什么非要落盘一个文件**：标题栏与任务栏按钮的图标是我们自己 `WM_SETICON` 上去的，进程活着就在；但 .lnk 与任务栏固定项的图标是 **shell 保管的一份静态引用**（`IShellLinkW::SetIconLocation` 指向某个 .ico 或 exe），它不认内存里的 HICON、更不认网页。
- **缓存位置**：`config.json` 旁边的 `icons\<面板ID>.ico`（`panelIconCacheDirFor` 由 `configStore.path` 推出，不重新推导便携/`%APPDATA%`，否则配置文件被指定到别处时图标会写到另一个地方）。用面板 ID 命名，与改名无关。
- **交互刻意分两步**，因为改的是**用户自己创建的东西**，必须先摆出「会改成什么」让人点头：
  - `App.InspectPanelIcon(id)` → `PanelIconPreview`：能不能刷、图标预览（data URL）、会动到哪几个 .lnk、写到哪个路径、**桌面那份是要覆盖还是要新建**、有哪些重复项会被清掉。**不写任何文件**。
  - `App.ApplyPanelIcon(id)` → `PanelIconApplyResult`：写 .ico → 桌面那份创建或覆盖 → 逐个改写任务栏固定项 → 收敛桌面上多余的 → 通知 shell。`Updated` / `Failed` **分开报**：完全可能「桌面那个改了、固定项没改成」，一句「成功」会让人以为全都好了。
  - **桌面没有快捷方式时必须顺手创建一个**（否则刷新图标只剩「下载保存」，对用户没意义）。所以前端主按钮在那时叫「创建快捷方式并应用」，有既存项时叫「应用」。这条同时要求 `ApplyPanelIcon` 把路径 `setShortcut` 记进配置 —— 不记的话删面板时这份 .lnk 会漏在桌面上没人管。
- **「有没有图标可用」只能看面板窗口留下的样本**（`panelIconRegistry`）。三种情况提示完全不同：① 面板没打开过（或页面还在加载）→ 「还没打开过，请先打开它」；② 开过但站点没给图标（样本在、候选为空）→ 「当前地址没有图标可用」；③ 有候选 → 可刷。**窗口销毁时必须注销样本**（`panelWindow.dispose` 里调），否则窗口都关了按钮还说「有图标可用」。样本里只记**站点真实给出**的图标，不含自绘的首字母色块。
- **多标签一律取第一个标签的图标**（`cfg.Tabs[0]`）。缓存是分组级的，若跟着「当前显示的标签」走，同一个快捷方式两次刷新会给出不同图标，没法解释。
- **.ico 里要有多档尺寸**：16、32、48、64/128、256。只塞一帧的后果是**某些尺寸下糊**（shell 会把 16 拉大成 256）。≤48 用 **DIB 帧**（兼容性最好），>48 用 **PNG 帧**（体积小一个数量级：256 的 DIB 光像素就 256KB）。
- **源图不够大的档位直接跳过，不做放大**：站点只给 32 时硬拉出 256 只会得到一张模糊的图，还不如让 shell 缩放 32 那一帧。例外是 16 —— 列表视图的最低要求，实在没有就用最大的源缩下去。

### 坑

- **DIB 帧的 `biHeight` 必须写两倍高度**（ICO 把 XOR 位图与 AND 掩码描述成一张上下拼接的位图），写一倍高度会让后半截被当成像素；像素是**自底向上**存的（第一行是图像最后一行）。
- **帧里的 alpha 是直通（straight），不是预乘** —— 而 `panelResample` 产出的是预乘值，所以出帧前必须过 `panelToNRGBA` 反预乘（`png.Encode` 也要给 NRGBA，它对 RGBA 会再乘一遍 alpha）。搞反的症状是半透明边缘发亮，不放大看不出来。
- **改写 .lnk 必须「读回来再整份重写」**（`setShortcutIcon`）：`IShellLinkW` 只有整份 `Save`，新建出来的对象是空的 —— 不先把 `GetPath`/`GetArguments`/`GetDescription` 读回来就 `SetPath`，会得到一个**双击没反应的空壳，而返回值全是 `S_OK`**。传空 `iconPath` 表示回退到目标程序的内嵌图标。
- **扫描范围**：`listPanelShortcutsForIcon` 扫**桌面 + 任务栏固定目录**（固定时 Windows 把 .lnk **复制**过去，是两份独立文件，只改桌面那份任务栏不会有任何变化）。**不要**把固定目录并进 `listPanelShortcuts` —— 那个函数是**删面板**时的清理范围，扩大会让删面板顺手删掉用户亲手固定的项目。
- **删面板时的收尾顺序不能反**（`dropPanelIconCache`）：先把还指着这份缓存的 .lnk 的图标改回 exe 自带图标，**再**删 .ico。反过来的话那些 .lnk 指向不存在的图标，桌面上变成空白方块。另外只动**图标确实等于我们那份缓存**的 .lnk（`readShortcutIconPath` 比对）—— 用户自己换过的图标不碰。
- **改完必须通知 shell**（`SHChangeNotify`，`SHCNE_UPDATEITEM` + `SHCNF_FLUSH`；改过固定项目录里的东西再补一次 `SHCNE_UPDATEDIR`）。不通知的症状是「改了但看不见」：.lnk 里已是新值，桌面上还是旧图标。
- 新建/覆盖快捷方式会自动用上缓存（`CreatePanelShortcut` 与 `ApplyPanelIcon` 都传 `cachedPanelIconPath`），不必再点一次「刷新图标」。

## 托盘图标

- 图标由**常驻 IPC 窗口**承载（`tray_windows.go`，固定 uid=1），因此**与打开几个面板无关** —— 一个面板都没开托盘里也有入口（这也是「零面板时关闭管理窗口不退出」的前提）。
- **保留图标的三条理由**（`App.trayNeeded`，任一成立即保留，即使 `showTrayIcon=false`）：全局开关开启 / 管理窗口正藏在托盘（`App.resident`）/ 某个面板窗口正藏在托盘（`panelWindow.hiddenInTray`）。窗口从托盘恢复时清掉对应标记；`App.syncTray()` 幂等地增删图标。**例外**：`App.quitting` 时无条件不维护。
  第 2、3 条优先于「用户取消了托盘勾选」—— 藏起来的窗口必须有图标才能找回。
- **托盘图标与「最小化到托盘」的绑定（不变量）**：面板窗口的关闭行为是 `tray` 时，`showTrayIcon` **自动且必须为 true**。理由：选了这个行为的窗口一关闭就藏进托盘，图标是把它找回来的唯一入口。四个落点缺一不可：`load()` 纠正矛盾配置并写回、`settingsLocked()` 读取时归一化（`trayNeeded` / `quitWhenNoWindows` 都读它，绝不能看到矛盾组合）、`configStore.setCloseAction` 选中 `tray` 时连带打开、`setShowTrayIcon(false)` 直接拒绝（`ErrTrayIconRequired`）。前端据下拉值显示 `#st-tray-forced` 提示。
- **没有面板级 `minimizeToTray`**：面板的最小化按钮就是普通最小化 —— 把窗口送进托盘只发生在「关闭」时。`panelWindow.hiddenInTray` 是记录「窗口正藏在托盘里」的唯一字段。
- **Explorer 重启要重注册图标**：注册 `TaskbarCreated` 消息，收到后调 `trayReAdd()` —— 否则任务栏重启后托盘图标会消失。
- **托盘图标兜底**：`hideToTray()` 与最小化到托盘前会 `setHiddenInTray(true)`，确保窗口隐藏后必然有图标可找回；`showFromTray()` / `activate()` 恢复时清掉该标记。
- 托盘菜单：显示/隐藏面板、窗口置顶、关闭面板（以上三项对「活动面板」生效，无面板打开时置灰）、打开管理面板、退出 PanelDock。「活动面板」= `App.activePanelID`，已关闭时退化为配置顺序里第一个仍打开的面板。轻量模式下靠「打开管理面板」呼出被隐藏的主窗口。
- Windows 11 默认把新图标折叠进任务栏的「隐藏的图标」浮出层（点 `^`），首次可能看不到，需手动拖出来 —— 这不是程序问题。
- 托盘悬浮提示固定为 `PanelDock · Web管理面板启动器`（应用级，不带面板名）；活动面板名显示在菜单项文字里（超过 `trayMenuNameLimit` 字符截断）。

## 管理界面 UI 约定

- **面板卡片头部固定两行**：第一行 `.panel-title-row` —— 「面板名 + 启用/停用徽章」同行；第二行 `.panel-card-actions`（勾选框 + 操作按钮，左对齐、按需折行）。名称 `overflow-wrap: anywhere` + `min-width: 0` 允许收缩、徽章 `flex: none`，超长名称/URL 不会撑破卡片。**操作按钮不要塞回名称那一行** —— 名称一长就把按钮挤到下一行，同一种卡片在不同面板上高度与列位就不一致了。
- **界面不展示面板运行时状态**：卡片上只留「启用/停用」徽章。卡片主按钮固定写「打开」（`title` 里说明「已经打开时会切到前台」），不再按运行状态在「打开/切换」之间变字 —— 那需要主界面持续向后端要状态，而前端只有事件驱动、**没有轮询**。**不要**为了让按钮文字更聪明把运行时状态查询加回来。
- **确认对话框一律用应用内 `<dialog>`（`showModal`），不是 `window.confirm`**：浏览器确认框放不下复选框，而「清理快捷方式」这类恰恰需要复选框。破坏性操作的对话框默认聚焦「取消」—— 删除不可撤销，回车不该直接执行。

## 测试与验证陷阱

测试环境的注入缝、两条铁律、以及每条 E2E 用例独有的变量。常规构建命令见 AGENTS.md。

### E2E 用例清单

只写「守什么」和「写这条时容易踩的变量」；用例本体在 `e2e_windows_test.go`。

| 用例 | 守什么 / 坑 |
|---|---|
| `TestE2ELightweightSingleInstanceAndQuit` | 轻量启动 + 转发 + 干净退出。「面板可见」必须 `waitForCondition` —— 窗口对象在 `ShowWindow` 之前就已存在 |
| `TestE2EClosePromptPanelOnlyAffectsItself` | **「关闭只影响本面板」核心回归**：两面板，先选托盘再选直接关闭，断言只销毁被关的那个 |
| `TestE2ECloseRememberIsPersisted` | 「记住我的选择」落盘。跨进程改控件用 `BM_SETCHECK` 而非 `BM_CLICK` |
| `TestE2EManagerCloseOnlyClosesItself` | 管理窗口点 X 不弹框、面板与托盘不动。**收尾必须 `Kill` + `waitForNoInstance`**（结束时进程还活着，会占着单实例锁） |
| `TestE2ETrayIconRegistered` / `…Disabled` | 用 `Shell_NotifyIconW(NIM_MODIFY)` 探测是否真注册（内含 uid=99 反向自检）。都**不带 `--open`**，验证零面板时图标照样在 |
| `TestE2EManagerCloseKeepsTrayWhenNoPanels` / `…QuitsWhenNoTrayAndNoPanels` | 管理窗口固定关闭动作的两半 |
| `TestE2ETrayIconForcedByMinimizeToTray` | 故意写成矛盾配置（`showTrayIcon=false` + `panelCloseAction=tray`），断言被纠正并写回。反过来，`…Disabled` 的前置必须把关闭行为摆成非 tray |
| `TestE2EFreshSessionClearedOnClose` | 「关闭后清空」。自己造 fresh 面板 + 本地 HTTP 服务。**不要**断言「目录不存在」；必须先断言会话数据真的落盘（否则清空断言形同虚设） |
| `TestE2EDisabledPanelAsksToEnable` / `TestE2EForwardedOpenOnDisabledPanel` | 停用询问的两条路径。前者两轮验证，**轮次之间必须 `Process.Kill()` + `waitForNoInstance`**：第一轮只留在托盘，它持有的单实例锁会把第二轮转发过去 |
| `TestE2EDirtyWindowStateFallsBackToDefault` | 窗口状态哨兵值 `(-32000, 160x28)` 的兜底（可反向验证） |
| `TestE2EMinimizedPanelKeepsLastWindowState` | **目前无法反向验证**（`dispose` 时窗口已销毁、`GetWindowRect` 失败、`lastRect` 保持构造值）。留下它是钉住这条真实路径的现状 |
| `TestE2EFramelessWindowHasCustomTitleBar` | 只查骨架，不比对像素。**用例自带窗口尺寸**：地址栏宽度是「窗口宽减去两侧按钮」，拿用户配置里的面板做样本会把它那条窗口状态变成用例输入 |
| `TestE2ETitlebarFollowsRealNavigation` | 查**事件与图标真的活过来了**（骨架用例对 vtable 错位完全无感）。三条断言都是「只有我方代码真跑起来才成立」的观测量：① 配置 URL 与最终 URL **必须不同**（`currentURL` 创建时就被配置 URL 种了值）；② `/icon.png` 只认 `PanelDock/` UA 前缀（判别器在 `panelFetchBytes`，改那请连带改用例）；③ `WM_GETICON(ICON_BIG)` 非零（它只返回**显式设置过**的图标，不会假阳性） |
| `TestE2ETabBarThemeButtonSwitchesPanelChrome` | 断言配置真的变、**且实际像素跟着换**。坐标必须是客户区坐标；前置把 `theme` 钉成显式 dark；期望色号从配色表算；只能读自绘 GDI 内容 |

### E2E 编写的通用约定

- **窗口定位**：按**标题**找（`waitForWindowByTitle(t, panelWindowTitle(name))`）—— 多个面板同时打开时不能用「按类名找第一个」。询问框靠标题栏文案定位；「有没有询问框」用 `findWindowByClass(promptWindowClassName)`。
- **点按钮**统一走 `clickPromptButton(t, hwnd, buttonID)`（跨进程 `WM_COMMAND` + `SendMessageW` 同步等待）。
- 用例通过 `e2ePatchSettings` / `e2eReadConfig` 读写便携配置并在结束时按原字节还原；检测到已有真实实例在运行时自动跳过（**串跑时会因此跳过若干用例，需要单独重跑**）。
- `e2eSkipUnlessReady` 在「配置里没有启用面板」时**不再静默跳过**：它自己临时启用第一个面板。跳过 ≠ 通过。
- **⚠️ 改配置的三个坑**：
  1. 还原必须**无条件**：即使补丁没有任何实际改动也要写回原始字节。被测程序自己会写配置（把关闭行为设成 tray 会连带打开托盘图标、勾「记住我的选择」会写入关闭行为），跳过还原就会把便携包改坏、让后续用例静默跳过。
  2. 补丁要**清掉已废弃的字段**（旧 `closeAction` / `managerCloseAction`）：加载时的迁移/清理会重写它们，可能覆盖刚写入的值。
  3. 「关闭最后一个面板 → 进程退出」取决于 `lightweightQuitOnLastPanel`：需要这一前提的用例必须自己打开它（`e2eForceLightweightQuit`；顺带要「每次询问」的用 `e2eForceCloseActionAsk`）。
- `e2ePatchSettings` 是把整个 settings 节点换掉，不是在原始 JSON 上做小改动 —— 需要「插入一个键」以外的效果时注意这个边界。

### 测试地图

| 文件 | 覆盖什么 |
|---|---|
| `config_test.go` | 配置加载/原子写/损坏回退/旧字段迁移（含 `shortcuts` 数组收敛） |
| `session_test.go` / `session_windows_test.go` | 会话模式归一化、两种清理策略、`preparePanelProfile` |
| `shortcut_test.go` | 桌面快捷方式创建/覆盖/收敛/删除联动 |
| `taskbar_windows_test.go` | 固定状态回读、不抢前台、别的项不算数 |
| `iconcache_windows_test.go` / `icons_windows_test.go` | 多帧 ICO 往返、DIB 方向与通道序、按边长择优（够大的里挑最小的）、`LoadImageW` 让 Windows 自己认这份 .ico |
| `theme_test.go` / `theme_panel_windows_test.go` | 配色 token 解析、`panelColorRef` 换位、标签上限不压按钮、命中与矩形同源 |
| `theme_panel_windows_test.go` | 见上；注意 `app.panels` 是 `map[string]*panelWindow` |
| `tab_order_test.go` / `tab_delete_test.go` | 重排保 id、`DefaultTabIndex` 重映射、删标签清数据 |
| `reset_panel_test.go` / `delete_panel_windows_test.go` | 重置不改配置、先关后清顺序、清数据失败即中止 |
| `tray_test.go` / `window_state_test.go` / `titlebar_test.go` | 托盘不变量、窗口状态哨兵值、标题栏骨架 |
| `native_text_windows_test.go` | 词典完整性（map 字面量编译器帮不上忙，少填一个键运行时才以「菜单少一项」暴露）、`normalizeLanguage`、`SetLanguage` 往返 |
| `enable_prompt_windows_test.go` | 停用询问框的 spec 与按钮 ID 唯一性 |
| `orphan_profile_test.go` / `aumid_windows_test.go` | 孤儿 profile 目录、AUMID 真的设上了（防 `shell32` 导出名写错导致静默 no-op） |

### 两条测试的铁律

1. **新写的用例必须做反向验证**：把修复撤掉重建、确认用例变红。当初第一版 E2E 有两条断言写成恒真的空断言（配置 URL 与最终 URL 相同、只断言「有人来取过图标」），全靠反向验证才发现。
2. **任何「会写用户目录」的路径都必须走注入缝**（`shortcutDesktopDirectory` / `resolvePortableRoot` 这类包级变量），不要直接调真实函数 —— 否则测试会往**用户真实桌面**写 .lnk、往真实 profile 目录写会话数据。

### 没有自动化框架时的真机验证法

Python + ctypes 从窗口 DC `BitBlt` 一小条出来存 PNG，配合 `SendMessageW(hwnd, WM_LBUTTONDOWN, ..., (y<<16)|x)` 模拟点击。**坐标必须是客户区坐标** —— 误传屏幕坐标时点击静默失效，`SendMessage` 照样返回 1，看着像成功。
