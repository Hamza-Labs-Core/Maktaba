// Story 54.14 §5 and T06: the store online key rotation drill, with test keys, run by the
// `store` CI job. The store signs with key S1; S1 is "compromised"; the owner signs a new
// store-keys.json without it (root key); the store re-signs every live package with S2 in
// one batch; homes pick both up. The Go verifier then checks, as a home does
// (shared/plugins/pluginsig, TestRotationDrill with MAKTABA_STORE_DRILL=<dir>):
//
//   - store-keys v2 is accepted after v1, and v1 is refused once v2 was seen;
//   - every package re-signed with S2 verifies under v2;
//   - every package still carrying S1's store.sig is refused under v2;
//   - a revocation list S1 signed after the rotation is refused.
//
//   tsx scripts/drill-rotation.ts <out dir>

import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { canonicalStringOf } from "../signing/canonical";
import { capabilitiesSHA256, filesSHA256, sha256Hex, signEnvelope, signFile } from "../signing/sig";
import { testSigner } from "./gen-vectors";

const out = process.argv[2];
if (!out) {
  console.error("usage: drill-rotation.ts <out dir>");
  process.exit(2);
}
mkdirSync(out, { recursive: true });
const now = new Date();
const iso = (ms: number) => new Date(now.getTime() + ms).toISOString().replace(/\.\d{3}Z$/, "Z");
const H = 3600_000;
const D = 24 * H;

const root = await testSigner("drill-root");
const s1 = await testSigner("drill-store-1");
const s2 = await testSigner("drill-store-2");
const dev = await testSigner("drill-developer");
const key = (s: typeof s1) => ({ id: s.id, public: s.public, use: "store", not_before: iso(-30 * D), not_after: iso(365 * D) });

const write = (name: string, text: string) => writeFileSync(join(out, name), text);
write("root.json", await signEnvelope("root", { version: 1, threshold: 1, keys: [{ id: root.id, public: root.public }] }, root));
write("store-keys-v1.json", await signEnvelope("store-keys", { version: 1, expires: iso(300 * D), keys: [key(s1)] }, root));
// the owner, offline, drops S1 and lists S2
write("store-keys-v2.json", await signEnvelope("store-keys", { version: 2, expires: iso(300 * D), keys: [key(s2)] }, root));

const caps = await capabilitiesSHA256({ optional: [], required: [] });
const packages = [];
for (let i = 0; i < 5; i++) {
  const id = `dev.example.drill${i}`;
  const files = canonicalStringOf([{ path: "manifest.toml", sha256: await sha256Hex(`m${i}`), size: 2 }]);
  const sum = await filesSHA256(files);
  const developer = await signFile("developer", dev, { plugin_id: id, version: "1.0.0", files_sha256: sum });
  const signed = {
    plugin_id: id,
    version: "1.0.0",
    files_sha256: sum,
    developer_key_id: dev.id,
    review_level: "reviewed",
    reviewed_at: iso(-2 * H),
    capabilities_sha256: caps,
  };
  // the batch re-sign: the same signed object, S2's signature, the time of the re-sign
  const resigned = { ...signed, reviewed_at: iso(-60_000) };
  packages.push({
    plugin_id: id,
    version: "1.0.0",
    capabilities_sha256: caps,
    files_json: files,
    developer_sig: developer,
    store_sig_old: await signFile("store", s1, signed),
    store_sig_new: await signFile("store", s2, resigned),
  });
}
write("packages.json", JSON.stringify(packages, null, 2));
write(
  "revoked-by-old-key.json",
  await signEnvelope("revoked", { version: 9, issued: iso(-60_000), expires: iso(47 * H), entries: [] }, s1),
);
write(
  "revoked-by-new-key.json",
  await signEnvelope("revoked", { version: 9, issued: iso(-60_000), expires: iso(47 * H), entries: [] }, s2),
);
console.log(`drill files in ${out}`);
