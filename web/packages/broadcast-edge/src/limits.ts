// Viewer limits (story 50.26 §4). The exact ones live in ViewerAccess (one per publisher):
// concurrent viewers per publisher and per channel, open sessions per network, daily minutes,
// short-code tries. The per-address request rates are Workers Rate Limiting bindings in front.
import { sha256Hex } from "./crypto";
import type { Env } from "./env";

/** A session is live while its last beat is younger than this; then it ends (Epic 34 R4.4). */
export const SESSION_TTL_MS = 150_000;
/** A viewer token lives 10 minutes and is refreshed after half its life. */
export const TOKEN_TTL_S = 600;
export const REFRESH_AFTER_S = 300;
/** A beat counts a viewer-minute at most once per this gap (PublisherControl's own rule). */
export const MINUTE_GAP_MS = 50_000;
/** Short codes: 10 tries a minute per address; 60 failed tries a minute per publisher. */
export const CODE_TRIES_PER_IP = 10;
export const CODE_FAILS_PER_PUB = 60;
export const CODE_WINDOW_MS = 60_000;
/** The open sessions one network may hold by default (the publisher's setting, 1–50). */
export const DEFAULT_SESSIONS_PER_IP = 3;

/**
 * ipHash is the address as a salted hash: the salt is the edge's key and the UTC day, so a hash
 * links nothing across days, and the address itself is never kept (§5).
 */
export async function ipHash(env: Env, ip: string, nowMs: number): Promise<string> {
  const day = new Date(nowMs).toISOString().slice(0, 10);
  return (await sha256Hex(`maktaba-broadcast-ip-v1\0${day}\0${env.VIEWER_TOKEN_KEY}\0${ip}`)).slice(
    0,
    32
  );
}

export function clientIP(request: Request): string {
  return request.headers.get("CF-Connecting-IP") ?? "";
}

/**
 * rateLimited asks a Workers Rate Limiting binding (30 plays, 120 other requests a minute per
 * address). Absent (dev, most tests) it lets the request through.
 */
export async function rateLimited(rl: Env["PLAY_RL"], key: string): Promise<boolean> {
  if (!rl) return false;
  const { success } = await rl.limit({ key });
  return !success;
}
