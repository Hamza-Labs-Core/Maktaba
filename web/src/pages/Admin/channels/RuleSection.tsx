// What a non-grid channel plays: a marathon of one series (picked, not typed),
// a shuffle of the library, or a smart mix. A schedule channel uses the grid
// (ScheduleSection) instead.
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { Toggle } from "@ds/components/Choice/Toggle";
import { useI18n } from "../../../lib/i18n";
import type { ChannelMode } from "../../../lib/channels";
import { SourceSearch } from "./pickers";

export function RuleSection({
  mode,
  config,
  setConfig,
}: {
  mode: ChannelMode;
  config: Record<string, unknown>;
  setConfig: (key: string, value: unknown) => void;
}) {
  const { t } = useI18n();
  const str = (k: string) => (typeof config[k] === "string" ? (config[k] as string) : "");

  if (mode === "marathon") {
    return (
      <fieldset className="mkt-rule">
        <legend>{t("channels.rule.marathon")}</legend>
        {str("series_id") ? (
          <p className="mkt-cb-picked">
            {t("channels.slot.picked")}: <strong>{str("series_name") || str("series_id")}</strong>
          </p>
        ) : (
          <p className="mkt-form__warn">{t("channels.rule.pickSeries")}</p>
        )}
        <SourceSearch
          kind="series"
          selectedId={str("series_id")}
          label={t("channels.source.search.series")}
          onPick={(h) => {
            setConfig("series_id", h.id);
            setConfig("series_name", h.name);
          }}
        />
        <Select
          label={t("channels.rule.order")}
          value={str("order") || "aired"}
          options={[
            { value: "aired", label: t("channels.rule.order.aired") },
            { value: "dvd", label: t("channels.rule.order.dvd") },
            { value: "filename", label: t("channels.rule.order.filename") },
          ]}
          onChange={(e) => setConfig("order", e.target.value)}
        />
        <Toggle
          checked={Boolean(config.loop)}
          onChange={(e) => setConfig("loop", e.target.checked)}
          label={t("channels.rule.loop")}
        />
      </fieldset>
    );
  }
  if (mode === "smart_mix") {
    return (
      <fieldset className="mkt-rule">
        <legend>{t("channels.rule.smartMix")}</legend>
        <Input
          label={t("channels.rule.daypart")}
          value={str("daypart_profile")}
          onChange={(e) => setConfig("daypart_profile", e.target.value)}
        />
        <p className="mkt-muted">{t("channels.rule.smartMixFallback")}</p>
      </fieldset>
    );
  }
  return (
    <fieldset className="mkt-rule">
      <legend>{t("channels.rule.shuffle")}</legend>
      <p className="mkt-muted">{t("channels.rule.shuffleHint")}</p>
    </fieldset>
  );
}

/** What blocks saving the rule, as an i18n key (null: fine). */
export function ruleProblem(mode: ChannelMode, config: Record<string, unknown>): string | null {
  if (mode === "marathon" && !config.series_id && !config.source) return "channels.rule.pickSeries";
  if (mode === "schedule" && !(Array.isArray(config.slots) && config.slots.length > 0))
    return "channels.grid.empty";
  return null;
}
