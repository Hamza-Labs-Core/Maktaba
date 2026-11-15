// The broadcast rehearsal's edge entry (Epic 50 story 50.29): the real Worker of
// web/packages/broadcast-edge, unchanged, with counters around its bindings. One copy runs per
// simulated location (server.mjs), so each location has its own cache, as Cloudflare's do; the
// Durable Objects and the R2 buckets are shared. Rehearsal only: never bundled for a deploy.
//
// The counters are the edge's side of the fan-out rule (one upload per segment per rung, R2 read
// at most once per object per location): every R2 get by key, every ingest PUT the edge took, and
// every Durable Object stub the Worker asked for. They are read at /__rehearsal/counters, which
// only this entry answers. The fake Analytics Engine also keeps its points (the newest
// AE_KEEP), read at /__rehearsal/ae?since_ms=N by the rehearsal's cloud monitor
// (cloud/cmd/rehearsal-broadcast-monitor), as the cloud reads the dataset through the SQL API.
import worker, * as edge from "@edge/worker";
import { advanceClock } from "@edge/clock";
import type { Env } from "@edge/env";

interface Counters {
  r2_get: number;
  r2_get_by_key: Record<string, number>;
  r2_put: number;
  ingest_put: number;
  ingest_put_created: number;
  /** the PUTs the edge stored (201), by rung (the slate included, as "slate") */
  ingest_put_by_rung: Record<string, number>;
  do_get: number;
  ae_points: number;
  ae_by_event: Record<string, number>;
  /** `ingest` points that carry glass-to-glass (the canary publisher's, 50.28 §5) */
  ae_ingest_glass: number;
  /** `blocked` and `error` points by their code ("blocked:revoked", "error:edge_error") */
  ae_by_code: Record<string, number>;
  requests: number;
  /**
   * viewers' segment GETs answered 200, by how long this Worker took from the request to its
   * response (the edge's own answer time: the cache, R2 and the Durable Object, not the body's
   * transfer nor the client's queue), counted in SEG_ANSWER_BOUNDS_MS buckets (the last is
   * longer than every bound)
   */
  seg_answer_ms: number[];
}

/** The answer-time buckets' upper bounds, in ms (stack_test.go segAnswerBounds, the same). */
const SEG_ANSWER_BOUNDS_MS = [5, 10, 25, 50, 100, 250, 500, 1000, 1500, 2500, 5000];

const fresh = (): Counters => ({
  r2_get: 0,
  r2_get_by_key: {},
  r2_put: 0,
  ingest_put: 0,
  ingest_put_created: 0,
  ingest_put_by_rung: {},
  do_get: 0,
  ae_points: 0,
  ae_by_event: {},
  ae_ingest_glass: 0,
  ae_by_code: {},
  requests: 0,
  seg_answer_ms: new Array(SEG_ANSWER_BOUNDS_MS.length + 1).fill(0),
});

/** recordSegAnswer counts one segment answer in its bucket. */
function recordSegAnswer(ms: number): void {
  let i = SEG_ANSWER_BOUNDS_MS.findIndex((b) => ms <= b);
  if (i < 0) i = SEG_ANSWER_BOUNDS_MS.length;
  counts.seg_answer_ms[i]++;
}
let counts = fresh();

/** One Analytics Engine point as written, with when (this isolate's clock) and its order. */
interface AEPoint {
  seq: number;
  at_ms: number;
  blobs: string[];
  doubles: number[];
}
const AE_KEEP = 20000;
let aePoints: AEPoint[] = [];
let aeSeq = 0;
/**
 * this isolate's points: a redeploy starts a new epoch, and the monitor reads it from its start
 * (made on first use: Workers allow no random values at global scope)
 */
let aeEpoch = "";

function wrapBucket(b: R2Bucket): R2Bucket {
  return {
    get: (key: string, opts?: R2GetOptions) => {
      counts.r2_get++;
      counts.r2_get_by_key[key] = (counts.r2_get_by_key[key] ?? 0) + 1;
      return b.get(key, opts);
    },
    put: (key: string, value: unknown, opts?: R2PutOptions) => {
      counts.r2_put++;
      return b.put(key, value as ReadableStream, opts);
    },
    head: (key: string) => b.head(key),
    delete: (keys: string | string[]) => b.delete(keys),
    list: (opts?: R2ListOptions) => b.list(opts),
    createMultipartUpload: (key: string, opts?: R2MultipartOptions) =>
      b.createMultipartUpload(key, opts),
    resumeMultipartUpload: (key: string, id: string) => b.resumeMultipartUpload(key, id),
  } as unknown as R2Bucket;
}

function wrapNamespace(ns: DurableObjectNamespace): DurableObjectNamespace {
  return new Proxy(ns, {
    get(target, prop) {
      const v = Reflect.get(target, prop) as unknown;
      if (prop === "get")
        return (id: DurableObjectId, o?: DurableObjectNamespaceGetDurableObjectOptions) => {
          counts.do_get++;
          return target.get(id, o);
        };
      return typeof v === "function" ? (v as (...a: unknown[]) => unknown).bind(target) : v;
    },
  });
}

/**
 * The fake Analytics Engine: points counted by event (blob 1 in the 50.28 schema), the codes of
 * `blocked` and `error` points (blob 9) and the `ingest` points with glass-to-glass (double 8),
 * and every point kept (aePoints) for the cloud monitor.
 */
