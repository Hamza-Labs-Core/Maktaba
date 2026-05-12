import { useI18n } from "../lib/i18n";

export function LangToggle() {
  const { locale, setLocale } = useI18n();
  const next = locale === "en" ? "ar" : "en";
  return (
    <button
      type="button"
      className="mkt-btn mkt-btn--ghost"
      onClick={() => setLocale(next)}
      aria-label={`Switch to ${next === "ar" ? "Arabic" : "English"}`}
    >
      {locale === "en" ? "AR" : "EN"}
    </button>
  );
}
