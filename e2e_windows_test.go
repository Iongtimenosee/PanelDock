//go:build windows

package main

// 轻量模式 / 单实例 / 转发 的端到端验证（默认跳过，需显式开启）：
//
//	PANELDOCK_E2E=1 go test -run TestE2E -v .
//
// 需要已构建的 build\bin\PanelDock.exe 与 build\bin\data\config.json。
// 另有真实实例在运行时自动跳过（否则会干扰那个实例）。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// win32WMSendCommand 与生产代码的 win32WMCommand 同值，测试里显式命名以突出用途。
	win32WMSendCommand = 0x0111

	// win32WMGetText 用于跨进程读取控件文本（见 e2eWindowText）。
	win32WMGetText = 0x000D

	// 任务栏 / Alt+Tab 图标。WM_GETICON 不读系统兜底，只读显式设置过的。
	win32WMGetIcon = 0x007F
	win32IconBig   = 1
)

const (
	// win32NIMModify 用于探测托盘图标是否存在：图标不存在时 Shell_NotifyIconW 返回 FALSE。
	win32NIMModify = 1
)

const (
	// win32SWMinimizeForE2E 与生产代码的 win32SWMinimize 同值（SW_MINIMIZE），
	// 用于把面板窗口真的最小化（标题栏最小化按钮走的就是它）。
	win32SWMinimizeForE2E = 6
)

const (
	// 复选框状态操作：BM_SETCHECK 把「记住我的选择」置为指定状态，
	// 比 BM_CLICK 更确定（不看当前状态）。BST_CHECKED 与生产代码同源。
	win32BMSetCheck   = 0x00F1
	win32BSTUnchecked = 0
)

// e2eStaleInstanceWait 是「判定没有别的实例在跑」的最长等待时间。
// 用途见 e2eSkipUnlessReady：等上一个用例刚 Kill 掉的进程把 IPC 窗口拆完。
const e2eStaleInstanceWait = 8 * time.Second

var (
	e2eUser32                   = windows.NewLazySystemDLL("user32.dll")
	e2eShell32                  = windows.NewLazySystemDLL("shell32.dll")
	e2eFindWindowW              = e2eUser32.NewProc("FindWindowW")
	e2eFindWindowExW            = e2eUser32.NewProc("FindWindowExW")
	e2eGetWindowRect            = e2eUser32.NewProc("GetWindowRect")
	e2eGetClientRect            = e2eUser32.NewProc("GetClientRect")
	e2eGetDlgItem               = e2eUser32.NewProc("GetDlgItem")
	e2eGetClassNameW            = e2eUser32.NewProc("GetClassNameW")
	e2eGetWindowThreadProcessId = e2eUser32.NewProc("GetWindowThreadProcessId")
	e2eIsWindowVisible          = e2eUser32.NewProc("IsWindowVisible")
	e2eShowWindowW              = e2eUser32.NewProc("ShowWindow")
	e2eIsIconic                 = e2eUser32.NewProc("IsIconic")
	e2ePostMessageW             = e2eUser32.NewProc("PostMessageW")
	e2eSendMessageW             = e2eUser32.NewProc("SendMessageW")
	e2eGetWindowLongPtrW        = e2eUser32.NewProc("GetWindowLongPtrW")
	e2eGetDC                    = e2eUser32.NewProc("GetDC")
	e2eReleaseDC                = e2eUser32.NewProc("ReleaseDC")
	e2eShellNotifyIcon          = e2eShell32.NewProc("Shell_NotifyIconW")
	e2eGdi32                    = windows.NewLazySystemDLL("gdi32.dll")
	e2eGetPixel                 = e2eGdi32.NewProc("GetPixel")
)

// e2eWindowPixel 取窗口客户区某点的颜色（0x00BBGGRR 的 COLORREF，与 GDI 同布局）。
//
// 只能用来读**自绘的 GDI 内容**（标题栏 / 标签栏 / 边框那一圈）；WebView2 的内容区
// 是另一个进程合成的，这里读不到，别拿它去断言页面。
func e2eWindowPixel(t *testing.T, hwnd uintptr, x, y int) (uint32, bool) {
	t.Helper()

	hdc, _, _ := e2eGetDC.Call(hwnd)
	if hdc == 0 {
		return 0, false
	}
	defer e2eReleaseDC.Call(hwnd, hdc)

	value, _, _ := e2eGetPixel.Call(hdc, uintptr(int32(x)), uintptr(int32(y)))
	if uint32(value) == 0xFFFFFFFF { // CLR_INVALID
		return 0, false
	}
	return uint32(value), true
}

// findWindowByClass 按类名查找顶层窗口；找不到返回 0。
func findWindowByClass(class string) uintptr {
	ptr, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	hwnd, _, _ := e2eFindWindowW.Call(uintptr(unsafe.Pointer(ptr)), 0)
	return hwnd
}

// findWindowByTitle 按标题精确查找顶层窗口；找不到返回 0。
func findWindowByTitle(title string) uintptr {
	ptr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return 0
	}
	hwnd, _, _ := e2eFindWindowW.Call(0, uintptr(unsafe.Pointer(ptr)))
	return hwnd
}

func windowVisible(hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	r, _, _ := e2eIsWindowVisible.Call(hwnd)
	return r != 0
}

// e2eSkipUnlessReady 做齐前置检查：开关、exe、配置、以及「没有别的实例在跑」。
//
// 「没有别的实例」这一条必须等一下再判定：上一个用例结束时 `Process.Kill()` 的进程
// 还在拆窗口，常驻 IPC 窗口会多存活几百毫秒。若立刻判定就会把紧随其后的用例
// 全部标成 skip —— 而**跳过 ≠ 通过**，那些回归等于没跑（改动前的实测：一次运行里
// 有 4 个用例因此静默跳过）。这里给一个有限的等待窗口，真的有人开着 PanelDock
// 时仍然跳过（不能去干扰用户正在用的那个实例）。
func e2eSkipUnlessReady(t *testing.T) (exePath string, panel PanelConfig) {
	t.Helper()
	if os.Getenv("PANELDOCK_E2E") == "" {
		t.Skip("设置 PANELDOCK_E2E=1 启用端到端验证")
	}
	exePath = filepath.Join("build", "bin", "PanelDock.exe")
	if _, err := os.Stat(exePath); err != nil {
		t.Skipf("未找到构建产物 %s: %v", exePath, err)
	}
	// 已有真实实例在运行时会抢占单实例锁并把请求转发过去，测试无法自证，直接跳过。
	if !waitForCondition(e2eStaleInstanceWait, func() bool {
		return findWindowByClass(ipcWindowClass) == 0
	}) {
		t.Skip("已有 PanelDock 实例在运行，跳过端到端验证")
	}

	cfg := e2eReadConfig(t)
	panels := e2eEnabledPanels(cfg)
	if len(panels) == 0 {
		// 面板全被停用时**不能静默跳过** —— 跳过 ≠ 通过，整包回归会变成假的绿
		// （本机实测就踩到过：配置里唯一的面板是停用的，所有用例一声不响地全体跳过）。
		// 用例自己临时启用第一个面板，结束按原字节还原。
		if len(cfg.Panels) == 0 {
			t.Skip("便携配置里一个面板都没有，无法做端到端验证")
		}
		e2eEnableFirstPanel(t)
		panels = []PanelConfig{e2eReadConfig(t).Panels[0]}
	}
	return exePath, panels[0]
}

// e2eEnableFirstPanel 临时启用配置里的第一个面板，用例结束按原字节还原。
//
// 面板 ID、标签、名称都不动，用例照旧按面板名找窗口。放在这里是为了让
// 「用户把面板全停用了」这种正常情况不再把整套回归变成静默跳过。
func e2eEnableFirstPanel(t *testing.T) {
	t.Helper()

	restore := e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels[0].Enabled = true
	})
	t.Cleanup(restore)
}

func startE2EInstance(t *testing.T, exePath string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(exePath, args...)
	if err := cmd.Start(); err != nil {
		t.Fatalf("启动实例失败: %v", err)
	}
	return cmd
}

