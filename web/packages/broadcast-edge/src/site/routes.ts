// watch.maktaba.tv's pages and the 50.27 viewer API, beside 50.26's (play.ts):
//
//   GET  /s/watch.js, /s/watch.css       the pages' script and stylesheet
//   GET  /, /ar, /en                     what a watch link is (no directory)
//   GET  /<lang>/<publisher>             the channel grid the guest's credential allows
//   GET  /<lang>/<publisher>/<channel>   the player, now and next, today, attribution, Report
//   GET  /<lang>/<publisher>/guide       the guide for the publisher's channels
//   GET  /<lang>/legal/<page>            copyright, terms, privacy (summaries)
//   GET  /pair, /<lang>/pair             the phone's side of TV pairing; POST: send to the TV
//   GET  /api/v1/lineup?publisher=       the grid as JSON (the apps)
//   GET  /api/v1/guide?publisher=&channel=   a channel's guide as JSON (the apps)
//   POST /api/v1/pair                    the TV: a pairing nonce, its poll key and code (pairing.ts)
//   GET  /api/v1/pair/<nonce>?wait=25    the TV's long-poll (X-Maktaba-Pair-Key)
//   POST /api/v1/pair/<nonce>            the phone: send a credential to the TV
//
// Without a language prefix a page answers in the Accept-Language's (Arabic by default), and
// /<publisher>… redirects to /<lang>/<publisher>…. WATCH_SITE_OFF="1" turns the pages into
// "temporarily unavailable" while the API and playback stay up (the story's rollback).
import { now } from "../clock";
import { DEFAULT_LIVE_HOST, globallyOff, type Env } from "../env";
import { getBranding, getGuide, nowNext, today, type Programme } from "../guide";
import { CODE_RE, mintOpen, PASS_TTL_S, randomId } from "../invites";
import { clientIP, ipHash } from "../limits";
import {
  NONCE_RE,
  PAIR_KEY_HEADER,
  PAIR_TTL_MS,
  PAIR_WAIT_MS,
  pairCode,
  type Handover,
} from "../pairing";
import {
  answer,
  body as jsonBody,
  channelState,
  credOf,
  redeemToken,
  refuse,
  str,
  viewer,
} from "../play";
import type { Cred } from "../access";
import { isRevoked, revocationsFor } from "../revocations";
import { ERROR_KEYS, langOf, type Lang } from "../strings";
import { CLIENT_CSS, clientJS } from "./client";
import {
  channelPage,
  gridPage,
  guidePage,
  LEGAL,
  legalPage,
  messagePage,
  pairPage,
  type LegalPage,
  type Lineup,
  type LineupChannel,
} from "./pages";

const PUB_RE = /^[A-Za-z0-9-]{1,64}$/;
const CH_RE = /^[a-z0-9]{1,64}$/;
/** First path parts that are never a publisher. */
const RESERVED = new Set(["api", "i", "s", "pair", "ar", "en", "legal", "guide"]);

export function liveOriginOf(env: Env): string {
  return `https://${env.LIVE_HOST || DEFAULT_LIVE_HOST}`;
}

function siteOff(env: Env): boolean {
  return env.WATCH_SITE_OFF === "1" || env.WATCH_SITE_OFF === "true";
}

/** lineup is the publisher's channels a credential allows, with their state and now and next. */
export async function lineup(env: Env, pub: string, cred: Cred): Promise<Lineup> {
  const access = viewer(env, pub);
  const ids = cred.kind === "none" ? await access.openChannels() : await access.allowed(cred);
  const t0 = now();
  const list = await revocationsFor(env);
  const off = globallyOff(env);
  const channels = await Promise.all(
    ids.slice(0, 64).map(async (id): Promise<LineupChannel> => {
      const [g, state] = await Promise.all([getGuide(env, pub, id), channelState(env, id)]);
      const nn = g ? nowNext(g.programmes, t0) : { now: null, next: null };
      return {
        id,
        name: g?.channel.name ?? id,
        number: g?.channel.number ?? 0,
        state: off || isRevoked(list, { pub, ch: id }) ? "revoked" : state,
        now: nn.now,
        next: nn.next,
      };
    })
  );
  channels.sort((a, b) => a.number - b.number || a.name.localeCompare(b.name));
  const brand = await getBranding(env, pub);
  return {
    publisher: { id: pub, name: brand?.name ?? "", ...(brand?.logo ? { logo: brand.logo } : {}) },
    channels,
  };
}