const fakeAE = {
  writeDataPoint(p: { blobs?: string[]; doubles?: number[] }) {
    counts.ae_points++;
    aePoints.push({
      seq: ++aeSeq,
      at_ms: Date.now(),
      blobs: [...(p.blobs ?? [])],
      doubles: [...(p.doubles ?? [])],
    });
    if (aePoints.length > AE_KEEP) aePoints = aePoints.slice(aePoints.length - AE_KEEP);
    const ev = p.blobs?.[0] ?? "";
    counts.ae_by_event[ev] = (counts.ae_by_event[ev] ?? 0) + 1;
    if (ev === "blocked" || ev === "error") {
      const k = `${ev}:${p.blobs?.[8] ?? ""}`;
      counts.ae_by_code[k] = (counts.ae_by_code[k] ?? 0) + 1;
    }
    if (ev === "ingest" && (p.doubles?.[7] ?? 0) > 0) counts.ae_ingest_glass++;
  },
};

const wrapped = new WeakMap<object, Env>();
function wrapEnv(env: Env): Env {
  const hit = wrapped.get(env);
  if (hit) return hit;
  const e = {
    ...env,
    SEGMENTS: wrapBucket(env.SEGMENTS),
    CHANNEL: wrapNamespace(env.CHANNEL),
    PUBLISHER: wrapNamespace(env.PUBLISHER),
    AE: fakeAE,
  } as Env;
  if (env.GUIDE) e.GUIDE = wrapBucket(env.GUIDE);
  wrapped.set(env, e);
  return e;
}

// The Durable Objects see the same counters (R2 lists and deletes from the clean-up alarm).
type Ctor<T> = new (ctx: DurableObjectState, env: Env) => T;
function wrapClass<T>(C: Ctor<T>): Ctor<T> {
  return class extends (C as Ctor<object>) {
    constructor(ctx: DurableObjectState, env: Env) {
      super(ctx, wrapEnv(env));
    }
  } as unknown as Ctor<T>;
}
export const ChannelLive = wrapClass(edge.ChannelLive);
export const PublisherControl = wrapClass(edge.PublisherControl);
export const Revocations = wrapClass(edge.Revocations);
export const ViewerAccess = wrapClass(edge.ViewerAccess);
export const Pairing = wrapClass(edge.Pairing);

async function control(request: Request, url: URL): Promise<Response> {
  const json = (v: unknown) =>
    new Response(JSON.stringify(v), {
      headers: { "Content-Type": "application/json" },
    });
  switch (url.pathname) {
    case "/__rehearsal/counters":
      return json(counts);
    case "/__rehearsal/ae": {
      // kept across /__rehearsal/reset: the counters are a scenario's, the points the cloud's
      const since = Number(url.searchParams.get("since_ms") ?? "0") || 0;
      aeEpoch ||= crypto.randomUUID();
      return json({
        ae_epoch: aeEpoch,
        ae_points: aePoints.filter((p) => p.at_ms >= since),
      });
    }
    case "/__rehearsal/reset":
      if (request.method !== "POST") break;
      counts = fresh();
      return json({ ok: true });
    case "/__rehearsal/clock": {
      // the fast clock (the 24 h scenarios): this isolate's Worker and Durable Objects
      if (request.method !== "POST") break;
      const ms = Number(url.searchParams.get("advance") ?? "0");
      if (!Number.isFinite(ms) || ms < 0) break;
      advanceClock(ms);
      return json({ ok: true, advanced_ms: ms });
    }
  }
  return new Response("not found", { status: 404 });
}

// The planted regression (story 50.29 T03): REHEARSAL_PLANT=per-viewer-fetch makes this
// location's cache miss every time, so each viewer's segment is an R2 read. The fan-out's
// self-test must fail on it, naming the R2 counter. Done here, not with Miniflare's cache option,
// so it holds whatever the simulator's options do.
let planted = false;
function plant(env: Env): void {
  if (
    planted ||
    (env as unknown as { REHEARSAL_PLANT?: string }).REHEARSAL_PLANT !== "per-viewer-fetch"
  )
    return;
  planted = true;
  const real = (caches as unknown as { default: Cache }).default;
  const missing: Cache = {
    match: async () => undefined,
    put: (k: RequestInfo | URL, v: Response) => real.put(k, v),
    delete: (k: RequestInfo | URL) => real.delete(k),
  } as Cache;
  Object.defineProperty(globalThis, "caches", {
    configurable: true,
    value: { default: missing, open: async () => missing },
  });
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    plant(env);
    const url = new URL(request.url);
    if (url.pathname.startsWith("/__rehearsal/")) return control(request, url);
    counts.requests++;
    const ingestPut = request.method === "PUT" && url.pathname.startsWith("/v1/ingest/");
    if (ingestPut) counts.ingest_put++;
    const segGet = request.method === "GET" && url.pathname.endsWith(".ts");
    // Date.now() moves on I/O in a Worker: this spans the cache, R2 and Durable Object waits
    const t0 = Date.now();
    const res = await (worker as ExportedHandler<Env>).fetch!(
      request as Request<unknown, IncomingRequestCfProperties>,
      wrapEnv(env),
      ctx
    );
    if (segGet && res.status === 200) recordSegAnswer(Date.now() - t0);
    if (ingestPut && res.status === 201) {
      counts.ingest_put_created++;
      const rung = url.pathname.split("/")[4] ?? "";
      counts.ingest_put_by_rung[rung] = (counts.ingest_put_by_rung[rung] ?? 0) + 1;
    }
    return res;
  },
  async scheduled(c: ScheduledController, env: Env, ctx: ExecutionContext): Promise<void> {
    await (worker as ExportedHandler<Env>).scheduled!(c, wrapEnv(env), ctx);
  },
} satisfies ExportedHandler<Env>;
