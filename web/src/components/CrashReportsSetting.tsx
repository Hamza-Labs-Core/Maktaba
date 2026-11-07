// Crash reports to the home (Epic 53 story 53.14): the switch in Settings → Privacy, and the
// one-time question after sign-in, with two equal buttons and neither chosen for the person.
// Off until the person says yes; kept on this device.
import { useEffect, useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Card } from "@ds/components/Card/Card";
import { useI18n } from "../lib/i18n";
import {
  crashReportsAsked,
  crashReportsOn,
  onCrashReportsChange,
  setCrashReports,
} from "../lib/crashReports";

function useCrashReports(): [boolean, boolean] {
  const [state, setState] = useState(
    () => [crashReportsOn(), crashReportsAsked()] as [boolean, boolean]
  );
  useEffect(
    () => onCrashReportsChange(() => setState([crashReportsOn(), crashReportsAsked()])),
    []
  );
  return state;
}

export function CrashReportsSetting() {
  const { t } = useI18n();
  const [on] = useCrashReports();
  return (
    <label className="mkt-settings__row" data-testid="crash-reports-setting">
      <input type="checkbox" checked={on} onChange={(e) => setCrashReports(e.target.checked)} />{" "}
      <span>{t("crash.setting")}</span>
      <span className="mkt-muted"> {t("crash.settingNote")}</span>
    </label>
  );
}

export function CrashReportsQuestion() {
  const { t } = useI18n();
  const [, asked] = useCrashReports();
  if (asked) return null;
  return (
    <Card
      className="mkt-crash-ask"
      role="region"
      aria-label={t("crash.onboarding.title")}
      data-testid="crash-ask"
    >
      <h2>{t("crash.onboarding.title")}</h2>
      <p>{t("crash.onboarding.body")}</p>
      <div className="mkt-crash-ask__buttons">
        <Button variant="secondary" onClick={() => setCrashReports(true)}>
          {t("crash.onboarding.yes")}
        </Button>
        <Button variant="secondary" onClick={() => setCrashReports(false)}>
          {t("crash.onboarding.no")}
        </Button>
      </div>
    </Card>
  );
}
