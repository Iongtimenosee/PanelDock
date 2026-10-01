//go:build windows

package main

// 「固定到任务栏」为什么只能做一半 —— 这是 Windows 的硬限制，不是没实现。
//
// Windows 7/8 时代 shell 提供过 `taskbarpin` 动词（ShellExecuteEx 的 lpVerb），程序可以
// 直接把快捷方式钉到任务栏。微软从 Windows 10 起明确关掉了这条路，官方答复是：
// 「固定到任务栏属于用户偏好，程序不应代为决定，只有用户本人可以固定」。
// Windows 11 又把仅剩的两条旁路也封了：
//   · 直接往 `%APPDATA%\Microsoft\Internet Explorer\Quick Launch\User Pinned\TaskBar`
//     拷 .lnk 不再生效（虽然固定项确实存在这个目录里）；
//   · `shell:::{4234d49b-0245-4df3-b780-3893943456e1}` 命名空间的 pin 动词被隐藏，
//     该命名空间现在只能取消固定，不能固定。
// 目前唯一还能无人值守写入任务栏的办法是 LayoutModification.xml + 杀掉并重启 explorer.exe，
// 代价是任务栏整条消失再重建、所有托盘图标重载，写错还会覆盖用户已经排好的任务栏布局。
//
// **而且 `taskbarpin` 连「无害地失败」都做不到**（2026-09-30 实测，见 AGENTS.md）：
// 传入 shell 认不出的动词时它不会报错，而是**退化成 `open`** —— 目标程序被真的启动。
// 也就是说留着「先试一下自动固定」这条路，用户点「固定任务栏」的副作用是多开一个面板窗口，
// 而固定本身照样不会发生。所以这条路必须整条去掉，不是「试了没用就回退」。
//
// 最终做法：只做「备好快捷方式 + 确认状态 + 讲清引导」，一步都不替用户走。
//   1. 确保该面板的 .lnk 存在（任务栏固定本质就是把这个 .lnk 复制进固定目录）；
//   2. 回读任务栏固定目录，判断是不是已经固定过了（判定与桌面扫描同源）；
//   3. 剩下的一律交给用户：前端对话框讲清「右键 →『固定到任务栏』（Windows 11 需先点
//      『显示更多选项』）」，并给一个「选中快捷方式」按钮 —— 用户读完自己点，才在
//      资源管理器中选中该 .lnk（`revealShortcutInExplorer`）。
//
// 第 3 步**不随弹框自动发生**（2026-09-30 调整）：弹出对话框的同时抢走前台，
// 用户还没来得及读那几句说明就被切到资源管理器，属于「擅作主张」。什么时候切过去，
// 由用户点按钮决定。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── Win32 声明 ────────────────────────────────────────────────────────────────

var shortcutShellExecuteExW = shortcutShell32.NewProc("ShellExecuteExW")

const (
	// SEE_MASK_NOASYNC：不把操作丢到后台线程。
	shortcutSEEMaskNoAsync = 0x00000100
	// SEE_MASK_FLAG_NO_UI：出错时不要弹 Windows 自己的错误框，由我们把话说清楚。
	shortcutSEEMaskFlagNoUI = 0x00000400

	// SW_SHOWNORMAL：让 explorer 正常显示并选中目标。
	shortcutSWShowNormal = 1
)

// shortcutShellExecuteInfoW 对应 SHELLEXECUTEINFOW（shellapi.h，x64 布局）。
// 字段顺序与对齐必须与 ABI 一致，改动需重新核对：
//
//	cbSize(4) fMask(4) hwnd(8) lpVerb(8) lpFile(8) lpParameters(8) lpDirectory(8)
//	nShow(4) [4 字节对齐填充] hInstApp(8) lpIDList(8) lpClass(8) hkeyClass(8)
//	dwHotKey(4) [4 字节对齐填充] 联合体 hIcon/hMonitor(8) hProcess(8) —— 共 112 字节
type shortcutShellExecuteInfoW struct {
	CbSize         uint32
	FMask          uint32
	Hwnd           uintptr
	LpVerb         *uint16
	LpFile         *uint16
	LpParameters   *uint16
	LpDirectory    *uint16
	NShow          int32
	HInstApp       uintptr
	LpIDList       uintptr
	LpClass        *uint16
	HkeyClass      uintptr
	DwHotKey       uint32
	HIconOrMonitor uintptr
	HProcess       uintptr
}

// ─── 可注入的测试缝 ────────────────────────────────────────────────────────────

