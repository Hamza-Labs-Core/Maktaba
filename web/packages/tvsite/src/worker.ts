// The Maktaba TV public site's Worker (Epic 34, first slice; design §6).
//
// A visitor only ever reaches this Worker and Cloudflare Stream; the home is
// never called. The home posts its state outward (POST /_home/state, signed),
// which the SiteControl Durable Object keeps. A visitor's play request gets a
// Stream token signed here with the site's own key, for the channel's live
// video.
//
//   POST /_home/state      the home's lineup and live videos (HMAC, see homeauth.ts)
//   GET  /api/v1/lineup    the public channels: slug, number, name, on air (no video ids)
//   POST /api/v1/play      {slug} → {url, exp, refresh_after} | 404 no_such_channel | 503 off_air
//   GET  /api/v1/version   {server_time}
//   everything else        the static site (the ASSETS binding)
//
// Not yet: guide blobs and R2, LiveHub, sessions and limits, access modes
// (Turnstile, Access), budget (design §3, §6.2, §6.7, §7, §9).

import { HEADER_TIME, verifyHome } from "./homeauth";
import { importSigningKey, signToken } from "./sign";

/** HOME_SILENT_SECONDS: with no post from the home this long the site is off air (34.4 AC6). */
export const HOME_SILENT_SECONDS = 60;
export const DEFAULT_TOKEN_TTL_SECONDS = 600;

export interface SiteChannel {
  slug: string;
  number: number;
  name: string;
  on_air: boolean;
  video_uid?: string;
}

export interface SiteState {
  schema: number;
  at: string;
  customer_code: string;
  channels: SiteChannel[];
}

export interface Stored {
  state: SiteState;
  received_at: number; // unix seconds
  home_ts: number; // the accepted post's time (replay guard)
}

// The parts of the Workers runtime this file uses (no @cloudflare/workers-types here).
export interface DurableObjectStorageLike {
  get<T>(key: string): Promise<T | undefined>;
  put<T>(key: string, value: T): Promise<void>;
}
export interface DurableObjectStateLike {
  storage: DurableObjectStorageLike;
}
export interface DurableObjectStubLike {
  fetch(input: Request | string, init?: RequestInit): Promise<Response>;
}
export interface DurableObjectNamespaceLike {
  idFromName(name: string): unknown;
  get(id: unknown): DurableObjectStubLike;
}

export interface Env {
  SITE: DurableObjectNamespaceLike;
  ASSETS?: { fetch(req: Request): Promise<Response> };
  /** secrets (wrangler secret put): from Admin → Live TV → Public site → "Create the site's secrets" */
  HOME_HMAC: string;
  STREAM_KEY_ID: string;
  STREAM_KEY_JWK: string;
  /** vars */
  TOKEN_TTL_SECONDS?: string;
  /** STREAM_BASE_URL overrides https://customer-<code>.cloudflarestream.com (local development) */
  STREAM_BASE_URL?: string;
}

const now = (): number => Math.floor(Date.now() / 1000);

function json(status: number, body: unknown, extra: Record<string, string> = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: {
      "Content-Type": "application/json; charset=utf-8",
      "Cache-Control": "no-store",
      "X-Server-Time": new Date().toISOString(),
      ...extra,
    },
  });
}

function problem(status: number, code: string, detail: string): Response {
  return json(status, { code, detail });
}

/** SiteControl is the site's single writer: the home's latest state. */
export class SiteControl {
  constructor(private readonly ctx: DurableObjectStateLike) {}

  async fetch(req: Request): Promise<Response> {
    const url = new URL(req.url);
    if (url.pathname !== "/state") return problem(404, "not_found", "no such route");
    if (req.method === "GET") {
      const s = await this.ctx.storage.get<Stored>("state");
      return s ? json(200, s) : problem(404, "no_state", "the home has not posted yet");
    }
    if (req.method === "PUT") {
      const ts = Number(req.headers.get(HEADER_TIME));
      const prev = await this.ctx.storage.get<Stored>("state");
      if (prev && ts <= prev.home_ts) return problem(409, "replayed", "an older or repeated post");
      const state = (await req.json()) as SiteState;
      await this.ctx.storage.put<Stored>("state", { state, received_at: now(), home_ts: ts });
      return new Response(null, { status: 204 });
    }
    return problem(405, "method_not_allowed", "GET or PUT");
  }
}

