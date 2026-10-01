// English dictionary. Key set must mirror zh-CN.js (the source locale);
// a missing key here falls back to Chinese rather than breaking the UI.

export const enUS = {
  // ─── Top bar ────────────────────────────────────────────────────────────
  'app.eyebrow': 'PANELDOCK · Web panel launcher',
  'app.subtitle': 'Each panel is a multi-tab window with isolated WebView2 login sessions per tab.',

  'theme.toLight': 'Switch to light',
  'theme.toDark': 'Switch to dark',

  // ─── App settings ───────────────────────────────────────────────────────
  'settings.sectionLabel': 'App settings',
  'settings.generalTitle': 'General',

  'settings.trayLabel': 'Show tray icon',
  'settings.trayHint':
    'Keeps a PanelDock icon in the system tray (hosted by a background window, regardless of how many panels are open). Right-click it for Open manager, Show/hide panel, Always on top, Close panel and Quit PanelDock. In lightweight mode (direct shortcut launch) the main window is hidden and the tray menu is the easiest way back.',
  'settings.trayForced':
    'The tray icon cannot be turned off right now: "When closing a panel window" below is set to "Minimize to tray" — such windows hide into the tray on close, and the icon is the only way to get them back. Change that setting first, then disable the icon.',

  'settings.themeLabel': 'Theme',
  'settings.themeAuto': 'Follow system (Windows light/dark)',
  'settings.themeLight': 'Light',
  'settings.themeDark': 'Dark',
  'settings.themeHint':
    'Affects this manager window only. The ◐ button in the top-right corner toggles light/dark (whichever you pick becomes sticky); while on "Follow system", toggling pins the theme to that side. Panel windows and web content do not follow this setting.',

  'settings.languageLabel': 'Language',
  'settings.languageAuto': 'Follow system',
  'settings.languageZhCN': '简体中文',
  'settings.languageEnUS': 'English',
  'settings.languageHint':
    'Applies to this manager window and the app\'s own dialogs and tray menu; the UI refreshes once after switching. System components such as the WebView2 context menu follow the Windows display language and are not affected.',

  'settings.closeActionLabel': 'When closing a panel window',
  'settings.closeActionAsk': 'Ask every time (minimize to tray / close)',
  'settings.closeActionTray': 'Minimize to tray (app keeps running in the background)',
  'settings.closeActionClose': 'Close it (no more prompts; closes only that panel)',
  'settings.closeActionHint':
    'A panel window is a grouped-tab window. "Close it" closes only that panel — other panels and the manager window are not affected. Whether it is the last window left decides whether the app exits afterwards (it stays while any window or the tray icon is around).',

  'settings.lightweightLabel': 'In lightweight mode (direct shortcut), quit after the last panel closes',
  'settings.lightweightTitle': 'Lightweight mode = launched directly via desktop/taskbar shortcut; the manager window was never opened.',
  'settings.lightweightHint':
    'Checked (default): shortcut-launched instances are "use and go" — closing the last panel ends the process, nothing lingers in the tray. Unchecked: identical to a normal launch; the app stays as long as "Show tray icon" is enabled.',

  'settings.configLabel': 'Configuration file',
  'settings.configExport': 'Export',
  'settings.configImport': 'Import',
  'settings.configHint':
    'Exports all panels and settings (the contents of config.json) for backup or moving the installed app to another machine. Icon caches and website login data are not included — logins live in each session folder and do not travel with the config.',
  'settings.configExported': 'Configuration exported.',
  'settings.configImportConfirm':
    'Importing replaces ALL current panels and settings (your existing config is backed up to config.json.bak first). Continue?',
  'settings.configImported': 'Configuration imported; the UI has been refreshed.',

  // ─── Panel list ─────────────────────────────────────────────────────────
  'panels.sectionLabel': 'Panels',
  'panels.sectionTitle': 'Web panels',
  'panels.add': '＋ New panel',
  'empty.hint': 'No panels yet — click "New panel" in the top-right corner to create the first one.',

  // ─── Panel form ─────────────────────────────────────────────────────────
  'form.nameLabel': 'Panel name',
  'form.namePlaceholder': 'e.g. Router admin',
  'form.tabsLabel': 'Tabs',
  'form.addTab': '＋ Add tab',
  'form.httpWarning': 'Prefer https:// for remote addresses — plain http exposes credentials on the wire.',
  'form.tabsHint':
    'One tab makes a single-tab panel; multiple tabs form a group, ordered as listed here. Drag the left handle or use ↑ ↓ to reorder — this order is the tab order in the panel window.',
  'form.sessionLabel': 'Browser state after closing',
  'form.sessionPersistTitle': 'Keep state',
  'form.sessionPersistDesc': 'Cookies, logins, cache and local storage stay on disk; the next open resumes the last session. Default.',
  'form.sessionFreshTitle': 'Clear on close',
  'form.sessionFreshDesc':
    'Clears this panel\'s browser data for all tabs when it closes, so the next open is a fresh environment (you will need to log in again). An extra pre-open sweep removes anything a previous close missed.',
  'form.sessionHint':
    'Affects this panel only; other panels and their login sessions are untouched. Clearing wipes logins, cache and site preferences — make sure you still remember those accounts. Note: saving the panel reopens a running panel, so if it is open now it will be cleared and reloaded on save.',
  'form.passwordLabel': 'Passwords',
  'form.passwordTitle':
    'Uses WebView2\'s built-in password saving: after submitting a login page you are asked whether to save, and it auto-fills on the next open. Passwords are encrypted by WebView2 inside this group\'s browser data; this app never reads or exports them.',
  'form.passwordCheck': 'Save login passwords',
  'form.passwordHint':
    'When enabled, login pages offer to save passwords like Edge does and auto-fill them on the next open of this group. Passwords are encrypted by WebView2 inside this group\'s browser data; this app neither reads nor exports them. Two things to know: turning it off only <strong>stops saving new passwords</strong> — previously saved ones still auto-fill; to leave nothing behind, set "Browser state after closing" above to "Clear on close" — that wipes passwords too.',
  'form.enabledCheck': 'Enable this panel',
  'form.save': 'Save',
  'form.cancel': 'Cancel',
  'form.needOneTab': 'At least one valid tab is required (both name and address are required)',

  // ─── Orphan profiles banner ─────────────────────────────────────────────
  'orphan.hint':
    'Browser data folders that belong to no group/tab were found (likely left by tabs or groups deleted by early versions). They can no longer be opened or recovered; they just take up disk space and may still hold saved passwords.',
  'orphan.clean': 'Clean up',
  'orphan.title': 'Found {count} leftover browser data folder(s)',
  'orphan.confirm': 'This deletes all leftover folders and everything inside (including saved passwords). This cannot be undone. Clean up now?',
  'orphan.done': 'Cleaned up {count} leftover folder(s).',

  'footer.text':
    'Config: <code>data\\config.json</code> next to the exe (portable mode, copy the whole folder) or <code>%APPDATA%\\PanelDock\\config.json</code> · one isolated WebView2 profile per tab · saved passwords are encrypted and kept by WebView2; this app never reads or exports them',

  // ─── Delete confirmation ────────────────────────────────────────────────
  'del.sectionLabel': 'Delete panel',
  'del.title': 'Delete panel "{name}"?',
  'del.message': 'The panel disappears from the list and its open panel window (if any) is closed too. This cannot be undone.',
  'del.warning':
    'Its browser data is deleted as well: cookies, logins, cache, local storage, and passwords saved by WebView2 — every tab\'s login session is gone for good; re-adding the panel will not bring it back.',
  'del.removeShortcuts': 'Also delete this panel\'s desktop shortcuts',
  'del.shortcutHintSome':
    'Leave unchecked to keep the {count} shortcut(s) below: after deletion they point to no panel; double-clicking just opens the manager window.',
  'del.shortcutHintNone': 'This panel has no desktop shortcuts (never created, or already deleted manually).',
  'del.footnote':
    'Only want to wipe the data and keep the panel? Cancel and use "Reset data" on the card. If this panel is pinned to the taskbar, this app leaves that alone — right-click it on the taskbar and choose "Unpin from taskbar" if needed.',
  'del.cancel': 'Cancel',
  'del.confirm': 'Delete panel',

  // ─── Reset confirmation ─────────────────────────────────────────────────
  'rs.sectionLabel': 'Reset panel data',
  'rs.title': 'Reset all data of panel "{name}"?',
  'rs.messagePrefixOne': 'This deletes this group\'s browser data:',
  'rs.messagePrefixMany': 'This deletes the browser data of all {count} tab(s) in this group:',
  'rs.messageBody':
    'cookies, logins, cache, local storage, and passwords saved by WebView2. The next open will be a fresh environment: you will need to log in again and saved passwords will not auto-fill. This cannot be undone.',
  'rs.footnote':
    'If the panel is currently open, its window is closed first, then the data is cleared; you can reopen it from the card afterwards. The panel\'s addresses, tabs, shortcuts and settings are not affected — only its browser data is reset.',
  'rs.cancel': 'Cancel',
  'rs.confirm': 'Reset data',

  // ─── Pin to taskbar guide ───────────────────────────────────────────────
  'pin.sectionLabel': 'Pin to taskbar',
  'pin.title': 'Pin "{name}" to the taskbar',
  'pin.message':
    'Since Windows 10, apps are not allowed to pin themselves to the taskbar (pinning is considered "the user\'s own choice"), and Windows 11 closed the last workaround. So the shortcut can only be prepared for you — the final two clicks are yours.',
  'pin.readyLabel': 'Prepared shortcut',
  'pin.step1': 'After reading the steps, click <strong>"Select shortcut"</strong> below; File Explorer opens with it selected',
  'pin.step2': '<strong>Right-click</strong> the shortcut',
  'pin.step3': 'Choose <strong>"Pin to taskbar"</strong> (on Windows 11, click "Show more options" first)',
  'pin.footnote': 'The taskbar icon appears as soon as it is pinned; clicking it opens this panel directly. Click "Select shortcut" when you are ready.',
  'pin.close': 'Got it',
  'pin.reveal': 'Select shortcut',
  'pin.working': 'Opening…',
  'pin.revealOk': 'File Explorer is open with the shortcut selected — right-click it to pin.',
  'pin.revealFailed':
    'Could not open File Explorer: {err}. Most likely the shortcut was deleted — close this dialog and recreate it with "Desktop shortcut" on the panel card.',

  // ─── Refresh icon dialog ────────────────────────────────────────────────
  'ic.sectionLabel': 'Refresh icon',
  'ic.titleWithCreate': 'Set "{name}" to its site icon and create a desktop shortcut',
  'ic.title': 'Set "{name}" to its site icon',
  'ic.messageWithCreate':
    'Saves the first tab\'s site icon as an .ico file (kept long-term in the icons folder next to the config) and creates a desktop shortcut that uses it. Shortcuts created later pick it up automatically.',
  'ic.message':
    'Saves the first tab\'s site icon as an .ico file (kept long-term in the icons folder next to the config) and switches the shortcuts below to it. Shortcuts created later pick it up automatically.',
  'ic.willCreate': 'There is no desktop shortcut for this group yet; one will be created.',
  'ic.willOverwrite': 'The existing desktop shortcut will be overwritten (its file name is kept).',
  'ic.cachedOverwrite': 'The icon file was saved before and will be overwritten.',
  'ic.duplicatesNote': '{count} other shortcut(s) of this group on the desktop will be cleaned up (one per group).',
  'ic.previewAlt': 'Icon preview',
  'ic.sourceWithHost': 'Icon source: {host} ({tab})',
  'ic.source': 'Icon source: {tab}',
  'ic.firstTab': 'first tab',
  'ic.sizes': 'Sizes written: {sizes} px',
  'ic.locationCached': 'Icon location: {path} (exists; will be overwritten)',
  'ic.location': 'Icon location: {path}',
  'ic.cancel': 'Cancel',
  'ic.apply': 'Apply',
  'ic.applyCreate': 'Create shortcut and apply',
  'ic.working': 'Working…',
  'ic.workingHint': 'Writing the icon and updating shortcuts…',
  'ic.applyFailed': 'Could not apply: {err}',

  'ic.doneSaved': 'Saved the site icon of "{name}" to:',
  'ic.doneCreated': 'There was no desktop shortcut for this group; one was created with this icon:',
  'ic.doneUpdated': 'Shortcuts now using this icon:',
  'ic.doneF5': 'If the desktop icons do not change right away, press F5 to refresh.',
  'ic.doneShortcutFailed': 'The desktop shortcut could not be updated; only the icon file was saved.',
  'ic.doneRemoved': 'Also cleaned up {count} extra shortcut(s) of this group (one per group):',
  'ic.doneFailedCount': '{count} item(s) could not be completed (most likely moved or in use):',

  // ─── Panel card ─────────────────────────────────────────────────────────
  'card.enabled': 'Enabled',
  'card.disabled': 'Disabled',
  'card.top': 'Always on top',
  'card.topTitle': 'Keep this panel\'s window above all others (independent of group order)',
  'card.fresh': 'Clear on close',
  'card.freshTitle':
    'Clear this panel\'s browser data for all tabs when it closes (cookies, logins, cache, local storage); the next open is a fresh environment. An extra pre-open sweep removes anything missed.',
  'card.password': 'Save passwords',
  'card.passwordTitle':
    'Save this group\'s login passwords: login pages offer to save, and auto-fill on the next open. Passwords are encrypted by WebView2 inside this group\'s browser data; this app never reads or exports them. Turning it off only stops saving new passwords — previously saved ones still auto-fill.',
  'card.open': 'Open',
  'card.openTitle': 'Open all tabs of this group; if it is already open, bring it to the front',
  'card.shortcut': 'Shortcut',
  'card.shortcutTitle':
    'Create a direct --open shortcut on the desktop; if one already exists for this group you are asked first, then it is overwritten (one per group)',
  'card.taskbar': 'Pin',
  'card.taskbarTitle': 'Prepares and selects the shortcut; you right-click to pin it (Windows does not allow apps to pin themselves)',
  'card.icon': 'Icon',
  'card.iconTitle':
    'Saves this group\'s first tab site icon as a local .ico and switches the desktop shortcut and taskbar pin to it; creates a desktop shortcut if there is none. Open the group first and let the page load its icon',
  'card.edit': 'Edit',
  'card.reset': 'Reset data',
  'card.resetTitle':
    'Clears all browser data of this group (cookies, logins, cache, local storage, saved passwords) — a fresh environment; the panel config and shortcuts are not affected',
  'card.delete': 'Delete',
  'card.deleteTitle':
    'Deletes this group: its browser data (cookies, logins, cache, local storage, saved passwords) is wiped too. This cannot be undone',
  'card.noTabs': 'No tabs',
  'card.positionDefault': 'Position: default',
  'card.position': 'Position ({x}, {y}) · Size {w} × {h}',
  'card.metaTabs': '{count} tab(s)',
  'card.metaProfile': 'isolated profile',

  // ─── Tab rows in the form ───────────────────────────────────────────────
  'tab.namePlaceholder': 'Tab name',
  'tab.nameQuote': '"{name}"',
  'tab.this': 'This tab',
  'tab.dragTitle': 'Drag to reorder',
  'tab.moveUp': 'Move up',
  'tab.moveDown': 'Move down',
  'tab.remove': 'Remove tab',
  'tab.removeConfirm':
    '{what} will be removed from the group; its browser data (login state, saved passwords, etc.) is deleted on save. This cannot be undone.\n\nRemove it?',
  'tab.orderHint': 'This order is the tab order in the panel window; the active tab stays where it was.',

  // ─── Shortcut / taskbar / icon results ──────────────────────────────────
  'shortcut.extraNote': '\n\nAlso, {count} extra shortcut(s) of this group on the desktop will be cleaned up:\n{list}',
  'shortcut.iconNoteExisting': '\n\nIt will use the previously saved site icon.',
  'shortcut.iconNoteNone': '\n\nTo use the site icon on the desktop, open this group first, then use "Icon" on the card.',
  'shortcut.confirmOverwrite': 'The desktop already has a shortcut for "{name}":\n{path}{extra}\n\nOverwrite it?{iconNote}',
  'shortcut.created': 'Shortcut created on the desktop:\n{path}',
  'shortcut.overwritten': 'Overwrote the existing desktop shortcut:\n{path}',
  'shortcut.openHint': 'Double-click it to open "{name}" directly; it keeps working after the panel is renamed.',
  'shortcut.removedNote': 'Also cleaned up {count} extra shortcut(s) of this group:',

  'taskbar.alreadyPinned': '"{name}" is already on the taskbar.\n\n{path}',

  'icon.notAvailable': 'Cannot refresh the icon of "{name}" right now.\n\n{reason}',

  'reset.done':
    'Reset "{name}": its browser data (cookies, logins, cache, local storage, saved passwords) has been wiped.\nThe panel config and shortcuts are untouched; the next open is a fresh environment and you will need to log in again.',

  'delete.doneKept':
    'Deleted panel "{name}".\nDesktop shortcuts were kept as you chose:\n{list}\n\nThey no longer point to any panel; double-clicking just opens the manager window. Delete them at your convenience.',

  // ─── Failure prefixes (joined with tErr-translated backend codes) ───────
  'errors.topFailed': 'Failed to set always-on-top: {err}',
  'errors.sessionFailed': 'Failed to set session mode: {err}',
  'errors.passwordFailed': 'Failed to set password saving: {err}',
  'errors.openFailed': 'Failed to open panel: {err}',
  'errors.shortcutFailed': 'Failed to create shortcut: {err}',
  'errors.taskbarFailed': 'Failed to prepare taskbar pinning: {err}',
  'errors.iconCheckFailed': 'Failed to inspect icon: {err}',
  'errors.resetFailed': 'Reset failed: {err}',
  'errors.deleteFailed': 'Failed to delete panel: {err}',
  'errors.saveFailed': 'Failed to save panel: {err}',
  'errors.themeFailed': 'Failed to switch theme: {err}',
  'errors.trayFailed': 'Failed to change tray icon setting: {err}',
  'errors.closeActionFailed': 'Failed to set close action: {err}',
  'errors.lightweightFailed': 'Failed to save setting: {err}',
  'errors.languageFailed': 'Failed to switch language: {err}',
  'errors.configExportFailed': 'Failed to export configuration: {err}',
  'errors.configImportFailed': 'Failed to import configuration: {err}',
  'errors.listFailed': 'Could not read the panel list: {err}',
  'errors.cleanFailed': 'Cleanup failed: {err}',

  // ─── Backend error codes (see apperror.go; keep both sides in sync) ─────
  'err.panel.notFound': 'Panel not found',
  'err.panel.notOpen': 'Panel window is not open',
  'err.panel.disabled': 'Panel is disabled',
  'err.panel.noTabs': 'The panel has no usable tabs',
  'err.panel.nameRequired': 'Panel name is required',
  'err.panel.nameTooLong': 'Panel name is too long (64 characters max)',
  'err.panel.urlRequired': 'Address is required',
  'err.panel.urlInvalid': 'Invalid address format',
  'err.panel.urlScheme': 'Only http:// and https:// addresses are supported',
  'err.panel.urlHost': 'The address is missing a host name',
  'err.tab.notFound': 'Tab not found',
  'err.tab.minOne': 'At least one tab must be kept',
  'err.tab.removeUnclean': 'The tab was deleted but its browser data was not fully cleared (a browser process is likely still exiting; "Reset data" can sweep it again shortly)',
  'err.tabs.removeUnclean': 'Tabs were removed but some browser data was not fully cleared (a browser process is likely still exiting; "Reset data" can sweep it again shortly)',
  'err.shortcut.missing': 'This panel has no shortcut yet',
  'err.tray.iconRequired':
    '"When closing a panel window" is set to "Minimize to tray", so the tray icon must stay on — such windows hide into the tray on close and the icon is the only way to get them back. Change that setting first, then disable the tray icon',
  'err.delete.clearFailed': 'Failed to clear its browser data; the panel was not deleted (a browser process is likely still holding the folder — try again shortly)',
  'err.reset.clearFailed': 'Failed to clear browser data (a browser process is likely still holding the folder — try again shortly)',
  'err.clean.failed': 'Failed to clean up leftover folders',
  'err.app.execPathFailed': 'Failed to locate the app executable',
  'err.icon.reason.noTabs': 'This group has no tabs, so there is no address to take an icon from.',
  'err.icon.reason.notOpen':
    'This group has not been opened yet (or the page is still loading), so no icon is available. Open it first, wait for the page to show, then use "Icon" again.',
  'err.icon.reason.noIcon': 'The current address has no icon available; only the default icon can be used.',
  'err.icon.reason.undecodable':
    'An icon was found at the current address but could not be decoded into a usable size; only the default icon can be used.',
  'err.icon.idInvalid': 'Invalid panel ID; cannot generate the icon file name',
  'err.icon.noFrames': 'No usable icon frames',
  'err.icon.mkdirFailed': 'Failed to create the icon folder',
  'err.icon.writeFailed': 'Failed to write the icon',
  'err.icon.saveFailed': 'Failed to save the icon',
  'err.shortcut.pathEmpty': 'Shortcut path is empty',
  'err.shortcut.missingFile': 'Shortcut does not exist',
  'err.settings.closeActionInvalid': 'Unknown close action',
  'err.settings.themeInvalid': 'Unknown theme',
  'err.settings.sessionModeInvalid': 'Unknown session mode',
  'err.settings.passwordInvalid': 'Unknown password-saving value',
  'err.settings.languageInvalid': 'Unknown language',
  'err.config.export.failed': 'Failed to write the destination file',
  'err.config.import.failed': 'Failed to read the source file or write the config',
  'err.config.import.invalid': 'Not a valid PanelDock configuration file (corrupted JSON or incompatible schema version)',
};