function langPrefix(part: string | undefined): Lang | null {
  return part === "ar" || part === "en" ? part : null;
}

/**
 * handleSite answers the pages and the 50.27 API, or null for a path it doesn't own (the 50.26
 * routes in play.ts).
 */
export async function handleSite(request: Request, env: Env, url: URL): Promise<Response | null> {
  const path = url.pathname;
  const live = liveOriginOf(env);

  // the API
  if (path === "/api/v1/lineup" && request.method === "GET") return apiLineup(request, env, url);
  if (path === "/api/v1/guide" && request.method === "GET") return apiGuide(request, env, url);
  if (path === "/api/v1/pair" && request.method === "POST") return pairOpen(env, url);
  const pm = /^\/api\/v1\/pair\/([^/]+)$/.exec(path);
  if (pm) {
    if (request.method === "GET") return pairPoll(request, env, pm[1]!, url);
    if (request.method === "POST") {
      const b = await jsonBody(request);
      const r = await pairDeliver(request, env, pm[1]!, {
        invite: str(b.invite),
        publisher: str(b.publisher),
        code: str(b.code),
        open: b.open === true,
      });
      return r.ok
        ? answer(200, { ok: true, publisher: r.publisher })
        : refuse(request, r.status, r.error);
    }
  }
  if (path.startsWith("/api/") || path.startsWith("/i/")) return null;

  // static
  if (path === "/s/watch.js")
    return new Response(clientJS(), {
      headers: {
        "Content-Type": "text/javascript; charset=utf-8",
        "Cache-Control": "public, max-age=300",
      },
    });
  if (path === "/s/watch.css")
    return new Response(CLIENT_CSS, {
      headers: {
        "Content-Type": "text/css; charset=utf-8",
        "Cache-Control": "public, max-age=300",
      },
    });

  const parts = path
    .split("/")
    .filter(Boolean)
    .map((p) => decodeURIComponent(p));
  const prefixed = langPrefix(parts[0]);
  const lang = prefixed ?? langOf(request);
  const rest = prefixed ? parts.slice(1) : parts;

  if (siteOff(env)) return messagePage(lang, path, "site.unavailable", live, 503);

  if (request.method !== "GET" && !(request.method === "POST" && rest[0] === "pair"))
    return new Response(null, { status: 405, headers: { Allow: "GET" } });

  if (rest.length === 0) return messagePage(lang, "/", "site.home", live);
  if (rest[0] === "pair") {
    const nonce = url.searchParams.get("tv") ?? "";
    const pub = url.searchParams.get("p") ?? "";
    const okNonce = NONCE_RE.test(nonce) ? nonce : "";
    const okPub = PUB_RE.test(pub) ? pub : "";
    if (request.method === "POST") return pairForm(request, env, lang, okNonce, okPub);
    if (!okNonce) return pairPage(lang, "", okPub, live);
    const code = await pairing(env, okNonce).live();
    if (!code) return pairPage(lang, okNonce, okPub, live, { key: "site.pair.gone", status: 410 });
    return pairPage(lang, okNonce, okPub, live, undefined, code);
  }
  if (rest[0] === "legal" && rest.length === 2 && (LEGAL as readonly string[]).includes(rest[1]!))
    return legalPage(lang, rest[1] as LegalPage, live);

  const pub = rest[0]!;
  if (RESERVED.has(pub) || !PUB_RE.test(pub) || rest.length > 2)
    return messagePage(lang, path, "broadcast.invite.invalid", live, 404);
  // /<publisher>… without a language: to the guest's language
  if (!prefixed) {
    const to = `/${lang}/${rest.map(encodeURIComponent).join("/")}`;
    return new Response(null, { status: 302, headers: { Location: to, Vary: "Accept-Language" } });
  }

  const { cred } = await credOf(request, env, pub);
  const l = await lineup(env, pub, cred);
  if (rest.length === 1) return gridPage(lang, l, live);
  if (rest[1] === "guide") {
    const days: Record<string, Programme[]> = {};
    const t0 = now();
    await Promise.all(
      l.channels.map(async (c) => {
        const g = await getGuide(env, pub, c.id);
        days[c.id] = g ? today(g.programmes, t0) : [];
      })
    );
    return guidePage(lang, l, days, live);
  }
  const ch = rest[1]!;
  const c = CH_RE.test(ch) ? l.channels.find((x) => x.id === ch) : undefined;
  if (!c) {
    // not a channel this guest may see: say how to get in, never which channels exist
    const key = cred.kind === "pass" ? "broadcast.inviteRequired" : "site.noChannels";
    return messagePage(lang, path, key, live, 404);
  }
  const g = await getGuide(env, pub, ch);
  const settings = await viewer(env, pub).settings();
  return channelPage(
    lang,
    {
      lineup: l,
      channel: c,
      today: g ? today(g.programmes, now()) : [],
      ...(env.TURNSTILE_SITE_KEY ? { turnstileSiteKey: env.TURNSTILE_SITE_KEY } : {}),
      indexable: settings?.channels[ch]?.indexable === true,
    },
    live
  );
}

