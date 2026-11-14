// ingest.maktaba.tv (story 50.21 §2): the home's side, outbound only. Every request carries the
// ingest token (Authorization: Bearer mkbi1…) and X-Maktaba-Time; a segment also carries its
// SHA-256 and duration.
//
//   PUT  /v1/ingest/<channel>/<rung>/<seq>.ts   a segment (201 new, 200 retry or stale, 409 conflict)
//   PUT  /v1/ingest/<channel>/slate/<idx>.ts    a segment of the offline slate loop (50.22)
//   POST /v1/ingest/<channel>/state             the rungs' attributes; {"on_air": false} ends it
//   POST /v1/ingest/<channel>/stream            the channel's Stream live input (the fallback, §5)
//   GET  /v1/ingest/ws                          the control WebSocket (ChannelLive)
//   PUT  /v1/ingest/speedtest                   up to 10 s of body: the measured rate (50.22)
//
// And the cloud's control posts, signed with a CLOUD_PUBKEYS key (no token):
//   PUT  /v1/control/plan                       a publisher's plan state (50.20)
//   PUT  /v1/control/revocations                the revocation list (50.25)
//   GET  /v1/control/revocations                the version held
import { now } from "./clock";
import { maxSegmentBytes, MAX_SLATE, type PrecheckVerdict } from "./channel_live";
import { hex } from "./crypto";
import { globallyOff, type Env } from "./env";
import { json, problem } from "./http";
import { log } from "./log";
import { segmentKey } from "./playlist";
import type { PlanState } from "./publisher";
import { forgetRevocations, stopVerdict } from "./revocations";
import { createLiveInput, streamConfigured } from "./stream_fallback";
import { verifyControl, verifyIngest, type IngestClaims } from "./tokens";

export const HEADER_TIME = "X-Maktaba-Time";
export const HEADER_SHA256 = "X-Maktaba-Body-SHA256";
export const HEADER_DURATION = "X-Maktaba-Duration";
export const HEADER_DISCONTINUITY = "X-Maktaba-Discontinuity";
export const HEADER_SEGMENT_TIME = "X-Maktaba-Segment-Time";
export const HEADER_EDGE_NOW = "X-Maktaba-Edge-Now";
/** The edge refuses a home clock more than this far off (broadcast.clockSkew in 50.22). */
export const MAX_SKEW_MS = 60_000;
/** The longest segment the edge takes, in seconds. */
export const MAX_DURATION_S = 12;
/** Whatever the plan says, no segment is bigger than this. */
export const ABS_MAX_SEGMENT_BYTES = 48 * 1024 * 1024;
export const SPEEDTEST_MAX_MS = 10_000;
export const SPEEDTEST_MAX_BYTES = 128 * 1024 * 1024;

const RUNG_RE = /^[a-z0-9][a-z0-9_]{0,15}$/;
const CH_RE = /^[A-Za-z0-9_-]{1,64}$/;

function bearer(request: Request): string {
  const h = request.headers.get("Authorization") ?? "";
  return h.startsWith("Bearer ") ? h.slice(7).trim() : "";
}

/** clockCheck refuses a request whose X-Maktaba-Time is more than 60 s from the edge's. */
function clockCheck(request: Request, t: number): Response | null {
  const ts = Number(request.headers.get(HEADER_TIME));
  if (!Number.isFinite(ts) || ts <= 0)
    return problem(400, "missing_time", { [HEADER_EDGE_NOW]: String(t) });
  if (Math.abs(t - ts) > MAX_SKEW_MS)
    return problem(400, "clock_skew", { [HEADER_EDGE_NOW]: String(t) });
  return null;
}

/** readCapped reads a body, refusing it (null) once it passes `max` bytes. */
async function readCapped(request: Request, max: number): Promise<Uint8Array | null> {
  const declared = Number(request.headers.get("Content-Length") ?? NaN);
  if (Number.isFinite(declared) && declared > max) return null;
  if (!request.body) return new Uint8Array();
  const reader = request.body.getReader();
  const parts: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    size += value.byteLength;
    if (size > max) {
      await reader.cancel();
      return null;
    }
    parts.push(value);
  }
  const out = new Uint8Array(size);
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.byteLength;
  }
  return out;
}

