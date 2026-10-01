//go:build windows

package main

import (
	"testing"
	"time"
)

func TestIPCPanelIDFromCommand(t *testing.T) {
	cases := []struct {
		cmd, want string
	}{
		{"open:349f8184-9fe5-4cb1-8e62-fd2e03ad1782", "349f8184-9fe5-4cb1-8e62-fd2e03ad1782"},
		{"open:  padded  ", "padded"},
		{"open:", ""},
		{"show", ""},
		{"", ""},
		{"somethingelse", ""},
	}
	for _, c := range cases {
		if got := ipcPanelIDFromCommand(c.cmd); got != c.want {
			t.Errorf("ipcPanelIDFromCommand(%q) = %q, want %q", c.cmd, got, c.want)
		}
	}
}

func TestIPCCommandFor(t *testing.T) {
	if got := ipcCommandFor(""); got != ipcCmdShow {
		t.Errorf("无参数启动应生成 show，得到 %q", got)
	}
	if got := ipcCommandFor("abc-123"); got != "open:abc-123" {
		t.Errorf("带面板 ID 应生成 open: 前缀，得到 %q", got)
	}
}

// TestSingleInstanceDetection 验证命名互斥体的单实例判定（同名第二次应判为已有实例）。
func TestSingleInstanceDetection(t *testing.T) {
	orig := panelDockMutexName
	defer func() { panelDockMutexName = orig }()
	panelDockMutexName = `Local\PanelDock.Test.SingleInstance.Mutex`

	if !acquireSingleInstance() {
		t.Fatal("首次获取应为唯一实例")
	}
	if acquireSingleInstance() {
		t.Fatal("同名互斥体已存在时应判定为已有实例")
	}
}

// TestIPCListenerDeliversCommand 端到端验证：IPC 窗口收到 WM_COPYDATA 后把命令交给回调。
func TestIPCListenerDeliversCommand(t *testing.T) {
	origClass := ipcWindowClassName
	origHandler := ipcHandler
	defer func() {
		ipcWindowClassName = origClass
		ipcHandlerMutex.Lock()
		ipcHandler = origHandler
		ipcHandlerMutex.Unlock()
	}()
	ipcWindowClassName = "PanelDock.IPC.Test"

	received := make(chan string, 4)
	if _, err := startIPCListener(func(cmd string) { received <- cmd }); err != nil {
		t.Fatalf("startIPCListener: %v", err)
	}

	want := "open:端到端-测试面板"
	if !forwardIPCCommand(want) {
		t.Fatal("forwardIPCCommand 应成功送达")
	}

	select {
	case got := <-received:
		if got != want {
			t.Fatalf("回调收到 %q，want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("超时：IPC 回调未被触发")
	}
}

// TestForwardIPCCommandWithoutListener 无监听窗口时应快速失败（而不是永久阻塞）。
func TestForwardIPCCommandWithoutListener(t *testing.T) {
	origClass := ipcWindowClassName
	defer func() { ipcWindowClassName = origClass }()
	ipcWindowClassName = "PanelDock.IPC.NotRunning"

	start := time.Now()
	if forwardIPCCommand(ipcCmdShow) {
		t.Fatal("不存在的 IPC 窗口不应返回成功")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("重试等待过久: %v", elapsed)
	}
}

// TestAppLightweightFlag 验证轻量模式仅在带 --open 启动时启用。
func TestAppLightweightFlag(t *testing.T) {
	if app := NewApp(""); app.lightweight {
		t.Error("无 --open 参数时不应进入轻量模式")
	}
	if app := NewApp("some-id"); !app.lightweight {
		t.Error("带 --open 参数时应进入轻量模式")
	}
}
