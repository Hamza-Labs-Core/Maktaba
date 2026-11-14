// Story 58.09 (strings: 58.15) — the launcher's English and Arabic. The catalogs are
// apps/tv/web/i18n/{en,ar}.json, checked key for key by tools/i18n/check_tv_catalogs.py.
// Arabic mirrors the page (dir="rtl" on <html>, logical CSS); the language is the one the
// person chose (here or in /tv/'s Settings, which tells the launcher), else the TV's own.

import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

export type Lang = "en" | "ar";
type Catalog = Record<string, string>;

const CATALOGS: Record<Lang, Catalog> = { en, ar };
const KEY = "maktaba.launcher.lang";

let current: Lang = "en";
const listeners: (() => void)[] = [];

/** The language a TV's own setting implies. */
export function langOf(navLang: string | undefined): Lang {
  return navLang && navLang.toLowerCase().indexOf("ar") === 0 ? "ar" : "en";
}

/** The string for key, its %1$s / %1$d (or %s / %d) filled from args. */
export function t(key: string, ...args: (string | number)[]): string {
  const s = CATALOGS[current][key] ?? CATALOGS.en[key] ?? key;
  let next = 0;
  return s.replace(/%(?:(\d+)\$)?([sd])/g, (_, pos: string | undefined) => {
    const i = pos ? Number(pos) - 1 : next++;
    const v = args[i];
    return v === undefined ? "" : String(v);
  });
}

export function lang(): Lang {
  return current;
}

export function dir(): "rtl" | "ltr" {
  return current === "ar" ? "rtl" : "ltr";
}

/** Sets the language (kept for the next start) and tells the screens. */
export function setLang(l: Lang): void {
  current = l;
  try {
    localStorage.setItem(KEY, l);
  } catch {
    // storage refused: the language holds for this run
  }
  document.documentElement.lang = l;
  document.documentElement.dir = dir();
  listeners.forEach((f) => f());
}

/** The saved language, else the TV's. */
export function initLang(navLang: string | undefined): void {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem(KEY);
  } catch {
    saved = null;
  }
  setLang(saved === "ar" || saved === "en" ? saved : langOf(navLang));
}

export function onLangChange(f: () => void): void {
  listeners.push(f);
}

/** Every key of both catalogs (tests). */
export function catalogKeys(l: Lang): string[] {
  return Object.keys(CATALOGS[l]);
}
