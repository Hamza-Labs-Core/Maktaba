// Small crypto helpers on WebCrypto.
const enc = new TextEncoder();

export function randomBytes(n: number): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(n));
}

export function b64url(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function fromB64url(s: string): Uint8Array {
  const b = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b + "=".repeat((4 - (b.length % 4)) % 4));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

export function hex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export async function sha256(data: Uint8Array | string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.digest("SHA-256", (typeof data === "string" ? enc.encode(data) : data) as BufferSource));
}

export async function sha256hex(data: Uint8Array | string): Promise<string> {
  return hex(await sha256(data));
}

/** An id with a prefix: "a_" + 20 random bytes in base32-ish hex. */
export function newId(prefix: string): string {
  return prefix + "_" + hex(randomBytes(12));
}

export function token(): string {
  return b64url(randomBytes(32));
}

/** Constant-time comparison of two byte strings. */
export function same(a: Uint8Array, b: Uint8Array): boolean {
  if (a.length !== b.length) return false;
  let d = 0;
  for (let i = 0; i < a.length; i++) d |= a[i]! ^ b[i]!;
  return d === 0;
}

async function aesKey(b64: string): Promise<CryptoKey> {
  const raw = fromB64url(b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, ""));
  if (raw.length !== 32) throw new Error("MFA_ENC_KEY must be 32 bytes");
  return crypto.subtle.importKey("raw", raw as BufferSource, "AES-GCM", false, ["encrypt", "decrypt"]);
}

/** Encrypts a secret with the Worker secret key (AES-256-GCM): "v1." + iv + ciphertext. */
export async function seal(keyB64: string | undefined, plain: Uint8Array): Promise<string> {
  if (!keyB64) throw new Error("MFA_ENC_KEY is not set");
  const iv = randomBytes(12);
  const ct = new Uint8Array(
    await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv as BufferSource }, await aesKey(keyB64), plain as BufferSource),
  );
  return "v1." + b64url(iv) + "." + b64url(ct);
}

export async function unseal(keyB64: string | undefined, sealed: string): Promise<Uint8Array> {
  if (!keyB64) throw new Error("MFA_ENC_KEY is not set");
  const [v, iv, ct] = sealed.split(".");
  if (v !== "v1" || !iv || !ct) throw new Error("not a sealed secret");
  return new Uint8Array(
    await crypto.subtle.decrypt(
      { name: "AES-GCM", iv: fromB64url(iv) as BufferSource },
      await aesKey(keyB64),
      fromB64url(ct) as BufferSource,
    ),
  );
}
