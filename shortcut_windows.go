//go:build windows

package main

// 桌面快捷方式（.lnk）创建：用 IShellLinkW + IPersistFile 手工 COM 子集实现。
// 与 panel_window_windows.go 保持同一风格：手工 vtable 声明，不引入额外依赖。
// 快捷方式目标固定为 `PanelDock.exe --open <面板ID>`，面板改名后仍有效（ID 不可变）。

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ─── Win32 / COM 声明 ──────────────────────────────────────────────────────────

var (
	shortcutOle32            = windows.NewLazySystemDLL("ole32.dll")
	shortcutShell32          = windows.NewLazySystemDLL("shell32.dll")
	shortcutCoCreateInstance = shortcutOle32.NewProc("CoCreateInstance")
	shortcutSHGetFolderPathW = shortcutShell32.NewProc("SHGetFolderPathW")
)

const (
	// 用 CSIDL_DESKTOP 而非 CSIDL_DESKTOPDIRECTORY：实测在桌面被重定向到非系统盘的
	// 机器上后者返回 E_FAIL，前者能正确跟随重定向返回物理桌面目录。
	shortcutCSIDLDesktop       = 0x00
	shortcutCLSCTXInprocServer = 0x1

	// 读取 .lnk 内路径/参数时的缓冲长度（MAX_PATH 足够存放快捷方式里的字符串）。
	shortcutReadBufferSize = windows.MAX_PATH
)

// shortcutSFalse 是 CoInitializeEx 在「本线程已按同一模式初始化」时返回的 S_FALSE。
// x/sys 的 CoInitializeEx 包装把任何非 0 HRESULT 都转成 error，S_FALSE(1) 于是
// 表现为 ERROR_INVALID_FUNCTION（"Incorrect function"），必须单独识别。
const shortcutSFalse = syscall.Errno(0x00000001)

