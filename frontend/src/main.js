import './style.css';
import './app.css';

import { EventsOn } from '../wailsjs/runtime/runtime';

import {
  ApplyPanelIcon,
  ApplyWindowTheme,
  CleanOrphanProfiles,
  CreatePanel,
  CreatePanelShortcut,
  DeletePanel,
  ExportConfig,
  GetPanelTabs,
  GetSettings,
  ImportConfig,
  InspectPanelIcon,
  InspectPanelShortcut,
  ListOrphanProfiles,
  ListPanelShortcuts,
  ListPanels,
  OpenPanel,
  PinPanelToTaskbar,
  ResetPanelData,
  RevealPanelShortcut,
  SetLanguage,
  SetLightweightQuitOnLastPanel,
  SetPanelAlwaysOnTop,
  SetPanelCloseAction,
  SetPanelPasswordAutosave,
  SetPanelSessionMode,
  SetShowTrayIcon,
  SetTheme,
  UpdatePanel,
  UpdatePanelTabs,
} from '../wailsjs/go/main/App';

import { getLocale, initLocale, resolveLocale, t, tErr } from './i18n/index.js';

// ─── 界面语言（i18n）───────────────────────────────────────────────────────
// 词典编译进产物（src/i18n/），运行时无外挂语言文件。语言设置（settings.language，
// config.json 唯一事实源）镜像进 localStorage，供**首帧渲染前**确定语言 ——
// main.js 的模板在模块加载时就渲染，而 Wails 绑定（GetSettings）那时还不可用，
// 不镜像的话英文用户每次启动都先看到一屏中文再闪成英文（与主题防闪同一套路）。
const LANGUAGE_STORAGE_KEY = 'paneldock.language';
let languageSetting = 'auto';
try {
  languageSetting = localStorage.getItem(LANGUAGE_STORAGE_KEY) || 'auto';
} catch (e) {
  // 隐私模式等读不到：按「跟随系统」走，只损失一次镜像同步。
}
initLocale(languageSetting);

