// Test helpers for the store Worker in Miniflare: requests, accounts, factors (a fixture
// passkey authenticator and TOTP codes), developers and packages. TEST keys only.
import { env, exports } from "cloudflare:workers";
import { b64url, fromB64url, sha256 } from "../../web/crypto";
import type { Env } from "../../web/env";
import { code, stepAt } from "../../web/totp";
import { pack, inspect } from "../../web/wasm";
import { Signer } from "../../signing/sig";

export const E = env as unknown as Env & { TEST_STORE_KEYS: string; PLUGINS_ROOT: string; TEST_COMPONENTS: string };

/** A component of the corpus (checks/testdata/components). */
export function component(name: string): Uint8Array {
  const b64 = (JSON.parse(E.TEST_COMPONENTS) as Record<string, string>)[name];
  if (!b64) throw new Error(`no component ${name}`);
  return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
}
export const ORIGIN = "https://plugins.maktaba.tv";

export interface Res {
  status: number;
  body: Record<string, unknown> & { error?: string };
  cookie?: string;
}

export async function call(
  method: string,
  path: string,
  opts: { body?: unknown; cookie?: string; headers?: Record<string, string>; raw?: BodyInit } = {},
): Promise<Res> {
  // each call from its own address unless a test names one (the rate limits are per address)
  const ip = `10.${(Math.random() * 255) | 0}.${(Math.random() * 255) | 0}.${(Math.random() * 255) | 0}`;
  const h = new Headers({ Origin: ORIGIN, "CF-Connecting-IP": ip, ...(opts.headers ?? {}) });
  if (opts.cookie) h.set("Cookie", `__Host-mks_session=${opts.cookie}`);
  if (opts.body !== undefined) h.set("Content-Type", "application/json");
  const r = await (exports as unknown as { default: Fetcher }).default.fetch(`${ORIGIN}${path}`, {
    method,
    redirect: "manual",
    headers: h,
    body: opts.raw ?? (opts.body !== undefined ? JSON.stringify(opts.body) : undefined),
  });
  const set = r.headers.get("Set-Cookie") ?? "";
  const m = /__Host-mks_session=([^;]*)/.exec(set);
  const text = await r.text();
  let body: Res["body"] = {};
  try {
    body = text ? JSON.parse(text) : {};
  } catch {
    body = { text };
  }
  return { status: r.status, body, cookie: m && m[1] ? m[1] : undefined };
}

/** The newest mail to an address in the outbox (MAIL_PROVIDER "outbox"); mail is queued, so it waits. */
export async function mailTo(
  to: string,
  template?: string,
): Promise<{ template: string; params: Record<string, string>; subject: string; text: string } | null> {
  for (let i = 0; i < 400; i++) {
    const list = await E.PRIVATE.list({ prefix: "mail-outbox/" });
    const found = [];
    for (const o of list.objects) {
      const m = JSON.parse(await (await E.PRIVATE.get(o.key))!.text());
      if (m.to === to && (!template || m.template === template)) found.push(m);
    }
    if (found.length) return found.at(-1);
    await new Promise((r) => setTimeout(r, 25));
  }
  return null;
}

let n = 0;
export const uniqueEmail = (tag = "u") => `${tag}${Date.now()}${n++}@example.com`;

/** A verified account, signed in: its cookie. */
export async function account(
  opts: { verified?: boolean; password?: string; email?: string } = {},
): Promise<{ cookie: string; email: string; id: string; password: string }> {
  const email = opts.email ?? uniqueEmail();
  const password = opts.password ?? "correct horse battery";
  const r = await call("POST", "/v1/accounts", { body: { email, password, display_name: "Tester" } });
  if (r.status !== 202 || !r.cookie) throw new Error(`signup ${r.status} ${JSON.stringify(r.body)}`);
  if (opts.verified !== false) {
    const m = await mailTo(email, "verify_email");
    const token = m!.params.link!.split("#")[1];
    const v = await call("POST", "/v1/accounts/verify", { body: { token } });
    if (v.status !== 200) throw new Error(`verify ${v.status}`);
  }
  const id = (await E.DB_ACCOUNTS.prepare("SELECT id FROM accounts WHERE email = ?").bind(email).first<{ id: string }>())!.id;
  return { cookie: r.cookie, email, id, password };
}