// CLSID / IID（固定 ABI 常量，来自 ShObjIdl_core.h / ObjIdl.h）。
var (
	shortcutCLSIDShellLink = windows.GUID{
		Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	shortcutIIDIShellLinkW = windows.GUID{
		Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	shortcutIIDIPersistFile = windows.GUID{
		Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

// ─── IShellLinkW vtable（ShObjIdl_core.h，顺序不可变）─────────────────────────

type shortcutIShellLinkWVtbl struct {
	panelIUnknownVtbl                // 槽位 0-2
	GetPath             panelCOMProc // 3
	GetIDList           panelCOMProc // 4
	SetIDList           panelCOMProc // 5
	GetDescription      panelCOMProc // 6
	SetDescription      panelCOMProc // 7
	GetWorkingDirectory panelCOMProc // 8
	SetWorkingDirectory panelCOMProc // 9
	GetArguments        panelCOMProc // 10
	SetArguments        panelCOMProc // 11
	GetHotkey           panelCOMProc // 12
	SetHotkey           panelCOMProc // 13
	GetShowCmd          panelCOMProc // 14
	SetShowCmd          panelCOMProc // 15
	GetIconLocation     panelCOMProc // 16
	SetIconLocation     panelCOMProc // 17
	SetRelativePath     panelCOMProc // 18
	Resolve             panelCOMProc // 19
	SetPath             panelCOMProc // 20
}

type shortcutIShellLinkW struct {
	Vtbl *shortcutIShellLinkWVtbl
}

// ─── IPersistFile vtable（ObjIdl.h，顺序不可变）───────────────────────────────

type shortcutIPersistFileVtbl struct {
	panelIUnknownVtbl              // 槽位 0-2
	GetClassID        panelCOMProc // 3
	IsDirty           panelCOMProc // 4
	Load              panelCOMProc // 5
	Save              panelCOMProc // 6
	SaveCompleted     panelCOMProc // 7
	GetCurFile        panelCOMProc // 8
}

type shortcutIPersistFile struct {
	Vtbl *shortcutIPersistFileVtbl
}

// ─── 对外接口 ──────────────────────────────────────────────────────────────────

// shortcutWrite 是 createDesktopShortcut 的结果。
type shortcutWrite struct {
	// Path 是这次写下的 .lnk 完整路径。
	Path string
	// Created 为真表示桌面本来没有这个分组的快捷方式，这次是新建。
	Created bool
	// Renamed 为真表示覆盖既有那份时，把它的文件名同步成了当前面板名（旧文件已被替换掉）。
	Renamed bool
}

// createDesktopShortcut 确保该面板在桌面上有且仅有一份快捷方式，返回它的完整路径。
// exePath 为目标程序绝对路径，panelID 追加为 `--open` 参数，panelName 用于命名。
// iconPath 非空时快捷方式用它当图标（站点图标缓存），否则用 exe 自带的图标。
// recorded 是配置里记录的路径，用来在扫描结果里优先挑中「我们一直在维护的那一份」。
//
// 已有就**覆盖**那一份，并把它的名字同步成当前面板名；没有才新建 —— 不再生成
// 「名字 (2).lnk」这类副本：桌面是给人看的，同一个分组堆出好几份只会让人分不清哪个是哪个，
// 删面板时还要一口气清一堆。
//
// 名字为什么一定要同步（而不是保留用户起的文件名）：面板改名时快捷方式已经跟着改名了
// （见 renamePanelDesktopShortcut），若点这个按钮又保留旧名，同一个分组就会出现
// 「改名自动同步、点按钮不同步」的两套规则 —— 用户只会当程序坏了。
//
// 注意：SHGetFolderPathW 与 IShellLink 都要求 COM 已初始化，全程包在 shortcutWithCOM 内。
//
// 桌面目录必须走可注入的 shortcutDesktopDirectory（而不是直接调 shortcutDesktopDir）：
// 否则测试里调 CreatePanelShortcut 会往**用户真实桌面**写文件
// （TestPinPanelToTaskbarCreatesShortcutWhenMissing 逮到过：桌面上真的多了一个 .lnk）。
func createDesktopShortcut(exePath, panelID, panelName, iconPath, recorded string) (shortcutWrite, error) {
	var out shortcutWrite
	var stale string
	err := shortcutWithCOM(func() error {
		desktop, err := shortcutDesktopDirectory()
		if err != nil {
			return err
		}
		if existing := findPanelDesktopShortcut(desktop, panelID, recorded); existing != "" {
			out.Path = shortcutRenameTarget(desktop, panelName, existing)
			if !strings.EqualFold(out.Path, existing) {
				stale = existing
				out.Renamed = true
			}
		} else {
			out.Path = uniqueShortcutPath(desktop, panelName)
			out.Created = true
		}
		return writeShortcutLnk(out.Path, exePath, "--open "+panelID, shortcutDescription(panelName), iconPath)
	})
	if err != nil {
		return shortcutWrite{}, err
	}
	// 新名字确实写成功了才删旧的：写失败时旧的那份还在，不会被凭空抹掉。
	if stale != "" {
		_ = os.Remove(stale)
	}
	return out, nil
}

// shortcutDescription 是快捷方式备注文字的格式（悬停时看到的说明）。
// 抽成函数是因为创建与改名两处都要用同一句 —— 各写一遍的话，改了格式就会漏一处。
func shortcutDescription(panelName string) string {
	return "PanelDock · " + panelName
}

// renamePanelDesktopShortcut 把该分组在桌面的快捷方式改名为 newName，返回改名后的路径。
// 桌面没有这个分组的快捷方式时返回空串 —— 改名**不新建**：用户没要过快捷方式，
// 改个面板名不该凭空在桌面上多出一个 .lnk。
//
// 同步的是三样：文件名、备注里的面板名、以及配置里记录的路径（后者由调用方写回）。
// 目标与参数原样保留：面板 ID 没变，快捷方式本来就还能用，动它只会把人带到别处去。
//
// 顺序刻意是「先重写内容，再改文件名」：写内容失败时文件还在原位、内容也还是旧的，
// 等于什么都没发生；反过来先改名再写，写失败就留下一个改了名却内容过期的文件。
//
// 任务栏固定目录**刻意不在范围内** —— 见 collapsePanelDesktopShortcuts 的理由。
func renamePanelDesktopShortcut(panelID, newName, recorded string) (string, error) {
	desktop, err := shortcutDesktopDirectory()
	if err != nil {
		return "", err
	}
	existing := findPanelDesktopShortcut(desktop, panelID, recorded)
	if existing == "" {
		return "", nil
	}
	want := shortcutRenameTarget(desktop, newName, existing)

	var target, args, icon string
	if err := shortcutWithCOM(func() error {
		return withLoadedShortcut(existing, func(link *shortcutIShellLinkW) error {
			var inner error
			if target, inner = shortcutGetPath(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetPath: %w", inner)
			}
			if args, inner = shortcutGetArguments(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetArguments: %w", inner)
			}
			if icon, _, inner = shortcutGetIconLocation(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetIconLocation: %w", inner)
			}
			return nil
		})
	}); err != nil {
		return "", err
	}

	if err := shortcutWithCOM(func() error {
		return writeShortcutLnk(existing, target, args, shortcutDescription(newName), icon)
	}); err != nil {
		return "", err
	}
	if !strings.EqualFold(want, existing) {
		if err := os.Rename(existing, want); err != nil {
			return existing, err
		}
	}
	return want, nil
}

// shortcutRenameTarget 算出改名后的目标路径：旧文件已经叫这个名字就不动（含只有大小写不同），
// 这个名字空着就直接用，被别的文件占了才退到带序号的名字。
//
// 不能直接用 uniqueShortcutPath：旧文件此刻还在桌面上，它会把自己当成「已占用」，
// 于是每次改名都多出一个「新名 (2).lnk」。
func shortcutRenameTarget(desktop, newName, existing string) string {
	first := filepath.Join(desktop, sanitizeShortcutName(newName)+".lnk")
	if strings.EqualFold(first, existing) {
		return existing
	}
	if _, err := os.Stat(first); errors.Is(err, os.ErrNotExist) {
		return first
	}
	return uniqueShortcutPath(desktop, newName)
}

// findPanelDesktopShortcut 在桌面目录里找出该分组**已有的**快捷方式；一份都没有时返回空串。
//
// 两条来源，缺一不可：
//   - 扫描桌面（目标是本程序 + 参数含 `--open <面板ID>`）：用户改过名也照样认得出；
//   - 配置里记录的那一份：文件确实躺在桌面上就算数。这条兜底覆盖扫描认不出的情况 ——
//     程序被改过名（`PanelDock.exe` → 新版文件名）之后，旧 .lnk 的目标名匹配不上了，
//     但它明明是这个分组的快捷方式，该被覆盖而不是再堆一份新的。
func findPanelDesktopShortcut(desktop, panelID, recorded string) string {
	found := listPanelShortcutsIn([]string{desktop}, panelID, "")
	for _, path := range found {
		if recorded != "" && strings.EqualFold(path, recorded) {
			return path
		}
	}
	if recorded != "" && strings.EqualFold(filepath.Dir(recorded), desktop) {
		if info, err := os.Stat(recorded); err == nil && !info.IsDir() {
			return recorded
		}
	}
	return pickPanelShortcut(found, "")
}

// pickPanelShortcut 从一组候选里挑出「该留下的那一份」：优先配置记录的那个，其次第一个。
// 没有候选时返回空串。
func pickPanelShortcut(found []string, recorded string) string {
	if len(found) == 0 {
		return ""
	}
	if recorded != "" {
		for _, path := range found {
			if strings.EqualFold(path, recorded) {
				return path
			}
		}
	}
	return found[0]
}

// collapsePanelDesktopShortcuts 把桌面上该分组多余的快捷方式收敛掉，只留 keep 那一份，
// 返回实际被删掉的路径。
//
// 删的全是**本程序自己为该分组创建的**（扫描条件：目标是本程序 + 参数含 `--open <面板ID>`），
// 不是用户手工新建的无关文件。
//
// 任务栏固定目录**刻意不在范围内**：那是用户自己右键固定上去的，一份固定项本来也只对应
// 一个分组，动它属于越界（同 listPanelShortcuts 的选择）。
func collapsePanelDesktopShortcuts(desktop, panelID, keep string) []string {
	if keep == "" {
		return nil // 没有「要留的那一份」就什么都不删，避免误伤
	}
	var extra []string
	for _, path := range listPanelShortcutsIn([]string{desktop}, panelID, "") {
		if strings.EqualFold(path, keep) {
			continue
		}
		extra = append(extra, path)
	}
	return deleteShortcutFiles(extra)
}

// ─── 既有快捷方式的读取与清理（删除面板时联动） ────────────────────────────────

// shortcutSelfExeBase 返回「本程序的可执行文件名」，用于识别指向本程序的快捷方式。
// 包级变量便于测试注入（测试二进制名与 PanelDock.exe 不同）。
var shortcutSelfExeBase = defaultShortcutSelfExeBase

func defaultShortcutSelfExeBase() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Base(exe)
}

// shortcutDesktopDirectory 返回当前用户桌面目录。
// 包级变量便于测试注入：真实桌面无法在测试里安全改写。
var shortcutDesktopDirectory = shortcutDesktopDir

// shortcutArgsMatchPanel 判断快捷方式参数是否指向指定面板（`--open <面板ID>`）。
// 面板 ID 之后必须是结尾或空白，避免「ID 恰好是另一个 ID 前缀」时误匹配。
func shortcutArgsMatchPanel(args, panelID string) bool {
	if panelID == "" {
		return false
	}
	needle := "--open " + panelID
	for offset := 0; offset+len(needle) <= len(args); {
		idx := strings.Index(args[offset:], needle)
		if idx < 0 {
			return false
		}
		end := offset + idx + len(needle)
		if end == len(args) {
			return true
		}
		switch args[end] {
		case ' ', '\t', '\r', '\n':
			return true
		}
		offset += idx + 1
	}
	return false
}

// readShortcutTarget 读取已有 .lnk 的目标路径与命令行参数（自行初始化 COM）。
func readShortcutTarget(lnkPath string) (target, args string, err error) {
	err = shortcutWithCOM(func() error {
		var inner error
		target, args, inner = readShortcutTargetInCOM(lnkPath)
		return inner
	})
	return target, args, err
}

// readShortcutTargetInCOM 同 readShortcutTarget，但要求调用方已初始化 COM。
func readShortcutTargetInCOM(lnkPath string) (string, string, error) {
	var target, args string
	err := withLoadedShortcut(lnkPath, func(link *shortcutIShellLinkW) error {
		var inner error
		if target, inner = shortcutGetPath(link); inner != nil {
			return fmt.Errorf("IShellLinkW.GetPath: %w", inner)
		}
		if args, inner = shortcutGetArguments(link); inner != nil {
			return fmt.Errorf("IShellLinkW.GetArguments: %w", inner)
		}
		return nil
	})
	return target, args, err
}

// withLoadedShortcut 打开一个 .lnk 并交给 fn 读取或修改，负责 COM 对象的创建与释放。
// 要求调用方已初始化 COM。
//
// 抽出来是因为「读目标与参数」和「改图标」两处都要走同样一串
// CoCreateInstance → QueryInterface(IPersistFile) → Load。这段全是手工 vtable 调用，
// 抄第二遍就等于多一处可能写错槽位的地方，而槽位写错的表现是「一切正常但结果不对」。
func withLoadedShortcut(lnkPath string, fn func(link *shortcutIShellLinkW) error) error {
	var link *shortcutIShellLinkW
	hr, _, _ := shortcutCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&shortcutCLSIDShellLink)),
		0,
		uintptr(shortcutCLSCTXInprocServer),
		uintptr(unsafe.Pointer(&shortcutIIDIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if hr != 0 {
		return fmt.Errorf("CoCreateInstance(ShellLink): 0x%08x", hr)
	}
	defer link.Vtbl.Release.Call(uintptr(unsafe.Pointer(link)))

	var persist *shortcutIPersistFile
	hr, _, _ = link.Vtbl.QueryInterface.Call(
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&shortcutIIDIPersistFile)),
		uintptr(unsafe.Pointer(&persist)),
	)
	if hr != 0 {
		return fmt.Errorf("QueryInterface(IPersistFile): 0x%08x", hr)
	}
	defer persist.Vtbl.Release.Call(uintptr(unsafe.Pointer(persist)))

	lnkPtr, err := windows.UTF16PtrFromString(lnkPath)
	if err != nil {
		return err
	}
	// STGM_READ = 0：只读取，不写入。
	hr, _, _ = persist.Vtbl.Load.Call(
		uintptr(unsafe.Pointer(persist)),
		uintptr(unsafe.Pointer(lnkPtr)),
		0,
	)
	if hr != 0 {
		return fmt.Errorf("IPersistFile.Load: 0x%08x", hr)
	}
	return fn(link)
}

// shortcutGetPath 读取 .lnk 的目标路径（无路径时返回 S_FALSE，按空串处理）。
func shortcutGetPath(link *shortcutIShellLinkW) (string, error) {
	buf := make([]uint16, shortcutReadBufferSize)
	hr, _, _ := link.Vtbl.GetPath.Call(
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		0, // WIN32_FIND_DATAW *pfd
		0, // fFlags
	)
	if int32(hr) < 0 {
		return "", fmt.Errorf("0x%08x", uint32(hr))
	}
	return windows.UTF16ToString(buf), nil
}

// shortcutGetArguments 读取 .lnk 的命令行参数（无参数时返回 S_FALSE，按空串处理）。
func shortcutGetArguments(link *shortcutIShellLinkW) (string, error) {
	buf := make([]uint16, shortcutReadBufferSize)
	hr, _, _ := link.Vtbl.GetArguments.Call(
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if int32(hr) < 0 {
		return "", fmt.Errorf("0x%08x", uint32(hr))
	}
	return windows.UTF16ToString(buf), nil
}

// shortcutGetDescription 读取 .lnk 的备注文字。
// 改写图标时要把它一并带过去 —— 只改图标却把备注清空，是那种「没人叫你动」的破坏。
func shortcutGetDescription(link *shortcutIShellLinkW) (string, error) {
	buf := make([]uint16, shortcutReadBufferSize)
	hr, _, _ := link.Vtbl.GetDescription.Call(
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if int32(hr) < 0 {
		return "", fmt.Errorf("0x%08x", uint32(hr))
	}
	return windows.UTF16ToString(buf), nil
}

// shortcutGetIconLocation 读回 .lnk 当前指向的图标位置与图标索引。
// 索引在「图标来自 exe 且要挑第 n 个资源」时才有意义，我们只写单个 .ico 文件，
// 所以只用得上路径，但仍然把它读出来 —— 改写时要原样保留这个语义。
func shortcutGetIconLocation(link *shortcutIShellLinkW) (string, int, error) {
	buf := make([]uint16, shortcutReadBufferSize)
	var index int32
	hr, _, _ := link.Vtbl.GetIconLocation.Call(
		uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&index)),
	)
	if int32(hr) < 0 {
		return "", 0, fmt.Errorf("0x%08x", uint32(hr))
	}
	return windows.UTF16ToString(buf), int(index), nil
}

// ─── 图标改写（「刷新图标」按钮用） ─────────────────────────────────────────────

// readShortcutIconPath 读回既有快捷方式指向的图标文件路径（自行初始化 COM）。
func readShortcutIconPath(lnkPath string) (string, error) {
	var icon string
	err := shortcutWithCOM(func() error {
		return withLoadedShortcut(lnkPath, func(link *shortcutIShellLinkW) error {
			var inner error
			icon, _, inner = shortcutGetIconLocation(link)
			return inner
		})
	})
	return icon, err
}

// setShortcutIcon 把既有快捷方式的图标改到 iconPath，**目标、参数、备注原样保留**。
// iconPath 传空串表示回退到目标程序的内嵌图标（面板被删除、缓存要清掉时用）。
//
// 为什么是「读回来再整份重写」而不是只调 SetIconLocation 后 Save：
// IShellLinkW 只有整份 Save，没有「只落盘某个字段」的接口；而重新 CoCreateInstance 出来的
// 对象是空的，不先把原值读回来就 SetPath，等于把快捷方式改成一个没有目标的空壳。
func setShortcutIcon(lnkPath, iconPath string) error {
	if lnkPath == "" {
		return errCode(errShortcutPathEmpty)
	}
	return shortcutWithCOM(func() error {
		var target, args, desc string
		err := withLoadedShortcut(lnkPath, func(link *shortcutIShellLinkW) error {
			var inner error
			if target, inner = shortcutGetPath(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetPath: %w", inner)
			}
			if args, inner = shortcutGetArguments(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetArguments: %w", inner)
			}
			if desc, inner = shortcutGetDescription(link); inner != nil {
				return fmt.Errorf("IShellLinkW.GetDescription: %w", inner)
			}
			return nil
		})
		if err != nil {
			return err
		}
		return writeShortcutLnk(lnkPath, target, args, desc, iconPath)
	})
}

// listPanelShortcuts 找出属于指定面板的桌面快捷方式，返回去重后的现存 .lnk 路径。
// 两条来源：① 配置里记录的路径（可能被移动过）；② 扫描桌面目录，用 IShellLinkW
// 读回目标与参数，匹配「指向本程序 + `--open <面板ID>`」——后者能覆盖用户改名、
// 以及本功能上线之前手工创建的快捷方式。
// shortcutIconScanDirs 返回「可能存放本程序面板快捷方式的目录」。
//
// 两个地方：
//   - 桌面：用户点「桌面快捷方式」时我们写的那一份；
//   - 任务栏固定目录：用户把桌面那份固定到任务栏之后，Windows **复制**过去的那一份。
//
// 是两份独立文件，不是同一个文件的两处引用 —— 所以改图标必须两份都改。
// 只改桌面那份的话，任务栏按钮上的图标不会有任何变化（这正是用户报的那个现象）。
func shortcutIconScanDirs() []string {
	var out []string
	if desktop, err := shortcutDesktopDirectory(); err == nil && desktop != "" {
		out = append(out, desktop)
	}
	if pinned, err := shortcutTaskbarPinnedDir(); err == nil && pinned != "" {
		dup := false
		for _, d := range out {
			if strings.EqualFold(d, pinned) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, pinned)
		}
	}
	return out
}

