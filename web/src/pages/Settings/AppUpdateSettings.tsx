// Story 28.5 — how often the mobile app looks for a new version of itself (off / daily /
// weekly). Only where the update banner is (the sideloaded Android app): a store build is
// updated by its store, the browser and the desktop app by other means. Kept on this device
// (lib/appUpdate), like the theme.
import { useState } from "react";
import { RadioGroup, Radio } from "@ds/components/Choice/Radio";
import { useI18n } from "../../lib/i18n";
import { nativePlatform, type NativePlatform } from "../../lib/native";
import {
  bannerOffered,
  distribution,
  readFrequency,
  writeFrequency,
  type CheckFrequency,
  type Distribution,
} from "../../lib/appUpdate";

const FREQUENCIES: CheckFrequency[] = ["daily", "weekly", "off"];

export function AppUpdateSettings({
  platform = nativePlatform(),
  dist = distribution(platform),
}: {
  platform?: NativePlatform;
  dist?: Distribution;
}) {
  const { t } = useI18n();
  const [freq, setFreq] = useState<CheckFrequency>(() => readFrequency());
  if (!bannerOffered(platform, dist)) return null;
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
