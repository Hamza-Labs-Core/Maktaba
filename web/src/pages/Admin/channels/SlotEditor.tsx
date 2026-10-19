// One slot of the grid: its time range, its days (weekly grids), what it
// plays (a plan lane, a series, a collection, filler only — or a live
// broadcast, which becomes a `tv.live` row), in which order, and the filler
// pool its gaps take first. Split and delete live here too.
import { useState } from "react";
import { Button } from "@ds/components/Button/Button";
import { Input } from "@ds/components/Input/Input";
import { Select } from "@ds/components/Select/Select";
import { useI18n } from "../../../lib/i18n";
import type { FillerPool, SourceHit, SourceKind } from "../../../lib/channels";
import { DayChips, SourceSearch } from "./pickers";
import {
  DAY_MINUTES,
  endMinutes,
  fromMinutes,
  slotDays,
  slotOrder,
  slotProblems,
  sourceLabel,
  toMinutes,
  type LiveRow,
  type Slot,
} from "./schedule";

type Choice = "lane" | "series" | "collection" | "filler" | "live";
const CHOICES: Choice[] = ["lane", "series", "collection", "filler", "live"];

function choiceOf(slot: Slot): Choice {
  const t = slot.source?.type;
  if (slot.kind === "flex" || !t || t === "filler") return "filler";
  return t === "lane" || t === "series" || t === "collection" ? t : "filler";
}

/** The time input value for a slot end ("24:00" shows as 00:00, the end of the day). */
function timeValue(s: string): string {
  return s === "24:00" ? "00:00" : s;
}

