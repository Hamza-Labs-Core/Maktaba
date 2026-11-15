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

/** A rung's playlist, per mode: a `lowest` session is refused rungs a `full` one may play. */
export function mediaCacheKey(env: Env, ch: string, rung: string, mode: string): string {
  return `https://${host(env)}/c/${ch}/${rung}/index-${mode}.m3u8`;
}

export function masterCacheKey(env: Env, ch: string, mode: string): string {
  return `https://${host(env)}/c/${ch}/master-${mode}.m3u8`;
}

export type CacheStatus = "hit" | "miss";

function edgeCache(): Cache {
  return (caches as unknown as { default: Cache }).default;
}

/** A fill's answer, kept as bytes so every request waiting on it builds its own Response. */
interface Filled {
  body: ArrayBuffer;
  headers: [string, string][];
}

/**
 * The fills under way in this isolate, by cache key. The Cache API has no lock of its own in an
 * isolate: viewers who all miss a new segment before the first read stores it would each read
 * R2 (the nightly fan-out saw 6 reads of one object at one location at 1,000 viewers). The first
 * miss fills; the others wait for its bytes. Cloudflare's cache lock does the same across a
 * location's isolates.
 */
const inflight = new Map<string, Promise<Filled | null>>();

/** How long a request waits on another's fill before reading for itself (a target duration). */
export const COALESCE_WAIT_MS = 4000;

/** fillsInFlight is how many fills this isolate has under way (tests). */
export function fillsInFlight(): number {
  return inflight.size;
}

function fromFilled(f: Filled): Response {
  return new Response(f.body, { status: 200, headers: f.headers });
}

/** join waits for another request's fill: its bytes, or null (not a 200, failed, too slow). */
async function join(p: Promise<Filled | null>): Promise<Filled | null> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const late = new Promise<null>((resolve) => {
    timer = setTimeout(() => resolve(null), COALESCE_WAIT_MS);
  });
  try {
    return await Promise.race([p.catch(() => null), late]);
  } finally {
    if (timer !== undefined) clearTimeout(timer);
  }
}

/**
 * cached answers from the edge cache under `key`, or calls `fill` and stores what it returns
 * (200s only). Simultaneous misses on one key in this isolate share one fill: only the first
 * reads R2 (or asks the Durable Object); a fill that isn't a 200 is not shared, each request
 * then fills for itself. The stored copy carries no per-viewer header; the caller adds those,
 * and every per-viewer check (the token) runs before this.
 */
export async function cached(
  key: string,
  fill: () => Promise<Response>
): Promise<{ res: Response; status: CacheStatus }> {
  const cache = edgeCache();
  const running = inflight.get(key);
  if (running) {
    const shared = await join(running);
    if (shared) return { res: fromFilled(shared), status: "hit" };
  } else {
    const hit = await cache.match(key);
    if (hit) return { res: hit, status: "hit" };
    // another request may have started the fill while this one looked in the cache
    const started = inflight.get(key);
    if (started) {
      const shared = await join(started);
      if (shared) return { res: fromFilled(shared), status: "hit" };
    }
  }
  return fillAndStore(cache, key, fill);
}

async function fillAndStore(
  cache: Cache,
  key: string,
  fill: () => Promise<Response>
): Promise<{ res: Response; status: CacheStatus }> {
  let share!: (f: Filled | null) => void;
  const mine = new Promise<Filled | null>((resolve) => {
    share = resolve;
  });
  inflight.set(key, mine);
  try {
    const res = await fill();
    if (res.status !== 200) {
      share(null);
      return { res, status: "miss" };
    }
    const body = await res.arrayBuffer();
    const headers = [...res.headers];
    share({ body, headers });
    const stored = new Response(body, { status: 200, headers });
    // the key stays in flight until the cache has it, so no request misses in between
    await cache.put(key, stored.clone());
    return { res: stored, status: "miss" };
  } catch (err) {
    share(null);
    throw err;
  } finally {
    if (inflight.get(key) === mine) inflight.delete(key);
  }
}
