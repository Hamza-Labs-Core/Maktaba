// i18n for the cloud's web apps: the same shape as the home app's provider
// (web/src/lib/i18n.tsx). Each app passes its own catalogs (the user app's, web/cloud/src/i18n;
// the operator console's, web/admin/src/i18n), so no bundle ships another's copy. Arabic flips
// <html dir> to rtl.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export type Locale = "en" | "ar";

/** An app's catalogs, by locale. */
export type Messages = Record<Locale, Record<string, string>>;

const STORAGE_KEY = "mkt:cloud:locale";

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

export function I18nProvider({ messages, children }: { messages: Messages; children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(initialLocale);
  const dir = locale === "ar" ? "rtl" : "ltr";
  const intlLocale = locale === "ar" ? "ar-EG" : "en";

  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dir = dir;
  }, [locale, dir]);

  const t = useCallback(
    (key: string, vars?: Record<string, string | number>) =>
      interpolate(messages[locale][key] ?? messages.en[key] ?? key, vars),
    [messages, locale]
  );
  const has = useCallback((key: string) => key in messages.en, [messages]);
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
