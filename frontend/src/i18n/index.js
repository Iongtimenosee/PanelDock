// 前端 i18n：词典查表 + 占位符替换，无第三方依赖（词汇量一百多条，i18next 那套
// init/backend/plugin 对这个体量是运钞车送外卖）。
//
// 约定：
//   - flat key（'settings.trayLabel'），不嵌套对象 —— 好 grep、好 diff、好做完整性比对；
//   - zh-CN 是源语言：某语言缺 key 时回落到 zh-CN，再缺就显示 key 本身（宁可露 key
//     也不显示空白，且能一眼看出是哪条没翻）；
//   - 占位符 {name}，String.replaceAll 替换；没有复数规则 —— 文案改写规避
//     （'已清理 {n} 个' / 'Cleaned {n} folder(s)'）。
//
// 词典随 vite 编译进产物，运行时没有外挂语言文件（与后端 native_text_windows.go
// 同一决定：桌面工具没有「不发版加语言」的需求）。

import { zhCN } from './zh-CN.js';
import { enUS } from './en-US.js';

const dictionaries = {
  'zh-CN': zhCN,
  'en-US': enUS,
};

const SOURCE_LOCALE = 'zh-CN';

let currentLocale = SOURCE_LOCALE;

// resolveLocale 把三态设置（auto / zh-CN / en-US）解析成具体语言。
// auto 跟随 WebView2 的 navigator.language：zh 开头 → zh-CN，其余 → en-US。
// 与后端 systemLanguage（GetUserDefaultUILanguage，中文主语言 → zh-CN）同一语义。
export function resolveLocale(pref) {
  if (pref === 'zh-CN' || pref === 'en-US') return pref;
  return String(navigator.language || '').toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US';
}

// initLocale 在渲染任何界面前调用（main.js 顶部）：定语言 + 设 <html lang>
// （影响屏幕阅读器与中英字体回退）。
export function initLocale(pref) {
  currentLocale = resolveLocale(pref);
  document.documentElement.lang = currentLocale;
}

export function getLocale() {
  return currentLocale;
}

// has 判断词典里有没有某个 key（错误码翻译前先探测）。
export function has(key) {
  return Object.prototype.hasOwnProperty.call(dictionaries[currentLocale], key) ||
    Object.prototype.hasOwnProperty.call(dictionaries[SOURCE_LOCALE], key);
}

// t 取词条；params 里的 {占位符} 会被替换。
export function t(key, params) {
  let text = dictionaries[currentLocale][key];
  if (text === undefined) text = dictionaries[SOURCE_LOCALE][key];
  if (text === undefined) return key;
  if (params) {
    for (const [name, value] of Object.entries(params)) {
      text = text.replaceAll(`{${name}}`, String(value));
    }
  }
  return text;
}

// 后端错误码格式（见 apperror.go，两边必须同步改）：
//   "panel.notFound"            → 词典 err.panel.notFound 的文案
//   "panel.urlInvalid: <技术细节>" → 文案 +（技术细节）
// 翻译不到的错误原样返回（内部技术错误、底层 os 错误）—— 宁可露技术细节也不硬编。
const ERROR_CODE_RE = /^([a-z]+(?:\.[A-Za-z0-9]+)+)(?:: (.*))?$/;

export function tErr(e) {
  const raw = String(e);
  const m = raw.match(ERROR_CODE_RE);
  if (m && has(`err.${m[1]}`)) {
    const message = t(`err.${m[1]}`);
    return m[2] ? `${message}（${m[2]}）` : message;
  }
  return raw;
}