func TestE2ELightweightSingleInstanceAndQuit(t *testing.T) {
	exePath, panel := e2eSkipUnlessReady(t)

	// 阶段 A 断言「关闭最后一个面板 → 进程退出」，那是可配置的行为，用例自己打开它。
	restore := e2eForceLightweightQuit(t)
	defer restore()

	// ── 阶段 A：轻量启动 → 单实例转发 → 关闭最后一个面板后退出 ──────────────────
	t.Run("轻量启动与随面板退出", func(t *testing.T) {
		first := startE2EInstance(t, exePath, "--open", panel.ID)
		defer func() { _ = first.Process.Kill() }()

		// 等面板窗口出现（含 500ms 延迟开面板 + WebView2 初始化）。
		var panelHwnd uintptr
		deadline := time.Now().Add(25 * time.Second)
		for time.Now().Before(deadline) {
			if hwnd := findWindowByClass(panelWindowClassNameString()); hwnd != 0 {
				panelHwnd = hwnd
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if panelHwnd == 0 {
			t.Fatal("超时：面板窗口未出现")
		}
		// 窗口对象在 ShowWindow 之前就已存在（窗口类先注册、窗口后显示），所以按类名找到它
		// 不等于它已经可见 —— 直接断言会偶发失败（实测踩到）。等一会儿再判。
		if !waitForCondition(8*time.Second, func() bool { return windowVisible(panelHwnd) }) {
			t.Error("面板窗口应可见")
		}
		if titleHwnd := findWindowByTitle("PanelDock"); windowVisible(titleHwnd) {
			t.Error("轻量模式下 Wails 主窗口不应可见")
		}

		// 第二次启动：应转发后退出，不叠出第二个进程。
		second := startE2EInstance(t, exePath, "--open", panel.ID)
		if err := second.Wait(); err != nil {
			t.Errorf("第二个实例应在转发后正常退出，实际: %v", err)
		}
		if findWindowByClass(ipcWindowClass) == 0 {
			t.Error("转发后首个实例应仍在运行")
		}

		// 关闭最后一个面板：进程应干净退出（此阶段管理窗口始终未显示）。
		// 这里用「直接关闭」私有消息，它等价于程序主动关闭（删除面板 / 退出），
		// 不触发关闭询问框 —— 询问框走的是用户点 X 的 WM_CLOSE（见 TestE2EClosePrompt）。
		if r, _, _ := e2ePostMessageW.Call(panelHwnd, win32WMDirectClose, 0, 0); r == 0 {
			t.Fatal("发送直接关闭消息到面板窗口失败")
		}
		exited := make(chan error, 1)
		go func() { exited <- first.Wait() }()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			t.Fatal("关闭最后一个面板后进程未退出")
		}
		waitForNoInstance(t)
	})

	// ── 阶段 B：无参数二次启动转发 show，把被隐藏的管理窗口呼出来 ─────────────────
	t.Run("转发 show 呼出管理窗口", func(t *testing.T) {
		first := startE2EInstance(t, exePath, "--open", panel.ID)
		defer func() { _ = first.Process.Kill() }()
		waitForPanelWindow(t)

		third := startE2EInstance(t, exePath) // 无参数：应转发 show
		if err := third.Wait(); err != nil {
			t.Errorf("无参数实例应在转发后正常退出，实际: %v", err)
		}
		var managerVisible bool
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if windowVisible(findWindowByTitle("PanelDock")) {
				managerVisible = true
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !managerVisible {
			t.Error("转发 show 后管理窗口应变为可见")
		}
	})
}

// TestE2EClosePromptPanelOnlyAffectsItself 验证本轮修正的核心：
// 面板窗口的「直接关闭」只关掉该面板，不再连带关闭其他面板与管理面板。
//
// 用两个面板窗口（同一个实例被 `--open` 打开两次）验证：关掉第一个后，第二个窗口
// 与进程都必须还在；关掉最后一个时才收工（轻量模式「用完即走」，见 AGENTS.md）。
func TestE2EClosePromptPanelOnlyAffectsItself(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	// 前置（两个**可打开**的面板）由用例自己建立，绝不因为「只有一个启用面板」而 t.Skip：
	// 「关闭只影响本面板」是本项目最核心的不变量，一旦静默跳过就等于没人守它。
	// 便携配置里随时可能只剩一个启用面板（用户停用另一个，2026-09-30 实测就是这样）。
	cfg := e2eReadConfig(t)
	if len(cfg.Panels) < 2 {
		t.Skip("便携配置只有一个面板，无法验证「关闭只影响本面板」")
	}
	firstPanel, secondPanel := cfg.Panels[0], cfg.Panels[1]

	// 先把前两个面板临时置为启用，再把关闭行为改成「每次询问」。
	//
	// 两个补丁都改同一个文件、各自按原字节备份并 defer 还原，**顺序有讲究**：
	// defer 是 LIFO，先注册 restorePanels 后注册 restoreActions，于是收尾时
	// restoreActions 先写回「面板已启用」的版本，restorePanels 最后写回**原始字节**。
	// 反过来注册就会把临时启用的面板永久留在用户配置里。
	restorePanels := e2ePatchConfig(t, func(c *AppConfig) {
		for i := range c.Panels {
			if c.Panels[i].ID == firstPanel.ID || c.Panels[i].ID == secondPanel.ID {
				c.Panels[i].Enabled = true
			}
		}
	})
	defer restorePanels()

	restore := e2eForceCloseActionAsk(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", firstPanel.ID)
	defer func() { _ = instance.Process.Kill() }()
	firstHwnd := waitForWindowByTitle(t, panelWindowTitle(firstPanel.Name))

	// 第二个面板用第二个实例 `--open` 转发到同一个进程（单实例）。
	opener := startE2EInstance(t, exePath, "--open", secondPanel.ID)
	if err := opener.Wait(); err != nil {
		t.Errorf("第二个实例应在转发后正常退出，实际: %v", err)
	}
	secondHwnd := waitForWindowByTitle(t, panelWindowTitle(secondPanel.Name))

	// ── 1. 第一个面板点 X：应弹出「面板窗口」角色的询问框 ─────────────────────────
	e2ePostMessageW.Call(firstHwnd, win32WMCLOSE, 0, 0)
	prompt := waitForClosePrompt(t)
	if !windowVisible(prompt) {
		t.Error("询问框应可见")
	}
	if !windowVisible(firstHwnd) {
		t.Error("询问框弹出期间，被关闭的窗口应仍然存在")
	}

	// ── 2. 选「最小化到托盘」：窗口隐藏、进程存活（托盘图标可找回） ────────────────
	clickPromptButton(t, prompt, closePromptBtnTray)
	if !waitForCondition(8*time.Second, func() bool { return !windowVisible(firstHwnd) }) {
		t.Error("选择「最小化到托盘」后面板窗口应隐藏")
	}
	if !waitForCondition(5*time.Second, func() bool {
		return findWindowByClass(promptWindowClassName) == 0
	}) {
		t.Error("做出选择后询问框应关闭")
	}
	if findWindowByClass(ipcWindowClass) == 0 {
		t.Error("选择「最小化到托盘」后进程应继续运行")
	}
	if !windowVisible(findWindowByTitle(panelWindowTitle(secondPanel.Name))) {
		t.Error("一个面板藏进托盘不应影响其他面板窗口")
	}

	// ── 3. 再次点 X 并选「直接关闭」：只关本面板，另一个面板与进程都必须还在 ───────
	e2ePostMessageW.Call(firstHwnd, win32WMCLOSE, 0, 0)
	prompt = waitForClosePrompt(t)
	clickPromptButton(t, prompt, closePromptBtnClose)
	if !waitForCondition(10*time.Second, func() bool {
		return findWindowByTitle(panelWindowTitle(firstPanel.Name)) == 0
	}) {
		t.Error("选择「直接关闭」后该面板窗口应被销毁")
	}
	if !windowVisible(findWindowByTitle(panelWindowTitle(secondPanel.Name))) {
		t.Error("关闭一个面板不应连带关闭其他面板窗口")
	}
	if findWindowByClass(ipcWindowClass) == 0 {
		t.Error("还有其他面板打开时进程不应退出")
	}

	// ── 4. 关掉最后一个面板：轻量模式下进程收工 ─────────────────────────────────
	e2ePostMessageW.Call(secondHwnd, win32WMCLOSE, 0, 0)
	prompt = waitForClosePrompt(t)
	clickPromptButton(t, prompt, closePromptBtnClose)

	exited := make(chan error, 1)
	go func() { exited <- instance.Wait() }()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("关闭最后一个面板后进程未退出")
	}
	waitForNoInstance(t)
}

// TestE2ECloseRememberIsPersisted 验证「记住我的选择」落盘：在面板窗口的询问框里勾选
// 「记住我的选择」并选「直接关闭」后，配置里 panelCloseAction 变成 close，下次不再询问。
func TestE2ECloseRememberIsPersisted(t *testing.T) {
	exePath, panel := e2eSkipUnlessReady(t)

	restore := e2eForceCloseActionAsk(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", panel.ID)
	defer func() { _ = instance.Process.Kill() }()
	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(panel.Name))

	// 面板窗口点 X：勾选「记住我的选择」后选「直接关闭」。
	e2ePostMessageW.Call(panelHwnd, win32WMCLOSE, 0, 0)
	prompt := waitForClosePrompt(t)
	setClosePromptRemember(t, prompt, true)
	clickPromptButton(t, prompt, closePromptBtnClose)

	if !waitForCondition(10*time.Second, func() bool {
		return findWindowByTitle(panelWindowTitle(panel.Name)) == 0
	}) {
		t.Error("选择「直接关闭」后该面板窗口应被销毁")
	}

	// 轻量模式下这是唯一的面板，关闭后进程收工；等它退出再读配置，断言写入的内容。
	exited := make(chan error, 1)
	go func() { exited <- instance.Wait() }()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("关闭最后一个面板后进程未退出")
	}
	waitForNoInstance(t)

	if settings := e2eReadSettings(t); settings.PanelCloseAction != CloseActionClose {
		t.Errorf("勾选「记住」后 panelCloseAction 应为 %q，实际 %q",
			CloseActionClose, settings.PanelCloseAction)
	}
}

// TestE2EManagerCloseOnlyClosesItself 验证管理窗口的关闭动作（固定、不询问）：
// 点 X 后管理窗口消失，**分组标签窗口照旧运行**，托盘图标与设置一个都不动 —— 进程继续运行。
func TestE2EManagerCloseOnlyClosesItself(t *testing.T) {
	exePath, panel := e2eSkipUnlessReady(t)

	// 前置条件「托盘图标开着」由用例自己临时建立（结束按原字节还原），不依赖用户当前配置：
	// 依赖配置会让整个用例静默 skip，而**跳过 ≠ 通过**（托盘被关掉时这条回归就白跑了）。
	restore := e2ePatchSettings(t, func(s *AppSettings) {
		s.PanelCloseAction = CloseActionAsk
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
		s.ShowTrayIcon = true
	})
	defer restore()

	// 普通启动（管理窗口可见），再用第二个实例转发打开一个分组标签窗口。
	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()
	manager := waitForManagerWindow(t)

	opener := startE2EInstance(t, exePath, "--open", panel.ID)
	if err := opener.Wait(); err != nil {
		t.Errorf("第二个实例应在转发后正常退出，实际: %v", err)
	}
	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(panel.Name))

	// ── 管理窗口点 X：没有询问框，直接执行固定动作 ─────────────────────────────
	e2ePostMessageW.Call(manager, win32WMCLOSE, 0, 0)
	if !waitForCondition(15*time.Second, func() bool { return !windowVisible(manager) }) {
		t.Error("关闭管理窗口后它应消失（还有面板，所以是隐藏而非销毁）")
	}
	// 固定动作不询问：不能冒出任何关闭询问框。
	if hwnd := findWindowByTitle(nativeUITexts[LanguageZhCN].ClosePrompt.Caption); hwnd != 0 {
		t.Error("关闭管理窗口不应弹询问框（它的动作是固定的）")
	}
	if !windowVisible(panelHwnd) {
		t.Error("分组标签窗口不应受管理窗口关闭的影响")
	}
	ipcHwnd := findWindowByClass(ipcWindowClass)
	if ipcHwnd == 0 {
		t.Fatal("还有分组标签窗口时进程必须继续运行（分组标签不能被连带带走）")
	}

	// 托盘图标必须照旧保留 —— 关闭管理窗口不碰托盘设置。探针自检：未知 uid 必须报告
	// 「未注册」，否则下面的断言毫无意义。
	if trayIconRegistered(ipcHwnd, 99, nativeUITexts[LanguageZhCN].Tray.Tip) {
		t.Fatal("探针自检失败：未知 uid 也被判定为已注册")
	}
	if !trayIconRegistered(ipcHwnd, trayIconUID, nativeUITexts[LanguageZhCN].Tray.Tip) {
		t.Error("关闭管理窗口不应撤掉托盘图标")
	}
	if settings := e2eReadSettings(t); !settings.ShowTrayIcon {
		t.Error("关闭管理窗口不应改动「在系统托盘显示图标」")
	}

	// ── 关掉最后一个分组标签：托盘图标还开着，程序留在托盘里而不是退出 ────────
	e2ePostMessageW.Call(panelHwnd, win32WMCLOSE, 0, 0)
	prompt := waitForClosePrompt(t)
	clickPromptButton(t, prompt, closePromptBtnClose)

	if !waitForCondition(6*time.Second, func() bool { return !windowVisible(panelHwnd) }) {
		t.Error("选择「直接关闭」后该面板窗口应被销毁")
	}
	// 退出判定在最后一个面板关闭后先等 200ms 再动手，所以这里要多等一会儿再确认，
	// 不能只测「此刻还在」。托盘里有图标，程序就该留下 —— 那是找回界面的入口。
	time.Sleep(2 * time.Second)
	if findWindowByClass(ipcWindowClass) == 0 {
		t.Error("托盘图标还开着时，关掉最后一个面板不应退出进程")
	}

	// 收尾：本用例结束时进程仍活在托盘里，必须显式结束并等它真的消失 ——
	// 它持有的单实例锁会把下一个用例的启动请求转发走。
	_ = instance.Process.Kill()
	waitForNoInstance(t)
}

// TestE2ETrayIconRegistered 验证默认设置（showTrayIcon=true）下托盘图标确实注册了，
// 而且**一个面板都没开时照样存在**——这正是「无面板时最小化到托盘不再退化为退出」的前提。
// 托盘图标由常驻 IPC 窗口承载，因此探测目标是 IPC 窗口而非面板窗口。
func TestE2ETrayIconRegistered(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	// 前置条件由用例自己建立（结束还原）：用户配置里托盘图标可能是关的，
	// 靠 t.Skip 跳过就只能看着「通过」的假象 —— 这条回归必须真的跑。
	restore := e2ePatchSettings(t, func(s *AppSettings) { s.ShowTrayIcon = true })
	defer restore()

	// 故意不带 --open：整个进程不会出现任何面板窗口。
	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()

	ipcHwnd := waitForIpcWindow(t)
	if !waitForCondition(6*time.Second, func() bool {
		return trayIconRegistered(ipcHwnd, trayIconUID, nativeUITexts[LanguageZhCN].Tray.Tip)
	}) {
		t.Fatal("零面板时未注册托盘图标（默认 showTrayIcon=true 时应注册）")
	}
	// 探针自检：不存在的 uid 必须返回「未注册」，否则上面的断言毫无意义。
	if trayIconRegistered(ipcHwnd, 99, nativeUITexts[LanguageZhCN].Tray.Tip) {
		t.Error("探针自检失败：未知 uid 也被判定为已注册")
	}
}

