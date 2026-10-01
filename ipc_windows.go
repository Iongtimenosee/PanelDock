//go:build windows

package main

// 单实例调度与进程间转发。
//
// 第一个实例创建命名互斥体，并启动一个不可见的 IPC 窗口（类名 PanelDock.IPC）。
// 后续实例启动时发现互斥体已存在，就用 WM_COPYDATA 把命令发给已运行实例，
// 然后自行退出，避免叠出多套「主窗口 + 面板窗口」。
//
// 命令格式：`open:<面板ID>` 打开/激活面板；`show` 呼出管理界面。
// 与 panel_window_windows.go 同一风格：手工 Win32 声明，无额外依赖。

import (
	"errors"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	win32WMCOPYDATA      = 0x004A
	win32SMTOAbortIfHung = 0x0002

	ipcWindowClass = "PanelDock.IPC"
	ipcOpenPrefix  = "open:"
	ipcCmdShow     = "show"
	// 等待已有实例 IPC 窗口就绪的上限（覆盖「上一个实例仍在启动中」的竞态）。
	ipcFindWindowRetries = 20
	ipcFindWindowDelay   = 50 * time.Millisecond
	ipcSendTimeoutMS     = 3000
)

// panelDockMutexName 为包级变量，便于测试注入独立名称，避免与真实运行实例互相干扰。
var panelDockMutexName = `Local\PanelDock.SingleInstance.Mutex`

// ipcWindowClassName 同样允许测试注入，避免测试误连到真实运行实例的 IPC 窗口。
var ipcWindowClassName = ipcWindowClass

var (
	ipcUser32              = syscall.NewLazyDLL("user32.dll")
	ipcFindWindowW         = ipcUser32.NewProc("FindWindowW")
	ipcSendMessageTimeoutW = ipcUser32.NewProc("SendMessageTimeoutW")

	ipcWindowProcedure = windows.NewCallback(ipcWindowProc)

	ipcHandlerMutex sync.RWMutex
	ipcHandler      func(cmd string)

	ipcMutexHandle windows.Handle
)

// panelCOPYDATASTRUCT 与 Windows COPYDATASTRUCT 内存布局一致（x64）。
// LpData 直接声明为指针：布局与 LPVOID 相同，但避免 uintptr → unsafe.Pointer 转换
// （go vet 的 unsafeptr 检查会拒绝后者）。该内存属于发送进程，只在回调内读取。
type panelCOPYDATASTRUCT struct {
	DwData uintptr
	CbData uint32
	LpData *uint16
}

// ipcCommandFor 根据启动参数生成转发命令。
func ipcCommandFor(autoOpenID string) string {
	if autoOpenID == "" {
		return ipcCmdShow
	}
	return ipcOpenPrefix + autoOpenID
}

// ipcPanelIDFromCommand 从 `open:<面板ID>` 命令中取出面板 ID；非 open 命令返回空串。
func ipcPanelIDFromCommand(cmd string) string {
	if !strings.HasPrefix(cmd, ipcOpenPrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(cmd, ipcOpenPrefix))
}

// acquireSingleInstance 尝试成为唯一实例；已有实例时返回 false。
// 互斥体句柄持有到进程结束，由操作系统在进程退出时自动释放（无需显式关闭）。
func acquireSingleInstance() bool {
	name, err := windows.UTF16PtrFromString(panelDockMutexName)
	if err != nil {
		return true
	}
	handle, err := windows.CreateMutex(nil, false, name)
	switch {
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS):
		return false
	case err != nil:
		// 创建失败（权限等）：不阻塞使用，退化为多实例模式。
		return true
	}
	ipcMutexHandle = handle
	return true
}

// forwardIPCCommand 把命令转发给已运行实例；成功送达返回 true。
// 需要重试是为了覆盖启动竞态：新实例可能在旧实例创建 IPC 窗口之前就启动了。
func forwardIPCCommand(cmd string) bool {
	className, err := windows.UTF16PtrFromString(ipcWindowClassName)
	if err != nil {
		return false
	}

	var hwnd uintptr
	for attempt := 0; attempt < ipcFindWindowRetries; attempt++ {
		hwnd, _, _ = ipcFindWindowW.Call(uintptr(unsafe.Pointer(className)), 0)
		if hwnd != 0 {
			break
		}
		time.Sleep(ipcFindWindowDelay)
	}
	if hwnd == 0 {
		return false
	}

	data := windows.StringToUTF16(cmd) // 含结尾 NUL
	cds := panelCOPYDATASTRUCT{
		CbData: uint32(len(data) * 2),
		LpData: &data[0],
	}
	var result uintptr
	r, _, _ := ipcSendMessageTimeoutW.Call(
		hwnd,
		win32WMCOPYDATA,
		0,
		uintptr(unsafe.Pointer(&cds)),
		win32SMTOAbortIfHung,
		ipcSendTimeoutMS,
		uintptr(unsafe.Pointer(&result)),
	)
	return r != 0
}