// 注意：Wails 传给前端的数据字段名遵循 Go 结构体的 json tag（小写驼峰），
// 例如 panel.id / panel.tabs / tab.url，而不是 Go 字段名 panel.ID / panel.Tabs。
document.querySelector('#app').innerHTML = `
  <div class="shell">
    <header class="topbar">
      <div class="topbar-title">
        <p class="eyebrow">${t('app.eyebrow')}</p>
        <h1>PanelDock</h1>
        <p class="subtitle">${t('app.subtitle')}</p>
      </div>
      <div class="topbar-actions">
        <!-- 明暗切换：太阳/月亮两个 SVG 都写在这里，显示哪侧由 CSS 按 <html data-theme> 决定，
             JS 只负责改属性和落盘，不碰图标。 -->
        <button id="theme-toggle" class="theme-toggle" type="button" aria-label="${t('theme.toDark')}">
          <svg class="icon-moon" viewBox="0 0 24 24" aria-hidden="true"><path d="M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z"/></svg>
          <svg class="icon-sun" viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>
        </button>
      </div>
    </header>

    <main class="workspace">
      <section class="settings-section" aria-labelledby="settings-title">
        <div class="section-head">
          <div>
            <p class="section-label">${t('settings.sectionLabel')}</p>
            <h2 id="settings-title">${t('settings.generalTitle')}</h2>
          </div>
        </div>

        <div class="settings-card">
          <label class="check-line">
            <input id="st-show-tray" type="checkbox" />
            ${t('settings.trayLabel')}
          </label>
          <p class="settings-hint">
            ${t('settings.trayHint')}
          </p>
          <p id="st-tray-forced" class="settings-hint is-warn" hidden>
            ${t('settings.trayForced')}
          </p>

          <div class="form-row settings-row">
            <label for="st-theme">${t('settings.themeLabel')}</label>
            <select id="st-theme">
              <option value="auto">${t('settings.themeAuto')}</option>
              <option value="light">${t('settings.themeLight')}</option>
              <option value="dark">${t('settings.themeDark')}</option>
            </select>
          </div>
          <p class="settings-hint">
            ${t('settings.themeHint')}
          </p>

          <div class="form-row settings-row">
            <label for="st-language">${t('settings.languageLabel')}</label>
            <select id="st-language">
              <option value="auto">${t('settings.languageAuto')}</option>
              <option value="zh-CN">${t('settings.languageZhCN')}</option>
              <option value="en-US">${t('settings.languageEnUS')}</option>
            </select>
          </div>
          <p class="settings-hint">
            ${t('settings.languageHint')}
          </p>

          <div class="form-row settings-row">
            <label for="st-panel-close-action">${t('settings.closeActionLabel')}</label>
            <select id="st-panel-close-action">
              <option value="ask">${t('settings.closeActionAsk')}</option>
              <option value="tray">${t('settings.closeActionTray')}</option>
              <option value="close">${t('settings.closeActionClose')}</option>
            </select>
          </div>
          <p class="settings-hint">
            ${t('settings.closeActionHint')}
          </p>

          <label class="check-line" title="${t('settings.lightweightTitle')}">
            <input id="st-lightweight-quit" type="checkbox" />
            ${t('settings.lightweightLabel')}
          </label>
          <p class="settings-hint">
            ${t('settings.lightweightHint')}
          </p>

          <div class="form-row settings-row">
            <label>${t('settings.configLabel')}</label>
            <div class="settings-row-actions">
              <button id="st-export-config" type="button" class="btn-sm">${t('settings.configExport')}</button>
              <button id="st-import-config" type="button" class="btn-sm">${t('settings.configImport')}</button>
            </div>
          </div>
          <p class="settings-hint">
            ${t('settings.configHint')}
          </p>
        </div>
      </section>

      <section class="panels-section" aria-labelledby="panels-title">
        <div class="section-head">
          <div>
            <p class="section-label">${t('panels.sectionLabel')}</p>
            <h2 id="panels-title">${t('panels.sectionTitle')}</h2>
          </div>
          <button id="add-panel-btn" type="button">${t('panels.add')}</button>
        </div>

        <form id="panel-form" class="panel-form" hidden>
          <input type="hidden" id="pf-id" />
          <div class="form-row">
            <label for="pf-name">${t('form.nameLabel')}</label>
            <input id="pf-name" type="text" maxlength="64" placeholder="${t('form.namePlaceholder')}" required />
          </div>

          <div class="tabs-section">
            <div class="tabs-section-head">
              <label>${t('form.tabsLabel')}</label>
              <button type="button" id="pf-add-tab" class="btn-sm">${t('form.addTab')}</button>
            </div>
            <!-- 地址填写的硬性风险提示：每个标签的地址都在这里填，因此放在列表上方，
                 用户动手之前就能看到（远程明文 HTTP 会把凭据暴露在链路上）。 -->
            <p class="form-alert">${t('form.httpWarning')}</p>
            <div id="pf-tabs" class="pf-tabs"></div>
          </div>
          <p class="form-hint">
            ${t('form.tabsHint')}
          </p>

          <div class="session-section">
            <div class="tabs-section-head">
              <label>${t('form.sessionLabel')}</label>
            </div>
            <label class="radio-line">
              <input type="radio" name="pf-session" value="persist" checked />
              <span class="radio-title">${t('form.sessionPersistTitle')}</span>
              <span class="radio-desc">${t('form.sessionPersistDesc')}</span>
            </label>
            <label class="radio-line">
              <input type="radio" name="pf-session" value="fresh" />
              <span class="radio-title">${t('form.sessionFreshTitle')}</span>
              <span class="radio-desc">${t('form.sessionFreshDesc')}</span>
            </label>
            <p class="pf-tabs-hint">
              ${t('form.sessionHint')}
            </p>
          </div>

          <div class="choice-section">
            <div class="tabs-section-head">
              <label>${t('form.passwordLabel')}</label>
            </div>
            <label class="check-line" title="${t('form.passwordTitle')}">
              <input id="pf-password" type="checkbox" checked />
              ${t('form.passwordCheck')}
            </label>
            <p class="pf-tabs-hint">
              ${t('form.passwordHint')}
            </p>
          </div>

          <div class="form-row form-row-inline">
            <label class="check-line">
              <input id="pf-enabled" type="checkbox" checked />
              ${t('form.enabledCheck')}
            </label>
          </div>
          <div class="form-actions">
            <button type="submit" class="primary">${t('form.save')}</button>
            <button type="button" id="pf-cancel">${t('form.cancel')}</button>
          </div>
        </form>

        <div id="orphan-banner" class="orphan-banner" hidden>
          <div class="orphan-banner-text">
            <strong id="orphan-title"></strong>
            <p class="settings-hint">
              ${t('orphan.hint')}
            </p>
          </div>
          <button id="orphan-clean-btn" type="button" class="warn">${t('orphan.clean')}</button>
        </div>

        <div id="panel-list" class="panel-list"></div>
        <p id="empty-hint" class="empty-hint" hidden>${t('empty.hint')}</p>
      </section>
    </main>

    <footer class="footnote">
      ${t('footer.text')}
    </footer>
  </div>

  <dialog id="delete-dialog" class="confirm-dialog" aria-labelledby="dd-title">
    <form method="dialog" class="confirm-form">
      <p class="section-label">${t('del.sectionLabel')}</p>
      <h2 id="dd-title"></h2>
      <p id="dd-message" class="confirm-message"></p>

      <p id="dd-warning" class="confirm-warning"></p>

      <div class="confirm-option">
        <label class="check-line">
          <input id="dd-remove-shortcuts" type="checkbox" checked />
          ${t('del.removeShortcuts')}
        </label>
        <p id="dd-shortcut-hint" class="confirm-option-hint"></p>
        <ul id="dd-shortcut-list" class="shortcut-list" hidden></ul>
      </div>

      <p id="dd-footnote" class="confirm-footnote"></p>

      <div class="confirm-actions">
        <button id="dd-cancel" value="cancel">${t('del.cancel')}</button>
        <button id="dd-confirm" class="danger-solid" value="confirm">${t('del.confirm')}</button>
      </div>
    </form>
  </dialog>

  <dialog id="reset-dialog" class="confirm-dialog" aria-labelledby="rs-title">
    <form method="dialog" class="confirm-form">
      <p class="section-label">${t('rs.sectionLabel')}</p>
      <h2 id="rs-title"></h2>
      <p id="rs-message" class="confirm-message"></p>

      <p id="rs-footnote" class="confirm-footnote"></p>

      <div class="confirm-actions">
        <button id="rs-cancel" value="cancel">${t('rs.cancel')}</button>
        <button id="rs-confirm" class="danger-solid" value="confirm">${t('rs.confirm')}</button>
      </div>
    </form>
  </dialog>

  <dialog id="pin-dialog" class="confirm-dialog" aria-labelledby="pt-title">
    <form method="dialog" class="confirm-form">
      <p class="section-label">${t('pin.sectionLabel')}</p>
      <h2 id="pt-title"></h2>
      <p id="pt-message" class="confirm-message"></p>

      <div class="confirm-option">
        <p class="confirm-option-hint">${t('pin.readyLabel')}</p>
        <ul id="pt-shortcut-list" class="shortcut-list"></ul>
      </div>

      <ol class="pin-steps">
        <li>${t('pin.step1')}</li>
        <li>${t('pin.step2')}</li>
        <li>${t('pin.step3')}</li>
      </ol>

      <p id="pt-footnote" class="confirm-footnote"></p>

      <div class="confirm-actions">
        <button id="pt-close" value="close">${t('pin.close')}</button>
        <button type="button" id="pt-reveal" class="primary">${t('pin.reveal')}</button>
      </div>
    </form>
  </dialog>

  <dialog id="icon-dialog" class="confirm-dialog" aria-labelledby="ic-title">
    <form method="dialog" class="confirm-form">
      <p class="section-label">${t('ic.sectionLabel')}</p>
      <h2 id="ic-title"></h2>
      <p id="ic-message" class="confirm-message"></p>

      <div class="icon-preview-row">
        <img id="ic-preview" class="icon-preview" alt="${t('ic.previewAlt')}" />
        <div class="icon-preview-meta">
          <p id="ic-source" class="confirm-option-hint"></p>
          <p id="ic-sizes" class="confirm-option-hint"></p>
        </div>
      </div>

      <div class="confirm-option">
        <p id="ic-shortcut-hint" class="confirm-option-hint"></p>
        <ul id="ic-shortcut-list" class="shortcut-list"></ul>
      </div>

      <p id="ic-footnote" class="confirm-footnote"></p>

      <div class="confirm-actions">
        <button id="ic-cancel" value="cancel">${t('ic.cancel')}</button>
        <button type="button" id="ic-apply" class="primary">${t('ic.apply')}</button>
      </div>
    </form>
  </dialog>
`;

const addPanelBtn = document.querySelector('#add-panel-btn');
const panelForm = document.querySelector('#panel-form');
const pfId = document.querySelector('#pf-id');
const pfName = document.querySelector('#pf-name');
const pfEnabled = document.querySelector('#pf-enabled');
const pfPassword = document.querySelector('#pf-password');
const pfCancel = document.querySelector('#pf-cancel');
const pfAddTab = document.querySelector('#pf-add-tab');
const pfTabs = document.querySelector('#pf-tabs');
const panelListEl = document.querySelector('#panel-list');
const emptyHint = document.querySelector('#empty-hint');
const orphanBanner = document.querySelector('#orphan-banner');
const orphanTitle = document.querySelector('#orphan-title');
const orphanCleanBtn = document.querySelector('#orphan-clean-btn');
const stShowTray = document.querySelector('#st-show-tray');
const stPanelCloseAction = document.querySelector('#st-panel-close-action');
const stLightweightQuit = document.querySelector('#st-lightweight-quit');
const stTrayForced = document.querySelector('#st-tray-forced');
const stExportConfig = document.querySelector('#st-export-config');
const stImportConfig = document.querySelector('#st-import-config');
const themeToggle = document.querySelector('#theme-toggle');
const stTheme = document.querySelector('#st-theme');
const stLanguage = document.querySelector('#st-language');
const deleteDialog = document.querySelector('#delete-dialog');
const ddTitle = document.querySelector('#dd-title');
const ddMessage = document.querySelector('#dd-message');
const ddWarning = document.querySelector('#dd-warning');
const ddRemoveShortcuts = document.querySelector('#dd-remove-shortcuts');
const ddShortcutHint = document.querySelector('#dd-shortcut-hint');
const ddShortcutList = document.querySelector('#dd-shortcut-list');
const ddFootnote = document.querySelector('#dd-footnote');
const ddCancel = document.querySelector('#dd-cancel');
const resetDialog = document.querySelector('#reset-dialog');
const rsTitle = document.querySelector('#rs-title');
const rsMessage = document.querySelector('#rs-message');
const rsFootnote = document.querySelector('#rs-footnote');
const rsCancel = document.querySelector('#rs-cancel');
const pinDialog = document.querySelector('#pin-dialog');
const ptTitle = document.querySelector('#pt-title');
const ptMessage = document.querySelector('#pt-message');
const ptShortcutList = document.querySelector('#pt-shortcut-list');
const ptFootnote = document.querySelector('#pt-footnote');
const ptReveal = document.querySelector('#pt-reveal');
const iconDialog = document.querySelector('#icon-dialog');
const icTitle = document.querySelector('#ic-title');
const icMessage = document.querySelector('#ic-message');
const icPreview = document.querySelector('#ic-preview');
const icSource = document.querySelector('#ic-source');
const icSizes = document.querySelector('#ic-sizes');
const icShortcutHint = document.querySelector('#ic-shortcut-hint');
const icShortcutList = document.querySelector('#ic-shortcut-list');
const icFootnote = document.querySelector('#ic-footnote');
const icApply = document.querySelector('#ic-apply');

