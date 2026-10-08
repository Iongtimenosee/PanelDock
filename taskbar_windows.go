//go:build windows

package main

// 「固定到任务栏」只能做到「准备好 + 引导」这一半：Windows 10 起不允许程序自己固定
// （微软立场：固定属于用户偏好），Windows 11 又封了仅剩的旁路 —— 见 docs/behavior.md#固定到任务栏。
//
// ⚠️ **不要**试图用 `taskbarpin` 动词绕过：shell 认不出该动词时**静默退化成 `open`**
// （返回成功，却把目标程序真启动了），副作用是用户点一次「固定」就白开一个面板窗口。
// 另一条 LayoutModification.xml 的路要杀掉并重启 explorer.exe，代价更大。见 docs/pitfalls.md。
//
// 本文件只做三步：确保 .lnk 存在 → 回读固定目录判是否已固定 → 交给用户自己右键固定。
// 选中快捷方式**不随弹框自动发生**（抢前台会盖掉还没读完的说明），由用户点按钮触发。

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

// isPanelPinnedToTaskbar 回读任务栏固定目录判断该面板是否已固定。
// 判定与桌面扫描同源（listPanelShortcuts）：指向本程序（文件名比对）+ 参数为 `--open <面板ID>`。
//
// 靠回读目录是因为 Windows 没有「查询固定状态」的 API，而这个目录就是事实本身。
// Windows 自己生成的固定项能被 IShellLinkW 正常读出目标与参数（连 `-taskbar-tab <uuid>` 都在）。
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