var (
	// shortcutTaskbarPinnedDir 返回 Windows 存放「任务栏固定项快捷方式」的目录。
	// 测试注入临时目录，避免读写用户真实的任务栏。
	shortcutTaskbarPinnedDir = defaultShortcutTaskbarPinnedDir

	// shortcutRevealInExplorer 在资源管理器中打开并选中一个文件。
	// 测试注入假实现：真跑会弹出资源管理器窗口。
	shortcutRevealInExplorer = shortcutRevealInExplorerReal
)

// ─── 对外接口 ──────────────────────────────────────────────────────────────────

// isPanelPinnedToTaskbar 回读任务栏固定目录，判断该面板是否已被固定到任务栏。
// 判定与桌面扫描同源（listPanelShortcuts）：快捷方式指向本程序（按文件名比对）
// 且参数为 `--open <面板ID>`。
//
// 为什么靠回读目录而不是靠某个 API 的返回值：Windows 没有「查询固定状态」的接口，
// 而这个目录就是事实本身 —— 固定项以 .lnk 形式存放在这里。Windows 自己生成的固定项
// 能被 IShellLinkW 正常读出目标与参数，参数也被完整保留（连 `-taskbar-tab <uuid>` 这类都在）。
func isPanelPinnedToTaskbar(panelID string) bool {
	if panelID == "" {
		return false
	}
	exeBase := shortcutSelfExeBase()
	if exeBase == "" {
		return false
	}

	dir, err := shortcutTaskbarPinnedDir()
	if err != nil {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false // 目录不存在 = 从没固定过任何东西
	}

	found := false
	// 读 .lnk 需要 COM 已初始化。
	_ = shortcutWithCOM(func() error {
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".lnk") {
				continue
			}
			target, args, err := readShortcutTargetInCOM(filepath.Join(dir, entry.Name()))
			if err != nil {
				continue // 损坏的固定项、或 File Explorer 这类无目标的特殊项：跳过
			}
			if !strings.EqualFold(filepath.Base(target), exeBase) {
				continue
			}
			if !shortcutArgsMatchPanel(args, panelID) {
				continue
			}
			found = true
			return nil
		}
		return nil
	})
	return found
}

// revealShortcutInExplorer 在资源管理器中打开所在目录并选中该快捷方式。
// 这是「无法自动固定」之后的引导落点：用户右键就能看到「固定到任务栏」。
// 只由用户点「选中快捷方式」时调用 —— 不在弹框时自动执行，理由见文件头。
func revealShortcutInExplorer(lnkPath string) error {
	if lnkPath == "" {
		return errCode(errShortcutPathEmpty)
	}
	if _, err := os.Stat(lnkPath); err != nil {
		return errCodeWrap(errShortcutMissingFile, err)
	}
	return shortcutRevealInExplorer(lnkPath)
}

// ─── 具体实现 ──────────────────────────────────────────────────────────────────

func shortcutRevealInExplorerReal(lnkPath string) error {
	// /select,<path> 让资源管理器定位并选中该文件。路径可能含空格，必须带引号。
	return shortcutShellExecute("open", "explorer.exe", `/select,"`+lnkPath+`"`)
}

// shortcutShellExecute 用 ShellExecuteExW 执行一次 shell 动作，失败返回错误。
//
// 注意：**不要**用它去执行 `taskbarpin` 这类 shell 已不再支持的动词 —— 见文件头说明，
// 它会静默退化成 open 并把目标程序启动起来。
func shortcutShellExecute(verb, file, params string) error {
	verbPtr, err := windows.UTF16PtrFromString(verb)
	if err != nil {
		return err
	}
	filePtr, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}

	info := shortcutShellExecuteInfoW{
		CbSize: uint32(unsafe.Sizeof(shortcutShellExecuteInfoW{})),
		FMask:  shortcutSEEMaskNoAsync | shortcutSEEMaskFlagNoUI,
		LpVerb: verbPtr,
		LpFile: filePtr,
		NShow:  shortcutSWShowNormal,
	}
	if params != "" {
		paramPtr, err := windows.UTF16PtrFromString(params)
		if err != nil {
			return err
		}
		info.LpParameters = paramPtr
	}

	ok, _, callErr := shortcutShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		return fmt.Errorf("ShellExecuteEx(%s %s): %w", verb, file, callErr)
	}
	return nil
}

// defaultShortcutTaskbarPinnedDir 返回任务栏固定项目录。
// 用 %APPDATA% 拼路径而不是 SHGetKnownFolderPath：这个目录没有对应的 KNOWNFOLDERID，
// 它是 shell 自己约定的固定位置，各版本 Windows 都一致。
func defaultShortcutTaskbarPinnedDir() (string, error) {
	appData, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appData, "Microsoft", "Internet Explorer", "Quick Launch", "User Pinned", "TaskBar"), nil
}
