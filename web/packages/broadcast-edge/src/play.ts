// watch.maktaba.tv's viewer API (story 50.26). Viewers have no account and never pay: there is
// no payment route on watch. or live. (WATCH_ROUTES; a test lists them).
//
//   GET  /i/<invite>                 the invite page (noindex): one button redeems it
//   POST /i/<invite>                 the page's form: redeem, set the pass cookie, say so
//   POST /api/v1/redeem              {invite} or {publisher, code}: a guest pass (cookie and body)
//   POST /api/v1/open/turnstile      {token}: an open session after Turnstile (cookie and body)
//   POST /api/v1/open/challenge      an attestation challenge (a nonce, once, 5 minutes)
//   POST /api/v1/open/attest         {platform, attestation, nonce}: an app's open session
//   POST /api/v1/play                {publisher, channel}: a session and its viewer token
//   POST /api/v1/play/<session>/beat {publisher, beat_key, rung, stats}: the 60 s heartbeat
//
// A credential comes as a cookie (the web: mkp_<publisher> for a guest pass, mko for an open
// session) or as Authorization: Bearer (the apps keep it in the keychain or keystore). Every
// answer carries X-Server-Time (ms), so a TV with a wrong clock uses the edge's.
import { now } from "./clock";
import { globallyOff, type Env } from "./env";
import { newChallenge, verifyAttestation } from "./attest";
import { problem } from "./http";
import {
  CODE_RE,
  mintOpen,
  mintPass,
  OPEN_PREFIX,
  OPEN_TTL_S,
  PASS_PREFIX,
  PASS_TTL_S,
  randomId,
  readOpen,
  readPass,
  verifyInvite,
  type InviteClaims,
} from "./invites";
import { clientIP, ipHash, rateLimited, REFRESH_AFTER_S, TOKEN_TTL_S } from "./limits";
import type { Cred, PlanView } from "./access";
import type { ChannelStatus } from "./channel_live";
import { isRevoked, revocationsFor } from "./revocations";
import { ERROR_KEYS, langOf, t, type Lang } from "./strings";
import { mintViewerToken, type Mode } from "./tokens";
import { verifyTurnstile } from "./turnstile";
import { handleSite } from "./site/routes";
import { DEFAULT_LIVE_HOST } from "./env";

/** Every route watch.maktaba.tv answers (T07: none takes a payment). */
export const WATCH_ROUTES = [
  "GET /i/<invite>",
  "POST /i/<invite>",
  "POST /api/v1/redeem",
  "POST /api/v1/open/turnstile",
  "POST /api/v1/open/challenge",
  "POST /api/v1/open/attest",
  "POST /api/v1/play",
  "POST /api/v1/play/<session>/beat",
  // 50.27: the pages and the apps' reads and TV pairing (site/routes.ts)
  "GET /s/watch.js",
  "GET /s/watch.css",
  "GET /",
  "GET /<lang>",
  "GET /<lang>/<publisher>",
  "GET /<lang>/<publisher>/<channel>",
  "GET /<lang>/<publisher>/guide",
  "GET /<lang>/legal/<page>",
  "GET /pair",
  "POST /pair",
  "GET /api/v1/lineup",
  "GET /api/v1/guide",
  "POST /api/v1/pair",
  "GET /api/v1/pair/<nonce>",
  "POST /api/v1/pair/<nonce>",
] as const;

const UUIDISH = /^[A-Za-z0-9-]{1,64}$/;
const CH_RE = /^[a-z0-9]{1,64}$/;
const SESSION_RE = /^[A-Za-z0-9_-]{16,32}$/;
const OPEN_COOKIE = "mko";

