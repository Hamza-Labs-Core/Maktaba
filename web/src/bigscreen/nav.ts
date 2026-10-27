// Epic 46 — moving between big-screen screens.
//
// Every move inside big screen carries how deep it is (location.state.bs):
// Back goes one entry back while there is one; on a top screen (a tab, or a
// page opened from a pasted link) it goes Home; on Home it asks to leave.
import { useCallback } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { BIG_BASE } from "../lib/bigScreen";

export { BIG_BASE };

export const bigPath = {
  home: BIG_BASE,
  libraries: `${BIG_BASE}/libraries`,
  library: (id: string) => `${BIG_BASE}/library/${encodeURIComponent(id)}`,
  seriesList: `${BIG_BASE}/series`,
  series: (id: string) => `${BIG_BASE}/series/${encodeURIComponent(id)}`,
  video: (id: string) => `${BIG_BASE}/video/${encodeURIComponent(id)}`,
  watch: (id: string, opts: { t?: number; series?: string | null } = {}) => {
    const q = new URLSearchParams();
    if (opts.t && opts.t > 0) q.set("t", String(Math.floor(opts.t)));
    if (opts.series) q.set("series", opts.series);
    const qs = q.toString();
    return `${BIG_BASE}/watch/${encodeURIComponent(id)}${qs ? `?${qs}` : ""}`;
  },
  search: `${BIG_BASE}/search`,
  live: `${BIG_BASE}/live`,
  channel: (n: number) => `${BIG_BASE}/live/${n}`,
  settings: `${BIG_BASE}/settings`,
};

export function depthOf(state: unknown): number {
  const d = (state as { bs?: unknown } | null)?.bs;
  return typeof d === "number" && d > 0 ? d : 0;
}

/** What Back does from `pathname` at `depth`: one entry back, Home, or ask to leave. */
export function backTarget(pathname: string, depth: number): "history" | "home" | "exit" {
  if (depth > 0) return "history";
  const p = pathname.replace(/\/+$/, "");
  return p === BIG_BASE ? "exit" : "home";
}

export function useBigNav() {
  const nav = useNavigate();
  const location = useLocation();
  const depth = depthOf(location.state);

  /** Opens a screen below this one (Back returns here). */
  const go = useCallback(
    (to: string, opts: { replace?: boolean } = {}) =>
      nav(to, { state: { bs: opts.replace ? depth : depth + 1 }, replace: opts.replace }),
    [nav, depth]
  );

  /** Opens a top screen (a tab): Back from it goes Home. */
  const top = useCallback((to: string) => nav(to, { state: { bs: 0 } }), [nav]);

  return { go, top, depth };
}