/** Moves the session's last factor confirmation back by minutes (the re-auth tests). */
export async function ageFactor(cookie: string, minutes: number): Promise<void> {
  const h = [...(await sha256(cookie))].map((x) => x.toString(16).padStart(2, "0")).join("");
  await E.DB_ACCOUNTS.prepare("UPDATE sessions SET mfa_at = ? WHERE id_hash = ?")
    .bind(new Date(Date.now() - minutes * 60_000).toISOString().replace(/\.\d{3}Z$/, "Z"), h)
    .run();
}

function b32decode(s: string): Uint8Array {
  const A = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = 0;
  let v = 0;
  const out: number[] = [];
  for (const c of s) {
    v = (v << 5) | A.indexOf(c);
    bits += 5;
    if (bits >= 8) {
      out.push((v >>> (bits - 8)) & 255);
      bits -= 8;
    }
  }
  return new Uint8Array(out);
}

/** Adds a TOTP factor: answers a function giving the code for a step offset. */
export async function addTotp(cookie: string): Promise<{ codeAt: (offset?: number) => Promise<string>; recovery: string[] }> {
  const b = await call("POST", "/v1/mfa/totp/begin", { cookie });
  if (b.status !== 200) throw new Error(`totp begin ${b.status} ${JSON.stringify(b.body)}`);
  const secret = b32decode(b.body.secret as string);
  const codeAt = (offset = 0) => code(secret, stepAt(new Date()) + offset);
  const c = await call("POST", "/v1/mfa/totp/confirm", { cookie, body: { setup_id: b.body.setup_id, code: await codeAt(-1) } });
  if (c.status !== 201) throw new Error(`totp confirm ${c.status} ${JSON.stringify(c.body)}`);
  return { codeAt, recovery: c.body.recovery_codes as string[] };
}

// ---- a fixture passkey authenticator (ES256, user verified) --------------------------------

function cborEncode(v: unknown): Uint8Array {
  const out: number[] = [];
  const head = (major: number, n: number) => {
    if (n < 24) out.push((major << 5) | n);
    else if (n < 256) out.push((major << 5) | 24, n);
    else out.push((major << 5) | 25, n >> 8, n & 255);
  };
  const item = (x: unknown): void => {
    if (typeof x === "number") {
      if (x >= 0) head(0, x);
      else head(1, -1 - x);
    } else if (typeof x === "string") {
      const b = new TextEncoder().encode(x);
      head(3, b.length);
      out.push(...b);
    } else if (x instanceof Uint8Array) {
      head(2, x.length);
      out.push(...x);
    } else if (x instanceof Map) {
      head(5, x.size);
      for (const [k, val] of x) {
        item(k);
        item(val);
      }
    } else throw new Error("cbor: unsupported");
  };
  item(v);
  return new Uint8Array(out);
}

function rawToDer(raw: Uint8Array): Uint8Array {
  const int = (b: Uint8Array) => {
    let i = 0;
    while (i < b.length - 1 && b[i] === 0) i++;
    let v = b.slice(i);
    if (v[0]! & 0x80) v = new Uint8Array([0, ...v]);
    return [0x02, v.length, ...v];
  };
  const body = [...int(raw.slice(0, 32)), ...int(raw.slice(32))];
  return new Uint8Array([0x30, body.length, ...body]);
}

export class Authenticator {
  private count = 0;
  private constructor(
    private key: CryptoKeyPair,
    readonly id: Uint8Array,
  ) {}

