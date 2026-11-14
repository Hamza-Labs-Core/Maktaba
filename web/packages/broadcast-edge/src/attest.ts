// The native apps' open session (story 50.26 §2): a TV or phone app can't run Turnstile, so it
// sends an app attestation (App Attest on Apple, Play Integrity on Android) and gets the same
// 12 h session. The app first asks for a challenge (POST /api/v1/open/challenge: a nonce good
// once, for 5 minutes) and binds it into its attestation; the verifier bound as ATTEST_API checks
// the attestation against that nonce (50.27 builds the apps' side and the verifier); without one,
// the exchange answers 503. A nonce the edge didn't hand out, or one already used, is 403.
import type { Env } from "./env";

/** The challenges live in one ViewerAccess instance of their own. */
export function nonces(env: Env) {
  return env.VIEWER.get(env.VIEWER.idFromName("attest-challenges"));
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
  if (!nonce || nonce.length > 64 || !(await nonces(env).useNonce(nonce)))
    return { ok: false, status: 403, error: "attest_challenge" };
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
  return body.valid === true ? { ok: true } : { ok: false, status: 403, error: "attest_failed" };
}
