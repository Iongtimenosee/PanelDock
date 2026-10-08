# PanelDock

A lightweight Windows desktop tool that gives every back-end page you want to glance at any second its own window — router consoles, NAS, OpenClash / Zashboard, ops dashboards, status monitors, e-commerce back-ends. Anything you open just to check that things are still fine: one window and one isolated session per panel, and no more browser tabs piling up.

*English · [简体中文](README.md)*

## Why use this instead of a browser tab

A browser is a machine that can do anything. The catch: **you want one feature, and you pay for the whole machine.** PanelDock keeps only the last step:

- **It saves memory.** A panel *is* a WebView2, paying only for that one page. Open the same router page in a browser tab versus in PanelDock and the difference in resident memory is **on the order of several times** — and the simpler the page, the bigger the gap.
- **It saves CPU.** No extension can wake itself up, no sync is polling, no update checker, no telemetry. Close a panel window and it stops consuming anything at all.
- **It's faster.** Double-click a desktop shortcut and you land directly on the target page — no browser shell cold-start in between.
- **It's safer.** Panel windows **inject no script and expose no bridge**, so a page gets zero local capability.
- **It's genuinely portable.** Unzip and run, nothing written to the registry. **Create an empty `data` folder next to the exe** and you switch to portable mode; copy that one folder and you have migrated (see "Data locations").

## Features

- Panel management (create / edit / delete / enable-disable / **reset data**), stored in `%APPDATA%\PanelDock\config.json`.
- Each panel supports multiple tabs; tabs can be added, removed and **reordered** (the order is the order shown in the tab bar), and **each tab uses its own WebView2 profile**, so login sessions never interfere. One tab makes a single-tab panel; several tabs make a group.
- Panel windows are independent native Win32 windows with a native tab bar and **inject nothing into the page and expose no bridge**.
- One-click desktop shortcut creation (`--open <panelID>` launches straight into it); **renaming a panel renames its desktop shortcut too**; "Pin to taskbar" prepares the shortcut and walks you through it — you right-click to pin yourself (Windows does not let an app pin itself).
- Always-on-top (**click the pin directly in the panel title bar**) and remembered window position and size; a resident tray icon, and **all tray behaviour lives in "App settings":** a single "when closing a panel window: ask / minimize to tray / close directly" — set it to "minimize to tray" and **the tray icon is forced on** (closed panels hide in the tray and the icon is the only way to get them back).
- **Closing a window only ever closes that window.** Closing a panel closes only that panel; the manager window's action is fixed (a tray icon or another window means it just closes itself, otherwise it exits). Plus "in lightweight mode, quit after the last panel closes" (on by default).
- **Double-clicking the tray icon is a toggle:** it brings the same window back and sends it away again, no need to put it back on the desktop and click X first; the right-click menu lists every panel hidden in the tray, click a name to get it back, while the "Close a window for good" submenu below actually closes one. In lightweight mode **as long as you have not operated the tray yet** every close closes for real, however many panels are open — the process is about to shut down anyway and a hidden window would never get another chance; once you restore one, it stays in the tray as usual.
- **Light / dark theme** and **中文 / English** UI language, three options each (`auto` follows the system). **Only the shell changes, never the page** — panels show somebody else's web page and this tool injects no style or script.
- **Export / import configuration** (migrating machines or backups): config.json only, no icon cache and no login state. The current config is auto-backed-up to `config.json.bak`, and an invalid import file leaves your existing config untouched.
- **Post-close browser state is configurable**: "keep state" by default; "clear on close" deletes the browser data of all the panel's tabs on close, so the next open is a completely fresh environment (with one more cleanup pass before opening).
- **Opening a disabled panel from a shortcut asks you first**: "enable and open" or "keep disabled" — double-clicking no longer does nothing.
- **Login passwords can be saved** (per-group switch, on by default): encrypted by WebView2 into that group's browser data — **this tool neither reads it nor exports it**.
- **One-click "reset data" per group**: immediately clears cookies, login state, cache and local storage (**including saved passwords**) while keeping the panel itself.
- **Deleting a group clears its data too** — the only difference from "reset data" is whether the panel survives.

## Installation