function withServerTime(res: Response): Response {
  const out = new Response(res.body, res);
  out.headers.set("X-Server-Time", String(now()));
  if (!out.headers.has("Cache-Control")) out.headers.set("Cache-Control", "no-store");
  // a channel the publisher lets search engines index (50.26 §2) says so; nothing else is
  if (out.headers.get("X-Maktaba-Indexable") === "1") out.headers.delete("X-Maktaba-Indexable");
  else out.headers.set("X-Robots-Tag", "noindex");
  out.headers.set("X-Content-Type-Options", "nosniff");
  return out;
}

export function answer(
  status: number,
  body: unknown,
  headers: Record<string, string> = {}
): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

/** refuse answers an error with its viewer message key (and the words in the asked language). */
export function refuse(
  request: Request,
  status: number,
  error: string,
  extra: Record<string, unknown> = {}
): Response {
  const key = ERROR_KEYS[error];
  const code = error === "broadcast_wait" ? "broadcast_full" : error;
  const body: Record<string, unknown> = { error: code, ...extra };
  if (key) {
    body.key = key;
    body.message = t(key, langOf(request));
  }
  const headers: Record<string, string> = {};
  if (status === 429) headers["Retry-After"] = error === "rate_limited" ? "60" : "30";
  return answer(status, body, headers);
}

export function cookies(request: Request): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of (request.headers.get("Cookie") ?? "").split(";")) {
    const i = part.indexOf("=");
    if (i > 0) out[part.slice(0, i).trim()] = part.slice(i + 1).trim();
  }
  return out;
}

export function passCookieName(pub: string): string {
  return `mkp_${pub}`;
}

export function setCookie(name: string, value: string, maxAge: number): string {
  return `${name}=${value}; Path=/; Max-Age=${maxAge}; HttpOnly; Secure; SameSite=Lax`;
}

export async function body(request: Request): Promise<Record<string, unknown>> {
  try {
    const text = await request.text();
    if (text.length > 16384) return {};
    const v = JSON.parse(text) as unknown;
    return typeof v === "object" && v !== null ? (v as Record<string, unknown>) : {};
  } catch {
    return {};
  }
}

export function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export function viewer(env: Env, pub: string) {
  return env.VIEWER.get(env.VIEWER.idFromName(pub));
}

export async function handleWatch(request: Request, env: Env, url: URL): Promise<Response> {
  return withServerTime(await route(request, env, url));
}

async function route(request: Request, env: Env, url: URL): Promise<Response> {
  const path = url.pathname;
  const ip = clientIP(request);
  const isPlay = path === "/api/v1/play";
  if (await rateLimited(isPlay ? env.PLAY_RL : env.API_RL, `${isPlay ? "play" : "api"}:${ip}`))
    return refuse(request, 429, "rate_limited");
  if (path.startsWith("/i/")) {
    const token = decodeURIComponent(path.slice(3));
    if (request.method === "GET") return invitePage(request, token);
    if (request.method === "POST") return invitePost(request, env, token);
    return problem(405, "method");
  }
  const site = await handleSite(request, env, url);
  if (site) return site;
  if (request.method !== "POST") return problem(404, "not_found");
  if (path === "/api/v1/redeem") return redeem(request, env);
  if (path === "/api/v1/open/turnstile") return openTurnstile(request, env);
  if (path === "/api/v1/open/challenge") return answer(200, await newChallenge(env));
  if (path === "/api/v1/open/attest") return openAttest(request, env);
  if (isPlay) return play(request, env);
  const m = /^\/api\/v1\/play\/([^/]+)\/beat$/.exec(path);
  if (m) return beat(request, env, m[1]!);
  return problem(404, "not_found");
}

// ---- invites ------------------------------------------------------------------------------

export type Redeemed =
  | { ok: true; pass: string; claims: InviteClaims; reused: boolean }
  | { ok: false; status: number; error: string };

/**
 * redeemToken checks an invite and counts one device's use. `reuse` (the default) lets a device
 * that already holds a pass of the same invite keep it; pairing a TV never reuses the phone's.
 */