let panels = [];
let formTabs = []; // 当前表单中的标签列表

function esc(value) {
  return String(value).replace(/[&<>"']/g, (ch) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[ch]);
}

function formatPosition(panel) {
  const { x, y, width: w, height: h } = panel.window || {};
  if (!w || !h) return t('card.positionDefault');
  return t('card.position', { x, y, w, h });
}

function render() {
  panelListEl.innerHTML = '';
  emptyHint.hidden = panels.length > 0;

  for (const panel of panels) {
    const card = document.createElement('article');
    card.className = 'panel-card';
    card.dataset.id = panel.id;

    const tabsHTML = (panel.tabs || []).map((tab) =>
      `<span class="tab-chip" title="${esc(tab.url)}">${esc(tab.name)}</span>`
    ).join('');

    card.innerHTML = `
      <div class="panel-card-head">
        <div class="panel-title-row">
          <h3 class="panel-name">${esc(panel.name)}</h3>
          <span class="state-pill ${panel.enabled ? 'enabled' : 'disabled'}">${panel.enabled ? t('card.enabled') : t('card.disabled')}</span>
        </div>
        <div class="panel-card-actions">
          <label class="toggle" title="${t('card.topTitle')}">
            <input type="checkbox" data-action="top" ${panel.alwaysOnTop ? 'checked' : ''} />
            <span>${t('card.top')}</span>
          </label>
          <label class="toggle" title="${t('card.freshTitle')}">
            <input type="checkbox" data-action="fresh" ${panel.sessionMode === 'fresh' ? 'checked' : ''} />
            <span>${t('card.fresh')}</span>
          </label>
          <label class="toggle" title="${t('card.passwordTitle')}">
            <input type="checkbox" data-action="password" ${panel.passwordAutosave !== 'off' ? 'checked' : ''} />
            <span>${t('card.password')}</span>
          </label>
          <button class="ghost" data-action="open" title="${t('card.openTitle')}" ${!panel.enabled ? 'disabled' : ''}>${t('card.open')}</button>
          <button class="ghost" data-action="shortcut" title="${t('card.shortcutTitle')}">${t('card.shortcut')}</button>
          <button class="ghost" data-action="taskbar" title="${t('card.taskbarTitle')}">${t('card.taskbar')}</button>
          <button class="ghost" data-action="icon" title="${t('card.iconTitle')}">${t('card.icon')}</button>
          <button class="ghost" data-action="edit">${t('card.edit')}</button>
          <button class="warn" data-action="reset" title="${t('card.resetTitle')}">${t('card.reset')}</button>
          <button class="danger" data-action="delete" title="${t('card.deleteTitle')}">${t('card.delete')}</button>
        </div>
      </div>
      <div class="panel-tabs-row">${tabsHTML || `<span class="tab-chip empty">${t('card.noTabs')}</span>`}</div>
      <p class="panel-meta">${formatPosition(panel)} · ${t('card.metaTabs', { count: (panel.tabs || []).length })} · ${t('card.metaProfile')}</p>
    `;

    card.querySelectorAll('input[data-action]').forEach((input) => {
      input.addEventListener('change', () => {
        const on = input.checked;
        const action = input.dataset.action;
        if (action === 'top') {
          SetPanelAlwaysOnTop(panel.id, on).catch((e) => {
            input.checked = !on;
            alert(t('errors.topFailed', { err: tErr(e) }));
          });
        } else if (action === 'fresh') {
          SetPanelSessionMode(panel.id, on ? 'fresh' : 'persist').catch((e) => {
            input.checked = !on;
            alert(t('errors.sessionFailed', { err: tErr(e) }));
          });
        } else if (action === 'password') {
          SetPanelPasswordAutosave(panel.id, on ? 'on' : 'off').catch((e) => {
            input.checked = !on;
            alert(t('errors.passwordFailed', { err: tErr(e) }));
          });
        }
      });
    });

    card.querySelector('[data-action="open"]').addEventListener('click', async () => {
      const btn = card.querySelector('[data-action="open"]');
      btn.disabled = true;
      try {
        await OpenPanel(panel.id);
      } catch (e) {
        alert(t('errors.openFailed', { err: tErr(e) }));
      } finally {
        btn.disabled = false;
        await refresh();
      }
    });

    card.querySelector('[data-action="shortcut"]').addEventListener('click', async () => {
      const btn = card.querySelector('[data-action="shortcut"]');
      btn.disabled = true;
      try {
        // 一个分组桌面只留一份快捷方式：已经有了就先问一句再覆盖，
        // 而不是默默再堆一个「名字 (2).lnk」出来（用户分不清哪个是哪个）。
        const info = await InspectPanelShortcut(panel.id);
        if (info.exists) {
          const extra =
            (info.extra || []).length > 0
              ? t('shortcut.extraNote', { count: info.extra.length, list: info.extra.join('\n') })
              : '';
          const iconNote = info.iconPath
            ? t('shortcut.iconNoteExisting')
            : t('shortcut.iconNoteNone');
          if (!confirm(t('shortcut.confirmOverwrite', { name: panel.name, path: info.shortcuts[0], extra, iconNote }))) {
            return;
          }
        }
        const result = await CreatePanelShortcut(panel.id);
        const lines = [
          result.created
            ? t('shortcut.created', { path: result.path })
            : t('shortcut.overwritten', { path: result.path }),
          '',
          t('shortcut.openHint', { name: panel.name }),
        ];
        if ((result.removed || []).length > 0) {
          lines.push('', t('shortcut.removedNote', { count: result.removed.length }));
          for (const p of result.removed) lines.push(`· ${p}`);
        }
        alert(lines.join('\n'));
      } catch (e) {
        alert(t('errors.shortcutFailed', { err: tErr(e) }));
      } finally {
        btn.disabled = false;
      }
    });

    card.querySelector('[data-action="taskbar"]').addEventListener('click', async () => {
      const btn = card.querySelector('[data-action="taskbar"]');
      btn.disabled = true;
      try {
        const result = await PinPanelToTaskbar(panel.id);
        if (result.alreadyPinned) {
          alert(t('taskbar.alreadyPinned', { name: panel.name, path: result.shortcut }));
        } else {
          showPinDialog(panel, result);
        }
      } catch (e) {
        alert(t('errors.taskbarFailed', { err: tErr(e) }));
      } finally {
        btn.disabled = false;
      }
    });

    card.querySelector('[data-action="icon"]').addEventListener('click', async () => {
      const btn = card.querySelector('[data-action="icon"]');
      btn.disabled = true;
      try {
        // 先只做检查：能不能刷、会变成什么样、会动到哪几个快捷方式。
        // 真正落盘要用户在对话框里点「应用」—— 改的是他自己桌面上的东西。
        const preview = await InspectPanelIcon(panel.id);
        if (!preview.available) {
          // reason 是错误码（icon.reason.*），过 tErr 翻译；未知值原样显示。
          alert(t('icon.notAvailable', { name: panel.name, reason: tErr(preview.reason) }));
          return;
        }
        showIconDialog(panel, preview);
      } catch (e) {
        alert(t('errors.iconCheckFailed', { err: tErr(e) }));
      } finally {
        btn.disabled = false;
      }
    });

    card.querySelector('[data-action="edit"]').addEventListener('click', async () => {
      let tabs = panel.tabs || [];
      if (tabs.length === 0) {
        try {
          tabs = await GetPanelTabs(panel.id) || [];
        } catch {
          tabs = [];
        }
      }
      showForm({
        id: panel.id,
        name: panel.name,
        enabled: panel.enabled,
        sessionMode: panel.sessionMode,
        passwordAutosave: panel.passwordAutosave,
        tabs: tabs,
      });
    });

    card.querySelector('[data-action="reset"]').addEventListener('click', async () => {
      const btn = card.querySelector('[data-action="reset"]');
      if (!(await askResetPanel(panel))) return; // 取消

      btn.disabled = true;
      try {
        await ResetPanelData(panel.id);
        alert(t('reset.done', { name: panel.name }));
      } catch (e) {
        alert(t('errors.resetFailed', { err: tErr(e) }));
      } finally {
        btn.disabled = false;
      }
      await refresh();
    });

    card.querySelector('[data-action="delete"]').addEventListener('click', async () => {
      // 删除前先问后端：该面板在桌面上有哪些快捷方式。清单既用于在对话框里明确列出，
      // 也决定「同时删除快捷方式」可选项是否可用 —— 一个都没有就没有可清理的东西。
      let shortcuts = [];
      try {
        shortcuts = (await ListPanelShortcuts(panel.id)) || [];
      } catch {
        shortcuts = [];
      }

      const choice = await askDeletePanel(panel, shortcuts);
      if (!choice) return; // 取消

      try {
        await DeletePanel(panel.id, choice.removeShortcuts);
        if (!choice.removeShortcuts && shortcuts.length > 0) {
          alert(t('delete.doneKept', {
            name: panel.name,
            list: shortcuts.map((p) => `· ${p}`).join('\n'),
          }));
        }
      } catch (e) {
        alert(t('errors.deleteFailed', { err: tErr(e) }));
      }
      await refresh();
    });

    panelListEl.appendChild(card);
  }
}

// ─── 删除确认对话框 ────────────────────────────────────────────────────────────

// askDeletePanel 弹出删除确认对话框，返回 null（取消）或 { removeShortcuts }。
//
// 为什么不用 window.confirm：浏览器确认框放不下复选框，而本轮需求正是「清理快捷方式」
// 必须是用户显式勾选的结果。用应用内 <dialog>（showModal）还能顺带拿到 Esc 关闭、
// 焦点圈定、遮罩（::backdrop）这些模态行为，与主界面的视觉风格也保持一致。
//
// 可选项的默认与可用性：
//   - 有快捷方式 → 默认勾选（保持「删除面板即清理快捷方式」这一既有语义，想要保留得显式取消）；
//   - 没有快捷方式 → 禁用并置灰，避免给出一个无从执行的承诺。
//
// 数据清空这件事必须单独用醒目的一行讲出来（`#dd-warning` 红字加粗）：删除面板**连带**清掉
// 该分组所有标签的 WebView2 数据，其中包含已保存的登录密码 —— 混在普通说明里用户看不见，
// 而这条后果不可撤销。顺带解释「为什么重新添加也找不回来」：每个标签的 profile 目录名就是
// 它的标签 ID，新建面板会生成新的 ID，谁也无法再读到旧目录。
function askDeletePanel(panel, shortcuts) {
  const hasShortcuts = shortcuts.length > 0;

  ddTitle.textContent = t('del.title', { name: panel.name });
  ddMessage.textContent = t('del.message');
  ddWarning.textContent = t('del.warning');

  ddRemoveShortcuts.checked = hasShortcuts;
  ddRemoveShortcuts.disabled = !hasShortcuts;
  ddShortcutHint.textContent = hasShortcuts
    ? t('del.shortcutHintSome', { count: shortcuts.length })
    : t('del.shortcutHintNone');
  ddShortcutList.hidden = !hasShortcuts;
  ddShortcutList.innerHTML = shortcuts.map((p) => `<li title="${esc(p)}">${esc(p)}</li>`).join('');

  // 脚注收起两件次要的事：① 想「只清数据、保留面板」该走哪个入口（两者后果像、结果不同，
  // 必须指路，否则用户会用删除来代替重置）；② 任务栏固定项不归本工具管。
  ddFootnote.textContent = t('del.footnote');

  return new Promise((resolve) => {
    const onClose = () => {
      deleteDialog.removeEventListener('close', onClose);
      if (deleteDialog.returnValue !== 'confirm') {
        resolve(null);
        return;
      }
      resolve({ removeShortcuts: hasShortcuts && ddRemoveShortcuts.checked });
    };
    deleteDialog.addEventListener('close', onClose);
    deleteDialog.returnValue = ''; // 复位：上次的 'confirm' 不能被这次继承
    deleteDialog.showModal();
    // 聚焦「取消」：删除不可撤销，回车不该直接把面板删掉。
    ddCancel.focus();
  });
}

// ─── 重置面板数据的确认对话框 ──────────────────────────────────────────────────

// askResetPanel 弹出重置确认对话框，返回 true（确认）/ false（取消）。
//
// 这个动作和「删除面板」一样不可撤销，但后果容易被低估：面板还在列表里、勾选状态也没变，
// 用户很容易以为只是"清个缓存"，实际上连 WebView2 保存的登录密码一起没了。所以对话框
// 必须把「包括密码」和「配置不受影响」两件事同时写清楚 —— 前者是警告，后者是防止误以为删了面板。
//
// 面板正开着时后端会先关掉它的窗口再清（目录被浏览器进程占着，不关就删不干净），
// 这一点也要提前讲，否则用户会以为程序顺手把自己的界面关掉了。
function askResetPanel(panel) {
  const tabCount = (panel.tabs || []).length;

  rsTitle.textContent = t('rs.title', { name: panel.name });
  rsMessage.textContent =
    (tabCount > 1
      ? t('rs.messagePrefixMany', { count: tabCount })
      : t('rs.messagePrefixOne')) + t('rs.messageBody');
  // 措辞对「开着／没开」两种情形都成立：运行时状态已不在界面上展示，也没什么可对照的，
  // 而后端两种情况的行为本来就一致（开着就先关窗口再清）。
  rsFootnote.textContent = t('rs.footnote');

  return new Promise((resolve) => {
    const onClose = () => {
      resetDialog.removeEventListener('close', onClose);
      resolve(resetDialog.returnValue === 'confirm');
    };
    resetDialog.addEventListener('close', onClose);
    resetDialog.returnValue = ''; // 复位：上次的 'confirm' 不能被这次继承
    resetDialog.showModal();
    // 聚焦「取消」：数据清掉就回不来了，回车不该直接执行。
    rsCancel.focus();
  });
}

// ─── 固定到任务栏的引导对话框 ──────────────────────────────────────────────────

// 当前引导中的面板 ID，供「选中快捷方式」用。
let pinPanelID = '';

// setPinFootnote 设置引导对话框的脚注；warn 为真时用告警色（失败信息用）。
function setPinFootnote(text, warn) {
  ptFootnote.textContent = text;
  ptFootnote.classList.toggle('is-warn', !!warn);
}

// showPinDialog 说明为什么不能一键固定，并把用户该点的那几步讲清楚。
//
// 这里**不替用户打开资源管理器**（2026-09-30 调整）：弹框的同时把前台抢走，
// 用户还没读完就切过去了，属于擅作主张。选中快捷方式由用户读完点「选中快捷方式」触发。
// 也不装模作样地放一个「自动固定」按钮：Windows 不给这个能力（见 taskbar_windows.go）。
function showPinDialog(panel, result) {
  pinPanelID = panel.id;

  ptTitle.textContent = t('pin.title', { name: panel.name });
  ptMessage.textContent = t('pin.message');
  ptShortcutList.innerHTML = `<li title="${esc(result.shortcut)}">${esc(result.shortcut)}</li>`;
  setPinFootnote(t('pin.footnote'), false);

  pinDialog.returnValue = '';
  pinDialog.showModal();
  ptReveal.focus();
}

ptReveal.addEventListener('click', async () => {
  if (!pinPanelID) return;
  ptReveal.disabled = true;
  ptReveal.textContent = t('pin.working');
  try {
    await RevealPanelShortcut(pinPanelID);
    setPinFootnote(t('pin.revealOk'), false);
  } catch (e) {
    setPinFootnote(t('pin.revealFailed', { err: tErr(e) }), true);
  } finally {
    ptReveal.disabled = false;
    ptReveal.textContent = t('pin.reveal');
  }
});

// ─── 刷新图标：预览与应用的对话框 ──────────────────────────────────────────────

// 当前待应用图标的面板 ID。
let iconPanelID = '';

// 主按钮的文案。桌面没有快捷方式时是「创建快捷方式并应用」—— 那时这个动作确实会
// 顺手创建一份，只说「应用」会让人以为只是存个图标（用户 2026-09-30 的原话：
// 「当桌面没有快捷方式时，刷新图标后只是下载保存动作，这没啥意义」）。
let iconApplyLabel = t('ic.apply');

// setIconFootnote 设置对话框的脚注；warn 为真时用告警色（失败信息用）。
function setIconFootnote(text, warn) {
  icFootnote.textContent = text;
  icFootnote.classList.toggle('is-warn', !!warn);
}

// iconApplyMessage 把应用结果说清楚：创建/覆盖了哪一份、改成了哪几个、哪几个没成。
// 一律逐个列路径 —— 「成功」两个字盖不住「桌面那个改了、任务栏固定项没改成」。
function iconApplyMessage(panel, result) {
  const updated = result.updated || [];
  const failed = result.failed || [];
  const removed = result.removed || [];
  const lines = [t('ic.doneSaved', { name: panel.name }), result.iconPath, ''];

  if (updated.length > 0) {
    if (result.createdShortcut) {
      lines.push(t('ic.doneCreated'));
    } else {
      lines.push(t('ic.doneUpdated'));
    }
    for (const p of updated) lines.push(`· ${p}`);
    lines.push('', t('ic.doneF5'));
  } else {
    lines.push(t('ic.doneShortcutFailed'));
  }

  if (removed.length > 0) {
    lines.push('', t('ic.doneRemoved', { count: removed.length }));
    for (const p of removed) lines.push(`· ${p}`);
  }

  if (failed.length > 0) {
    lines.push('', t('ic.doneFailedCount', { count: failed.length }));
    for (const p of failed) lines.push(`· ${p}`);
  }
  return lines.join('\n');
}

// showIconDialog 把「会变成什么样、会动到哪几个文件」摆出来让用户点头。
//
// 这一步不做任何写操作。改写快捷方式图标动的是用户自己创建的东西，
// 必须先看清楚再点「应用」—— 所以预检（InspectPanelIcon）与应用（ApplyPanelIcon）
// 是两个分开的桥接方法，不是一个「点一下全做完」。
function showIconDialog(panel, preview) {
  iconPanelID = panel.id;

  const shortcuts = preview.shortcuts || [];
  const duplicates = preview.duplicates || [];
  const willCreate = !!preview.willCreateShortcut;

  icTitle.textContent = willCreate
    ? t('ic.titleWithCreate', { name: panel.name })
    : t('ic.title', { name: panel.name });
  icMessage.textContent = willCreate ? t('ic.messageWithCreate') : t('ic.message');

  // 把「这次会覆盖什么」逐条摆出来。图标只有一份、快捷方式也只有一份，
  // 覆盖是既有的东西被替换掉，必须让用户点之前就知道。
  const notes = [];
  notes.push(willCreate ? t('ic.willCreate') : t('ic.willOverwrite'));
  if (preview.alreadyCached) {
    notes.push(t('ic.cachedOverwrite'));
  }
  if (duplicates.length > 0) {
    notes.push(t('ic.duplicatesNote', { count: duplicates.length }));
  }
  icShortcutHint.innerHTML = notes.map((n) => esc(n)).join('<br>');

  icPreview.src = preview.preview;
  const tabName = preview.tabName || t('ic.firstTab');
  icSource.textContent = preview.host
    ? t('ic.sourceWithHost', { host: preview.host, tab: tabName })
    : t('ic.source', { tab: tabName });
  icSizes.textContent = t('ic.sizes', { sizes: (preview.sizes || []).join(' / ') });

  icShortcutList.innerHTML = shortcuts.map((p) => `<li title="${esc(p)}">${esc(p)}</li>`).join('');
  icShortcutList.hidden = shortcuts.length === 0;

  setIconFootnote(
    preview.alreadyCached
      ? t('ic.locationCached', { path: preview.iconPath })
      : t('ic.location', { path: preview.iconPath }),
    false,
  );

  iconApplyLabel = willCreate ? t('ic.applyCreate') : t('ic.apply');
  icApply.textContent = iconApplyLabel;

  iconDialog.returnValue = '';
  iconDialog.showModal();
  icApply.focus();
}

icApply.addEventListener('click', async () => {
  if (!iconPanelID) return;
  icApply.disabled = true;
  icApply.textContent = t('ic.working');
  setIconFootnote(t('ic.workingHint'), false);
  try {
    const result = await ApplyPanelIcon(iconPanelID);
    const panel = panels.find((p) => p.id === iconPanelID) || { name: '' };
    iconDialog.close();
    alert(iconApplyMessage(panel, result));
  } catch (e) {
    // 失败留在对话框里报，用户可以直接重试或取消 —— 关掉再弹 alert 会把上下文弄丢。
    setIconFootnote(t('ic.applyFailed', { err: tErr(e) }), true);
  } finally {
    icApply.disabled = false;
    icApply.textContent = iconApplyLabel;
  }
});

// ─── 表单标签管理 ──────────────────────────────────────────────────────────────

// 拖拽排序的瞬时状态：dragIndex 是按下把手的那一项，dropIndex 是当前落点（0..len，len 表示排到最后）。
let dragIndex = -1;
let dropIndex = -1;

// moveTab 把第 from 项搬到第 to 项的位置。
//
// 注意搬的是**整个标签对象**，tab.id 跟着一起走 —— id 决定 WebView2 profile 目录
// （`WebViewProfiles\<tabID>`），换了 id 就等于换了浏览器身份，登录会话会丢。
function moveTab(from, to) {
  const last = formTabs.length - 1;
  if (from < 0 || from > last || to < 0 || to > last || from === to) return;
  const [moved] = formTabs.splice(from, 1);
  formTabs.splice(to, 0, moved);
  renderFormTabs();
}

function clearDropHints() {
  pfTabs.querySelectorAll('.pf-tab-item').forEach((el) => {
    el.classList.remove('is-drop-before', 'is-drop-after', 'is-dragging');
  });
}

function renderFormTabs() {
  pfTabs.innerHTML = '';
  const sortable = formTabs.length > 1;

  formTabs.forEach((tab, index) => {
    const item = document.createElement('div');
    item.className = 'pf-tab-item';
    item.dataset.index = String(index);
    item.innerHTML = `
      <div class="pf-tab-row">
        ${sortable ? `<span class="pf-tab-order">${index + 1}</span>
        <span class="pf-tab-handle" draggable="true" title="${t('tab.dragTitle')}">⠿</span>` : ''}
        <input type="text" class="pf-tab-name" placeholder="${t('tab.namePlaceholder')}" value="${esc(tab.name)}" maxlength="64" />
        <input type="url" class="pf-tab-url" placeholder="http://..." value="${esc(tab.url)}" />
        ${sortable ? `<span class="pf-tab-move">
          <button type="button" data-move-tab="-1" title="${t('tab.moveUp')}" ${index === 0 ? 'disabled' : ''}>▲</button>
          <button type="button" data-move-tab="1" title="${t('tab.moveDown')}" ${index === formTabs.length - 1 ? 'disabled' : ''}>▼</button>
        </span>` : ''}
        ${sortable ? `<button type="button" class="btn-sm btn-danger-sm" data-remove-tab="${index}" title="${t('tab.remove')}">✕</button>` : ''}
      </div>
    `;
    pfTabs.appendChild(item);
  });

  if (sortable) {
    const hint = document.createElement('p');
    hint.className = 'pf-tabs-hint';
    hint.textContent = t('tab.orderHint');
    pfTabs.appendChild(hint);
  }

  // 绑定事件。
  pfTabs.querySelectorAll('.pf-tab-name').forEach((input, i) => {
    input.addEventListener('input', () => { formTabs[i].name = input.value; });
  });
  pfTabs.querySelectorAll('.pf-tab-url').forEach((input, i) => {
    input.addEventListener('input', () => { formTabs[i].url = input.value; });
  });
  pfTabs.querySelectorAll('[data-remove-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const idx = parseInt(btn.dataset.removeTab);
      // 已保存过的标签（有 ID）删除时会连带清掉它的浏览器数据 —— 新建分组时
      // 手滑加错的行（id 为空）还没产生任何数据，不用打扰。
      const tab = formTabs[idx];
      if (tab && tab.id) {
        const what = tab.name ? t('tab.nameQuote', { name: tab.name }) : t('tab.this');
        if (!confirm(t('tab.removeConfirm', { what }))) return;
      }
      formTabs.splice(idx, 1);
      renderFormTabs();
    });
  });
  pfTabs.querySelectorAll('[data-move-tab]').forEach((btn) => {
    btn.addEventListener('click', () => {
      const delta = parseInt(btn.dataset.moveTab);
      const idx = parseInt(btn.closest('.pf-tab-item').dataset.index);
      moveTab(idx, idx + delta);
    });
  });
  pfTabs.querySelectorAll('.pf-tab-handle').forEach((handle) => {
    handle.addEventListener('dragstart', (event) => {
      const item = handle.closest('.pf-tab-item');
      dragIndex = parseInt(item.dataset.index);
      dropIndex = -1;
      event.dataTransfer.effectAllowed = 'move';
      // 部分浏览器（含 WebView2 内核）不给 dataTransfer 设值就不启动拖拽。
      event.dataTransfer.setData('text/plain', String(dragIndex));
      // 默认拖影只有把手那么大，换成整行更好辨认。
      event.dataTransfer.setDragImage(item, 14, 14);
      item.classList.add('is-dragging');
    });

    handle.addEventListener('dragend', () => {
      dragIndex = -1;
      dropIndex = -1;
      clearDropHints();
    });
  });
}

