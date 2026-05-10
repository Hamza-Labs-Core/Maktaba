// i18n provider for the Maktaba web app (Story 11.12).
//
// No external i18n library yet — a tiny lookup table is enough for
// Phase 10 scaffolding. The provider exposes `t(key)` and `dir` so
// page components can switch RTL/LTR via `dir={dir}` on the layout
// root.
//
// Strings live under `messages/<locale>.json` (loaded eagerly in this
// minimal scaffold). Replace with i18next or formatjs in a later
// phase once the message catalogue grows.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';

export type Locale = 'en' | 'ar';
export type Direction = 'ltr' | 'rtl';

const STORAGE_KEY = 'mkt:locale';

const MESSAGES: Record<Locale, Record<string, string>> = {
  en: {
    'app.title': 'Maktaba',
    'nav.library': 'Library',
    'nav.search': 'Search',
    'nav.queue': 'Processing',
    'nav.settings': 'Settings',
    'login.username': 'Username',
    'login.password': 'Password',
    'login.submit': 'Sign in',
    'login.error': 'Invalid username or password.',
    'common.loading': 'Loading…',
    'common.empty': 'Nothing here yet.',
    'common.error': 'Something went wrong.',
    'common.retry': 'Try again',
  },
  ar: {
    'app.title': 'مكتبة',
    'nav.library': 'المكتبة',
    'nav.search': 'البحث',
    'nav.queue': 'قائمة المعالجة',
    'nav.settings': 'الإعدادات',
    'login.username': 'اسم المستخدم',
    'login.password': 'كلمة المرور',
    'login.submit': 'تسجيل الدخول',
    'login.error': 'اسم المستخدم أو كلمة المرور غير صحيحة.',
    'common.loading': 'جارٍ التحميل…',
    'common.empty': 'لا يوجد شيء بعد.',
    'common.error': 'حدث خطأ ما.',
    'common.retry': 'إعادة المحاولة',
  },
};

interface I18nState {
  locale: Locale;
  dir: Direction;
  t: (key: string) => string;
  setLocale: (l: Locale) => void;
}

const I18nContext = createContext<I18nState | undefined>(undefined);

function resolveInitialLocale(): Locale {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'en' || stored === 'ar') return stored;
  } catch {
    // ignored
  }
  if (typeof navigator !== 'undefined' && navigator.language?.startsWith('ar')) {
    return 'ar';
  }
  return 'en';
}

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(resolveInitialLocale);
  const dir: Direction = locale === 'ar' ? 'rtl' : 'ltr';

  useEffect(() => {
    document.documentElement.lang = locale;
    document.documentElement.dir = dir;
  }, [locale, dir]);

  const value = useMemo<I18nState>(() => ({
    locale,
    dir,
    t: (key: string) => MESSAGES[locale][key] ?? key,
    setLocale: (l: Locale) => {
      setLocaleState(l);
      try {
        localStorage.setItem(STORAGE_KEY, l);
      } catch {
        // ignored
      }
    },
  }), [locale, dir]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nState {
  const ctx = useContext(I18nContext);
  if (!ctx) {
    throw new Error('useI18n must be used inside <I18nProvider>');
  }
  return ctx;
}
