// Viewer credentials (story 50.26 §1–§2):
//
//   invite        mkbn1.<b64url(payload)>.<b64url(Ed25519 sig)>   cloud → publisher → viewer
//   guest pass    mkbg1.<b64url(payload)>.<b64url(HMAC-SHA256)>   edge → one device (30 days)
//   open session  mkbo1.<b64url(payload)>.<b64url(HMAC-SHA256)>   edge → one browser or app (12 h)
//
// The invite is checked with CLOUD_PUBKEYS (cloud/internal/broadcast/invites.go signs it). The
// edge's own two are HMACs with VIEWER_TOKEN_KEY under their own domains, so neither passes as a
// viewer token or as the other. A guest pass holds a random id and the invite's id and channels;
// an open session a random id: never an identity.
import { b64url, fromB64url, sameText, utf8 } from "./crypto";
import { verifyCloud } from "./tokens";

export const INVITE_PREFIX = "mkbn1.";
export const INVITE_DOMAIN = "maktaba-broadcast-invite-v1";
export const PASS_PREFIX = "mkbg1.";
export const PASS_DOMAIN = "maktaba-broadcast-guest-v1";
export const OPEN_PREFIX = "mkbo1.";
export const OPEN_DOMAIN = "maktaba-broadcast-open-v1";

/** A guest pass lives 30 days and is renewed when used with less than half left. */
export const PASS_TTL_S = 30 * 24 * 3600;
/** An open session lives 12 h (one Turnstile check or app attestation). */
export const OPEN_TTL_S = 12 * 3600;

export interface InviteClaims {
  typ: "invite";
  /** the publisher: the home's server id */
  pub: string;
  /** the edge channel ids it opens; empty: all of the publisher's */
  chs: string[];
  inv: string;
  max: number;
  iat: number;
  exp: number;
  kid: string;
}

export interface PassClaims {
  pub: string;
  inv: string;
  id: string;
  chs: string[];
  exp: number;
}

export interface OpenClaims {
  id: string;
  via: "turnstile" | "attest";
  exp: number;
}

export type InviteCheck =
  | { ok: true; claims: InviteClaims }
  | { ok: false; status: 401 | 410; error: string };

/** verifyInvite: a bad signature or shape is 401; an expired invite 410 invite_expired. */
export async function verifyInvite(
  token: string,
  pubkeys: string,
  nowMs: number
): Promise<InviteCheck> {
  const v = await verifyCloud(token, INVITE_PREFIX, INVITE_DOMAIN, pubkeys);
  if (!v.ok) return { ok: false, status: 401, error: v.error };
  const p = v.payload;
  if (
    p.typ !== "invite" ||
    typeof p.pub !== "string" ||
    typeof p.inv !== "string" ||
    typeof p.max !== "number" ||
    typeof p.exp !== "number" ||
    !Array.isArray(p.chs) ||
    !p.chs.every((c) => typeof c === "string")
  )
    return { ok: false, status: 401, error: "bad_token" };
  if (p.exp * 1000 <= nowMs) return { ok: false, status: 410, error: "invite_expired" };
  return { ok: true, claims: p as unknown as InviteClaims };
}

const keys = new Map<string, Promise<CryptoKey>>();

function hmacKey(secret: string): Promise<CryptoKey> {
  let k = keys.get(secret);
  if (!k) {
    k = crypto.subtle.importKey(
      "raw",
      fromB64url(secret),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"]
    );
    keys.set(secret, k);
  }
  return k;
}

async function mac(secret: string, domain: string, body: string): Promise<string> {
  const sig = await crypto.subtle.sign("HMAC", await hmacKey(secret), utf8(`${domain}.${body}`));
  return b64url(new Uint8Array(sig));
}

async function mint(
  secret: string,
  prefix: string,
  domain: string,
  claims: object
): Promise<string> {
  const body = b64url(utf8(JSON.stringify(claims)));
  return `${prefix}${body}.${await mac(secret, domain, body)}`;
}

async function open<T>(
  token: string,
  secret: string,
  prefix: string,
  domain: string,
  nowMs: number
): Promise<T | null> {
  if (!secret || !token.startsWith(prefix)) return null;
  const [body, sig, extra] = token.slice(prefix.length).split(".");
  if (!body || !sig || extra !== undefined) return null;
  if (!sameText(await mac(secret, domain, body), sig)) return null;
  try {
    const p = JSON.parse(new TextDecoder().decode(fromB64url(body))) as { exp?: unknown };
    if (typeof p.exp !== "number" || p.exp * 1000 <= nowMs) return null;
    return p as T;
  } catch {
    return null;
  }
}

export function mintPass(secret: string, c: PassClaims): Promise<string> {
  return mint(secret, PASS_PREFIX, PASS_DOMAIN, c);
}

export async function readPass(
  token: string,
  secret: string,
  nowMs: number
): Promise<PassClaims | null> {
  const p = await open<PassClaims>(token, secret, PASS_PREFIX, PASS_DOMAIN, nowMs);
  if (
    !p ||
    typeof p.pub !== "string" ||
    typeof p.inv !== "string" ||
    typeof p.id !== "string" ||
    !Array.isArray(p.chs)
  )
    return null;
  return p;
}

export function mintOpen(secret: string, c: OpenClaims): Promise<string> {
  return mint(secret, OPEN_PREFIX, OPEN_DOMAIN, c);
}

export async function readOpen(
  token: string,
  secret: string,
  nowMs: number
): Promise<OpenClaims | null> {
  const p = await open<OpenClaims>(token, secret, OPEN_PREFIX, OPEN_DOMAIN, nowMs);
  if (!p || typeof p.id !== "string") return null;
  return p;
}

/** randomId is a 128-bit random id, base64url. */
export function randomId(): string {
  return b64url(crypto.getRandomValues(new Uint8Array(16)));
}

/** A short code is MK- and 4 of A–Z, 0–9 (cloud/internal/broadcast.ValidShortCode). */
export const CODE_RE = /^MK-[A-Z0-9]{4}$/;
