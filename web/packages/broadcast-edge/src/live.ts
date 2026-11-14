// live.maktaba.tv (story 50.21 §4): what players fetch. The viewer token sits in the path, so a
// player needs no header (native players and TVs).
//
//   GET  /<token>/<channel>/master.m3u8              the rungs this session's mode allows
//   GET  /<token>/<channel>/<rung>/index.m3u8        a rung's live window (1 s at the edge)
//   GET  /<token>/<channel>/<rung>/<seq>-<hash>.ts   a segment (immutable; slate: rung "slate")
//   POST /<token>/<channel>/beat                     the session's 60 s heartbeat (metering)
//
// Story 50.28: the analytics events (events.ts). A beat may carry what the player saw:
// {rung, rebuffer_ms, kbps, startup_ms, device, switches: [{from, to, ms}], errors: [code]}.
// The session's first counted beat writes `play`; each counted beat writes `beat`; a refusal
// for a full channel or a revoked one writes `blocked`; a 5xx the edge answers writes `error`.
//
// A segment costs a token check (no Durable Object, no R2), then the edge cache under a key with
// no token; only a miss reads R2. A playlist miss asks ChannelLive, which also answers whether
// the channel is revoked or off air. The home is never fetched.
import {
  cached,
  masterCacheKey,
  mediaCacheKey,
  PLAYLIST_CACHE_CONTROL,
  SEGMENT_CACHE_CONTROL,
  segmentCacheKey,
} from "./cache";
import type { PlaylistAnswer } from "./channel_live";
import { now } from "./clock";
import { globalDelivery, globallyOff, type Env } from "./env";
import * as ev from "./events";
import { json, preflight, problem, withLiveHeaders } from "./http";
import { log } from "./log";
import { manifestURL, streamConfigured } from "./stream_fallback";
import { verifyViewer, type ViewerClaims } from "./tokens";

const SEG_RE = /^\d{1,12}-[0-9a-f]{8}\.ts$/;
const RUNG_RE = /^[a-z0-9][a-z0-9_]{0,15}$/;
const HLS = "application/vnd.apple.mpegurl; charset=utf-8";

export async function handleLive(request: Request, env: Env, url: URL): Promise<Response> {
  if (request.method === "OPTIONS") return preflight(request, env);
  const res = await route(request, env, url);
  return withLiveHeaders(res, request, env);
}

async function route(request: Request, env: Env, url: URL): Promise<Response> {
  const parts = url.pathname.split("/").filter(Boolean);
  if (parts.length < 3) return problem(404, "not_found");
  const [token, ch] = [parts[0]!, parts[1]!];
  const v = await verifyViewer(token, env.VIEWER_TOKEN_KEY, now(), ch);
  if (!v.ok) return problem(v.status, v.error);
  if (globallyOff(env)) return problem(503, "broadcast_off");
  const claims = v.claims;
  if (claims.mode === "off") return problem(403, "broadcast_off");
  if (claims.mode === "wait") {
    if (parts[2] === "master.m3u8") ev.write(env, ev.blocked(claims.pub, ch, "full"));
    return problem(429, "broadcast_full", { "Retry-After": "30" });
  }

  const get = request.method === "GET" || request.method === "HEAD";
  let res: Response;
  if (parts.length === 3 && parts[2] === "beat" && request.method === "POST")
    res = await beat(request, env, claims);
  else if (parts.length === 3 && parts[2] === "master.m3u8" && get) res = await master(env, claims);
  else if (parts.length === 4 && RUNG_RE.test(parts[2]!) && get && parts[3] === "index.m3u8")
    res = await media(env, claims, parts[2]!);
  else if (parts.length === 4 && RUNG_RE.test(parts[2]!) && get && SEG_RE.test(parts[3]!))
    res = await segment(env, ch, parts[2]!, parts[3]!);
  else return problem(404, "not_found");
  await recordRefusal(env, claims, parts[2] === "master.m3u8", res);
  return res;
}

/**
 * recordRefusal writes `blocked` for a revoked channel (once per session start: on the master)
 * and `error` for a 5xx the edge answered, by its code.
 */
async function recordRefusal(
  env: Env,
  claims: ViewerClaims,
  isMaster: boolean,
  res: Response
): Promise<void> {
  if (res.status < 403) return;
  const code = await problemCode(res);
  if (res.status === 403 && code === "revoked" && isMaster)
    ev.write(env, ev.blocked(claims.pub, claims.ch, "revoked"));
  else if (res.status >= 500)
    ev.write(env, ev.error(claims.pub, claims.ch, code || `http_${res.status}`));
}

async function problemCode(res: Response): Promise<string> {
  try {
    const body = (await res.clone().json()) as { error?: unknown };
    return typeof body.error === "string" ? body.error : "";
  } catch {
    return "";
  }
}

function channel(env: Env, ch: string) {
  return env.CHANNEL.get(env.CHANNEL.idFromName(ch));
}

async function segment(env: Env, ch: string, rung: string, name: string): Promise<Response> {
  const key = `${env.BROADCAST_ENV}/${ch}/${rung}/${name}`;
  const { res, status } = await cached(segmentCacheKey(env, ch, rung, name), async () => {
    let obj: R2ObjectBody | null;
    try {
      obj = await env.SEGMENTS.get(key);
    } catch (err) {
      log("warn", "R2 read failed", { component: "live", channel: ch, err });
      return problem(503, "storage_unavailable", { "Retry-After": "1" });
    }
    if (!obj) return problem(404, "gone");
    return new Response(obj.body, {
      status: 200,
      headers: {
        "Content-Type": "video/mp2t",
        "Cache-Control": SEGMENT_CACHE_CONTROL,
        ETag: obj.httpEtag,
      },
    });
  });
  const out = new Response(res.body, res);
  out.headers.set("X-Maktaba-Cache", status);
  return out;
}

