// The Schedule section of the channel editor for a `schedule` channel: the
// regular grid and (when the channel has one) the Ramadan grid, daily or
// weekly, with the selected slot's editor beside the grid.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { useI18n } from "../../../lib/i18n";
import type { FillerPool } from "../../../lib/channels";
import { GridLegend, ScheduleGrid } from "./ScheduleGrid";
import { SlotEditor } from "./SlotEditor";
import {
  isWeekly,
  newSlot,
  removeSlot,
  replaceSlot,
  sortSlots,
  splitSlot,
  toDaily,
  toWeekly,
  type Day,
  type LiveRow,
  type Slot,
} from "./schedule";

type Grid = "regular" | "ramadan";

export function ScheduleSection({
  config,
  setConfig,
  live,
  number,
  pools,
  onAddLive,
  onOpenLive,
}: {
  config: Record<string, unknown>;
  setConfig: (key: string, value: unknown) => void;
  live: LiveRow[];
  number: number;
  pools: FillerPool[];
  onAddLive: (row: LiveRow) => void;
  onOpenLive: (index: number) => void;
}) {
  const { t } = useI18n();
  const ramadan =
    config.ramadan && typeof config.ramadan === "object"
      ? (config.ramadan as Record<string, unknown>)
      : null;
  const [grid, setGrid] = useState<Grid>("regular");
  const [selected, setSelected] = useState<number | null>(null);

  // a wrong-typed value (an object, a string) is treated as no slots
  const raw = grid === "ramadan" ? ramadan?.slots : config.slots;
  const slots: Slot[] = Array.isArray(raw) ? (raw as Slot[]) : [];
  const [weeklyPref, setWeeklyPref] = useState<boolean>(isWeekly(slots));
  const weekly = weeklyPref || isWeekly(slots);

  function write(next: Slot[]) {
    if (grid === "ramadan") {
      setConfig("ramadan", {
        activate: { hijri_month: 9 },
        ...(ramadan ?? {}),
        slots: next,
      });
    } else {
      setConfig("slots", next);
    }
  }

  function add(minute: number, day?: Day) {
    const next = sortSlots([...slots, newSlot(minute, { day })]);
    write(next);
    const s = newSlot(minute, { day });
    setSelected(next.findIndex((x) => x.start === s.start && x.end === s.end));
  }

  function setMode(w: boolean) {
    setWeeklyPref(w);
    write(w ? toWeekly(slots) : toDaily(slots));
  }

  const slot = selected !== null ? slots[selected] : undefined;

  return (
    <div className="mkt-cb-schedule">
      <div className="mkt-cb-toolbar">
        {ramadan ? (
          <div role="tablist" aria-label={t("channels.grid.which")} className="mkt-cb-seg">
            {(["regular", "ramadan"] as const).map((g) => (
              <button
                key={g}
                type="button"
                role="tab"
                aria-selected={grid === g}
                className={`mkt-cb-seg__btn${grid === g ? " mkt-cb-seg__btn--on" : ""}`}
                onClick={() => {
                  setGrid(g);
                  setSelected(null);
                }}
              >
                {t(`channels.grid.${g}`)}
              </button>
            ))}
          </div>
        ) : (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => setConfig("ramadan", { activate: { hijri_month: 9 }, slots: [] })}
          >
            {t("channels.grid.addRamadan")}
          </Button>
        )}
        <div role="radiogroup" aria-label={t("channels.grid.repeat")} className="mkt-cb-seg">
          {[false, true].map((w) => (
            <button
              key={String(w)}
              type="button"
              role="radio"
              aria-checked={weekly === w}
              className={`mkt-cb-seg__btn${weekly === w ? " mkt-cb-seg__btn--on" : ""}`}
              onClick={() => setMode(w)}
            >
              {t(w ? "channels.grid.weekly" : "channels.grid.daily")}
            </button>
          ))}
        </div>
        <Button size="sm" onClick={() => add(slots.length ? 20 * 60 : 0)}>
          {t("channels.grid.addSlot")}
        </Button>
      </div>
      {grid === "ramadan" && <p className="mkt-muted">{t("channels.grid.ramadanHint")}</p>}
      {slots.length === 0 && <p className="mkt-form__warn">{t("channels.grid.empty")}</p>}
      <div className="mkt-cb-schedule__body">
        <div className="mkt-cb-schedule__grid">
          <ScheduleGrid
            slots={slots}
            live={grid === "regular" ? live : []}
            number={number}
            weekly={weekly}
            selected={selected}
            onSelect={setSelected}
            onSelectLive={onOpenLive}
            onAdd={add}
          />
          <GridLegend />
        </div>
        {slot && selected !== null && (
          <SlotEditor
            key={`${grid}-${selected}`}
            slot={slot}
            index={selected}
            all={slots}
            weekly={weekly}
            pools={pools}
            onChange={(s) => write(replaceSlot(slots, selected, s))}
            onDelete={() => {
              write(removeSlot(slots, selected));
              setSelected(null);
            }}
            onSplit={() => write(splitSlot(slots, selected))}
            onMakeLive={(row) => {
              onAddLive({ ...row, channel: number });
              write(removeSlot(slots, selected));
              setSelected(null);
            }}
            onClose={() => setSelected(null)}
          />
        )}
      </div>
    </div>
  );
}
