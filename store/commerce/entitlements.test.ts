// T03 (store side): the signer makes the shared vectors' signatures byte for byte, so the
// home's Go verifier (api/internal/plugins/entitlements.go) checks what the store signs.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { type EntitlementBody, type Signer, signEntitlement } from "./entitlements.js";

const doc = JSON.parse(
  readFileSync(join(import.meta.dirname, "..", "..", "shared", "plugins", "testdata", "entitlements", "vectors.json"), "utf8")
) as {
  test_seeds: Record<string, string>;
  cases: { name: string; entitlement: { key_id: string; sig: string; signed: EntitlementBody }; want: string }[];
};

async function seedSigner(keyId: string, seedHex: string): Promise<Signer> {
  const pkcs8 = Buffer.concat([Buffer.from("302e020100300506032b657004220420", "hex"), Buffer.from(seedHex, "hex")]);
  const k = await crypto.subtle.importKey("pkcs8", pkcs8, { name: "Ed25519" }, false, ["sign"]);
  return { keyId, sign: async (m) => new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, k, m)) };
}

describe("entitlement vectors", () => {
  it("the signer reproduces every signed vector", async () => {
    let checked = 0;
    for (const c of doc.cases) {
      if (c.want === "entitlement_bad_signature" || c.want === "entitlement_unknown_key") continue;
      const kid = c.entitlement.key_id;
      const seed = doc.test_seeds[kid];
      if (!seed) continue;
      const got = await signEntitlement(await seedSigner(kid, seed), c.entitlement.signed);
      expect(got, c.name).toEqual(c.entitlement);
      checked++;
    }
    expect(checked).toBeGreaterThanOrEqual(5);
  });
});
