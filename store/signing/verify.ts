// The chain of story 54.14 §3 in TypeScript: the store checks a developer's signature on
// upload (54.16 C2) and its own files before it publishes them, by the same rules as the
// home (Go: shared/plugins/pluginsig, Rust: plugind/crates/sig). The three agree on
// shared/plugins/testdata/signatures/vectors.json.

import { canonicalOf as canonicalOfValue, parseJSON, type JSONValue } from "./canonical";
import { filesSHA256, fromB64, keyId, sha256Hex, verifySig, type Kind } from "./sig";

export const CODE = {
  signatureInvalid: "plugin_signature_invalid",
  developerKeyMismatch: "developer_key_mismatch",
  keyUseMismatch: "key_use_mismatch",
  storeKeysRollback: "store_keys_rollback_refused",
  storeKeysExpired: "store_keys_expired",
  revocationRollback: "revocation_rollback_refused",
  revocationExpired: "revocation_expired",
  catalogRollback: "catalog_rollback_refused",
  catalogExpired: "catalog_expired",
  entitlementInvalid: "entitlement_invalid",
} as const;

export class VerifyError extends Error {
  constructor(
    readonly code: string,
    readonly step: string,
    msg: string,
  ) {
    super(`${code}: ${step}: ${msg}`);
  }
}

const fail = (code: string, step: string, msg: string) => new VerifyError(code, step, msg);

export interface TrustState {
  store_keys_version: number;
  store_keys_sha256?: string;
  revoked_version: number;
  revoked_sha256?: string;
  catalog_versions?: Record<string, number>;
  newest_issued?: string;
}

export const emptyState = (): TrustState => ({ store_keys_version: 0, revoked_version: 0 });

/** The home's time: the newest of the clock and the newest issue time seen. */
export function homeNow(st: TrustState, clock: Date): Date {
  const n = st.newest_issued ? parseTime("newest_issued", st.newest_issued, "list") : null;
  return n && n > clock ? n : clock;
}

function parseTime(field: string, s: unknown, step: string): Date {
  if (typeof s !== "string" || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/.test(s)) {
    throw fail(CODE.signatureInvalid, step, `${field}: ${JSON.stringify(s)} is not an RFC 3339 time`);
  }
  return new Date(s);
}

type Obj = { [k: string]: JSONValue };

function obj(v: JSONValue | undefined, step: string, what: string): Obj {
  if (v === null || typeof v !== "object" || Array.isArray(v)) throw fail(CODE.signatureInvalid, step, `${what} is not an object`);
  return v;
}

/** Only these fields, and the required ones present (Go and Rust refuse unknown fields too). */
function fields(o: Obj, step: string, what: string, required: string[], optional: string[] = []): void {
  for (const k of Object.keys(o)) {
    if (!required.includes(k) && !optional.includes(k)) throw fail(CODE.signatureInvalid, step, `${what}: unknown field ${k}`);
  }
  for (const k of required) {
    if (!(k in o)) throw fail(CODE.signatureInvalid, step, `${what}: no ${k}`);
  }
}

function str(o: Obj, k: string, step: string): string {
  const v = o[k];
  if (typeof v !== "string") throw fail(CODE.signatureInvalid, step, `${k} is not a string`);
  return v;
}

function int(o: Obj, k: string, step: string): number {
  const v = o[k];
  if (typeof v !== "number") throw fail(CODE.signatureInvalid, step, `${k} is not an integer`);
  return v;
}

function arr(o: Obj, k: string, step: string): JSONValue[] {
  const v = o[k];
  if (!Array.isArray(v)) throw fail(CODE.signatureInvalid, step, `${k} is not a list`);
  return v;
}

function parse(raw: string | Uint8Array, step: string): JSONValue {
  try {
    return parseJSON(raw);
  } catch (e) {
    throw fail(CODE.signatureInvalid, step, (e as Error).message);
  }
}

