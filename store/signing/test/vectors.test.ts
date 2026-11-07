// Story 54.14 T05, T07, T08 (ts): the TypeScript verifier on the shared vectors, the same
// answers as Go and Rust.
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { canonical } from "../canonical";
import { fromB64, Signer, signEnvelope } from "../sig";
import { emptyState, Root, StoreKeys, VerifyError, type TrustState } from "../verify";

const DIR = resolve(__dirname, "../../../shared/plugins/testdata/signatures");
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const V: any = JSON.parse(readFileSync(resolve(DIR, "vectors.json"), "utf8"));
const C: { cases: { name: string; input: string; output?: string; refused?: boolean }[] } = JSON.parse(
  readFileSync(resolve(DIR, "canonical.json"), "utf8"),
);
const now = new Date(V.now);
const dec = new TextDecoder();

async function outcome<T>(f: () => Promise<T>): Promise<{ ok: true; v: T } | { ok: false; code: string; step: string }> {
  try {
    return { ok: true, v: await f() };
  } catch (e) {
    if (e instanceof VerifyError) return { ok: false, code: e.code, step: e.step };
    throw e;
  }
}

const state = (s: Record<string, unknown>): TrustState => ({ ...emptyState(), ...s }) as TrustState;

async function keysOf(name: string | null): Promise<StoreKeys | null> {
  if (!name) return null;
  const root = await Root.parse(V.roots.v1);
  return root.verifyStoreKeys(V.store_keys[name], emptyState(), now);
}

describe("canonical JSON", () => {
  for (const c of C.cases) {
    it(c.name, () => {
      if (c.refused) expect(() => canonical(c.input)).toThrow(/not canonical/);
      else expect(dec.decode(canonical(c.input))).toBe(c.output);
    });
  }
});

describe("packages", () => {
  for (const c of V.package_cases) {
    it(c.name, async () => {
      const keys = await keysOf(c.package.store_keys);
      const files = new Map<string, Uint8Array>(Object.entries(c.package.files as Record<string, string>).map(([k, v]) => [k, fromB64(v)]));
      const run = async () => {
        if (c.package.store_sig && !keys) {
          throw new VerifyError("plugin_signature_invalid", "store_keys", "no store-keys.json");
        }
        const k = keys ?? (await keysOf("v3"))!;
        return k.verifyPackage({
          filesJSON: c.package.files_json,
          developerSig: c.package.developer_sig,
          storeSig: c.package.store_sig,
          pluginId: c.package.plugin_id,
          version: c.package.version,
          capabilitiesSHA256: c.package.capabilities_sha256,
          files,
        });
      };
      const r = await outcome(run);
      if (c.expect.ok) {
        expect(r.ok, JSON.stringify(r)).toBe(true);
        if (r.ok) {
          expect(r.v.level).toBe(c.expect.level);
          if (c.expect.developer_key_id) expect(r.v.developerKeyId).toBe(c.expect.developer_key_id);
        }
      } else {
        expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
      }
    });
  }
});

describe("store-keys.json", () => {
  for (const c of V.store_keys_cases) {
    it(c.name, async () => {
      const root = await Root.parse(V.roots.v1);
      const r = await outcome(() => root.verifyStoreKeys(V.store_keys[c.raw], state(c.state), new Date(c.now)));
      if (c.expect.ok) expect(r.ok && r.v.version).toBe(c.expect.version);
      else expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
    });
  }
  it("an empty root trusts nothing", async () => {
    const root = await Root.parse(V.roots.empty);
    const r = await outcome(() => root.verifyStoreKeys(V.store_keys.v3, emptyState(), now));
    expect(r).toEqual({ ok: false, code: "plugin_signature_invalid", step: "root" });
  });
});

describe("the revocation list", () => {
  for (const c of V.revoked_cases) {
    it(c.name, async () => {
      const keys = (await keysOf("v3"))!;
      const r = await outcome(() => keys.verifyRevoked(V.revoked[c.raw], state(c.state), new Date(c.now)));
      if (c.expect.ok) expect(r.ok && r.v.version).toBe(c.expect.version);
      else expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
    });
  }
  for (const c of V.head_cases) {
    it("head: " + c.name, async () => {
      const keys = (await keysOf("v3"))!;
      const r = await outcome(() => keys.verifyRevokedHead(V.heads[c.raw]));
      if (!c.expect.ok) return expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
      const list = await keys.verifyRevoked(V.revoked[c.list], emptyState(), now);
      expect(r.ok && r.v.version === list.version && r.v.sha256 === list.sha256).toBe(c.expect.matches);
    });
  }
});

describe("the catalog", () => {
  for (const c of V.catalog_cases) {
    it(c.name, async () => {
      const keys = (await keysOf("v3"))!;
      const r = await outcome(() => keys.verifyCatalog(V.catalogs[c.raw], c.api_major, state(c.state), new Date(c.now)));
      if (c.expect.ok) expect(r.ok && r.v.version).toBe(c.expect.version);
      else expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
    });
  }
});

describe("entitlements (T08)", () => {
  for (const c of V.entitlement_cases) {
    it(c.name, async () => {
      const keys = (await keysOf("v3"))!;
      const r = await outcome(() => keys.verifyEntitlement(c.raw, emptyState(), now));
      if (c.expect.ok) expect(r.ok).toBe(true);
      else expect(r).toEqual({ ok: false, code: c.expect.code, step: c.expect.step });
    });
  }
});

describe("the signer", () => {
  it("signs deterministically, and the seed cannot be read back", async () => {
    const s = await Signer.fromSeed(new Uint8Array(32).fill(7));
    const a = await signEnvelope("catalog", { b: 1, a: 2 }, s);
    const b = await signEnvelope("catalog", { a: 2, b: 1 }, s);
    expect(a).toBe(b);
    expect(JSON.stringify(s)).not.toContain("BwcH");
  });
});
