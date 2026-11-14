// live.maktaba.tv (story 50.21 §4): what players fetch. The viewer token sits in the path, so a
// player needs no header (native players and TVs).
//
//   GET  /<token>/<channel>/master.m3u8              the rungs this session's mode allows
//   GET  /<token>/<channel>/<rung>/index.m3u8        a rung's live window (1 s at the edge)
//   GET  /<token>/<channel>/<rung>/<seq>-<hash>.ts   a segment (immutable; slate: rung "slate")
//   POST /<token>/<channel>/beat                     the session's 60 s heartbeat (metering)
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
  if (claims.mode === "wait") return problem(429, "broadcast_full", { "Retry-After": "30" });

  const get = request.method === "GET" || request.method === "HEAD";
  if (parts.length === 3 && parts[2] === "beat" && request.method === "POST")
    return beat(env, claims);
  if (parts.length === 3 && parts[2] === "master.m3u8" && get) return master(env, claims);
  if (parts.length === 4 && RUNG_RE.test(parts[2]!) && get) {
    if (parts[3] === "index.m3u8") return media(env, claims, parts[2]!);
    if (SEG_RE.test(parts[3]!)) return segment(env, ch, parts[2]!, parts[3]!);
  }
  return problem(404, "not_found");
}

function channel(env: Env, ch: string) {
  return env.CHANNEL.get(env.CHANNEL.idFromName(ch));
}

function count(env: Env, ch: string, kind: string, status: string, bytes = 0): void {
  env.AE?.writeDataPoint({ blobs: [kind, ch, status], doubles: [1, bytes], indexes: [ch] });
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
  count(env, ch, "segment", status, Number(res.headers.get("Content-Length") ?? 0));
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

async function master(env: Env, claims: ViewerClaims): Promise<Response> {
  const mode = claims.mode === "lowest" ? "lowest" : "full";
  if (globalDelivery(env) === "stream") {
    return toStream(env, claims, (await channel(env, claims.ch).streamInput())?.uid ?? null);
  }
  let redirect: PlaylistAnswer | null = null;
  const { res, status } = await cached(masterCacheKey(env, claims.ch, mode), async () => {
    const a = await channel(env, claims.ch).master(mode);
    if (a.kind === "stream") redirect = a;
    return playlistResponse(a);
  });
  const r = redirect as PlaylistAnswer | null;
  if (r?.kind === "stream") return toStream(env, claims, r.uid);
  count(env, claims.ch, "master", status);
  const out = new Response(res.body, res);
  out.headers.set("X-Maktaba-Cache", status);
  return out;
}

async function media(env: Env, claims: ViewerClaims, rung: string): Promise<Response> {
  if (globalDelivery(env) === "stream") {
    return toStream(env, claims, (await channel(env, claims.ch).streamInput())?.uid ?? null);
  }
  let redirect: PlaylistAnswer | null = null;
  const { res, status } = await cached(mediaCacheKey(env, claims.ch, rung), async () => {
    const a = await channel(env, claims.ch).media(rung);
    if (a.kind === "stream") redirect = a;
    return playlistResponse(a);
  });
  const r = redirect as PlaylistAnswer | null;
  if (r?.kind === "stream") return toStream(env, claims, r.uid);
  count(env, claims.ch, "playlist", status);
  const out = new Response(res.body, res);
  out.headers.set("X-Maktaba-Cache", status);
  return out;
}

async function beat(env: Env, claims: ViewerClaims): Promise<Response> {
  const r = await env.PUBLISHER.get(env.PUBLISHER.idFromName(claims.pub)).beat(
    claims.pub,
    claims.session,
    claims.ch,
    claims.mode
  );
  return json(200, r);
}