// TestE2ETrayIconDisabled 验证全局关闭托盘图标后不再注册（上面用例的镜像断言）。
// 不带 --open 启动时没有任何面板窗口，因此不存在「窗口藏在托盘里必须保留图标」的例外。
//
// 前置条件必须连关闭行为一起摆好：两处只要有一个是「最小化到托盘」，图标就会被强制
// 保留（见 TestE2ETrayIconForcedByMinimizeToTray）——那种状态下这条断言根本不成立。
// 用户机器上的真实配置恰好就是「两处都是 tray」，所以这一步不能省。
func TestE2ETrayIconDisabled(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restore := e2ePatchSettings(t, func(s *AppSettings) {
		s.ShowTrayIcon = false
		s.PanelCloseAction = CloseActionAsk
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
	defer restore()

	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()

	ipcHwnd := waitForIpcWindow(t)

	// 留出注册时机，再断言图标确实不存在。
	time.Sleep(1500 * time.Millisecond)
	if trayIconRegistered(ipcHwnd, trayIconUID, nativeUITexts[LanguageZhCN].Tray.Tip) {
		t.Error("关闭托盘图标后不应注册图标")
	}
}

// TestE2ETrayIconForcedByMinimizeToTray 验证「面板窗口会最小化到托盘 → 托盘图标必须开着」
// 这条不变量在真实进程里成立：把配置写成矛盾组合（showTrayIcon=false + 面板关闭行为 tray），
// 启动后图标照样注册，且矛盾被纠正落盘。
//
// 这不是纸面规则：用户机器上就出现过这个组合 —— 窗口关掉后藏进托盘，而托盘里没有图标。
func TestE2ETrayIconForcedByMinimizeToTray(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restore := e2ePatchSettings(t, func(s *AppSettings) {
		s.ShowTrayIcon = false
		s.PanelCloseAction = CloseActionTray
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
	defer restore()

	// 不带 --open：整场不出现任何面板窗口，排除「窗口藏在托盘里才保留图标」的干扰。
	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()

	ipcHwnd := waitForIpcWindow(t)
	if !waitForCondition(6*time.Second, func() bool {
		return trayIconRegistered(ipcHwnd, trayIconUID, nativeUITexts[LanguageZhCN].Tray.Tip)
	}) {
		t.Fatal("有窗口会最小化到托盘时，即使配置写着 showTrayIcon=false 也必须注册图标")
	}

	// 矛盾组合应被纠正并写回文件，否则下次启动还得再纠正一遍。
	if settings := e2eReadSettings(t); !settings.ShowTrayIcon {
		t.Error("加载时纠正出的 showTrayIcon=true 应写回配置")
	}
}

// TestE2EManagerCloseKeepsTrayWhenNoPanels 覆盖固定关闭动作的一半：
// 一个面板都没有、但托盘图标开着时，关管理窗口是「藏进托盘」—— 进程存活、图标仍在，
// 用户随时能从托盘把管理面板找回来。
func TestE2EManagerCloseKeepsTrayWhenNoPanels(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restore := e2ePatchSettings(t, func(s *AppSettings) {
		s.ShowTrayIcon = true
		s.PanelCloseAction = CloseActionAsk
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
	defer restore()

	// 不带 --open：一个面板窗口都不会出现。
	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()

	manager := waitForManagerWindow(t)
	ipcHwnd := waitForIpcWindow(t)

	// 点 X：没有询问框，窗口直接藏进托盘。
	e2ePostMessageW.Call(manager, win32WMCLOSE, 0, 0)
	if !waitForCondition(8*time.Second, func() bool { return !windowVisible(manager) }) {
		t.Error("托盘图标还开着时，关管理窗口应把它藏起来")
	}
	if hwnd := findWindowByTitle(nativeUITexts[LanguageZhCN].ClosePrompt.Caption); hwnd != 0 {
		t.Error("关闭管理窗口不应弹询问框（它的动作是固定的）")
	}
	if findWindowByClass(ipcWindowClass) == 0 {
		t.Fatal("一个面板都没有、但托盘图标开着时，不应退出进程")
	}
	if !waitForCondition(5*time.Second, func() bool {
		return trayIconRegistered(ipcHwnd, trayIconUID, nativeUITexts[LanguageZhCN].Tray.Tip)
	}) {
		t.Error("窗口藏进托盘后必须保留托盘图标，否则窗口再也找不回来")
	}

	// 收尾：进程还活在托盘里，必须显式结束并等它消失（否则它拿着单实例锁影响下一个用例）。
	_ = instance.Process.Kill()
	waitForNoInstance(t)
}

// TestE2EManagerCloseQuitsWhenNoTrayAndNoPanels 覆盖固定关闭动作的出口：
// 既没有面板窗口、也没有托盘图标时，关管理窗口就是关掉最后一个窗口 —— 进程随之结束。
func TestE2EManagerCloseQuitsWhenNoTrayAndNoPanels(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restore := e2ePatchSettings(t, func(s *AppSettings) {
		s.ShowTrayIcon = false
		s.PanelCloseAction = CloseActionAsk
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
	defer restore()

	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()

	manager := waitForManagerWindow(t)

	e2ePostMessageW.Call(manager, win32WMCLOSE, 0, 0)

	exited := make(chan error, 1)
	go func() { exited <- instance.Wait() }()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("没有面板也没有托盘图标时，关掉管理窗口后进程应退出")
	}
	waitForNoInstance(t)
}

// ─── 「关闭后清空浏览器状态」的端到端验证 ─────────────────────────────────────

// 「关闭后清空」用例专用的临时面板 / 标签 ID。用固定值是为了让测试能提前算出
// profile 目录路径，从而在启动前埋「上次没清干净」的残留。
const (
	e2eFreshPanelID   = "e2e-fresh-session-panel"
	e2eFreshPanelName = "E2E 临时面板"
	e2eFreshTabID     = "e2e-fresh-session-tab"
)

// e2eUsePortableProfiles 把会话目录解析到便携包（build/bin/data），返回还原函数。
//
// 必须注入：defaultPortableRoot 是按「**本进程自己的** exe 同目录有没有 data」判定的，
// 而测试二进制的 exe 在临时目录里 —— 不注入就会解析到 %LOCALAPPDATA%\PanelDock\WebViewProfiles，
// 与真实运行的程序用的根本不是同一个目录，断言会全部落空（还顺手往用户目录里写东西）。
func e2eUsePortableProfiles(t *testing.T) func() {
	t.Helper()

	orig := resolvePortableRoot
	resolvePortableRoot = func() string {
		abs, err := filepath.Abs(filepath.Join("build", "bin", "data"))
		if err != nil {
			return filepath.Join("build", "bin", "data")
		}
		return abs
	}
	return func() { resolvePortableRoot = orig }
}

// e2eAddFreshSessionPanel 在便携配置里临时加一个「关闭后清空」的测试面板，返回还原函数。
//
// 为什么不用便携包里已有的面板：本用例会把 profile 目录**整个删掉**，而真实面板里存的是
// 用户自己的登录会话 —— 配置能按原字节还原，会话目录还原不了。用例自己造面板、
// 自己负责清理，才对得起「跑完零污染」这条要求。
//
// rawURL 指向用例自己起的本地 HTTP 服务（浏览器要真的把会话数据写到磁盘上，
// 才谈得上验证「关掉之后还在不在」）。
func e2eAddFreshSessionPanel(t *testing.T, rawURL string) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:          e2eFreshPanelID,
			Name:        e2eFreshPanelName,
			Tabs:        []PanelTab{{ID: e2eFreshTabID, Name: "E2E 标签", URL: rawURL}},
			Enabled:     true,
			SessionMode: SessionModeFresh,
			Window:      PanelWindowState{Width: 720, Height: 560},
		})
	})
}

// e2eProfileContains 在 profile 目录里递归搜一个字符串（按原始字节匹配），返回是否命中。
//
// 直接读文件而不解析 SQLite / leveldb：我们只关心这段数据**在不在磁盘上**。
// 能搜到就说明会话数据还在，搜不到就说明清干净了 —— 这正是本功能要断言的东西，
// 比「目录在不在」精确得多（浏览器在退出时会重建目录，见 TestE2EFreshSessionClearedOnClose）。
func e2eProfileContains(root, token string) bool {
	needle := []byte(token)
	found := false

	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		if bytes.Contains(data, needle) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	return found
}

// TestE2EFreshSessionClearedOnClose 验证「关闭后清空浏览器状态」这条链路真的落在磁盘上：
//  1. **打开前兜底**：磁盘上留着的旧会话数据（模拟上次崩溃 / 被强杀没清干净）在打开前被抹掉；
//  2. **关闭后清空**：本次运行期间真实写入的会话数据（页面设的 Cookie / localStorage），
//     在关闭面板之后**再也搜不到**。
//
// 断言的是真实磁盘数据而不是配置字段：这个功能的全部价值就在「磁盘上没有残留」，
// 只断言配置写对了等于什么都没验证。
//
// 这里刻意**不**断言「profile 目录不存在」：实测 WebView2 浏览器进程在退出时会把刚被删掉的
// 目录又建回来（带一整棵空的 EBWebView 树），那是它自己的初始化行为，不是会话残留；
// 目录在不在是时序问题，「会话数据在不在」才是承诺本身。
func TestE2EFreshSessionClearedOnClose(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	// 用例末尾要断言「关闭唯一的面板后进程退出」，那是可配置的行为，自己打开它。
	restore := e2eForceLightweightQuit(t)
	defer restore()

	// 用例自己起一个本地页面：页面写 localStorage，响应头设一个名字唯一的 Cookie。
	// token 带随机后缀，保证搜到的一定是本次会话写下的数据。
	nonce := time.Now().UnixNano()
	localStorageToken := fmt.Sprintf("paneldock-e2e-ls-%d", nonce)
	cookieName := fmt.Sprintf("paneldock_e2e_%d", nonce)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "1", Path: "/", MaxAge: 3600})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, fmt.Sprintf(
			`<html><body><script>localStorage.setItem('paneldock_e2e','%s');</script>ok</body></html>`,
			localStorageToken,
		))
	}))
	defer srv.Close()

	defer e2eUsePortableProfiles(t)()
	defer e2eAddFreshSessionPanel(t, srv.URL)()

	tabDir, err := tabProfileDir(e2eFreshTabID)
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	// 这个目录是本用例自己造的：用完整棵删掉，不给便携包留下测试垃圾。
	// 走产品自己的「删到稳定为止」：用例结束时 WebView2 子进程可能还没退完，
	// 单次 RemoveAll 会因文件被占用而只删掉一部分，留下残留（实测踩到）。
	t.Cleanup(func() { _ = removeProfileDir(tabDir, clearUntilStable) })

	// ── 1. 打开前兜底：先埋一份「上次没清干净」的残留 ─────────────────────────
	leftover := filepath.Join(tabDir, "e2e-leftover.txt")
	if err := os.MkdirAll(tabDir, 0o700); err != nil {
		t.Fatalf("准备残留目录失败: %v", err)
	}
	if err := os.WriteFile(leftover, []byte("上次没清干净"), 0o600); err != nil {
		t.Fatalf("写入残留标记失败: %v", err)
	}

	instance := startE2EInstance(t, exePath, "--open", e2eFreshPanelID)
	defer func() { _ = instance.Process.Kill() }()
	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(e2eFreshPanelName))

	if !waitForCondition(8*time.Second, func() bool {
		_, statErr := os.Stat(leftover)
		return os.IsNotExist(statErr)
	}) {
		t.Error("「关闭后清空」的面板应在打开前抹掉上次残留的会话数据")
	}

	// ── 2. 确认这次会话**真的**在磁盘上留下了数据（否则后面的清空断言没有意义）────
	if !waitForCondition(25*time.Second, func() bool {
		return e2eProfileContains(tabDir, localStorageToken) || e2eProfileContains(tabDir, cookieName)
	}) {
		t.Fatal("未能确认本次会话已把数据写入磁盘（页面没加载成功？），清空断言将形同虚设")
	}

	// ── 3. 关闭面板：本次写入的会话数据必须再也搜不到 ─────────────────────────
	// 走主动关闭路径（等价于退出程序 / 删除面板时的关闭），不弹询问框。
	if r, _, _ := e2ePostMessageW.Call(panelHwnd, win32WMDirectClose, 0, 0); r == 0 {
		t.Fatal("发送直接关闭消息到面板窗口失败")
	}

	exited := make(chan error, 1)
	go func() { exited <- instance.Wait() }()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("关闭唯一的面板后进程未退出")
	}
	waitForNoInstance(t)

	if waitForCondition(10*time.Second, func() bool {
		return e2eProfileContains(tabDir, localStorageToken) || e2eProfileContains(tabDir, cookieName)
	}) {
		t.Error("关闭后仍能在 profile 目录里搜到本次会话的数据（localStorage / Cookie 残留）")
	}
	if _, statErr := os.Stat(leftover); !os.IsNotExist(statErr) {
		t.Error("关闭后残留标记文件应已不存在")
	}
	// 实测加固：关掉之后隔一会儿再查一遍。浏览器进程是在陆续退出的，
	// 一次「没搜到」可能只是它还没来得及把内存里的数据写回磁盘。
	time.Sleep(2 * time.Second)
	if e2eProfileContains(tabDir, localStorageToken) || e2eProfileContains(tabDir, cookieName) {
		t.Error("关闭 2 秒后 profile 目录里又出现了本次会话的数据（浏览器在退出时写了回来）")
	}
}