// listPanelShortcutsForIcon 是「刷新图标」的扫描范围：桌面 + 任务栏固定目录。
//
// 刻意**不**把这个范围并进 listPanelShortcuts：那个函数是「删除面板」时的清理范围，
// 把固定目录塞进去会让「删面板」顺手删掉用户亲手固定到任务栏的项目。那是用户的东西。
func listPanelShortcutsForIcon(panelID, recorded string) []string {
	return listPanelShortcutsIn(shortcutIconScanDirs(), panelID, recorded)
}

func listPanelShortcuts(panelID, recorded string) []string {
	var dirs []string
	if desktop, err := shortcutDesktopDirectory(); err == nil && desktop != "" {
		dirs = append(dirs, desktop)
	}
	return listPanelShortcutsIn(dirs, panelID, recorded)
}

// listPanelShortcutsIn 在给定目录里找出属于指定面板的快捷方式，返回去重后的现存 .lnk 路径。
// 两条来源：① 配置里记录的**那一个**路径（可能被改名/移动过）；② 扫描目录，用 IShellLinkW
// 读回目标与参数，匹配「指向本程序 + `--open <面板ID>`」——后者能覆盖用户改名、
// 以及本功能上线之前手工创建的快捷方式。
//
// recorded 只有一个：一个分组桌面只留一份快捷方式（见 PanelConfig.Shortcut）。
func listPanelShortcutsIn(dirs []string, panelID, recorded string) []string {
	out := make([]string, 0, 1)
	seen := make(map[string]bool)

	add := func(path string) {
		if path == "" {
			return
		}
		key := strings.ToLower(path)
		if seen[key] {
			return
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			return
		}
		seen[key] = true
		out = append(out, path)
	}

	add(recorded)

	if panelID == "" {
		return out
	}
	exeBase := shortcutSelfExeBase()
	if exeBase == "" {
		return out
	}

	// 目录路径与 IShellLink 都要求 COM 已初始化。
	_ = shortcutWithCOM(func() error {
		for _, dir := range dirs {
			entries, err := os.ReadDir(dir)
			if err != nil {
				continue // 目录不存在（比如从没固定过任何东西）：跳过，不影响其余目录
			}
			for _, entry := range entries {
				if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".lnk") {
					continue
				}
				full := filepath.Join(dir, entry.Name())
				if seen[strings.ToLower(full)] {
					continue
				}
				target, args, err := readShortcutTargetInCOM(full)
				if err != nil {
					continue // 损坏或非快捷方式：跳过，不影响其余清理
				}
				if !strings.EqualFold(filepath.Base(target), exeBase) {
					continue
				}
				if !shortcutArgsMatchPanel(args, panelID) {
					continue
				}
				add(full)
			}
		}
		return nil
	})
	return out
}

