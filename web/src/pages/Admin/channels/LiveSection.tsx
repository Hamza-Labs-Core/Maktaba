// The live broadcasts this channel carries: the `tv.live` rules rows that
// name its number (a stream address, what the guide calls it, from `start`
// every `every_minutes` until `end`, `minutes` long, on `days`). Saved with
// the channel, through the rules API — a live row is not a channel edit.
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { useI18n } from "../../../lib/i18n";
import { DayChips } from "./pickers";
import { liveProblems, slotDays, type LiveRow } from "./schedule";

export function newLiveRow(number: number): LiveRow {
  return {
    channel: number,
    url: "",
    title: "",
    start: "00:00",
    end: "00:00",
    every_minutes: 60,
    minutes: 10,
    days: [],
  };
}

export function LiveSection({
  rows,
  number,
  focus,
  inherited,
  onChange,
}: {
  /** every tv.live row (all channels) */
  rows: LiveRow[];
  number: number;
  /** the row index the grid asked to open */
  focus: number | null;
  /** the rows came from the dizqueTV export's list (tv.live was empty) */
  inherited: boolean;
  onChange: (rows: LiveRow[]) => void;
}) {
  const { t } = useI18n();
  const mine = rows.map((r, i) => [r, i] as const).filter(([r]) => Number(r.channel) === number);

  function update(i: number, patch: Partial<LiveRow>) {
    onChange(rows.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  }

  return (
    <div className="mkt-cb-live">
      <p className="mkt-muted">{t("channels.live.help")}</p>
      {inherited && <p className="mkt-muted">{t("channels.live.inherited")}</p>}
      {mine.length === 0 && <p className="mkt-muted">{t("channels.live.none")}</p>}
      {mine.map(([row, i]) => {
        const problems = liveProblems(row);
        const allDays = slotDays({ days: row.days });
        return (
          <fieldset
            key={i}
            className={`mkt-cb-live__row${focus === i ? " mkt-cb-live__row--focus" : ""}`}
            data-testid={`live-row-${i}`}
          >
            <legend>{row.title || row.url || t("channels.live.untitled")}</legend>
            <Input
              label={t("channels.live.url")}
              value={row.url ?? ""}
              placeholder="https://www.youtube.com/@aljazeera/live"
              autoFocus={focus === i}
              error={
                problems.includes("channels.live.err.url")
                  ? t("channels.live.err.url")
                  : problems.includes("channels.live.err.urlFormat")
                    ? t("channels.live.err.urlFormat")
                    : undefined
              }
              onChange={(e) => update(i, { url: e.target.value })}
            />
            <Input
              label={t("channels.live.titleField")}
              value={row.title ?? ""}
              onChange={(e) => update(i, { title: e.target.value })}
            />
            <div className="mkt-cb-row">
              <Input
                label={t("channels.live.start")}
                type="time"
                value={row.start ?? "00:00"}
                onChange={(e) => update(i, { start: e.target.value || "00:00" })}
              />
              <Input
                label={t("channels.live.end")}
                type="time"
                value={row.end ?? "00:00"}
                description={t("channels.live.endHint")}
                onChange={(e) => update(i, { end: e.target.value || "00:00" })}
              />
              <Input
                label={t("channels.live.every")}
                type="number"
                min={1}
                value={String(row.every_minutes ?? 60)}
                error={
                  problems.includes("channels.live.err.every")
                    ? t("channels.live.err.every")
                    : undefined
                }
                onChange={(e) => update(i, { every_minutes: Number(e.target.value) })}
              />
              <Input
                label={t("channels.live.minutes")}
                type="number"
                min={1}
                value={String(row.minutes ?? 10)}
                error={
                  problems.includes("channels.live.err.minutes")
                    ? t("channels.live.err.minutes")
                    : undefined
                }
                onChange={(e) => update(i, { minutes: Number(e.target.value) })}
              />
            </div>
            <DayChips
              label={t("channels.live.days")}
              value={allDays}
              onChange={(days) => update(i, { days: days.length === 7 ? [] : days })}
            />
            <div className="mkt-cb-slot__actions">
              <Button
                size="sm"
                variant="destructive"
                onClick={() => onChange(rows.filter((_, j) => j !== i))}
              >
                {t("channels.live.remove")}
              </Button>
            </div>
          </fieldset>
        );
      })}
      <Button size="sm" onClick={() => onChange([...rows, newLiveRow(number)])}>
        {t("channels.live.add")}
      </Button>
    </div>
  );
}
