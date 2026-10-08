package main

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// 面板的「会话状态」清理：把标签的 WebView2 profile 目录整个删掉。
//
// 为什么是**整个目录**而不是挑几个文件删：profile 目录（WebViewProfiles\<tabID>）就是
// 这个标签的浏览器身份本身 —— Cookie、Local Storage、IndexedDB、Service Worker、缓存、
// 权限授权全在里面，且目录结构随 WebView2 版本变。逐类清理既会漏（漏一样就等于登录态还在），
// 又要跟着上游改。
//
// 四个调用场景见 docs/behavior.md#会话状态，坑见 docs/pitfalls.md。
// 清空后 newPanelWindow 会重新 MkdirAll，标签 ID 不变，配置 / 快捷方式 / 窗口状态都不用动。

// sessionClearPolicy 决定删除 profile 目录时要不要「确认它不再出现」。
type sessionClearPolicy int

const (
	// clearOnce 删掉即可，不等待。
	//
	// 用于**打开面板前**的兜底清空：那一刻该 profile 还没有任何浏览器进程
	// （面板尚未打开），不存在「删了又被重建」的问题，没必要白等一个静默期。
	clearOnce sessionClearPolicy = iota

	// clearUntilStable 删到目录连续若干次检查都不存在为止。
	//
	// 用于**关闭面板后**的清空：此时浏览器进程还在退出，会把目录重建回来
	// （实测：`RemoveAll` 成功、目录随后带着一整棵 EBWebView 树又冒了出来，
	// 端到端用例当场逮到），只删一次就等于没清干净 —— 而「不留残留」正是这个功能的
	// 全部价值所在。浏览器一停，目录就不会再冒出来。
	clearUntilStable
)

// 删除 WebView2 profile 目录的重试预算。
// 正常情况「删一次 + 静默期确认」就够了，失败路径才会吃满整个预算。
var (
	profileClearAttempts = 16
	profileClearDelay    = 250 * time.Millisecond
	// profileClearQuietChecks 是 clearUntilStable 要求的连续「确认不存在」次数。
	// 3 次 + 250ms 间隔 = 500ms 静默期，用来覆盖浏览器进程退出期间的重建动作。
	profileClearQuietChecks = 3
)

// 删除动作与「是否已消失」的判定做成包级变量，测试可注入以精确编排
// 「删掉 → 又被建回来 → 再删掉 → 稳定」这种时序，而不必真的去制造文件占用
// （也制造不出来：Go 打开文件默认带 FILE_SHARE_DELETE，删得掉）。
// 与 resolvePortableRoot / shortcutDesktopDirectory 同一套路。
var (
	profileRemove = os.RemoveAll
	profileGone   = func(path string) bool {
		_, err := os.Stat(path)
		return os.IsNotExist(err)
	}
)

// removeProfileDir 删除一个 WebView2 用户数据目录。
// 目录本就不存在视为成功（关闭清理与打开前兜底会前后各清一次，第二次必然面对不存在的目录）。
//
// 失败时把错误交出来，而不是静默重试到放弃：「清空」这条承诺不能默默落空，
// 调用方据此决定是记日志还是拒绝打开面板。
func removeProfileDir(dir string, policy sessionClearPolicy) error {
	if dir == "" {
		return nil
	}

	quiet := 0
	var lastErr error
	for attempt := 0; attempt < profileClearAttempts; attempt++ {
		switch err := profileRemove(dir); {
		case err != nil:
			lastErr = fmt.Errorf("删除失败: %w", err)
			quiet = 0
		case profileGone(dir):
			lastErr = nil
			if policy == clearOnce {
				return nil
			}
			quiet++
			if quiet >= profileClearQuietChecks {
				return nil
			}
		default:
			// 删成功了、目录却又在：多半是还没退干净的浏览器进程重建的，继续删。
			lastErr = errors.New("目录删除后又被重建")
			quiet = 0
		}

		if attempt < profileClearAttempts-1 && profileClearDelay > 0 {
			time.Sleep(profileClearDelay)
		}
	}

	if lastErr == nil {
		lastErr = errors.New("清空后目录仍反复出现")
	}
	return lastErr
}

// clearTabProfile 清空单个标签的会话数据。
func clearTabProfile(tabID string, policy sessionClearPolicy) error {
	dir, err := tabProfileDir(tabID)
	if err != nil {
		return err
	}
	return removeProfileDir(dir, policy)
}

// clearPanelProfiles 清空面板下所有标签的会话数据。
//
// 逐项尝试后合并错误：一个标签清不掉（比如它的浏览器进程还赖着）不应让其余标签也不清，
// 全清掉显然比一个都不清好。
func clearPanelProfiles(tabs []PanelTab, policy sessionClearPolicy) error {
	var failures []error
	for _, tab := range tabs {
		if err := clearTabProfile(tab.ID, policy); err != nil {
			failures = append(failures, fmt.Errorf("标签「%s」: %w", tab.Name, err))
		}
	}
	return errors.Join(failures...)
}

// preparePanelProfile 为即将打开的标签准备 profile 目录，返回其路径。
//
// fresh 为真时**先清空再建目录**：这是「每次新开都是全新环境」的兜底。上次正常关闭时
// 已经清过了，这里通常是一次空操作；真正起作用的是上次没清干净的情形
// （程序被强杀、关机、清空时目录还被浏览器进程占着），否则用户会带着上次的登录态进来。
//
// 清空失败时返回错误，调用方应让本次打开失败：宁可让用户看到「面板打不开 + 原因」，
// 也不能悄悄带着上一次的登录会话打开 —— 那正是用户开这个开关要避免的事。
func preparePanelProfile(tabID string, fresh bool) (string, error) {
	dir, err := tabProfileDir(tabID)
	if err != nil {
		return "", err
	}

	if fresh {
		if err := removeProfileDir(dir, clearOnce); err != nil {
			return "", fmt.Errorf(
				"清空标签 %s 的浏览器数据失败（可能有残留的浏览器进程正在占用该目录，稍后重试）: %w",
				tabID, err,
			)
		}
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create isolated browser profile for tab %s: %w", tabID, err)
	}
	return dir, nil
}