async function authorize(
  request: Request,
  env: Env,
  t: number,
  want: { ch?: string; rung?: string }
): Promise<{ ok: true; claims: IngestClaims } | { ok: false; res: Response }> {
  const v = await verifyIngest(bearer(request), env.CLOUD_PUBKEYS, t, want);
  if (!v.ok) return { ok: false, res: problem(v.status, v.error) };
  const skew = clockCheck(request, t);
  if (skew) return { ok: false, res: skew };
  // a programme's slate keeps the home uploading; off_air, suspend and terminate refuse it
  const stop = await stopVerdict(
    env,
    { pub: v.claims.pub, ch: v.claims.ch, home: v.claims.home },
    t
  );
  if (stop?.stop === "off_air")
    return { ok: false, res: json(403, { error: "revoked", reason: stop.reason }) };
  return { ok: true, claims: v.claims };
}

// The Middle East's countries: Cloudflare's "me" hint (the rest go by continent).
const ME = new Set([
  "AE",
  "BH",
  "EG",
  "IQ",
  "IR",
  "IL",
  "JO",
  "KW",
  "LB",
  "OM",
  "PS",
  "QA",
  "SA",
  "SY",
  "TR",
  "YE",
]);

/**
 * locationHint places a new ChannelLive near the home (edge case "the DO's location"): the
 * first ingest request creates it, so the hint comes from that request's country. Far viewers
 * read edge-cached playlists; only misses travel to the object.
 */
export function locationHint(request?: Request): DurableObjectLocationHint | undefined {
  const cf = (request as { cf?: { country?: string; continent?: string } } | undefined)?.cf;
  if (!cf) return undefined;
  if (cf.country && ME.has(cf.country)) return "me";
  switch (cf.continent) {
    case "EU":
      return "weur";
    case "NA":
      return "enam";
    case "SA":
      return "sam";
    case "AS":
      return "apac";
    case "OC":
      return "oc";
    case "AF":
      return "afr";
    default:
      return undefined;
  }
}

function channel(env: Env, ch: string, request?: Request) {
  const hint = locationHint(request);
  return env.CHANNEL.get(env.CHANNEL.idFromName(ch), hint ? { locationHint: hint } : undefined);
}

const REFUSALS: Record<
  Exclude<PrecheckVerdict, "new" | "duplicate" | "stale" | "conflict">,
  [number, string]
> = {
  rate_limited: [429, "rate_limited"],
  revoked: [403, "revoked"],
  plan_off: [403, "broadcast_off"],
  plan_rungs: [403, "plan_rungs"],
  too_large: [413, "too_large"],
  wrong_publisher: [403, "wrong_publisher"],
};

export async function handleIngest(request: Request, env: Env, url: URL): Promise<Response> {
  const path = url.pathname;
  if (path.startsWith("/v1/control/")) return handleControl(request, env, path);
  if (globallyOff(env)) return problem(503, "broadcast_off");
  const t = now();

  if (path === "/v1/ingest/speedtest" && request.method === "PUT")
    return speedtest(request, env, t);
  if (path === "/v1/ingest/ws" && request.method === "GET") {
    const a = await authorize(request, env, t, {});
    if (!a.ok) return a.res;
    if (request.headers.get("Upgrade") !== "websocket") return problem(426, "websocket_required");
    return channel(env, a.claims.ch).fetch(request);
  }

  const seg = path.match(/^\/v1\/ingest\/([^/]+)\/([^/]+)\/(\d{1,12})\.ts$/);
  if (seg && request.method === "PUT")
    return putSegment(request, env, t, seg[1]!, seg[2]!, Number(seg[3]));
  const act = path.match(/^\/v1\/ingest\/([^/]+)\/(state|stream)$/);
  if (act && request.method === "POST") {
    const ch = act[1]!;
    if (!CH_RE.test(ch)) return problem(400, "bad_channel");
    const a = await authorize(request, env, t, { ch });
    if (!a.ok) return a.res;
    return act[2] === "state" ? postState(request, env, ch) : postStream(env, ch);
  }
  return problem(404, "not_found");
}

