// What a library's queue row and Epic 47 summary say it needs, in one shape the
// library dashboard's tiles, the Queues page's attention list and the library
// page share: overall progress, its state, what needs an admin, and the
// dashboard's filters and sorts. Pure; no React.
import type { LibraryQueueRow, OfflineSummary } from "../../lib/queueApi";

export type LibraryState = "failing" | "working" | "waiting" | "done" | "empty";
/** A library's YouTube: no sources, every source off, some on, or a source that did not list cleanly. */
export type YouTubeState = "none" | "off" | "on" | "problem";
export type Reason = "failing" | "youtube" | "duplicates" | "convert";

export interface LibraryHealth {
  row: LibraryQueueRow;
  id: string;
  name: string;
  /** stage units done, of the units every shown stage has to do */
  done: number;
  total: number;
  running: number;
  waiting: number;
  failed: number;
  /** stages with at least one failed item */
  failingStages: number;
  /** 0..100, floored, so 99.6 % never reads as finished */
  pct: number;
  state: LibraryState;
  /** null without the summary (it is the admins') */
  youtube: YouTubeState | null;
  sources: number;
  sourcesOn: number;
  toConvert: number;
  converting: number;
  duplicates: number;
  /** what needs an admin, most urgent first */
  reasons: Reason[];
  /** 0 = most urgent; the "Needs attention" sort */
  tier: number;
}

export function youtubeState(s: OfflineSummary | undefined): YouTubeState | null {
  if (!s) return null;
  const y = s.youtube;
  if (!y || y.sources === 0) return "none";
  if ((y.problems ?? []).length > 0) return "problem";
  return y.sources_on > 0 ? "on" : "off";
}

export function libraryHealth(row: LibraryQueueRow, s?: OfflineSummary): LibraryHealth {
  let done = 0;
  let total = 0;
  let running = 0;
  let waiting = 0;
  let failed = 0;
  let failingStages = 0;
  for (const c of row.strip ?? []) {
    if (c.total > 0) {
      total += c.total;
      done += Math.min(c.done, c.total);
    }
    running += c.running;
    waiting += c.pending;
    failed += c.failed;
    if (c.failed > 0) failingStages += 1;
  }
  const pct = total > 0 ? Math.floor((done / total) * 100) : 0;
  const state: LibraryState =
    failed > 0
      ? "failing"
      : running > 0
        ? "working"
        : waiting > 0
          ? "waiting"
          : total === 0
            ? "empty"
            : "done";
  const youtube = youtubeState(s);
  const toConvert = s?.not_mp4 ?? 0;
  const duplicates = s?.duplicates ?? 0;
  const reasons: Reason[] = [];
  if (failed > 0) reasons.push("failing");
  if (youtube === "problem") reasons.push("youtube");
  if (duplicates > 0) reasons.push("duplicates");
  if (toConvert > 0) reasons.push("convert");
  const order: Record<Reason, number> = { failing: 0, youtube: 1, duplicates: 2, convert: 3 };
  const tier = reasons.length
    ? order[reasons[0]]
    : state === "working" || state === "waiting"
      ? 4
      : 5;
  return {
    row,
    id: row.library_id,
    name: row.name,
    done,
    total,
    running,
    waiting,
    failed,
    failingStages,
    pct,
    state,
    youtube,
    sources: s?.youtube?.sources ?? 0,
    sourcesOn: s?.youtube?.sources_on ?? 0,
    toConvert,
    converting: s?.converting ?? 0,
    duplicates,
    reasons,
    tier,
  };
}

export const FILTERS = ["all", "attention", "failing", "convert", "youtubeOff"] as const;
export type Filter = (typeof FILTERS)[number];
export const SORTS = ["attention", "name", "failing", "size", "recent"] as const;
export type Sort = (typeof SORTS)[number];

export function matches(h: LibraryHealth, f: Filter): boolean {
  switch (f) {
    case "attention":
      return h.reasons.length > 0;
    case "failing":
      return h.failed > 0;
    case "convert":
      return h.toConvert > 0;
    case "youtubeOff":
      return h.youtube === "off";
    default:
      return true;
  }
}

/** Libraries in the filter whose name holds the query (case and diacritics aside). */
export function filterLibraries(list: LibraryHealth[], f: Filter, query: string): LibraryHealth[] {
  const q = fold(query.trim());
  return list.filter((h) => matches(h, f) && (q === "" || fold(h.name).includes(q)));
}

export function filterCounts(list: LibraryHealth[]): Record<Filter, number> {
  const out = {} as Record<Filter, number>;
  for (const f of FILTERS) out[f] = list.filter((h) => matches(h, f)).length;
  return out;
}

export function sortLibraries(list: LibraryHealth[], sort: Sort, locale: string): LibraryHealth[] {
  const coll = new Intl.Collator(locale, { sensitivity: "base", numeric: true });
  const byName = (a: LibraryHealth, b: LibraryHealth) => coll.compare(a.name, b.name);
  const at = (h: LibraryHealth) =>
    h.row.last_activity_at ? Date.parse(h.row.last_activity_at) : 0;
  const cmp: Record<Sort, (a: LibraryHealth, b: LibraryHealth) => number> = {
    attention: (a, b) =>
      a.tier - b.tier || b.failed - a.failed || b.toConvert - a.toConvert || byName(a, b),
    name: byName,
    failing: (a, b) => b.failed - a.failed || byName(a, b),
    size: (a, b) => b.row.videos - a.row.videos || byName(a, b),
    recent: (a, b) => at(b) - at(a) || byName(a, b),
  };
  return [...list].sort(cmp[sort]);
}

/** Lower case without Arabic diacritics or tatweel, so "مُحاضرات" finds "محاضرات". */
function fold(s: string): string {
  return s.toLocaleLowerCase().replace(/[ً-ٰٟـ]/g, "");
}