// deleteShortcutFiles 删除给定路径中实际存在的 .lnk，返回删除成功的路径。
// 只处理 .lnk 后缀：即使配置被改坏，也不会误删普通文件。
func deleteShortcutFiles(paths []string) []string {
	deleted := make([]string, 0, len(paths))
	for _, path := range paths {
		if !strings.EqualFold(filepath.Ext(path), ".lnk") {
			continue
		}
		if err := os.Remove(path); err == nil {
			deleted = append(deleted, path)
		}
	}
	return deleted
}

// shortcutDesktopDir 返回当前用户桌面目录（跟随文件夹重定向，如 OneDrive / 非系统盘）。
// SHGetFolderPathW 失败时回退到 %USERPROFILE%\Desktop。
func shortcutDesktopDir() (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	hr, _, _ := shortcutSHGetFolderPathW.Call(
		0, uintptr(shortcutCSIDLDesktop), 0, 0,
		uintptr(unsafe.Pointer(&buf[0])),
	)
	if hr == 0 {
		if dir := windows.UTF16ToString(buf); dir != "" {
			return dir, nil
		}
	}
	if profile, ok := os.LookupEnv("USERPROFILE"); ok && profile != "" {
		return filepath.Join(profile, "Desktop"), nil
	}
	if hr != 0 {
		return "", fmt.Errorf("SHGetFolderPathW(Desktop): 0x%08x", hr)
	}
	return "", errors.New("SHGetFolderPathW(Desktop): empty path")
}