function playlistResponse(answer: PlaylistAnswer): Response {
  if (answer.kind === "playlist") {
    return new Response(answer.body, {
      status: 200,
      headers: { "Content-Type": HLS, "Cache-Control": PLAYLIST_CACHE_CONTROL },
    });
  }
  if (answer.kind === "error") return problem(answer.status, answer.error);
  return problem(500, "unexpected");
}

/** toStream redirects a player to the channel's signed Stream manifest (the fallback, §5). */
async function toStream(env: Env, claims: ViewerClaims, uid: string | null): Promise<Response> {
  if (!uid || !streamConfigured(env))
    return problem(503, "stream_not_ready", { "Retry-After": "5" });
  const nowS = Math.floor(now() / 1000);
  const ttl = Math.max(60, claims.exp - nowS);
  return new Response(null, {
    status: 302,
    headers: { Location: await manifestURL(env, uid, ttl, nowS), "Cache-Control": "no-store" },
  });
}

/** viaStream is the global Stream delivery: the channel's refusals first, then the redirect. */
async function viaStream(env: Env, claims: ViewerClaims): Promise<Response> {
  const a = await channel(env, claims.ch).streamFor();
  if (a.kind === "error") return problem(a.status, a.error);
  return toStream(env, claims, a.kind === "stream" ? a.uid : null);
}

async function master(env: Env, claims: ViewerClaims): Promise<Response> {
  const mode = claims.mode === "lowest" ? "lowest" : "full";
  if (globalDelivery(env) === "stream") return viaStream(env, claims);
  let redirect: PlaylistAnswer | null = null;
  const { res, status } = await cached(masterCacheKey(env, claims.ch, mode), async () => {
    const a = await channel(env, claims.ch).master(mode);
    if (a.kind === "stream") redirect = a;
    return playlistResponse(a);
  });
  const r = redirect as PlaylistAnswer | null;
  if (r?.kind === "stream") return toStream(env, claims, r.uid);
  const out = new Response(res.body, res);
  out.headers.set("X-Maktaba-Cache", status);
  return out;
}

async function media(env: Env, claims: ViewerClaims, rung: string): Promise<Response> {
  if (globalDelivery(env) === "stream") return viaStream(env, claims);
  let redirect: PlaylistAnswer | null = null;
  const mode = claims.mode === "lowest" ? "lowest" : "full";
  const { res, status } = await cached(mediaCacheKey(env, claims.ch, rung, mode), async () => {
    const a = await channel(env, claims.ch).media(rung, mode);
    if (a.kind === "stream") redirect = a;
    return playlistResponse(a);
  });
  const r = redirect as PlaylistAnswer | null;
  if (r?.kind === "stream") return toStream(env, claims, r.uid);
  const out = new Response(res.body, res);
  out.headers.set("X-Maktaba-Cache", status);
  return out;
}

/** BeatReport is what a player may say with its heartbeat; every field is optional. */
export interface BeatReport {
  rung?: string;
  rebuffer_ms?: number;
  kbps?: number;
  startup_ms?: number;
  device?: string;
  switches?: { from?: string; to?: string; ms?: number }[];
  errors?: string[];
}

const BEAT_BODY_MAX = 4096;
const CODE_RE = /^[a-z0-9_]{1,40}$/;

async function readReport(request: Request): Promise<BeatReport> {
  try {
    const text = await request.text();
    if (!text || text.length > BEAT_BODY_MAX) return {};
    const r = JSON.parse(text) as unknown;
    return typeof r === "object" && r !== null ? (r as BeatReport) : {};
  } catch {
    return {};
  }
}

const num = (n: unknown): number => (typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : 0);
const rungName = (r: unknown): string => (typeof r === "string" && RUNG_RE.test(r) ? r : "");

async function beat(request: Request, env: Env, claims: ViewerClaims): Promise<Response> {
  const report = await readReport(request);
  const r = await env.PUBLISHER.get(env.PUBLISHER.idFromName(claims.pub)).beat(
    claims.pub,
    claims.session,
    claims.ch,
    claims.mode
  );
  const viewer: ev.ViewerContext = {
    pub: claims.pub,
    ch: claims.ch,
    country: ev.countryOf(claims.country, request),
    device: ev.deviceClass(
      report.device ?? request.headers.get("X-Maktaba-Device"),
      request.headers.get("User-Agent")
    ),
    access: claims.acc === "open" ? "open" : "invite",
    mode: claims.mode,
    session: await ev.sessionHash(env.VIEWER_TOKEN_KEY, claims.session, now()),
  };
  if (r.counted) {
    if (r.first) ev.write(env, ev.play(viewer, num(report.startup_ms)));
    ev.write(
      env,
      ev.beat(viewer, rungName(report.rung), num(report.rebuffer_ms), num(report.kbps))
    );
  }
  for (const s of (Array.isArray(report.switches) ? report.switches : []).slice(0, 10)) {
    const from = rungName(s?.from);
    const to = rungName(s?.to);
    if (from && to) ev.write(env, ev.rungSwitch(claims.pub, claims.ch, from, to, num(s.ms)));
  }
  for (const code of (Array.isArray(report.errors) ? report.errors : []).slice(0, 10)) {
    if (typeof code === "string" && CODE_RE.test(code))
      ev.write(env, ev.error(claims.pub, claims.ch, code));
  }
  return json(200, { counted: r.counted, concurrent: r.concurrent, delivery: r.delivery });
}
