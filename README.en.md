# PanelDock

A lightweight Windows desktop tool that gives every back-end page you want to glance at any second its own window — router consoles, NAS, OpenClash / Zashboard, ops dashboards, status monitors, e-commerce back-ends, digital-currency wallet consoles. Anything you open just to check that things are still fine: safe, fast and light, with one window and one isolated session per panel, and no more browser tabs piling up.

*English · [简体中文](README.md)*

## Why use this instead of a browser tab

A browser is a machine that can do anything. The catch: **you want one feature, and you pay for the whole machine.** A router back-end, an OpenClash dashboard, a NAS console — all they need is "render this one page." A browser, in order to do that, starts up process management, an extension system, sync services, update checkers, an account stack, recommendation content… and every one of them sits resident in memory and quietly burns CPU in the background. PanelDock keeps only the last step.

- **It saves memory, and saves a lot of it.** RAM is priced like gold these days, and a browser keeps dozens of processes resident, most of which have nothing to do with the router page you actually want to see. A PanelDock panel *is* a WebView2, paying only for that one page. Open the same router page in a browser tab versus in PanelDock and the difference in resident memory is **on the order of several times** — and the simpler the page, the bigger the gap (router back-ends happen to be the simplest kind). The memory you save on one tab is enough to run several more panels.
- **It saves CPU, because nothing runs in the background.** No extension can wake itself up, no sync is polling, no update checker, no telemetry. Close a panel window and it stops consuming anything at all.
- **It's faster.** Double-click a desktop shortcut and you land directly on the target page — no browser shell cold-start in between.
- **It's safer.** Panel windows **inject no script and expose no bridge**, so a page gets zero local capability. No add-on can insert itself into your session. No browser background process keeps running after you thought you closed everything.
- **It's genuinely portable, and it's one folder away.** A portable build: unzip and run, nothing written to the registry. **Create an empty `data` folder next to the exe** and the next launch switches to portable mode: configuration and every login session go into it, and you copy that one folder to a USB stick or another machine and carry straight on (see "Data locations"). No `data` folder and it uses the system directories — the two are independent and never interfere with each other.

## Features