export async function redeemToken(
  request: Request,
  env: Env,
  token: string,
  reuse = true
): Promise<Redeemed> {
  const v = await verifyInvite(token, env.CLOUD_PUBKEYS, now());
  if (!v.ok) return v;
  const c = v.claims;
  // a revoked publisher (50.25) gets no new viewers
  if (isRevoked(await revocationsFor(env), { pub: c.pub, ch: "" }))
    return { ok: false, status: 410, error: "invite_revoked" };
  const held = reuse
    ? await readPass(
        (cookies(request)[passCookieName(c.pub)] ?? bearer(request)) || "",
        env.VIEWER_TOKEN_KEY,
        now()
      )
    : null;
  const r = await viewer(env, c.pub).redeem(c, held?.inv === c.inv ? held.id : undefined);
  if (!r.ok) return r;
  const pass = await mintPass(env.VIEWER_TOKEN_KEY, {
    pub: c.pub,
    inv: c.inv,
    id: r.id,
    chs: c.chs,
    exp: Math.floor(now() / 1000) + PASS_TTL_S,
  });
  return { ok: true, pass, claims: c, reused: r.reused };
}

export function bearer(request: Request): string {
  const h = request.headers.get("Authorization") ?? "";
  return h.startsWith("Bearer ") ? h.slice(7).trim() : "";
}

async function redeem(request: Request, env: Env): Promise<Response> {
  const b = await body(request);
  let token = str(b.invite);
  if (!token) {
    const pub = str(b.publisher);
    const code = str(b.code).toUpperCase();
    if (!UUIDISH.test(pub) || !CODE_RE.test(code)) return refuse(request, 400, "bad_request");
    const r = await viewer(env, pub).code(code, await ipHash(env, clientIP(request), now()));
    if (!r.ok) return refuse(request, r.status, r.error);
    token = r.token;
  }
  const r = await redeemToken(request, env, token);
  if (!r.ok) return refuse(request, r.status, r.error);
  return answer(
    200,
    {
      pass: r.pass,
      publisher: r.claims.pub,
      channels: r.claims.chs,
      expires_at: new Date(now() + PASS_TTL_S * 1000).toISOString(),
      reused: r.reused,
    },
    { "Set-Cookie": setCookie(passCookieName(r.claims.pub), r.pass, PASS_TTL_S) }
  );
}

const PAGE_CSS =
  "body{font-family:system-ui,sans-serif;background:#111;color:#eee;display:flex;min-height:100vh;align-items:center;justify-content:center;margin:0;padding:16px}main{max-width:28rem;text-align:center}button{font-size:1.1rem;padding:.7rem 1.4rem;border-radius:.5rem;border:0;background:#2f7de1;color:#fff}p.free{opacity:.7}";

function page(lang: Lang, inner: string, status = 200): Response {
  const dir = lang === "ar" ? "rtl" : "ltr";
  return new Response(
    `<!doctype html><html lang="${lang}" dir="${dir}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="robots" content="noindex"><title>Maktaba</title><style>${PAGE_CSS}</style></head><body><main>${inner}</main></body></html>`,
    {
      status,
      headers: {
        "Content-Type": "text/html; charset=utf-8",
        "Content-Security-Policy":
          "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'",
      },
    }
  );
}

