// One request id per user action (Epic 53 story 53.08): every call to the home carries
// X-Request-Id, a UUIDv7 the app makes; a page load and its parallel calls (within a
// second of each other) share one, so the home's lines for that action can be followed
// from the API through the pipeline and streaming. The last 20 are kept with their route
// pattern and result, for a problem report (story 53.15).

/** A UUIDv7: 48 bits of milliseconds, then random bits, version 7, variant 10. */
export function uuidv7(now: number = Date.now()): string {
  const b = new Uint8Array(16);
  crypto.getRandomValues(b);
  let ms = now;
  for (let i = 5; i >= 0; i--) {
    b[i] = ms & 0xff;
    ms = Math.floor(ms / 256);
  }
  b[6] = (b[6] & 0x0f) | 0x70;
  b[8] = (b[8] & 0x3f) | 0x80;
  const h = Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

/** How long calls count as one action. */
export const ACTION_WINDOW_MS = 1000;

let current: { id: string; at: number } | null = null;

/** The current action's id: the one of a call within the last second, else a new one. */
export function actionRequestId(now: number = Date.now()): string {
  if (!current || now - current.at > ACTION_WINDOW_MS) current = { id: uuidv7(now), at: now };
  else current.at = now;
  return current.id;
}

/** A new action starts (a navigation, a click): its calls get a new id. */
export function newAction(): void {
  current = null;
}

export interface RequestSeen {
  id: string;
  method: string;
  route: string;
  status: number;
  at: string;
}

/** How many requests are kept. */
export const KEPT_REQUESTS = 20;
const seen: RequestSeen[] = [];

/** Keeps a finished request (its route as a pattern, never the URL with its query). */
export function noteRequest(r: RequestSeen): void {
  seen.push(r);
  if (seen.length > KEPT_REQUESTS) seen.splice(0, seen.length - KEPT_REQUESTS);
}

/** The last requests, oldest first (a copy). */
export function lastRequests(): RequestSeen[] {
  return seen.slice();
}

/** The short reference an error screen shows: the id's first 8 characters. */
export function shortRef(id: string | undefined): string {
  return id ? id.slice(0, 8) : "";
}
