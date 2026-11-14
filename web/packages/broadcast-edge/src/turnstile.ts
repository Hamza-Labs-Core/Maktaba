// Open access's bot check (story 50.26 §2, Epic 34 R7.1): the browser's Turnstile token is
// verified server-side with siteverify, once per browser session; a token is good once
// (siteverify answers timeout-or-duplicate to a replay).
import type { Env } from "./env";

export const SITEVERIFY = "https://challenges.cloudflare.com/turnstile/v0/siteverify";

export type TurnstileResult = { ok: true } | { ok: false; status: 403 | 503; error: string };

export async function verifyTurnstile(
  env: Env,
  token: string,
  ip: string
): Promise<TurnstileResult> {
  if (!env.TURNSTILE_SECRET) return { ok: false, status: 503, error: "turnstile_unavailable" };
  if (!token || token.length > 2048) return { ok: false, status: 403, error: "turnstile_failed" };
  const form = new FormData();
  form.append("secret", env.TURNSTILE_SECRET);
  form.append("response", token);
  if (ip) form.append("remoteip", ip);
  let res: Response;
  try {
    const req = new Request(SITEVERIFY, { method: "POST", body: form });
    res = await (env.TURNSTILE_API ? env.TURNSTILE_API.fetch(req) : fetch(req));
  } catch {
    return { ok: false, status: 503, error: "turnstile_unavailable" };
  }
  if (!res.ok) return { ok: false, status: 503, error: "turnstile_unavailable" };
  const body = (await res.json().catch(() => ({}))) as { success?: boolean };
  return body.success === true
    ? { ok: true }
    : { ok: false, status: 403, error: "turnstile_failed" };
}
