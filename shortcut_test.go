//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeShortcutName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"openwrt", "openwrt"},
		{`a\b/c:d*e?f"g<h>i|j`, "abcdefghij"},
		{"  spaced  ", "spaced"},
		{"", "PanelDock"},
		{"   ", "PanelDock"},
	}
	for _, c := range cases {
		if got := sanitizeShortcutName(c.in); got != c.want {
			t.Errorf("sanitizeShortcutName(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	long := strings.Repeat("长", 100)
	if got := sanitizeShortcutName(long); len(got) != 80 {
		t.Errorf("sanitizeShortcutName 超长未截断: %d 字节", len(got))
	}
}

func TestUniqueShortcutPath(t *testing.T) {
	dir := t.TempDir()

	first := uniqueShortcutPath(dir, "面板A")
	if want := filepath.Join(dir, "面板A.lnk"); first != want {
		t.Errorf("首个路径 = %q, want %q", first, want)
	}

	// 模拟同名快捷方式已存在，应得到序号 2。
	if err := os.WriteFile(first, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := uniqueShortcutPath(dir, "面板A")
	if want := filepath.Join(dir, "面板A (2).lnk"); second != want {
		t.Errorf("重名路径 = %q, want %q", second, want)
	}

	// 占掉 2 之后，下一个应是 3。
	if err := os.WriteFile(second, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := uniqueShortcutPath(dir, "面板A")
	if want := filepath.Join(dir, "面板A (3).lnk"); third != want {
		t.Errorf("三次重名路径 = %q, want %q", third, want)
	}
}

// TestWriteShortcutLnk 走真实 COM：IShellLinkW + IPersistFile 写 .lnk 到临时目录。
func TestWriteShortcutLnk(t *testing.T) {
	dir := t.TempDir()
	lnkPath := filepath.Join(dir, "test.lnk")

	err := shortcutWithCOM(func() error {
		return writeShortcutLnk(lnkPath, `C:\PanelDock\PanelDock.exe`, "--open abc-123", "PanelDock · 测试", "")
	})
	if err != nil {
		t.Fatalf("writeShortcutLnk: %v", err)
	}

	data, err := os.ReadFile(lnkPath)
	if err != nil {
		t.Fatalf("读取 .lnk: %v", err)
	}
	// Shell link 文件头：4C 00 00 00（"L\0\0\0"）+ LinkCLSID。
	if len(data) < 21 {
		t.Fatalf(".lnk 过小: %d 字节", len(data))
	}
	if data[0] != 0x4C || data[1] != 0x00 || data[2] != 0x00 || data[3] != 0x00 {
		t.Errorf(".lnk 文件头错误: % x", data[:4])
	}
	// LinkCLSID {00021401-0000-0000-C000-000000000046}。
	wantCLSID := []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
	if string(data[4:20]) != string(wantCLSID) {
		t.Errorf(".lnk CLSID 不符: % x", data[4:20])
	}
}

// TestShortcutArgsMatchPanel 验证参数匹配不会因「面板 ID 互为前缀」而误判。
func TestShortcutArgsMatchPanel(t *testing.T) {
	const id = "349f8184-9fe5-4cb1-8e62-fd2e03ad1782"

	cases := []struct {
		name string
		args string
		want bool
	}{
		{"仅参数", "--open " + id, true},
		{"后接其他参数", "--open " + id + " --incognito", true},
		{"前有其他参数", "--verbose --open " + id, true},
		{"ID 只匹配前缀", "--open " + id + "-extra", false},
		{"ID 被截短", "--open " + id[:len(id)-1], false},
		{"缺少空格", "--open" + id, false},
		{"其他面板", "--open 00000000-0000-0000-0000-000000000000", false},
		{"空参数", "", false},
		{"只有开关", "--open ", false},
	}
	for _, c := range cases {
		if got := shortcutArgsMatchPanel(c.args, id); got != c.want {
			t.Errorf("%s: shortcutArgsMatchPanel(%q) = %v, want %v", c.name, c.args, got, c.want)
		}
	}

	if shortcutArgsMatchPanel("--open anything", "") {
		t.Error("空面板 ID 不应匹配任何参数")
	}
}

// TestDeleteShortcutFiles 验证只删除 .lnk，且忽略已不存在的路径。
func TestDeleteShortcutFiles(t *testing.T) {
	dir := t.TempDir()
	lnk := filepath.Join(dir, "面板.lnk")
	keep := filepath.Join(dir, "不要动我.txt")
	for _, path := range []string{lnk, keep} {
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	deleted := deleteShortcutFiles([]string{
		"",
		lnk,
		keep, // 非 .lnk：必须保留
		filepath.Join(dir, "不存在.lnk"),
	})
	if len(deleted) != 1 || deleted[0] != lnk {
		t.Fatalf("应只删除 .lnk，实际删除: %v", deleted)
	}
	if _, err := os.Stat(lnk); !os.IsNotExist(err) {
		t.Error("目标 .lnk 应已被删除")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("非 .lnk 文件不应被删除: %v", err)
	}
}

// TestListPanelShortcutsRecordedPaths 验证配置里记录的**那一个**路径会被返回，不存在时跳过。
//
// 记录只有一个（一个分组桌面只留一份快捷方式），所以这里不再有「去重」的语义 ——
// 但仍然要顶住「配置里写着、文件已被用户删掉」这种情况：不能返回一个不存在的路径，
// 上游拿它去删/改都会失败。
func TestListPanelShortcutsRecordedPaths(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(stubShortcutSeams(t, dir))
	lnk := filepath.Join(dir, "记录过的.lnk")
	if err := os.WriteFile(lnk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := listPanelShortcuts("panel-id", lnk); len(got) != 1 || got[0] != lnk {
		t.Fatalf("应返回配置记录的现存路径，实际: %v", got)
	}
	if got := listPanelShortcuts("panel-id", filepath.Join(dir, "不存在.lnk")); len(got) != 0 {
		t.Fatalf("记录里的路径不存在时不应返回，实际: %v", got)
	}
}

// TestListPanelShortcutsScansDesktop 走完整的「扫描目录 + 读回 .lnk + 匹配面板 + 删除」链路。
// 桌面目录与「本程序文件名」都注入到测试环境，不碰真实桌面。
func TestListPanelShortcutsScansDesktop(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(stubShortcutSeams(t, dir))

	const targetID = "11111111-2222-3333-4444-555555555555"
	const otherID = "99999999-8888-7777-6666-555555555555"

	write := func(name, target, args string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := shortcutWithCOM(func() error {
			return writeShortcutLnk(path, target, args, "PanelDock · 测试", "")
		}); err != nil {
			t.Fatalf("写入 %s: %v", name, err)
		}
		return path
	}

	// 目标面板：名字故意与面板名不同，模拟用户重命名过的快捷方式。
	mine := write("改过名的.lnk", `C:\Tools\`+shortcutTestExeName, "--open "+targetID)
	// 别的面板：不应被匹配。
	other := write("别的面板.lnk", `C:\Tools\`+shortcutTestExeName, "--open "+otherID)
	// 别的程序：即使参数相同也不应被匹配。
	foreign := write("别的程序.lnk", `C:\Tools\Other.exe`, "--open "+targetID)

	got := listPanelShortcuts(targetID, "")
	if len(got) != 1 || got[0] != mine {
		t.Fatalf("应只匹配目标面板的 .lnk，实际: %v", got)
	}

	deleted := deleteShortcutFiles(got)
	if len(deleted) != 1 || deleted[0] != mine {
		t.Fatalf("应删除目标 .lnk，实际: %v", deleted)
	}
	for _, keep := range []string{other, foreign} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("不应删除 %s: %v", keep, err)
		}
	}
}

// TestListPanelShortcutsRealDesktop 在**真实桌面**上确认扫描能找出本程序创建的 .lnk。
// 默认跳过；设置 PANELDOCK_DESKTOP_TEST=1 启用（只读扫描，不做任何删除）。
func TestListPanelShortcutsRealDesktop(t *testing.T) {
	if os.Getenv("PANELDOCK_DESKTOP_TEST") == "" {
		t.Skip("设置 PANELDOCK_DESKTOP_TEST=1 以在真实桌面验证快捷方式扫描")
	}

	originalBase := shortcutSelfExeBase
	shortcutSelfExeBase = func() string { return shortcutTestExeName }
	defer func() { shortcutSelfExeBase = originalBase }()

	panelID := os.Getenv("PANELDOCK_DESKTOP_PANEL_ID")
	if panelID == "" {
		t.Skip("设置 PANELDOCK_DESKTOP_PANEL_ID=<面板ID> 指定要扫描的面板")
	}

	for _, path := range listPanelShortcuts(panelID, "") {
		t.Logf("匹配到: %s", path)
	}
}

// TestCreatePanelShortcutOverwritesInsteadOfStacking 锁定「一个分组桌面只能有一个快捷方式」。
//
// 旧版每次点「桌面快捷方式」都调 uniqueShortcutPath 另起一个名字，于是桌面被堆成
// 「名字.lnk / 名字 (2).lnk / 名字 (3).lnk」，配置里也攒一串路径。
// 现在已有的那一份直接被覆盖（连文件名一起留着），不再新增。
func TestCreatePanelShortcutOverwritesInsteadOfStacking(t *testing.T) {
	desktop := t.TempDir()
	restore := stubShortcutSeams(t, desktop)
	defer restore()

	app := newTestApp(t)
	panel := createTestPanel(t, app, "唯一快捷方式")

	first, err := app.CreatePanelShortcut(panel.ID)
	if err != nil {
		t.Fatalf("CreatePanelShortcut: %v", err)
	}
	if !first.Created {
		t.Error("桌面本来是空的，第一次应当算「新建」")
	}
	if n := countLnk(t, desktop); n != 1 {
		t.Fatalf("桌面应有 1 个 .lnk，实际 %d", n)
	}
	if cfg, _ := app.config.get(panel.ID); cfg.Shortcut != first.Path {
		t.Errorf("配置里应记下新建的路径，实际 %q", cfg.Shortcut)
	}

	second, err := app.CreatePanelShortcut(panel.ID)
	if err != nil {
		t.Fatalf("CreatePanelShortcut: %v", err)
	}
	if second.Created {
		t.Error("桌面已经有这个分组的一份了，第二次应当是「覆盖」而不是新建")
	}
	if !strings.EqualFold(second.Path, first.Path) {
		t.Errorf("覆盖后路径变了：%q -> %q", first.Path, second.Path)
	}
	if n := countLnk(t, desktop); n != 1 {
		t.Errorf("桌面不该多出副本，实际 %d 个 .lnk", n)
	}
	if cfg, _ := app.config.get(panel.ID); cfg.Shortcut != first.Path {
		t.Errorf("配置里仍然只该有一个路径，实际 %q", cfg.Shortcut)
	}
}

// TestCollapsePanelDesktopShortcuts 把历史上堆在桌面的多份收敛成一份。
//
// 只动**本程序为该分组创建的**那些（扫描条件：目标是本程序 + 参数含 `--open <面板ID>`），
// 别的分组的 .lnk 必须毫发无伤；keep 为空时一个都不删 —— 那是「拿不准就别动手」的兜底。
func TestCollapsePanelDesktopShortcuts(t *testing.T) {
	desktop := t.TempDir()
	restore := stubShortcutSeams(t, desktop)
	defer restore()

	const mine = "11111111-2222-3333-4444-555555555555"
	const other = "99999999-8888-7777-6666-555555555555"

	keep := writePanelShortcut(t, desktop, "要留的那份.lnk", mine)
	extra1 := writePanelShortcut(t, desktop, "旧的 (2).lnk", mine)
	extra2 := writePanelShortcut(t, desktop, "旧的 (3).lnk", mine)
	foreign := writePanelShortcut(t, desktop, "别的分组.lnk", other)

	removed := collapsePanelDesktopShortcuts(desktop, mine, keep)
	if len(removed) != 2 {
		t.Fatalf("应清掉 2 个多余项，实际 %v", removed)
	}
	for _, path := range []string{keep, foreign} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("不该删 %s: %v", path, err)
		}
	}
	for _, path := range []string{extra1, extra2} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("多余的 %s 应已被清理", path)
		}
	}

	// keep 为空 = 没有「要留的那一份」，此时一个都不能删。
	if got := collapsePanelDesktopShortcuts(desktop, mine, ""); len(got) != 0 {
		t.Errorf("keep 为空时不该删任何东西，实际 %v", got)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("keep 为空时把保留项也删了: %v", err)
	}
}

// TestCreateDesktopShortcutIntegration 在真实桌面创建快捷方式的集成验证。
// 默认跳过；设置 PANELDOCK_DESKTOP_TEST=1 启用，验证后自动清理。
func TestCreateDesktopShortcutIntegration(t *testing.T) {
	if os.Getenv("PANELDOCK_DESKTOP_TEST") == "" {
		t.Skip("设置 PANELDOCK_DESKTOP_TEST=1 以在真实桌面验证快捷方式创建")
	}
	exePath, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path, _, err := createDesktopShortcut(exePath, "pdock-integration-test", "PDock 集成测试/验证", "", "")
	if err != nil {
		t.Fatalf("createDesktopShortcut: %v", err)
	}
	defer os.Remove(path)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("桌面快捷方式未生成: %v", err)
	}
	t.Logf("已创建并清理: %s", path)
}
