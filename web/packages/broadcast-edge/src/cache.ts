// The edge cache (story 50.21 §4). Every key is token-free: the viewer's token is in the URL
// path, so two viewers' URLs differ, but both read one cached copy per location. Cloudflare's
// cache lock collapses simultaneous misses on a key to one R2 read (or one Durable Object call)
// per location; the home is never fetched.
import { DEFAULT_LIVE_HOST, type Env } from "./env";

export const SEGMENT_CACHE_CONTROL = "public, max-age=86400, immutable";
export const PLAYLIST_CACHE_CONTROL = "public, max-age=1";

function host(env: Env): string {
  return env.LIVE_HOST || DEFAULT_LIVE_HOST;
}

export function segmentCacheKey(env: Env, ch: string, rung: string, name: string): string {
  return `https://${host(env)}/c/${ch}/${rung}/${name}`;
}

export function mediaCacheKey(env: Env, ch: string, rung: string): string {
  return `https://${host(env)}/c/${ch}/${rung}/index.m3u8`;
}

export function masterCacheKey(env: Env, ch: string, mode: string): string {
  return `https://${host(env)}/c/${ch}/master-${mode}.m3u8`;
}

export type CacheStatus = "hit" | "miss";

function edgeCache(): Cache {
  return (caches as unknown as { default: Cache }).default;
}

/**
 * cached answers from the edge cache under `key`, or calls `fill` and stores what it returns
 * (200s only). The stored copy carries no per-viewer header; the caller adds those.
 */
export async function cached(
  key: string,
  fill: () => Promise<Response>
): Promise<{ res: Response; status: CacheStatus }> {
  const cache = edgeCache();
  const hit = await cache.match(key);
  if (hit) return { res: hit, status: "hit" };
  const res = await fill();
  if (res.status !== 200) return { res, status: "miss" };
  const body = await res.arrayBuffer();
  const stored = new Response(body, { status: 200, headers: res.headers });
  await cache.put(key, stored.clone());
  return { res: stored, status: "miss" };
}