// ─── 「面板已停用」询问框的端到端验证 ───────────────────────────────────────

// 「面板已停用」用例专用的临时面板 / 标签 ID。
const (
	e2eDisabledPanelID   = "e2e-disabled-panel"
	e2eDisabledPanelName = "E2E 停用面板"
	e2eDisabledTabID     = "e2e-disabled-tab"
)

// e2eAddDisabledPanel 在便携配置里临时加一个**停用**的测试面板，返回还原函数。
//
// 与「关闭后清空」的用例同理：不用便携包里已有的面板。本用例会把它启用（写进配置）
// 并真的打开它（写 profile 目录）——配置能按原字节还原，自己造的面板才谈得上零污染。
// 地址指向一个必然连不上的本地端口：只用例关心「开没开」，不该去访问用户的局域网设备。
func e2eAddDisabledPanel(t *testing.T) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:      e2eDisabledPanelID,
			Name:    e2eDisabledPanelName,
			Tabs:    []PanelTab{{ID: e2eDisabledTabID, Name: "E2E 标签", URL: "http://127.0.0.1:1/"}},
			Enabled: false,
			Window:  PanelWindowState{Width: 720, Height: 560},
		})
	})
}

// TestE2EDisabledPanelAsksToEnable 验证「用快捷方式打开一个已停用的面板」不再毫无反应：
//
//	场景一（保持停用）：弹出「面板已停用」询问框；选「保持停用」后**什么都不发生** ——
//	  面板窗口不出现、管理窗口也不会被弹出来，配置仍是停用（即旧行为的静默状态）；
//	场景二（启用并打开）：同样的询问框，选「启用并打开」后面板真的打开，且启用状态**落盘**。
//
// 这两条都必须跑在真实进程上：询问框是原生模态窗口，配置写入发生在另一个进程里，
// 单测只能覆盖「同意 / 拒绝」这段逻辑（见 ensurePanelEnabledForExternalOpen 的单测）。
func TestE2EDisabledPanelAsksToEnable(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	// 「关闭唯一的面板后进程退出」是可配置的行为，用例自己打开它。
	restore := e2eForceLightweightQuit(t)
	defer restore()

	defer e2eUsePortableProfiles(t)()
	defer e2eAddDisabledPanel(t)()

	// 面板真打开时 WebView2 会写 profile 目录；这个目录是本用例自己造的，整棵清掉。
	tabDir, err := tabProfileDir(e2eDisabledTabID)
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	// 走产品自己的「删到稳定为止」：用例结束时 WebView2 子进程可能还没退完，
	// 单次 RemoveAll 会因文件被占用而只删掉一部分，留下残留（实测踩到）。
	t.Cleanup(func() { _ = removeProfileDir(tabDir, clearUntilStable) })

	// ── 场景一：保持停用 ────────────────────────────────────────────────────
	declined := startE2EInstance(t, exePath, "--open", e2eDisabledPanelID)
	defer func() { _ = declined.Process.Kill() }()

	prompt := waitForVisiblePrompt(t, nativeUITexts[LanguageZhCN].EnablePrompt.Caption)
	if !windowVisible(prompt) {
		t.Error("「面板已停用」询问框应可见")
	}
	clickPromptButton(t, prompt, enablePromptBtnKeep)

	if !waitForCondition(5*time.Second, func() bool {
		return findWindowByTitle(nativeUITexts[LanguageZhCN].EnablePrompt.Caption) == 0
	}) {
		t.Error("做出选择后询问框应关闭")
	}
	// 静默收场：面板没打开，管理窗口也没被弹出来（用户刚回答过，不该再拿窗口盖上去）。
	time.Sleep(2 * time.Second)
	if findWindowByTitle(panelWindowTitle(e2eDisabledPanelName)) != 0 {
		t.Error("选择「保持停用」后面板窗口不应出现")
	}
	if windowVisible(findWindowByTitle(appMainWindowTitle)) {
		t.Error("选择「保持停用」后不应弹出管理窗口")
	}
	if panel := e2eFindPanel(t, e2eDisabledPanelID); panel.Enabled {
		t.Error("选择「保持停用」不应改动配置里的启用状态")
	}

	// 这一轮没有面板窗口，轻量模式下进程只会留在托盘里，直接结束它再做下一轮
	// （否则它能一直持有单实例锁，第二个实例会被转发到它那儿去）。
	_ = declined.Process.Kill()
	waitForNoInstance(t)

	// ── 场景二：启用并打开 ──────────────────────────────────────────────────
	accepted := startE2EInstance(t, exePath, "--open", e2eDisabledPanelID)
	defer func() { _ = accepted.Process.Kill() }()

	prompt = waitForVisiblePrompt(t, nativeUITexts[LanguageZhCN].EnablePrompt.Caption)
	clickPromptButton(t, prompt, enablePromptBtnOpen)

	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(e2eDisabledPanelName))
	if !windowVisible(panelHwnd) {
		t.Error("选择「启用并打开」后面板窗口应可见")
	}
	if panel := e2eFindPanel(t, e2eDisabledPanelID); !panel.Enabled {
		t.Error("选择「启用并打开」应把启用状态落盘（否则下次打开还得再问一遍）")
	}

	// 收尾：走主动关闭路径关掉这个面板（不弹询问框），轻量模式下进程随之收工。
	if r, _, _ := e2ePostMessageW.Call(panelHwnd, win32WMDirectClose, 0, 0); r == 0 {
		t.Fatal("发送直接关闭消息到面板窗口失败")
	}
	exited := make(chan error, 1)
	go func() { exited <- accepted.Wait() }()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		t.Fatal("关闭唯一的面板后进程未退出")
	}
	waitForNoInstance(t)
}

// TestE2EForwardedOpenOnDisabledPanel 验证**真实使用场景**：程序已经在运行（管理窗口开着），
// 双击一个属于停用面板的快捷方式 —— 命令被转发到已在运行的实例，在那里弹询问框。
//
// 与 TestE2EDisabledPanelAsksToEnable 的区别正是这一段：那条用例走的是「没有实例在跑、
// 自己轻量启动」的路径（询问框 owner 为空、居中到屏幕），这里走的是转发路径
// （`handleIPCCommand` → 询问框 owner = 可见的管理窗口），两条路径的实现不同，都得有回归。
func TestE2EForwardedOpenOnDisabledPanel(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	defer e2eUsePortableProfiles(t)()
	defer e2eAddDisabledPanel(t)()

	tabDir, err := tabProfileDir(e2eDisabledTabID)
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	// 走产品自己的「删到稳定为止」：用例结束时 WebView2 子进程可能还没退完，
	// 单次 RemoveAll 会因文件被占用而只删掉一部分，留下残留（实测踩到）。
	t.Cleanup(func() { _ = removeProfileDir(tabDir, clearUntilStable) })

	// 普通启动：管理窗口可见，但它不属于任何面板。
	instance := startE2EInstance(t, exePath)
	defer func() { _ = instance.Process.Kill() }()
	manager := waitForManagerWindow(t)

	// 快捷方式那条路：第二个实例带 --open 启动，转发给已运行的实例后自行退出。
	opener := startE2EInstance(t, exePath, "--open", e2eDisabledPanelID)
	if err := opener.Wait(); err != nil {
		t.Errorf("第二个实例应在转发后正常退出，实际: %v", err)
	}

	prompt := waitForVisiblePrompt(t, nativeUITexts[LanguageZhCN].EnablePrompt.Caption)
	if !windowVisible(prompt) {
		t.Error("转发过来的打开请求也应弹出「面板已停用」询问框")
	}
	clickPromptButton(t, prompt, enablePromptBtnOpen)

	// 面板真的打开，且管理窗口与进程都还在 —— 询问框挂的是管理窗口，别把它一起带走。
	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(e2eDisabledPanelName))
	if !windowVisible(panelHwnd) {
		t.Error("选择「启用并打开」后面板窗口应可见")
	}
	if !windowVisible(manager) {
		t.Error("打开面板不应影响管理窗口")
	}
	if findWindowByClass(ipcWindowClass) == 0 {
		t.Error("进程应继续运行")
	}
	if panel := e2eFindPanel(t, e2eDisabledPanelID); !panel.Enabled {
		t.Error("选择「启用并打开」应把启用状态落盘")
	}

	// 收尾：关掉面板（主动关闭路径）后进程仍在（管理窗口还开着），再整体结束它。
	if r, _, _ := e2ePostMessageW.Call(panelHwnd, win32WMDirectClose, 0, 0); r == 0 {
		t.Fatal("发送直接关闭消息到面板窗口失败")
	}
	if !waitForCondition(10*time.Second, func() bool {
		return findWindowByTitle(panelWindowTitle(e2eDisabledPanelName)) == 0
	}) {
		t.Error("面板窗口应被关闭")
	}
	_ = instance.Process.Kill()
	waitForNoInstance(t)
}

// e2eReadConfig 读取便携配置（与 e2eSkipUnlessReady 读的是同一份文件）。
// 多面板场景（如「关闭只影响本面板」）需要按索引取面板，故单独暴露。
func e2eReadConfig(t *testing.T) AppConfig {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("build", "bin", "data", "config.json"))
	if err != nil {
		t.Skipf("读取便携配置失败: %v", err)
	}
	var cfg AppConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Skipf("解析便携配置失败: %v", err)
	}
	return cfg
}

