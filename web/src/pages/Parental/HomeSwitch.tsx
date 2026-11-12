// Epic 57 story 57.12: turning parental controls on in a home that already exists.
//
//   GET  /api/parental/settings                 admin
//   PUT  /api/parental/settings {enabled}       admin; off is 409 while an account is restricted
//   POST /api/parental/settings/notice/dismiss  admin
//
// An update hides nothing from anyone: parental controls start off, and a device that says
// a child uses it is only recorded. The admin is told once (the notice here and in the
// notification centre) and sets things up below: the first restricted account, or the
// first library default rating, turns parental controls on. The home decides everything;
// this shows its answer. An older home has no such route: the section is then left out.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../lib/i18n";
import {
  dismissChildDeviceNotice,
  fetchParentalSettings,
  refusalCode,
  setParentalEnabled,
  type ParentalSettings,
} from "../../lib/familyApi";

export function HomeSwitch() {
  const { t, n } = useI18n();
  const [settings, setSettings] = useState<ParentalSettings | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let live = true;
    fetchParentalSettings()
      .then((s) => live && setSettings(s))
      // a home older than this page (404), or one that refuses: no section
      .catch(() => live && setSettings(null));
    return () => {
      live = false;
    };
  }, []);

  if (!settings) return null;

  const run = async (what: () => Promise<ParentalSettings>, failed: string) => {
    setBusy(true);
    setError("");
    try {
      setSettings(await what());
    } catch (e) {
      if (refusalCode(e) === "restrictions_exist") {
        setError(t("parental.enabled.offBlocked"));
        setSettings({ ...settings, canDisable: false });
      } else {
        setError(failed);
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      {settings.childDeviceNotice && (
        <aside className="mkt-parental-notice" role="status" data-testid="parental-notice">
          <h2>{t("parental.notice.title")}</h2>
          <p>{t("parental.notice.body")}</p>
          <div className="mkt-parental-row">
            <a className="mkt-parental-notice__action" href="#family-make">
              {t("parental.notice.action")}
            </a>
            <Button
              variant="secondary"
              disabled={busy}
              onClick={() => void run(dismissChildDeviceNotice, t("parental.notice.failed"))}
            >
              {t("parental.notice.dismiss")}
            </Button>
          </div>
        </aside>
      )}
      <section aria-labelledby="parental-enabled" data-testid="parental-enabled">
        <h2 id="parental-enabled">{t("parental.enabled")}</h2>
        <p className="mkt-parental-note" data-testid="parental-enabled-state">
          {t(settings.enabled ? "parental.enabled.on" : "parental.enabled.off")}
        </p>
        {!settings.enabled && settings.childDevices > 0 && (
          <p className="mkt-parental-note" data-testid="parental-enabled-devices">
            {t("parental.enabled.devices", { count: n(settings.childDevices) })}
          </p>
        )}
        {settings.enabled && !settings.canDisable ? (
          <p className="mkt-parental-note" data-testid="parental-enabled-blocked">
            {t("parental.enabled.offBlocked")}
          </p>
        ) : (
          <Button
            variant="secondary"
            disabled={busy}
            onClick={() =>
              void run(() => setParentalEnabled(!settings.enabled), t("parental.enabled.failed"))
            }
          >
            {t(settings.enabled ? "parental.enabled.turnOff" : "parental.enabled.turnOn")}
          </Button>
        )}
        {error && (
          <p className="mkt-alert" role="alert">
            {error}
          </p>
        )}
      </section>
    </>
  );
}
