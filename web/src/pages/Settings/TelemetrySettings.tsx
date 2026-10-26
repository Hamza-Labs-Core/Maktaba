// Settings → Privacy → Usage analytics (story 16.5): this device's opt-in
// switch (the kill switch: off stops collection at once and drops what waits)
// and "Forget my device", which deletes everything this device sent from the
// server. Turning the switch off does not delete; forgetting is its own,
// explicit step.
import { useState } from "react";
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

export function TelemetrySettings() {
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
    </section>
  );
}