// sanitizeShortcutName 清理 Windows 文件名非法字符，并限制长度避免路径超限。
func sanitizeShortcutName(name string) string {
	repl := strings.NewReplacer(
		`\`, "", `/`, "", `:`, "", `*`, "", `?`, "", `"`, "",
		`<`, "", `>`, "", `|`, "",
	)
	out := strings.TrimSpace(repl.Replace(name))
	if out == "" {
		return "PanelDock"
	}
	// 桌面路径 + 文件名需要给 .lnk 后缀与序号留余量。
	if len(out) > 80 {
		out = out[:80]
	}
	return out
}

// uniqueShortcutPath 在 dir 内为 name 生成不冲突的 .lnk 路径，重名自动加序号。
func uniqueShortcutPath(dir, name string) string {
	base := sanitizeShortcutName(name)
	first := filepath.Join(dir, base+".lnk")
	if _, err := os.Stat(first); errors.Is(err, os.ErrNotExist) {
		return first
	}
	for i := 2; ; i++ {
		candidate := filepath.Join(dir, fmt.Sprintf("%s (%d).lnk", base, i))
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
}

// shortcutWithCOM 在 STA COM 环境中执行 fn。
//
// 必须 runtime.LockOSThread：COM 单元（apartment）是「每线程」状态，若 goroutine 在
// CoInitializeEx 与 CoUninitialize 之间迁移到别的线程，就会变成「初始化在 A 线程、
// 反初始化在 B 线程」——A 线程永久停在 STA，后续落到 A 线程的调用会拿到 S_FALSE，
// 表现为间歇性的 `CoInitializeEx: Incorrect function`（0x80070001，即 S_FALSE=1
// 被 x/sys 当成 error 返回）。
func shortcutWithCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	switch {
	case err == nil || errors.Is(err, shortcutSFalse):
		// nil = S_OK；S_FALSE = 本线程已按同一模式初始化过（x/sys 把任何非 0 HRESULT
		// 都当错误返回，S_FALSE(1) 于是变成 ERROR_INVALID_FUNCTION）。
		// 两种情况都算初始化成功，规范要求各配对一次 CoUninitialize。
		defer windows.CoUninitialize()
	case errors.Is(err, syscall.Errno(0x80010106)): // RPC_E_CHANGED_MODE
		// 已被其他并发模式初始化：ShellLink 进程内对象仍可创建，继续但不配对反初始化。
	default:
		return fmt.Errorf("CoInitializeEx: %w", err)
	}
	return fn()
}