- Panel list management (create / edit / delete / enable-disable / **reset data**), stored in `%APPDATA%\PanelDock\config.json`.
- Each panel supports multiple tabs; tabs can be added, removed and **reordered** (the order is the order shown in the tab bar), and **each tab uses its own WebView2 profile**, so login sessions never interfere with each other.
- One tab makes a single-tab panel; several tabs make a group — there is no separate mode to pick.
- Panel windows are independent native Win32 windows with a native tab bar and **inject nothing into the page and expose no bridge**, so remote pages cannot call local capabilities.
- One-click desktop shortcut creation (`--open <panelID>` launches straight into it); "Pin to taskbar" prepares the shortcut and walks you through it — you click to select the shortcut, then right-click to pin (Windows does not let an app pin itself; see below).
- Always-on-top and a resident tray icon (**click the pin directly in the panel title bar** to toggle always-on-top; tray menu: open manager, show/hide, always-on-top, close panel, quit).
- **All tray behaviour lives in "App settings":** a single "close behaviour" ("When closing a panel window": ask / minimize to tray / close directly), with no separate per-panel "minimize to tray" switch. As soon as this is set to "minimize to tray", **the tray icon is forced on** — closed panels hide in the tray and the icon is the only way to get them back, so "show icon in system tray" cannot be switched off (and the app tells you why).
- **Closing a window only ever closes that window; whether the process exits depends on whether any window or tray icon remains.** Closing a panel window closes only that panel; other panels and the manager are untouched. **Closing the manager window has no options — the action is fixed:** if a tray icon or another window remains, it just closes itself (window hidden, icon stays); if neither remains it is the last window and closing it exits the app. There is also "in lightweight mode (shortcut launch), quit after the last panel closes" (on by default): when launched by double-clicking a shortcut and the manager was never opened, closing the last panel ends the session instead of leaving a process in the tray.
- Remembers each panel window's position and size and restores them on reopen.
- **Light / dark UI theme**: `auto` (default, follows Windows) / light / dark. Switch from the ◐ button at the top right of the manager, or the button at the far right of the **panel window's tab bar** (whichever side you pick becomes fixed — it never lands on `auto`); the "UI theme" dropdown under "App settings · General" writes the same value. **Only the shell changes, never the page:** panels display somebody else's web page, and this tool injects no style or script into it — the page's own light/dark look is up to the site. What follows your choice is only the window shell and WebView2's default background colour (in dark mode a new tab no longer flashes white first).
- **UI language: 中文 / English**: `auto` (default, follows the Windows display language) / Simplified Chinese / English. Covers the manager UI, the app's own dialogs and the tray menu; backend errors are translated too. System components such as the WebView2 context menu follow the Windows display language and are unaffected. Dictionaries are compiled into the binary (adding a language means writing one more dictionary and shipping a release); switch it under "App settings · General · UI language".
- **Export / import configuration** ("App settings · General"): dump every panel and setting (i.e. the contents of `config.json`) to a file, or import one to replace everything — for migrating machines or backups. Configuration only; icon cache and website login data are not included (login state lives in the per-session directories and does not travel with config). The current config is auto-backed-up to `config.json.bak` before importing, and an invalid import file leaves your existing config untouched.
- **Post-close browser state is configurable**: "keep state" by default (the next open resumes the previous login session); set a panel to "clear on close" and closing it deletes the browser data of all its tabs (cookies, login state, cache, local storage), so **the next open is a completely fresh environment** — with one more cleanup pass before opening as a safety net. See below.
- **Opening a disabled panel from a shortcut asks you first**: after you disable a panel, its shortcut and taskbar icon still point at it, and double-clicking no longer does nothing — you get a "panel is disabled" dialog offering "enable and open" (set it enabled and open immediately) or "keep disabled" (do nothing). See below.
- **Login passwords can be saved (per-group switch, on by default)**: with it on, a login page offers to save the account and password just like Edge does, and the next time you open that group they are filled in automatically. New groups start with it on and each group is independent. The password is encrypted by WebView2 and stored in that group's browser data — **this tool neither reads it nor exports it**. Two caveats: turning the switch off only stops saving *new* passwords, **passwords already saved are still filled in** (WebView2's defined behaviour); to leave no password behind at all, set the group to "clear on close" — that removes the passwords too.
- **One-click "reset data" per group**: the "Reset data" button on a card immediately deletes the WebView2 data of every tab in that group (cookies, login state, cache, local storage, **including passwords saved by WebView2**), so the next open is a fresh environment. Configuration and shortcuts are unaffected — this clears data, it does not delete the panel. A confirmation dialog explains the consequences first. See below.
- **Deleting a group clears its data too**: deleting a panel removes the WebView2 data directories of all its tabs (including saved passwords) so nothing is left on disk — the only difference from "reset data" is whether the panel itself survives. See "Data locations".

## Installation

Both packages contain exactly the same thing — pick either.

**Option A: installer (recommended for most people)**

1. Download `PanelDock-windows-x64-setup.exe` and double-click it.
2. It installs to `%LOCALAPPDATA%\Programs\PanelDock` — **no administrator rights and no UAC prompt**.
3. You get a Start Menu shortcut and a normal uninstall entry in Windows' installed-apps list.

**Option B: portable**

