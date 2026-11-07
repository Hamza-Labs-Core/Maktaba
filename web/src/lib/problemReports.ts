// Problem reports (Epic 53 story 53.15): what the person sends from "Report a problem", the
// admin's inbox and a person's own reports. The home adds its side (its lines of the
// report's requests and the warnings around its time); the sender never reads that part.
import { api, apiUrl, csrfHeaders, ApiError } from "./api";
import { logLines } from "./errorLog";
import { lastRequests } from "./requestIds";
import { installId } from "./crashReports";

export const CATEGORIES = ["playback", "live_tv", "search", "signing_in", "other"] as const;
export type Category = (typeof CATEGORIES)[number];
export const STATUSES = ["new", "looking", "fixed", "closed"] as const;
export type ReportStatus = (typeof STATUSES)[number];

/** The lines a report takes: the last 30 minutes of the app's log, at most 2,000. */
export function reportLines(now = Date.now()) {
  return logLines()
    .filter((l) => now - Date.parse(l.ts) <= 30 * 60_000)
    .slice(-2000);
}

export interface ReportDraft {
  category: Category;
  text: string;
  when?: string; // ISO; empty: just now
  appVersion: string;
  requestRef?: string; // the error screen's reference it was opened from
}

/** Sends a report; answers its number. */
export async function sendReport(d: ReportDraft): Promise<number> {
  const reqs = lastRequests().map((r) => r.id);
  if (d.requestRef && !reqs.some((r) => r.startsWith(d.requestRef as string)))
    reqs.push(d.requestRef);
  const body = new FormData();
  body.set(
    "report",
    JSON.stringify({
      category: d.category,
      text: d.text,
      when: d.when,
      app: "web",
      version: d.appVersion,
      platform: "web",
      os_version: typeof navigator === "undefined" ? "" : navigator.userAgent.slice(0, 200),
      install_id: installId(),
      request_ids: reqs.slice(-20),
      lines: reportLines(),
    })
  );
  const r = await fetch(apiUrl("/api/reports"), {
    method: "POST",
    credentials: "include",
    headers: csrfHeaders(),
    body,
  });
  if (!r.ok) {
    let title = "";
    try {
      title = ((await r.json()) as { title?: string }).title ?? "";
    } catch {
      // not a problem document
    }
    throw new ApiError({ type: "about:blank", title, status: r.status });
  }
  return ((await r.json()) as { id: number }).id;
}

export interface ReportSummary {
  id: number;
  at: string;
  name?: string;
  app: string;
  category: Category;
  status: ReportStatus;
  shared_with_maktaba: boolean;
  reply?: string;
}

export interface ReportLine {
  ts?: string;
  level?: string;
  service?: string;
  msg?: string;
  request_id?: string;
  [k: string]: unknown;
}

export interface ReportDoc extends ReportSummary {
  when: string;
  version: string;
  platform: string;
  os_version?: string;
  device_model?: string;
  text?: string;
  screenshot: boolean;
  request_ids?: string[];
  lines?: ReportLine[];
  server?: ReportLine[];
  crashes?: unknown[];
}

export async function listReports(): Promise<ReportSummary[]> {
  const r = await api.get<{ reports?: ReportSummary[] }>("/api/reports");
  return r?.reports ?? [];
}

export function getReport(id: number): Promise<ReportDoc> {
  return api.get<ReportDoc>(`/api/reports/${id}`);
}

export function setReportStatus(id: number, status: ReportStatus): Promise<ReportSummary> {
  return api.post<ReportSummary>(`/api/reports/${id}/status`, { status });
}

export function replyToReport(id: number, reply: string): Promise<ReportSummary> {
  return api.post<ReportSummary>(`/api/reports/${id}/reply`, { reply });
}

export const reportBundlePath = (id: number) => `/api/reports/${id}/bundle`;

/** The report's timeline: the app's lines and the home's merged by time, oldest first. */
export function timeline(d: ReportDoc): (ReportLine & { side: "app" | "home" })[] {
  const all = [
    ...(d.lines ?? []).map((l) => ({ ...l, side: "app" as const })),
    ...(d.server ?? []).map((l) => ({ ...l, side: "home" as const })),
  ];
  return all.sort((a, b) => (Date.parse(a.ts ?? "") || 0) - (Date.parse(b.ts ?? "") || 0));
}
