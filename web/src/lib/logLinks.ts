// Links from a failure to the logs that tell its whole story (update-attempts.md): the
// admin's log page (/admin/logs) on the failure's time window, with the machine's own log
// files (server.log, app.log, launcher.log; a joined machine's over its channel: ?node=),
// and the bundle of those logs (/api/admin/logs/export) for the same window and machine.

/** How far around a failure the logs are shown: two minutes before it began, two after it ended. */
export const LOG_MARGIN_MS = 2 * 60_000;

/** The filters /admin/logs reads from its address. */
export interface LogFilters {
  services?: string;
  level?: string;
  /** ISO time: older lines are hidden */
  since?: string;
  /** ISO time: newer lines are hidden */
  until?: string;
  /** a joined machine's id: its own log files (empty: this machine's, the home's) */
  node?: string;
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

/** A joined machine's own log files on /admin/logs, in the window. */
export function nodeLogsLink(nodeId: string, f: Pick<LogFilters, "since" | "until"> = {}): string {
  return logsLink({ node: nodeId, since: f.since, until: f.until });
}

/** The logs bundle (the live logs and the log files) of the window, a joined machine's
 * files with `node`. */
export function logsExportURL(f: LogFilters = {}): string {
  return `/api/admin/logs/export${query({ ...f })}`;
}

/** A machine's log files (server.log, app.log, launcher.log) in the window: this machine's,
 * or a joined machine's over its channel. */
export function logFilesURL(
  f: Pick<LogFilters, "since" | "until" | "node"> & { limit?: number }
): string {
  return `/api/admin/logs/files${query({
    since: f.since,
    until: f.until,
    node: f.node,
    limit: f.limit ? String(f.limit) : undefined,
  })}`;
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
    services: p.get("services") || undefined,
    level: p.get("level") || undefined,
    since: time("since"),
    until: time("until"),
    node: p.get("node") || undefined,
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
