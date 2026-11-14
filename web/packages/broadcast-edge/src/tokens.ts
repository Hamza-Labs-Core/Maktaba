// The edge's three credentials (story 50.21 §2, §4). No shared secret with any home:
//
//   ingest token   mkbi1.<b64url(payload)>.<b64url(Ed25519 sig)>   cloud → home → edge
//   control post   mkbc1.<b64url(payload)>.<b64url(Ed25519 sig)>   cloud → edge (plans, revocations)
//   viewer token   mkbv1.<b64url(payload)>.<b64url(HMAC-SHA256)>   watch.maktaba.tv → player → edge
//
// Each signature covers "<domain>." + <b64url(payload)>, the domain naming the kind, so a
// signature made for one kind never passes as another. The cloud signs ingest tokens and control
// posts (cloud/internal/broadcast); the edge checks them with CLOUD_PUBKEYS, picking the key by
// the payload's kid, so a key rotates by adding the new key, issuing with it, then dropping the
// old one. Both sides check the same vectors (cloud/internal/broadcast/testdata/vectors.json).
import { cleanGeo, type Geo } from "./geo";
import { b64url, fromB64url, sameText, utf8 } from "./crypto";

export const INGEST_PREFIX = "mkbi1.";
export const CONTROL_PREFIX = "mkbc1.";
export const VIEWER_PREFIX = "mkbv1.";
export const INGEST_DOMAIN = "maktaba-broadcast-ingest-v1";
export const CONTROL_DOMAIN = "maktaba-broadcast-control-v1";
export const VIEWER_DOMAIN = "maktaba-broadcast-viewer-v1";

/** An ingest token lives 24 h; the home refreshes it every hour (50.22). */
export const INGEST_MAX_TTL_S = 24 * 3600;
/** A speed-test token lives at most 10 minutes (50.24: the cloud issues it without a cloud id). */
export const SPEEDTEST_MAX_TTL_S = 600;
/** A viewer token lives at most 10 minutes (50.26 mints it). */
export const VIEWER_MAX_TTL_S = 600;
/** Clock slack allowed on a token's expiry bounds. */
const SLACK_S = 60;

/** The ingest token's claims: one publisher, one channel, its rungs, and the plan's top kbps. */
export interface IngestClaims {
  /** "ingest", or "ingest-speedtest": a speed-test token (no cloud id, before the first publish),
   * taken on /v1/ingest/speedtest only */
  typ: "ingest" | "ingest-speedtest";
  /** publisher: the cloud account that publishes */
  pub: string;
  /** channel: the edge's opaque channel id (the cloud derives it from the home and channel) */
  ch: string;
  /** the rung names this channel may push, top first (the plan's rung count) */
  rungs: string[];
  /** the home (server id) the token was issued to */
  home: string;
  /** the plan's top bitrate in kbps: the size guard (§2) */
  kbps: number;
  /** the channel's country rule, Maktaba's deny list merged in (50.23 §5); absent: none */
  geo?: Geo;
  iat: number;
  exp: number;
  kid: string;
}

export type Mode = "full" | "lowest" | "wait" | "off";

/** The viewer token's claims (minted by watch.maktaba.tv in 50.26). */
export interface ViewerClaims {
  pub: string;
  ch: string;
  session: string;
  mode: Mode;
  exp: number;
  country?: string;
}

export type Checked<T> = { ok: true; claims: T } | { ok: false; status: 401 | 403; error: string };

const keyCache = new Map<string, Map<string, Promise<CryptoKey>>>();

/** cloudKey imports CLOUD_PUBKEYS' key for kid, once per isolate. */
function cloudKey(pubkeys: string, kid: string): Promise<CryptoKey> | undefined {
  let keys = keyCache.get(pubkeys);
  if (!keys) {
    keys = new Map();
    let parsed: Record<string, string> = {};
    try {
      parsed = JSON.parse(pubkeys || "{}") as Record<string, string>;
    } catch {
      parsed = {};
    }
    for (const [k, v] of Object.entries(parsed)) {
      keys.set(
        k,
        crypto.subtle.importKey("raw", fromB64url(v), { name: "Ed25519" }, false, ["verify"])
      );
    }
    keyCache.set(pubkeys, keys);
  }
  return keys.get(kid);
}

