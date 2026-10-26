// Story 16.5: "Help improve Maktaba", this app's one question about sharing its
// anonymous usage data with Maktaba (its makers, not the home's admin). Not a
// dialog: a notice at the top of the page that the app stays usable around,
// asked once per app install until answered ("Not now" is an answer: off).
// Nothing is collected before "Share"; Settings → Privacy changes the answer
// later. It also starts the collection where the user agreed, and counts the
// areas of the app it visits.
import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import {
  noteRoute,
  optIn,
  optOut,
  startTelemetry,
  telemetryDecided,
} from "../lib/telemetry/client";

/** the bullets of what is sent, and of what never is (shared with Settings → Privacy) */
export function TelemetryScope() {
  const { t } = useI18n();
  return (
    <>
      <p className="mkt-telemetry__label">{t("telemetry.collected")}</p>
      <ul>
        <li>{t("telemetry.bullet.version")}</li>
        <li>{t("telemetry.bullet.features")}</li>
        <li>{t("telemetry.bullet.errors")}</li>
      </ul>
      <p className="mkt-telemetry__label">{t("telemetry.never")}</p>
      <ul>
        <li>{t("telemetry.bullet.never")}</li>
      </ul>
    </>
  );
}

export function TelemetryPrompt() {
  const { t } = useI18n();
  const { pathname } = useLocation();
  const [open, setOpen] = useState(() => !telemetryDecided());

  // starts once (a no-op later, and before an opt-in), then counts each route
  useEffect(() => {
    startTelemetry();
    noteRoute(pathname);
  }, [pathname]);

  if (!open) return null;
  return (
    <section
      className="mkt-update-banner mkt-telemetry"
      aria-labelledby="mkt-telemetry-title"
      data-testid="telemetry-prompt"
    >
      <div className="mkt-telemetry__body">
        <h2 id="mkt-telemetry-title" className="mkt-telemetry__title">
          {t("telemetry.title")}
        </h2>
        <p>{t("telemetry.intro")}</p>
        <TelemetryScope />
        <p className="mkt-muted">
          {t("telemetry.later")} <Link to="/settings/privacy">{t("telemetry.settingsLink")}</Link>
        </p>
      </div>
      <span className="mkt-update-banner__actions">
        <button
          type="button"
          className="mkt-btn mkt-btn--primary"
          onClick={() => {
            optIn();
            noteRoute(pathname);
            setOpen(false);
          }}
        >
          {t("telemetry.optIn")}
        </button>
        <button
          type="button"
          className="mkt-btn mkt-btn--ghost"
          onClick={() => {
            optOut();
            setOpen(false);
          }}
        >
          {t("telemetry.decline")}
        </button>
      </span>
    </section>
  );
}
