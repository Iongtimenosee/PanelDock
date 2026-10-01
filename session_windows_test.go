//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewPanelWindowAppliesSessionMode 验证面板窗口对象在构造时就按会话设置准备好了 profile：
// 「关闭后清空」的面板每次打开前先抹掉残留（打开前兜底），「保留」的面板原样沿用。
//
// 这里只构造窗口对象、不创建真实窗口（newPanelWindow 全是纯 Go + 文件操作，不碰 Win32），
// 因此可以在测试进程里直接断言。真实施加在窗口上的关闭清理见 E2E
// （TestE2EFreshSessionClearedOnClose）。
func TestNewPanelWindowAppliesSessionMode(t *testing.T) {
	useTempPortableRoot(t)

	freshCfg := PanelConfig{
		ID:          "panel-fresh",
		Name:        "临时面板",
		SessionMode: SessionModeFresh,
		Tabs:        []PanelTab{{ID: "tab-fresh", Name: "标签", URL: "http://a.lan"}},
	}
	persistCfg := PanelConfig{
		ID:   "panel-persist",
		Name: "常驻面板",
		Tabs: []PanelTab{{ID: "tab-persist", Name: "标签", URL: "http://b.lan"}},
	}

	// 「关闭后清空」：打开前必须把上次残留（崩溃/强杀留下的）抹干净。
	dirty := seedProfile(t, "tab-fresh")
	if _, err := newPanelWindow(&App{}, freshCfg); err != nil {
		t.Fatalf("newPanelWindow(fresh): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirty, "EBWebView", "Default", "Cookies")); !os.IsNotExist(err) {
		t.Errorf("「关闭后清空」的面板应在打开前抹掉残留会话数据 (err=%v)", err)
	}

	// 「保留」（老配置缺字段时的默认）：一个字节都不该动。
	kept := seedProfile(t, "tab-persist")
	if _, err := newPanelWindow(&App{}, persistCfg); err != nil {
		t.Fatalf("newPanelWindow(persist): %v", err)
	}
	if _, err := os.Stat(filepath.Join(kept, "EBWebView", "Default", "Cookies")); err != nil {
		t.Errorf("「保留」的面板不应清空会话数据: %v", err)
	}

	// 窗口对象要带着构造时的快照，关闭路径据此决定是否清空。
	// 存快照而不是关闭时回查配置：否则「打开时是保留、关闭前被改成清空」会让
	// 同一次关闭的后果不可预测。
	freshWindow, err := newPanelWindow(&App{}, freshCfg)
	if err != nil {
		t.Fatalf("newPanelWindow(fresh): %v", err)
	}
	if !freshWindow.clearOnClose {
		t.Error("clearOnClose 未按配置置位")
	}

	persistWindow, err := newPanelWindow(&App{}, persistCfg)
	if err != nil {
		t.Fatalf("newPanelWindow(persist): %v", err)
	}
	if persistWindow.clearOnClose {
		t.Error("「保留」的面板不应带清空标记")
	}
}

// TestNewPanelWindowAppliesPasswordAutosave 验证窗口对象在构造时就把分组的「保存登录密码」
// 开关快照下来：这个值要在每个标签创建 WebView2 时送进去（见 controllerCompleted）。
//
// 它守的是「配置字段忘了接到窗口上」这类静默失效：单看配置层一切正常（能存能读），
// 但窗口拿不到值，所有分组都会按零值（不保存）跑，而编译、vet 全都不报错。
func TestNewPanelWindowAppliesPasswordAutosave(t *testing.T) {
	useTempPortableRoot(t)

	cases := []struct {
		name string
		mode string
		want bool
	}{
		{"默认（老配置缺字段）", "", true},
		{"显式开启", PasswordAutosaveOn, true},
		{"显式关闭", PasswordAutosaveOff, false},
		{"非法值按默认", "maybe", true},
	}

	for _, c := range cases {
		tabID := "tab-password-" + c.mode + c.name
		cfg := PanelConfig{
			ID:               "panel-" + c.name,
			Name:             c.name,
			PasswordAutosave: c.mode,
			Tabs:             []PanelTab{{ID: tabID, Name: "标签", URL: "https://a.lan"}},
		}

		window, err := newPanelWindow(&App{}, cfg)
		if err != nil {
			t.Fatalf("newPanelWindow(%s): %v", c.name, err)
		}
		if got := window.passwordAutosaveEnabled(); got != c.want {
			t.Errorf("%s: passwordAutosave 应为 %v，实际 %v", c.name, c.want, got)
		}

		// 改动开关（App.SetPanelPasswordAutosave 走的就是这个方法）要能被读回来 ——
		// 已打开的窗口靠它把新值重设到所有标签上。
		window.setPasswordAutosave(!c.want)
		if got := window.passwordAutosaveEnabled(); got != !c.want {
			t.Errorf("%s: 改开关后应为 %v，实际 %v", c.name, !c.want, got)
		}
	}
}
