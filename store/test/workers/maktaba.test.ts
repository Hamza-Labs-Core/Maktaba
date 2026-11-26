// Story 54.45 §2: Maktaba's own plugins in the store. One publisher account for Maktaba, set
// by a store admin, is the only one that may submit a tv.maktaba.* id (community prefixes
// aside); its native packages are read in the trusted mode, their shape checked, nothing run,
// and a person reviews each.
import { describe, expect, it } from "vitest";
import { prefixCovers } from "../../web/developers";
import { runChecks } from "../../web/checks";
import { countersign, resignPage } from "../../web/publish";
import { pack, inspect } from "../../web/wasm";
import { Signer, capabilitiesSHA256, signEnvelope, toB64 } from "../../signing/sig";
import { Root, emptyState } from "../../signing/verify";
import { type Dev, E, account, addPasskey, addTotp, ageFactor, call, developer, grant, submit } from "./helpers";

const enc = new TextEncoder();
const VERSION = "0.9.0";
const ID = "tv.maktaba.thumbnails";

/** The thumbnails plugin's manifest as a release stamps it (tools/plugins/build.py). */
function manifest(id = ID, version = VERSION): string {
  return `schema     = 1
id         = "${id}"
version    = "${version}"
plugin_api = "1.0"
maktaba    = "=${version}"
licence    = "AGPL-3.0-only"
runtime    = "native-python"

[strings]
name    = "fp.thumbnails.name"
summary = "fp.thumbnails.summary"

[trusted]
runner    = "files"
component = "media.thumbnails"
db.read   = ["videos"]
db.write  = ["video_thumbnails"]
files.read  = ["library"]
files.write = ["data.thumbnails"]
exec      = ["ffmpeg"]

[[stage]]
name        = "thumb_small"
subject     = "video"
export      = "thumb-small"
version     = 1
pool        = "cpu"
`;
}

function files(id = ID, version = VERSION, extra: Record<string, Uint8Array> = {}): Record<string, Uint8Array> {
  const dist = `maktaba_fp_${id.replace("tv.maktaba.", "").replace(/-/g, "_")}`;
  return {
    "manifest.toml": enc.encode(manifest(id, version)),
    "i18n/en.json": enc.encode('{"fp.thumbnails.name":"Thumbnails","fp.thumbnails.summary":"Makes the small pictures."}'),
    "i18n/ar.json": enc.encode('{"fp.thumbnails.name":"الصور المصغّرة","fp.thumbnails.summary":"تصنع الصور الصغيرة."}'),
    "statements.json": enc.encode("[]"),
    [`wheels/${dist}-${version}-py3-none-any.whl`]: enc.encode("PK the wheel's bytes"),
    ...extra,
  };
}

async function signed(dev: Dev, f: Record<string, Uint8Array>, id = ID, version = VERSION): Promise<Uint8Array> {
  const i = await inspect(await pack(f));
  const { filesSHA256, signFile } = await import("../../signing/sig");
  const sig = await signFile("developer", dev.key, { plugin_id: id, version, files_sha256: await filesSHA256(i.files_json) });
  return pack(f, { "signatures/developer.sig": sig });
}

async function storeAdmin(): Promise<string> {
  const a = await account();
  await grant(a.id, "store_admin");
  return (await addPasskey(a.cookie)).cookie;
}

// one publisher per store: made once, shared by the tests
let thePublisher: Promise<Dev> | undefined;
function publisher(): Promise<Dev> {
  thePublisher ??= (async () => {
    const dev = await developer();
    const r = await call("POST", "/v1/admin/maktaba-publisher", { cookie: await storeAdmin(), body: { account: dev.id } });
    if (r.status !== 200) throw new Error(`maktaba-publisher ${r.status} ${JSON.stringify(r.body)}`);
    return dev;
  })();
  return thePublisher;
}

