// The time-slot grid of a `schedule` channel, as the scheduler reads it
// (pipeline channels/planner/schedule.py): `mode_config.slots` (and
// `mode_config.ramadan.slots` during Ramadan), each
//
//   { start: "HH:MM", end: "HH:MM" | "24:00", days?: ["mon", ...],
//     source: { type: "lane", lane, order? } | { type: "series", series_id, order? }
//           | { type: "collection", collection_id, order? } | { type: "filler" },
//     kind?: "flex", filler_pool?: string, ... }
//
// Absent `days` = every day. Keys the editor does not know (daypart, the
// lane's shows and pattern, …) are kept as they are.
//
// Live broadcasts are not slots: they are `rules.tv.live` rows naming the
// channel by its number (from `start` every `every_minutes` until `end`,
// `minutes` long, on `days`). The grid shows both.
//
// Everything here is pure, so the grid editor is tested without a DOM.

export const DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
export type Day = (typeof DAYS)[number];
export const DAY_MINUTES = 24 * 60;

export type SlotSourceType = "lane" | "series" | "collection" | "filler";
export type SlotOrder = "next" | "shuffle";

export interface SlotSource {
  type: SlotSourceType | string;
  lane?: string;
  series_id?: string;
  collection_id?: string;
  order?: string;
  /** the picked thing's name, kept so the grid can label it */
  name?: string;
  [key: string]: unknown;
}

export interface Slot {
  start: string;
  end: string;
  days?: string[];
  source?: SlotSource;
  kind?: string;
  filler_pool?: string | null;
  [key: string]: unknown;
}

export interface LiveRow {
  channel?: number;
  url?: string;
  title?: string;
  start?: string;
  end?: string;
  every_minutes?: number;
  minutes?: number;
  days?: string[];
  [key: string]: unknown;
}

/** "HH:MM" → minutes since midnight; "24:00" → 1440; invalid → null. */
export function toMinutes(s: unknown): number | null {
  if (typeof s !== "string") return null;
  const m = /^(\d{1,2}):(\d{2})$/.exec(s.trim());
  if (!m) return null;
  const h = Number(m[1]);
  const min = Number(m[2]);
  if (min > 59 || h > 24 || (h === 24 && min !== 0)) return null;
  return h * 60 + min;
}

/** minutes since midnight → "HH:MM" (1440 → "24:00"). */
export function fromMinutes(m: number): string {
  const v = Math.max(0, Math.min(DAY_MINUTES, Math.round(m)));
  const h = Math.floor(v / 60);
  return `${String(h).padStart(2, "0")}:${String(v % 60).padStart(2, "0")}`;
}

/** A slot's end in minutes: "24:00" and "00:00" both mean the end of the day. */
export function endMinutes(s: string): number | null {
  const m = toMinutes(s);
  if (m === null) return null;
  return m === 0 ? DAY_MINUTES : m;
}

/** The days a slot airs on (absent / empty / unknown → every day). */
export function slotDays(slot: Pick<Slot, "days">): Day[] {
  const got = (slot.days ?? [])
    .map((d) => String(d).trim().toLowerCase().slice(0, 3))
    .filter((d): d is Day => (DAYS as readonly string[]).includes(d));
  return got.length ? DAYS.filter((d) => got.includes(d)) : [...DAYS];
}

/** True when any slot names its days (the grid is a weekly one). */
export function isWeekly(slots: Slot[]): boolean {
  return slots.some((s) => (s.days ?? []).length > 0 && slotDays(s).length < 7);
}

/** What a slot plays, for colour and label. */
export type SourceClass = "lane" | "series" | "collection" | "filler" | "live" | "other";

export function sourceClass(slot: Slot): SourceClass {
  const t = slot.source?.type;
  if (slot.kind === "flex" || t === "filler" || !t) return "filler";
  if (t === "lane" || t === "series" || t === "collection") return t;
  return "other";
}

