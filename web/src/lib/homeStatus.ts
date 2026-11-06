// The home's status (Epic 53 story 53.06): /status.json, answered by the supervisor even
// when the API or the database is down (docs/api/status.md). The app reads it at start,
// every 10 s while the page is visible, and at once when a request fails with a server
// error; a failed read changes nothing (an older home has no /status.json: no strip).
import { useEffect, useSyncExternalStore } from "react";
import { BASE } from "./basePath";

export type GroupState = "ok" | "starting" | "degraded" | "restarting" | "down" | "off";

export interface HomeStatus {
  v: number;
  at: string;
  mode: "normal" | "safe" | "recovery";
  overall: "ok" | "degraded" | "down";
  components: Partial<
    Record<"database" | "api" | "streaming" | "processing" | "search" | "logs", GroupState>
  >;
  features_down: string[];
  banner?: string;
}

/** How often the status is read while the page is visible. */
export const STATUS_EVERY_MS = 10_000;

let current: HomeStatus | null = null;
const listeners = new Set<() => void>();
let inflight: Promise<void> | null = null;

function publish(s: HomeStatus | null) {
  current = s;
  listeners.forEach((l) => l());
}

/** Reads /status.json once (a read under way is shared); a failure keeps what was known. */
export function refreshHomeStatus(fetcher: typeof fetch = fetch): Promise<void> {
  if (inflight) return inflight;
  inflight = (async () => {
    try {
      const r = await fetcher(`${BASE}/status.json`, {
        cache: "no-store",
        credentials: "same-origin",
      });
      if (!r.ok) return;
      const doc = (await r.json()) as HomeStatus;
      if (doc && doc.v === 1 && doc.components) publish(doc);
    } catch {
      // the home did not answer: what the app knew stays (the offline line says the rest)
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

/** A request failed with a server error or a timeout: read the status now. */
export function noteRequestFailed(status: number): void {
  // only while a strip shows the status: nothing else reads it
  if (listeners.size > 0 && (status === 0 || status >= 500)) void refreshHomeStatus();
}

/** The status as last read (null until a read answered). */
export function homeStatusNow(): HomeStatus | null {
  return current;
}

/** The status as last read (tests set it). */
export function setHomeStatus(s: HomeStatus | null): void {
  publish(s);
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** A listener as a mounted strip has (tests). */
export function subscribeForTest(): () => void {
  const off = subscribe(() => {});
  return () => void off();
}

/** The home's status, kept fresh while mounted (null until a read answered). */
export function useHomeStatus(poll = true): HomeStatus | null {
  useEffect(() => {
    if (!poll) return;
    void refreshHomeStatus();
    const t = window.setInterval(() => {
      if (document.visibilityState !== "hidden") void refreshHomeStatus();
    }, STATUS_EVERY_MS);
    return () => window.clearInterval(t);
  }, [poll]);
  return useSyncExternalStore(subscribe, () => current);
}

/** One line the strip can show, highest priority first. */
export interface StripLine {
  key: string;
  /** an i18n key */
  text: string;
  /** admins get a Details link: the recovery page when the API is down, else System health */
  details?: string;
}

/**
 * The strip's lines for a status, in priority order (story 53.06 §1): offline; the home
 * restarting or its database down; safe mode; a part a viewer feels (playback, search);
 * admin-only parts (processing). A feature that is off is not an outage: off groups have no
 * line, and Live TV is left out of the playback sentence when it is off.
 */
export function stripLines(
  s: HomeStatus | null,
  opts: { online: boolean; admin: boolean; liveTvOff?: boolean }
): StripLine[] {
  const out: StripLine[] = [];
  if (!opts.online) out.push({ key: "offline", text: "error.offlineBanner" });
  if (!s) return out;
  const g = s.components;
  const bad = (st?: GroupState) => st === "degraded" || st === "restarting" || st === "down";
  const admin = (link: string) => (opts.admin ? link : undefined);
  if (bad(g.database))
    out.push({ key: "database", text: "status.database", details: admin("/recovery") });
  else if (s.overall === "down" || bad(g.api)) {
    out.push({ key: "restarting", text: "status.restarting", details: admin("/recovery") });
  }
  if (s.mode === "safe")
    out.push({ key: "safe", text: "status.safe", details: admin("/recovery") });
  if (bad(g.streaming)) {
    out.push({
      key: "streaming",
      text: opts.liveTvOff ? "status.streamingNoTv" : "status.streaming",
      details: admin("/admin/health"),
    });
  }
  if (bad(g.search))
    out.push({ key: "search", text: "status.search", details: admin("/admin/health") });
  if (opts.admin && bad(g.processing)) {
    out.push({ key: "processing", text: "status.processing", details: "/admin/health" });
  }
  if (opts.admin && bad(g.logs))
    out.push({ key: "logs", text: "status.logs", details: "/admin/health" });
  return out;
}

/** What the strip shows now, so a closed strip comes back when it changes. */
export function stripSignature(lines: StripLine[]): string {
  return lines.map((l) => l.key + ":" + l.text).join("|");
}
