// Epic 57 story 57.06 on the web: what this tab asked a parent for.
//
// What became of a request is the home's to say (lib/parentalRequests reads
// GET /api/me/parental/requests). This is only what the page shows before the home
// has answered, and all that is known on a home without that route: what this tab
// remembers having asked. Every answer of the home rewrites it. A mark is:
//
//   - one account's: it is kept under that account's id, so another child who
//     signs in in the same tab starts with none;
//   - as long-lived as the request: it holds the request's own `expires_at` (the
//     home's: seven days for a thing, the home's day end for more time) and stops
//     counting then, so the child can ask again;
//   - dropped when it is answered: when a parent's PIN opens the thing or gives
//     the minutes, and, for time, when the home says watching is allowed again
//     (the minutes asked for came; the next time's-up screen asks afresh).
//
// It lives in sessionStorage (the tab's life); where that is not available it
// lives for as long as the page is open.
import { useEffect, useState } from "react";

const KEY = "mkt:parental:asked";

/** The mark of a request for more time (there is one such request at a time). */
export const ASKED_TIME = "time";

/** what -> when its request expires (ISO 8601); "": the home did not say */
type Marks = Record<string, string>;

const keyOf = (userId: string | null) => `${KEY}:${userId ?? ""}`;
// where sessionStorage cannot be used: the page's own memory
const memory = new Map<string, Marks>();
const listeners = new Set<() => void>();

function read(userId: string | null): Marks {
  const key = keyOf(userId);
  try {
    const v: unknown = JSON.parse(sessionStorage.getItem(key) ?? "{}");
    if (!v || typeof v !== "object" || Array.isArray(v)) return {};
    const marks: Marks = {};
    for (const [what, until] of Object.entries(v)) {
      if (typeof until === "string") marks[what] = until;
    }
    return marks;
  } catch {
    return memory.get(key) ?? {};
  }
}

function write(userId: string | null, marks: Marks): void {
  const key = keyOf(userId);
  try {
    sessionStorage.setItem(key, JSON.stringify(marks));
  } catch {
    memory.set(key, marks);
  }
  for (const l of [...listeners]) l();
}

/**
 * Until when this tab's request for `what` ("video:<id>", ASKED_TIME) waits, in
 * ms; Infinity when the home did not say; null when nothing waits.
 */
export function askedUntil(userId: string | null, what: string, now = Date.now()): number | null {
  const until = read(userId)[what];
  if (until === undefined) return null;
  const at = Date.parse(until);
  if (!Number.isFinite(at)) return Infinity;
  return at > now ? at : null;
}

/** Whether this account asked for `what` in this tab, and the request still waits. */
export function wasAsked(userId: string | null, what: string, now = Date.now()): boolean {
  return askedUntil(userId, what, now) !== null;
}

/** Notes a request the home took, with the `expires_at` the home gave it. */
export function noteAsked(userId: string | null, what: string, expiresAt: string): void {
  write(userId, { ...read(userId), [what]: expiresAt });
}

/** Drops a mark: the request was answered, or what it asked for came. */
export function forgetAsked(userId: string | null, what: string): void {
  const marks = read(userId);
  if (!(what in marks)) return;
  delete marks[what];
  write(userId, marks);
}

/**
 * Whether this account's request for `what` waits, kept current: it follows the
 * account and the thing, a mark noted or dropped anywhere on the page, and the
 * request's own expiry.
 */
export function useAsked(userId: string | null, what: string): boolean {
  const [, again] = useState(0);
  useEffect(() => {
    const l = () => again((x) => x + 1);
    listeners.add(l);
    return () => {
      listeners.delete(l);
    };
  }, []);
  const until = askedUntil(userId, what);
  useEffect(() => {
    if (until === null || until === Infinity) return;
    // setTimeout holds at most about 24 days; a request expires sooner
    const id = window.setTimeout(
      () => again((x) => x + 1),
      Math.min(Math.max(0, until - Date.now()) + 50, 0x7fffffff)
    );
    return () => window.clearTimeout(id);
  }, [until]);
  return until !== null;
}
