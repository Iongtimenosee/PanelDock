package main

import (
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestApplyAppUserModelIDSetsExplicitID 守的是「静默失效」：
//
// SetCurrentProcessExplicitAppUserModelID 这个名字一旦写错（DLL 名、导出名拼错），
// applyAppUserModelID 里 Find() 失败会直接 return —— 编译、vet、其它测试全绿，
// 但任务栏图标会**悄悄退回**被快捷方式劫持的老行为（显示 exe 内嵌的默认 W 图标），
// 而这条退化只有在真实任务栏上才看得见。所以这里真的设一次、再真的读回来。
//
// 读回来用的是 GetCurrentProcessExplicitAppUserModelID，它是同一个 DLL 里配对的查询函数，
// 拿到的必须是刚设进去的那个字符串。
func TestApplyAppUserModelIDSetsExplicitID(t *testing.T) {
	applyAppUserModelID()

	shell32 := syscall.NewLazyDLL("shell32.dll")
	proc := shell32.NewProc("GetCurrentProcessExplicitAppUserModelID")
	if err := proc.Find(); err != nil {
		t.Fatalf("找不到 GetCurrentProcessExplicitAppUserModelID: %v", err)
	}

	var out *uint16
	if hr, _, _ := proc.Call(uintptr(unsafe.Pointer(&out))); hr != 0 {
		t.Fatalf("GetCurrentProcessExplicitAppUserModelID: 0x%08x", hr)
	}
	if out == nil {
		t.Fatal("AUMID 为空：applyAppUserModelID 没生效（任务栏图标会被快捷方式劫持）")
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(out))

	if got := windows.UTF16PtrToString(out); got != panelAppUserModelID {
		t.Fatalf("AUMID = %q，期望 %q", got, panelAppUserModelID)
	}
}