function esc(s: string): string {
  return s.replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

/** invitePage only shows a button: opening the link (or a chat app's preview of it) uses nothing. */
function invitePage(request: Request, token: string): Response {
  const lang = langOf(request);
  return page(
    lang,
    `<h1>${esc(t("broadcast.invite.open", lang))}</h1><form method="post" action="/i/${esc(encodeURIComponent(token))}"><button type="submit">${esc(t("broadcast.invite.accept", lang))}</button></form><p class="free">${esc(t("broadcast.free", lang))}</p>`
  );
}

async function invitePost(request: Request, env: Env, token: string): Promise<Response> {
  const lang = langOf(request);
  const r = await redeemToken(request, env, token);
  if (!r.ok) {
    const key = ERROR_KEYS[r.error] ?? "broadcast.invite.invalid";
    return page(lang, `<h1>${esc(t(key, lang))}</h1>`, r.status);
  }
  // then to the channel (an invite for one) or the publisher's grid (50.27)
  const pub = encodeURIComponent(r.claims.pub);
  const to =
    r.claims.chs.length === 1
      ? `/${lang}/${pub}/${encodeURIComponent(r.claims.chs[0]!)}`
      : `/${lang}/${pub}`;
  return new Response(null, {
    status: 303,
    headers: {
      Location: to,
      "Set-Cookie": setCookie(passCookieName(r.claims.pub), r.pass, PASS_TTL_S),
    },
  });
}

// ---- open sessions ------------------------------------------------------------------------

async function openSession(
  via: "turnstile" | "attest",
  env: Env
): Promise<{ token: string; exp: number }> {
  const exp = Math.floor(now() / 1000) + OPEN_TTL_S;
  return { token: await mintOpen(env.VIEWER_TOKEN_KEY, { id: randomId(), via, exp }), exp };
}

async function openTurnstile(request: Request, env: Env): Promise<Response> {
  const b = await body(request);
  const v = await verifyTurnstile(env, str(b.token), clientIP(request));
  if (!v.ok) return refuse(request, v.status, v.error);
  const s = await openSession("turnstile", env);
  return answer(
    200,
    { session: s.token, expires_at: new Date(s.exp * 1000).toISOString() },
    { "Set-Cookie": setCookie(OPEN_COOKIE, s.token, OPEN_TTL_S) }
  );
}

async function openAttest(request: Request, env: Env): Promise<Response> {
  const b = await body(request);
  const v = await verifyAttestation(env, str(b.platform), str(b.attestation), str(b.nonce));
  if (!v.ok) return refuse(request, v.status, v.error);
  const s = await openSession("attest", env);
  return answer(200, { session: s.token, expires_at: new Date(s.exp * 1000).toISOString() });
}

// ---- play and beat ------------------------------------------------------------------------

export async function credOf(
  request: Request,
  env: Env,
  pub: string
): Promise<{ cred: Cred; pass?: string; passExp?: number; openExp?: number }> {
  const c = cookies(request);
  const auth = bearer(request);
  const t0 = now();
  const passText = c[passCookieName(pub)] ?? (auth.startsWith(PASS_PREFIX) ? auth : "");
  if (passText) {
    const p = await readPass(passText, env.VIEWER_TOKEN_KEY, t0);
    if (p && p.pub === pub)
      return {
        cred: { kind: "pass", id: p.id, inv: p.inv, chs: p.chs },
        pass: passText,
        passExp: p.exp,
      };
  }
  const openText = c[OPEN_COOKIE] ?? (auth.startsWith(OPEN_PREFIX) ? auth : "");
  if (openText) {
    const o = await readOpen(openText, env.VIEWER_TOKEN_KEY, t0);
    if (o) return { cred: { kind: "open", id: o.id }, openExp: o.exp };
  }
  return { cred: { kind: "none" } };
}

export function liveOrigin(env: Env): string {
  return `https://${env.LIVE_HOST || DEFAULT_LIVE_HOST}`;
}

async function masterURL(
  env: Env,
  pub: string,
  ch: string,
  session: string,
  mode: Mode,
  expS: number
): Promise<string> {
  const token = await mintViewerToken(env.VIEWER_TOKEN_KEY, { pub, ch, session, mode, exp: expS });
  return `${liveOrigin(env)}/${token}/${ch}/master.m3u8`;
}

/** channelState is the channel's own state from its ChannelLive (50.21 §3). */
export async function channelState(env: Env, ch: string): Promise<ChannelStatus> {
  return env.CHANNEL.get(env.CHANNEL.idFromName(ch)).status();
}

async function planOf(env: Env, pub: string): Promise<PlanView | null> {
  const p = await env.PUBLISHER.get(env.PUBLISHER.idFromName(pub)).plan();
  if (!p) return null;
  return p.max_viewers !== undefined
    ? { mode: p.mode, max_viewers: p.max_viewers }
    : { mode: p.mode };
}

async function play(request: Request, env: Env): Promise<Response> {
  const b = await body(request);
  const pub = str(b.publisher);
  const ch = str(b.channel);
  if (!UUIDISH.test(pub) || !CH_RE.test(ch)) return refuse(request, 400, "bad_request");
  if (globallyOff(env) || isRevoked(await revocationsFor(env), { pub, ch }))
    return refuse(request, 403, "broadcast_off");
  const { cred, pass, passExp } = await credOf(request, env, pub);
  const t0 = now();
  const plan = await planOf(env, pub);
  const ip = cred.kind === "open" ? await ipHash(env, clientIP(request), t0) : "";
  const r = await viewer(env, pub).play(ch, cred, ip, plan);
  if (!r.ok) return refuse(request, r.status, r.error);
  const expS = Math.floor(t0 / 1000) + TOKEN_TTL_S;
  await viewer(env, pub).minted(r.session, expS * 1000);
  const neighbours = await Promise.all(
    r.channels
      .filter((n) => n !== ch)
      .slice(0, 16)
      .map(async (n) => ({
        channel: n,
        master_url: await masterURL(env, pub, n, r.session, r.mode, expS),
      }))
  );
  const headers: Record<string, string> = {};
  // a guest pass used with less than half its life left is renewed
  if (
    pass &&
    passExp !== undefined &&
    passExp - t0 / 1000 < PASS_TTL_S / 2 &&
    cred.kind === "pass"
  ) {
    const renewed = await mintPass(env.VIEWER_TOKEN_KEY, {
      pub,
      inv: cred.inv,
      id: cred.id,
      chs: cred.chs,
      exp: Math.floor(t0 / 1000) + PASS_TTL_S,
    });
    headers["Set-Cookie"] = setCookie(passCookieName(pub), renewed, PASS_TTL_S);
    headers["X-Maktaba-Pass"] = renewed;
  }
  return answer(
    200,
    {
      session_id: r.session,
      beat_key: r.key,
      master_url: await masterURL(env, pub, ch, r.session, r.mode, expS),
      expires_at: new Date(expS * 1000).toISOString(),
      refresh_after: REFRESH_AFTER_S,
      state: r.mode,
      // the channel itself: live, waiting (tuning), offline_slate, off_air (50.27 §3)
      channel_state: await channelState(env, ch),
      neighbours,
    },
    headers
  );
}

async function beat(request: Request, env: Env, session: string): Promise<Response> {
  if (!SESSION_RE.test(session)) return refuse(request, 404, "session_ended");
  const b = await body(request);
  const pub = str(b.publisher);
  if (!UUIDISH.test(pub)) return refuse(request, 400, "bad_request");
  const r = await viewer(env, pub).beat(session, str(b.beat_key), REFRESH_AFTER_S * 1000);
  if (!r.ok) return refuse(request, r.status, r.error);
  // a viewer-minute for the meter (PublisherControl keeps its own once-a-minute rule, 50.20 §3)
  await env.PUBLISHER.get(env.PUBLISHER.idFromName(pub)).beat(pub, session, r.ch, r.mode);
  const out: Record<string, unknown> = {
    ok: true,
    state: r.mode,
    concurrent: r.concurrent,
    refresh_after: REFRESH_AFTER_S,
    channel_state: await channelState(env, r.ch),
  };
  if (r.refresh) {
    const expS = Math.floor(now() / 1000) + TOKEN_TTL_S;
    out.master_url = await masterURL(env, pub, r.ch, session, r.mode, expS);
    out.expires_at = new Date(expS * 1000).toISOString();
    await viewer(env, pub).minted(session, expS * 1000);
  }
  return answer(200, out);
}