/** Store admins set the publisher; it is one account. */
describe("the Maktaba publisher", () => {
  it("is set by a store admin with a passkey, for an active developer, and only one account holds it", async () => {
    const dev = await developer();
    // a developer cannot make itself the publisher
    expect((await call("POST", "/v1/admin/maktaba-publisher", { cookie: dev.cookie, body: { account: dev.id } })).status).toBe(403);
    // nor can a store admin name an account that is no developer
    const admin = await storeAdmin();
    const plain = await account();
    expect((await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: { account: plain.id } })).status).toBe(404);
    // the account set holds it, again idempotently; a second account is refused
    const owner = (await publisher()).id;
    const held = await E.DB_CATALOG.prepare("SELECT account, kind, verified_at FROM developer_prefixes WHERE prefix = 'tv.maktaba'").first<{
      account: string;
      kind: string;
      verified_at: string | null;
    }>();
    expect(held).toMatchObject({ account: owner, kind: "maktaba" });
    expect(held?.verified_at).toBeTruthy();
    expect((await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: { account: owner } })).status).toBe(200);
    const other = await developer();
    const second = await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: { account: other.id } });
    expect(second).toMatchObject({ status: 409, body: { error: "maktaba_publisher_taken" } });
    // the prefix can never be claimed: tv.maktaba is refused at a claim
    const claim = await call("POST", "/v1/developers/prefixes", { cookie: other.cookie, body: { prefix: "tv.maktaba" } });
    expect(claim.status).toBe(400);
  });

  it("handles concurrent setup cleanly with 409 maktaba_publisher_taken instead of 500 (#1458)", async () => {
    const owner = await publisher();
    const admin1 = await storeAdmin();
    const admin2 = await storeAdmin();
    const d1 = await developer();
    const d2 = await developer();
    // Temporarily clear tv.maktaba so both admins race on the initial insert.
    await E.DB_CATALOG.prepare("DELETE FROM developer_prefixes WHERE prefix = 'tv.maktaba'").run();
    try {
      const [r1, r2] = await Promise.all([
        call("POST", "/v1/admin/maktaba-publisher", { cookie: admin1, body: { account: d1.id } }),
        call("POST", "/v1/admin/maktaba-publisher", { cookie: admin2, body: { account: d2.id } }),
      ]);
      const statuses = [r1.status, r2.status].sort((a, b) => a - b);
      expect(statuses).toEqual([200, 409]);
      const loser = r1.status === 409 ? r1 : r2;
      expect(loser.body.error).toBe("maktaba_publisher_taken");
    } finally {
      await E.DB_CATALOG.prepare("DELETE FROM developer_prefixes WHERE prefix = 'tv.maktaba'").run();
      const restored = await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin1, body: { account: owner.id } });
      expect(restored.status).toBe(200);
    }
  });

  it("can be moved by a passkey-confirmed store admin with an audit row and plugin ownership transfer (#1458)", async () => {
    const owner = await publisher();
    const replacement = await developer();
    const admin = await storeAdmin();
    const staleAdmin = await storeAdmin();
    await ageFactor(staleAdmin, 6);

    // Ensure the current publisher already owns a tv.maktaba.* plugin row.
    const pluginId = "tv.maktaba.thumbnails";
    const v1 = `0.7.${(Date.now() % 1e6) + 1}`;
    await submit(owner, pluginId, v1, await signed(owner, files(pluginId, v1), pluginId, v1));

    // Role, re-auth, and target validation checks.
    expect(
      (await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: replacement.cookie, body: { account: replacement.id } })).status,
    ).toBe(403);
    expect(await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: staleAdmin, body: { account: replacement.id } })).toMatchObject(
      { status: 401, body: { error: "reauth_required" } },
    );
    const plain = await account();
    expect((await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: plain.id } })).status).toBe(404);

    try {
      const moved = await call("POST", "/v1/admin/maktaba-publisher/move", {
        cookie: admin,
        body: { account: replacement.id, reason: "key_compromised" },
      });
      expect(moved).toMatchObject({
        status: 200,
        body: { status: "moved", previous: owner.id, account: replacement.id, prefix: "tv.maktaba" },
      });

      const held = await E.DB_CATALOG.prepare(
        "SELECT account, kind, verified_at FROM developer_prefixes WHERE prefix = 'tv.maktaba'",
      ).first<{
        account: string;
        kind: string;
        verified_at: string | null;
      }>();
      expect(held).toMatchObject({ account: replacement.id, kind: "maktaba" });
      expect(held?.verified_at).toBeTruthy();

      const auditRow = await E.DB_ACCOUNTS.prepare(
        "SELECT action, target FROM audit WHERE action = 'maktaba_publisher.move' ORDER BY id DESC LIMIT 1",
      ).first<{ action: string; target: string }>();
      expect(auditRow).toEqual({
        action: "maktaba_publisher.move",
        target: `${owner.id}->${replacement.id}:key_compromised`,
      });

      // Moving to the same account is idempotent.
      expect(await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: replacement.id } })).toMatchObject({
        status: 200,
        body: { status: "moved", previous: replacement.id, account: replacement.id, prefix: "tv.maktaba" },
      });

      // The previous publisher can no longer submit tv.maktaba.* plugins.
      const denied = await call("POST", "/v1/submissions", {
        cookie: owner.cookie,
        body: {
          plugin_id: pluginId,
          version: `0.7.${(Date.now() % 1e6) + 2}`,
          package_sha256: "0".repeat(64),
          package_size: 10,
          source_size: 1,
        },
      });
      expect(denied).toMatchObject({ status: 403, body: { error: "prefix_unverified" } });

      // The new publisher owns the transferred plugin and can submit a new version.
      const v2 = `0.7.${(Date.now() % 1e6) + 3}`;
      const sid = await submit(replacement, pluginId, v2, await signed(replacement, files(pluginId, v2), pluginId, v2));
      expect(sid).toBeTruthy();
    } finally {
      // Restore tv.maktaba to the shared fixture publisher for subsequent tests.
      const back = await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: owner.id } });
      expect(back.status).toBe(200);
    }
  });

  it("validates missing account, unset prefix, TOTP-only admin, community plugins, concurrent same-account set, concurrent moves, and same-target repair", async () => {
    const owner = await publisher();
    const admin = await storeAdmin();
    const admin2 = await storeAdmin();
    const d1 = await developer();
    const d2 = await developer();

    // Missing account field and unknown account id.
    expect(await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: {} })).toMatchObject({
      status: 400,
      body: { error: "field_invalid" },
    });
    expect(await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: {} })).toMatchObject({
      status: 400,
      body: { error: "field_invalid" },
    });
    expect(
      await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: "missing-account-id" } }),
    ).toMatchObject({
      status: 404,
      body: { error: "not_found" },
    });

    // Fresh TOTP-only store_admin (no passkey) is refused with 403 passkey_required.
    const totpAdmin = await account();
    await grant(totpAdmin.id, "store_admin");
    await addTotp(totpAdmin.cookie);
    expect(await call("POST", "/v1/admin/maktaba-publisher", { cookie: totpAdmin.cookie, body: { account: d1.id } })).toMatchObject({
      status: 403,
      body: { error: "passkey_required" },
    });
    expect(await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: totpAdmin.cookie, body: { account: d1.id } })).toMatchObject({
      status: 403,
      body: { error: "passkey_required" },
    });

    // Unset tv.maktaba prefix: move returns 404, and concurrent same-account setup succeeds with one audit row.
    await E.DB_CATALOG.prepare("DELETE FROM developer_prefixes WHERE prefix = 'tv.maktaba'").run();
    try {
      expect(await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: d1.id } })).toMatchObject({
        status: 404,
        body: { error: "not_found" },
      });

      const auditBefore = (await E.DB_ACCOUNTS.prepare(
        "SELECT count(*) AS n FROM audit WHERE action = 'maktaba_publisher.set' AND target = ?",
      )
        .bind(d1.id)
        .first<{ n: number }>())!.n;
      const [s1, s2] = await Promise.all([
        call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: { account: d1.id } }),
        call("POST", "/v1/admin/maktaba-publisher", { cookie: admin2, body: { account: d1.id } }),
      ]);
      expect([s1.status, s2.status]).toEqual([200, 200]);
      const auditAfter = (await E.DB_ACCOUNTS.prepare(
        "SELECT count(*) AS n FROM audit WHERE action = 'maktaba_publisher.set' AND target = ?",
      )
        .bind(d1.id)
        .first<{ n: number }>())!.n;
      expect(auditAfter - auditBefore).toBe(1);

      // Seed a first-party plugin and a community plugin both owned by d1.
      const fpPlugin = `tv.maktaba.atomic${Date.now() % 1e6}`;
      const commPlugin = `${d1.prefix}.communitytool`;
      await E.DB_CATALOG.batch([
        E.DB_CATALOG.prepare("INSERT OR IGNORE INTO plugins (id, developer, developer_name, created) VALUES (?, ?, 'D1', ?)").bind(
          fpPlugin,
          d1.id,
          new Date().toISOString(),
        ),
        E.DB_CATALOG.prepare("INSERT OR IGNORE INTO plugins (id, developer, developer_name, created) VALUES (?, ?, 'D1', ?)").bind(
          commPlugin,
          d1.id,
          new Date().toISOString(),
        ),
      ]);

      // Concurrent conflicting moves (d1 -> d2 and d1 -> owner): one wins, one gets 409,
      // prefix and first-party plugin agree on the winner, and the community plugin stays with d1.
      const [m1, m2] = await Promise.all([
        call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: d2.id, reason: "race1" } }),
        call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin2, body: { account: owner.id, reason: "race2" } }),
      ]);
      expect([m1.status, m2.status].sort((a, b) => a - b)).toEqual([200, 409]);
      const loser = m1.status === 409 ? m1 : m2;
      expect(loser.body.error).toBe("maktaba_publisher_taken");

      const winner = (await E.DB_CATALOG.prepare("SELECT account FROM developer_prefixes WHERE prefix = 'tv.maktaba'").first<{
        account: string;
      }>())!.account;
      const fpOwner = (await E.DB_CATALOG.prepare("SELECT developer FROM plugins WHERE id = ?")
        .bind(fpPlugin)
        .first<{ developer: string }>())!.developer;
      const commOwner = (await E.DB_CATALOG.prepare("SELECT developer FROM plugins WHERE id = ?")
        .bind(commPlugin)
        .first<{ developer: string }>())!.developer;
      expect(fpOwner).toBe(winner);
      expect(commOwner).toBe(d1.id);

      // Same-target retry repairs any stranded first-party plugin row.
      await E.DB_CATALOG.prepare("UPDATE plugins SET developer = ? WHERE id = ?").bind(d1.id, fpPlugin).run();
      const repaired = await call("POST", "/v1/admin/maktaba-publisher/move", {
        cookie: admin,
        body: { account: winner, reason: "repair" },
      });
      expect(repaired.status).toBe(200);
      const afterRepair = (await E.DB_CATALOG.prepare("SELECT developer FROM plugins WHERE id = ?")
        .bind(fpPlugin)
        .first<{ developer: string }>())!.developer;
      expect(afterRepair).toBe(winner);
    } finally {
      await E.DB_CATALOG.prepare("DELETE FROM developer_prefixes WHERE prefix = 'tv.maktaba'").run();
      const restored = await call("POST", "/v1/admin/maktaba-publisher", { cookie: admin, body: { account: owner.id } });
      expect(restored.status).toBe(200);
      await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: owner.id } });
    }
  });

  it("re-signs historical approved versions after a publisher move during store key rotation, and blocks pending submissions from the old publisher", async () => {
    const owner = await publisher();
    const replacement = await developer();
    const admin = await storeAdmin();
    const reviewer = await account();
    await grant(reviewer.id, "store_reviewer");
    const reviewerCookie = (await addPasskey(reviewer.cookie)).cookie;

    // 1. Submit and approve a version under the original publisher (owner).
    const approvedVer = `0.6.${(Date.now() % 1e6) + 1}`;
    const approvedSid = await submit(owner, ID, approvedVer, await signed(owner, files(ID, approvedVer), ID, approvedVer));
    await countersign(E, approvedSid, "reviewed");
    await E.DB_CATALOG.prepare("UPDATE submissions SET status = 'approved' WHERE id = ?").bind(approvedSid).run();

    // 2. Submit a second version under owner that stays in_review when the move happens.
    const pendingVer = `0.6.${(Date.now() % 1e6) + 2}`;
    const pendingSid = await submit(owner, ID, pendingVer, await signed(owner, files(ID, pendingVer), ID, pendingVer));
    await runChecks(E, pendingSid);

    const origStoreKeys = await (await E.BUCKET.get("store-keys.json"))!.text();
    try {
      // 3. Move tv.maktaba from owner -> replacement.
      const moved = await call("POST", "/v1/admin/maktaba-publisher/move", {
        cookie: admin,
        body: { account: replacement.id, reason: "handover" },
      });
      expect(moved.status).toBe(200);

      // 4. Reviewer trying to approve owner's pending submission is refused with 409 maktaba_publisher_changed,
      // and direct countersign refuses with publisher_changed.
      const approveRes = await call("POST", `/v1/admin/submissions/${pendingSid}/approve`, { cookie: reviewerCookie });
      expect(approveRes).toMatchObject({ status: 409, body: { error: "maktaba_publisher_changed" } });
      await expect(countersign(E, pendingSid, "reviewed")).rejects.toThrow(/publisher_changed/);

      // 5. Rotate store signing key to S2 and verify resignPage re-signs owner's already-approved version.
      const rootSeed = new Uint8Array(
        await crypto.subtle.digest("SHA-256", enc.encode("maktaba-plugins TEST KEY, never for production: root-1")),
      );
      const rootSigner = await Signer.fromSeed(rootSeed);
      const s1Seed = new Uint8Array(
        await crypto.subtle.digest("SHA-256", enc.encode("maktaba-plugins TEST KEY, never for production: store-1")),
      );
      const s1 = await Signer.fromSeed(s1Seed);
      const s2Seed = new Uint8Array(
        await crypto.subtle.digest("SHA-256", enc.encode("maktaba-plugins TEST KEY, never for production: store-2")),
      );
      const s2 = await Signer.fromSeed(s2Seed);
      const day = 24 * 3600_000;
      const isoAt = (ms: number) => new Date(Date.now() + ms).toISOString().replace(/\.\d{3}Z$/, "Z");
      const rotatedKeys = await signEnvelope(
        "store-keys",
        {
          version: 2,
          expires: isoAt(300 * day),
          keys: [
            { id: s1.id, public: s1.public, use: "store", not_before: isoAt(-30 * day), not_after: isoAt(365 * day) },
            { id: s2.id, public: s2.public, use: "store", not_before: isoAt(-30 * day), not_after: isoAt(365 * day) },
          ],
        },
        rootSigner,
      );
      await E.BUCKET.put("store-keys.json", rotatedKeys);

      const rotatedEnv = { ...E, STORE_SIGNING_KEY: toB64(s2Seed) };
      let cursor = "";
      for (let i = 0; i < 20; i++) {
        cursor = await resignPage(rotatedEnv, cursor, 50);
        if (!cursor) break;
      }

      const verRow = await E.DB_CATALOG.prepare("SELECT sha256, store_key, developer_key FROM versions WHERE plugin = ? AND version = ?")
        .bind(ID, approvedVer)
        .first<{ sha256: string; store_key: string; developer_key: string }>();
      expect(verRow?.store_key).toBe(s2.id);
      expect(verRow?.developer_key).toBe(owner.key.id);

      const obj = await E.BUCKET.get(`${verRow!.sha256}.mkplugin`);
      const pkg = await inspect(new Uint8Array(await obj!.arrayBuffer()));
      const verifiedKeys = await (await Root.parse(E.PLUGINS_ROOT)).verifyStoreKeys(rotatedKeys, emptyState(), new Date());
      const v = await verifiedKeys.verifyPackage({
        filesJSON: pkg.files_json,
        developerSig: pkg.developer_sig,
        storeSig: pkg.store_sig,
        pluginId: ID,
        version: approvedVer,
        capabilitiesSHA256: await capabilitiesSHA256(JSON.parse(pkg.manifest).capabilities),
      });
      expect(v).toMatchObject({ level: "reviewed", developerKeyId: owner.key.id });
      expect((JSON.parse(pkg.store_sig!) as { key_id: string }).key_id).toBe(s2.id);
    } finally {
      await E.BUCKET.put("store-keys.json", origStoreKeys);
      const back = await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: owner.id } });
      expect(back.status).toBe(200);
    }
  });

  it("guards first-party plugin creation in startSubmission against concurrent publisher moves", async () => {
    const owner = await publisher();
    const replacement = await developer();
    const admin = await storeAdmin();
    const freshId = `tv.maktaba.race${Date.now() % 1e6}`;
    const ver = "1.0.0";
    const pkg = await signed(owner, files(freshId, ver), freshId, ver);

    try {
      const [subRes, moveRes] = await Promise.all([
        call("POST", "/v1/submissions", {
          cookie: owner.cookie,
          body: { plugin_id: freshId, version: ver, package_sha256: "0".repeat(64), package_size: pkg.length, source_size: 1 },
        }),
        call("POST", "/v1/admin/maktaba-publisher/move", {
          cookie: admin,
          body: { account: replacement.id, reason: "race_guard" },
        }),
      ]);
      expect(moveRes.status).toBe(200);
      expect([201, 403]).toContain(subRes.status);
      // After the move commits, if the plugin row exists at all it MUST be owned by replacement, never stranded on owner.
      const row = await E.DB_CATALOG.prepare("SELECT developer FROM plugins WHERE id = ?").bind(freshId).first<{ developer: string }>();
      if (subRes.status === 403) {
        expect(subRes.body.error).toBe("prefix_unverified");
        expect(row).toBeNull();
      } else {
        expect(row?.developer).toBe(replacement.id);
      }
    } finally {
      const back = await call("POST", "/v1/admin/maktaba-publisher/move", { cookie: admin, body: { account: owner.id } });
      expect(back.status).toBe(200);
    }
  });
});

