// The weekly day × time grid of a schedule channel: seven columns of 24
// hours, each slot a block as tall as it is long, coloured by what it plays
// (a plan lane, a series, a collection, filler only, a live broadcast).
// Clicking a block opens it; clicking free time adds a slot there.
import type { MouseEvent } from "react";
import { useI18n } from "../../../lib/i18n";
import {
  DAYS,
  DAY_MINUTES,
  fromMinutes,
  layoutLive,
  layoutSlots,
  type Day,
  type GridBlock,
  type LiveRow,
  type Slot,
} from "./schedule";

const HOUR_MARKS = [0, 3, 6, 9, 12, 15, 18, 21];

export function ScheduleGrid({
  slots,
  live,
  number,
  weekly,
  selected,
  onSelect,
  onSelectLive,
  onAdd,
}: {
  slots: Slot[];
  live: LiveRow[];
  number: number;
  weekly: boolean;
  selected: number | null;
  onSelect: (index: number) => void;
  onSelectLive: (index: number) => void;
  onAdd: (minute: number, day?: Day) => void;
}) {
  const { t } = useI18n();
  const blocks = layoutSlots(slots);
  const liveBlocks = layoutLive(live, number);

  function addAt(e: MouseEvent<HTMLDivElement>, day: Day) {
    if (e.target !== e.currentTarget) return; // a block was clicked
    const rect = e.currentTarget.getBoundingClientRect();
    const frac = rect.height > 0 ? (e.clientY - rect.top) / rect.height : 0;
    onAdd(Math.max(0, Math.min(DAY_MINUTES - 30, frac * DAY_MINUTES)), weekly ? day : undefined);
  }

  function label(b: GridBlock) {
    const what = b.live
      ? t("channels.grid.live", { title: b.label })
      : b.label || t(`channels.source.kind.${b.cls}`);
    return t("channels.grid.blockLabel", {
      day: t(`channels.day.long.${b.day}`),
      start: fromMinutes(b.start),
      end: fromMinutes(b.end),
      what,
    });
  }

  return (
    <div className="mkt-cb-grid" role="group" aria-label={t("channels.grid.title")}>
      <div className="mkt-cb-grid__corner" aria-hidden="true" />
      {DAYS.map((d) => (
        <div key={d} className="mkt-cb-grid__head" aria-hidden="true">
          {t(`channels.day.${d}`)}
        </div>
      ))}
      <div className="mkt-cb-grid__gutter" aria-hidden="true">
        {HOUR_MARKS.map((h) => (
          <span key={h} style={{ insetBlockStart: `${(h / 24) * 100}%` }}>
            {fromMinutes(h * 60)}
          </span>
        ))}
      </div>
      {DAYS.map((d) => (
        <div
          key={d}
          className="mkt-cb-grid__col"
          data-testid={`grid-day-${d}`}
          onClick={(e) => addAt(e, d)}
          title={t("channels.grid.addHint")}
        >
          {blocks
            .filter((b) => b.day === d)
            .map((b) => (
              <button
                key={`s${b.index}`}
                type="button"
                className={[
                  "mkt-cb-block",
                  `mkt-cb-block--${b.cls}`,
                  b.clash ? "mkt-cb-block--clash" : "",
                  selected === b.index ? "mkt-cb-block--on" : "",
                ].join(" ")}
                style={{
                  insetBlockStart: `${(b.start / DAY_MINUTES) * 100}%`,
                  blockSize: `${((b.end - b.start) / DAY_MINUTES) * 100}%`,
                }}
                aria-label={label(b)}
                aria-pressed={selected === b.index}
                onClick={() => onSelect(b.index)}
              >
                <span className="mkt-cb-block__time">{fromMinutes(b.start)}</span>
                <span className="mkt-cb-block__name">
                  {b.label || t(`channels.source.kind.${b.cls}`)}
                </span>
              </button>
            ))}
          {liveBlocks
            .filter((b) => b.day === d)
            .map((b, i) => (
              <button
                key={`l${b.index}-${i}`}
                type="button"
                className="mkt-cb-block mkt-cb-block--live"
                style={{
                  insetBlockStart: `${(b.start / DAY_MINUTES) * 100}%`,
                  blockSize: `max(4px, ${((b.end - b.start) / DAY_MINUTES) * 100}%)`,
                }}
                aria-label={label(b)}
                onClick={() => onSelectLive(b.index)}
              />
            ))}
        </div>
      ))}
    </div>
  );
}

/** The colour key under the grid. */
export function GridLegend() {
  const { t } = useI18n();
  return (
    <ul className="mkt-cb-legend" aria-label={t("channels.grid.legend")}>
      {(["lane", "series", "collection", "filler", "live"] as const).map((k) => (
        <li key={k}>
          <span className={`mkt-cb-swatch mkt-cb-block--${k}`} aria-hidden="true" />
          {t(`channels.source.kind.${k}`)}
        </li>
      ))}
    </ul>
  );
}
