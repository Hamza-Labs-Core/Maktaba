// Passkeys in the browser (story 50.33 §2, part 2). The API sends WebAuthn options as JSON
// (binary fields in base64url, as go-webauthn writes them) and takes the browser's answer back
// as JSON in the same encoding. These helpers turn one into the other around
// navigator.credentials.create and .get, so they work in every browser that has passkeys,
// whether or not it has the newer parse…FromJSON / toJSON methods.

/** base64url (no padding) to bytes. */
export function fromB64url(s: string): ArrayBuffer {
  const b64 = s.replace(/-/g, "+").replace(/_/g, "/");
  const bin = atob(b64 + "=".repeat((4 - (b64.length % 4)) % 4));
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out.buffer;
}

/** bytes to base64url (no padding). */
export function toB64url(buf: ArrayBuffer | ArrayBufferView): string {
  const bytes =
    buf instanceof ArrayBuffer
      ? new Uint8Array(buf)
      : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** True when this browser can make and use passkeys. */
export function passkeysSupported(): boolean {
  return (
    typeof window !== "undefined" &&
    typeof window.PublicKeyCredential !== "undefined" &&
    typeof navigator !== "undefined" &&
    !!navigator.credentials
  );
}

interface Descriptor {
  id: string;
  type: string;
  transports?: string[];
}

/** The API's registration options: `{ publicKey: … }` with base64url binary fields. */
export interface CreationOptionsJSON {
  publicKey: {
    challenge: string;
    user: { id: string; name: string; displayName: string };
    excludeCredentials?: Descriptor[];
    [key: string]: unknown;
  };
}

/** The API's assertion options: `{ publicKey: … }` with base64url binary fields. */
export interface RequestOptionsJSON {
  publicKey: {
    challenge: string;
    allowCredentials?: Descriptor[];
    [key: string]: unknown;
  };
}

const descriptors = (ds: Descriptor[] | undefined): PublicKeyCredentialDescriptor[] | undefined =>
  ds?.map((d) => ({
    type: "public-key",
    id: fromB64url(d.id),
    transports: d.transports as AuthenticatorTransport[] | undefined,
  }));

/** The no-passkey outcome a caller tells apart: the person closed the browser's prompt. */
export class PasskeyCancelled extends Error {
  constructor() {
    super("passkey cancelled");
  }
}

function rethrow(err: unknown): never {
  // NotAllowedError is a closed prompt or a timeout: the person can try again
  if (err instanceof DOMException && err.name === "NotAllowedError") throw new PasskeyCancelled();
  throw err;
}

/** navigator.credentials.create for the API's options; the answer, ready to post. */
export async function createPasskey(options: CreationOptionsJSON): Promise<unknown> {
  const pk = options.publicKey;
  const publicKey = {
    ...pk,
    challenge: fromB64url(pk.challenge),
    user: { ...pk.user, id: fromB64url(pk.user.id) },
    excludeCredentials: descriptors(pk.excludeCredentials),
  } as unknown as PublicKeyCredentialCreationOptions;
  let cred: PublicKeyCredential | null;
  try {
    cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential | null;
  } catch (err) {
    rethrow(err);
  }
  if (!cred) throw new PasskeyCancelled();
  const res = cred.response as AuthenticatorAttestationResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      attestationObject: toB64url(res.attestationObject),
      transports: res.getTransports?.() ?? [],
    },
  };
}

/** navigator.credentials.get for the API's options; the answer, ready to post. */
export async function getPasskey(options: RequestOptionsJSON): Promise<unknown> {
  const pk = options.publicKey;
  const publicKey = {
    ...pk,
    challenge: fromB64url(pk.challenge),
    allowCredentials: descriptors(pk.allowCredentials),
  } as unknown as PublicKeyCredentialRequestOptions;
  let cred: PublicKeyCredential | null;
  try {
    cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential | null;
  } catch (err) {
    rethrow(err);
  }
  if (!cred) throw new PasskeyCancelled();
  const res = cred.response as AuthenticatorAssertionResponse;
  return {
    id: cred.id,
    rawId: toB64url(cred.rawId),
    type: cred.type,
    authenticatorAttachment: cred.authenticatorAttachment ?? undefined,
    clientExtensionResults: cred.getClientExtensionResults?.() ?? {},
    response: {
      clientDataJSON: toB64url(res.clientDataJSON),
      authenticatorData: toB64url(res.authenticatorData),
      signature: toB64url(res.signature),
      userHandle: res.userHandle ? toB64url(res.userHandle) : undefined,
    },
  };
}
