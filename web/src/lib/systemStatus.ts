// The System health page's status (GET /api/admin/system/status, admin):
// every part of Maktaba ok / degraded / down with its reason, and the
// overall state. The page reads it every 30 s; the admin nav reads the same
// state to show a red dot on "System health" when it is down. Both share
// one store: what the page reads updates the nav at once.
import { useEffect, useState } from "react";
import { api } from "./api";

export type HealthStatus = "ok" | "degraded" | "down";

export interface StatusItem {
  id: string;
  group: "services" | "workers" | "dependencies" | "integrations" | "queues_tv";
  kind: string;
  name: string;
  status: HealthStatus;
  /** health.reason.<code> in the catalog, with params; reason is its English. */
  code: string;
  params?: Record<string, unknown>;
  reason: string;
  critical?: boolean;
  /** A stale record kept for reference (a replaced worker): never a failure. */
  history?: boolean;
  /** OK, but something waits for the admin. */
  attention?: boolean;
  last_checked: string;
  /** The help entry key (the kind). */
  help: string;
  link?: string;
  details?: Record<string, unknown>;
}

export interface StatusReport {
  status: HealthStatus;
  code: string;
  params?: Record<string, unknown>;
  reason: string;
  checked_at: string;
  items: StatusItem[];
  processes_supervised: boolean;
}

export const STATUS_PATH = "/api/admin/system/status";

/** How often the nav re-reads the state while no page does. */
export const NAV_REFRESH_MS = 60_000;

let last: StatusReport | null = null;
const listeners = new Set<(r: StatusReport | null) => void>();

/** Shares a report with every view on screen (the page calls it after a read). */
export function publishStatus(r: StatusReport | null) {
  last = r;
  for (const l of listeners) l(r);
}

/** Reads the status; null when it cannot be read (an older server, no database). */
export async function fetchStatus(): Promise<StatusReport | null> {
  try {
    const r = await api.get<StatusReport>(STATUS_PATH);
    const ok = !!r && typeof r === "object" && Array.isArray(r.items);
    const rep = ok ? r : null;
    publishStatus(rep);
    return rep;
  } catch {
    publishStatus(null);
    return null;
  }
}

/** The overall state for the nav badge (admins only; null for others or before a read). */
export function useSystemStatus(enabled: boolean): HealthStatus | null {
  const [rep, setRep] = useState<StatusReport | null>(enabled ? last : null);
  useEffect(() => {
    if (!enabled) return;
    listeners.add(setRep);
    void fetchStatus();
    const id = setInterval(() => void fetchStatus(), NAV_REFRESH_MS);
    return () => {
      listeners.delete(setRep);
      clearInterval(id);
    };
  }, [enabled]);
  return enabled ? (rep?.status ?? null) : null;
}

/** Test seam: forget the shared report. */
export function resetStatusStore() {
  last = null;
}