// ---- the apps' JSON -----------------------------------------------------------------------

async function apiLineup(request: Request, env: Env, url: URL): Promise<Response> {
  const pub = url.searchParams.get("publisher") ?? "";
  if (!PUB_RE.test(pub)) return refuse(request, 400, "bad_request");
  const { cred } = await credOf(request, env, pub);
  return answer(200, await lineup(env, pub, cred));
}

async function apiGuide(request: Request, env: Env, url: URL): Promise<Response> {
  const pub = url.searchParams.get("publisher") ?? "";
  const ch = url.searchParams.get("channel") ?? "";
  if (!PUB_RE.test(pub) || !CH_RE.test(ch)) return refuse(request, 400, "bad_request");
  const { cred } = await credOf(request, env, pub);
  const v = viewer(env, pub);
  const ids = cred.kind === "none" ? await v.openChannels() : await v.allowed(cred);
  if (!ids.includes(ch))
    return refuse(request, 403, cred.kind === "none" ? "invite_required" : "not_invited");
  const g = await getGuide(env, pub, ch);
  return answer(200, {
    channel: { id: ch, name: g?.channel.name ?? ch, number: g?.channel.number ?? 0 },
    programmes: g?.programmes ?? [],
  });
}

// ---- pairing ------------------------------------------------------------------------------

function pairing(env: Env, nonce: string) {
  return env.PAIRING.get(env.PAIRING.idFromName(nonce));
}

async function pairOpen(env: Env, url: URL): Promise<Response> {
  const nonce = randomId();
  const key = randomId();
  const code = pairCode();
  if (!(await pairing(env, nonce).open(key, code)))
    return answer(503, { error: "pair_unavailable" });
  return answer(201, {
    nonce,
    // the TV's alone: sent with each poll, never shown or put in the QR code
    poll_key: key,
    // shown on the TV beside the QR code; the phone's page shows it too
    code,
    url: `${url.origin}/pair?tv=${nonce}`,
    expires_at: new Date(now() + PAIR_TTL_MS).toISOString(),
    poll: `/api/v1/pair/${nonce}`,
  });
}

/** pairPoll is the TV's long-poll; ?wait=<seconds> (0–25, default 25) bounds one wait. */
async function pairPoll(request: Request, env: Env, nonce: string, url: URL): Promise<Response> {
  if (!NONCE_RE.test(nonce)) return refuse(request, 410, "pair_gone");
  const w = Number(url.searchParams.get("wait") ?? NaN);
  const waitMs = Number.isFinite(w) ? Math.max(0, Math.min(PAIR_WAIT_MS, w * 1000)) : PAIR_WAIT_MS;
  const key = request.headers.get(PAIR_KEY_HEADER) ?? "";
  const r = await pairing(env, nonce).take(key, waitMs);
  if (r.status === "ready") return answer(200, r.handover);
  if (r.status === "pending") return new Response(null, { status: 204 });
  return refuse(request, 410, "pair_gone");
}