// e2eEnabledPanels 返回配置中已启用的面板（按原顺序），供端到端用例挑选可打开的面板。
func e2eEnabledPanels(cfg AppConfig) []PanelConfig {
	var out []PanelConfig
	for _, p := range cfg.Panels {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out
}

// e2eFindPanel 从便携配置里按 ID 取面板，找不到直接失败。
// 供用例断言「启用状态有没有被写进配置」——状态是后端自己改的，只能从文件里读。
func e2eFindPanel(t *testing.T, id string) PanelConfig {
	t.Helper()

	for _, panel := range e2eReadConfig(t).Panels {
		if panel.ID == id {
			return panel
		}
	}
	t.Fatalf("便携配置里没有面板 %s", id)
	return PanelConfig{}
}

// e2eReadSettings 读取便携配置里的应用级设置（缺失时套用默认值）。
func e2eReadSettings(t *testing.T) AppSettings {
	t.Helper()

	cfg := e2eReadConfig(t)
	if cfg.Settings == nil {
		return defaultAppSettings()
	}
	return *cfg.Settings
}

// waitForIpcWindow 等待常驻 IPC 窗口出现（它就是托盘图标的宿主），超时即失败。
func waitForIpcWindow(t *testing.T) uintptr {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowByClass(ipcWindowClass); hwnd != 0 {
			return hwnd
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("超时：IPC 窗口未出现")
	return 0
}

// waitForManagerWindow 等待 Wails 管理窗口可见，超时即失败。
func waitForManagerWindow(t *testing.T) uintptr {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowByTitle("PanelDock"); windowVisible(hwnd) {
			return hwnd
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("超时：管理窗口未出现")
	return 0
}

// trayIconRegistered 探测 (hwnd, uid) 对应的托盘图标是否已注册。
// 依据：NIM_MODIFY 在图标不存在时返回 FALSE。
func trayIconRegistered(hwnd uintptr, uid uint32, tip string) bool {
	utf16Tip, err := windows.UTF16FromString(tip)
	if err != nil {
		return false
	}

	var nid panelNOTIFYICONDATA
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = uid
	nid.UFlags = win32NIFTIP
	copy(nid.SzTip[:], utf16Tip)

	result, _, _ := e2eShellNotifyIcon.Call(win32NIMModify, uintptr(unsafe.Pointer(&nid)))
	return result != 0
}

// clickPromptButton 点击询问框上的按钮（跨进程 SendMessage，等待处理完成）。
// 所有询问框共用窗口类与窗口过程，因此关闭询问框与「面板已停用」询问框都用它。
func clickPromptButton(t *testing.T, prompt uintptr, buttonID uintptr) {
	t.Helper()
	if prompt == 0 {
		t.Fatal("询问框句柄为空")
	}
	// SendMessage 会同步等到目标处理完点击（对话框随即销毁），
	// WM_COMMAND 的返回值不作断言。
	e2eSendMessageW.Call(prompt, win32WMSendCommand, buttonID, 0)
}

// setClosePromptRemember 勾选/取消询问框上的「记住我的选择」复选框。
// 复选框是自建对话框里的原生控件，按控件 ID 取到子窗口后直接改状态，
// 比 BM_CLICK 更确定（不看当前状态）。生产代码用 BM_GETCHECK 读取，因此这里一经
// 设置就会真的被读走。
func setClosePromptRemember(t *testing.T, prompt uintptr, on bool) {
	t.Helper()
	if prompt == 0 {
		t.Fatal("询问框句柄为空")
	}
	check, _, _ := e2eGetDlgItem.Call(prompt, closePromptChkRemember)
	if check == 0 {
		t.Fatal("未找到「记住我的选择」复选框")
	}
	if on {
		e2eSendMessageW.Call(check, win32BMSetCheck, win32BSTChecked, 0)
		return
	}
	e2eSendMessageW.Call(check, win32BMSetCheck, win32BSTUnchecked, 0)
}

// waitForClosePrompt 等待关闭询问框出现，超时即失败。
// 只有面板窗口会问（管理窗口的关闭动作是固定的，不弹框），因此按唯一的标题文案定位。
func waitForClosePrompt(t *testing.T) uintptr {
	t.Helper()
	caption := nativeUITexts[LanguageZhCN].ClosePrompt.Caption
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowByTitle(caption); hwnd != 0 {
			return hwnd
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("超时：未出现标题为 %q 的关闭询问框", caption)
	return 0
}

// e2eWindowDebug 返回一个窗口的可读描述（类名 / 是否可见 / 所属进程），用于排查
// 「按标题找到了窗口、断言却不成立」这类现场 —— 单看句柄什么都看不出来。
func e2eWindowDebug(hwnd uintptr) string {
	if hwnd == 0 {
		return "<无窗口>"
	}
	var class [128]uint16
	length, _, _ := e2eGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	var pid uint32
	e2eGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	visible := 0
	if windowVisible(hwnd) {
		visible = 1
	}
	return fmt.Sprintf("hwnd=%#x class=%q visible=%d pid=%d",
		hwnd, windows.UTF16ToString(class[:length]), visible, pid)
}

// waitForVisiblePrompt 等待标题为 title 的**可见**询问框出现，超时即失败并打印现场。
//
// 不能只等「窗口存在」：询问框是「先 CreateWindowExW、再 ShowWindow」两步创建的，
// 中间还夹着创建子控件与跨线程 EnableWindow(owner)。只等存在就会撞上「已创建但尚未显示」
// 那一瞬，随后断言 `windowVisible` 直接失败 —— 实测：串跑时必现、单独跑时偶发
// （时序取决于上一个用例留下的机器负载），属于**用例自己的竞态**，不是产品问题。
func waitForVisiblePrompt(t *testing.T, title string) uintptr {
	t.Helper()

	deadline := time.Now().Add(20 * time.Second)
	var seen uintptr
	for time.Now().Before(deadline) {
		if hwnd := findWindowByTitle(title); hwnd != 0 {
			seen = hwnd
			if windowVisible(hwnd) {
				return hwnd
			}
		}
		time.Sleep(120 * time.Millisecond)
	}
	if seen != 0 {
		t.Fatalf("超时：标题为 %q 的窗口始终不可见（%s）", title, e2eWindowDebug(seen))
	}
	t.Fatalf("超时：未出现标题为 %q 的询问框", title)
	return 0
}

// waitForWindowByTitle 等待指定标题的顶层窗口出现（可以是隐藏窗口），超时即失败。
func waitForWindowByTitle(t *testing.T, title string) uintptr {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowByTitle(title); hwnd != 0 {
			return hwnd
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("超时：标题为 %q 的窗口未出现", title)
	return 0
}

// 「窗口骨架」用例专用的临时面板 / 标签 ID。
// 另开一套而不复用别的用例：每条用例各自 patch 配置、各自还原，免得看到对方留下的窗口状态。
const (
	e2eFramelessPanelID   = "e2e-frameless-panel"
	e2eFramelessPanelName = "E2E 窗口骨架面板"
	e2eFramelessTabID     = "e2e-frameless-tab"
)

// e2eAddFramelessPanel 临时加一个给「窗口骨架」用例用的面板，**并带上明确窗口尺寸**。
//
// 为什么非得自带尺寸：这条用例是拿窗口宽度去核对地址栏几何的（地址栏占标题栏中段，
// 窗口太窄时宽度合法地算成 0）。拿用户配置里的面板做样本，就等于把用户那一条窗口状态
// 变成用例的输入 —— 2026-09-30 实测踩到：那个面板的窗口状态被历史脏数据写成了
// x=-32000 / 160x28（最小化时存下的哨兵值），用例报「地址栏 EDIT 尺寸异常 0x26」，
// 看着像产品坏了，其实是被别人的数据坑了。
func e2eAddFramelessPanel(t *testing.T) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:      e2eFramelessPanelID,
			Name:    e2eFramelessPanelName,
			Tabs:    []PanelTab{{ID: e2eFramelessTabID, Name: "E2E 标签", URL: "http://example.lan"}},
			Enabled: true,
			Window:  PanelWindowState{X: 120, Y: 120, Width: 1120, Height: 760},
		})
	})
}