interface Env {
  signed: Obj;
  signedText: string;
  signatures: { key_id: string; sig: string }[];
}

function envelope(raw: string | Uint8Array, step: string): Env {
  const o = obj(parse(raw, step), step, "the file");
  fields(o, step, "the envelope", ["signed", "signatures"]);
  const signed = obj(o.signed, step, "signed");
  const signatures = arr(o, "signatures", step).map((s) => {
    const so = obj(s, step, "a signature");
    fields(so, step, "a signature", ["key_id", "sig"]);
    return { key_id: str(so, "key_id", step), sig: str(so, "sig", step) };
  });
  return { signed, signedText: JSON.stringify(signed), signatures };
}

async function decodeKey(id: string, pub: string, step: string): Promise<Uint8Array> {
  let raw: Uint8Array;
  try {
    raw = fromB64(pub);
  } catch {
    throw fail(CODE.signatureInvalid, step, `key ${id}: not base64`);
  }
  if (raw.length !== 32) throw fail(CODE.signatureInvalid, step, `key ${id}: not an Ed25519 public key`);
  if ((await keyId(raw)) !== id) throw fail(CODE.signatureInvalid, step, `key ${id}: the id does not name its public key`);
  return raw;
}

async function countValid(env: Env, kind: Kind, keys: Map<string, Uint8Array>): Promise<number> {
  const seen = new Set<string>();
  for (const s of env.signatures) {
    const pub = keys.get(s.key_id);
    if (!pub || seen.has(s.key_id)) continue;
    if (await verifySig(pub, kind, env.signedText, s.sig)) seen.add(s.key_id);
  }
  return seen.size;
}

// ---- the root ----------------------------------------------------------------------------

export class Root {
  constructor(
    readonly version: number,
    readonly threshold: number,
    readonly keys: Map<string, Uint8Array>,
  ) {}

  static async parse(raw: string | Uint8Array): Promise<Root> {
    const step = "root";
    const env = envelope(raw, step);
    fields(env.signed, step, "the root", ["version", "threshold", "keys"]);
    const keys = new Map<string, Uint8Array>();
    for (const k of arr(env.signed, "keys", step)) {
      const ko = obj(k, step, "a key");
      fields(ko, step, "a root key", ["id", "public"]);
      keys.set(str(ko, "id", step), await decodeKey(str(ko, "id", step), str(ko, "public", step), step));
    }
    const threshold = int(env.signed, "threshold", step);
    if (keys.size > 0 && (threshold < 1 || threshold > keys.size)) throw fail(CODE.signatureInvalid, step, `threshold ${threshold}`);
    if (keys.size > 0 && (await countValid(env, "root", keys)) < threshold) {
      throw fail(CODE.signatureInvalid, step, "the root is not signed by its own keys");
    }
    return new Root(int(env.signed, "version", step), threshold, keys);
  }

  async verifyStoreKeys(raw: string | Uint8Array, st: TrustState, clock: Date): Promise<StoreKeys> {
    if (this.keys.size === 0) throw fail(CODE.signatureInvalid, "root", "this build carries no plugin root keys");
    const step = "store_keys";
    const env = envelope(raw, step);
    if ((await countValid(env, "store-keys", this.keys)) < this.threshold) {
      throw fail(CODE.signatureInvalid, step, "not signed by a key of the plugin root");
    }
    fields(env.signed, step, "store-keys.json", ["version", "expires", "keys"]);
    const version = int(env.signed, "version", step);
    const expires = parseTime("expires", env.signed.expires, step);
    const sum = await sha256Hex(canonicalText(env.signed));
    const keys = new Map<string, StoreKey>();
    if (version < st.store_keys_version) {
      throw fail(CODE.storeKeysRollback, step, `version ${version} is older than ${st.store_keys_version}, the newest seen`);
    }
    if (version === st.store_keys_version && st.store_keys_sha256 && st.store_keys_sha256 !== sum) {
      throw fail(CODE.storeKeysRollback, step, `version ${version} differs from the copy seen before`);
    }
    if (homeNow(st, clock) > expires) throw fail(CODE.storeKeysExpired, step, `expired ${String(env.signed.expires)}`);
    for (const k of arr(env.signed, "keys", step)) {
      const ko = obj(k, step, "a key");
      fields(ko, step, "a store key", ["id", "public", "use", "not_before", "not_after"]);
      const id = str(ko, "id", step);
      const use = str(ko, "use", step);
      if (use !== "store" && use !== "entitlement") throw fail(CODE.signatureInvalid, step, `key ${id}: use ${use}`);
      if (keys.has(id)) throw fail(CODE.signatureInvalid, step, `key ${id} is listed twice`);
      keys.set(id, {
        pub: await decodeKey(id, str(ko, "public", step), step),
        use,
        notBefore: parseTime("not_before", ko.not_before, step),
        notAfter: parseTime("not_after", ko.not_after, step),
      });
    }
    return new StoreKeys(version, expires, sum, keys);
  }
}