describe("prefixCovers (54.45 §2)", () => {
  it("covers Maktaba's ids only by the publisher's prefix, and a community id never by it", () => {
    const maktaba = [{ prefix: "tv.maktaba", kind: "maktaba" }];
    const community = [{ prefix: "tv.maktaba.community.bob", kind: "community" }];
    const dns = [{ prefix: "dev.example", kind: "dns" }];
    expect(prefixCovers("tv.maktaba.thumbnails", maktaba)).toBe(true);
    expect(prefixCovers("tv.maktaba.thumbnails", community)).toBe(false);
    expect(prefixCovers("tv.maktaba.thumbnails", dns)).toBe(false);
    expect(prefixCovers("tv.maktaba.community.bob.tags", maktaba)).toBe(false);
    expect(prefixCovers("tv.maktaba.community.bob.tags", community)).toBe(true);
    expect(prefixCovers("dev.example.tags", dns)).toBe(true);
    expect(prefixCovers("dev.example.tags", maktaba)).toBe(false);
  });
});

describe("a Maktaba plugin's submission", () => {
  it("from any other account is refused at the start (prefix_unverified)", async () => {
    const dev = await developer();
    const pkg = await signed(dev, files());
    const s = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: { plugin_id: "tv.maktaba.convert", version: VERSION, package_sha256: "0".repeat(64), package_size: pkg.length, source_size: 1 },
    });
    expect(s).toMatchObject({ status: 403, body: { error: "prefix_unverified" } });
  });

  it("from the publisher is read trusted, its shape checked, nothing run, and goes to a person", async () => {
    const dev = await publisher();
    const id = `tv.maktaba.thumbnails`;
    const version = `0.9.${(Date.now() % 1e6) + 1}`;
    const sid = await submit(dev, id, version, await signed(dev, files(id, version), id, version));
    const report = await runChecks(E, sid);
    const byId = Object.fromEntries(report.checks.map((c) => [c.id, c]));
    expect(
      report.checks.find((c) => c.result === "block"),
      JSON.stringify(report.checks),
    ).toBeUndefined();
    expect(byId.C2).toMatchObject({ result: "pass" });
    expect(byId.C3).toMatchObject({ result: "pass" });
    expect(byId.C4).toMatchObject({ result: "pass" });
    expect(byId.C5).toMatchObject({ result: "skip" });
    expect(byId.C8).toMatchObject({ result: "skip" });
    const row = await E.DB_CATALOG.prepare("SELECT status, route FROM submissions WHERE id = ?")
      .bind(sid)
      .first<{ status: string; route: string }>();
    expect(row).toMatchObject({ status: "in_review", route: "human" });
  });

  it("with a file more than its shape is blocked at C4 (package_native_shape)", async () => {
    const dev = await publisher();
    const version = `0.8.${(Date.now() % 1e6) + 1}`;
    const sid = await submit(dev, ID, version, await signed(dev, files(ID, version, { "run.sh": enc.encode("#!/bin/sh") }), ID, version));
    const report = await runChecks(E, sid);
    expect(report.checks.find((c) => c.result === "block")).toMatchObject({ id: "C4", code: "package_native_shape" });
  });

  it("does not let the publisher into a community prefix", async () => {
    const dev = await publisher();
    const s = await call("POST", "/v1/submissions", {
      cookie: dev.cookie,
      body: {
        plugin_id: "tv.maktaba.community.someone.tags",
        version: "1.0.0",
        package_sha256: "0".repeat(64),
        package_size: 1,
        source_size: 1,
      },
    });
    expect(s).toMatchObject({ status: 403, body: { error: "prefix_unverified" } });
  });
});
