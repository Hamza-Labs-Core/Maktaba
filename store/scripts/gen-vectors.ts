// Writes the shared signature vectors (story 54.14 T05, T07) that the TypeScript signer
// makes and the Go, Rust and TypeScript verifiers all check:
//
//   shared/plugins/testdata/signatures/canonical.json   canonical JSON in → out (or refused)
//   shared/plugins/testdata/signatures/vectors.json     keys, roots and one case per rule
//
// Every key here is a TEST key made from a public label (sha256 of "maktaba-plugins TEST
// KEY ..." + the label): anyone can recompute it, so nothing it signs is trusted by a real
// build, whose root (shared/plugins/plugins-root.json) never lists one.
//
//   npm run vectors      write the files
//   npm run vectors:check   fail when they differ from what this script makes (CI)

import { readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { canonicalStringOf } from "../signing/canonical";
import { Signer, capabilitiesSHA256, filesSHA256, sha256Hex, signEnvelope, signFile, toB64 } from "../signing/sig";

const HERE = dirname(fileURLToPath(import.meta.url));
const OUT = resolve(HERE, "../../shared/plugins/testdata/signatures");
const enc = new TextEncoder();

export async function testSigner(label: string): Promise<Signer> {
  const seed = new Uint8Array(
    await crypto.subtle.digest("SHA-256", enc.encode(`maktaba-plugins TEST KEY, never for production: ${label}`)),
  );
  return Signer.fromSeed(seed);
}

const NOW = "2026-11-06T12:00:00Z";
const at = (offsetHours: number) => new Date(Date.parse(NOW) + offsetHours * 3600_000).toISOString().replace(/\.\d{3}Z$/, "Z");

const CANONICAL: { name: string; input: string; output?: string }[] = [
  {
    name: "sorted keys, no spaces",
    input: '{ "b": 1, "a": [true, false, null], "c": {"z": "x", "y": -2} }',
    output: '{"a":[true,false,null],"b":1,"c":{"y":-2,"z":"x"}}',
  },
  { name: "keys by UTF-8 bytes", input: '{"é":1,"z":2,"Z":3,"":4,"😀":5}', output: '{"Z":3,"z":2,"é":1,"":4,"😀":5}' },
  {
    name: "escapes only what must be",
    input: '"a\\"b\\\\c\\/d\\u0001\\u001f\\t\\n\\r\\b\\f <>&  é"',
    output: '"a\\"b\\\\c/d\\u0001\\u001f\\t\\n\\r\\b\\f <>&  é"',
  },
  { name: "unicode escapes are written as characters", input: '"\\u00e9\\u0645\\u0643"', output: '"éمك"' },
  { name: "the largest safe integer", input: "[9007199254740991,-9007199254740991,0]", output: "[9007199254740991,-9007199254740991,0]" },
  { name: "a fraction is refused", input: "[1.5]" },
  { name: "an exponent is refused", input: "[1e3]" },
  { name: "past the safe range is refused", input: "[9007199254740992]" },
  { name: "minus zero is refused", input: "[-0]" },
  { name: "a key twice is refused", input: '{"a":1,"a":2}' },
  { name: "a surrogate escape is refused", input: '"\\ud83d\\ude00"' },
  { name: "a lone surrogate escape is refused", input: '"\\ud800"' },
  { name: "data after the value is refused", input: '{"a":1} {}' },
  { name: "a raw control character is refused", input: '"a\u0001b"' },
  { name: "empty containers", input: '{"a":{},"b":[]}', output: '{"a":{},"b":[]}' },
];

const MANIFEST = `schema = 1
id = "dev.example.signed"
version = "1.0.0"
plugin_api = "1.0"
maktaba = ">=0.6.0, <0.9.0"
licence = "MIT"

[strings]
name = "plugin.name"
summary = "plugin.summary"

[capabilities]
required = ["library.read"]

[[stage]]
name = "tag"
subject = "video"
export = "run-tag"
version = 1
`;
const CAPS = { optional: [], required: ["library.read"] };

async function filesJSON(files: Record<string, Uint8Array>): Promise<string> {
  const list = [];
  for (const p of Object.keys(files).sort()) list.push({ path: p, sha256: await sha256Hex(files[p]!), size: files[p]!.length });
  return canonicalStringOf(list);
}

function b64files(files: Record<string, Uint8Array>): Record<string, string> {
  return Object.fromEntries(Object.entries(files).map(([k, v]) => [k, toB64(v)]));
}

export async function build(): Promise<{ canonical: string; vectors: string }> {
  const k = {
    root1: await testSigner("root-1"),
    root2: await testSigner("root-2"),
    root3: await testSigner("root-3"),
    rootX: await testSigner("not-a-root"),
    store1: await testSigner("store-1"),
    store2: await testSigner("store-2"),
    ent1: await testSigner("entitlement-1"),
    dev1: await testSigner("developer-1"),
    dev2: await testSigner("developer-2"),
  };
  const keys = Object.fromEntries(Object.entries(k).map(([n, s]) => [n, { id: s.id, public: s.public }]));
  const rootSigned = (version: number, ...rs: Signer[]) => ({
    version,
    threshold: 1,
    keys: rs.map((r) => ({ id: r.id, public: r.public })),
  });
  const root = await signEnvelope("root", rootSigned(1, k.root1, k.root2), k.root1, k.root2);
  const roots = {
    v1: root,
    v2_chained: await signEnvelope("root", rootSigned(2, k.root1, k.root3), k.root2, k.root1, k.root3),
    v2_unchained: await signEnvelope("root", rootSigned(2, k.root3), k.root3),
    v3_skips: await signEnvelope("root", rootSigned(3, k.root1, k.root3), k.root1, k.root3),
    empty: canonicalStringOf({ signatures: [], signed: { keys: [], threshold: 1, version: 0 } }),
  };
  const sk = (s: Signer, use: string, nb = at(-24 * 30), na = at(24 * 365)) => ({
    id: s.id,
    public: s.public,
    use,
    not_before: nb,
    not_after: na,
  });
  const storeKeysSigned = (version: number, expires: string, list: object[]) => ({ version, expires, keys: list });
  const v3 = storeKeysSigned(3, at(24 * 300), [sk(k.store1, "store"), sk(k.ent1, "entitlement")]);
  const storeKeys = {
    v3: await signEnvelope("store-keys", v3, k.root1),
    v3_by_root2: await signEnvelope("store-keys", v3, k.root2),
    v3_other: await signEnvelope("store-keys", storeKeysSigned(3, at(24 * 300), [sk(k.store2, "store")]), k.root1),
    v4_rotated: await signEnvelope(
      "store-keys",
      storeKeysSigned(4, at(24 * 300), [sk(k.store2, "store"), sk(k.ent1, "entitlement")]),
      k.root1,
    ),
    bad_root: await signEnvelope("store-keys", v3, k.rootX),
    expired: await signEnvelope("store-keys", storeKeysSigned(5, at(-1), [sk(k.store1, "store")]), k.root1),
    short_window: await signEnvelope("store-keys", storeKeysSigned(3, at(24 * 300), [sk(k.store1, "store", at(-48), at(-24))]), k.root1),
  };

  // ---- packages -------------------------------------------------------------------------
  const files: Record<string, Uint8Array> = {
    "manifest.toml": enc.encode(MANIFEST),
    "plugin.wasm": new Uint8Array([0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00, 0x42, 0x42, 0x42]),
    "i18n/en.json": enc.encode('{"plugin.name":"Signed","plugin.summary":"A signed plugin."}\n'),
    "i18n/ar.json": enc.encode('{"plugin.name":"موقّع","plugin.summary":"إضافة موقّعة."}\n'),
  };
  const fj = await filesJSON(files);
  const fsum = await filesSHA256(fj);
  const caps = await capabilitiesSHA256(CAPS);
  const pid = "dev.example.signed";
  const ver = "1.0.0";
  const dev = (s: Signer, json = fj, id = pid, v = ver) =>
    filesSHA256(json).then((sum) => signFile("developer", s, { plugin_id: id, version: v, files_sha256: sum }));
  const storeSigned = (over: Record<string, unknown> = {}) => ({
    plugin_id: pid,
    version: ver,
    files_sha256: fsum,
    developer_key_id: k.dev1.id,
    review_level: "reviewed",
    reviewed_at: at(-2),
    capabilities_sha256: caps,
    ...over,
  });
  const st = (s: Signer, over: Record<string, unknown> = {}) => signFile("store", s, storeSigned(over));
  const dev1sig = await dev(k.dev1);
  const store1sig = await st(k.store1);
  const tamperedWasm = { ...files, "plugin.wasm": new Uint8Array([...files["plugin.wasm"]!.slice(0, -1), 0x43]) };
  const bigger = JSON.parse(fj) as { path: string; sha256: string; size: number }[];
  bigger[1]!.size += 1;
  const tamperedFJ = canonicalStringOf(bigger);
  const flip = (sigFile: string) => {
    const o = JSON.parse(sigFile) as { sig: string };
    const raw = Uint8Array.from(atob(o.sig), (c) => c.charCodeAt(0));
    raw[0] = raw[0]! ^ 1;
    o.sig = toB64(raw);
    return canonicalStringOf(o);
  };
  const otherPublic = (sigFile: string) => {
    const o = JSON.parse(sigFile) as { public: string };
    o.public = k.dev2.public;
    return canonicalStringOf(o);
  };
  const withField = (sigFile: string) => {
    const o = JSON.parse(sigFile) as { signed: Record<string, unknown> };
    o.signed.extra = "x";
    return canonicalStringOf(o);
  };
  // the store's key signing the store's object as the developer kind: valid bytes, wrong kind
  const crossKind = async () => {
    const text = canonicalStringOf(storeSigned());
    return canonicalStringOf({ key_id: k.store1.id, sig: await k.store1.sign("developer", text), signed: JSON.parse(text) });
  };

  type Pkg = {
    files: Record<string, string>;
    files_json: string;
    developer_sig: string | null;
    store_sig: string | null;
    plugin_id: string;
    version: string;
    capabilities_sha256: string;
    store_keys: string | null;
  };
  const pkg = (over: Partial<Pkg> & { rawFiles?: Record<string, Uint8Array> } = {}): Pkg => {
    const { rawFiles, ...rest } = over;
    return {
      files: b64files(rawFiles ?? files),
      files_json: fj,
      developer_sig: dev1sig,
      store_sig: store1sig,
      plugin_id: pid,
      version: ver,
      capabilities_sha256: caps,
      store_keys: "v3",
      ...rest,
    };
  };
  const ok = (level: string) => ({ ok: true, level });
  const no = (code: string, step: string) => ({ ok: false, code, step });
  const packageCases = [
    { name: "developer and store, reviewed", package: pkg(), expect: { ...ok("reviewed"), developer_key_id: k.dev1.id } },
    {
      name: "developer and store, automated",
      package: pkg({ store_sig: await st(k.store1, { review_level: "automated" }) }),
      expect: ok("automated"),
    },
    { name: "developer only", package: pkg({ store_sig: null }), expect: ok("developer") },
    { name: "no signature", package: pkg({ store_sig: null, developer_sig: null }), expect: ok("none") },
    { name: "one byte of plugin.wasm changed", package: pkg({ rawFiles: tamperedWasm }), expect: no("plugin_signature_invalid", "digest") },
    {
      name: "files.json changed under the signatures",
      package: pkg({ files_json: tamperedFJ }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "files.json changed, developer only",
      package: pkg({ files_json: tamperedFJ, store_sig: null }),
      expect: no("plugin_signature_invalid", "developer_sig"),
    },
    {
      name: "store.sig copied onto another developer's package",
      package: pkg({ developer_sig: await dev(k.dev2) }),
      expect: no("developer_key_mismatch", "developer_sig"),
    },
    {
      name: "store.sig by a key not in store-keys.json",
      package: pkg({ store_sig: await st(k.store2) }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    { name: "store.sig by an entitlement key", package: pkg({ store_sig: await st(k.ent1) }), expect: no("key_use_mismatch", "store_sig") },
    {
      name: "reviewed_at outside the key's window",
      package: pkg({ store_sig: await st(k.store1, { reviewed_at: at(-24 * 400) }) }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "the store key's window has ended, signed inside it",
      package: pkg({ store_keys: "short_window", store_sig: await st(k.store1, { reviewed_at: at(-30) }) }),
      expect: ok("reviewed"),
    },
    {
      name: "a bad developer signature",
      package: pkg({ developer_sig: flip(dev1sig) }),
      expect: no("plugin_signature_invalid", "developer_sig"),
    },
    { name: "a bad store signature", package: pkg({ store_sig: flip(store1sig) }), expect: no("plugin_signature_invalid", "store_sig") },
    {
      name: "developer.sig's public key is not its key id",
      package: pkg({ developer_sig: otherPublic(dev1sig), store_sig: null }),
      expect: no("plugin_signature_invalid", "developer_sig"),
    },
    {
      name: "store.sig without developer.sig",
      package: pkg({ developer_sig: null }),
      expect: no("plugin_signature_invalid", "developer_sig"),
    },
    { name: "store.sig and no store-keys.json", package: pkg({ store_keys: null }), expect: no("plugin_signature_invalid", "store_keys") },
    {
      name: "store.sig for another plugin",
      package: pkg({ plugin_id: "dev.example.other", developer_sig: await dev(k.dev1, fj, "dev.example.other") }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "capabilities differ from those reviewed",
      package: pkg({ capabilities_sha256: await capabilitiesSHA256({ optional: [], required: ["library.read", "media.read"] }) }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "an unknown field in store.sig",
      package: pkg({ store_sig: withField(store1sig) }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "a store key's signature made for another kind",
      package: pkg({ store_sig: await crossKind() }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "a bad review level",
      package: pkg({ store_sig: await st(k.store1, { review_level: "developer" }) }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "rotation: the old key under the new store-keys.json",
      package: pkg({ store_keys: "v4_rotated" }),
      expect: no("plugin_signature_invalid", "store_sig"),
    },
    {
      name: "rotation: re-signed with the new key",
      package: pkg({ store_keys: "v4_rotated", store_sig: await st(k.store2) }),
      expect: ok("reviewed"),
    },
  ];

  const state = (o: Record<string, unknown> = {}) => ({
    store_keys_version: 0,
    revoked_version: 0,
    newest_issued: "0001-01-01T00:00:00Z",
    ...o,
  });
  const storeKeysCases = [
    { name: "signed by root 1", raw: "v3", state: state(), now: NOW, expect: { ok: true, version: 3 } },
    { name: "signed by root 2 (threshold 1)", raw: "v3_by_root2", state: state(), now: NOW, expect: { ok: true, version: 3 } },
    {
      name: "signed by a key not in the root",
      raw: "bad_root",
      state: state(),
      now: NOW,
      expect: no("plugin_signature_invalid", "store_keys"),
    },
    { name: "expired", raw: "expired", state: state(), now: NOW, expect: no("store_keys_expired", "store_keys") },
    {
      name: "older than the newest seen",
      raw: "v3",
      state: state({ store_keys_version: 4 }),
      now: NOW,
      expect: no("store_keys_rollback_refused", "store_keys"),
    },
    {
      name: "the same version with other content",
      raw: "v3_other",
      state: state({ store_keys_version: 3, store_keys_sha256: await sha256Hex(canonicalStringOf(v3)) }),
      now: NOW,
      expect: no("store_keys_rollback_refused", "store_keys"),
    },
    {
      name: "the same version seen before",
      raw: "v3",
      state: state({ store_keys_version: 3, store_keys_sha256: await sha256Hex(canonicalStringOf(v3)) }),
      now: NOW,
      expect: { ok: true, version: 3 },
    },
    {
      name: "a clock in the past does not make it fresh",
      raw: "expired",
      state: state({ newest_issued: at(-0.5) }),
      now: at(-24 * 30),
      expect: no("store_keys_expired", "store_keys"),
    },
    {
      name: "rotation: the new list",
      raw: "v4_rotated",
      state: state({ store_keys_version: 3 }),
      now: NOW,
      expect: { ok: true, version: 4 },
    },
  ];

  // ---- the revocation list, its head and the catalog ------------------------------------
  const entry = (o: Record<string, unknown> = {}) => ({
    plugin_id: pid,
    versions: "<1.3.0",
    action: "disable",
    reason_code: "malware",
    issued: at(-1),
    ...o,
  });
  const revokedSigned = (version: number, issued: string, expires: string, entries = [entry()]) => ({ version, issued, expires, entries });
  const r120 = await signEnvelope("revoked", revokedSigned(120, at(-1), at(47)), k.store1);
  const revoked = {
    v120: r120,
    v119: await signEnvelope("revoked", revokedSigned(119, at(-2), at(46)), k.store1),
    v120_other: await signEnvelope("revoked", revokedSigned(120, at(-1), at(47), [entry({ action: "warn" })]), k.store1),
    v121: await signEnvelope(
      "revoked",
      revokedSigned(121, at(-0.5), at(47.5), [
        entry(),
        entry({ plugin_id: "dev.example.other", action: "warn", developer_key_id: k.dev2.id }),
      ]),
      k.store1,
    ),
    expired: await signEnvelope("revoked", revokedSigned(122, at(-24 * 5), at(-24 * 3)), k.store1),
    too_long: await signEnvelope("revoked", revokedSigned(123, at(-1), at(48)), k.store1),
    by_entitlement_key: await signEnvelope("revoked", revokedSigned(124, at(-1), at(47)), k.ent1),
    by_unlisted_key: await signEnvelope("revoked", revokedSigned(124, at(-1), at(47)), k.store2),
    as_catalog_kind: canonicalStringOf({
      signed: revokedSigned(124, at(-1), at(47)),
      signatures: [{ key_id: k.store1.id, sig: await k.store1.sign("catalog", canonicalStringOf(revokedSigned(124, at(-1), at(47)))) }],
    }),
    bad_action: await signEnvelope("revoked", revokedSigned(125, at(-1), at(47), [entry({ action: "delete" })]), k.store1),
  };
  const sha = async (s: string) => sha256Hex(s);
  const revokedCases = [
    { name: "a fresh list", raw: "v120", state: state(), now: NOW, expect: { ok: true, version: 120 } },
    {
      name: "older than the newest seen",
      raw: "v119",
      state: state({ revoked_version: 120, revoked_sha256: await sha(r120) }),
      now: NOW,
      expect: no("revocation_rollback_refused", "list"),
    },
    {
      name: "the same version with other entries",
      raw: "v120_other",
      state: state({ revoked_version: 120, revoked_sha256: await sha(r120) }),
      now: NOW,
      expect: no("revocation_rollback_refused", "list"),
    },
    {
      name: "the same list again",
      raw: "v120",
      state: state({ revoked_version: 120, revoked_sha256: await sha(r120) }),
      now: NOW,
      expect: { ok: true, version: 120 },
    },
    { name: "newer", raw: "v121", state: state({ revoked_version: 120 }), now: NOW, expect: { ok: true, version: 121 } },
    { name: "expired three days ago", raw: "expired", state: state(), now: NOW, expect: no("revocation_expired", "list") },
    {
      name: "a clock in the past does not make an expired list fresh",
      raw: "expired",
      state: state({ newest_issued: at(-1) }),
      now: at(-24 * 4),
      expect: no("revocation_expired", "list"),
    },
    { name: "claims more than 48 hours", raw: "too_long", state: state(), now: NOW, expect: no("plugin_signature_invalid", "list") },
    { name: "signed by an entitlement key", raw: "by_entitlement_key", state: state(), now: NOW, expect: no("key_use_mismatch", "list") },
    {
      name: "signed by a key not listed",
      raw: "by_unlisted_key",
      state: state(),
      now: NOW,
      expect: no("plugin_signature_invalid", "list"),
    },
    {
      name: "a signature made for a catalog",
      raw: "as_catalog_kind",
      state: state(),
      now: NOW,
      expect: no("plugin_signature_invalid", "list"),
    },
    { name: "an unknown action", raw: "bad_action", state: state(), now: NOW, expect: no("plugin_signature_invalid", "list") },
  ];
  const stored = [
    { name: "the embedded list is newer", stored: "v120", embedded: "v121", expect_version: 121 },
    { name: "the stored list is newer", stored: "v121", embedded: "v119", expect_version: 121 },
    { name: "only the embedded list", stored: null, embedded: "v119", expect_version: 119 },
    {
      name: "an expired stored list still loads, and says so",
      stored: "expired",
      embedded: null,
      expect_version: 122,
      expect_expired: true,
    },
  ];

  const heads = {
    v121: await signEnvelope("revoked-head", { version: 121, issued: at(-0.5), sha256: await sha(revoked.v121) }, k.store1),
    v121_wrong_sha: await signEnvelope("revoked-head", { version: 121, issued: at(-0.5), sha256: await sha(revoked.v120) }, k.store1),
    unsigned: canonicalStringOf({ signed: { version: 121, issued: at(-0.5), sha256: await sha(revoked.v121) }, signatures: [] }),
  };
  const headCases = [
    { name: "the head names the list", raw: "v121", list: "v121", expect: { ok: true, matches: true } },
    { name: "the head names another list", raw: "v121_wrong_sha", list: "v121", expect: { ok: true, matches: false } },
    { name: "an unsigned head", raw: "unsigned", list: "v121", expect: no("plugin_signature_invalid", "list") },
  ];

  const catalogPlugin = {
    id: pid,
    developer: "Example",
    names: { en: "Signed", ar: "موقّع" },
    summaries: { en: "A signed plugin.", ar: "إضافة موقّعة." },
    icon: "",
    category: "metadata",
    tags: ["tags"],
    rating_avg_x100: 0,
    rating_count: 0,
    installs: 0,
    price: "free",
    machine_translated: [],
    versions: [
      {
        version: ver,
        sha256: "0".repeat(64),
        size: 1234,
        url: "https://plugins-cdn.maktaba.tv/" + "0".repeat(64) + ".mkplugin",
        maktaba: ">=0.6.0, <0.9.0",
        plugin_api: "1.0",
        platforms: ["any"],
        capabilities: ["library.read"],
        review_level: "reviewed",
        released: at(-2),
        files_sha256: fsum,
      },
    ],
  };
  const catalogSigned = (version: number, issued: string, expires: string, apiMajor = 1) => ({
    version,
    issued,
    expires,
    api_major: apiMajor,
    plugins: [catalogPlugin],
  });
  const catalogs = {
    v10: await signEnvelope("catalog", catalogSigned(10, at(-1), at(24 * 7 - 1)), k.store1),
    v9: await signEnvelope("catalog", catalogSigned(9, at(-2), at(24 * 6)), k.store1),
    v10_other: await signEnvelope("catalog", catalogSigned(10, at(-2), at(24 * 6)), k.store1),
    expired: await signEnvelope("catalog", catalogSigned(11, at(-24 * 9), at(-24 * 2)), k.store1),
    major2: await signEnvelope("catalog", catalogSigned(12, at(-1), at(24 * 6), 2), k.store1),
    too_long: await signEnvelope("catalog", catalogSigned(13, at(-1), at(24 * 7)), k.store1),
  };
  const catalogCases = [
    { name: "a fresh catalog", raw: "v10", api_major: 1, state: state(), now: NOW, expect: { ok: true, version: 10 } },
    {
      name: "older than the newest seen",
      raw: "v9",
      api_major: 1,
      state: state({ catalog_versions: { "1": 10 } }),
      now: NOW,
      expect: no("catalog_rollback_refused", "list"),
    },
    {
      name: "the newest seen version with other content",
      raw: "v10_other",
      api_major: 1,
      state: state({ catalog_versions: { "1": 10 }, catalog_sha256: { "1": await sha(catalogs.v10) } }),
      now: NOW,
      expect: no("catalog_rollback_refused", "list"),
    },
    {
      name: "the newest seen version again, the same bytes",
      raw: "v10",
      api_major: 1,
      state: state({ catalog_versions: { "1": 10 }, catalog_sha256: { "1": await sha(catalogs.v10) } }),
      now: NOW,
      expect: { ok: true, version: 10 },
    },
    { name: "expired", raw: "expired", api_major: 1, state: state(), now: NOW, expect: no("catalog_expired", "list") },
    {
      name: "another plugin API major",
      raw: "major2",
      api_major: 1,
      state: state(),
      now: NOW,
      expect: no("plugin_signature_invalid", "list"),
    },
    {
      name: "claims more than 7 days",
      raw: "too_long",
      api_major: 1,
      state: state(),
      now: NOW,
      expect: no("plugin_signature_invalid", "list"),
    },
  ];

  const ent = { plugin_id: pid, home_id: "h_test", valid_until: at(24 * 31) };
  const entitlementCases = [
    { name: "signed by the entitlement key", raw: await signEnvelope("entitlement", ent, k.ent1), expect: { ok: true } },
    { name: "signed by a store key", raw: await signEnvelope("entitlement", ent, k.store1), expect: no("key_use_mismatch", "list") },
  ];
  const rootCases = [
    { name: "a new root signed by the old root's key", old: "v1", next: "v2_chained", expect: { ok: true, version: 2 } },
    { name: "a new root signed only by its own keys", old: "v1", next: "v2_unchained", expect: no("plugin_signature_invalid", "root") },
    { name: "a root that skips a version", old: "v1", next: "v3_skips", expect: no("plugin_signature_invalid", "root") },
  ];

  const vectors = {
    _comment:
      "Story 54.14 T05: made by store/scripts/gen-vectors.ts (npm run vectors), verified by Go (shared/plugins/pluginsig), Rust (plugind/crates/sig) and TypeScript (store/signing). Every key is a TEST key from a public label; no real build trusts one.",
    now: NOW,
    keys,
    roots,
    store_keys: storeKeys,
    package_cases: packageCases,
    store_keys_cases: storeKeysCases,
    revoked,
    revoked_cases: revokedCases,
    revoked_stored_cases: stored,
    heads,
    head_cases: headCases,
    catalogs,
    catalog_cases: catalogCases,
    entitlement_cases: entitlementCases,
    root_cases: rootCases,
  };
  const canonicalCases = CANONICAL.map((c) => ({
    name: c.name,
    input: c.input,
    ...(c.output !== undefined ? { output: c.output } : { refused: true }),
  }));
  return {
    canonical:
      JSON.stringify(
        { _comment: "Story 54.14 T07: canonical JSON, the same bytes in Go, Rust and TypeScript.", cases: canonicalCases },
        null,
        2,
      ) + "\n",
    vectors: JSON.stringify(vectors, null, 2) + "\n",
  };
}

const isMain = process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url);
if (isMain) {
  const check = process.argv.includes("--check");
  const out = await build();
  let stale = false;
  for (const [name, text] of [
    ["canonical.json", out.canonical],
    ["vectors.json", out.vectors],
  ] as const) {
    const path = resolve(OUT, name);
    if (check) {
      let cur = "";
      try {
        cur = readFileSync(path, "utf8");
      } catch {
        /* missing */
      }
      if (cur !== text) {
        console.error(`${path} is stale: run npm run vectors in store/`);
        stale = true;
      }
    } else {
      writeFileSync(path, text);
      console.log(`wrote ${path}`);
    }
  }
  if (stale) process.exit(1);
}
