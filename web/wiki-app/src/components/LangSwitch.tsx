import { useLocation } from '@tanstack/react-router';
import { Languages } from 'lucide-react';
import { LANG, otherLangHref, t } from '@/lib/i18n';

// The same page in the other language: /wiki/#/entry/x <-> /ar/wiki/#/entry/x.
export function LangSwitch({ className = '' }: { className?: string }) {
  useLocation(); // re-render on every route change, so the link keeps the current page
  const href = otherLangHref(LANG, window.location);
  const other = LANG === 'ar' ? 'en' : 'ar';
  return (
    <a
      id="lang-switch"
      href={href}
      hrefLang={other}
      lang={other}
      aria-label={t('lang.switch.label')}
      title={t('lang.switch.label')}
      className={`inline-flex items-center gap-1.5 rounded border border-slate-200 dark:border-slate-700 px-2 py-1 text-[12px] text-slate-600 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 hover:text-slate-900 dark:hover:text-slate-100 transition-colors ${className}`}
    >
      <Languages className="h-3.5 w-3.5" aria-hidden />
      {t('lang.switch')}
    </a>
  );
}
