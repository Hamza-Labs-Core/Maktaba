// Passkeys (WebAuthn level 2) checked on WebCrypto (story 54.15 §4): user verification
// required, ES256 (-7) or EdDSA (-8), the origin and relying party of the store's host, a
// challenge used once, and the signature counter never going back. Attestation is not asked
// for ("none"): what a passkey proves here is possession, not its maker.
import { b64url, fromB64url, sha256, same } from "./crypto";

// ---- a small CBOR reader (RFC 8949: what authenticator data and COSE keys use) ----------

type CBOR = number | bigint | string | Uint8Array | boolean | null | CBOR[] | Map<CBOR, CBOR>;

export function decodeCBOR(b: Uint8Array): { value: CBOR; end: number } {
  let i = 0;
  const dv = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const len = (info: number): number => {
    if (info < 24) return info;
    if (info === 24) return b[i++]!;
    if (info === 25) {
      const v = dv.getUint16(i);
      i += 2;
      return v;
    }
    if (info === 26) {
      const v = dv.getUint32(i);
      i += 4;
      return v;
    }
    if (info === 27) {
      const v = dv.getBigUint64(i);
      i += 8;
      if (v > BigInt(Number.MAX_SAFE_INTEGER)) throw new Error("CBOR: a length too large");
      return Number(v);
    }
    throw new Error("CBOR: an indefinite length");
  };
  const item = (depth: number): CBOR => {
    if (depth > 16 || i >= b.length) throw new Error("CBOR: too deep or truncated");
    const first = b[i++]!;
    const major = first >> 5;
    const info = first & 31;
    switch (major) {
      case 0:
        return len(info);
      case 1:
        return -1 - len(info);
      case 2: {
        const n = len(info);
        if (i + n > b.length) throw new Error("CBOR: truncated bytes");
        const v = b.slice(i, i + n);
        i += n;
        return v;
      }
      case 3: {
        const n = len(info);
        if (i + n > b.length) throw new Error("CBOR: truncated text");
        const v = new TextDecoder("utf-8", { fatal: true, ignoreBOM: false }).decode(b.subarray(i, i + n));
        i += n;
        return v;
      }
      case 4: {
        const n = len(info);
        const a: CBOR[] = [];
        for (let k = 0; k < n; k++) a.push(item(depth + 1));
        return a;
      }
      case 5: {
        const n = len(info);
        const m = new Map<CBOR, CBOR>();
        for (let k = 0; k < n; k++) {
          const key = item(depth + 1);
          m.set(key, item(depth + 1));
        }
        return m;
      }
      case 7:
        if (info === 20) return false;
        if (info === 21) return true;
        if (info === 22) return null;
        throw new Error("CBOR: an unsupported simple value");
    }
    throw new Error("CBOR: an unsupported type");
  };
  const value = item(0);
  return { value, end: i };
}

// ---- authenticator data -------------------------------------------------------------------

const UP = 0x01;
const UV = 0x04;
const AT = 0x40;

interface AuthData {
  rpIdHash: Uint8Array;
  flags: number;
  signCount: number;
  credentialId?: Uint8Array;
  publicKey?: Map<CBOR, CBOR>;
}

function parseAuthData(a: Uint8Array): AuthData {
  if (a.length < 37) throw new Error("authenticator data too short");
  const flags = a[32]!;
  const signCount = new DataView(a.buffer, a.byteOffset + 33, 4).getUint32(0);
  const out: AuthData = { rpIdHash: a.slice(0, 32), flags, signCount };
  if (flags & AT) {
    const n = (a[53]! << 8) | a[54]!;
    out.credentialId = a.slice(55, 55 + n);
    const { value } = decodeCBOR(a.subarray(55 + n));
    if (!(value instanceof Map)) throw new Error("the credential public key is not a COSE key");
    out.publicKey = value;
  }
  return out;
}

/** A credential public key as stored: a JWK (P-256 for ES256, Ed25519 for EdDSA). */
export interface StoredKey {
  alg: -7 | -8;
  jwk: JsonWebKey;
}

function coseToJwk(k: Map<CBOR, CBOR>): StoredKey {
  const kty = k.get(1);
  const alg = k.get(3);
  const x = k.get(-2);
  if (kty === 2 && alg === -7 && k.get(-1) === 1 && x instanceof Uint8Array && k.get(-3) instanceof Uint8Array) {
    return { alg: -7, jwk: { kty: "EC", crv: "P-256", x: b64url(x), y: b64url(k.get(-3) as Uint8Array) } };
  }
  if (kty === 1 && alg === -8 && k.get(-1) === 6 && x instanceof Uint8Array) {
    return { alg: -8, jwk: { kty: "OKP", crv: "Ed25519", x: b64url(x) } };
  }
  throw new Error("only ES256 and EdDSA passkeys are accepted");
}

export class WebAuthnError extends Error {}

interface ClientData {
  type: string;
  challenge: string;
  origin: string;
  crossOrigin?: boolean;
}

