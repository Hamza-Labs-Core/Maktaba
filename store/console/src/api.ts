// The store API from the console: same origin, the session cookie, JSON in and out.
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    readonly body: Record<string, unknown>,
  ) {
    super(code);
  }
}

export async function api<T = Record<string, unknown>>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    credentials: "same-origin",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  const j = (await r.json().catch(() => ({}))) as Record<string, unknown>;
  if (!r.ok) throw new ApiError(r.status, String(j.error ?? "error"), j);
  return j as T;
}

// ---- passkeys in the browser (WebAuthn) -------------------------------------------------

const toBuf = (s: string) => {
  const b = s.replace(/-/g, "+").replace(/_/g, "/");
  return Uint8Array.from(atob(b + "=".repeat((4 - (b.length % 4)) % 4)), (c) => c.charCodeAt(0)).buffer;
};
const toB64url = (b: ArrayBuffer) =>
  btoa(String.fromCharCode(...new Uint8Array(b)))
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");

interface Opts {
  challenge: string;
  user?: { id: string; name: string; displayName: string };
  allowCredentials?: { type: string; id: string }[];
  excludeCredentials?: { type: string; id: string }[];
  [k: string]: unknown;
}

export async function createPasskey(o: Opts): Promise<unknown> {
  const c = (await navigator.credentials.create({
    publicKey: {
      ...(o as unknown as PublicKeyCredentialCreationOptions),
      challenge: toBuf(o.challenge),
      user: { ...o.user!, id: toBuf(o.user!.id) },
      excludeCredentials: (o.excludeCredentials ?? []).map((x) => ({ type: "public-key" as const, id: toBuf(x.id) })),
    },
  })) as PublicKeyCredential;
  const r = c.response as AuthenticatorAttestationResponse;
  return {
    id: c.id,
    rawId: toB64url(c.rawId),
    type: c.type,
    response: { clientDataJSON: toB64url(r.clientDataJSON), attestationObject: toB64url(r.attestationObject) },
  };
}

export async function getPasskey(o: Opts): Promise<unknown> {
  const c = (await navigator.credentials.get({
    publicKey: {
      ...(o as unknown as PublicKeyCredentialRequestOptions),
      challenge: toBuf(o.challenge),
      allowCredentials: (o.allowCredentials ?? []).map((x) => ({ type: "public-key" as const, id: toBuf(x.id) })),
    },
  })) as PublicKeyCredential;
  const r = c.response as AuthenticatorAssertionResponse;
  return {
    id: c.id,
    rawId: toB64url(c.rawId),
    type: c.type,
    response: {
      clientDataJSON: toB64url(r.clientDataJSON),
      authenticatorData: toB64url(r.authenticatorData),
      signature: toB64url(r.signature),
    },
  };
}
