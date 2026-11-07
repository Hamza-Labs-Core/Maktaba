// A home that is reconnecting (Epic 50 story 50.13 §4): the relay holds a request up to 15 s
// for a home that was just seen, then answers 503 {"error": "home_reconnecting"} with
// Retry-After. That is a blip, not an outage: the request is tried again after 2, 4, 8 and
// then every 15 s, up to 60 s in all, and the status strip says "Reconnecting to your home…"
// from the first retry. The relay never handed such a request to the home, so trying it again
// is safe for every method. 502 server_offline (the home not connected at all) is not retried.
import { useSyncExternalStore } from "react";

export const RECONNECT_BUDGET_MS = 60_000;
const FIRST_MS = 2_000;
const CAP_MS = 15_000;

let waiting = 0; // requests waiting for the home right now
const listeners = new Set<() => void>();
const publish = () => listeners.forEach((l) => l());

/** Whether an answer is the relay's home_reconnecting. */
export async function isReconnecting(res: Response): Promise<boolean> {
  if (res.status !== 503) return false;
  try {
    const body = (await res.clone().json()) as { error?: unknown; code?: unknown };
    return body?.error === "home_reconnecting" || body?.code === "home_reconnecting";
  } catch {
    return false;
  }
}

/** The delays between tries: 2, 4, 8, 15, 15, … seconds while the 60 s last. */
export function delays(budget = RECONNECT_BUDGET_MS): number[] {
  const out: number[] = [];
  let d = FIRST_MS;
  let total = 0;
  while (total + d <= budget) {
    out.push(d);
    total += d;
    d = Math.min(d * 2, CAP_MS);
  }
  return out;
}

/**
 * Sends with send(), and while the relay answers home_reconnecting, waits and sends again.
 * The last answer is returned as it came (the caller's error handling takes it from there).
 */
export async function rideOutReconnect(
  send: () => Promise<Response>,
  sleep: (ms: number) => Promise<void> = (ms) => new Promise((r) => setTimeout(r, ms))
): Promise<Response> {
  let res = await send();
  const waits = delays();
  let counted = false;
  try {
    for (const ms of waits) {
      if (!(await isReconnecting(res))) break;
      if (!counted) {
        counted = true;
        waiting++;
        publish();
      }
      await sleep(ms);
      res = await send();
    }
  } finally {
    if (counted) {
      waiting--;
      publish();
    }
  }
  return res;
}

function subscribe(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

/** True while a request waits for the home to come back. */
export function useReconnecting(): boolean {
  return useSyncExternalStore(subscribe, () => waiting > 0);
}
