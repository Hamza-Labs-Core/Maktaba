// The native apps' open session (story 50.26 §2): a TV or phone app can't run Turnstile, so it
// sends an app attestation (App Attest on Apple, Play Integrity on Android) and gets the same
// 12 h session. The app first asks for a challenge (POST /api/v1/open/challenge: a nonce good
// once, for 5 minutes) and binds it into its attestation; the verifier bound as ATTEST_API checks
// the attestation against that nonce (50.27 builds the apps' side and the verifier); without one,
// the exchange answers 503. A nonce the edge didn't hand out, or one already used, is 403.
import { b64url, sameText, utf8 } from "./crypto";
import type { Env } from "./env";
import { now } from "./clock";

/** An attestation challenge is good for 5 minutes, once. */
export const CHALLENGE_TTL_MS = 5 * 60_000;
const CHALLENGE_DOMAIN = "maktaba-broadcast-attest-challenge-v1";

/** The spent challenges live in ViewerAccess instances of their own, 16 by the nonce's start. */
function spent(env: Env, nonce: string) {
  return env.VIEWER.get(env.VIEWER.idFromName(`attest-spent-${nonce.charCodeAt(0) % 16}`));
}

async function challengeMac(env: Env, body: string): Promise<string> {
  const key = await crypto.subtle.importKey(
    "raw",
    utf8(`${CHALLENGE_DOMAIN}\0${env.VIEWER_TOKEN_KEY}`),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  );
  return b64url(new Uint8Array(await crypto.subtle.sign("HMAC", key, utf8(body)))).slice(0, 32);
}

/** newChallenge is a stateless challenge: <random>.<expiry ms>.<mac>, stored nowhere. */
export async function newChallenge(env: Env): Promise<{ nonce: string; expires_at: number }> {
  const exp = now() + CHALLENGE_TTL_MS;
  const body = `${b64url(crypto.getRandomValues(new Uint8Array(16)))}.${exp}`;
  return { nonce: `${body}.${await challengeMac(env, body)}`, expires_at: exp };
}

/** readChallenge checks a challenge's mac and expiry, spending nothing: its random part. */
async function readChallenge(
  env: Env,
  nonce: string
): Promise<{ rand: string; exp: number } | null> {
  const parts = nonce.split(".");
  if (parts.length !== 3) return null;
  const body = `${parts[0]}.${parts[1]}`;
  if (!sameText(await challengeMac(env, body), parts[2]!)) return null;
  const exp = Number(parts[1]);
  if (!Number.isFinite(exp) || exp <= now()) return null;
  return { rand: parts[0]!, exp };
}

export const PLATFORMS = ["apple", "android"] as const;
export type Platform = (typeof PLATFORMS)[number];

export type AttestResult = { ok: true } | { ok: false; status: 400 | 403 | 503; error: string };

export async function verifyAttestation(
  env: Env,
  platform: string,
  attestation: string,
  nonce: string
): Promise<AttestResult> {
  if (
    !(PLATFORMS as readonly string[]).includes(platform) ||
    !attestation ||
    attestation.length > 16384
  )
    return { ok: false, status: 400, error: "bad_attestation" };
  if (!env.ATTEST_API) return { ok: false, status: 503, error: "attest_unavailable" };
  // The challenge is checked first (cheap, stateless) and spent only once the attestation holds,
  // so a caller without one cannot fill the spent set.
  const challenge = nonce && nonce.length <= 128 ? await readChallenge(env, nonce) : null;
  if (!challenge) return { ok: false, status: 403, error: "attest_challenge" };
  let res: Response;
  try {
    res = await env.ATTEST_API.fetch("https://attest.internal/verify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ platform, attestation, nonce }),
    });
  } catch {
    return { ok: false, status: 503, error: "attest_unavailable" };
  }
  if (!res.ok) return { ok: false, status: 503, error: "attest_unavailable" };
  const body = (await res.json().catch(() => ({}))) as { valid?: boolean };
  if (body.valid !== true) return { ok: false, status: 403, error: "attest_failed" };
  const spend = await spent(env, challenge.rand).spend(challenge.rand, challenge.exp);
  if (spend === "full") return { ok: false, status: 503, error: "attest_unavailable" };
  return spend ? { ok: true } : { ok: false, status: 403, error: "attest_challenge" };
}