export function SlotEditor({
  slot,
  index,
  all,
  weekly,
  pools,
  onChange,
  onDelete,
  onSplit,
  onMakeLive,
  onClose,
}: {
  slot: Slot;
  index: number;
  all: Slot[];
  weekly: boolean;
  pools: FillerPool[];
  onChange: (slot: Slot) => void;
  onDelete: () => void;
  onSplit: () => void;
  onMakeLive: (row: LiveRow) => void;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const [choice, setChoice] = useState<Choice>(choiceOf(slot));
  const [liveUrl, setLiveUrl] = useState("");
  const [liveTitle, setLiveTitle] = useState("");
  const problems = slotProblems(slot, all, index);
  const start = toMinutes(slot.start) ?? 0;
  const end = endMinutes(slot.end) ?? DAY_MINUTES;

  function setTime(key: "start" | "end", v: string) {
    if (!v) return;
    onChange({ ...slot, [key]: key === "end" && v === "00:00" ? "24:00" : v });
  }

  function pickChoice(c: Choice) {
    setChoice(c);
    if (c === "filler") {
      const { kind: _k, ...rest } = slot;
      void _k;
      onChange({ ...rest, source: { type: "filler" } });
    } else if (c !== "live" && slot.source?.type !== c) {
      onChange({ ...slot, source: { type: c, order: slotOrder(slot) } });
    }
  }

  function pick(hit: SourceHit) {
    const key =
      hit.kind === "lane" ? "lane" : hit.kind === "series" ? "series_id" : "collection_id";
    const base = slot.source?.type === hit.kind ? slot.source : { type: hit.kind };
    onChange({
      ...slot,
      source: { ...base, type: hit.kind, [key]: hit.id, name: hit.name, order: slotOrder(slot) },
    });
  }

  const pickedId =
    slot.source?.type === "lane"
      ? slot.source.lane
      : slot.source?.type === "series"
        ? slot.source.series_id
        : slot.source?.type === "collection"
          ? slot.source.collection_id
          : undefined;

  const liveOk = /^(https?|rtmps?|rtsp|udp|srt):\/\//i.test(liveUrl.trim());

  return (
    <section className="mkt-cb-slot" aria-label={t("channels.slot.title")}>
      <header className="mkt-cb-slot__head">
        <h3>
          {t("channels.slot.title")} · {fromMinutes(start)}–{fromMinutes(end)}
        </h3>
        <Button size="sm" variant="ghost" onClick={onClose}>
          {t("common.close")}
        </Button>
      </header>
      <div className="mkt-cb-row">
        <Input
          label={t("channels.slot.start")}
          type="time"
          step={300}
          value={timeValue(slot.start)}
          onChange={(e) => setTime("start", e.target.value)}
        />
        <Input
          label={t("channels.slot.end")}
          type="time"
          step={300}
          value={timeValue(slot.end)}
          description={t("channels.slot.endHint")}
          onChange={(e) => setTime("end", e.target.value)}
        />
      </div>
      {weekly && (
        <DayChips
          label={t("channels.slot.days")}
          value={slotDays(slot)}
          onChange={(days) => onChange({ ...slot, days: days.length ? days : undefined })}
        />
      )}

      <fieldset className="mkt-cb-choice">
        <legend>{t("channels.slot.plays")}</legend>
        <div role="radiogroup" aria-label={t("channels.slot.plays")} className="mkt-cb-seg">
          {CHOICES.map((c) => (
            <button
              key={c}
              type="button"
              role="radio"
              aria-checked={choice === c}
              className={`mkt-cb-seg__btn mkt-cb-block--${c}${choice === c ? " mkt-cb-seg__btn--on" : ""}`}
              onClick={() => pickChoice(c)}
            >
              {t(`channels.source.kind.${c}`)}
            </button>
          ))}
        </div>
      </fieldset>

      {(choice === "lane" || choice === "series" || choice === "collection") && (
        <>
          {pickedId && (
            <p className="mkt-cb-picked">
              {t("channels.slot.picked")}: <strong>{sourceLabel(slot) || pickedId}</strong>
            </p>
          )}
          <SourceSearch
            kind={choice as SourceKind}
            selectedId={pickedId}
            label={t(`channels.source.search.${choice}`)}
            onPick={pick}
          />
          <Select
            label={t("channels.slot.order")}
            value={slotOrder(slot)}
            options={[
              { value: "next", label: t("channels.slot.order.next") },
              { value: "shuffle", label: t("channels.slot.order.shuffle") },
            ]}
            onChange={(e) =>
              onChange({
                ...slot,
                source: { ...(slot.source ?? { type: choice }), order: e.target.value },
              })
            }
          />
        </>
      )}

      {choice === "filler" && <p className="mkt-muted">{t("channels.slot.fillerOnly")}</p>}

      {choice === "live" ? (
        <div className="mkt-cb-live-new">
          <p className="mkt-muted">{t("channels.slot.liveHint")}</p>
          <Input
            label={t("channels.live.url")}
            value={liveUrl}
            placeholder="https://www.youtube.com/@aljazeera/live"
            error={liveUrl && !liveOk ? t("channels.live.err.urlFormat") : undefined}
            onChange={(e) => setLiveUrl(e.target.value)}
          />
          <Input
            label={t("channels.live.titleField")}
            value={liveTitle}
            onChange={(e) => setLiveTitle(e.target.value)}
          />
          <Button
            size="sm"
            disabled={!liveOk}
            onClick={() =>
              onMakeLive({
                url: liveUrl.trim(),
                title: liveTitle.trim(),
                start: slot.start,
                end: slot.end === "24:00" ? "00:00" : slot.end,
                every_minutes: Math.max(1, end - start),
                minutes: Math.max(1, end - start),
                days: weekly ? slotDays(slot) : [],
              })
            }
          >
            {t("channels.slot.makeLive")}
          </Button>
        </div>
      ) : (
        <Select
          label={t("channels.slot.fillerPool")}
          description={t("channels.slot.fillerPoolHint")}
          value={slot.filler_pool ?? ""}
          options={[
            { value: "", label: t("channels.slot.fillerPool.none") },
            ...pools.map((p) => ({ value: p.external_key || p.id, label: p.name })),
          ]}
          onChange={(e) => onChange({ ...slot, filler_pool: e.target.value || null })}
        />
      )}

      {problems.length > 0 && (
        <ul className="mkt-cb-problems" role="alert">
          {problems.map((p) => (
            <li key={p}>{t(p)}</li>
          ))}
        </ul>
      )}

      <div className="mkt-cb-slot__actions">
        <Button size="sm" variant="secondary" onClick={onSplit} disabled={end - start < 2}>
          {t("channels.slot.split")}
        </Button>
        <Button size="sm" variant="destructive" onClick={onDelete}>
          {t("channels.slot.delete")}
        </Button>
      </div>
    </section>
  );
}
