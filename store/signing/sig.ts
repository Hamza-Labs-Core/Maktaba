// The store's signer (story 54.14), on WebCrypto Ed25519 so the same code runs in a
// Cloudflare Worker and in Node. It writes exactly what the home's verifiers read
// (shared/plugins/pluginsig in Go, plugind/crates/sig in Rust), and verifies the same
// files (the store checks a developer's signature on upload, 54.16 C2).
//
// A signature is Ed25519 over "maktaba-plugins/v1/<kind>\n" + the canonical bytes of the
// signed object. A key id is the first 16 bytes of the sha256 of the raw public key, in hex.

import { canonical, canonicalOf, canonicalStringOf, parseJSON, type JSONValue } from "./canonical";

export const KIND = {
  root: "root",
  storeKeys: "store-keys",
  developer: "developer",
  store: "store",
  catalog: "catalog",
  revoked: "revoked",
  revokedHead: "revoked-head",
  entitlement: "entitlement",
} as const;
export type Kind = (typeof KIND)[keyof typeof KIND];

const enc = new TextEncoder();
const subtle = globalThis.crypto.subtle;

export function toB64(b: Uint8Array): string {
  let s = "";
  for (const x of b) s += String.fromCharCode(x);
  return btoa(s);
}

export function fromB64(s: string): Uint8Array {
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(s) || s.length % 4 !== 0) throw new Error("not base64");
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export function toHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export async function sha256(b: Uint8Array): Promise<Uint8Array> {
  return new Uint8Array(await subtle.digest("SHA-256", b as BufferSource));
}

export async function sha256Hex(b: Uint8Array | string): Promise<string> {
  return toHex(await sha256(typeof b === "string" ? enc.encode(b) : b));
}

export async function keyId(publicRaw: Uint8Array): Promise<string> {
  return toHex((await sha256(publicRaw)).slice(0, 16));
}

/** The message a signature of kind covers. */
export function message(kind: Kind, signed: Uint8Array | string): Uint8Array {
  const c = canonical(signed);
  const prefix = enc.encode(`maktaba-plugins/v1/${kind}\n`);
  const out = new Uint8Array(prefix.length + c.length);
  out.set(prefix, 0);
  out.set(c, prefix.length);
  return out;
}

// PKCS #8 for an Ed25519 seed: the fixed DER head and the 32 bytes (RFC 8410).
const PKCS8_HEAD = new Uint8Array([0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04, 0x20]);

/** A private key and its id. The store's online key is a Worker secret (its seed in base64). */
export class Signer {
  private constructor(
    readonly id: string,
    readonly publicRaw: Uint8Array,
    private readonly key: CryptoKey,
  ) {}

  /** From a 32-byte seed. */
  static async fromSeed(seed: Uint8Array): Promise<Signer> {
    if (seed.length !== 32) throw new Error("an Ed25519 seed is 32 bytes");
    const pkcs8 = new Uint8Array(PKCS8_HEAD.length + 32);
    pkcs8.set(PKCS8_HEAD, 0);
    pkcs8.set(seed, PKCS8_HEAD.length);
    const key = await subtle.importKey("pkcs8", pkcs8 as BufferSource, { name: "Ed25519" }, true, ["sign"]);
    const jwk = await subtle.exportKey("jwk", key);
    const publicRaw = fromB64url(jwk.x ?? "");
    const signer = new Signer(await keyId(publicRaw), publicRaw, key);
    // keep only a non-extractable copy: the seed never leaves through this object
    const sealed = await subtle.importKey("pkcs8", pkcs8 as BufferSource, { name: "Ed25519" }, false, ["sign"]);
    pkcs8.fill(0);
    return new Signer(signer.id, publicRaw, sealed);
  }

  /** From a base64 seed (a Worker secret's value). */
  static async fromSecret(b64: string): Promise<Signer> {
    return Signer.fromSeed(fromB64(b64.trim()));
  }

  get public(): string {
    return toB64(this.publicRaw);
  }

  async sign(kind: Kind, signed: Uint8Array | string): Promise<string> {
    const sig = await subtle.sign({ name: "Ed25519" }, this.key, message(kind, signed) as BufferSource);
    return toB64(new Uint8Array(sig));
  }
}

function fromB64url(s: string): Uint8Array {
  const b = s.replace(/-/g, "+").replace(/_/g, "/");
  return fromB64(b + "=".repeat((4 - (b.length % 4)) % 4));
}

export async function verifySig(publicRaw: Uint8Array, kind: Kind, signed: Uint8Array | string, sig: string): Promise<boolean> {
  let s: Uint8Array;
  let msg: Uint8Array;
  try {
    s = fromB64(sig);
    msg = message(kind, signed);
  } catch {
    return false;
  }
  if (s.length !== 64 || publicRaw.length !== 32) return false;
  const key = await subtle.importKey("raw", publicRaw as BufferSource, { name: "Ed25519" }, false, ["verify"]);
  return subtle.verify({ name: "Ed25519" }, key, s as BufferSource, msg as BufferSource);
}

export interface Signature {
  key_id: string;
  sig: string;
}

export interface Envelope<T = JSONValue> {
  signed: T;
  signatures: Signature[];
}

/** An envelope of signed, signed by each signer, as canonical text. */
export async function signEnvelope(kind: Kind, signed: unknown, ...signers: Signer[]): Promise<string> {
  const text = canonicalStringOf(signed);
  const signatures: Signature[] = [];
  for (const s of signers) signatures.push({ key_id: s.id, sig: await s.sign(kind, text) });
  return canonicalStringOf({ signed: JSON.parse(text), signatures });
}

/** A package signature file ({key_id, sig, signed}, and the developer's public key). */
export async function signFile(kind: "developer" | "store", signer: Signer, signed: unknown): Promise<string> {
  const text = canonicalStringOf(signed);
  const file: Record<string, unknown> = { key_id: signer.id, sig: await signer.sign(kind, text), signed: JSON.parse(text) };
  if (kind === "developer") file.public = signer.public;
  return canonicalStringOf(file);
}

/** files_sha256: the sha256 of the canonical bytes of files.json. */
export async function filesSHA256(filesJSON: string | Uint8Array): Promise<string> {
  return toHex(await sha256(canonical(filesJSON)));
}

/** capabilities_sha256: the sha256 of the canonical normalised capabilities object. */
export async function capabilitiesSHA256(capabilities: unknown): Promise<string> {
  return toHex(await sha256(canonicalOf(capabilities)));
}

export { canonical, canonicalOf, canonicalStringOf, parseJSON };
