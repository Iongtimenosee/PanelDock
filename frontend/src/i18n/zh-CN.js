// 简体中文词典（源语言）。所有 key 必须在这里齐备；en-US 缺 key 时回落到这里。
// 改动纪律：新增/改名 key 时同步 en-US.js；两边的 key 集合要保持一致
// （肉眼比对或 node -e 检查，本文件是唯一事实源）。

export const zhCN = {
  // ─── 顶栏 ───────────────────────────────────────────────────────────────
  'app.eyebrow': 'PANELDOCK · Web管理面板启动器',
  'app.subtitle': '每个面板支持多标签独立窗口，各标签 WebView2 登录会话互不干扰。',

  'theme.toLight': '切换到浅色',
  'theme.toDark': '切换到深色',

  // ─── 应用设置 ───────────────────────────────────────────────────────────
  'settings.sectionLabel': '应用设置',
  'settings.generalTitle': '常规',

  'settings.trayLabel': '在系统托盘显示图标',
  'settings.trayHint':
    '托盘里常驻一个 PanelDock 图标（由后台常驻窗口承载，与打开几个面板无关）。右键列出所有藏在托盘里的面板，点名字即取回；下方「彻底关闭窗口」子菜单会真的关掉某一个。双击图标是开关：在同一个窗口上来回「恢复 / 收回」。轻量模式（快捷方式直达）下主窗口是隐藏的，托盘菜单是最方便的入口。',
  'settings.trayForced':
    '托盘图标当前无法取消：下面的「关闭面板窗口时」被设置为「最小化到托盘」—— 面板窗口关闭后会藏进托盘，图标是找回它的唯一入口。要取消请先把它改成别的。',

  'settings.themeLabel': '界面配色',
  'settings.themeAuto': '跟随系统（Windows 深浅色）',
  'settings.themeLight': '浅色',
  'settings.themeDark': '深色',
  'settings.themeHint':
    '只影响本管理界面。右上角的 ◐ 按钮在浅色 / 深色之间一键切换（切到哪边就记住哪边）；「跟随系统」时按钮临时切过去，等于把配色固定成了那一侧。面板窗口与网页内容不跟随此设置。',

  'settings.languageLabel': '界面语言',
  'settings.languageAuto': '跟随系统',
  'settings.languageZhCN': '简体中文',
  'settings.languageEnUS': 'English',
  'settings.languageHint':
    '管理界面与本程序自己的询问框、托盘菜单跟随此项。切换后界面会刷新一次。WebView2 的右键菜单等系统组件跟随 Windows 显示语言，不受此项影响。',

  'settings.closeActionLabel': '关闭面板窗口时',
  'settings.closeActionAsk': '每次询问（最小化到托盘 / 直接关闭）',
  'settings.closeActionTray': '最小化到托盘（程序在后台继续运行）',
  'settings.closeActionClose': '直接关闭（不再询问，只关这个面板）',
  'settings.closeActionHint':
    '面板窗口 = 分组标签窗口。选「直接关闭」只关闭该面板，其他面板与管理面板都不受影响；它是不是最后一个窗口，决定关掉后面程序会不会退出（有别的窗口或托盘图标就留着）。',

  'settings.lightweightLabel': '轻量模式（快捷方式直达）下，关闭最后一个面板后退出程序',
  'settings.lightweightTitle': '轻量模式 = 双击快捷方式 / 任务栏图标直达启动，管理面板从未打开。',
  'settings.lightweightHint':
    '勾选（默认）：快捷方式直达的实例「用完即走」—— 关掉最后一个面板就收工，不在托盘里留进程。取消勾选：与常规启动一致，只要「在系统托盘显示图标」还开着就留在托盘里。',

  'settings.configLabel': '配置文件',
  'settings.configExport': '导出配置',
  'settings.configImport': '导入配置',
  'settings.configHint':
    '导出当前全部面板与设置（即 config.json 的内容），用于安装版换机或备份。不含图标缓存与网页登录数据（登录态在各自的会话目录里，不随配置走）。',
  'settings.configExported': '配置已导出。',
  'settings.configImportConfirm':
    '导入会整体替换当前的全部面板与设置（导入前会自动把现有配置备份为 config.json.bak）。确定继续？',
  'settings.configImported': '配置已导入，界面已按新配置刷新。',

  // ─── 面板列表 ───────────────────────────────────────────────────────────
  'panels.sectionLabel': '面板列表',
  'panels.sectionTitle': 'Web管理面板',
  'panels.add': '＋ 新建面板',
  'empty.hint': '还没有面板，点击右上角「新建面板」创建第一个。',

  // ─── 面板表单 ───────────────────────────────────────────────────────────
  'form.nameLabel': '面板名称',
  'form.namePlaceholder': '如：路由器后台',
  'form.tabsLabel': '标签页',
  'form.addTab': '＋ 添加标签',
  'form.httpWarning': '远程地址务必使用 https 协议，否则有泄露风险。',
  'form.tabsHint':
    '只有一个标签就是单标签面板；多个标签即分组，标签栏按这里的顺序排列。拖拽左侧把手或点 ↑ ↓ 调整顺序 —— 顺序即打开后面板标签栏的排列顺序。',
  'form.sessionLabel': '关闭后的浏览器状态',
  'form.sessionPersistTitle': '保留状态',
  'form.sessionPersistDesc': 'Cookie、登录态、缓存、本地存储都留在磁盘上，下次打开接着上次的会话。默认。',
  'form.sessionFreshTitle': '关闭后清空',
  'form.sessionFreshDesc':
    '关闭这个面板时清掉它所有标签的浏览器数据，下次打开等同全新环境（需重新登录）。打开前还会再清一次兜底，上次没清干净的也会被清掉。',
  'form.sessionHint':
    '只影响这个面板，其他面板与它们各自的登录会话不受影响。清空后登录态、页面缓存与站点偏好设置都会丢，请确认这些页面的账号你还记得。注意：保存面板设置会重开正在运行的面板，所以这个面板如果正开着，保存后它也会被清空并重新加载。',
  'form.passwordLabel': '账号密码',
  'form.passwordTitle':
    '用 WebView2 自带的密码保存：登录页提交后提示是否保存，下次打开自动回填。密码由 WebView2 加密后存在这个分组的浏览器数据里，本工具不读取、不导出。',
  'form.passwordCheck': '保存登录密码',
  'form.passwordHint':
    '开启后，登录页会像 Edge 那样提示保存密码，下次打开这个分组自动回填。密码由 WebView2 加密后存放在这个分组的浏览器数据里，本工具既不读取也不导出。两点要知道：关掉它只是<strong>不再保存新密码</strong>，此前已经存下的密码仍会被回填；想彻底不留密码，请把上面的「关闭后的浏览器状态」设为「关闭后清空」—— 那会连密码一起清掉。',
  'form.enabledCheck': '启用该面板',
  'form.save': '保存',
  'form.cancel': '取消',
  'form.needOneTab': '至少需要一个有效的标签（名称和地址均不能为空）',

  // ─── 残留目录提示条 ─────────────────────────────────────────────────────
  'orphan.hint':
    '检测到不属于任何分组/标签的浏览器数据残留目录（可能来自早期版本删除的标签或分组）。它们已无法被打开或恢复，只占磁盘空间，其中可能还留着已保存的密码。',
  'orphan.clean': '清理残留',
  'orphan.title': '发现 {count} 个浏览器数据残留目录',
  'orphan.confirm': '将删除全部残留目录及其中的所有数据（含已保存的密码），不可恢复。确定清理吗？',
  'orphan.done': '已清理 {count} 个残留目录。',

  'footer.text':
    '配置存储：exe 同目录 <code>data\\config.json</code>（便携模式，可整包拷走）或 <code>%APPDATA%\\PanelDock\\config.json</code> · 每个标签独立 WebView2 profile · 面板保存的密码由 WebView2 加密托管，本工具不读取、不导出',

  // ─── 删除确认对话框 ─────────────────────────────────────────────────────
  'del.sectionLabel': '删除面板',
  'del.title': '删除面板「{name}」？',
  'del.message': '删除后本面板会从列表中消失，其已打开的面板窗口也会一并关闭。此操作不可撤销。',
  'del.warning':
    '它的浏览器数据会一并删除：Cookie、登录态、缓存、本地存储，以及 WebView2 保存的登录密码 —— 每个标签的登录会话都会永久消失，重新添加也找不回来。',
  'del.removeShortcuts': '同时删除该面板的桌面快捷方式',
  'del.shortcutHintSome':
    '不勾选则保留下列 {count} 个快捷方式：删除面板后它们不再指向任何面板，双击只会打开管理界面。',
  'del.shortcutHintNone': '该面板没有桌面快捷方式（未创建过，或已被手动删除）。',
  'del.footnote':
    '只想清空数据、保留这个面板？取消后用卡片上的「重置数据」。任务栏上若已固定过这个面板的图标，本工具不会去动它 —— 需要时请自行在任务栏上右键「从任务栏取消固定」。',
  'del.cancel': '取消',
  'del.confirm': '删除面板',

  // ─── 重置确认对话框 ─────────────────────────────────────────────────────
  'rs.sectionLabel': '重置面板数据',
  'rs.title': '重置面板「{name}」的全部数据？',
  'rs.messagePrefixOne': '会删掉这个分组的浏览器数据：',
  'rs.messagePrefixMany': '会删掉这个分组全部 {count} 个标签的浏览器数据：',
  'rs.messageBody':
    'Cookie、登录态、缓存、本地存储，以及 WebView2 保存的登录密码。清空后下次打开等同全新环境，需要重新登录，已保存的密码也不会再回填。此操作不可撤销。',
  'rs.footnote':
    '若该面板正开着，会先关闭它的窗口再清空数据，之后可在卡片上重新打开。面板的地址、标签、快捷方式与各项设置都不受影响 —— 重置的只是它的浏览器数据。',
  'rs.cancel': '取消',
  'rs.confirm': '重置数据',

  // ─── 固定到任务栏引导 ───────────────────────────────────────────────────
  'pin.sectionLabel': '固定到任务栏',
  'pin.title': '固定「{name}」到任务栏',
  'pin.message':
    'Windows 从 10 起就不再允许程序自己把图标固定到任务栏（固定被视作「用户自己的选择」），Windows 11 连仅剩的旁路也封了。所以只能帮你把快捷方式准备好，最后两下需要你来点。',
  'pin.readyLabel': '已准备好的快捷方式',
  'pin.step1': '看完说明后点下面的<strong>「选中快捷方式」</strong>，资源管理器会打开并选中它',
  'pin.step2': '在这个快捷方式上点<strong>右键</strong>',
  'pin.step3': '选<strong>「固定到任务栏」</strong>（Windows 11 需先点「显示更多选项」）',
  'pin.footnote': '任务栏图标会在固定后立刻出现，点它就是直接打开该面板。看完上面几条再点「选中快捷方式」即可。',
  'pin.close': '知道了',
  'pin.reveal': '选中快捷方式',
  'pin.working': '正在打开…',
  'pin.revealOk': '资源管理器已打开并选中了这个快捷方式，在上面右键即可固定。',
  'pin.revealFailed':
    '没能打开资源管理器：{err}。多半是快捷方式已被删掉，请先关掉这里，用面板卡片上的「桌面快捷方式」重新创建一次。',

  // ─── 刷新图标对话框 ─────────────────────────────────────────────────────
  'ic.sectionLabel': '刷新图标',
  'ic.titleWithCreate': '把「{name}」的图标刷成网站图标，并创建桌面快捷方式',
  'ic.title': '把「{name}」的图标刷成网站图标',
  'ic.messageWithCreate':
    '把这个分组第一个标签的网站图标存成一份 .ico 文件，放在配置旁边的 icons 文件夹里长期保留，并在桌面上创建一份快捷方式用它。以后新建的快捷方式会自动带上它。',
  'ic.message':
    '把这个分组第一个标签的网站图标存成一份 .ico 文件，放在配置旁边的 icons 文件夹里长期保留，并让下面这些快捷方式改用它。以后新建的快捷方式也会自动用它。',
  'ic.willCreate': '桌面还没有这个分组的快捷方式，本次会创建一个。',
  'ic.willOverwrite': '桌面已有的那份快捷方式会被更新（文件名同步为当前面板名）。',
  'ic.cachedOverwrite': '图标文件之前已经存过，本次会覆盖它。',
  'ic.duplicatesNote': '桌面上另外 {count} 个同分组的快捷方式会被清理掉（一个分组只留一份）。',
  'ic.previewAlt': '图标预览',
  'ic.sourceWithHost': '图标来源：{host}（{tab}）',
  'ic.source': '图标来源：{tab}',
  'ic.firstTab': '第一个标签',
  'ic.sizes': '写入尺寸：{sizes} 像素',
  'ic.locationCached': '图标保存位置：{path}（已存在，这次会覆盖它）',
  'ic.location': '图标保存位置：{path}',
  'ic.cancel': '取消',
  'ic.apply': '应用',
  'ic.applyCreate': '创建快捷方式并应用',
  'ic.working': '正在处理…',
  'ic.workingHint': '正在写入图标并更新快捷方式…',
  'ic.applyFailed': '没能应用：{err}',

  'ic.doneSaved': '已把「{name}」的网站图标存到：',
  'ic.doneCreated': '桌面原本没有这个分组的快捷方式，已创建一个并改用这个图标：',
  'ic.doneUpdated': '已改用这个图标的快捷方式：',
  'ic.doneF5': '桌面上的图标如果没有立刻变化，按一下 F5 刷新即可。',
  'ic.doneShortcutFailed': '桌面快捷方式这次没能弄成，只把图标文件存了下来。',
  'ic.doneRemoved': '顺带清理了桌面上多余的 {count} 个同分组快捷方式（一个分组只留一份）：',
  'ic.doneFailedCount': '有 {count} 项没能完成（多半是被移动或占用）：',

  // ─── 面板卡片 ───────────────────────────────────────────────────────────
  'card.enabled': '启用',
  'card.disabled': '停用',
  'card.top': '窗口置顶',
  'card.topTitle': '让这个面板的窗口始终显示在最前面（与分组顺序无关）',
  'card.fresh': '关闭即清空',
  'card.freshTitle':
    '关闭这个面板时清空它所有标签的浏览器数据（Cookie、登录态、缓存、本地存储），下次打开等同全新环境。打开前还会再清一次兜底。',
  'card.password': '保存密码',
  'card.passwordTitle':
    '保存这个分组的登录密码：登录页提交后提示保存，下次打开自动回填。密码由 WebView2 加密存放在该分组的浏览器数据里，本工具不读取、不导出。关掉只是不再保存新密码，此前已存下的仍会被回填。',
  'card.open': '打开',
  'card.openTitle': '打开这个分组的全部标签；如果它已经开着，就把它切到前台',
  'card.shortcut': '桌面快捷方式',
  'card.shortcutTitle':
    '在桌面创建 --open 直达快捷方式；桌面已经有这个分组的一份时会先问一句，然后覆盖它（一个分组只留一份）',
  'card.taskbar': '固定任务栏',
  'card.taskbarTitle': '准备并选中快捷方式，由你右键固定到任务栏（Windows 不允许程序代为固定）',
  'card.icon': '刷新图标',
  'card.iconTitle':
    '把这个分组第一个标签的网站图标存成本地 .ico，并让桌面快捷方式与任务栏固定项都用它；桌面还没有快捷方式时会顺手创建一个。需要先打开这个分组、等网页把图标加载出来',
  'card.edit': '编辑',
  'card.reset': '重置数据',
  'card.resetTitle':
    '清空这个分组的全部浏览器数据（Cookie、登录态、缓存、本地存储、已保存的密码），等同全新环境；面板配置与快捷方式不受影响',
  'card.delete': '删除',
  'card.deleteTitle':
    '删除这个分组：它的浏览器数据（Cookie、登录态、缓存、本地存储、已保存的密码）会一并清空，不可恢复',
  'card.noTabs': '无标签',
  'card.positionDefault': '位置：默认',
  'card.position': '位置 ({x}, {y}) · 尺寸 {w} × {h}',
  'card.metaTabs': '{count} 个标签',
  'card.metaProfile': '独立 profile',

  // ─── 表单标签行 ─────────────────────────────────────────────────────────
  'tab.namePlaceholder': '标签名称',
  'tab.nameQuote': '「{name}」',
  'tab.this': '该标签',
  'tab.dragTitle': '按住拖动调整顺序',
  'tab.moveUp': '上移',
  'tab.moveDown': '下移',
  'tab.remove': '删除标签',
  'tab.removeConfirm':
    '{what}将从分组移除，它的浏览器数据（登录状态、已保存的密码等）会在保存时一并删除，不可恢复。\n\n确定要移除吗？',
  'tab.orderHint': '顺序即打开面板时标签栏的排列顺序；激活的仍是上次所在的标签。',

  // ─── 快捷方式 / 任务栏 / 图标的动态结果 ─────────────────────────────────
  'shortcut.created': '已在桌面创建快捷方式：\n{path}',
  'shortcut.overwritten': '已更新桌面原有的快捷方式：\n{path}',
  'shortcut.renamed': '已把桌面上旧的那份换成：\n{path}',
  'shortcut.openHint': '双击即可直接打开「{name}」，面板改名后仍然有效。',
  'shortcut.removedNote': '顺带清理了多余的 {count} 个同分组快捷方式：',

  // 桌面已有一份时的确认对话框
  'sc.sectionLabel': '桌面快捷方式',
  'sc.title': '「{name}」的桌面快捷方式',
  'sc.messageRename': '桌面上已经有一份这个分组的快捷方式，名字和当前面板名不一致。要把它换成以当前面板名命名的那一份吗？旧的那份会被删掉。',
  'sc.messageSame': '桌面上已经有一份这个分组的快捷方式。要把它更新到最新吗（目标、图标、备注同步，文件名不变）？',
  'sc.currentLabel': '现在桌面上的是：',
  'sc.afterLabel': '换成之后是：',
  'sc.extraHint': '另外这几份同分组的快捷方式会一并清理（一个分组只留一份）：',
  'sc.iconNoteExisting': '它会用上已经存好的站点图标。',
  'sc.iconNoteNone': '桌面图标想用站点图标的话，先打开这个分组，再点「刷新图标」。',
  'sc.footnote': '一个分组只留一份：不会新建第二份，也不会把旧的留着让你分不清点哪个。任务栏上你自己固定的那份不归本工具管。',
  'sc.cancel': '保留现有，不改动',
  'sc.confirm': '更新并替换',

  'taskbar.alreadyPinned': '「{name}」已经在任务栏上了。\n\n{path}',

  'icon.notAvailable': '「{name}」暂时刷不了图标。\n\n{reason}',

  'reset.done':
    '已重置「{name}」：它的浏览器数据（Cookie、登录态、缓存、本地存储、已保存的密码）已全部清空。\n面板配置与快捷方式都还在，重新打开时等同全新环境，需要重新登录。',

  'delete.doneKept':
    '已删除面板「{name}」。\n桌面快捷方式按你的选择保留：\n{list}\n\n它们已不再指向任何面板，双击只会打开管理界面，可自行删除。',

  // ─── 操作失败前缀（拼接 tErr 翻译的后端错误码）─────────────────────────
  'errors.topFailed': '窗口置顶设置失败：{err}',
  'errors.sessionFailed': '会话状态设置失败：{err}',
  'errors.passwordFailed': '密码保存设置失败：{err}',
  'errors.openFailed': '打开面板失败：{err}',
  'errors.shortcutFailed': '创建快捷方式失败：{err}',
  'errors.taskbarFailed': '固定到任务栏失败：{err}',
  'errors.iconCheckFailed': '检查图标失败：{err}',
  'errors.resetFailed': '重置失败：{err}',
  'errors.deleteFailed': '删除面板失败：{err}',
  'errors.saveFailed': '保存面板失败：{err}',
  'errors.themeFailed': '切换配色失败：{err}',
  'errors.trayFailed': '托盘图标设置失败：{err}',
  'errors.closeActionFailed': '关闭行为设置失败：{err}',
  'errors.lightweightFailed': '设置失败：{err}',
  'errors.languageFailed': '切换语言失败：{err}',
  'errors.configExportFailed': '导出配置失败：{err}',
  'errors.configImportFailed': '导入配置失败：{err}',
  'errors.listFailed': '无法读取面板列表：{err}',
  'errors.cleanFailed': '清理失败：{err}',

  // ─── 后端错误码（apperror.go 的码 + '.' 前缀，两边同步改）──────────────
  'err.panel.notFound': '面板不存在',
  'err.panel.notOpen': '面板未打开',
  'err.panel.disabled': '面板已停用',
  'err.panel.noTabs': '面板没有可用的标签',
  'err.panel.nameRequired': '面板名称不能为空',
  'err.panel.nameTooLong': '面板名称过长（最多 64 字符）',
  'err.panel.urlRequired': '地址不能为空',
  'err.panel.urlInvalid': '地址格式无效',
  'err.panel.urlScheme': '仅支持 http:// 与 https:// 地址',
  'err.panel.urlHost': '地址缺少主机名',
  'err.tab.notFound': '标签不存在',
  'err.tab.minOne': '至少需要保留一个标签',
  'err.tab.removeUnclean': '标签已删除，但它的浏览器数据没清干净（多半是浏览器进程还没退出，稍后可用「重置数据」再清一次）',
  'err.tabs.removeUnclean': '标签已移除，但其中部分浏览器数据没清干净（多半是浏览器进程还没退出，稍后可用「重置数据」再清一次）',
  'err.shortcut.missing': '该面板还没有快捷方式',
  'err.tray.iconRequired':
    '「关闭面板窗口时」被设置为「最小化到托盘（程序在后台继续运行）」，托盘图标必须保留 —— 面板窗口关闭后会藏进托盘，图标是找回它的唯一入口。请先把「关闭面板窗口时」改成别的，再取消托盘图标',
  'err.delete.clearFailed': '清空它的浏览器数据失败，面板未删除（多半是还有浏览器进程占着该目录，请稍后重试）',
  'err.reset.clearFailed': '清空浏览器数据失败（多半是还有浏览器进程占着该目录，稍后重试）',
  'err.clean.failed': '清理残留目录失败',
  'err.app.execPathFailed': '获取程序路径失败',
  'err.icon.reason.noTabs': '这个分组没有标签，没有地址可取图标。',
  'err.icon.reason.notOpen':
    '这个分组还没打开过（或网页还在加载中），窗口里没有可以取用的图标。请先打开它，等页面显示出来之后再点「刷新图标」。',
  'err.icon.reason.noIcon': '当前地址没有图标可用，只能用默认图标。',
  'err.icon.reason.undecodable': '当前地址的图标取到了但解码不出可用尺寸，只能用默认图标。',
  'err.icon.idInvalid': '面板 ID 不合法，无法生成图标文件名',
  'err.icon.noFrames': '没有可用的图标帧',
  'err.icon.mkdirFailed': '创建图标目录失败',
  'err.icon.writeFailed': '写入图标失败',
  'err.icon.saveFailed': '保存图标失败',
  'err.shortcut.pathEmpty': '快捷方式路径为空',
  'err.shortcut.missingFile': '快捷方式不存在',
  'err.settings.closeActionInvalid': '未知的关闭行为',
  'err.settings.themeInvalid': '未知的界面配色',
  'err.settings.sessionModeInvalid': '未知的会话状态处理方式',
  'err.settings.passwordInvalid': '未知的密码保存取值',
  'err.settings.languageInvalid': '未知的界面语言',
  'err.config.export.failed': '写入目标文件失败',
  'err.config.import.failed': '读取源文件或写回配置失败',
  'err.config.import.invalid': '不是有效的 PanelDock 配置文件（JSON 损坏或版本不兼容）',
};