// ipcListenerReady 承载 IPC 窗口的创建结果。
type ipcListenerReady struct {
	hwnd uintptr
	err  error
}

// startIPCListener 创建不可见 IPC 窗口并进入消息循环，收到命令时回调 handler。
// 窗口为普通顶层窗口（不使用 HWND_MESSAGE），因为消息专用窗口无法被 FindWindow 找到。
// 返回的窗口句柄同时用于承载应用级托盘图标（见 tray_windows.go）。
func startIPCListener(handler func(cmd string)) (uintptr, error) {
	ipcHandlerMutex.Lock()
	ipcHandler = handler
	ipcHandlerMutex.Unlock()

	ready := make(chan ipcListenerReady, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		instance, err := panelModuleInstance()
		if err != nil {
			ready <- ipcListenerReady{err: err}
			return
		}

		className, err := windows.UTF16PtrFromString(ipcWindowClassName)
		if err != nil {
			ready <- ipcListenerReady{err: err}
			return
		}

		windowClass := panelWNDCLASSEX{
			Size:      uint32(unsafe.Sizeof(panelWNDCLASSEX{})),
			WndProc:   ipcWindowProcedure,
			Instance:  instance,
			ClassName: className,
			// Background 留空：窗口从不显示，无需背景刷。
		}
		atom, _, registerErr := panelRegisterClassEx.Call(uintptr(unsafe.Pointer(&windowClass)))
		if atom == 0 && registerErr != syscall.Errno(1410) { // 1410 = 类已注册
			ready <- ipcListenerReady{err: registerErr}
			return
		}

		// 无 WS_VISIBLE：窗口存在且可被 FindWindow 找到，但不可见、不占任务栏。
		hwnd, _, createErr := panelCreateWindowEx.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(className)),
			0,
			0, 0, 0, 0,
			0, 0, instance, 0,
		)
		if hwnd == 0 {
			ready <- ipcListenerReady{err: createErr}
			return
		}
		ready <- ipcListenerReady{hwnd: hwnd}

		var msg panelMSG
		for {
			r, _, _ := panelGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 { // 0 = WM_QUIT，-1 = 错误
				return
			}
			panelTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			panelDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()

	result := <-ready
	return result.hwnd, result.err
}

// ipcWindowProc IPC 窗口过程：处理 WM_COPYDATA 转发与托盘图标回调。
// lParam 声明为 unsafe.Pointer（而非 uintptr）：避免 uintptr → unsafe.Pointer 转换触发
// go vet 的 unsafeptr 检查，windows.NewCallback 支持该参数类型。
func ipcWindowProc(hwnd uintptr, message uint32, wParam uintptr, lParam unsafe.Pointer) uintptr {
	// Explorer 重启会广播 TaskbarCreated，托盘图标需要重新注册（否则图标消失）。
	if trayTaskbarCreatedMessage != 0 && message == trayTaskbarCreatedMessage {
		trayReAdd()
		return 0
	}

	switch message {
	case win32WMCOPYDATA:
		if lParam == nil {
			return 0
		}
		cds := (*panelCOPYDATASTRUCT)(lParam)
		if cds.LpData == nil || cds.CbData < 2 {
			return 0
		}
		// 对方进程的内存只在本回调内有效，必须先复制成 Go 字符串。
		raw := unsafe.Slice(cds.LpData, cds.CbData/2)
		cmd := windows.UTF16ToString(raw)

		ipcHandlerMutex.RLock()
		handler := ipcHandler
		ipcHandlerMutex.RUnlock()
		if handler != nil {
			// 不在消息线程里做实际工作：避免阻塞发送方的 SendMessageTimeout。
			go handler(cmd)
		}
		return 1 // TRUE = 已处理
	case trayCallbackMessage:
		trayHandleEvent(wParam, uintptr(lParam))
		return 0
	case win32WMDESTROY:
		panelPostQuitMessage.Call(0)
		return 0
	default:
		result, _, _ := panelDefWindowProc.Call(hwnd, uintptr(message), wParam, uintptr(lParam))
		return result
	}
}