async function putSegment(
  request: Request,
  env: Env,
  t: number,
  ch: string,
  rung: string,
  seq: number
): Promise<Response> {
  if (!CH_RE.test(ch) || !RUNG_RE.test(rung)) return problem(400, "bad_path");
  const isSlate = rung === "slate";
  const a = await authorize(request, env, t, isSlate ? { ch } : { ch, rung });
  if (!a.ok) return a.res;
  const claims = a.claims;
  if (isSlate && seq >= MAX_SLATE) return problem(400, "slate_index");

  const dur = Number(request.headers.get(HEADER_DURATION));
  if (!Number.isFinite(dur) || dur <= 0 || dur > MAX_DURATION_S)
    return problem(400, "bad_duration");
  const wantHash = (request.headers.get(HEADER_SHA256) ?? "").toLowerCase();
  if (!/^[0-9a-f]{64}$/.test(wantHash)) return problem(400, "missing_sha256");
  const max = Math.min(ABS_MAX_SEGMENT_BYTES, maxSegmentBytes(dur, claims.kbps));
  if (env.RL && !(await env.RL.limit({ key: `ingest:${ch}:${rung}` })).success)
    return problem(429, "rate_limited");

  const declared = Number(request.headers.get("Content-Length") ?? NaN);
  const stub = channel(env, ch, request);
  const hash8 = wantHash.slice(0, 8);
  const base = { pub: claims.pub, home: claims.home, rung, rungs: claims.rungs, seq, hash8, dur };

  // the cheap checks first: a retry or a stale segment is answered before its body is read
  if (!isSlate) {
    const pre = await stub.precheck({ ...base, bytes: Number.isFinite(declared) ? declared : 0 });
    if (pre === "duplicate" || pre === "stale") return json(200, { status: pre });
    if (pre === "conflict") return problem(409, "conflict");
    if (pre !== "new") {
      const [status, error] = REFUSALS[pre];
      return problem(status, error, status === 429 ? { "Retry-After": "1" } : {});
    }
  }
  const body = await readCapped(request, max);
  if (!body) return problem(413, "too_large");
  const got = hex(await crypto.subtle.digest("SHA-256", body));
  if (got !== wantHash) return problem(400, "sha256_mismatch");

  const key = segmentKey(env.BROADCAST_ENV, ch, rung, seq, hash8);
  try {
    await env.SEGMENTS.put(key, body, { httpMetadata: { contentType: "video/mp2t" } });
  } catch (err) {
    log("warn", "R2 write failed", { component: "ingest", channel: ch, err });
    return problem(503, "storage_unavailable", { "Retry-After": "1" });
  }

  if (isSlate) {
    const r = await stub.putSlate(
      { idx: seq, hash8, key, dur, bytes: body.byteLength, at: t },
      ch,
      claims.pub
    );
    return json(r === "created" ? 201 : 200, { status: r, key });
  }
  const pdtHeader = Number(request.headers.get(HEADER_SEGMENT_TIME));
  const pdt =
    Number.isFinite(pdtHeader) && pdtHeader > 0
      ? pdtHeader
      : Number(request.headers.get(HEADER_TIME)) - dur * 1000;
  const v = await stub.append({
    ...base,
    bytes: body.byteLength,
    key,
    disc: request.headers.get(HEADER_DISCONTINUITY) === "1",
    pdt,
  });
  if (v === "new") return json(201, { status: "created", key });
  if (v === "duplicate") return json(200, { status: "duplicate" });
  // lost a race: the slot was taken by another body, or skipped meanwhile; ours goes
  await env.SEGMENTS.delete(key);
  return v === "conflict" ? problem(409, "conflict") : json(200, { status: "stale" });
}