// pfTabs 上的统一 dragover/drop：挂在容器而不是每一项上，是为了让「落在标签之间的缝隙」
// 也是有效落点；dragover 事件会从当前悬停的子元素冒泡到这里。
//
// 拖拽过程中**只画提示线、不重排 DOM**：重排会重建这些节点，浏览器会当场中止拖拽。
// 真正的移动发生在 drop 那一刻（firefox/webview2 都如此）。
pfTabs.addEventListener('dragover', (event) => {
  if (dragIndex < 0) return;
  event.preventDefault();
  event.dataTransfer.dropEffect = 'move';

  const items = Array.from(pfTabs.querySelectorAll('.pf-tab-item'));
  let target = items.length;
  for (let i = 0; i < items.length; i++) {
    const rect = items[i].getBoundingClientRect();
    if (event.clientY < rect.top + rect.height / 2) {
      target = i;
      break;
    }
  }
  dropIndex = target;

  // 落点就在原位置（自己前面或自己后面）时不画提示，避免「动了一下其实没动」的错觉。
  const noop = target === dragIndex || target === dragIndex + 1;
  items.forEach((el, i) => {
    el.classList.toggle('is-drop-before', !noop && i === target);
    el.classList.toggle('is-drop-after', !noop && i === target - 1);
  });
});

