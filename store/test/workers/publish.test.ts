// Story 54.15 T02 (with 54.16's checks stubbed: the test approves directly) and 54.14 on the
// store's side: a submission is countersigned, the package on the CDN and the signed lists
// verify by the home's rules (the TypeScript verifier, which agrees with Go's and Rust's on
// the shared vectors), and a revocation reaches the list and its head.
import { env } from "cloudflare:workers";
import { describe, expect, it } from "vitest";
import { Root, emptyState } from "../../signing/verify";
import { capabilitiesSHA256 } from "../../signing/sig";
import { countersign } from "../../web/publish";
import { inspect } from "../../web/wasm";
import { E, account, addPasskey, buildPackage, call, developer, grant, submit } from "./helpers";

const writer = () => E.CATALOG_WRITER.get(E.CATALOG_WRITER.idFromName("main"));
const keys = async () => (await Root.parse(E.PLUGINS_ROOT)).verifyStoreKeys(E.TEST_STORE_KEYS, emptyState(), new Date());

describe("submit, countersign, publish", () => {
  it("an approved version is on the CDN, countersigned, and in a catalog that verifies", async () => {
    const dev = await developer();
    const id = `${dev.prefix}.tagger`;
    const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key, { capabilities: { optional: ["tags.write"] } }));
    expect((await call("GET", `/v1/submissions/${sid}`, { cookie: dev.cookie })).body.status).toBe("checking");
    const out = await countersign(env as never, sid, "reviewed");
    const obj = await E.BUCKET.get(`${out.sha256}.mkplugin`);
    expect(obj!.httpMetadata!.cacheControl).toContain("immutable");
    const pkg = await inspect(new Uint8Array(await obj!.arrayBuffer()));
    const k = await keys();
    const v = await k.verifyPackage({
      filesJSON: pkg.files_json,
      developerSig: pkg.developer_sig,
      storeSig: pkg.store_sig,
      pluginId: id,
      version: "1.0.0",
      capabilitiesSHA256: await capabilitiesSHA256(JSON.parse(pkg.manifest).capabilities),
    });
    expect(v).toMatchObject({ level: "reviewed", developerKeyId: dev.key.id });

    const r = await writer().regenerate();
    const cat = await k.verifyCatalog(await (await E.BUCKET.get("catalog-1.json"))!.text(), 1, emptyState(), new Date());
    expect(cat.version).toBe(r.catalog);
    const plugins = cat.signed.plugins as { id: string; versions: { sha256: string; review_level: string; url: string }[] }[];
    const p = plugins.find((x) => x.id === id)!;
    expect(p.versions[0]).toMatchObject({
      sha256: out.sha256,
      review_level: "reviewed",
      url: `https://plugins-cdn.maktaba.tv/${out.sha256}.mkplugin`,
    });
  });

  it("a withdrawn plugin leaves the catalog; its file stays", async () => {
    const dev = await developer();
    const id = `${dev.prefix}.gone`;
    const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key));
    const out = await countersign(env as never, sid, "automated");
    expect((await call("POST", `/v1/plugins/${id}/withdraw`, { cookie: dev.cookie })).status).toBe(200);
    await writer().regenerate();
    const k = await keys();
    const cat = await k.verifyCatalog(await (await E.BUCKET.get("catalog-1.json"))!.text(), 1, emptyState(), new Date());
    expect((cat.signed.plugins as { id: string }[]).some((x) => x.id === id)).toBe(false);
    expect(await E.BUCKET.head(`${out.sha256}.mkplugin`)).not.toBeNull();
  });

  it("a revocation reaches revoked.json and its head; an older list is refused by a home", async () => {
    const admin = await account();
    await grant(admin.id, "store_admin");
    const { cookie } = await addPasskey(admin.cookie);
    const k = await keys();
    await writer().regenerate();
    const before = await (await E.BUCKET.get("revoked.json"))!.text();
    const r = await call("POST", "/v1/admin/revocations", {
      cookie,
      body: { plugin_id: "dev.example.bad", versions: "<1.3.0", action: "disable", reason_code: "malware" },
    });
    expect(r.status).toBe(201);
    const text = await (await E.BUCKET.get("revoked.json"))!.text();
    const list = await k.verifyRevoked(text, emptyState(), new Date());
    expect(
      (list.signed.entries as { plugin_id: string; action: string }[]).some(
        (e) => e.plugin_id === "dev.example.bad" && e.action === "disable",
      ),
    ).toBe(true);
    const head = await k.verifyRevokedHead(await (await E.BUCKET.get("revoked-head.json"))!.text());
    expect(head.version).toBe(list.version);
    expect(head.sha256).toBe(list.sha256);
    expect(list.expires.getTime() - list.issued.getTime()).toBe(48 * 3600_000);
    // a home that has seen the newer list refuses the older one, validly signed (rollback)
    await expect(
      k.verifyRevoked(before, { ...emptyState(), revoked_version: list.version, revoked_sha256: list.sha256 }, new Date()),
    ).rejects.toMatchObject({
      code: "revocation_rollback_refused",
    });
  });

  it("a package signed by a key the developer did not register is not countersigned", async () => {
    const dev = await developer();
    const { Signer } = await import("../../signing/sig");
    const stranger = await Signer.fromSeed(crypto.getRandomValues(new Uint8Array(32)));
    const id = `${dev.prefix}.stranger`;
    const sid = await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", stranger));
    await expect(countersign(env as never, sid, "reviewed")).rejects.toThrow(/developer_key_inactive/);
  });

  it("the store will not publish with a key its store-keys.json does not list", async () => {
    const e = { ...(env as object), STORE_SIGNING_KEY: btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32)))) };
    const { regenerate } = await import("../../web/publish");
    await expect(regenerate(e as never)).rejects.toThrow(/store_key_unlisted/);
  });
});

describe("a restored database", () => {
  it("never publishes a list version below the one on the CDN (homes would refuse it as a rollback)", async () => {
    const first = await writer().regenerate();
    // the catalog database restored to an earlier time: its counters went back
    await E.DB_CATALOG.prepare("DELETE FROM lists").run();
    const next = await writer().regenerate();
    expect(next.revoked).toBeGreaterThan(first.revoked);
    expect(next.catalog).toBeGreaterThan(first.catalog);
  });
});
