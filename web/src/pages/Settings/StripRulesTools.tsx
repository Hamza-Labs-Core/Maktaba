// Settings → Channel strip (rules tv_strip, Story 27.11): the palettes and
// "reset colours to default" over the section's colour settings, and a live
// preview of the strip with the values as edited (saved or not).
import type { RuleField } from "../../lib/rulesApi";
import type { StripSettings } from "../../lib/channels";
import { useI18n } from "../../lib/i18n";
import { DEFAULT_STRIP, STRIP_COLOR_KEYS, stripColors, type StripColors } from "../../lib/strip";
import { StripColorsEditor } from "../Admin/channels/StripColors";
import { StripPreview } from "../Admin/channels/StripPreview";

export function StripRulesTools({
  fields,
  pending,
  readOnly,
  onSet,
}: {
  fields: RuleField[];
  pending: Record<string, unknown>;
  readOnly: boolean;
  onSet: (f: RuleField, v: unknown) => void;
}) {
  const { t } = useI18n();
  const current: Record<string, unknown> = { ...DEFAULT_STRIP };
  const defaults: Record<string, unknown> = { ...DEFAULT_STRIP };
  for (const f of fields) {
    if (!(f.name in DEFAULT_STRIP)) continue;
    defaults[f.name] = f.default;
    current[f.name] = f.name in pending ? pending[f.name] : f.value;
  }
  const s = current as unknown as StripSettings;
  const byName = new Map(fields.map((f) => [f.name, f]));
  return (
    <div className="mkt-strip" data-testid="strip-rules-tools">
      <StripColorsEditor
        value={stripColors(s)}
        defaults={stripColors(defaults as unknown as StripSettings)}
        readOnly={readOnly}
        onChange={(c: Partial<StripColors>) => {
          for (const k of STRIP_COLOR_KEYS) {
            const f = byName.get(k);
            if (f && k in c) onSet(f, c[k]);
          }
        }}
      />
      <StripPreview
        settings={{ ...s, enabled: true }}
        now={t("channels.strip.sample.now")}
        next={t("channels.strip.sample.next")}
        channelName={t("channels.strip.sample.channel")}
      />
    </div>
  );
}
