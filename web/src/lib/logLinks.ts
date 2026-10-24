// Links from a failure to the logs that tell its whole story (update-attempts.md): the
// admin's log page (/admin/logs) filtered to the failure's time, or a joined machine's own
// logs on the Machines page (/admin/machines?logs=<node>), open on the failure's day.

/** How far around a failure the logs are shown: two minutes before it began, two after it ended. */
export const LOG_MARGIN_MS = 2 * 60_000;

/** The filters /admin/logs reads from its address. */
export interface LogFilters {
  q?: string;
  services?: string;
  level?: string;
  /** ISO time: older lines are hidden */
  since?: string;
  /** ISO time: newer lines are hidden */
  until?: string;
}

/** The time window around something that began at `at` and ended at `endedAt` (still
 * going: no end). An unreadable time gives no bound. */
export function logWindow(at?: string, endedAt?: string): Pick<LogFilters, "since" | "until"> {
  const out: Pick<LogFilters, "since" | "until"> = {};
  const start = at ? Date.parse(at) : NaN;
  if (Number.isFinite(start)) out.since = new Date(start - LOG_MARGIN_MS).toISOString();
  const end = endedAt ? Date.parse(endedAt) : NaN;
  if (Number.isFinite(end)) out.until = new Date(end + LOG_MARGIN_MS).toISOString();
  return out;
}

function query(params: Record<string, string | undefined>): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) p.set(k, v);
  const s = p.toString();
  return s ? `?${s}` : "";
}

/** /admin/logs with these filters. */
export function logsLink(f: LogFilters = {}): string {
  return `/admin/logs${query({ ...f })}`;
}

/** A joined machine's own logs on the Machines page (its Logs tab), from `since` (the day
 * of its files that holds it, when they have it). */
export function machineLogsLink(
  nodeId: string,
  f: Pick<LogFilters, "since" | "until"> = {}
): string {
  return `/admin/machines${query({ logs: nodeId, since: f.since, until: f.until })}`;
}

/** The filters in an address's query string (window.location.search). A time that does
 * not parse is left out. */
export function readLogFilters(search: string): LogFilters {
  const p = new URLSearchParams(search);
  const time = (k: string) => {
    const v = p.get(k) ?? "";
    return v && Number.isFinite(Date.parse(v)) ? v : undefined;
  };
  return {
    q: p.get("q") || undefined,
    services: p.get("services") || undefined,
    level: p.get("level") || undefined,
    since: time("since"),
    until: time("until"),
  };
}

/** Whether a log line's time falls in the window (a line without a readable time is kept). */
export function inWindow(ts: string, f: Pick<LogFilters, "since" | "until">): boolean {
  const t = Date.parse(ts);
  if (!Number.isFinite(t)) return true;
  if (f.since && t < Date.parse(f.since)) return false;
  if (f.until && t > Date.parse(f.until)) return false;
  return true;
}
