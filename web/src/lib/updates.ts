// Story 28.6 — the server's update status (GET /api/system/updates), shared
// by the About section and the nav badge. Each view reads it when it mounts
// (the reads of one render share one request); "Check now" on the About page
// refreshes it for every view on screen.
import { useEffect, useState } from "react";
import { api } from "./api";

// UpdateStatus mirrors GET /api/system/updates (Stories 28.2/28.6, 35.3).
export interface UpdateStatus {
  available: boolean;
  disabled?: boolean;
  current_version: string;
  latest_version?: string;
  channel: string;
  release_url?: string;
  release_notes?: string;
  checked_at: string;
  /** How this server updates: managed | docker | deb | rpm | runtime | binary. */
  install?: string;
  /** A container that cannot update itself (the stack built from source): the command an
   * operator runs. The Docker image updates itself and has none. */
  instructions?: string;
  image?: string;
  /** The last self-update that could not start and was undone. */
  rolled_back?: { version: string; reason: string; at: string };
}

let inflight: Promise<UpdateStatus | null> | null = null;
const listeners = new Set<(s: UpdateStatus | null) => void>();

function publish(s: UpdateStatus | null) {
  for (const l of listeners) l(s);
}

/**
 * fetchUpdateStatus reads the status and shares it with every view on
 * screen; `refresh` asks the server to check again (and rejects on a
 * failure, for the caller's toast). A plain read never rejects: null.
 */
export function fetchUpdateStatus(refresh = false): Promise<UpdateStatus | null> {
  if (refresh) {
    const p = api.get<UpdateStatus>("/api/system/updates?refresh=true").then((s) => s ?? null);
    p.then(publish, () => {});
    return p;
  }
  if (!inflight) {
    const p: Promise<UpdateStatus | null> = api
      .get<UpdateStatus>("/api/system/updates")
      .then((s) => s ?? null)
      .catch(() => null);
    // shared only by the reads of one render (the nav and the About tab
    // mounting together); a later mount reads again
    inflight = p;
    queueMicrotask(() => {
      if (inflight === p) inflight = null;
    });
    void p.then(publish);
  }
  return inflight;
}

/** Test hook: forget a read in flight. */
export function resetUpdateStatusCache(): void {
  inflight = null;
}

/** useUpdateStatus is the status (null until loaded, or when it can't be read). */
export function useUpdateStatus(): UpdateStatus | null {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  useEffect(() => {
    const l = (s: UpdateStatus | null) => setStatus(s);
    listeners.add(l);
    void fetchUpdateStatus();
    return () => {
      listeners.delete(l);
    };
  }, []);
  return status;
}

/** updateAvailable is true when a newer release exists and checks are on. */
export function updateAvailable(s: UpdateStatus | null): boolean {
  return Boolean(s && s.available && !s.disabled && s.latest_version);
}