pfTabs.addEventListener('dragleave', (event) => {
  // 只在真正离开容器时清提示；在子元素之间移动不算离开。
  if (!pfTabs.contains(event.relatedTarget)) clearDropHints();
});

pfTabs.addEventListener('drop', (event) => {
  if (dragIndex < 0) return;
  event.preventDefault();
  const from = dragIndex;
  // 落点在自身之后时要减一：先摘掉自己，后面的下标才对齐。
  let to = dropIndex < 0 ? from : dropIndex;
  if (from < to) to -= 1;
  dragIndex = -1;
  dropIndex = -1;
  clearDropHints();
  moveTab(from, to);
});

// selectedSessionMode 读取表单里选中的会话处理方式（后端只认 'persist' / 'fresh'）。
// 表单里只有一个单选组，用 :checked 直接取值；理论上不会为空，兜底给默认值。
function selectedSessionMode() {
  const checked = panelForm.querySelector('input[name="pf-session"]:checked');
  return checked ? checked.value : 'persist';
}

// applySessionMode 把面板当前的会话处理方式回填到表单单选组。
// 空值（老配置没有这个字段）按「保留」渲染 —— 与后端 normalizeSessionMode 同一套语义。
function applySessionMode(panel) {
  const mode = panel && panel.sessionMode === 'fresh' ? 'fresh' : 'persist';
  panelForm.querySelectorAll('input[name="pf-session"]').forEach((radio) => {
    radio.checked = radio.value === mode;
  });
}