function site(env: Env): DurableObjectStubLike {
  return env.SITE.get(env.SITE.idFromName("site"));
}

async function stored(env: Env): Promise<Stored | null> {
  const r = await site(env).fetch("https://site-control/state");
  return r.ok ? ((await r.json()) as Stored) : null;
}

/** live reports whether the home was heard recently enough for its state to stand. */
function live(s: Stored | null, at: number): s is Stored {
  return s !== null && at - s.received_at <= HOME_SILENT_SECONDS;
}

let keyCache: { jwk: string; key: Promise<CryptoKey> } | null = null;
function signingKey(env: Env): Promise<CryptoKey> {
  if (!keyCache || keyCache.jwk !== env.STREAM_KEY_JWK) {
    keyCache = { jwk: env.STREAM_KEY_JWK, key: importSigningKey(env.STREAM_KEY_JWK) };
  }
  return keyCache.key;
}

export function streamBase(env: Env, customerCode: string): string {
  return (env.STREAM_BASE_URL || `https://customer-${customerCode}.cloudflarestream.com`).replace(
    /\/+$/,
    ""
  );
}

async function homeState(req: Request, env: Env): Promise<Response> {
  const body = new Uint8Array(await req.arrayBuffer());
  const check = await verifyHome(
    env.HOME_HMAC,
    req.method,
    new URL(req.url).pathname,
    req.headers,
    body,
    now()
  );
  if (!check.ok) return problem(401, "unauthorized", check.why);
  try {
    const st = JSON.parse(new TextDecoder().decode(body)) as SiteState;
    if (st.schema !== 1 || !Array.isArray(st.channels))
      return problem(400, "bad_state", "schema 1 expected");
  } catch {
    return problem(400, "bad_state", "not JSON");
  }
  return site(env).fetch("https://site-control/state", {
    method: "PUT",
    body,
    headers: { [HEADER_TIME]: String(check.ts), "Content-Type": "application/json" },
  });
}

async function lineup(env: Env): Promise<Response> {
  const s = await stored(env);
  const fresh = live(s, now());
  const channels = (s?.state.channels ?? []).map((c) => ({
    slug: c.slug,
    number: c.number,
    name: c.name,
    on_air: fresh && c.on_air && !!c.video_uid,
  }));
  return json(200, {
    channels,
    home_seen_at: s ? new Date(s.received_at * 1000).toISOString() : null,
  });
}

async function play(req: Request, env: Env): Promise<Response> {
  let slug = "";
  try {
    slug = String(((await req.json()) as { slug?: unknown }).slug ?? "");
  } catch {
    return problem(400, "bad_request", "{slug} expected");
  }
  const s = await stored(env);
  const ch = s?.state.channels.find((c) => c.slug === slug);
  if (!s || !ch) return problem(404, "no_such_channel", "no such channel");
  if (!live(s, now()) || !ch.on_air || !ch.video_uid) {
    return json(
      503,
      { code: "off_air", detail: "this channel is not on the air right now" },
      { "Retry-After": "15" }
    );
  }
  const ttl = Number(env.TOKEN_TTL_SECONDS) || DEFAULT_TOKEN_TTL_SECONDS;
  const at = now();
  const token = await signToken(await signingKey(env), env.STREAM_KEY_ID, ch.video_uid, ttl, at);
  return json(200, {
    url: `${streamBase(env, s.state.customer_code)}/${token}/manifest/video.m3u8`,
    exp: at + ttl,
    refresh_after: Math.floor(ttl / 2),
  });
}

export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const { pathname } = new URL(req.url);
    if (pathname === "/_home/state") {
      return req.method === "POST"
        ? homeState(req, env)
        : problem(405, "method_not_allowed", "POST");
    }
    if (pathname === "/api/v1/lineup" && req.method === "GET") return lineup(env);
    if (pathname === "/api/v1/play" && req.method === "POST") return play(req, env);
    if (pathname === "/api/v1/version" && req.method === "GET")
      return json(200, { server_time: new Date().toISOString() });
    if (pathname.startsWith("/api/") || pathname.startsWith("/_home/"))
      return problem(404, "not_found", "no such route");
    return env.ASSETS ? env.ASSETS.fetch(req) : problem(404, "not_found", "no site assets");
  },
};