function canonicalText(o: JSONValue): string {
  // the canonical text of an already parsed value
  return new TextDecoder().decode(canonicalOfValue(o));
}

interface StoreKey {
  pub: Uint8Array;
  use: string;
  notBefore: Date;
  notAfter: Date;
}

export interface PackageInput {
  filesJSON: string | Uint8Array;
  developerSig?: string | Uint8Array | null;
  storeSig?: string | Uint8Array | null;
  pluginId: string;
  version: string;
  capabilitiesSHA256: string;
  /** Every file by its files.json path, for the last step (omit to skip it). */
  files?: Map<string, Uint8Array>;
}

export interface PackageResult {
  level: "none" | "developer" | "automated" | "reviewed";
  developerKeyId: string;
  reviewedAt: Date | null;
  filesSHA256: string;
}

export class StoreKeys {
  constructor(
    readonly version: number,
    readonly expires: Date,
    readonly sha256: string,
    private readonly keys: Map<string, StoreKey>,
  ) {}

  has(id: string): boolean {
    return this.keys.has(id);
  }

  private async verifyWith(env: Env, kind: Kind, use: string, step: string, at: Date): Promise<void> {
    let last: VerifyError = fail(CODE.signatureInvalid, step, "no signature");
    for (const s of env.signatures) {
      const k = this.keys.get(s.key_id);
      if (!k) {
        last = fail(CODE.signatureInvalid, step, `key ${s.key_id} is not in store-keys.json version ${this.version}`);
        continue;
      }
      if (k.use !== use) {
        last = fail(CODE.keyUseMismatch, step, `key ${s.key_id} is a ${k.use} key, not a ${use} key`);
        continue;
      }
      if (at < k.notBefore || at > k.notAfter) {
        last = fail(CODE.signatureInvalid, step, `key ${s.key_id} is not valid at ${at.toISOString()}`);
        continue;
      }
      if (await verifySig(k.pub, kind, env.signedText, s.sig)) return;
      last = fail(CODE.signatureInvalid, step, `the signature by ${s.key_id} does not verify`);
    }
    throw last;
  }