// applyPasswordAutosave 把面板当前的「保存登录密码」开关回填到表单勾选框。
// 空值（老配置没有这个字段）按「开启」渲染 —— 与后端 normalizePasswordAutosave 同一套语义；
// 新建面板时 panel 是空对象，同样落到「默认勾选」。
function applyPasswordAutosave(panel) {
  pfPassword.checked = !panel || panel.passwordAutosave !== 'off';
}

function showForm(panel) {
  panelForm.hidden = false;
  addPanelBtn.hidden = true;
  pfId.value = panel.id || '';
  pfName.value = panel.name || '';
  pfEnabled.checked = panel.enabled !== false;
  applySessionMode(panel);
  applyPasswordAutosave(panel);

  formTabs = panel.tabs && panel.tabs.length > 0
    ? panel.tabs.map((tab) => ({ id: tab.id || '', name: tab.name || '', url: tab.url || '' }))
    : [{ id: '', name: panel.name || '', url: '' }];
  renderFormTabs();
  pfName.focus();
}

function hideForm() {
  panelForm.hidden = true;
  addPanelBtn.hidden = false;
  panelForm.reset();
  pfId.value = '';
  formTabs = [];
  pfTabs.innerHTML = '';
}

pfAddTab.addEventListener('click', () => {
  formTabs.push({ id: '', name: '', url: '' });
  renderFormTabs();
  // 聚焦新增的标签名称输入框。
  const items = pfTabs.querySelectorAll('.pf-tab-item');
  if (items.length > 0) {
    items[items.length - 1].querySelector('.pf-tab-name').focus();
  }
});