  static async create(): Promise<Authenticator> {
    const key = (await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"])) as CryptoKeyPair;
    return new Authenticator(key, crypto.getRandomValues(new Uint8Array(16)));
  }

  private async authData(attested: boolean, flags = 0x05): Promise<Uint8Array> {
    const rpIdHash = await sha256(new URL(ORIGIN).hostname);
    this.count += 1;
    const c = this.count;
    const parts = [...rpIdHash, flags | (attested ? 0x40 : 0), (c >>> 24) & 255, (c >>> 16) & 255, (c >>> 8) & 255, c & 255];
    if (attested) {
      const jwk = (await crypto.subtle.exportKey("jwk", this.key.publicKey)) as JsonWebKey;
      const cose = cborEncode(
        new Map<number, unknown>([
          [1, 2],
          [3, -7],
          [-1, 1],
          [-2, fromB64url(jwk.x!)],
          [-3, fromB64url(jwk.y!)],
        ]),
      );
      parts.push(...new Uint8Array(16), this.id.length >> 8, this.id.length & 255, ...this.id, ...cose);
    }
    return new Uint8Array(parts);
  }

  private client(type: string, challenge: string, origin = ORIGIN): Uint8Array {
    return new TextEncoder().encode(JSON.stringify({ type, challenge, origin }));
  }

  async register(challenge: string, opts: { origin?: string; flags?: number } = {}) {
    const authData = await this.authData(true, opts.flags);
    const att = cborEncode(
      new Map<string, unknown>([
        ["fmt", "none"],
        ["attStmt", new Map()],
        ["authData", authData],
      ]),
    );
    return {
      id: b64url(this.id),
      rawId: b64url(this.id),
      type: "public-key",
      response: { clientDataJSON: b64url(this.client("webauthn.create", challenge, opts.origin)), attestationObject: b64url(att) },
    };
  }

  async assert(challenge: string, opts: { origin?: string; flags?: number; replayCount?: number } = {}) {
    if (opts.replayCount !== undefined) this.count = opts.replayCount - 1;
    const authData = await this.authData(false, opts.flags);
    const cd = this.client("webauthn.get", challenge, opts.origin);
    const signed = new Uint8Array([...authData, ...(await sha256(cd))]);
    const sig = new Uint8Array(await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, this.key.privateKey, signed as BufferSource));
    return {
      id: b64url(this.id),
      rawId: b64url(this.id),
      type: "public-key",
      response: { clientDataJSON: b64url(cd), authenticatorData: b64url(authData), signature: b64url(rawToDer(sig)) },
    };
  }
}

/** Adds a passkey and signs in with it: the new session's cookie. */
export async function addPasskey(cookie: string, email?: string): Promise<{ auth: Authenticator; cookie: string }> {
  const auth = await Authenticator.create();
  const o = await call("POST", "/v1/mfa/passkey/options", { cookie });
  if (o.status !== 200) throw new Error(`passkey options ${o.status} ${JSON.stringify(o.body)}`);
  const pk = o.body.publicKey as { challenge: string };
  const r = await call("POST", "/v1/mfa/passkey", {
    cookie,
    body: { challenge_id: o.body.challenge_id, credential: await auth.register(pk.challenge) },
  });
  if (r.status !== 201) throw new Error(`passkey ${r.status} ${JSON.stringify(r.body)}`);
  void email;
  return { auth, cookie: await passkeySignIn(auth) };
}

export async function passkeySignIn(auth: Authenticator): Promise<string> {
  const o = await call("POST", "/v1/sessions/passkey/options", {});
  const s = await call("POST", "/v1/sessions/passkey", {
    body: { challenge_id: o.body.challenge_id, credential: await auth.assert((o.body.publicKey as { challenge: string }).challenge) },
  });
  if (s.status !== 200 || !s.cookie) throw new Error(`passkey sign-in ${s.status} ${JSON.stringify(s.body)}`);
  return s.cookie;
}

export async function grant(id: string, role: string): Promise<void> {
  await E.DB_ACCOUNTS.prepare("INSERT OR IGNORE INTO roles (account, role, granted_by, at) VALUES (?, ?, 'test', ?)")
    .bind(id, role, new Date().toISOString())
    .run();
}

// ---- developers and packages ------------------------------------------------------------

export interface Dev {
  cookie: string;
  id: string;
  email: string;
  key: Signer;
  prefix: string;
  totp: (o?: number) => Promise<string>;
}