function split(
  token: string,
  prefix: string
): { body: string; sig: Uint8Array; payload: unknown } | null {
  if (!token.startsWith(prefix)) return null;
  const rest = token.slice(prefix.length);
  const dot = rest.indexOf(".");
  if (dot <= 0 || rest.indexOf(".", dot + 1) !== -1) return null;
  const body = rest.slice(0, dot);
  try {
    const payload = JSON.parse(new TextDecoder().decode(fromB64url(body))) as unknown;
    return { body, sig: fromB64url(rest.slice(dot + 1)), payload };
  } catch {
    return null;
  }
}

async function verifyCloud(
  token: string,
  prefix: string,
  domain: string,
  pubkeys: string
): Promise<{ ok: true; payload: Record<string, unknown> } | { ok: false; error: string }> {
  const parts = split(token, prefix);
  if (!parts || typeof parts.payload !== "object" || parts.payload === null)
    return { ok: false, error: "bad_token" };
  const payload = parts.payload as Record<string, unknown>;
  const kid = typeof payload.kid === "string" ? payload.kid : "";
  const key = cloudKey(pubkeys, kid);
  if (!key) return { ok: false, error: "unknown_key" };
  let good = false;
  try {
    good = await crypto.subtle.verify(
      "Ed25519",
      await key,
      parts.sig,
      utf8(`${domain}.${parts.body}`)
    );
  } catch {
    good = false;
  }
  return good ? { ok: true, payload } : { ok: false, error: "bad_signature" };
}

function isStringList(v: unknown): v is string[] {
  return Array.isArray(v) && v.every((x) => typeof x === "string");
}

/**
 * verifyIngest checks an ingest token for one channel and, when given, one rung:
 * a bad or unsigned token, an unknown key or an expired one is 401; a good token for another
 * channel or a rung it does not list is 403.
 */
export async function verifyIngest(
  token: string,
  pubkeys: string,
  nowMs: number,
  want: { ch?: string; rung?: string; speedtest?: boolean }
): Promise<Checked<IngestClaims>> {
  const v = await verifyCloud(token, INGEST_PREFIX, INGEST_DOMAIN, pubkeys);
  if (!v.ok) return { ok: false, status: 401, error: v.error };
  const p = v.payload;
  // a speed-test token is good for the speed test only, never for a segment or the stream
  if (p.typ === "ingest-speedtest" && !want.speedtest)
    return { ok: false, status: 403, error: "speedtest_only" };
  if (
    (p.typ !== "ingest" && p.typ !== "ingest-speedtest") ||
    typeof p.pub !== "string" ||
    typeof p.ch !== "string" ||
    typeof p.home !== "string" ||
    !isStringList(p.rungs) ||
    typeof p.kbps !== "number" ||
    typeof p.exp !== "number" ||
    typeof p.iat !== "number"
  )
    return { ok: false, status: 401, error: "bad_token" };
  const nowS = nowMs / 1000;
  if (p.exp <= nowS) return { ok: false, status: 401, error: "expired" };
  const maxTTL = p.typ === "ingest-speedtest" ? SPEEDTEST_MAX_TTL_S : INGEST_MAX_TTL_S;
  if (p.exp - p.iat > maxTTL + SLACK_S || p.iat > nowS + SLACK_S)
    return { ok: false, status: 401, error: "bad_lifetime" };
  if (want.ch !== undefined && p.ch !== want.ch)
    return { ok: false, status: 403, error: "wrong_channel" };
  if (want.rung !== undefined && !p.rungs.includes(want.rung))
    return { ok: false, status: 403, error: "wrong_rung" };
  const claims = p as unknown as IngestClaims;
  const geo = cleanGeo(p.geo);
  if (geo) claims.geo = geo;
  else delete claims.geo;
  return { ok: true, claims };
}

