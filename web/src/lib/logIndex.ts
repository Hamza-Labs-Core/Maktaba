// The admin's log index, error groups and diagnostics settings (Epic 53 stories 53.09–53.11).
//
//   GET  /api/admin/logs/search?q=&service=&component=&level=&since=&until=&node=&request_id=&cursor=
//        → { lines: <line>[], ids: number[], cursor?, partial? } (503 log_index_unavailable:
//          the logs role is off or restarting; the page then reads the files)
//   GET  /api/admin/logs/tail?…    the same filters, Server-Sent Events, one line per event
//   GET  /api/admin/logs/around?id=  every service's lines around one line
//   GET  /api/admin/logs/users     { users: { <user_h>: name } }
//   GET  /api/admin/errors?status=&component=&service=&q=&sort=  { groups: ErrorGroup[] }
//   GET  /api/admin/errors/{fp}    { group, hours: { <hour>: n }, samples: { lines, ids } }
//   POST /api/admin/errors/{fp}/status  { status: seen|resolved|muted, version?, muted_until? }
//   GET/PUT /api/admin/logs/retention, POST /api/admin/logs/level (Retention, 53.11)
import { api, apiUrl } from "./api";

/** One line as the shared schema writes it (story 53.01); every other field is kept. */
export interface IndexLine {
  ts?: string;
  level?: string;
  service?: string;
  component?: string;
  node?: string;
  msg?: string;
  request_id?: string;
  job_id?: string;
  video_id?: string;
  session_id?: string;
  user_h?: string;
  version?: string;
  err?: unknown;
  [k: string]: unknown;
}

export interface SearchResult {
  lines: IndexLine[];
  ids: number[];
  cursor?: string;
  partial?: boolean;
}

/** The Logs page's filters, as its address holds them. */
export interface IndexQuery {
  q?: string;
  services?: string;
  components?: string;
  level?: string;
  since?: string;
  until?: string;
  node?: string;
  request?: string;
}

/** The time choices of the filter, newest window first (7 days: the default retention). */
export const WINDOWS = { "15m": 15, "1h": 60, "24h": 24 * 60, "7d": 7 * 24 * 60 } as const;
export type WindowKey = keyof typeof WINDOWS;

/** The since of a window that ends now. */
export function windowSince(w: WindowKey, now = Date.now()): string {
  return new Date(now - WINDOWS[w] * 60_000).toISOString();
}

/** A reference (the first 8 characters, as the error screens show) or a whole request id. */
export function looksLikeRequestId(s: string): boolean {
  const v = s.trim().toLowerCase();
  return (
    /^[0-9a-f]{8}$/.test(v) ||
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(v)
  );
}

function params(f: IndexQuery, extra: Record<string, string | undefined> = {}): string {
  const p = new URLSearchParams();
  const first = (v?: string) => (v ? v.split(",")[0] : undefined);
  const set: Record<string, string | undefined> = {
    q: f.q?.trim() || undefined,
    service: first(f.services),
    component: first(f.components),
    level: f.level,
    since: f.since,
    until: f.until,
    node: f.node,
    request_id: f.request,
    ...extra,
  };
  for (const [k, v] of Object.entries(set)) if (v) p.set(k, v);
  return p.toString();
}

export function searchLogs(f: IndexQuery, cursor?: string): Promise<SearchResult> {
  return api.get<SearchResult>(`/api/admin/logs/search?${params(f, { cursor })}`);
}

/** The live tail's address (EventSource; the session cookie rides with it). */
export function tailURL(f: IndexQuery): string {
  return apiUrl(`/api/admin/logs/tail?${params({ ...f, request: undefined })}`);
}

export function aroundLine(id: number): Promise<SearchResult> {
  return api.get<SearchResult>(`/api/admin/logs/around?id=${id}&before=25&after=25`);
}

export async function logUsers(): Promise<Record<string, string>> {
  const r = await api.get<{ users?: Record<string, string> }>("/api/admin/logs/users");
  return r?.users ?? {};
}

