// Stream signed-URL tokens, minted in the Worker with WebCrypto (Epic 34
// design §6.4). The token is the one api/internal/tvremote/cf.SignToken makes:
// header {alg, kid, typ}, claims {sub, kid, exp, nbf = now - 30 s}, RS256. Both
// serialize the JSON in the same key order, so for the same key and time the
// tokens are byte-identical (sign.test.ts checks against Go's fixture).

const enc = new TextEncoder();

function b64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function b64urlText(text: string): string {
  return b64url(enc.encode(text));
}

/** importSigningKey reads the key as the home gives it: a JWK, as base64 of its JSON or the JSON itself. */
export async function importSigningKey(jwk: string): Promise<CryptoKey> {
  const text = jwk.trim().startsWith("{") ? jwk : atob(jwk.trim());
  const parsed = JSON.parse(text) as JsonWebKey & { kid?: string };
  // WebCrypto refuses a JWK whose alg disagrees, and needs no kid
  delete parsed.kid;
  return crypto.subtle.importKey(
    "jwk",
    parsed,
    { name: "RSASSA-PKCS1-v1_5", hash: "SHA-256" },
    false,
    ["sign"]
  );
}

/** MAX_TTL_SECONDS is Cloudflare's limit on a token's life. */
export const MAX_TTL_SECONDS = 24 * 3600;

/** signToken mints a token for a live video (or input) id, valid ttl seconds from now. */
export async function signToken(
  key: CryptoKey,
  kid: string,
  sub: string,
  ttlSeconds: number,
  nowSeconds: number = Math.floor(Date.now() / 1000)
): Promise<string> {
  const ttl = ttlSeconds > 0 && ttlSeconds <= MAX_TTL_SECONDS ? ttlSeconds : MAX_TTL_SECONDS;
  const header = JSON.stringify({ alg: "RS256", kid, typ: "JWT" });
  const claims = JSON.stringify({ sub, kid, exp: nowSeconds + ttl, nbf: nowSeconds - 30 });
  const signing = `${b64urlText(header)}.${b64urlText(claims)}`;
  const sig = await crypto.subtle.sign("RSASSA-PKCS1-v1_5", key, enc.encode(signing));
  return `${signing}.${b64url(new Uint8Array(sig))}`;
}