// writeShortcutLnk 通过 IShellLinkW + IPersistFile 写出 .lnk 文件。
// 调用前 COM 必须已初始化。lnkPath 已保证不与现有文件冲突 ——
// 例外是 setShortcutIcon：它故意覆盖原文件，因为改的是同一个快捷方式的图标。
//
// iconPath 为快捷方式引用的图标文件；传空串表示用 targetPath 的内嵌图标
// （exe 自带的 build/appicon.ico）。
func writeShortcutLnk(lnkPath, targetPath, args, description, iconPath string) error {
	var link *shortcutIShellLinkW
	hr, _, _ := shortcutCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&shortcutCLSIDShellLink)),
		0,
		uintptr(shortcutCLSCTXInprocServer),
		uintptr(unsafe.Pointer(&shortcutIIDIShellLinkW)),
		uintptr(unsafe.Pointer(&link)),
	)
	if hr != 0 {
		return fmt.Errorf("CoCreateInstance(ShellLink): 0x%08x", hr)
	}
	defer link.Vtbl.Release.Call(uintptr(unsafe.Pointer(link)))

	utf16 := func(s string) (uintptr, error) {
		p, err := windows.UTF16PtrFromString(s)
		if err != nil {
			return 0, err
		}
		return uintptr(unsafe.Pointer(p)), nil
	}

	self := uintptr(unsafe.Pointer(link))

	pathPtr, err := utf16(targetPath)
	if err != nil {
		return err
	}
	if hr, _, _ := link.Vtbl.SetPath.Call(self, pathPtr); hr != 0 {
		return fmt.Errorf("IShellLinkW.SetPath: 0x%08x", hr)
	}

	argsPtr, err := utf16(args)
	if err != nil {
		return err
	}
	if hr, _, _ := link.Vtbl.SetArguments.Call(self, argsPtr); hr != 0 {
		return fmt.Errorf("IShellLinkW.SetArguments: 0x%08x", hr)
	}

	descPtr, err := utf16(description)
	if err != nil {
		return err
	}
	if hr, _, _ := link.Vtbl.SetDescription.Call(self, descPtr); hr != 0 {
		return fmt.Errorf("IShellLinkW.SetDescription: 0x%08x", hr)
	}

	// 图标：默认取 exe 内嵌图标（build/appicon.ico）；给了 iconPath 就用它 ——
	// 那是「刷新图标」缓存下来的站点图标。
	iconTarget := iconPath
	if iconTarget == "" {
		iconTarget = targetPath
	}
	iconPtr, err := utf16(iconTarget)
	if err != nil {
		return err
	}
	if hr, _, _ := link.Vtbl.SetIconLocation.Call(self, iconPtr, 0); hr != 0 {
		return fmt.Errorf("IShellLinkW.SetIconLocation: 0x%08x", hr)
	}

	// QueryInterface → IPersistFile，用其 Save 落盘。
	var persist *shortcutIPersistFile
	hr, _, _ = link.Vtbl.QueryInterface.Call(
		self,
		uintptr(unsafe.Pointer(&shortcutIIDIPersistFile)),
		uintptr(unsafe.Pointer(&persist)),
	)
	if hr != 0 {
		return fmt.Errorf("QueryInterface(IPersistFile): 0x%08x", hr)
	}
	defer persist.Vtbl.Release.Call(uintptr(unsafe.Pointer(persist)))

	lnkPtr, err := utf16(lnkPath)
	if err != nil {
		return err
	}
	hr, _, _ = persist.Vtbl.Save.Call(uintptr(unsafe.Pointer(persist)), lnkPtr, 1)
	if hr != 0 {
		return fmt.Errorf("IPersistFile.Save: 0x%08x", hr)
	}
	return nil
}
