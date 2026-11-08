// The dashboards (Epic 53 story 53.12): GET /api/admin/dashboards/{board}?range= answers a
// board's series in one call (≤ 50 series, ≤ 2,000 points each). Use also lists the week's
// top videos (counts and titles only). The Overview draws totals across the machines and
// the five most in need of attention (a joined machine that is offline first), names them
// all and the offline ones in `machines`, and answers one machine on its own with `machine=`.
// `names` gives each machine id the answer mentions its name, as the Machines page shows it.
import { api } from "./api";

export const BOARDS = ["overview", "queue", "processing", "streaming", "storage", "use"] as const;
export type Board = (typeof BOARDS)[number];
export const RANGES = ["1h", "24h", "7d", "30d", "90d"] as const;
export type Range = (typeof RANGES)[number];

export interface BoardSeries {
  name: string;
  labels?: string;
  kind: "counter" | "gauge";
  points: [number, number][];
}

export interface BoardDoc {
  board: string;
  range: string;
  step: number;
  series: BoardSeries[];
  top?: { title: string; plays: number }[];
  /** Overview: the one machine asked for. */
  machine?: string;
  /** Overview: how many machines there are, the ones shown, all of them, and the joined
   * machines that are offline (ranked first among those shown). */
  machines?: { total: number; shown: string[]; all: string[]; offline: string[] };
  /** Lines the board's 50 left out. */
  omitted?: number;
  /** Overview: each machine id the answer mentions, named as the Machines page names it (an
   * id with no name named by itself; an older home sends none). */
  names?: Record<string, string>;
}

/** A machine's name for display: its name from `names`, else its id. */
export function machineName(names: Record<string, string> | undefined, id: string): string {
  const v = names?.[id];
  return typeof v === "string" && v.trim() !== "" ? v : id;
}

export async function getBoard(board: Board, range: Range, machine = ""): Promise<BoardDoc> {
  const q = machine && board === "overview" ? `&machine=${encodeURIComponent(machine)}` : "";
  const d = await api.get<BoardDoc>(
    `/api/admin/dashboards/${encodeURIComponent(board)}?range=${encodeURIComponent(range)}${q}`
  );
  const m = d?.machines;
  return {
    ...d,
    series: Array.isArray(d?.series) ? d.series : [],
    top: d?.top ?? [],
    machines: m
      ? {
          total: m.total ?? 0,
          shown: Array.isArray(m.shown) ? m.shown : [],
          all: Array.isArray(m.all) ? m.all : [],
          offline: Array.isArray(m.offline) ? m.offline : [],
        }
      : undefined,
    omitted: d?.omitted ?? 0,
    names: namesOf(d?.names),
  };
}

/** The answer's names, kept only where both id and name are text. */
function namesOf(v: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (v && typeof v === "object" && !Array.isArray(v)) {
    for (const [k, n] of Object.entries(v as Record<string, unknown>)) {
      if (typeof n === "string") out[k] = n;
    }
  }
  return out;
}

/** The range as milliseconds (the Open in Logs window). */
export const RANGE_MS: Record<Range, number> = {
  "1h": 3600_000,
  "24h": 86_400_000,
  "7d": 7 * 86_400_000,
  "30d": 30 * 86_400_000,
  "90d": 90 * 86_400_000,
};

/** Series of one name, one panel each, in the board's order. */
export function panels(series: BoardSeries[]): { name: string; series: BoardSeries[] }[] {
  const out: { name: string; series: BoardSeries[] }[] = [];
  for (const s of series) {
    const p = out.find((x) => x.name === s.name);
    if (p) p.series.push(s);
    else out.push({ name: s.name, series: [s] });
  }
  return out;
}

/** A series' legend: its labels' values ("core.api · home"), a machine named by `names`, or
 * "" without labels. */
export function seriesLabel(s: BoardSeries, names?: Record<string, string>): string {
  if (!s.labels) return "";
  return s.labels
    .split(",")
    .map((kv) => {
      const [k, ...rest] = kv.split("=");
      const v = rest.join("=");
      return k === "machine" && v ? machineName(names, v) : v;
    })
    .filter(Boolean)
    .join(" · ");
}