// TestE2EPanelWindowIsFramelessWithCustomTitleBar 守住自绘标题栏的两个结构约束：
//
//  1. **面板窗口没有系统非客户区** —— 客户区尺寸必须等于窗口尺寸。否则系统标题栏
//     会与自绘标题栏叠成两条（实测踩到过：客户区 1104x721 vs 窗口 1120x760）。
//  2. **顶部结构自上而下是 自绘标题栏 → 原生标签栏**，且标题栏里挂着地址栏 EDIT。
//
// 只查窗口骨架、不比对像素，所以不依赖页面能否加载、也不依赖截图。
//
// 第 1 条是回归重点：WM_NCCALCSIZE 必须同时处理 wParam=TRUE 与 wParam=FALSE，
// 创建期系统发的偏偏是后者（见 panelWindowProc 里的注释）。只判 TRUE 的写法会
// 让这条用例当场变红。
func TestE2EPanelWindowIsFramelessWithCustomTitleBar(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restorePanel := e2eAddFramelessPanel(t)
	defer restorePanel()

	restore := e2eForceLightweightQuit(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", e2eFramelessPanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForPanelWindow(t)
	// 等自绘标题栏子窗口建好（创建时序在面板窗口之后）。
	var titleHwnd uintptr
	if !waitForCondition(15*time.Second, func() bool {
		titleHwnd = findChildWindowByClass(panelHwnd, "PanelDock.PanelTitleBar")
		return titleHwnd != 0
	}) {
		t.Fatal("超时：未找到自绘标题栏子窗口")
	}

	// ① 无边框：客户区尺寸 == 窗口尺寸（有系统边框时客户区必然更小）。
	window := e2eWindowRect(t, panelHwnd)
	client := e2eClientRect(t, panelHwnd)
	if w, h := window.Width(), window.Height(); w != client.Width() || h != client.Height() {
		t.Errorf("面板窗口不应有系统非客户区：窗口 %dx%d，客户区 %dx%d",
			w, h, client.Width(), client.Height())
	}

	// ② 垂直结构：自绘标题栏贴顶（top=0、高 titleBarHeight），标签栏紧随其下。
	titleRect := e2eWindowRect(t, titleHwnd)
	if got := titleRect.Top - window.Top; got != 0 {
		t.Errorf("自绘标题栏应贴窗口顶部，实际偏移 %d", got)
	}
	if got := titleRect.Height(); got != titleBarHeight {
		t.Errorf("自绘标题栏高度应为 %d，实际 %d", titleBarHeight, got)
	}

	barHwnd := findChildWindowByClass(panelHwnd, "PanelDock.PanelTabBar")
	if barHwnd == 0 {
		t.Fatal("未找到原生标签栏子窗口")
	}
	barRect := e2eWindowRect(t, barHwnd)
	if got := barRect.Top - window.Top; got != titleBarHeight {
		t.Errorf("标签栏应紧贴自绘标题栏下方（top=%d），实际偏移 %d", titleBarHeight, got)
	}
	if got := barRect.Height(); got != tabBarHeight {
		t.Errorf("标签栏高度应为 %d，实际 %d", tabBarHeight, got)
	}

	// ③ 地址栏 EDIT 挂在标题栏里（父窗口是标题栏，不是面板窗口）。
	editHwnd := findChildWindowByClass(titleHwnd, "EDIT")
	if editHwnd == 0 {
		t.Fatal("未找到自绘标题栏里的地址栏 EDIT")
	}
	// 地址栏宽度是标题栏**按窗口宽度**算出来的（layoutAddressEdit），窗口还没成型时算出来就是 0。
	// 控件先于布局存在，所以「按类名找到它」不等于「它已经就位」—— 直接断言等于赌布局已跑完
	// （2026-09-30 实测：整机负载高时挂在这一条，报 0x38）。这里等它就位再断言。
	var editRect e2eRect
	if !waitForCondition(8*time.Second, func() bool {
		if ok, _, _ := e2eGetWindowRect.Call(editHwnd, uintptr(unsafe.Pointer(&editRect))); ok == 0 {
			return false
		}
		return editRect.Width() > 0 && editRect.Height() > 0
	}) {
		t.Errorf("地址栏 EDIT 尺寸异常：%dx%d（面板窗口 %dx%d，标题栏 %dx%d）",
			editRect.Width(), editRect.Height(),
			window.Width(), window.Height(), titleRect.Width(), titleRect.Height())
	}
}

// ─── 标题栏「置顶开关」：自绘按钮点了真的有用 ────────────────────────────────

// win32GWLEXStyle / win32WSExTopMost 是「窗口是不是置顶」的唯一硬判据。
// 不用「窗口的 Z 序在最前」去判断：那要靠枚举窗口，还要受前台窗口干扰。
const (
	win32GWLEXStyle  = ^uintptr(19) // GWL_EXSTYLE = -20
	win32WSExTopMost = 0x00000008
	win32MKLButton   = 0x0001
)

// panelTopMost 报告面板窗口当前是否带 WS_EX_TOPMOST（即真的置顶了）。
func panelTopMost(hwnd uintptr) bool {
	style, _, _ := e2eGetWindowLongPtrW.Call(hwnd, win32GWLEXStyle)
	return style&win32WSExTopMost != 0
}

// clickTitleBarPin 往自绘标题栏的置顶开关上发一次「按下 + 抬起」。
//
// 自绘按钮没有自动化接口，但它的命中几何是纯函数（panelTitleBarLayoutFor），
// 用例与产品共用同一份计算，所以「点哪儿」不会各写一份坐标。
// 发到标题栏子窗口、用客户区坐标 —— 与真实鼠标走的是同一条消息路径。
func clickTitleBarPin(t *testing.T, titleHwnd uintptr) {
	t.Helper()

	layout := panelTitleBarLayoutFor(e2eClientRect(t, titleHwnd).Width())
	x := (layout.pin.Left + layout.pin.Right) / 2
	y := (layout.pin.Top + layout.pin.Bottom) / 2
	lparam := uintptr(uint16(x)) | uintptr(uint16(y))<<16

	e2ePostMessageW.Call(titleHwnd, win32WMLButtonDown, win32MKLButton, lparam)
	e2ePostMessageW.Call(titleHwnd, win32WMLButtonUp, 0, lparam)
}

// TestE2ETitleBarPinTogglesAlwaysOnTop 守住标题栏上的「置顶开关」真的能点。
//
// 这条用例覆盖的是三段分开写的代码：命中几何（`panelTitleBarLayoutFor` / `buttonAt`）、
// 动作分发（`activateTitleBarButton`）、状态持久化（`setAlwaysOnTop` 写配置）。
// 任何一段对不上，表现都是「图标画出来了、点了没反应」，而编译、vet、单测全绿 ——
// 布局单测只能证明「按钮在那儿」，证明不了「点它有用」。
//
// 两个方向都要断言：变置顶、再点回来。只测单向的话，「来回切换」写成了「只会打开」
// 这种错照样过得去。
func TestE2ETitleBarPinTogglesAlwaysOnTop(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restorePanel := e2eAddFramelessPanel(t)
	defer restorePanel()

	restore := e2eForceLightweightQuit(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", e2eFramelessPanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForPanelWindow(t)
	// 标题栏子窗口在面板窗口之后创建，等它出现（与骨架用例同一套等待）。
	var titleHwnd uintptr
	if !waitForCondition(15*time.Second, func() bool {
		titleHwnd = findChildWindowByClass(panelHwnd, "PanelDock.PanelTitleBar")
		return titleHwnd != 0
	}) {
		t.Fatal("超时：未找到自绘标题栏子窗口")
	}
	// 置顶图标的位置是等布局算完才成立的（标题栏先建好、再按宽度摆），所以点之前先等地址栏就位，
	// 否则可能按「窗口宽度还是 0」算出来的坐标去点。
	editHwnd := findChildWindowByClass(titleHwnd, "EDIT")
	if editHwnd == 0 {
		t.Fatal("未找到自绘标题栏里的地址栏 EDIT")
	}
	var editRect e2eRect
	if !waitForCondition(8*time.Second, func() bool {
		if ok, _, _ := e2eGetWindowRect.Call(editHwnd, uintptr(unsafe.Pointer(&editRect))); ok == 0 {
			return false
		}
		return editRect.Width() > 0
	}) {
		t.Fatal("超时：标题栏布局未就位（地址栏宽度仍是 0）")
	}

	if panelTopMost(panelHwnd) {
		t.Fatal("前置不成立：这个面板一开始就应该是非置顶（否则下面的断言分不清是它本来就置顶还是被点出来的）")
	}

	// ① 点一下 → 窗口真的带上 WS_EX_TOPMOST，并且写进配置（否则重启就丢）。
	clickTitleBarPin(t, titleHwnd)
	if !waitForCondition(5*time.Second, func() bool { return panelTopMost(panelHwnd) }) {
		t.Fatal("点了置顶开关，窗口没有变成置顶（WS_EX_TOPMOST 未置位）")
	}
	if !waitForCondition(3*time.Second, func() bool {
		return e2eFindPanel(t, e2eFramelessPanelID).AlwaysOnTop
	}) {
		t.Error("窗口已置顶，但配置里没写下来：重启后这个状态会丢")
	}

	// ② 再点一下 → 退回去（只测单向会漏掉「来回切换」没写对的情况）。
	clickTitleBarPin(t, titleHwnd)
	if !waitForCondition(5*time.Second, func() bool { return !panelTopMost(panelHwnd) }) {
		t.Fatal("再点一次没有取消置顶")
	}
	if !waitForCondition(3*time.Second, func() bool {
		return !e2eFindPanel(t, e2eFramelessPanelID).AlwaysOnTop
	}) {
		t.Error("已取消置顶，但配置里还是「置顶」")
	}
}

// ─── 标签栏「配色开关」：点一下真的换配色 ────────────────────────────────────

// e2eTabBarBg 读标签栏某点的颜色（COLORREF）。读不到时回一个哨兵值，
// 让调用方的 waitForCondition 继续重试，而不是当场失败。
func e2eTabBarBg(t *testing.T, barHwnd uintptr, x, y int) uint32 {
	t.Helper()

	value, ok := e2eWindowPixel(t, barHwnd, x, y)
	if !ok {
		return 0xFFFFFFFF
	}
	return value
}

// clickTabBarTheme 往标签栏右端的配色按钮发一次「按下 + 抬起」。
//
// 命中几何与产品共用同一份纯函数（panelTabBarThemeRect），所以「点哪儿」不会各写一份坐标。
// 发到标签栏子窗口、用**客户区坐标** —— 与真实鼠标走的是同一条消息路径。
// （别改成屏幕坐标：那次实测点击静默失效，PostMessage 照样返回 1，看着像成功。）
func clickTabBarTheme(t *testing.T, barHwnd uintptr) {
	t.Helper()

	rect := panelTabBarThemeRect(e2eClientRect(t, barHwnd).Width())
	x := (rect.Left + rect.Right) / 2
	y := (rect.Top + rect.Bottom) / 2
	lparam := uintptr(uint16(x)) | uintptr(uint16(y))<<16

	e2ePostMessageW.Call(barHwnd, win32WMLButtonDown, win32MKLButton, lparam)
	e2ePostMessageW.Call(barHwnd, win32WMLButtonUp, 0, lparam)
}

// TestE2ETabBarThemeButtonSwitchesPanelChrome 守住标签栏右端那个配色开关真的能用。
//
// 覆盖三段分开写的代码：命中几何（panelTabBarThemeRect / themeButtonHit）、
// 动作分发（pressTabBarButton / releaseTabBarButton → App.togglePanelTheme）、
// 以及「改完之后外壳真的换了颜色」。
//
// **只断言配置里 theme 变了是不够的**：配色是窗口自己按 p.theme 查表画的，
// 按钮能改设置而窗口不重绘（或重绘时查的还是老表）照样会把设置写对、界面不动。
// 所以这里读的是标签栏的**实际像素** —— 直接拿 GetPixel 从窗口 DC 上取。
//
// 两个方向都断言：变浅色、再点回深色。单向只测得出「切得过去」，测不出「切得回来」。
func TestE2ETabBarThemeButtonSwitchesPanelChrome(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restorePanel := e2eAddFramelessPanel(t)
	defer restorePanel()

	restore := e2eForceLightweightQuit(t)
	defer restore()

	// 前置：把设置钉成**显式** dark，而不是 auto。auto 会跟随本机 Windows 的深浅色偏好，
	// 断言就变成「看这台机器当时是什么色」—— 用户在系统里换一次主题，用例就假红。
	restoreTheme := e2ePatchSettings(t, func(s *AppSettings) { s.Theme = ThemeDark })
	defer restoreTheme()

	instance := startE2EInstance(t, exePath, "--open", e2eFramelessPanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForPanelWindow(t)

	// 标签栏子窗口在面板窗口之后创建。
	var barHwnd uintptr
	if !waitForCondition(15*time.Second, func() bool {
		barHwnd = findChildWindowByClass(panelHwnd, "PanelDock.PanelTabBar")
		return barHwnd != 0
	}) {
		t.Fatal("超时：未找到原生标签栏子窗口")
	}
	// 等标签栏按窗口宽度摆好：宽度还是 0 时算出来的按钮坐标是负的，会点到空处。
	var barRect e2eRect
	if !waitForCondition(8*time.Second, func() bool {
		barRect = e2eClientRect(t, barHwnd)
		return barRect.Width() > 2*tabBarPadding+tabBarThemeBtnWidth
	}) {
		t.Fatalf("超时：标签栏宽度异常（%d）", barRect.Width())
	}

	// 采样点：第一个标签右侧、配色按钮左侧的那片**纯底色**。
	// 第一个标签只占 [tabBarPadding, tabBarPadding+tabBarTabWidth]，配色按钮贴右缘，
	// 中间这段既不画标签也不画按钮。
	sampleX := int(tabBarPadding + tabBarTabWidth + tabBarTabGap*4)
	sampleY := int(tabBarHeight / 2)

	// 期望值从配色表算出来，不另抄一份色号 —— 本用例要守的是「点一下真的换了颜色」，
	// 不是「底色必须是这个色号」（那是观感，调整色号不该让用例变红）。
	bgDark := panelColorRef(panelChromeFor(ThemeDark).bg)
	bgLight := panelColorRef(panelChromeFor(ThemeLight).bg)

	// ① 初始为深色：底色像素就是深色表里的 bg。
	if !waitForCondition(5*time.Second, func() bool {
		return e2eTabBarBg(t, barHwnd, sampleX, sampleY) == bgDark
	}) {
		t.Fatalf("标签栏底色不是深色 bg：期望 0x%08X，实际 0x%08X",
			bgDark, e2eTabBarBg(t, barHwnd, sampleX, sampleY))
	}

	// ② 点一下 → 设置变成显式 light，且外壳真的跟着换色。
	clickTabBarTheme(t, barHwnd)
	if !waitForCondition(5*time.Second, func() bool {
		return e2eReadSettings(t).Theme == ThemeLight
	}) {
		t.Fatalf("点了配色按钮，设置没有切成 light（当前 %q）", e2eReadSettings(t).Theme)
	}
	if !waitForCondition(5*time.Second, func() bool {
		return e2eTabBarBg(t, barHwnd, sampleX, sampleY) == bgLight
	}) {
		t.Errorf("设置已切到 light，但标签栏底色没变：期望 0x%08X，实际 0x%08X",
			bgLight, e2eTabBarBg(t, barHwnd, sampleX, sampleY))
	}

	// ③ 再点一下 → 退回去（只测单向会漏掉「切得回来」没写对的情况）。
	clickTabBarTheme(t, barHwnd)
	if !waitForCondition(5*time.Second, func() bool {
		return e2eReadSettings(t).Theme == ThemeDark
	}) {
		t.Fatalf("再点一次设置没有切回 dark（当前 %q）", e2eReadSettings(t).Theme)
	}
	if !waitForCondition(5*time.Second, func() bool {
		return e2eTabBarBg(t, barHwnd, sampleX, sampleY) == bgDark
	}) {
		t.Errorf("设置已切回 dark，但标签栏底色没变：期望 0x%08X，实际 0x%08X",
			bgDark, e2eTabBarBg(t, barHwnd, sampleX, sampleY))
	}
}

// ─── 窗口状态持久化：最小化的窗口不该把哨兵矩形写进配置 ──────────────────────

// 「窗口状态」用例专用的临时面板 / 标签 ID。
const (
	e2eMinimizePanelID   = "e2e-minimize-panel"
	e2eMinimizePanelName = "E2E 最小化面板"
	e2eMinimizeTabID     = "e2e-minimize-tab"
)

// e2eAddMinimizePanel 临时加一个带明确窗口状态的面板，返回还原函数。
// 窗口状态是本用例的观测量，所以必须由用例自己给（不能拿用户配置里那条当样本）。
func e2eAddMinimizePanel(t *testing.T) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:      e2eMinimizePanelID,
			Name:    e2eMinimizePanelName,
			Tabs:    []PanelTab{{ID: e2eMinimizeTabID, Name: "E2E 标签", URL: "http://example.lan"}},
			Enabled: true,
			Window:  PanelWindowState{X: 240, Y: 180, Width: 1120, Height: 760},
		})
	})
}