Both packages contain exactly the same thing — pick either. Both need the **WebView2 Runtime** (Windows 10 / 11 usually ship with it; if startup reports it missing, install Microsoft's Evergreen Runtime once).

**Installer (recommended)**: double-click `PanelDock-windows-x64-setup.exe`. It installs to `%LOCALAPPDATA%\Programs\PanelDock` with **no administrator rights**, gives you a Start Menu shortcut and a normal uninstall entry.

**Portable**: unzip `PanelDock-windows-amd64.zip` anywhere and double-click `PanelDock.exe`. No installer, nothing written to the registry; delete the folder to uninstall.

> **Heads up on uninstalling**: the installer also removes `%APPDATA%\PanelDock` (config) and `%LOCALAPPDATA%\PanelDock` (per-tab login sessions). To keep your panel list, use "Export config" before uninstalling.

Every release ships a `SHA256SUMS.txt`:

```powershell
Get-FileHash .\PanelDock-windows-x64-setup.exe -Algorithm SHA256
```

### First run: Windows SmartScreen will block once

The program **has no code-signing certificate** (not worth it for a free open-source tool), so Windows shows one "unrecognised app" prompt. This is not a virus warning — it is Microsoft's uniform notice for every unsigned new program, and **unblocking after download makes it go away**:

```powershell
Unblock-File -Path .\PanelDock-windows-x64-setup.exe
Unblock-File -Path .\PanelDock-windows-amd64.zip
```

Or right-click the exe → Properties → tick "Unblock" at the bottom. A free signing service for open-source projects (SignPath Foundation) has been applied for; once approved, later releases will be signed.

## Data locations

| Content | Path |
|---|---|
| Configuration | `%APPDATA%\PanelDock\config.json` |
| Per-tab login sessions | `%LOCALAPPDATA%\PanelDock\WebViewProfiles\<tabID>` |

### Portable mode

If a `data` folder exists next to the exe at startup (**an empty one is enough** — same convention as VSCode), both items above move under `<exe dir>\data\`. Copy the whole "exe + data" folder and you have migrated. Without it you get the system directories; the two are independent and never interfere. Note that a portable build unzipped from the archive has **no** `data` folder by default — create one if you want to carry it around.

**Deleting a panel clears its data too**: the WebView2 data directory of every tab is removed wholesale (cookies, login state, cache, local storage, saved passwords). The directory name *is* the tab ID and a new panel gets new IDs, so **data cannot be read back by re-adding the panel afterwards**. If cleanup fails it reports the error and aborts — you never get "panel gone, data still there". Icons already pinned to the taskbar are left untouched.

## Privacy

This tool handles the most sensitive login pages you have, so here it is plainly: **it talks to no third party, and it does not store your passwords.**

- **Open source and verifiable.** MIT licensed, sources public; five minutes of reading code or capturing packets checks every claim below.
- **It talks to no third party.** No telemetry, crash reporting, usage stats, update checker, ads or account system — **no such server exists**, and there is no such logic in the code.
- **The only network request: asking your own panel for an icon.** The title bar shows the favicon, so it sends one GET to **the address you typed yourself** (`User-Agent: PanelDock/1.0`). Intranet and self-signed sites often return nothing, in which case the title bar falls back to an initial-letter tile — that is a miss, not a breakage.
- **Passwords are not stored by this program.** Saving them is a **WebView2 built-in**: it encrypts them into that tab's profile, and this program never reads or exports them and has no interface that could.
- **Everything else stays on this machine**, at the paths above: not transmitted, not synced, not uploaded.
- **Nothing is injected into the page** — it neither reads nor modifies page content.

## A few behaviours in detail

### Clearing browser state on close

- **Keep state (default)**: cookies, login state, cache and local storage stay on disk, so the next open resumes the previous session.
- **Clear on close**: the data directories of all the panel's tabs are deleted on close. **There is one more cleanup pass before opening**, so leftovers from a crash, a forced kill or a browser that had not exited yet are wiped too — "every open is a fresh environment" actually holds.

Only that one panel is affected; other panels' sessions are untouched.

### Reset data

"Reset data" on a card is the on-demand counterpart of "clear on close": no waiting for a close, no settings changed, one click and it clears (including passwords WebView2 has already saved). It does **not** touch the panel name, address, tabs, shortcut, window position or any setting — this resets data, not the panel.

**It cannot be undone**, so it always asks for confirmation with focus on "Cancel"; **if the panel is open, its window closes first** (a directory held open by the browser process gives you a silent partial delete) and it does not reopen afterwards; **failure is reported**, never faked as success.

### Opening a disabled panel from a shortcut

Disabling only prevents a panel from being opened; its shortcut and taskbar icon still point at it. Double-clicking the shortcut now asks you: **enable and open** (default, Enter selects it, written to config) or **keep disabled** (do nothing).

**There is no "remember my choice"** — remembering "always enable from now on" would quietly override your decision to disable it. Choosing "keep disabled" pops nothing else; a lightweight launch stays in the tray, and you open the manager from there when you need it.

### Pinning to the taskbar

Since Windows 10 an app may not pin itself to the taskbar (Microsoft's position: pinning is a user preference), and Windows 11 closed the last workarounds. So the button does this:

It prepares the panel's shortcut → shows an explanation (**opening nothing on your behalf**) → you click "select the shortcut" and only then does Explorer open with it selected → you right-click and choose "Pin to taskbar" (on Windows 11, "Show more options" first).

If the panel is already pinned it simply tells you. Step 3 is yours to click because stealing the foreground while the dialog is up would cover the text you have not read yet. Why not write it in by restarting Explorer: the whole taskbar disappears and rebuilds, every tray icon reloads, and you risk overwriting the layout you arranged — not worth one icon.

## Command line

```powershell
PanelDock.exe                   # open the manager
PanelDock.exe --open <panelID>  # lightweight launch: open that panel directly (this is what shortcuts use)
```

`--open` is the only parameter and it takes a **panel ID**. For "jump straight to one tab", create a panel that contains just that one tab.

## Development

Requirements: Go 1.25+ (per go.mod), Wails CLI v2, Node.js, WebView2 Runtime.

```powershell
wails dev     # dev mode
wails build   # produces build/bin/PanelDock.exe
```

If Wails cannot find `npm` when launched from Git Bash, run `wails build` from `cmd.exe` instead. Regression baseline: `go vet ./...` and `go test ./...` must both pass.

Docs for whoever picks up the code (or an AI):

- `AGENTS.md` — entry point: hard boundaries, mental model, file map, doc index.
- `docs/architecture.md` — runtime mechanics (process lifetime, close paths, single instance, config and settings, data locations).
- `docs/behavior.md` — behaviour contract cheat-sheet; `docs/pitfalls.md` — pitfalls (scan it before touching drawing, Win32, COM, WebView2 vtables or icon encoding).
- `docs/doc-policy.md` — how docs and comments are written (including the anti-bloat checks).