interface PairInput {
  invite: string;
  publisher: string;
  code: string;
  open: boolean;
}

type PairResult = { ok: true; publisher: string } | { ok: false; status: number; error: string };

/** inviteToken takes an invite link (…/i/<token>) or the bare token. */
export function inviteToken(s: string): string {
  const v = s.trim();
  const m = /\/i\/([^/?#\s]+)/.exec(v);
  return m ? decodeURIComponent(m[1]!) : v;
}

/**
 * pairDeliver is the phone's send: an invite (a new device use, never the phone's own pass), a
 * publisher's short code, or the phone's open session (a fresh one is made for the TV).
 */
async function pairDeliver(
  request: Request,
  env: Env,
  nonce: string,
  inp: PairInput
): Promise<PairResult> {
  if (!NONCE_RE.test(nonce)) return { ok: false, status: 410, error: "pair_gone" };
  const p = pairing(env, nonce);
  if (!(await p.live())) return { ok: false, status: 410, error: "pair_gone" };
  let handover: Handover;
  if (inp.open) {
    if (!PUB_RE.test(inp.publisher)) return { ok: false, status: 400, error: "bad_request" };
    const { cred, openExp } = await credOf(request, env, inp.publisher);
    if (cred.kind !== "open" || openExp === undefined)
      return { ok: false, status: 403, error: "turnstile_required" };
    const chs = await viewer(env, inp.publisher).openChannels();
    if (chs.length === 0) return { ok: false, status: 403, error: "invite_required" };
    // a token of the phone's session, not a new session: the same id (one live session, the
    // same daily minutes and network limit) and the same expiry, so no check is skipped or
    // extended; the phone's own cookie value never leaves it
    const tv = await mintOpen(env.VIEWER_TOKEN_KEY, {
      id: cred.id,
      via: "pair",
      exp: openExp,
    });
    handover = {
      publisher: inp.publisher,
      credential: tv,
      kind: "open",
      channels: chs,
      expires_at: new Date(openExp * 1000).toISOString(),
    };
  } else {
    let token = inviteToken(inp.invite);
    if (!token) {
      const code = inp.code.trim().toUpperCase();
      if (!PUB_RE.test(inp.publisher) || !CODE_RE.test(code))
        return { ok: false, status: 400, error: "bad_request" };
      const c = await viewer(env, inp.publisher).code(
        code,
        await ipHash(env, clientIP(request), now())
      );
      if (!c.ok) return c;
      token = c.token;
    }
    const r = await redeemToken(request, env, token, false);
    if (!r.ok) return r;
    handover = {
      publisher: r.claims.pub,
      credential: r.pass,
      kind: "pass",
      channels: r.claims.chs,
      expires_at: new Date(now() + PASS_TTL_S * 1000).toISOString(),
    };
  }
  if (!(await p.deliver(handover))) return { ok: false, status: 410, error: "pair_gone" };
  return { ok: true, publisher: handover.publisher };
}

/** pairForm is the pairing page's form post (no script needed on the phone). */
async function pairForm(
  request: Request,
  env: Env,
  lang: Lang,
  nonce: string,
  pub: string
): Promise<Response> {
  const live = liveOriginOf(env);
  let form: FormData;
  try {
    form = await request.formData();
  } catch {
    return pairPage(lang, nonce, pub, live, { key: "site.error", status: 400 });
  }
  const f = (k: string) => {
    const v = form.get(k);
    return typeof v === "string" ? v.slice(0, 4096) : "";
  };
  const r = await pairDeliver(request, env, nonce, {
    invite: f("invite"),
    publisher: pub,
    code: f("code"),
    open: f("open") === "1",
  });
  if (r.ok) return pairPage(lang, nonce, pub, live, { key: "site.pair.sent", status: 200 });
  const key =
    r.error === "pair_gone"
      ? "site.pair.gone"
      : (ERROR_KEYS[r.error] ?? "broadcast.invite.invalid");
  return pairPage(lang, nonce, pub, live, { key, status: r.status });
}
