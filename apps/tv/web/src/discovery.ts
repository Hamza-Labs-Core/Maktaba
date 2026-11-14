// Story 58.09 — reading a home and choosing how to reach it.
//
// GET /api/discovery/home is public and answers any origin (`Access-Control-Allow-Origin: *`,
// no credentials), so the launcher reads it from its file:// page. The answer names the
// home's base URLs: its HTTPS per-home names on the LAN first (story 50.15), then the relay.
// The launcher uses the first HTTPS name that answers as the same home; when none does (a
// router blocking DNS rebinding), it falls back to the plain-HTTP LAN address it found the
// home at, and the Account screen says so. Over plain HTTP the launcher signs in by code
// only, never by password.

import { log } from "./log";

export type FetchLike = (url: string, init?: { signal?: AbortSignal }) => Promise<Response>;

/** GET /api/discovery/home's answer (the fields the launcher reads). */
export interface HomeAnswer {
  id: string;
  name: string;
  version?: string;
  urls: string[];
  sign_in?: { password?: boolean; code?: boolean };
  api_level?: number;
  api_level_oldest?: number;
  client_floor?: Record<string, string> | null;
}

/** How the launcher reaches a home. */
export interface Connection {
  /** The base URL /tv/ is loaded from, without a trailing slash. */
  base: string;
  /** Plain HTTP on the LAN: say so on Account, sign in by code only. */
  plain: boolean;
  home: HomeAnswer;
}

/** A base URL from what a person typed ("192.168.1.20", "home.local:8088", a full URL). */
export function normalizeBase(input: string): string | null {
  let s = input.trim();
  if (!s) return null;
  if (!/^https?:\/\//i.test(s)) s = `http://${s}`;
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return null;
  }
  if (!u.hostname || u.username || u.password) return null;
  // a typed bare host with no port is the home's web port
  if (!u.port && u.protocol === "http:" && !/^https?:\/\//i.test(input.trim())) u.port = "8088";
  return `${u.protocol}//${u.host}`;
}

/** The origin of a base URL ("https://home.example:8443"). */
export function originOf(base: string): string {
  return new URL(base).origin;
}

function isAnswer(v: unknown): v is HomeAnswer {
  if (!v || typeof v !== "object") return false;
  const a = v as Partial<HomeAnswer>;
  return typeof a.id === "string" && a.id !== "" && Array.isArray(a.urls);
}

/** Reads base's discovery answer within timeoutMs; null when it is not a Maktaba home. */
export async function readHome(
  base: string,
  fetchFn: FetchLike,
  timeoutMs: number
): Promise<HomeAnswer | null> {
  const ctl = typeof AbortController !== "undefined" ? new AbortController() : null;
  const timer = setTimeout(() => ctl?.abort(), timeoutMs);
  try {
    const res = await Promise.race([
      fetchFn(`${base}/api/discovery/home`, ctl ? { signal: ctl.signal } : undefined),
      new Promise<never>((_, reject) => setTimeout(() => reject(new Error("timeout")), timeoutMs)),
    ]);
    if (!res.ok) return null;
    const body: unknown = await res.json();
    if (!isAnswer(body)) return null;
    return { ...body, urls: body.urls.filter((u) => typeof u === "string") };
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}

/** How long an HTTPS name gets to answer before the next is tried. */
export const NAME_TIMEOUT_MS = 2500;

/**
 * The connection to use for a home found (or typed) at foundAt: its first HTTPS URL that
 * answers as the same home, else foundAt itself (plain when it is http:).
 */
export async function chooseConnection(
  foundAt: string,
  answer: HomeAnswer,
  fetchFn: FetchLike,
  timeoutMs = NAME_TIMEOUT_MS
): Promise<Connection> {
  if (foundAt.startsWith("https:")) return { base: foundAt, plain: false, home: answer };
  for (const url of answer.urls) {
    const base = url.replace(/\/+$/, "");
    if (!base.startsWith("https:")) continue;
    const again = await readHome(base, fetchFn, timeoutMs);
    if (again && again.id === answer.id) {
      log("launcher", `home ${answer.id}: reached by its HTTPS name ${originOf(base)}`);
      return { base, plain: false, home: again };
    }
    log("launcher", `home ${answer.id}: ${originOf(base)} did not answer`);
  }
  log("launcher", `home ${answer.id}: plain HTTP on the LAN at ${foundAt}`);
  return { base: foundAt, plain: true, home: answer };
}
