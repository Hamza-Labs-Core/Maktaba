// Story 28.5 — how often the mobile app looks for a new version of itself (off / daily /
// weekly). Only inside the Capacitor app: the browser and the desktop app update by other
// means. Kept on this device (lib/appUpdate), like the theme.
import { useState } from "react";
import { RadioGroup, Radio } from "@ds/components/Choice/Radio";
import { useI18n } from "../../lib/i18n";
import { nativePlatform, type NativePlatform } from "../../lib/native";
import { readFrequency, writeFrequency, type CheckFrequency } from "../../lib/appUpdate";

const FREQUENCIES: CheckFrequency[] = ["daily", "weekly", "off"];

export function AppUpdateSettings({ platform = nativePlatform() }: { platform?: NativePlatform }) {
  const { t } = useI18n();
  const [freq, setFreq] = useState<CheckFrequency>(() => readFrequency());
  if (platform === "web") return null;
  return (
    <section className="mkt-about__app-updates" aria-labelledby="mkt-app-updates">
      <h2 id="mkt-app-updates">{t("update.app.title")}</h2>
      <p className="mkt-muted">{t("update.app.hint")}</p>
      <RadioGroup legend={t("update.app.frequency")} name="app-update-frequency">
        {FREQUENCIES.map((f) => (
          <Radio
            key={f}
            name="app-update-frequency"
            label={t(`update.app.frequency.${f}`)}
            checked={freq === f}
            onChange={() => {
              writeFrequency(f);
              setFreq(f);
            }}
          />
        ))}
      </RadioGroup>
    </section>
  );
}
