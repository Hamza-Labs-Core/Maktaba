// The Settings tab (story 33.11 AC3): the key diff of rules.tv / rules.tv_output, each
// key labelled from the settings schema (localised like the Settings page).
import { useEffect, useState } from "react";
import { useI18n } from "../../../../lib/i18n";
import { rulesApi, type RuleSection } from "../../../../lib/rulesApi";
import type { SettingChange } from "../../../../lib/tvChanges";
import { localizeSections } from "../../../Settings/RulesSection";

function show(v: unknown): string {
  if (v === undefined || v === null) return "—";
  if (typeof v === "string") return v;
  return JSON.stringify(v);
}

export function SettingsDiff({ settings }: { settings?: Record<string, SettingChange[]> }) {
  const { t, locale } = useI18n();
  const [sections, setSections] = useState<RuleSection[] | null>(null);

  useEffect(() => {
    let alive = true;
    rulesApi
      .list()
      .then((r) => alive && setSections(localizeSections(r.sections, locale)))
      .catch(() => alive && setSections([]));
    return () => {
      alive = false;
    };
  }, [locale]);

  const keys = Object.keys(settings ?? {}).sort();
  if (keys.length === 0) return <p className="mkt-tvx-muted">{t("tvchanges.settings.noChange")}</p>;
  const label = (section: string, key: string) =>
    sections?.find((s) => s.key === section)?.fields.find((f) => f.name === key)?.label ?? key;

  return (
    <div className="mkt-tvx-diff" data-testid="settings-diff">
      {keys.map((section) => (
        <section key={section} className="mkt-tvx-group">
          <h3>{t(`tvchanges.settings.section.${section}`)}</h3>
          <table className="mkt-tvx-kv">
            <thead>
              <tr>
                <th scope="col">{t("tvchanges.settings.key")}</th>
                <th scope="col">{t("tvchanges.settings.from")}</th>
                <th scope="col">{t("tvchanges.settings.to")}</th>
              </tr>
            </thead>
            <tbody>
              {(settings?.[section] ?? []).map((c) => (
                <tr key={c.key}>
                  <th scope="row">
                    {label(section, c.key)}
                    <span className="mkt-tvx-mono mkt-tvx-muted"> {c.key}</span>
                  </th>
                  <td className="mkt-tvx-del" dir="auto">
                    {show(c.from)}
                  </td>
                  <td className="mkt-tvx-add" dir="auto">
                    {show(c.to)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </section>
      ))}
    </div>
  );
}