  async verifyPackage(p: PackageInput): Promise<PackageResult> {
    let filesSum: string;
    try {
      filesSum = await filesSHA256(p.filesJSON);
    } catch (e) {
      throw fail(CODE.signatureInvalid, "digest", `files.json: ${(e as Error).message}`);
    }
    const res: PackageResult = { level: "none", developerKeyId: "", reviewedAt: null, filesSHA256: filesSum };
    let storeDevKey: string | null = null;
    if (p.storeSig && p.storeSig.length > 0) {
      const step = "store_sig";
      const f = obj(parse(p.storeSig, step), step, "store.sig");
      fields(f, step, "store.sig", ["key_id", "sig", "signed"]);
      const s = obj(f.signed, step, "signed");
      fields(s, step, "store.sig's signed", [
        "plugin_id",
        "version",
        "files_sha256",
        "developer_key_id",
        "review_level",
        "reviewed_at",
        "capabilities_sha256",
      ]);
      const at = parseTime("reviewed_at", s.reviewed_at, step);
      await this.verifyWith(
        { signed: s, signedText: JSON.stringify(s), signatures: [{ key_id: str(f, "key_id", step), sig: str(f, "sig", step) }] },
        "store",
        "store",
        step,
        at,
      );
      const level = str(s, "review_level", step);
      if (level !== "automated" && level !== "reviewed") throw fail(CODE.signatureInvalid, step, `review level ${level}`);
      if (str(s, "files_sha256", step) !== filesSum) throw fail(CODE.signatureInvalid, step, "store.sig covers another files.json");
      if (str(s, "plugin_id", step) !== p.pluginId || str(s, "version", step) !== p.version) {
        throw fail(CODE.signatureInvalid, step, "store.sig is for another plugin or version");
      }
      if (str(s, "capabilities_sha256", step) !== p.capabilitiesSHA256) {
        throw fail(CODE.signatureInvalid, step, "the capabilities differ from those the store reviewed");
      }
      storeDevKey = str(s, "developer_key_id", step);
      res.level = level;
      res.reviewedAt = at;
    }
    if (!p.developerSig || p.developerSig.length === 0) {
      if (storeDevKey !== null)
        throw fail(CODE.signatureInvalid, "developer_sig", "store.sig names a developer key and the package has no developer.sig");
    } else {
      const step = "developer_sig";
      const f = obj(parse(p.developerSig, step), step, "developer.sig");
      fields(f, step, "developer.sig", ["key_id", "sig", "signed"], ["public"]);
      const id = str(f, "key_id", step);
      if (storeDevKey !== null && id !== storeDevKey) {
        throw fail(CODE.developerKeyMismatch, step, `signed by ${id}, store.sig names ${storeDevKey}`);
      }
      const pub = await decodeKey(id, typeof f.public === "string" ? f.public : "", step);
      const s = obj(f.signed, step, "signed");
      if (!(await verifySig(pub, "developer", JSON.stringify(s), str(f, "sig", step)))) {
        throw fail(CODE.signatureInvalid, step, `the signature by ${id} does not verify`);
      }
      fields(s, step, "developer.sig's signed", ["plugin_id", "version", "files_sha256"]);
      if (str(s, "files_sha256", step) !== filesSum) throw fail(CODE.signatureInvalid, step, "developer.sig covers another files.json");
      if (str(s, "plugin_id", step) !== p.pluginId || str(s, "version", step) !== p.version) {
        throw fail(CODE.signatureInvalid, step, "developer.sig is for another plugin or version");
      }
      res.developerKeyId = id;
      if (storeDevKey === null) res.level = "developer";
    }
    if (p.files) {
      const list = parse(p.filesJSON, "digest");
      if (!Array.isArray(list)) throw fail(CODE.signatureInvalid, "digest", "files.json is not a list");
      for (const e of list) {
        const eo = obj(e, "digest", "a files.json line");
        const path = str(eo, "path", "digest");
        const b = p.files.get(path);
        if (!b || b.length !== int(eo, "size", "digest") || (await sha256Hex(b)) !== str(eo, "sha256", "digest")) {
          throw fail(CODE.signatureInvalid, "digest", `${path} does not match files.json`);
        }
      }
    }
    return res;
  }

  private async list(raw: string | Uint8Array, kind: Kind): Promise<{ env: Env; issued: Date }> {
    const step = "list";
    const env = envelope(raw, step);
    const issued = parseTime("issued", env.signed.issued, step);
    await this.verifyWith(env, kind, "store", step, issued);
    return { env, issued };
  }

