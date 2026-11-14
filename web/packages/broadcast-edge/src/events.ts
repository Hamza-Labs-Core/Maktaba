// Story 50.28 §1: the edge's Analytics Engine events (dataset broadcast_events). Every data point
// has the same columns, in the order of cloud/internal/broadcast/testdata/events_schema.json, so
// the cloud's hourly rollup reads them by position (blob1 is the event, blob2 the publisher …).
// A column an event does not use is "" or 0.
//
// What is never written: an IP address, a pass id, an invite label, a home id. The session is a
// salted hash (sessionHash), used only for distinct counts; the salt changes every UTC day, so
// a session can't be followed from one day to the next. The country is the two-letter code.
import { b64url, fromB64url, utf8 } from "./crypto";
import type { Env } from "./env";

export const BLOBS = [
  "event",
  "publisher",
  "channel",
  "country",
  "device",
  "access",
  "mode",
  "rung",
  "code",
  "from",
  "to",
  "session",
] as const;
export const DOUBLES = [
  "startup_ms",
  "minutes",
  "rebuffer_ms",
  "bitrate_kbps",
  "switch_ms",
  "segment_bytes",
  "upload_to_visible_ms",
  "glass_to_glass_ms",
] as const;

type Blob = (typeof BLOBS)[number];
type Double = (typeof DOUBLES)[number];

export type EventName = "play" | "beat" | "switch" | "error" | "blocked" | "ingest";
export const DEVICES = ["web", "ios", "tvos", "android", "androidtv", "desktop"] as const;
export type Device = (typeof DEVICES)[number];
export type BlockedReason = "geo" | "full" | "ip" | "revoked";

export interface DataPoint {
  indexes: string[];
  blobs: string[];
  doubles: number[];
}

/** point lays one event out in the schema's columns. */
export function point(
  event: EventName,
  pub: string,
  blobs: Partial<Record<Blob, string>>,
  doubles: Partial<Record<Double, number>> = {}
): DataPoint {
  const b: Partial<Record<Blob, string>> = { ...blobs, event, publisher: pub };
  return {
    indexes: [pub],
    blobs: BLOBS.map((k) => clip(b[k] ?? "")),
    doubles: DOUBLES.map((k) => finite(doubles[k])),
  };
}

function clip(s: string): string {
  return s.length > 64 ? s.slice(0, 64) : s;
}

function finite(n: number | undefined): number {
  return typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : 0;
}

/** write sends one point; Analytics Engine is optional (absent in dev) and never fails a request. */
export function write(env: Pick<Env, "AE">, p: DataPoint): void {
  try {
    env.AE?.writeDataPoint(p);
  } catch {
    // a dropped analytics point never costs a viewer a segment
  }
}

export interface ViewerContext {
  pub: string;
  ch: string;
  country: string;
  device: Device;
  access: "invite" | "open";
  mode: string;
  session: string; // already hashed
}

export const play = (v: ViewerContext, startupMs: number): DataPoint =>
  point(
    "play",
    v.pub,
    {
      channel: v.ch,
      country: v.country,
      device: v.device,
      access: v.access,
      mode: v.mode,
      session: v.session,
    },
    { startup_ms: startupMs }
  );

export const beat = (v: ViewerContext, rung: string, rebufferMs: number, kbps: number): DataPoint =>
  point(
    "beat",
    v.pub,
    { channel: v.ch, country: v.country, device: v.device, rung, session: v.session },
    { minutes: 1, rebuffer_ms: rebufferMs, bitrate_kbps: kbps }
  );

export const rungSwitch = (
  pub: string,
  ch: string,
  from: string,
  to: string,
  ms: number
): DataPoint => point("switch", pub, { channel: ch, from, to }, { switch_ms: ms });

export const error = (pub: string, ch: string, code: string): DataPoint =>
  point("error", pub, { channel: ch, code });

export const blocked = (pub: string, ch: string, reason: BlockedReason): DataPoint =>
  point("blocked", pub, { channel: ch, code: reason });

/** ingest is one segment made visible; glass-to-glass only for the canary publisher. */
export const ingest = (
  pub: string,
  ch: string,
  rung: string,
  bytes: number,
  visibleMs: number,
  glassMs?: number
): DataPoint =>
  point(
    "ingest",
    pub,
    { channel: ch, rung },
    { segment_bytes: bytes, upload_to_visible_ms: visibleMs, glass_to_glass_ms: glassMs }
  );

const SESSION_DOMAIN = "maktaba-broadcast-session-v1";
const sessionKeys = new Map<string, Promise<CryptoKey>>();

/**
 * sessionHash is the session's salted hash: HMAC-SHA256 under the viewer-token key, over the
 * domain, the UTC day and the session id, first 12 bytes. No new secret, and nobody without the
 * key can link a hash to a token.
 */
export async function sessionHash(secret: string, session: string, nowMs: number): Promise<string> {
  let key = sessionKeys.get(secret);
  if (!key) {
    key = crypto.subtle.importKey(
      "raw",
      fromB64url(secret),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"]
    );
    sessionKeys.set(secret, key);
  }
  const day = new Date(nowMs).toISOString().slice(0, 10);
  const mac = await crypto.subtle.sign(
    "HMAC",
    await key,
    utf8(`${SESSION_DOMAIN}\0${day}\0${session}`)
  );
  return b64url(new Uint8Array(mac).slice(0, 12));
}

/**
 * deviceClass is the player's own word (X-Maktaba-Device, or the beat's `device`) when it is one
 * of the six, else a guess from the User-Agent. The User-Agent itself is never written.
 */
export function deviceClass(hint: string | null | undefined, ua: string | null): Device {
  const h = (hint ?? "").toLowerCase();
  if ((DEVICES as readonly string[]).includes(h)) return h as Device;
  const u = ua ?? "";
  if (/AppleTV|tvOS/i.test(u)) return "tvos";
  if (/iPhone|iPad|iPod/i.test(u)) return "ios";
  if (/Android/i.test(u))
    return /\bTV\b|AFT|BRAVIA|GoogleTV|SMART-TV/i.test(u) ? "androidtv" : "android";
  if (/Electron|Maktaba-Desktop/i.test(u)) return "desktop";
  return "web";
}

/** countryOf is the token's country (watch.maktaba.tv's), else Cloudflare's, else "XX". */
export function countryOf(claimed: string | undefined, request: Request): string {
  const cf = (request as Request & { cf?: { country?: unknown } }).cf;
  const c = claimed ?? (typeof cf?.country === "string" ? cf.country : "");
  return /^[A-Z]{2}$/.test(c) ? c : "XX";
}
