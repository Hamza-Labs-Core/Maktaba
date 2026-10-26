// i18n for the cloud app: the same shape as the home app's provider
// (web/src/lib/i18n.tsx) with the cloud's own catalogs, so the two bundles
// do not ship each other's copy. Arabic flips <html dir> to rtl.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import en from "./i18n/en.json";
import ar from "./i18n/ar.json";

export type Locale = "en" | "ar";

const STORAGE_KEY = "mkt:cloud:locale";

export const MESSAGES: Record<Locale, Record<string, string>> = {
  en: en as Record<string, string>,
  ar: ar as Record<string, string>,
};

export interface I18nState {
  locale: Locale;
  dir: "ltr" | "rtl";
  t: (key: string, vars?: Record<string, string | number>) => string;
  /** true when the catalog has the key (for codes from the server). */
  has: (key: string) => boolean;
  n: (value: number, opts?: Intl.NumberFormatOptions) => string;
  formatDate: (value: string | number | Date) => string;
  setLocale: (l: Locale) => void;
}

const I18nContext = createContext<I18nState | undefined>(undefined);

function initialLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "en" || stored === "ar") return stored;
  } catch {
    // storage blocked
  }
  if (typeof navigator !== "undefined" && navigator.language?.startsWith("ar")) return "ar";
  return "en";
}

function interpolate(template: string, vars?: Record<string, string | number>): string {
  if (!vars) return template;
  return template.replace(/\{(\w+)\}/g, (m, k: string) => (k in vars ? String(vars[k]) : m));
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);
  const dir = locale === "ar" ? "rtl" : "ltr";
  const intlLocale = locale === "ar" ? "ar-EG" : "en";

  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dir = dir;
  }, [locale, dir]);

  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) =>
      interpolate(MESSAGES[locale][key] ?? MESSAGES.en[key] ?? key, vars),
    [locale]
  );
  const has = useCallback((key: string) => key in MESSAGES.en, []);
  const n = useCallback(
    (value: number, opts?: Intl.NumberFormatOptions) =>
      new Intl.NumberFormat(intlLocale, opts).format(value),
    [intlLocale]
  );
  const formatDate = useCallback(
    (value: string | number | Date) => {
      const d = value instanceof Date ? value : new Date(value);
      if (Number.isNaN(d.getTime())) return String(value);
      return new Intl.DateTimeFormat(intlLocale, {
        dateStyle: "medium",
        timeStyle: "short",
      }).format(d);
    },
    [intlLocale]
  );

  const value = useMemo<I18nState>(
    () => ({
      locale,
      dir,
      t,
      has,
      n,
      formatDate,
      setLocale: (l: Locale) => {
        setLocaleState(l);
        try {
          localStorage.setItem(STORAGE_KEY, l);
        } catch {
          // storage blocked: the choice lasts for this page only
        }
      },
    }),
    [locale, dir, t, has, n, formatDate]
  );
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nState {
  const ctx = useContext(I18nContext);
  if (!ctx) throw new Error("useI18n outside <I18nProvider>");
  return ctx;
}
