// i18n provider for the Maktaba web app (Story 11.12).
//
// Strings live in `src/i18n/<locale>.json` — NO prose is inlined in
// JSX (Epic-17 / 11.12 rule). The provider exposes:
//
//   - `t(key, vars?)`     localised string with {name} interpolation;
//                         a miss returns the key AND console.warns in
//                         dev so untranslated copy is caught early.
//   - `dir`               "rtl" for Arabic, "ltr" otherwise; stamped on
//                         <html dir> + <html lang>.
//   - `n(value)`          Intl.NumberFormat in the active locale
//                         (Arabic-Indic digits under `ar`).
//   - `formatDate(d)`     Intl.DateTimeFormat in the active locale.
//
// Catalogs are imported statically so a missing key in `ar` is a build
// /test-time concern, not a runtime fetch.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import en from "../i18n/en.json";
import ar from "../i18n/ar.json";

export type Locale = "en" | "ar";
export type Direction = "ltr" | "rtl";

const STORAGE_KEY = "mkt:locale";

const MESSAGES: Record<Locale, Record<string, string>> = {
  en: en as Record<string, string>,
  ar: ar as Record<string, string>,
};

const warnedMisses = new Set<string>();

export interface I18nState {
  locale: Locale;
  dir: Direction;
  t: (key: string, vars?: Record<string, string | number>) => string;
  n: (value: number) => string;
  formatDate: (value: string | number | Date) => string;
  setLocale: (l: Locale) => void;
}

const I18nContext = createContext<I18nState | undefined>(undefined);

function resolveInitialLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "en" || stored === "ar") return stored;
  } catch {
    // ignored — locked-down storage
  }
  if (typeof navigator !== "undefined" && navigator.language?.startsWith("ar")) {
    return "ar";
  }
  return "en";
}

function interpolate(template: string, vars?: Record<string, string | number>): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
}

// U+2068 FIRST STRONG ISOLATE … U+2069 POP DIRECTIONAL ISOLATE — wraps
// user-supplied runs so a bidi title can't reorder surrounding chrome.
export function bidiIsolate(s: string): string {
  return `⁨${s}⁩`;
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(resolveInitialLocale);
  const dir: Direction = locale === "ar" ? "rtl" : "ltr";

  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dir = dir;
  }, [locale, dir]);

  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) => {
      const hit = MESSAGES[locale][key] ?? MESSAGES.en[key];
      if (hit === undefined) {
        if (import.meta.env?.DEV && !warnedMisses.has(key)) {
          warnedMisses.add(key);
          console.warn(`i18n: missing key "${key}" for locale "${locale}"`);
        }
        return key;
      }
      return interpolate(hit, vars);
    },
    [locale]
  );

  const n = useCallback(
    (value: number) => new Intl.NumberFormat(locale === "ar" ? "ar-EG" : "en").format(value),
    [locale]
  );

  const formatDate = useCallback(
    (value: string | number | Date) => {
      const d = value instanceof Date ? value : new Date(value);
      if (Number.isNaN(d.getTime())) return String(value);
      return new Intl.DateTimeFormat(locale === "ar" ? "ar-EG" : "en", {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(d);
    },
    [locale]
  );

  const value = useMemo<I18nState>(
    () => ({
      locale,
      dir,
      t,
      n,
      formatDate,
      setLocale: (l: Locale) => {
        setLocaleState(l);
        try {
          localStorage.setItem(STORAGE_KEY, l);
        } catch {
          // ignored
        }
      },
    }),
    [locale, dir, t, n, formatDate]
  );

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nState {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error("useI18n must be used inside <I18nProvider>");
  }
  return ctx;
}