/** The Logs page's address with these filters (other pages link here). */
export function logsPageLink(f: IndexQuery): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, String(v));
  const s = p.toString();
  return s ? `/admin/logs?${s}` : "/admin/logs";
}

export function readIndexQuery(search: string): IndexQuery {
  const p = new URLSearchParams(search);
  const time = (k: string) => {
    const v = p.get(k) ?? "";
    return v && Number.isFinite(Date.parse(v)) ? v : undefined;
  };
  return {
    q: p.get("q") || undefined,
    services: p.get("services") || undefined,
    components: p.get("components") || undefined,
    level: p.get("level") || undefined,
    since: time("since"),
    until: time("until"),
    node: p.get("node") || undefined,
    request: p.get("request") || undefined,
  };
}

/** Where a line's time and the one before it are more than a second apart (Follow a request). */
export function gapsOver(lines: IndexLine[], ms = 1000): boolean[] {
  return lines.map((l, i) => {
    if (i === 0) return false;
    const a = Date.parse(lines[i - 1].ts ?? "");
    const b = Date.parse(l.ts ?? "");
    return Number.isFinite(a) && Number.isFinite(b) && b - a > ms;
  });
}

// ---- error groups (story 53.10) ----

export type ErrorStatus = "new" | "seen" | "resolved" | "muted";

export interface ErrorGroup {
  fp: string;
  service: string;
  component?: string;
  code?: string;
  title: string;
  first_seen: string;
  last_seen: string;
  count: number;
  count_24h: number;
  users: number;
  status: ErrorStatus;
  resolved_in?: string;
  came_back_in?: string;
  muted_until?: string;
  last_version?: string;
  last_request_id?: string;
  days?: number[];
}

export interface ErrorsQuery {
  status?: string;
  component?: string;
  service?: string;
  q?: string;
  sort?: "last_seen" | "count" | "users";
}

export async function listErrors(f: ErrorsQuery = {}): Promise<ErrorGroup[]> {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(f)) if (v) p.set(k, String(v));
  const r = await api.get<{ groups?: ErrorGroup[] }>(`/api/admin/errors?${p.toString()}`);
  return r?.groups ?? [];
}

export interface ErrorGroupDetail {
  group: ErrorGroup;
  hours?: Record<string, number>;
  samples?: SearchResult;
}

export function getErrorGroup(fp: string): Promise<ErrorGroupDetail> {
  return api.get<ErrorGroupDetail>(`/api/admin/errors/${encodeURIComponent(fp)}`);
}

export function setErrorStatus(
  fp: string,
  body: { status: "seen" | "resolved" | "muted"; version?: string; muted_until?: string }
): Promise<unknown> {
  return api.post(`/api/admin/errors/${encodeURIComponent(fp)}/status`, body);
}

// ---- Retention and detailed logs (story 53.11) ----

export interface Retention {
  logs_days: number;
  errors_days: number;
  reports_days: number;
  total_bytes: number;
  used: { logs: number; index: number; reports: number };
  ranges: Record<string, [number, number]>;
  detailed: Record<string, string>;
}

export type RetentionChange = Partial<
  Pick<Retention, "logs_days" | "errors_days" | "reports_days" | "total_bytes">
>;

export function getRetention(): Promise<Retention> {
  return api.get<Retention>("/api/admin/logs/retention");
}

export function putRetention(ch: RetentionChange): Promise<Retention> {
  return api.put<Retention>("/api/admin/logs/retention", ch);
}

/** Services whose level an admin can raise for an hour. */
export const LEVEL_SERVICES = ["api", "streaming", "pipeline", "logs"] as const;

export function setDetailedLogs(service: string, on: boolean): Promise<Retention> {
  return api.post<Retention>("/api/admin/logs/level", {
    service,
    level: on ? "debug" : "info",
    minutes: 60,
  });
}
