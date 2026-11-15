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
//
// The kill switch (50.25): break-glass (BROADCAST_GLOBAL_OFF, BROADCAST_OFF_CHANNELS) is checked
// first, before the token; a revoked channel answers 503 off_air with the reason on play, on the
// heartbeat and at the next playlist, so a playing viewer gets the off-air card within a reload.
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
import { globalDelivery, type Env } from "./env";
import * as ev from "./events";
import { geoBlocked, geoFor } from "./geo";
import { json, preflight, problem, withLiveHeaders } from "./http";
import { readCapped } from "./ingest";
import { log } from "./log";
import { breakGlass, stopVerdict } from "./revocations";
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
  const glass = breakGlass(env, ch);
  if (glass) return offAir(glass.reason);
  const v = await verifyViewer(token, env.VIEWER_TOKEN_KEY, now(), ch);
  if (!v.ok) return problem(v.status, v.error);
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
  else if (parts.length === 3 && parts[2] === "master.m3u8" && get)
    res = (await geoCheck(request, env, ch)) ?? (await master(env, claims));
  else if (parts.length === 4 && RUNG_RE.test(parts[2]!) && get && parts[3] === "index.m3u8")
    res = (await geoCheck(request, env, ch)) ?? (await media(env, claims, parts[2]!));
  else if (parts.length === 4 && RUNG_RE.test(parts[2]!) && get && SEG_RE.test(parts[3]!))
    res = await segment(env, ch, parts[2]!, parts[3]!);
  else return problem(404, "not_found");
  await recordRefusal(env, claims, parts[2] === "master.m3u8", res, request);
  return res;
}

/**
 * recordRefusal writes `blocked` for a revoked channel and for a viewer outside the channel's
 * countries (once per session start: on the master; geo with the viewer's country, 50.23 §5) and
 * `error` for a 5xx the edge answered, by its code. A channel the revocation list stopped
 * answers 503 off_air with the entry's reason (50.25): that is a block, never an edge error.
 */
async function recordRefusal(
  env: Env,
  claims: ViewerClaims,
  isMaster: boolean,
  res: Response,
  request: Request
): Promise<void> {
  if (res.status < 403) return;
  if (res.status === 451) {
    if (isMaster) ev.write(env, ev.blocked(claims.pub, claims.ch, "geo", viewerCountry(request)));
    return;
  }
  const { code, reason } = await problemOf(res);
  const stopped = code === "off_air" && reason !== "";
  if (stopped) {
    if (isMaster) ev.write(env, ev.blocked(claims.pub, claims.ch, "revoked"));
  } else if (res.status >= 500)
    ev.write(env, ev.error(claims.pub, claims.ch, code || `http_${res.status}`));
}

async function problemOf(res: Response): Promise<{ code: string; reason: string }> {
  try {
    const body = (await res.clone().json()) as { error?: unknown; reason?: unknown };
    return {
      code: typeof body.error === "string" ? body.error : "",
      reason: typeof body.reason === "string" ? body.reason : "",
    };
  } catch {
    return { code: "", reason: "" };
  }
}

/** viewerCountry is the viewer's country as Cloudflare tells it ("" when it can't). */
function viewerCountry(request: Request): string {
  return request.headers.get("CF-IPCountry") ?? "";
}

/**
 * geoCheck (50.23 §5) answers 451 geo_blocked to a viewer outside the channel's countries, at
 * play (master) and at every playlist; recordRefusal writes the blocked event, by country, for
 * the publisher's analytics (50.28).
 */
async function geoCheck(request: Request, env: Env, ch: string): Promise<Response | null> {
  if (!geoBlocked(await geoFor(env, ch), viewerCountry(request))) return null;
  return problem(451, "geo_blocked");
}

/** offAir is a stopped channel's answer: the player shows the off-air card. */
function offAir(reason: string): Response {
  return json(503, { error: "off_air", reason });
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
  if (answer.kind === "error")
    return answer.reason
      ? json(answer.status, { error: answer.error, reason: answer.reason })
      : problem(answer.status, answer.error);
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
  if (a.kind === "error") return playlistResponse(a);
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

/**
 * BeatReport is what a player may say with its heartbeat; every field is optional. Switches and
 * errors are written only with a counted beat (at most one a minute per session); a beat that
 * answers counted: false wasn't recorded, and the player sends them again with its next one.
 */
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
    const body = await readCapped(request, BEAT_BODY_MAX);
    if (!body || body.byteLength === 0) return {};
    const r = JSON.parse(new TextDecoder().decode(body)) as unknown;
    return typeof r === "object" && r !== null ? (r as BeatReport) : {};
  } catch {
    return {};
  }
}

/** num is a player's number, or 0 when it isn't one; capped so one report can't skew a sum. */
const num = (n: unknown, max: number): number =>
  typeof n === "number" && Number.isFinite(n) && n >= 0 ? Math.min(n, max) : 0;
const MS_MAX = 600_000; // 10 minutes
const KBPS_MAX = 100_000;
const rungName = (r: unknown): string => (typeof r === "string" && RUNG_RE.test(r) ? r : "");

async function beat(request: Request, env: Env, claims: ViewerClaims): Promise<Response> {
  const report = await readReport(request);
  const stop = await stopVerdict(env, { pub: claims.pub, ch: claims.ch }, now());
  if (stop?.stop === "off_air") return offAir(stop.reason);
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
    if (r.first) ev.write(env, ev.play(viewer, num(report.startup_ms, MS_MAX)));
    ev.write(
      env,
      ev.beat(
        viewer,
        rungName(report.rung),
        Math.min(num(report.rebuffer_ms, MS_MAX), 60_000), // at most the minute itself
        num(report.kbps, KBPS_MAX)
      )
    );
    for (const s of (Array.isArray(report.switches) ? report.switches : []).slice(0, 10)) {
      const from = rungName(s?.from);
      const to = rungName(s?.to);
      if (from && to)
        ev.write(env, ev.rungSwitch(claims.pub, claims.ch, from, to, num(s.ms, MS_MAX)));
    }
    for (const code of (Array.isArray(report.errors) ? report.errors : []).slice(0, 10)) {
      if (typeof code === "string" && CODE_RE.test(code))
        ev.write(env, ev.error(claims.pub, claims.ch, code));
    }
  }
  return json(200, { counted: r.counted, concurrent: r.concurrent, delivery: r.delivery });
}
