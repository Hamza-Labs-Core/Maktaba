// Geo rules (story 50.23 §5). A channel may name an allow list or a deny list of ISO countries;
// Maktaba's own deny list (broadcast_geo_deny, set by service admins) applies to every channel.
// The cloud puts both into the channel's ingest token (`geo`, the Maktaba list merged into
// `deny`), so the edge learns them from the home's own uploads and the home can't drop the
// Maktaba list. A viewer is checked against CF-IPCountry at play and at every playlist.
import { now } from "./clock";
import type { Env } from "./env";

/** A channel's country rule as its ingest token carries it. */
export interface Geo {
  /** when set, only these countries may watch */
  allow?: string[];
  /** these countries may never watch (Maktaba's list included) */
  deny?: string[];
}

const ISO = /^[A-Z]{2}$/;

/** cleanGeo keeps a token's geo claim only when it is well formed (a bad one is no rule). */
export function cleanGeo(v: unknown): Geo | null {
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  const list = (x: unknown): string[] | undefined =>
    Array.isArray(x) && x.every((c) => typeof c === "string" && ISO.test(c))
      ? (x as string[])
      : undefined;
  const allow = list(o.allow);
  const deny = list(o.deny);
  if (!allow?.length && !deny?.length) return null;
  const g: Geo = {};
  if (allow?.length) g.allow = allow;
  if (deny?.length) g.deny = deny;
  return g;
}

/**
 * geoBlocked answers whether a viewer in `country` (CF-IPCountry; "" when absent) may not watch.
 * A deny list wins. With an allow list, a country Cloudflare could not tell ("XX", Tor's "T1",
 * or none) is blocked: the publisher asked for these countries only.
 */
export function geoBlocked(geo: Geo | null | undefined, country: string): boolean {
  if (!geo) return false;
  const c = (country || "").toUpperCase();
  if (geo.deny?.includes(c)) return true;
  if (geo.allow?.length) return !ISO.test(c) || c === "XX" || c === "T1" || !geo.allow.includes(c);
  return false;
}

const CACHE_MS = 10_000;
const cache = new Map<string, { at: number; geo: Geo | null }>();

/** geoFor is the channel's rule as this isolate last read it from ChannelLive (≤ 10 s old). */
export async function geoFor(env: Env, ch: string): Promise<Geo | null> {
  const t = now();
  const hit = cache.get(ch);
  if (hit && t - hit.at < CACHE_MS) return hit.geo;
  const geo = await env.CHANNEL.get(env.CHANNEL.idFromName(ch)).geo();
  if (cache.size > 1000) cache.clear();
  cache.set(ch, { at: t, geo });
  return geo;
}

/** forgetGeo drops the isolate's copies (tests). */
export function forgetGeo(): void {
  cache.clear();
}
