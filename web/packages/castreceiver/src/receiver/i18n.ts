// The receiver's strings (i18n/en.json, i18n/ar.json, checked key for key by
// tools/i18n/check_tv_catalogs.py). The sender's `lang` picks the catalog; Arabic sets
// dir="rtl" on the page, and numbers and times keep their left-to-right order inside Arabic
// text with LRM marks.

import ar from "../../i18n/ar.json";
import en from "../../i18n/en.json";

export type Lang = "en" | "ar";
export type Key = keyof typeof en;

const CATALOGS: Record<Lang, Record<string, string>> = { en, ar };

/** The sender's language as one the receiver has: `ar` (or `ar-EG`…) is Arabic, anything else
 * English. */
export function langOf(value: unknown): Lang {
  return typeof value === "string" && /^ar\b/i.test(value) ? "ar" : "en";
}

/** The direction a language is written in. */
export function dirOf(lang: Lang): "rtl" | "ltr" {
  return lang === "ar" ? "rtl" : "ltr";
}

const LRM = "‎";

/** A number or time kept left to right inside right-to-left text. */
export function ltr(value: string): string {
  return `${LRM}${value}${LRM}`;
}

/** The string for `key` in `lang` with `%1$s`, `%2$s`… replaced by `args` in order. */
export function t(lang: Lang, key: Key, ...args: string[]): string {
  const s = CATALOGS[lang][key] ?? CATALOGS.en[key] ?? key;
  return s.replace(/%(\d+)\$s/g, (_, n: string) => args[Number(n) - 1] ?? "");
}

/** Seconds as h:mm:ss, or m:ss under an hour. */
export function clock(totalSec: number): string {
  const s = Math.max(0, Math.floor(Number.isFinite(totalSec) ? totalSec : 0));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

/** "12:34 of 1:45:00", the times marked left to right. */
export function position(lang: Lang, at: number, duration: number): string {
  return t(lang, "receiver.position", ltr(clock(at)), ltr(clock(duration)));
}