  async verifyRevoked(
    raw: string | Uint8Array,
    st: TrustState,
    clock: Date,
    stored = false,
  ): Promise<{ version: number; issued: Date; expires: Date; sha256: string; signed: Obj }> {
    const step = "list";
    const { env, issued } = await this.list(raw, "revoked");
    fields(env.signed, step, "revoked.json", ["version", "issued", "expires", "entries"]);
    const expires = parseTime("expires", env.signed.expires, step);
    if (stored) {
      return { version: int(env.signed, "version", step), issued, expires, sha256: await sha256Hex(raw), signed: env.signed };
    }
    if (!(expires > issued) || expires.getTime() - issued.getTime() > 48 * 3600 * 1000) {
      throw fail(CODE.signatureInvalid, step, "revoked.json claims more than 48 hours");
    }
    for (const e of arr(env.signed, "entries", step)) {
      const eo = obj(e, step, "an entry");
      fields(eo, step, "an entry", ["plugin_id", "versions", "action", "reason_code", "issued"], ["developer_key_id"]);
      const a = str(eo, "action", step);
      if (a !== "disable" && a !== "warn" && a !== "delist") throw fail(CODE.signatureInvalid, step, `action ${a}`);
      if (!str(eo, "plugin_id", step) || !str(eo, "versions", step))
        throw fail(CODE.signatureInvalid, step, "an entry names no plugin or versions");
    }
    const version = int(env.signed, "version", step);
    const sum = await sha256Hex(raw);
    if (version < st.revoked_version) throw fail(CODE.revocationRollback, step, `version ${version} is older than ${st.revoked_version}`);
    if (version === st.revoked_version && st.revoked_sha256 && st.revoked_sha256 !== sum) {
      throw fail(CODE.revocationRollback, step, `version ${version} differs from the copy seen before`);
    }
    if (homeNow(st, clock) > expires) throw fail(CODE.revocationExpired, step, `version ${version} expired`);
    return { version, issued, expires, sha256: sum, signed: env.signed };
  }

  async verifyRevokedHead(raw: string | Uint8Array): Promise<{ version: number; issued: Date; sha256: string }> {
    const step = "list";
    const { env, issued } = await this.list(raw, "revoked-head");
    fields(env.signed, step, "revoked-head.json", ["version", "issued", "sha256"]);
    return { version: int(env.signed, "version", step), issued, sha256: str(env.signed, "sha256", step) };
  }

  async verifyCatalog(raw: string | Uint8Array, apiMajor: number, st: TrustState, clock: Date): Promise<{ version: number; signed: Obj }> {
    const step = "list";
    const { env, issued } = await this.list(raw, "catalog");
    fields(env.signed, step, "the catalog", ["version", "issued", "expires", "api_major", "plugins"]);
    const expires = parseTime("expires", env.signed.expires, step);
    if (int(env.signed, "api_major", step) !== apiMajor) throw fail(CODE.signatureInvalid, step, "another plugin API major");
    if (!(expires > issued) || expires.getTime() - issued.getTime() > 7 * 24 * 3600 * 1000) {
      throw fail(CODE.signatureInvalid, step, "the catalog claims more than 7 days");
    }
    const version = int(env.signed, "version", step);
    const seen = st.catalog_versions?.[String(apiMajor)] ?? 0;
    if (version < seen) throw fail(CODE.catalogRollback, step, `version ${version} is older than ${seen}`);
    if (homeNow(st, clock) > expires) throw fail(CODE.catalogExpired, step, `version ${version} expired`);
    return { version, signed: env.signed };
  }

  async verifyEntitlement(raw: string | Uint8Array, st: TrustState, clock: Date): Promise<Obj> {
    const env = envelope(raw, "list");
    try {
      await this.verifyWith(env, "entitlement", "entitlement", "list", homeNow(st, clock));
    } catch (e) {
      if (e instanceof VerifyError && e.code === CODE.keyUseMismatch) throw e;
      throw fail(CODE.entitlementInvalid, "list", (e as Error).message);
    }
    return env.signed;
  }
}
