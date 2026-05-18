import { useI18n } from "../lib/i18n";

export function LangToggle() {
  const { locale, setLocale, t } = useI18n();
  const next = locale === "en" ? "ar" : "en";
  return (
    <button
      type="button"
      className="mkt-btn mkt-btn--ghost"
      onClick={() => setLocale(next)}
      aria-label={t("settings.language") + ": " + t(`settings.language.${next}`)}
    >
      {locale === "en" ? "AR" : "EN"}
    </button>
  );
}