// 「脏窗口状态」用例专用的临时面板 / 标签 ID。
const (
	e2eDirtyWindowPanelID   = "e2e-dirty-window-panel"
	e2eDirtyWindowPanelName = "E2E 脏窗口状态面板"
	e2eDirtyWindowTabID     = "e2e-dirty-window-tab"
)

// TestE2EDirtyWindowStateFallsBackToDefault 守住「配置里的窗口状态不可信就退回默认尺寸」。
//
// 对应的是**存量脏数据**（不是将来）：2026-09-30 在用户便携配置里就有一条 x=-32000 / 160x28
// —— 最小化时被写下的哨兵矩形。照着它打开面板，面板就是一个 160x28 的小方块、还落在屏幕外，
// 用户看到的现象是「点打开没反应」。
//
// 断言窗口**实际尺寸**而不是某个函数返回值：端到端用例要证明的是用户能看到的结果。
func TestE2EDirtyWindowStateFallsBackToDefault(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restorePanel := e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:      e2eDirtyWindowPanelID,
			Name:    e2eDirtyWindowPanelName,
			Tabs:    []PanelTab{{ID: e2eDirtyWindowTabID, Name: "E2E 标签", URL: "http://example.lan"}},
			Enabled: true,
			Window:  PanelWindowState{X: -32000, Y: -32000, Width: 160, Height: 28},
		})
	})
	defer restorePanel()

	restore := e2eForceLightweightQuit(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", e2eDirtyWindowPanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForPanelWindow(t)

	rect := e2eWindowRect(t, panelHwnd)
	if rect.Left <= -30000 || rect.Top <= -30000 || rect.Width() < 320 || rect.Height() < 240 {
		t.Errorf("配置里的窗口状态不可信时应退回默认尺寸，实际拿到 %+v（照用 160x28 会让面板小到看不见）", rect)
	}
}

// TestE2EMinimizedPanelKeepsLastWindowState 守住「最小化后关闭，不把窗口状态写坏」。
//
// 这是 2026-09-30 在用户便携配置里逮到的真实事故：面板最小化 → 从托盘关闭面板 →
// `dispose` 里那次记录读到的 `GetWindowRect` 是 Windows 的哨兵矩形
// (-32000,-32000,160,28)（「图标位置」），于是配置里存下 x=-32000 / 160x28，
// 下次打开那个面板缩成一个小方块、还落在屏幕外。
//
// 断言读的是**配置里的值**：修复的全部意义就是「别写坏配置」，中间状态都不算数。
// 也正因为要看配置，必须等进程真的把配置写完（`waitForNoInstance`）再读。
//
// **诚实说明：这条用例目前无法反向验证。** 把 `captureBounds` 的两道检查全撤掉它照样通过 ——
// 实测下来这条路径本来就写不坏：`dispose` 跑到记录那一步时窗口已销毁，`GetWindowRect` 失败，
// `lastRect` 保持构造值。也就是说，用户配置里那个 -32000 不是从这条路径来的（更可能是
// `recordBounds` 那条，它只在人工拖动窗口时触发，自动化做不出来）。
// 留下它的理由：它把「最小化 → 关闭」这条真实用户路径的现状钉住，将来谁改了 dispose 的
// 时序、让记录重新读到哨兵矩形，它会立刻变红。真正可反向验证的是下面那条
// `TestE2EDirtyWindowStateFallsBackToDefault`（撤掉 `initialWindowRect` 的校验就红）。
func TestE2EMinimizedPanelKeepsLastWindowState(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restorePanel := e2eAddMinimizePanel(t)
	defer restorePanel()

	restore := e2eForceLightweightQuit(t)
	defer restore()

	instance := startE2EInstance(t, exePath, "--open", e2eMinimizePanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForPanelWindow(t)

	// 真的最小化（标题栏最小化按钮走的就是 SW_MINIMIZE）。
	//
	// 先确认「最小化会让 GetWindowRect 变成哨兵矩形」这个前提真的成立 —— 它正是事故的输入，
	// 也是这条用例的全部意义。不验这一步的话，用例可能因为「窗口已销毁导致记录失败」而
	// 恒绿（第一版就是这样：撤掉修复它照样通过）。
	e2eShowWindowW.Call(panelHwnd, win32SWMinimizeForE2E)
	var rect e2eRect
	sentinel := waitForCondition(5*time.Second, func() bool {
		iconic, _, _ := e2eIsIconic.Call(panelHwnd)
		if iconic == 0 {
			return false
		}
		e2eGetWindowRect.Call(panelHwnd, uintptr(unsafe.Pointer(&rect)))
		return rect.Left <= -30000 || rect.Top <= -30000
	})
	if !sentinel {
		t.Fatalf("窗口最小化后未拿到哨兵矩形（rect=%+v），用例前提不成立", rect)
	}
	t.Logf("最小化状态下 GetWindowRect = %+v（哨兵矩形，即事故的输入）", rect)

	// 主动关闭 —— 等价于托盘菜单「关闭面板」，正是写坏配置的那条路径。
	if r, _, _ := e2ePostMessageW.Call(panelHwnd, win32WMDirectClose, 0, 0); r == 0 {
		t.Fatal("发送直接关闭消息失败")
	}
	waitForNoInstance(t)

	cfg := e2eReadConfig(t)
	var got *PanelWindowState
	for i := range cfg.Panels {
		if cfg.Panels[i].ID == e2eMinimizePanelID {
			got = &cfg.Panels[i].Window
		}
	}
	if got == nil {
		t.Fatal("配置里找不到该面板（用例的 patch 被别人覆盖了？）")
	}
	if got.Width != 1120 || got.Height != 760 || got.X != 240 || got.Y != 180 {
		t.Errorf("最小化后再关闭不该改写窗口状态：期望 240,180 / 1120x760，配置里却是 %+v", *got)
	}
}

// ─── 自绘标题栏「是活的」：WebView2 事件真的回调到了宿主 ──────────────────────

// 「标题栏跟随导航」用例专用的临时面板 / 标签 ID。
// 另开一套而不复用「关闭后清空」用例的 ID：两条用例各自造面板、各自还原，
// 免得跑了其中一条之后另一条的前置被改动。
const (
	e2eTitlebarPanelID   = "e2e-titlebar-panel"
	e2eTitlebarPanelName = "E2E 标题栏面板"
	e2eTitlebarTabID     = "e2e-titlebar-tab"
)

// e2eAddTitlebarPanel 临时加一个指向本地页面的测试面板，返回还原函数。
// 页面由用例自己起（浏览器要真的把页面加载出来，标题栏才有东西可同步）。
func e2eAddTitlebarPanel(t *testing.T, rawURL string) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		cfg.Panels = append(cfg.Panels, PanelConfig{
			ID:      e2eTitlebarPanelID,
			Name:    e2eTitlebarPanelName,
			Tabs:    []PanelTab{{ID: e2eTitlebarTabID, Name: "E2E 标签", URL: rawURL}},
			Enabled: true,
			Window:  PanelWindowState{Width: 900, Height: 640},
		})
	})
}