async function postState(request: Request, env: Env, ch: string): Promise<Response> {
  let post: { rungs?: unknown; on_air?: unknown };
  try {
    post = (await request.json()) as typeof post;
  } catch {
    return problem(400, "bad_json");
  }
  const rungs = Array.isArray(post.rungs)
    ? post.rungs
        .filter(
          (
            r
          ): r is {
            name: string;
            bandwidth: number;
            resolution?: string;
            codecs?: string;
            frame_rate?: number;
          } =>
            typeof r === "object" &&
            r !== null &&
            typeof (r as { name?: unknown }).name === "string" &&
            RUNG_RE.test((r as { name: string }).name) &&
            typeof (r as { bandwidth?: unknown }).bandwidth === "number"
        )
        .map((r) => ({
          name: r.name,
          bandwidth: r.bandwidth,
          resolution:
            typeof r.resolution === "string" && /^\d{2,5}x\d{2,5}$/.test(r.resolution)
              ? r.resolution
              : undefined,
          codecs:
            typeof r.codecs === "string" && /^[A-Za-z0-9.,]{1,64}$/.test(r.codecs)
              ? r.codecs
              : undefined,
          frame_rate: typeof r.frame_rate === "number" ? r.frame_rate : undefined,
        }))
    : undefined;
  const onAir = typeof post.on_air === "boolean" ? post.on_air : undefined;
  const status = await channel(env, ch).setState({ rungs, on_air: onAir }, ch);
  return json(200, { status });
}

async function postStream(env: Env, ch: string): Promise<Response> {
  if (!streamConfigured(env)) return problem(503, "stream_not_configured");
  const stub = channel(env, ch);
  const refused = await stub.streamRefusal();
  if (refused) return problem(403, refused);
  let input = await stub.streamInput();
  if (!input) {
    try {
      input = await createLiveInput(env, ch);
    } catch (err) {
      log("warn", "stream live input failed", { component: "stream", channel: ch, err });
      return problem(502, "stream_unavailable");
    }
    await stub.setStreamInput(input);
  }
  return json(200, { rtmps: { url: input.rtmps_url, stream_key: input.stream_key } });
}

async function speedtest(request: Request, env: Env, t: number): Promise<Response> {
  const a = await authorize(request, env, t, {});
  if (!a.ok) return a.res;
  const started = Date.now();
  let bytes = 0;
  if (request.body) {
    const reader = request.body.getReader();
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      bytes += value.byteLength;
      if (Date.now() - started > SPEEDTEST_MAX_MS || bytes > SPEEDTEST_MAX_BYTES) {
        await reader.cancel();
        break;
      }
    }
  }
  const ms = Math.max(1, Date.now() - started);
  return json(200, { bytes, ms, kbps: Math.round((bytes * 8) / ms) });
}

async function handleControl(request: Request, env: Env, path: string): Promise<Response> {
  const t = now();
  if (path === "/v1/control/revocations") {
    const stub = env.REVOCATIONS.get(env.REVOCATIONS.idFromName("global"));
    if (request.method === "GET") return json(200, { version: (await stub.current()).version });
    if (request.method !== "PUT") return problem(405, "method");
    const r = await stub.install(await request.text());
    forgetRevocations();
    return r.ok ? json(200, r) : problem(r.status, r.error);
  }
  if (path === "/v1/control/plan" && request.method === "PUT") {
    const v = await verifyControl(await request.text(), "plan", env.CLOUD_PUBKEYS, t);
    if (!v.ok) return problem(v.error === "wrong_type" ? 400 : 401, v.error);
    const p = v.payload;
    const modes = ["full", "lowest", "wait", "off"];
    if (
      typeof p.pub !== "string" ||
      typeof p.version !== "number" ||
      typeof p.mode !== "string" ||
      !modes.includes(p.mode)
    )
      return problem(400, "bad_plan");
    const num = (x: unknown) => (typeof x === "number" && x > 0 ? x : undefined);
    const plan: PlanState = {
      mode: p.mode as PlanState["mode"],
      version: p.version,
      max_rungs: num(p.max_rungs),
      max_top_kbps: num(p.max_top_kbps),
      max_viewers: num(p.max_viewers),
      delivery: p.delivery === "stream" || p.delivery === "r2" ? p.delivery : null,
    };
    const r = await env.PUBLISHER.get(env.PUBLISHER.idFromName(p.pub)).setPlan(p.pub, plan);
    return r === "applied" ? json(200, { status: r }) : problem(409, "older_plan");
  }
  return problem(404, "not_found");
}
