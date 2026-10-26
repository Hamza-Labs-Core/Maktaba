// The home's posts to the Worker are signed with the HMAC secret both hold
// (api/internal/tvremote.SignHome): hex HMAC-SHA256 over
// "<method>\n<path>\n<unix time>\n<hex sha256 of the body>". The time must be
// within MAX_SKEW_SECONDS, and newer than the last post accepted (no replay).

export const HEADER_TIME = "X-Maktaba-Time";
export const HEADER_SIGNATURE = "X-Maktaba-Signature";
export const MAX_SKEW_SECONDS = 60;

const enc = new TextEncoder();

function hex(buf: ArrayBuffer): string {
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export async function signHome(
  secret: string,
  method: string,
  path: string,
  ts: number,
  body: Uint8Array
): Promise<string> {
  const sum = hex(await crypto.subtle.digest("SHA-256", body));
  const key = await crypto.subtle.importKey(
    "raw",
    enc.encode(secret),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"]
  );
  return hex(
    await crypto.subtle.sign("HMAC", key, enc.encode(`${method}\n${path}\n${ts}\n${sum}`))
  );
}

function sameText(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

export type HomeCheck = { ok: true; ts: number } | { ok: false; why: string };

/** verifyHome checks a home post's signature and time (not the replay: the caller holds the last time). */
export async function verifyHome(
  secret: string,
  method: string,
  path: string,
  headers: Headers,
  body: Uint8Array,
  nowSeconds: number
): Promise<HomeCheck> {
  if (!secret) return { ok: false, why: "the site has no HOME_HMAC secret" };
  const ts = Number(headers.get(HEADER_TIME));
  const sig = headers.get(HEADER_SIGNATURE) ?? "";
  if (!Number.isInteger(ts) || Math.abs(nowSeconds - ts) > MAX_SKEW_SECONDS)
    return { ok: false, why: "stale or missing time" };
  const want = await signHome(secret, method, path, ts, body);
  if (!sameText(want, sig.toLowerCase())) return { ok: false, why: "bad signature" };
  return { ok: true, ts };
}