async function submitForm(event) {
  event.preventDefault();
  const name = pfName.value.trim();
  const enabled = pfEnabled.checked;
  const id = pfId.value;

  // 验证所有标签。
  const validTabs = formTabs.filter((tab) => tab.name.trim() && tab.url.trim());
  if (validTabs.length === 0) {
    alert(t('form.needOneTab'));
    return;
  }

  // 后端按 json tag 小写字段名反序列化标签。
  const tabsPayload = validTabs.map((tab) => ({
    id: tab.id || '',
    name: tab.name.trim(),
    url: tab.url.trim(),
  }));

  const submitBtn = panelForm.querySelector('[type="submit"]');
  const sessionMode = selectedSessionMode();
  const passwordAutosave = pfPassword.checked ? 'on' : 'off';
  submitBtn.disabled = true;
  try {
    if (id) {
      // 更新模式：先更新面板基本信息，再整体替换标签列表（可增、删、改名）。
      await UpdatePanel(id, name, tabsPayload[0].url, enabled);
      // 会话处理方式与密码保存开关必须在 UpdatePanelTabs **之前**落盘：后者会重开正在
      // 运行的面板，重开时读的是那一刻的配置 —— 顺序反了，新窗口就会带着旧的值。
      await SetPanelSessionMode(id, sessionMode);
      await SetPanelPasswordAutosave(id, passwordAutosave);
      await UpdatePanelTabs(id, tabsPayload);
    } else {
      // 创建模式：CreatePanel 已带第一个标签，多个标签时整体替换补充。
      const created = await CreatePanel(name, tabsPayload[0].url, enabled);
      if (sessionMode === 'fresh') {
        await SetPanelSessionMode(created.id, sessionMode);
      }
      // 新建面板后端默认就是「保存登录密码」，只有用户取消勾选时才需要多写一次。
      if (passwordAutosave !== 'on') {
        await SetPanelPasswordAutosave(created.id, passwordAutosave);
      }
      if (tabsPayload.length > 1) {
        await UpdatePanelTabs(created.id, tabsPayload);
      }
    }
    hideForm();
    await refresh();
  } catch (e) {
    alert(t('errors.saveFailed', { err: tErr(e) }));
  } finally {
    submitBtn.disabled = false;
  }
}

// ─── 应用设置 ──────────────────────────────────────────────────────────────────

// ─── 界面配色（明暗主题）──────────────────────────────────────────────────────
// 三态模型与后端 config.go 一致：light / dark 是显式选择，auto（默认）跟随 Windows
// 深浅色。config.json 是唯一事实源；localStorage 只镜像「设置值」，供 index.html 的
// head 防闪脚本在 Wails 绑定就绪前用 —— 那一刻还拿不到后端配置。

const THEME_STORAGE_KEY = 'paneldock.theme';
const themeMedia = window.matchMedia('(prefers-color-scheme: dark)');

// themeSetting 是配置里的三态取值；systemDark 是系统当前偏好（auto 模式下随系统变化）。
let themeSetting = 'auto';
let systemDark = themeMedia.matches;

function resolveEffectiveTheme(setting, sysDark) {
  if (setting === 'light' || setting === 'dark') return setting;
  return sysDark ? 'dark' : 'light';
}

// applyTheme 把一个三态取值真正落到界面上：换 <html data-theme>、镜像进 localStorage、
// 同步按钮文案与下拉框，并让后端把窗口原生底色跟着换（深色下不改就是启动白闪的另一半）。
// 幂等：settings-changed 广播重放、auto 模式下系统切换重放，结果都一样。
function applyTheme(setting) {
  themeSetting = setting;
  const effective = resolveEffectiveTheme(setting, systemDark);
  document.documentElement.dataset.theme = effective;
  try {
    localStorage.setItem(THEME_STORAGE_KEY, setting);
  } catch (e) {
    // 镜像写不进去只损失下次启动的防闪，功能本身不受影响。
  }
  const nextLabel = effective === 'dark' ? t('theme.toLight') : t('theme.toDark');
  themeToggle.title = nextLabel;
  themeToggle.setAttribute('aria-label', nextLabel);
  stTheme.value = setting;
  // 生效主题没变时调用是空操作；变了（auto 跟随系统 / 启动兜底）就把原生底色对齐。
  ApplyWindowTheme(effective).catch(() => {});
}

// commitTheme 走后端落盘（SetTheme 内部会同步窗口底色），成功后才应用；
// 失败时界面保持原样 —— data-theme 还没动，不需要回滚。
async function commitTheme(setting) {
  themeToggle.disabled = true;
  stTheme.disabled = true;
  try {
    await SetTheme(setting);
    applyTheme(setting);
  } catch (e) {
    alert(t('errors.themeFailed', { err: tErr(e) }));
  } finally {
    themeToggle.disabled = false;
    stTheme.disabled = false;
  }
}

