// Story 54.15 ACs on developers: MFA before submitting, re-auth for keys, another
// developer's plugin, the DNS prefix check and its monthly re-check (T04), versions.
import { describe, expect, it } from "vitest";
import { Signer } from "../../signing/sig";
import { checkPrefix, recheckPrefixes } from "../../web/developers";
import { E, account, ageFactor, buildPackage, call, developer, mailTo, submit } from "./helpers";

const doh = (txt: string[]) =>
  (async () =>
    new Response(JSON.stringify({ Status: 0, Answer: txt.map((t) => ({ type: 16, data: `"${t}"` })) }), {
      headers: { "Content-Type": "application/dns-json" },
    })) as unknown as typeof fetch;

describe("developers", () => {
  it("a developer without MFA cannot submit: 403 mfa_required", async () => {
    const a = await account();
    await call("POST", "/v1/developers", { cookie: a.cookie, body: { agreement_version: 1 } });
    const r = await call("POST", "/v1/submissions", { cookie: a.cookie, body: {} });
    expect(r).toMatchObject({ status: 403, body: { error: "mfa_required" } });
  });

  it("adding a key 10 minutes after the last factor is 401 reauth_required, then works", async () => {
    const dev = await developer();
    const k = await Signer.fromSeed(crypto.getRandomValues(new Uint8Array(32)));
    await ageFactor(dev.cookie, 10);
    expect((await call("POST", "/v1/developers/keys", { cookie: dev.cookie, body: { id: k.id, public: k.public } })).body.error).toBe(
      "reauth_required",
    );
    expect((await call("POST", "/v1/reauth", { cookie: dev.cookie, body: { code: await dev.totp(1) } })).status).toBe(200);
    expect((await call("POST", "/v1/developers/keys", { cookie: dev.cookie, body: { id: k.id, public: k.public } })).status).toBe(201);
    // an id that is not the key's fingerprint
    const bad = await call("POST", "/v1/developers/keys", { cookie: dev.cookie, body: { id: "0".repeat(32), public: k.public } });
    expect(bad.body.error).toBe("key_invalid");
  });

  it("developer A on developer B's plugin: 403, audited, nothing changed", async () => {
    const a = await developer();
    const b = await developer();
    const id = `${b.prefix}.mine`;
    await submit(b, id, "1.0.0", await buildPackage(id, "1.0.0", b.key));
    const before = await E.DB_CATALOG.prepare("SELECT status FROM plugins WHERE id = ?").bind(id).first<{ status: string }>();
    expect((await call("POST", `/v1/plugins/${id}/withdraw`, { cookie: a.cookie })).status).toBe(403);
    const sub = await E.DB_CATALOG.prepare("SELECT id FROM submissions WHERE plugin = ?").bind(id).first<{ id: string }>();
    expect((await call("GET", `/v1/submissions/${sub!.id}`, { cookie: a.cookie })).status).toBe(403);
    const after = await E.DB_CATALOG.prepare("SELECT status FROM plugins WHERE id = ?").bind(id).first<{ status: string }>();
    expect(after).toEqual(before);
    const audit = await E.DB_ACCOUNTS.prepare("SELECT count(*) AS n FROM audit WHERE actor = ? AND result = 'forbidden'")
      .bind(a.id)
      .first<{ n: number }>();
    expect(audit!.n).toBeGreaterThanOrEqual(2);
  });

  it("a submission outside the developer's prefixes, or of a version that exists, is refused", async () => {
    const dev = await developer();
    const pkg = await buildPackage("dev.someone.else", "1.0.0", dev.key);
    const r = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: "dev.someone.else", version: "1.0.0", package_sha256: "0".repeat(64), package_size: pkg.length, source_size: 10 },
    });
    expect(r.body.error).toBe("prefix_unverified");
    const id = `${dev.prefix}.twice`;
    await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key));
    const again = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: id, version: "1.0.0", package_sha256: "0".repeat(64), package_size: 10, source_size: 10 },
    });
    expect(again).toMatchObject({ status: 409, body: { error: "version_exists" } });
    const noSource = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: id, version: "1.0.1", package_sha256: "0".repeat(64), package_size: 10, source_size: 0 },
    });
    expect(noSource.body.error).toBe("source_required");
  });

  it("a revoked key leaves no key to submit with", async () => {
    const dev = await developer();
    expect((await call("DELETE", `/v1/developers/keys/${dev.key.id}`, { cookie: dev.cookie })).status).toBe(200);
    const r = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: `${dev.prefix}.x`, version: "1.0.0", package_sha256: "0".repeat(64), package_size: 10, source_size: 10 },
    });
    expect(r.body.error).toBe("key_required");
  });

  it("T04: a DNS prefix verifies by its TXT record; when the record goes, submissions under it stop and the developer is told", async () => {
    const dev = await developer();
    const domain = `ex${Date.now()}.dev`;
    const prefix = domain.split(".").reverse().join(".");
    const claim = await call("POST", "/v1/developers/prefixes", { cookie: dev.cookie, body: { prefix } });
    expect(claim.body).toMatchObject({ domain, verified: false });
    const txt = claim.body.txt as string;
    expect(await checkPrefix(E, prefix, doh(["v=spf1 -all"]))).toBe(false);
    expect(await checkPrefix(E, prefix, doh([txt]))).toBe(true);
    const id = `${prefix}.thing`;
    await submit(dev, id, "1.0.0", await buildPackage(id, "1.0.0", dev.key));
    await recheckPrefixes(E, doh([]));
    expect(await mailTo(dev.email, "prefix_lapsed")).not.toBeNull();
    const r = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: id, version: "1.0.1", package_sha256: "0".repeat(64), package_size: 10, source_size: 10 },
    });
    expect(r.body.error).toBe("prefix_unverified");
    // listed plugins stay: the plugin's row is untouched
    expect((await E.DB_CATALOG.prepare("SELECT status FROM plugins WHERE id = ?").bind(id).first<{ status: string }>())!.status).not.toBe(
      "removed",
    );
  });
});