async function checkClient(raw: Uint8Array, type: string, challenge: string, origin: string): Promise<void> {
  let c: ClientData;
  try {
    c = JSON.parse(new TextDecoder().decode(raw)) as ClientData;
  } catch {
    throw new WebAuthnError("clientDataJSON is not JSON");
  }
  if (c.type !== type) throw new WebAuthnError(`client data type ${c.type}`);
  if (c.challenge !== challenge) throw new WebAuthnError("the challenge differs");
  if (c.origin !== origin) throw new WebAuthnError(`origin ${c.origin}`);
  if (c.crossOrigin) throw new WebAuthnError("a cross-origin ceremony");
}

async function checkRp(a: AuthData, rpId: string): Promise<void> {
  if (!same(a.rpIdHash, await sha256(rpId))) throw new WebAuthnError("another relying party");
  if (!(a.flags & UP)) throw new WebAuthnError("the user was not present");
  if (!(a.flags & UV)) throw new WebAuthnError("the user was not verified");
}

export interface Registration {
  credentialId: string;
  key: StoredKey;
  signCount: number;
}

/** Checks a navigator.credentials.create() answer for challenge. */
export async function verifyRegistration(
  cred: { id?: string; response?: { clientDataJSON?: string; attestationObject?: string } },
  challenge: string,
  origin: string,
): Promise<Registration> {
  const rpId = new URL(origin).hostname;
  if (!cred.response?.clientDataJSON || !cred.response.attestationObject) throw new WebAuthnError("not a registration");
  await checkClient(fromB64url(cred.response.clientDataJSON), "webauthn.create", challenge, origin);
  const { value } = decodeCBOR(fromB64url(cred.response.attestationObject));
  if (!(value instanceof Map) || !(value.get("authData") instanceof Uint8Array)) throw new WebAuthnError("no authenticator data");
  const a = parseAuthData(value.get("authData") as Uint8Array);
  await checkRp(a, rpId);
  if (!a.credentialId || !a.publicKey) throw new WebAuthnError("no attested credential");
  let key: StoredKey;
  try {
    key = coseToJwk(a.publicKey);
  } catch (e) {
    throw new WebAuthnError((e as Error).message);
  }
  return { credentialId: b64url(a.credentialId), key, signCount: a.signCount };
}

/** ECDSA's DER signature as WebCrypto's r‖s. */
function derToRaw(der: Uint8Array): Uint8Array {
  if (der[0] !== 0x30) throw new WebAuthnError("not a DER signature");
  let i = 2;
  const int = () => {
    if (der[i++] !== 0x02) throw new WebAuthnError("not a DER signature");
    const n = der[i++]!;
    let v = der.slice(i, i + n);
    i += n;
    while (v.length > 32 && v[0] === 0) v = v.slice(1);
    const out = new Uint8Array(32);
    out.set(v, 32 - v.length);
    return out;
  };
  const r = int();
  const s = int();
  const raw = new Uint8Array(64);
  raw.set(r, 0);
  raw.set(s, 32);
  return raw;
}

/** Checks a navigator.credentials.get() answer; answers the new signature counter. */
export async function verifyAssertion(
  cred: { response?: { clientDataJSON?: string; authenticatorData?: string; signature?: string } },
  stored: StoredKey,
  storedCount: number,
  challenge: string,
  origin: string,
): Promise<number> {
  const rpId = new URL(origin).hostname;
  const r = cred.response;
  if (!r?.clientDataJSON || !r.authenticatorData || !r.signature) throw new WebAuthnError("not an assertion");
  const clientData = fromB64url(r.clientDataJSON);
  await checkClient(clientData, "webauthn.get", challenge, origin);
  const authData = fromB64url(r.authenticatorData);
  const a = parseAuthData(authData);
  await checkRp(a, rpId);
  if ((a.signCount !== 0 || storedCount !== 0) && a.signCount <= storedCount) {
    throw new WebAuthnError("the signature counter went back: a cloned authenticator");
  }
  const signed = new Uint8Array(authData.length + 32);
  signed.set(authData, 0);
  signed.set(await sha256(clientData), authData.length);
  const sig = fromB64url(r.signature);
  const ok =
    stored.alg === -7
      ? await crypto.subtle.verify(
          { name: "ECDSA", hash: "SHA-256" },
          await crypto.subtle.importKey("jwk", stored.jwk, { name: "ECDSA", namedCurve: "P-256" }, false, ["verify"]),
          derToRaw(sig) as BufferSource,
          signed as BufferSource,
        )
      : await crypto.subtle.verify(
          { name: "Ed25519" },
          await crypto.subtle.importKey("jwk", stored.jwk, { name: "Ed25519" }, false, ["verify"]),
          sig as BufferSource,
          signed as BufferSource,
        );
  if (!ok) throw new WebAuthnError("the signature does not verify");
  return a.signCount;
}
