// Epic 46 — big screen's Settings: leave big screen, the language, the
// playback preferences (this browser's), and in the desktop apps "Start in big
// screen" (saved by the shell on this machine).
import { useEffect, useState, type ReactNode } from "react";
import { useI18n } from "../../lib/i18n";
import { desktopBigScreenState, setDesktopStartInBigScreen } from "../../lib/bigScreen";
import { SUBTITLE_SCALES, usePrefs } from "../prefs";
import { Screen } from "../ui";

export function BigSettings({ onExit }: { onExit: () => void }) {
  const { t, locale, setLocale, n } = useI18n();
  const [prefs, update] = usePrefs();
  // null: not the desktop app (or its shell does not serve this page)
  const [start, setStart] = useState<boolean | null>(null);

  useEffect(() => {
    let cancelled = false;
    void desktopBigScreenState().then((s) => !cancelled && setStart(s ? s.start : null));
    return () => {
      cancelled = true;
    };
  }, []);

  const scaleIdx = (SUBTITLE_SCALES as readonly number[]).indexOf(prefs.subtitleScale);
  const nextScale = SUBTITLE_SCALES[(scaleIdx + 1) % SUBTITLE_SCALES.length]!;

  return (
    <Screen title={t("big.tab.settings")} testId="big-settings">
      <div className="bs-settings" data-bs-group="settings">
        <Setting label={t("settings.language")}>
          <button
            type="button"
            className="bs-btn"
            data-bs-focus="set:lang"
            data-bs-autofocus
            onClick={() => setLocale(locale === "en" ? "ar" : "en")}
            lang={locale === "en" ? "ar" : "en"}
          >
            {locale === "en" ? "العربية" : "English"}
          </button>
          <span className="bs-setting__value">{t(`settings.language.${locale}`)}</span>
        </Setting>
        <Setting label={t("big.set.autoplay")} hint={t("big.set.autoplay.hint")}>
          <Toggle
            id="set:autoplay"
            label={t("big.set.autoplay")}
            on={prefs.autoplayNext}
            onChange={(v) => update({ autoplayNext: v })}
          />
        </Setting>
        <Setting label={t("big.set.subtitles")} hint={t("big.set.subtitles.hint")}>
          <Toggle
            id="set:subs"
            label={t("big.set.subtitles")}
            on={prefs.subtitlesOn}
            onChange={(v) => update({ subtitlesOn: v })}
          />
        </Setting>
        <Setting label={t("player.subtitleSize")}>
          <button
            type="button"
            className="bs-btn"
            data-bs-focus="set:subsize"
            onClick={() => update({ subtitleScale: nextScale })}
          >
            {n(Math.round(prefs.subtitleScale * 100))}%
          </button>
        </Setting>
        {start !== null && (
          <Setting label={t("big.set.start")} hint={t("big.set.start.hint")}>
            <Toggle
              id="set:start"
              label={t("big.set.start")}
              on={start}
              onChange={(v) => {
                setStart(v);
                void setDesktopStartInBigScreen(v).then((ok) => !ok && setStart(!v));
              }}
            />
          </Setting>
        )}
        <Setting label={t("big.exit.title")}>
          <button
            type="button"
            className="bs-btn bs-btn--primary"
            data-bs-focus="set:exit"
            onClick={onExit}
          >
            {t("big.exit.leave")}
          </button>
        </Setting>
      </div>
    </Screen>
  );
}

function Setting({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="bs-setting">
      <div className="bs-setting__text">
        <span className="bs-setting__label">{label}</span>
        {hint && <span className="bs-setting__hint">{hint}</span>}
      </div>
      <div className="bs-setting__control">{children}</div>
    </div>
  );
}

function Toggle({
  id,
  label,
  on,
  onChange,
}: {
  id: string;
  label: string;
  on: boolean;
  onChange: (v: boolean) => void;
}) {
  const { t } = useI18n();
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      className={`bs-btn bs-toggle${on ? " bs-toggle--on" : ""}`}
      data-bs-focus={id}
      onClick={() => onChange(!on)}
    >
      {on ? t("big.on") : t("big.off")}
    </button>
  );
}
