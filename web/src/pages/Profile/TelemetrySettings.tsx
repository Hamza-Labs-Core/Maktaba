// Settings → Privacy → Usage data (story 16.5). For everyone, this app's own
// switch (the kill switch: off stops collection at once and drops what waits)
// and "Delete what this app sent", which asks Maktaba to delete it (through the
// home). Turning the switch off does not delete; deleting is its own, explicit
// step. For an admin, Settings puts the server's switch beside it (`server`,
// Settings/ServerUsageData): the two are separate answers, and neither gives the
// other. It lives here, not under pages/Settings, because the player's Preferences
// shows it too (Epic 42.5: no server page code in a player bundle).
import { useState, type ReactNode } from "react";
import { Button } from "@ds/components/Button/Button";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useToast } from "@ds/components/Toast/Toast";
import { useI18n } from "../../lib/i18n";
import { ApiError } from "../../lib/api";
import {
  forgetDevice,
  hasDeviceData,
  optIn,
  optOut,
  telemetryEnabled,
} from "../../lib/telemetry/client";
import { TelemetryScope } from "../../components/TelemetryPrompt";

export function TelemetrySettings({ server }: { server?: ReactNode }) {
  const { t } = useI18n();
  const toast = useToast();
  const [on, setOn] = useState(telemetryEnabled);
  const [canForget, setCanForget] = useState(hasDeviceData);
  const [busy, setBusy] = useState(false);

  function toggle() {
    if (on) optOut();
    else optIn();
    setOn(telemetryEnabled());
    setCanForget(hasDeviceData());
  }

  async function forget() {
    setBusy(true);
    try {
      await forgetDevice();
      toast.show({ tone: "success", message: t("telemetry.forgotten") });
    } catch (e) {
      toast.show({ tone: "error", message: e instanceof ApiError ? e.message : t("common.error") });
    } finally {
      setBusy(false);
      setCanForget(hasDeviceData());
    }
  }

  return (
    <section className="mkt-telemetry-settings" aria-labelledby="mkt-telemetry-settings-title">
      <h2 id="mkt-telemetry-settings-title">{t("telemetry.settings.title")}</h2>
      <p className="mkt-muted">{t("telemetry.settings.hint")}</p>
      <TelemetryScope />
      <Toggle
        checked={on}
        onChange={toggle}
        label={on ? t("telemetry.settings.on") : t("telemetry.settings.off")}
      />
      <p className="mkt-muted">{t("telemetry.settings.forgetHint")}</p>
      <Button
        size="sm"
        variant="secondary"
        disabled={busy || !canForget}
        onClick={() => void forget()}
      >
        {t("telemetry.settings.forget")}
      </Button>
      {server}
    </section>
  );
}