// e2eWindowText 读窗口文本（地址栏就是 EDIT 控件，内容同样走 WM_GETTEXT）。
//
// **必须用 SendMessage(WM_GETTEXT)，不能用 GetWindowTextW**：后者对「另一个进程里的控件」
// 按设计返回空串（它只回内核里缓存的那份窗口标题，跨进程时控件文本读不到），
// 于是断言永远是「空 == 期望」，白白变红。WM_GETTEXT 的消息参数由系统帮忙封送，
// 跨进程才拿得到真正的内容。
func e2eWindowText(hwnd uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := e2eSendMessageW.Call(
		hwnd,
		win32WMGetText,
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if n == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// TestE2ETitlebarFollowsRealNavigation 验证自绘标题栏**真的接到了 WebView2 的导航事件**。
//
// 为什么必须单独有这条用例 —— 骨架用例 `TestE2EPanelWindowIsFramelessWithCustomTitleBar`
// **抓不到 2026-09-30 那次事故**：手工 `ICoreWebView2` vtable 漏了 `NavigateToString`（槽位 6），
// 其后所有槽位整体错位一格，`add_*` 统统落到隔壁的 `remove_*` 上——**返回 S_OK、token 也写出来了，
// 但回调永不触发**；`ExecuteScript` 则落到 `RemoveScriptToExecuteOnDocumentCreated` 上，
// 脚本根本没执行。结果是地址栏不刷新、favicon 永远是兜底地球字形，而**编译 / go vet /
// 单测 / 全部 E2E 一路全绿**（窗口骨架完全正常）。
//
// 这条用例的两条断言都刻意选「**只有我方代码真的跑起来才可能成立**」的观测量。第一版用例
// 两条断言都是空的，靠反向验证（抽掉 `NavigateToString` 重建，用例照样 PASS）才发现，
// 记在这里免得后人重犯：
//
//   - 地址栏文本由 `tabState.currentURL` 驱动，而 `currentURL` **在标签创建时就被配置 URL 种了值**
//     （`panel_window_windows.go` 里 `currentURL: t.URL`）。所以「配置 URL == 最终 URL」的写法
//     等价于「地址栏从一开始就是对的」，跟事件到没到毫无关系。**必须让两者不同**：
//     配置指向 `/start`，`/start` 用 302 跳到 `/final`，断言地址栏最终为 `/final` ——
//     这个变化只可能由 `add_SourceChanged` / `add_NavigationCompleted` 写进去。
//
//   - `/icon.png` 也不能只断言「有人来取过」：**Chromium 自己会抓 favicon**（WebView2 的浏览器
//     进程内置 favicon 服务），于是即使 `ExecuteScript` 落到了错误槽位上、脚本根本没执行，
//     服务器照样收得到请求，断言永远成立。判别器是 **User-Agent**：我方 `panelFetchBytes`
//     显式带 `PanelDock/1.0`（见 `icons_windows.go`），Chromium 带的是 Chrome UA。
//     只认 `PanelDock/` 前缀的请求，才等价于「ExecuteScript 真的执行了 → 脚本返回了 favicon URL
//     → 宿主拿 Go 的 http 客户端去下载」。
//
//   - 第三条守的是**图标安装**那段：favicon 取回来变成 HICON 再 WM_SETICON 到窗口上，
//     中途任何一步失败（PNG 编码、CreateIconFromResourceEx、发消息）任务栏上就还是白纸。
//     `WM_GETICON` 只返回**显式设置过**的图标，返回 0 就等于没装上 —— 与系统给无图标窗口
//     兜底显示 exe 图标的行为不冲突，因为那个兜底不经过 WM_GETICON。
//
// 不做像素断言（截图在受限环境里不可靠），但这两条已经把链路上最容易错的两段守住了。
func TestE2ETitlebarFollowsRealNavigation(t *testing.T) {
	exePath, _ := e2eSkipUnlessReady(t)

	restore := e2eForceLightweightQuit(t)
	defer restore()

	var (
		hostFetchedIcon    int32 // 带 PanelDock UA 的 /icon.png 请求：只有我方 net/http 会带
		browserFetchedIcon int32 // 其它 UA 的 /icon.png 请求：Chromium 自己的 favicon 抓取
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/start":
			// 起始地址必须与最终地址不同，见上方注释第 1 条。
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<html><head><link rel="icon" href="/icon.png"></head><body>ok</body></html>`)
		case "/icon.png":
			if strings.HasPrefix(r.Header.Get("User-Agent"), "PanelDock/") {
				atomic.AddInt32(&hostFetchedIcon, 1)
			} else {
				atomic.AddInt32(&browserFetchedIcon, 1)
			}
			w.Header().Set("Content-Type", "image/png")
			// 内容不需要可解码：用例断言的是「宿主来取了」，不是「解码画出来了」。
			_, _ = io.WriteString(w, "\x89PNG\r\n\x1a\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	defer e2eUsePortableProfiles(t)()
	defer e2eAddTitlebarPanel(t, srv.URL+"/start")()

	tabDir, err := tabProfileDir(e2eTitlebarTabID)
	if err != nil {
		t.Fatalf("tabProfileDir: %v", err)
	}
	t.Cleanup(func() { _ = removeProfileDir(tabDir, clearUntilStable) })

	instance := startE2EInstance(t, exePath, "--open", e2eTitlebarPanelID)
	defer func() { _ = instance.Process.Kill() }()

	panelHwnd := waitForWindowByTitle(t, panelWindowTitle(e2eTitlebarPanelName))

	var titleHwnd uintptr
	if !waitForCondition(15*time.Second, func() bool {
		titleHwnd = findChildWindowByClass(panelHwnd, "PanelDock.PanelTitleBar")
		return titleHwnd != 0
	}) {
		t.Fatal("超时：未找到自绘标题栏子窗口")
	}
	editHwnd := findChildWindowByClass(titleHwnd, "EDIT")
	if editHwnd == 0 {
		t.Fatal("未找到自绘标题栏里的地址栏 EDIT")
	}

	// ① 导航事件到达宿主：地址栏从配置的 /start 变成实际落地的 /final。
	wantURL := srv.URL + "/final"
	if !waitForCondition(25*time.Second, func() bool { return e2eWindowText(editHwnd) == wantURL }) {
		t.Errorf("地址栏未同步为实际 URL：期望 %q，实际 %q（WebView2 导航事件没回调到宿主？注意配置里写的是 /start）",
			wantURL, e2eWindowText(editHwnd))
	}

	// ② ExecuteScript 真的执行了：favicon 脚本跑出来返回 URL，宿主才会用 Go 的 http 客户端去取。
	//    只认 PanelDock UA —— 服务器收到 Chromium 的请求不算数。
	if !waitForCondition(15*time.Second, func() bool { return atomic.LoadInt32(&hostFetchedIcon) > 0 }) {
		t.Errorf("宿主没有用 Go http 客户端去取 favicon —— ExecuteScript 没有真正执行（vtable 槽位错位？）"+
			"（Chromium 自己的 favicon 请求计数=%d，不作为依据）", atomic.LoadInt32(&browserFetchedIcon))
	}

	// ③ 图标真的装到了窗口上：任务栏 / Alt+Tab 用的是 ICON_BIG。
	//    WM_GETICON 只认「显式设置过」的图标（系统给无图标窗口的 exe 兜底不走它），
	//    返回 0 就等于这条链路断在了 PNG / HICON / WM_SETICON 的某一步。
	if !waitForCondition(15*time.Second, func() bool {
		big, _, _ := e2eSendMessageW.Call(panelHwnd, win32WMGetIcon, win32IconBig, 0)
		return big != 0
	}) {
		big, _, _ := e2eSendMessageW.Call(panelHwnd, win32WMGetIcon, win32IconBig, 0)
		t.Errorf("窗口没有大图标（WM_GETICON(ICON_BIG) = %#x）—— favicon 取到了却没装上去？"+
			"查 panelHIconFromImage / CreateIconFromResourceEx / applyPanelWindowIcons", big)
	}
}

// e2eRect 与 Win32 RECT 内存布局一致。
type e2eRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

func (r e2eRect) Width() int32  { return r.Right - r.Left }
func (r e2eRect) Height() int32 { return r.Bottom - r.Top }

// e2eWindowRect 返回窗口矩形（屏幕坐标）。
func e2eWindowRect(t *testing.T, hwnd uintptr) e2eRect {
	t.Helper()
	var rect e2eRect
	if ok, _, _ := e2eGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		t.Fatalf("GetWindowRect 失败（hwnd=%d）", hwnd)
	}
	return rect
}

// e2eClientRect 返回客户区矩形（客户区坐标，左上角恒为 0,0）。
func e2eClientRect(t *testing.T, hwnd uintptr) e2eRect {
	t.Helper()
	var rect e2eRect
	if ok, _, _ := e2eGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		t.Fatalf("GetClientRect 失败（hwnd=%d）", hwnd)
	}
	return rect
}

// findChildWindowByClass 在 parent 的直接子窗口中按类名查找；找不到返回 0。
func findChildWindowByClass(parent uintptr, class string) uintptr {
	ptr, err := windows.UTF16PtrFromString(class)
	if err != nil {
		return 0
	}
	hwnd, _, _ := e2eFindWindowExW.Call(parent, 0, uintptr(unsafe.Pointer(ptr)), 0)
	return hwnd
}

// waitForCondition 轮询条件直到成立或超时。
func waitForCondition(timeout time.Duration, condition func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return true
		}
		time.Sleep(120 * time.Millisecond)
	}
	return condition()
}

// e2ePatchSettings 临时修改便携配置里的应用级设置，返回还原函数。
// 备份按原字节保存并在用例结束（或 panic）后归还，避免污染用户真实配置。
//
// 还原是**无条件**的：即使本次补丁没有任何实际改动也要归还原始字节。
// 因为被测程序自己也会写配置（把某处关闭行为设为「最小化到托盘」会连带打开托盘图标；
// 在询问框里勾「记住我的选择」会写入关闭行为），跳过还原就会把便携包改坏，
// 还让后续用例静默跳过（跳过 ≠ 通过，串跑时没人会发现）。
func e2ePatchSettings(t *testing.T, patch func(*AppSettings)) func() {
	t.Helper()

	return e2ePatchConfig(t, func(cfg *AppConfig) {
		settings := defaultAppSettings()
		if cfg.Settings != nil {
			settings = *cfg.Settings
		}
		patch(&settings)
		// 用例里的窗口标题断言（关闭询问框 / 「面板已停用」询问框的 Caption）都是中文，
		// 语言必须钉死，否则在英文 Windows 上 auto 会解析成 en-US、按中文标题找窗口全部失败。
		settings.Language = LanguageZhCN
		cfg.Settings = &settings
	})
}

// e2ePatchConfig 按原字节备份便携配置、应用补丁、返回无条件还原函数。
//
// 这是本套用例改配置的**唯一**入口（e2ePatchSettings 也委托给它）：备份 / 还原这套
// 规矩踩过两次坑（「无改动就跳过还原」把用户配置改坏、改新字段却不清废弃字段），
// 只留一份实现才不会重蹈覆辙。
func e2ePatchConfig(t *testing.T, patch func(*AppConfig)) func() {
	t.Helper()

	path := filepath.Join("build", "bin", "data", "config.json")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("读取便携配置失败: %v", err)
	}

	restore := func() {
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Errorf("还原配置失败: %v", err)
		}
	}

	var cfg AppConfig
	if err := json.Unmarshal(original, &cfg); err != nil {
		t.Skipf("解析便携配置失败: %v", err)
	}

	patch(&cfg)

	updated, err := json.MarshalIndent(&cfg, "", "  ")
	if err != nil {
		t.Fatalf("编码配置失败: %v", err)
	}
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	return restore
}

// e2eForceCloseActionAsk 把面板窗口的关闭行为临时改为「每次询问」，
// 并把轻量模式的「用完即走」强制打开。
//
// 必须同时清掉旧版 closeAction 字段：它在加载时会被迁移并**覆盖**面板那一项，
// 留着它上面的 patch 就白改了（便携包里可能仍是旧格式）。
// 用完即走也要强制打开：多个用例的前提都是「关闭最后一个面板 → 进程退出」，
// 而这一项是可配置的，用户配置里关掉它那些用例就全挂了。
func e2eForceCloseActionAsk(t *testing.T) func() {
	t.Helper()
	return e2ePatchSettings(t, func(s *AppSettings) {
		s.PanelCloseAction = CloseActionAsk
		s.LightweightQuitOnLastPanel = true
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
}

// e2eForceLightweightQuit 只保证「轻量模式关掉最后一个面板 → 进程退出」这一个前提成立。
//
// 它与 e2eForceCloseActionAsk 分开：多数「关闭最后一个面板」的用例并不关心关闭行为是哪一档，
// 但都一样依赖这个可配置的开关。不强制打开的话，用户把这一项关掉就会让一串用例假失败。
func e2eForceLightweightQuit(t *testing.T) func() {
	t.Helper()
	return e2ePatchSettings(t, func(s *AppSettings) {
		s.LightweightQuitOnLastPanel = true
		s.DeprecatedCloseAction = ""
		s.DeprecatedManagerCloseAction = nil
	})
}

// waitForPanelWindow 等待面板窗口出现，超时即失败。
func waitForPanelWindow(t *testing.T) uintptr {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if hwnd := findWindowByClass(panelWindowClassNameString()); hwnd != 0 {
			return hwnd
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatal("超时：面板窗口未出现")
	return 0
}

// waitForNoInstance 等待上一个实例彻底退出（IPC 窗口消失），避免影响后续阶段。
func waitForNoInstance(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if findWindowByClass(ipcWindowClass) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("上一个实例未完全退出")
}

// panelWindowClassNameString 返回面板窗口类名（与 panel_window_windows.go 中保持一致）。
func panelWindowClassNameString() string {
	return "PanelDock.IsolatedPanel"
}