themeToggle.addEventListener('click', () => {
  const current = resolveEffectiveTheme(themeSetting, systemDark);
  commitTheme(current === 'dark' ? 'light' : 'dark');
});

stTheme.addEventListener('change', () => commitTheme(stTheme.value));

// auto 模式下系统切换深浅色要跟上去；固定 light/dark 时不受影响。
themeMedia.addEventListener('change', (e) => {
  systemDark = e.matches;
  if (themeSetting === 'auto') applyTheme('auto');
});

// ─── 界面语言 ──────────────────────────────────────────────────────────────────
// 切换 = SetLanguage 落盘 + 镜像 localStorage + location.reload()。
// 不做「原地切换」：全部事件监听是 innerHTML 渲染后一次性绑死的，原地换语言
// 等于要求整个渲染流程可重入；reload 干净可靠，桌面应用重启级语言切换是常态。
stLanguage.addEventListener('change', async () => {
  const value = stLanguage.value;
  stLanguage.disabled = true;
  try {
    await SetLanguage(value);
    try {
      localStorage.setItem(LANGUAGE_STORAGE_KEY, value);
    } catch (e) {
      // 镜像失败只损失下次启动的防闪；reload 后下拉仍从配置读回正确值。
    }
    location.reload();
  } catch (e) {
    alert(t('errors.languageFailed', { err: tErr(e) }));
    stLanguage.disabled = false;
  }
});

function applySettings(settings) {
  if (!settings) return;
  stShowTray.checked = !!settings.showTrayIcon;
  stPanelCloseAction.value = settings.panelCloseAction || 'ask';
  stLightweightQuit.checked = settings.lightweightQuitOnLastPanel !== false;
  stLanguage.value = settings.language || 'auto';

  // 「最小化到托盘」与托盘图标是绑定的：窗口关掉后藏进托盘，图标是找回它的唯一入口，
  // 所以选了它图标就必须开着（后端同样会拒绝取消勾选）。
  // 这里提前把话说清楚，省得用户点一下才知道做不到。
  stTrayForced.hidden = stPanelCloseAction.value !== 'tray';

  // 配色以后端配置为准重新应用一遍：这是启动路径（head 防闪脚本先按 localStorage
  // 兜了底），也是 settings-changed 广播的兜底 —— 幂等，见 applyTheme。
  applyTheme(settings.theme || 'auto');

  // 语言镜像同步（下次启动的防闪依据），并处理「配置与镜像不一致」的情形
  // （例如用户手改了 config.json）：以配置为准，解析出的语言与当前界面不同就 reload 一次。
  // reload 前镜像已更新，不会循环。
  languageSetting = settings.language || 'auto';
  try {
    localStorage.setItem(LANGUAGE_STORAGE_KEY, languageSetting);
  } catch (e) {
    // 同上：只损失防闪。
  }
  if (resolveLocale(languageSetting) !== getLocale()) {
    location.reload();
  }
}

stShowTray.addEventListener('change', async () => {
  const on = stShowTray.checked;
  stShowTray.disabled = true;
  try {
    await SetShowTrayIcon(on);
  } catch (e) {
    stShowTray.checked = !on;
    alert(t('errors.trayFailed', { err: tErr(e) }));
  } finally {
    stShowTray.disabled = false;
  }
});

async function submitCloseAction(select, setter) {
  const value = select.value;
  select.disabled = true;
  try {
    await setter(value);
  } catch (e) {
    alert(t('errors.closeActionFailed', { err: tErr(e) }));
    applySettings(await GetSettings().catch(() => null));
  } finally {
    select.disabled = false;
  }
}

stPanelCloseAction.addEventListener('change', () =>
  submitCloseAction(stPanelCloseAction, SetPanelCloseAction),
);

stLightweightQuit.addEventListener('change', async () => {
  const on = stLightweightQuit.checked;
  stLightweightQuit.disabled = true;
  try {
    await SetLightweightQuitOnLastPanel(on);
  } catch (e) {
    stLightweightQuit.checked = !on;
    alert(t('errors.lightweightFailed', { err: tErr(e) }));
  } finally {
    stLightweightQuit.disabled = false;
  }
});

// ─── 配置导出 / 导入 ───────────────────────────────────────────────────────────
// 文件对话框在后端（原生），这里只发指令；导入是整体覆盖，先 confirm 讲清后果。
// 导入成功后的界面刷新走后端广播（settings-changed / panels-changed 自己重读），
// 不 reload —— 语言若随导入变了，applySettings 的镜像对齐逻辑会 reload 一次。
stExportConfig.addEventListener('click', async () => {
  stExportConfig.disabled = true;
  try {
    const done = await ExportConfig();
    if (done) alert(t('settings.configExported'));
  } catch (e) {
    alert(t('errors.configExportFailed', { err: tErr(e) }));
  } finally {
    stExportConfig.disabled = false;
  }
});

stImportConfig.addEventListener('click', async () => {
  if (!confirm(t('settings.configImportConfirm'))) return;
  stImportConfig.disabled = true;
  try {
    const done = await ImportConfig();
    if (done) alert(t('settings.configImported'));
  } catch (e) {
    alert(t('errors.configImportFailed', { err: tErr(e) }));
  } finally {
    stImportConfig.disabled = false;
  }
});

// 后端也可能自行改设置：把「关闭面板窗口时」改成「最小化到托盘」会连带打开
// 「在系统托盘显示图标」勾选。收到通知就重新读一遍，否则界面上的勾选状态与配置不一致。
EventsOn('paneldock:settings-changed', async () => {
  applySettings(await GetSettings().catch(() => null));
});

// 面板列表同理：外部打开请求（双击快捷方式）遇到停用面板时，用户可以在询问框里选「启用并打开」，
// 那一改是后端自己做的 —— 不重新读一遍，卡片上会一直挂着过期的「停用」标记。
EventsOn('paneldock:panels-changed', () => {
  refresh();
});

async function refresh() {
  try {
    const [panelData, settings, orphans] = await Promise.all([
      ListPanels(),
      GetSettings(),
      ListOrphanProfiles().catch(() => null), // 检测失败只不显示提示条，不该连面板列表都刷不出来
    ]);
    panels = panelData || [];
    applySettings(settings);
    renderOrphanBanner(Array.isArray(orphans) ? orphans.length : 0);
    render();
  } catch (e) {
    panelListEl.innerHTML = `<p class="empty-hint">${t('errors.listFailed', { err: esc(e) })}</p>`;
  }
}

// 孤儿 profile 提示条：只在确实有残留目录时出现，清完即消失。
function renderOrphanBanner(count) {
  orphanBanner.hidden = count <= 0;
  if (count > 0) {
    orphanTitle.textContent = t('orphan.title', { count });
  }
}

orphanCleanBtn.addEventListener('click', async () => {
  if (!confirm(t('orphan.confirm'))) return;
  try {
    const n = await CleanOrphanProfiles();
    alert(t('orphan.done', { count: n }));
  } catch (e) {
    alert(t('errors.cleanFailed', { err: tErr(e) }));
  }
  await refresh();
});

addPanelBtn.addEventListener('click', () => showForm({}));
pfCancel.addEventListener('click', hideForm);
panelForm.addEventListener('submit', submitForm);

async function init() {
  await refresh();
}

init();
