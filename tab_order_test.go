package main

import "testing"

// seedTabs 造一个含三个标签的面板，返回面板与三个标签（顺序 A、B、C）。
func seedTabs(t *testing.T) (*configStore, PanelConfig, [3]PanelTab) {
	t.Helper()

	store := newTestStore(t)
	panel, err := store.create("分组面板", "http://a.lan", true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	tabs := [3]PanelTab{
		panel.Tabs[0], // A
		{ID: newTabID(), Name: "标签B", URL: "http://b.lan"},
		{ID: newTabID(), Name: "标签C", URL: "http://c.lan"},
	}
	if err := store.updateTabs(panel.ID, tabs[:]); err != nil {
		t.Fatalf("updateTabs: %v", err)
	}
	panel, _ = store.get(panel.ID)
	return store, panel, tabs
}

// TestReorderTabsKeepsDefaultTabByID 验证重排标签后「记住的默认标签」跟着标签本身走，而不是继续沿用旧下标。
//
// 下标只在旧顺序里有意义：把 [A,B,C] 排成 [C,A,B] 之后，旧下标 2 指向的已经是 B。
// 若按旧下标取值，用户排好序打开面板，激活的却是别人 —— 这正是「以排序打开」要避免的事。
func TestReorderTabsKeepsDefaultTabByID(t *testing.T) {
	store, panel, tabs := seedTabs(t)

	// 用户上次停留在 C（下标 2）。
	if err := store.setDefaultTabIndex(panel.ID, 2); err != nil {
		t.Fatalf("setDefaultTabIndex: %v", err)
	}

	// 前端把顺序改成 C、A、B 后整体提交。
	if err := store.updateTabs(panel.ID, []PanelTab{tabs[2], tabs[0], tabs[1]}); err != nil {
		t.Fatalf("updateTabs(reorder): %v", err)
	}

	got, ok := store.get(panel.ID)
	if !ok {
		t.Fatal("面板丢失")
	}
	if got.Tabs[0].ID != tabs[2].ID {
		t.Fatalf("排序未生效：第一个标签应为 C，实际 %s", got.Tabs[0].Name)
	}
	if got.DefaultTabIndex != 0 {
		t.Fatalf("默认标签应跟随 C 落到下标 0，实际 %d", got.DefaultTabIndex)
	}
	if got.Tabs[got.DefaultTabIndex].ID != tabs[2].ID {
		t.Fatalf("默认标签被换成了别的标签：%s", got.Tabs[got.DefaultTabIndex].Name)
	}
}

// TestReorderTabsKeepsTabIdentity 验证重排只换位置，绝不替换标签对象里的 id。
//
// id 决定 WebView2 profile 目录（WebViewProfiles\<tabID>）。重排若把 id 弄丢或重发一遍，
// 等价于换了浏览器身份：登录会话、Cookie 全部作废。名称与地址必须与原标签严格同进同出。
func TestReorderTabsKeepsTabIdentity(t *testing.T) {
	store, panel, tabs := seedTabs(t)

	// 倒序排。
	if err := store.updateTabs(panel.ID, []PanelTab{tabs[2], tabs[1], tabs[0]}); err != nil {
		t.Fatalf("updateTabs(reverse): %v", err)
	}

	got, _ := store.get(panel.ID)
	want := []PanelTab{tabs[2], tabs[1], tabs[0]}
	for i := range want {
		if got.Tabs[i].ID != want[i].ID {
			t.Errorf("第 %d 个标签 id 变了：%s → %s", i+1, want[i].ID, got.Tabs[i].ID)
		}
		if got.Tabs[i].Name != want[i].Name || got.Tabs[i].URL != want[i].URL {
			t.Errorf("第 %d 个标签的名称/地址没有跟着走：%+v", i+1, got.Tabs[i])
		}
	}
}

// TestDeleteDefaultTabFallsBackToFirst 验证默认标签本身被删掉时回落到第一个标签，而不是留下越界下标。
func TestDeleteDefaultTabFallsBackToFirst(t *testing.T) {
	store, panel, tabs := seedTabs(t)

	if err := store.setDefaultTabIndex(panel.ID, 2); err != nil {
		t.Fatalf("setDefaultTabIndex: %v", err)
	}
	// 删掉 C（即默认标签），只剩 A、B。
	if err := store.updateTabs(panel.ID, []PanelTab{tabs[0], tabs[1]}); err != nil {
		t.Fatalf("updateTabs(drop default): %v", err)
	}

	got, _ := store.get(panel.ID)
	if got.DefaultTabIndex != 0 {
		t.Fatalf("默认标签被删后应回落 0，实际 %d", got.DefaultTabIndex)
	}
	if got.Tabs[got.DefaultTabIndex].ID != tabs[0].ID {
		t.Fatalf("回落目标应为第一个标签 A，实际 %s", got.Tabs[got.DefaultTabIndex].Name)
	}

	// 删掉排在默认标签之前的那个，默认标签跟着前移。
	if err := store.setDefaultTabIndex(panel.ID, 1); err != nil {
		t.Fatalf("setDefaultTabIndex: %v", err)
	}
	if err := store.updateTabs(panel.ID, []PanelTab{tabs[1]}); err != nil {
		t.Fatalf("updateTabs(drop before default): %v", err)
	}
	got, _ = store.get(panel.ID)
	if got.DefaultTabIndex != 0 || got.Tabs[0].ID != tabs[1].ID {
		t.Fatalf("删除默认标签之前的项后应仍停留在同一个标签，实际 index=%d id=%s", got.DefaultTabIndex, got.Tabs[0].ID)
	}
}

// TestUpdatePanelTabsReorderThroughApp 走前端真正调用的那条桥接方法，
// 模拟「编辑面板 → 调整标签顺序 → 保存」：载荷里的 id 原样带回来，顺序按载荷落盘。
func TestUpdatePanelTabsReorderThroughApp(t *testing.T) {
	store, panel, tabs := seedTabs(t)
	app := &App{config: store}

	// 与前端 submitForm 构造的载荷一致：{id, name, url}，顺序即拖拽后的顺序。
	payload := []PanelTab{
		{ID: tabs[1].ID, Name: tabs[1].Name, URL: tabs[1].URL},
		{ID: tabs[2].ID, Name: tabs[2].Name, URL: tabs[2].URL},
		{ID: tabs[0].ID, Name: tabs[0].Name, URL: tabs[0].URL},
	}
	if err := app.UpdatePanelTabs(panel.ID, payload); err != nil {
		t.Fatalf("UpdatePanelTabs: %v", err)
	}

	got, _ := store.get(panel.ID)
	for i := range payload {
		if got.Tabs[i].ID != payload[i].ID {
			t.Fatalf("第 %d 个标签顺序不对：期望 %s，实际 %s", i+1, payload[i].Name, got.Tabs[i].Name)
		}
	}

	// 标签对象是复制进配置的：调用方之后再改自己的切片，不应污染已保存的配置。
	payload[0].Name = "被就地篡改"
	got, _ = store.get(panel.ID)
	if got.Tabs[0].Name == "被就地篡改" {
		t.Fatal("配置与调用方共享了底层数组，外部改动会污染已保存的标签列表")
	}
}
