// Small byte helpers on WebCrypto, shared by the tokens, the ingest checks and the edge's signer.
const enc = new TextEncoder();

export function utf8(s: string): Uint8Array {
  return enc.encode(s);
}

export function b64url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromB64url(text: string): Uint8Array {
  const s = text.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(s + "=".repeat((4 - (s.length % 4)) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function hex(bytes: ArrayBuffer | Uint8Array): string {
  const u = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
  return [...u].map((b) => b.toString(16).padStart(2, "0")).join("");
}

export async function sha256Hex(data: ArrayBuffer | Uint8Array | string): Promise<string> {
  const bytes = typeof data === "string" ? utf8(data) : data;
  return hex(await crypto.subtle.digest("SHA-256", bytes));
}

/** sameText compares two strings in constant time for their length. */
export function sameText(a: string, b: string): boolean {
  if (a.length !== b.length) return false;
  let diff = 0;
  for (let i = 0; i < a.length; i++) diff |= a.charCodeAt(i) ^ b.charCodeAt(i);
  return diff === 0;
}

// PKCS #8 for an Ed25519 seed: the fixed DER head and the 32 bytes (RFC 8410)
const PKCS8_HEAD = [
  0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20,
];

/** importEd25519Seed makes a signing key from a 32-byte seed (Go's ed25519.NewKeyFromSeed). */
export async function importEd25519Seed(seed: Uint8Array): Promise<CryptoKey> {
  if (seed.length !== 32) throw new Error("an Ed25519 seed is 32 bytes");
  const pkcs8 = new Uint8Array(PKCS8_HEAD.length + 32);
  pkcs8.set(PKCS8_HEAD);
  pkcs8.set(seed, PKCS8_HEAD.length);
  return crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, true, ["sign"]);
}

/** ed25519PublicOf is the raw 32-byte public key of a seed. */
export async function ed25519PublicOf(seed: Uint8Array): Promise<Uint8Array> {
  const key = await importEd25519Seed(seed);
  const jwk = (await crypto.subtle.exportKey("jwk", key)) as JsonWebKey;
  return fromB64url(jwk.x ?? "");
}
