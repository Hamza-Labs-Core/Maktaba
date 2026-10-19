// Story 28.6 — the server's update status (GET /api/system/updates), shared
// by the About section and the nav badge. The nav reads it once per page
// load; "Check now" on the About page refreshes it for both.
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
  /** How this server updates: managed | docker | deb | rpm | binary. */
  install?: string;
  /** Docker installs: the command an operator runs. */
  instructions?: string;
  image?: string;
  /** The last self-update that could not start and was undone. */
  rolled_back?: { version: string; reason: string; at: string };
}

let cached: Promise<UpdateStatus | null> | null = null;
const listeners = new Set<(s: UpdateStatus | null) => void>();

/** fetchUpdateStatus reads the status; `refresh` asks the server to re-check. */
export function fetchUpdateStatus(refresh = false): Promise<UpdateStatus | null> {
  if (refresh || !cached) {
    const p = api
      .get<UpdateStatus>(`/api/system/updates${refresh ? "?refresh=true" : ""}`)
      .then((s) => s ?? null);
    if (refresh) {
      // A failed refresh keeps the last good status for the badge; the
      // caller still sees the error.
      p.then(
        (s) => publish(Promise.resolve(s), s),
        () => {}
      );
      return p;
    }
    cached = p.catch(() => null);
  }
  return cached;
}

/** publishUpdateStatus shares a status read elsewhere with the badge. */
export function publishUpdateStatus(s: UpdateStatus | null): void {
  publish(Promise.resolve(s), s);
}

function publish(p: Promise<UpdateStatus | null>, s: UpdateStatus | null) {
  cached = p;
  for (const l of listeners) l(s);
}

/** Test hook: forget the cached status. */
export function resetUpdateStatusCache(): void {
  cached = null;
}

/** useUpdateStatus is the shared status (null until loaded or on error). */
export function useUpdateStatus(): UpdateStatus | null {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  useEffect(() => {
    let live = true;
    void fetchUpdateStatus().then((s) => {
      if (live) setStatus(s);
    });
    const l = (s: UpdateStatus | null) => setStatus(s);
    listeners.add(l);
    return () => {
      live = false;
      listeners.delete(l);
    };
  }, []);
  return status;
}

/** updateAvailable is true when a newer release exists and checks are on. */
export function updateAvailable(s: UpdateStatus | null): boolean {
  return Boolean(s && s.available && !s.disabled && s.latest_version);
}