1. Download `PanelDock-windows-amd64.zip` and unzip it anywhere (for example `D:\Tools\PanelDock\`). **No installer**, nothing written to the registry; delete the folder to uninstall.
2. Double-click `PanelDock.exe`.

Either way it requires the **WebView2 Runtime**. Windows 10 / 11 usually ship with it already; if startup reports WebView2 missing, install Microsoft's Evergreen Runtime once and every WebView2 app shares it.

> **Heads up on uninstalling**: the installer also removes `%APPDATA%\PanelDock` (config) and `%LOCALAPPDATA%\PanelDock` (per-tab login sessions), so router credentials don't linger on disk. To keep your panel list, use "Export config" in the app before uninstalling.

Every release ships a `SHA256SUMS.txt`; verify after download:

```powershell
Get-FileHash .\PanelDock-windows-x64-setup.exe -Algorithm SHA256
```

### First run: Windows SmartScreen will block once

The binary has **no code-signing certificate** (several hundred dollars a year is not worth it for a free open-source tool), so Windows shows a one-time "unrecognized app" block. This is not a malware warning — it is Microsoft's blanket prompt for every unsigned new program, and **unblocking the download before opening it avoids it entirely**:

```powershell
Unblock-File -Path .\PanelDock-windows-x64-setup.exe
Unblock-File -Path .\PanelDock-windows-amd64.zip
```

An already-extracted exe can be unblocked on its own too:

```powershell
Unblock-File -Path .\PanelDock.exe
```

Or: right-click the exe → Properties → tick "Unblock" at the bottom → OK.

The only way to remove that prompt for good is code signing — an application for free open-source signing (SignPath Foundation) is in progress, and later releases will be signed once it is approved.

## Development

Requirements: Go 1.25+, Wails CLI v2, Node.js, WebView2 Runtime.

```powershell
wails dev     # dev mode
wails build   # produces build/bin/PanelDock.exe
```

Note: if Wails cannot find `npm` when launched from Git Bash, run `wails build` from `cmd.exe` instead.

Regression baseline: `go vet ./...` and `go test ./...` must both pass.

## Data locations

| What | Path |
|---|---|
| Configuration | `%APPDATA%\PanelDock\config.json` |
| Per-tab login sessions | `%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>` |

The table above is the **default** (non-portable) location.

### Portable mode: put a `data` folder next to the exe

If a `data` folder exists in the exe's own directory at startup (**an empty one is enough** — the same convention as VSCode's `data` directory), the two paths above move to:

| What | Path |
|---|---|
| Configuration | `<exe directory>\data\config.json` |
| Per-tab login sessions | `<exe directory>\data\WebViewProfiles\<tabID>` |

So copying the whole "exe + data" folder completes a migration — USB stick, another machine, login sessions included. Without that `data` folder, it falls back to the system directories in the table above.

The two locations are independent and never interfere: deleting `data` leaves the system-directory configuration alone, and vice versa. **Note:** a green build extracted from the ZIP has **no** `data` folder by default, so running it as-is uses the system directories — create the folder yourself if you want to carry it around.

To wipe one group's login data right now, use "Reset data" on its card (see below) — it keeps the panel and clears only the data.

**Deleting a panel clears the data along with it**: each tab's WebView2 data directory is deleted wholesale — cookies, login state, cache, local storage, and passwords saved by WebView2 — leaving nothing on disk. Each tab's directory is named after its tab ID, and creating a panel generates new IDs, so **after deletion, re-adding the panel will not bring that data back**. A confirmation dialog appears first (with an optional "also delete the shortcut"), and if cleanup fails it reports an error and aborts the deletion, so you never end up with "panel gone, data still there". Either way, you can always delete the directories in the table above yourself.
When deleting a panel you can choose to clean up its desktop shortcut as well; icons already pinned to the taskbar are never modified by this tool.

## Privacy

This tool handles your router back-end and NAS login pages — about the most sensitive pages you have — so let's be explicit. The one-sentence version: **it talks to no third party, and it does not save your passwords.**

- **Open source, verifiable by you.** MIT licensed, all source public. You don't have to take any of the following on faith — read the code, run a packet capture, and you're done in five minutes. That is the point of open source, and the reason we hide nothing.
- **No communication with any third party.** No telemetry, no crash reporting, no usage statistics, no update checker, no ads, no account system. The program sends nothing to the author's server — because **no such server exists**, and the code contains no such logic. The next item is the only network activity, and its destination is your own device.
- **The only network request: asking your own panel for an icon.** The panel title bar shows the site's favicon, so the program sends a single GET to **the panel address you typed yourself** to fetch its icon or manifest. The header is `User-Agent: PanelDock/1.0`. This is the only non-page request that ever leaves the machine, and it goes to the router in your own LAN. Related: icons on intranet or self-signed-certificate sites sometimes cannot be fetched, and the title bar then falls back to an initial-letter colour tile — that is a fetch failure, not a bug.
- **Passwords are not saved by this program — it has no way to.** "Save login password" is a **built-in WebView2 capability**, unrelated to this program: WebView2 encrypts the password into that tab's own profile directory, and this program does not read it, export it, forward it, or expose any interface that could obtain it (see "Features"). To leave no password behind at all, set the group to "clear on close" or hit "Reset data".
- **Everything else stays on this machine.** Locations in "Data locations" above; nothing is uploaded, synced, or sent anywhere.
- **Nothing is injected into the page.** Panel windows inject zero scripts and zero styles; they neither read nor modify page content.

## About "clear browser state on close"

Each panel has a "browser state after closing" setting (a radio group in the panel editor, plus a "clear on close" shortcut switch on the card):

- **Keep state (default)**: cookies, login state, cache and local storage all stay on disk, so the next open resumes the previous session. Right for router back-ends where you want to stay logged in.
- **Clear on close**: closing the panel deletes the WebView2 data directories of all its tabs, so the next open is a fresh environment (you log in again, and page cache and site preferences are gone too). Right for borrowed machines, shared machines, or when you want no trace left.

Two details:

1. **It also clears before opening.** Clearing does not happen only at close — there is a pass before opening as well. If the process was killed, the power went out, or the browser process had not fully exited at close time, whatever was left over gets wiped at the next open, so "every open is a fresh environment" actually holds.
2. **It affects only this one panel.** Every tab is already an independent profile, so other panels' login sessions are completely unaffected. This setting governs "on close" and is a different action from "reset data" and "delete panel": clear-on-close fires only at the moment of closing, reset can be done any time (keeps the panel, clears the data), and delete removes the panel along with the data.

## About "reset data"

"Reset data" on a card is the on-demand counterpart of "clear on close": without waiting for a close and without changing any setting, one click immediately deletes the WebView2 data directories of every tab in that group. Right for "someone else is taking over this machine", "I just logged into the wrong account", or "leave no trace but keep the panel".

What it clears: cookies, login state, cache, local storage, and **passwords saved by WebView2** (that is the division of labour with the "save password" switch on the card — that one only decides whether new ones get saved, it cannot remove existing ones).
What it leaves alone: panel name, address, tabs, desktop shortcut, window position, and every setting (always-on-top, session, password, …) — what gets reset is the data, not the panel.

Three things to know up front:

1. **It cannot be undone**, so the button always raises a confirmation dialog, and the default focus is on "Cancel" (pressing Enter does not execute it).
2. **If the panel is open, its window is closed first.** The data directory is held by the browser process; leaving it open means the delete is incomplete — and "incomplete" produces no warning at all, only a login state that survived. After clearing, the panel **does not reopen automatically**; click "Open" on the card when you want it, and it will be a fresh environment.
3. **If it cannot clear, it reports an error** (a browser process may still hold the files; retry later) instead of pretending to succeed.

Both "reset data" and "delete panel" clear browser data; the only difference is the panel itself: **reset keeps the panel** (address, tabs, shortcut and settings all stay, data goes to zero), **delete removes the panel too**. Reset when you want a clean panel to keep using; delete when you no longer need the group.

## About "opening a disabled panel from a shortcut"

Disabling a panel ("Disable" on the card) only prevents it from being opened; its shortcut and taskbar icon still point at it. Double-clicking the shortcut now raises a dialog:

- **Enable and open** (default, selected by pressing Enter): sets the panel enabled and opens it immediately — the enabled state is written to config, so it won't ask again next time;
- **Keep disabled**: do nothing, same as the old behaviour.

Two things worth knowing:

1. **There is no "remember my choice".** Enabling is a deliberate decision; making it "auto-enable from now on" would defeat your intent to disable it, which would make disabling meaningless — so it asks every time.
2. **Choosing "keep disabled" pops up no other window** (the manager does not appear). A lightweight-launched process stays in the system tray; open the manager from there when you need it.

## About "pin to taskbar"

Since Windows 10 an app may no longer pin its own icon to the taskbar (Microsoft's position: pinning is a user preference and programs should not decide it), and Windows 11 closed the last remaining bypass. So PanelDock's "Pin to taskbar" button does this:

1. Prepares the shortcut for that panel;
2. Shows an explanatory window telling you why one-click pinning is impossible and which clicks to make next — **it opens no other window at this point**;
3. Once you have read it and click "Select shortcut", Explorer opens with the shortcut selected;
4. You right-click and choose "Pin to taskbar" (on Windows 11, click "Show more options" first).

If the panel is already on the taskbar, the program tells you so and stops there. Step 3 needs your click because opening Explorer while the dialog is up would steal the foreground — you would be switched away before finishing the text.

Why not just force it in by restarting Explorer or similar: that makes the whole taskbar vanish and rebuild, reloads every tray icon, and risks overwriting the taskbar layout you arranged — not worth it for one icon.

## Command line

```powershell
PanelDock.exe                 # open the manager
PanelDock.exe --open <panelID> # lightweight launch: open that panel directly (used by desktop shortcuts)
```

The entire command surface is one parameter, `--open`, taking a **panel ID**. For "direct to a single tab", create a panel containing one tab — tab-level parameters (the old `--tag-id` / `--tag` design) were dropped.

## Roadmap (see 方案讨论记录.md)

- ~~Configuration import / export / backup~~ (done, 2026-10-01: config.json only, see "Features"; icons and login data deliberately excluded).
