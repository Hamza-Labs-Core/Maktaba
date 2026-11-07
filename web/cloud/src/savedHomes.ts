// The saved home list (Epic 50 story 50.13): after every good GET /v1/homes the list is kept
// per cloud account in localStorage (names and addresses only, never a token), and shown when
// the cloud can't be reached: a network failure, a 5xx, or an answer that isn't the API's JSON
// (a captive portal). Kept 30 days, then still shown but marked old. Removed at sign-out.
import { CloudApiError } from "./api";

export type ConnectionKind = "local" | "direct" | "relay_hint" | "relay";
export interface Connection {
  url: string;
  kind: ConnectionKind;
}
export interface SavedHome {
  server_id: string;
  name: string;
  slug: string;
  connections: Connection[];
}
export interface SavedList {
  saved_at: string;
  homes: SavedHome[];
}

const PREFIX = "mkt:cloud:homes:";
export const OLD_AFTER_MS = 30 * 24 * 3600_000;
const ORDER: ConnectionKind[] = ["local", "direct", "relay_hint", "relay"];

/** The order apps try (story 50.13 §2): local, direct, relay hint, relay. */
export function ordered(cs: Connection[]): Connection[] {
  return [...cs]
    .filter((c) => ORDER.includes(c.kind))
    .sort((a, b) => ORDER.indexOf(a.kind) - ORDER.indexOf(b.kind));
}

/** A home's connections as the API gives them, or built from the older fields. */
export function connectionsOf(h: {
  connections?: Connection[];
  direct_url?: string;
  relay_url?: string;
}): Connection[] {
  if (Array.isArray(h.connections)) return ordered(h.connections);
  const out: Connection[] = [];
  if (h.direct_url) out.push({ url: h.direct_url, kind: "direct" });
  if (h.relay_url) out.push({ url: h.relay_url, kind: "relay" });
  return out;
}

export function saveHomes(
  userId: string,
  homes: {
    server_id: string;
    name: string;
    slug: string;
    connections?: Connection[];
    direct_url?: string;
    relay_url?: string;
  }[],
  now: Date = new Date()
): void {
  const list: SavedList = {
    saved_at: now.toISOString(),
    homes: homes.map((h) => ({
      server_id: h.server_id,
      name: h.name,
      slug: h.slug,
      connections: connectionsOf(h),
    })),
  };
  try {
    localStorage.setItem(PREFIX + userId, JSON.stringify(list));
  } catch {
    // storage full or blocked: the live list still shows
  }
}

export function loadHomes(userId: string): SavedList | null {
  try {
    const raw = localStorage.getItem(PREFIX + userId);
    if (!raw) return null;
    const list = JSON.parse(raw) as SavedList;
    return list && Array.isArray(list.homes) && typeof list.saved_at === "string" ? list : null;
  } catch {
    return null;
  }
}

export function isOld(list: SavedList, now: Date = new Date()): boolean {
  return now.getTime() - Date.parse(list.saved_at) > OLD_AFTER_MS;
}

/** Every account's saved list, at sign-out. */
export function forgetSavedHomes(): void {
  try {
    for (let i = localStorage.length - 1; i >= 0; i--) {
      const k = localStorage.key(i);
      if (k && k.startsWith(PREFIX)) localStorage.removeItem(k);
    }
  } catch {
    // nothing saved to remove
  }
}

/** The cloud couldn't be reached: no answer, a server error, or not the API's JSON. */
export function cloudUnreachable(err: unknown): boolean {
  if (err instanceof CloudApiError) return err.status >= 500 || err.status === 0;
  return true;
}