let devN = 0;
/** A developer with TOTP, the agreement, a community prefix and a registered key. */
export async function developer(): Promise<Dev> {
  const a = await account();
  const t = await addTotp(a.cookie);
  const d = await call("POST", "/v1/developers", { cookie: a.cookie, body: { agreement_version: 1 } });
  if (d.status !== 200) throw new Error(`developer ${d.status} ${JSON.stringify(d.body)}`);
  const handle = `dev${Date.now() % 1e9}${devN++}`;
  const p = await call("POST", "/v1/developers/prefixes", { cookie: a.cookie, body: { community: handle } });
  if (p.status !== 201) throw new Error(`prefix ${p.status} ${JSON.stringify(p.body)}`);
  const key = await Signer.fromSeed(crypto.getRandomValues(new Uint8Array(32)));
  const k = await call("POST", "/v1/developers/keys", { cookie: a.cookie, body: { id: key.id, public: key.public } });
  if (k.status !== 201) throw new Error(`key ${k.status} ${JSON.stringify(k.body)}`);
  return { cookie: a.cookie, id: a.id, email: a.email, key, prefix: p.body.prefix as string, totp: t.codeAt };
}

const enc = new TextEncoder();

export function manifestOf(id: string, version: string, capabilities: { required?: string[]; optional?: string[] } = {}): string {
  const q = (l: string[] = []) => `[${l.map((x) => `"${x}"`).join(", ")}]`;
  return `schema = 1
id = "${id}"
version = "${version}"
plugin_api = "1.0"
maktaba = ">=0.6.0, <0.9.0"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[capabilities]
required = ${q(capabilities.required)}
optional = ${q(capabilities.optional)}

[[stage]]
name = "tag"
subject = "video"
export = "run-tag"
version = 1
`;
}

/** A package signed by key (no signature with key null). */
export async function buildPackage(
  id: string,
  version: string,
  key: Signer | null,
  opts: { files?: Record<string, Uint8Array>; capabilities?: { required?: string[]; optional?: string[] } } = {},
): Promise<Uint8Array> {
  const files: Record<string, Uint8Array> = {
    "manifest.toml": enc.encode(manifestOf(id, version, opts.capabilities)),
    "i18n/en.json": enc.encode('{"plugin.name":"Tagger","plugin.summary":"Adds tags."}'),
    "i18n/ar.json": enc.encode('{"plugin.name":"الوسّام","plugin.summary":"يضيف الوسوم."}'),
    "plugin.wasm": new Uint8Array([0, 0x61, 0x73, 0x6d, 1, 2, 3]),
    ...(opts.files ?? {}),
  };
  const unsigned = await pack(files);
  if (!key) return unsigned;
  const i = await inspect(unsigned);
  const { filesSHA256, signFile } = await import("../../signing/sig");
  const sig = await signFile("developer", key, { plugin_id: id, version, files_sha256: await filesSHA256(i.files_json) });
  return pack(files, { "signatures/developer.sig": sig });
}

async function hexOf(b: Uint8Array): Promise<string> {
  return [...(await sha256(b))].map((x) => x.toString(16).padStart(2, "0")).join("");
}

/** Submits pkg as dev: start, both uploads, uploaded. Answers the submission id. */
export async function submit(dev: Dev, id: string, version: string, pkg: Uint8Array): Promise<string> {
  const source = enc.encode("source archive");
  const s = await call("POST", "/v1/submissions", {
    cookie: dev.cookie,
    body: {
      plugin_id: id,
      version,
      package_sha256: await hexOf(pkg),
      package_size: pkg.length,
      source_size: source.length,
      notes_en: "First.",
      notes_ar: "الأول.",
    },
  });
  if (s.status !== 201) throw new Error(`submit ${s.status} ${JSON.stringify(s.body)}`);
  const up = s.body.upload as { package: string; source: string };
  for (const [url, data] of [
    [up.package, pkg],
    [up.source, source],
  ] as const) {
    const r = await call("PUT", new URL(url).pathname, { raw: data });
    if (r.status !== 200) throw new Error(`upload ${r.status}`);
  }
  const u = await call("POST", `/v1/submissions/${s.body.id}/uploaded`, { cookie: dev.cookie });
  if (u.status !== 200) throw new Error(`uploaded ${u.status} ${JSON.stringify(u.body)}`);
  return s.body.id as string;
}
