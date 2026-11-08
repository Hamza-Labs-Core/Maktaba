// The dashboards (Epic 53 story 53.12): GET /api/admin/dashboards/{board}?range= answers a
// board's series in one call (≤ 50 series, ≤ 2,000 points each). Use also lists the week's
// top videos (counts and titles only). The Overview draws totals across the machines and
// the five most in need of attention (a joined machine that is offline first), names them
// all and the offline ones in `machines`, and answers one machine on its own with `machine=`.
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
  };
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

/** A series' legend: its labels' values ("core.api · home"), or the name without labels. */
export function seriesLabel(s: BoardSeries): string {
  if (!s.labels) return "";
  return s.labels
    .split(",")
    .map((kv) => kv.split("=").slice(1).join("="))
    .filter(Boolean)
    .join(" · ");
}