/** A control post's payload: what the cloud tells the edge (a plan state, a revocation list). */
export interface ControlPayload {
  typ: string;
  kid: string;
  iat: number;
  exp: number;
  [k: string]: unknown;
}

/** verifyControl checks a cloud-signed control post of one type; it must not be expired. */
export async function verifyControl(
  text: string,
  typ: string,
  pubkeys: string,
  nowMs: number
): Promise<{ ok: true; payload: ControlPayload } | { ok: false; error: string }> {
  const v = await verifyCloud(text.trim(), CONTROL_PREFIX, CONTROL_DOMAIN, pubkeys);
  if (!v.ok) return v;
  const p = v.payload;
  if (p.typ !== typ) return { ok: false, error: "wrong_type" };
  if (typeof p.exp !== "number" || typeof p.iat !== "number")
    return { ok: false, error: "bad_token" };
  if (p.exp * 1000 <= nowMs) return { ok: false, error: "expired" };
  return { ok: true, payload: p as ControlPayload };
}

// ---- viewer tokens (HMAC) ----------------------------------------------------------------

const hmacCache = new Map<string, Promise<CryptoKey>>();

function viewerKey(secret: string): Promise<CryptoKey> {
  let k = hmacCache.get(secret);
  if (!k) {
    k = crypto.subtle.importKey(
      "raw",
      fromB64url(secret),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"]
    );
    hmacCache.set(secret, k);
  }
  return k;
}

/** mintViewerToken makes a viewer token (watch.maktaba.tv in 50.26, and the tests). */
export async function mintViewerToken(secret: string, claims: ViewerClaims): Promise<string> {
  const body = b64url(utf8(JSON.stringify(claims)));
  const sig = await crypto.subtle.sign(
    "HMAC",
    await viewerKey(secret),
    utf8(`${VIEWER_DOMAIN}.${body}`)
  );
  return `${VIEWER_PREFIX}${body}.${b64url(new Uint8Array(sig))}`;
}

const MODES: readonly string[] = ["full", "lowest", "wait", "off"];

/**
 * verifyViewer checks a viewer token for one channel, with no Durable Object and no R2: a bad,
 * expired or over-long token is 401, a good one for another channel is 403.
 */
export async function verifyViewer(
  token: string,
  secret: string,
  nowMs: number,
  ch: string
): Promise<Checked<ViewerClaims>> {
  if (!secret) return { ok: false, status: 401, error: "no_viewer_key" };
  const parts = split(token, VIEWER_PREFIX);
  if (!parts || typeof parts.payload !== "object" || parts.payload === null)
    return { ok: false, status: 401, error: "bad_token" };
  const want = await crypto.subtle.sign(
    "HMAC",
    await viewerKey(secret),
    utf8(`${VIEWER_DOMAIN}.${parts.body}`)
  );
  if (!sameText(b64url(new Uint8Array(want)), b64url(parts.sig)))
    return { ok: false, status: 401, error: "bad_signature" };
  const p = parts.payload as Record<string, unknown>;
  if (
    typeof p.pub !== "string" ||
    typeof p.ch !== "string" ||
    typeof p.session !== "string" ||
    typeof p.mode !== "string" ||
    !MODES.includes(p.mode) ||
    typeof p.exp !== "number"
  )
    return { ok: false, status: 401, error: "bad_token" };
  const nowS = nowMs / 1000;
  if (p.exp <= nowS) return { ok: false, status: 401, error: "expired" };
  if (p.exp > nowS + VIEWER_MAX_TTL_S + SLACK_S)
    return { ok: false, status: 401, error: "bad_lifetime" };
  if (p.ch !== ch) return { ok: false, status: 403, error: "wrong_channel" };
  return { ok: true, claims: p as unknown as ViewerClaims };
}
