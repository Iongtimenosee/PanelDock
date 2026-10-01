package main

// 面向用户的错误码（i18n）。
//
// 背景：管理界面要支持多语言，而后端直接返回中文错误串的话，英文用户会在 alert 里
// 看到「保存面板失败：面板名称不能为空」这种混排。这里把**会到达用户眼前的**错误
// 改成稳定的机器码（如 "panel.nameRequired"），前端词典负责把码翻成当前语言的文案；
// 翻译不到的（内部技术错误、底层 os 错误）原样显示，宁可露英文技术细节也不硬编。
//
// 序列化格式约定（与前端 tErr 的正则配套，改一边必须改另一边）：
//   - 无细节：Error() == "panel.notFound"
//   - 带细节：Error() == "panel.urlInvalid: parse ...: missing protocol scheme"
//     细节是技术性补充（解析失败原因、底层 os 错误文本），**不参与翻译**。
//
// cause 保留 errors.Is / errors.As 链：ErrTrayIconRequired 这类哨兵错误被包进
// appError 后，`errors.Is(err, ErrTrayIconRequired)` 依然成立。
//
// 刻意不做的事：不把所有 Go 错误都码化。只码化「用户能看懂、也只需要看懂」的那层
// （表单校验、面板不存在之类的业务失败）；session.go 内部清理错误、文件系统错误
// 等技术细节留给 detail / 原文 —— 它们对用户的价值是「复制给开发者看」。

// appError 是带错误码的用户可见错误。
type appError struct {
	code   string
	detail string
	cause  error
}

func (e *appError) Error() string {
	detail := e.detail
	if detail == "" && e.cause != nil {
		detail = e.cause.Error()
	}
	if detail != "" {
		return e.code + ": " + detail
	}
	return e.code
}

// Unwrap 保持哨兵错误（ErrTrayIconRequired 等）的 errors.Is 判定可用。
func (e *appError) Unwrap() error { return e.cause }

// errCode 返回一个无细节的用户可见错误（纯错误码）。
func errCode(code string) error { return &appError{code: code} }

// errCodeDetail 返回带技术细节的错误码；细节原样展示、不翻译。
func errCodeDetail(code, detail string) error { return &appError{code: code, detail: detail} }

// errCodeWrap 用错误码包装底层错误：细节取底层错误文本，errors.Is 链保持可用。
func errCodeWrap(code string, cause error) error { return &appError{code: code, cause: cause} }

// ─── 错误码常量 ────────────────────────────────────────────────────────────────
// 命名规则：域.含义。前端词典键为 "err." + 码（见 frontend/src/i18n/zh-CN.js）。
// 改码名 = 改前端词典键，两边必须同步。
const (
	errPanelNotFound     = "panel.notFound"     // 面板不存在
	errPanelNotOpen      = "panel.notOpen"      // 面板未打开
	errPanelDisabled     = "panel.disabled"     // 面板已停用
	errPanelNoTabs       = "panel.noTabs"       // 面板没有可用的标签
	errPanelNameRequired = "panel.nameRequired" // 面板名称不能为空
	errPanelNameTooLong  = "panel.nameTooLong"  // 面板名称过长
	errPanelURLRequired  = "panel.urlRequired"  // 地址不能为空
	errPanelURLInvalid   = "panel.urlInvalid"   // 地址格式无效（细节为解析失败原因）
	errPanelURLScheme    = "panel.urlScheme"    // 仅支持 http/https
	errPanelURLHost      = "panel.urlHost"      // 地址缺少主机名
	errTabNotFound       = "tab.notFound"       // 标签不存在
	errTabMinOne         = "tab.minOne"         // 至少需要保留一个标签
	errTabRemoveUnclean  = "tab.removeUnclean"  // 标签已删除但数据没清干净
	errTabsRemoveUnclean = "tabs.removeUnclean" // 标签已移除但部分数据没清干净
	errShortcutMissing   = "shortcut.missing"   // 该面板还没有快捷方式
	errTrayIconRequired  = "tray.iconRequired"  // 托盘图标必须保留（有关闭行为为 tray）
	errDeleteClearFailed = "delete.clearFailed" // 删除面板时清空数据失败
	errResetClearFailed  = "reset.clearFailed"  // 重置面板数据失败
	errCleanFailed       = "clean.failed"       // 清理残留目录失败
	errExecPathFailed    = "app.execPathFailed" // 获取程序路径失败
	// 刷不了图标的四种「业务结果」：不当错误抛，走 PanelIconPreview.Reason，
	// 前端同样按码翻译（词典键 err.icon.reason.*）。
	errIconReasonNoTabs      = "icon.reason.noTabs"      // 分组没有标签
	errIconReasonNotOpen     = "icon.reason.notOpen"     // 还没打开过（没取到图标样本）
	errIconReasonNoIcon      = "icon.reason.noIcon"      // 当前地址没有图标
	errIconReasonUndecodable = "icon.reason.undecodable" // 取到但解码不出可用尺寸
	errIconIDInvalid         = "icon.idInvalid"          // 面板 ID 不合法（图标文件名）
	errIconNoFrames          = "icon.noFrames"           // 没有可用的图标帧
	errIconMkdirFailed       = "icon.mkdirFailed"        // 创建图标目录失败
	errIconWriteFailed       = "icon.writeFailed"        // 写入图标失败
	errIconSaveFailed        = "icon.saveFailed"         // 保存图标失败（改名）
	errShortcutPathEmpty     = "shortcut.pathEmpty"      // 快捷方式路径为空
	errShortcutMissingFile   = "shortcut.missingFile"    // 快捷方式不存在
	errCloseActionInvalid    = "settings.closeActionInvalid"
	errThemeInvalid          = "settings.themeInvalid"
	errSessionModeInvalid    = "settings.sessionModeInvalid"
	errPasswordInvalid       = "settings.passwordInvalid"
	errLanguageInvalid       = "settings.languageInvalid"
	errConfigExportFailed    = "config.export.failed"  // 导出：写目标文件失败
	errConfigImportFailed    = "config.import.failed"  // 导入：读源文件/写回失败
	errConfigImportInvalid   = "config.import.invalid" // 导入：不是有效配置（JSON 坏 / schema 版本不符）
)