export function sourceLabel(slot: Slot): string {
  const src = slot.source;
  if (!src) return "";
  if (typeof src.name === "string" && src.name) return src.name;
  if (src.type === "lane") {
    const shows = Array.isArray(src.shows) ? (src.shows as unknown[]).map(String) : [];
    return shows.length ? shows.join(" · ") : String(src.lane ?? "");
  }
  if (src.type === "series") return String(src.series_id ?? "");
  if (src.type === "collection") return String(src.collection_id ?? "");
  return "";
}

export function slotOrder(slot: Slot): SlotOrder {
  return slot.source?.order === "shuffle" ? "shuffle" : "next";
}

/** One block of the 7 × 24 h grid. */
export interface GridBlock {
  /** index into the slots (or live rows, for `live`) */
  index: number;
  day: Day;
  start: number;
  end: number;
  cls: SourceClass;
  label: string;
  live: boolean;
  /** another block of that day overlaps this one */
  clash: boolean;
}

/** The slots laid out per day, in minutes, with overlaps marked. */
export function layoutSlots(slots: Slot[]): GridBlock[] {
  const out: GridBlock[] = [];
  slots.forEach((slot, index) => {
    const start = toMinutes(slot.start);
    const end = endMinutes(slot.end);
    if (start === null || end === null || end <= start) return;
    for (const day of slotDays(slot)) {
      out.push({
        index,
        day,
        start,
        end,
        cls: sourceClass(slot),
        label: sourceLabel(slot),
        live: false,
        clash: false,
      });
    }
  });
  for (const day of DAYS) {
    const mine = out.filter((b) => b.day === day).sort((a, b) => a.start - b.start);
    for (let i = 0; i < mine.length; i++) {
      for (let j = i + 1; j < mine.length && mine[j].start < mine[i].end; j++) {
        mine[i].clash = true;
        mine[j].clash = true;
      }
    }
  }
  return out;
}

/** The live broadcasts of channel `number`, as grid blocks (one per airing). */
export function layoutLive(rows: LiveRow[], number: number): GridBlock[] {
  const out: GridBlock[] = [];
  rows.forEach((row, index) => {
    if (Number(row.channel) !== number) return;
    const minutes = Number(row.minutes ?? 10);
    const every = Math.max(1, Number(row.every_minutes ?? 60));
    const first = toMinutes(row.start ?? "00:00") ?? 0;
    let last = endMinutes(row.end ?? "00:00") ?? DAY_MINUTES;
    if (last <= first) last += DAY_MINUTES;
    if (!(minutes > 0)) return;
    for (const day of slotDays({ days: row.days })) {
      for (let m = first; m < last && m < DAY_MINUTES; m += every) {
        out.push({
          index,
          day,
          start: m,
          end: Math.min(DAY_MINUTES, m + minutes),
          cls: "live",
          label: String(row.title || row.url || ""),
          live: true,
          clash: false,
        });
      }
    }
  });
  return out;
}

/** Problems with one slot, as i18n keys (empty: fine). */
export function slotProblems(slot: Slot, all: Slot[], index: number): string[] {
  const out: string[] = [];
  const start = toMinutes(slot.start);
  const end = endMinutes(slot.end);
  if (start === null) out.push("channels.slot.err.start");
  if (end === null) out.push("channels.slot.err.end");
  if (start !== null && end !== null && end <= start) out.push("channels.slot.err.order");
  const src = slot.source;
  if (src?.type === "lane" && !src.lane) out.push("channels.slot.err.source");
  if (src?.type === "series" && !src.series_id) out.push("channels.slot.err.source");
  if (src?.type === "collection" && !src.collection_id) out.push("channels.slot.err.source");
  if (start !== null && end !== null && end > start) {
    const days = slotDays(slot);
    const clash = all.some((o, j) => {
      if (j === index) return false;
      const os = toMinutes(o.start);
      const oe = endMinutes(o.end);
      if (os === null || oe === null) return false;
      return os < end && start < oe && slotDays(o).some((d) => days.includes(d));
    });
    if (clash) out.push("channels.slot.err.overlap");
  }
  return out;
}

/** A new slot at `start` (minutes) on `day` (weekly grids) or every day. */
export function newSlot(start: number, opts: { day?: Day; length?: number } = {}): Slot {
  const s = Math.max(0, Math.min(DAY_MINUTES - 30, Math.floor(start / 30) * 30));
  const e = Math.min(DAY_MINUTES, s + (opts.length ?? 60));
  const slot: Slot = { start: fromMinutes(s), end: fromMinutes(e), source: { type: "filler" } };
  if (opts.day) slot.days = [opts.day];
  return slot;
}

/** Splits slot `index` at `at` minutes (default: its middle, on the half hour). */
export function splitSlot(slots: Slot[], index: number, at?: number): Slot[] {
  const slot = slots[index];
  const start = toMinutes(slot?.start);
  const end = endMinutes(slot?.end ?? "");
  if (!slot || start === null || end === null || end - start < 2) return slots;
  let cut = at ?? start + Math.round((end - start) / 2 / 30) * 30;
  if (cut <= start || cut >= end) cut = start + Math.floor((end - start) / 2);
  const a: Slot = { ...slot, end: fromMinutes(cut) };
  const b: Slot = { ...structuredCloneSafe(slot), start: fromMinutes(cut) };
  return [...slots.slice(0, index), a, b, ...slots.slice(index + 1)];
}

export function removeSlot(slots: Slot[], index: number): Slot[] {
  return slots.filter((_, i) => i !== index);
}

export function replaceSlot(slots: Slot[], index: number, slot: Slot): Slot[] {
  return slots.map((s, i) => (i === index ? slot : s));
}

/** Weekly → daily: every slot airs every day (`days` dropped). */
export function toDaily(slots: Slot[]): Slot[] {
  return slots.map((s) => {
    const { days: _drop, ...rest } = s;
    void _drop;
    return rest as Slot;
  });
}

/** Daily → weekly: every slot names all seven days, ready to be narrowed. */
export function toWeekly(slots: Slot[]): Slot[] {
  return slots.map((s) => ({ ...s, days: slotDays(s) }));
}

/** Slots sorted by start time (the order the planner reads them in is first-match). */
export function sortSlots(slots: Slot[]): Slot[] {
  return [...slots].sort((a, b) => (toMinutes(a.start) ?? 0) - (toMinutes(b.start) ?? 0));
}

function structuredCloneSafe<T>(v: T): T {
  return JSON.parse(JSON.stringify(v)) as T;
}

/** A live row's problems, as i18n keys. */
export function liveProblems(row: LiveRow): string[] {
  const out: string[] = [];
  const url = String(row.url ?? "").trim();
  if (!url) out.push("channels.live.err.url");
  else if (!/^(https?|rtmps?|rtsp|udp|srt):\/\//i.test(url))
    out.push("channels.live.err.urlFormat");
  if (row.start !== undefined && toMinutes(row.start) === null) out.push("channels.slot.err.start");
  if (row.end !== undefined && row.end !== "" && toMinutes(row.end) === null)
    out.push("channels.slot.err.end");
  const minutes = Number(row.minutes ?? 10);
  if (!Number.isFinite(minutes) || minutes < 1 || minutes > DAY_MINUTES)
    out.push("channels.live.err.minutes");
  const every = Number(row.every_minutes ?? 60);
  if (!Number.isFinite(every) || every < 1 || every > 7 * DAY_MINUTES)
    out.push("channels.live.err.every");
  return out;
}

/** "1 h 30 min" style duration from milliseconds (the caller localises the units). */
export function hoursMinutes(ms: number): { h: number; m: number } {
  const total = Math.max(0, Math.round(ms / 60_000));
  return { h: Math.floor(total / 60), m: total % 60 };
}
