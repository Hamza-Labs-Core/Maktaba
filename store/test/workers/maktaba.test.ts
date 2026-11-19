// Story 54.45 §2: Maktaba's own plugins in the store. One publisher account for Maktaba, set
// by a store admin, is the only one that may submit a tv.maktaba.* id (community prefixes
// aside); its native packages are read in the trusted mode, their shape checked, nothing run,
// and a person reviews each.
import { describe, expect, it } from "vitest";
import { prefixCovers } from "../../web/developers";
import { runChecks } from "../../web/checks";
import { pack, inspect } from "../../web/wasm";
import { type Dev, E, account, addPasskey, call, developer, grant, submit } from "./helpers";

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
    const held = await E.DB_CATALOG.prepare("SELECT account, kind, verified_at FROM developer_prefixes WHERE prefix = 'tv.maktaba'")
      .first<{ account: string; kind: string; verified_at: string | null }>();
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
    expect(report.checks.find((c) => c.result === "block"), JSON.stringify(report.checks)).toBeUndefined();
    expect(byId.C2).toMatchObject({ result: "pass" });
    expect(byId.C3).toMatchObject({ result: "pass" });
    expect(byId.C4).toMatchObject({ result: "pass" });
    expect(byId.C5).toMatchObject({ result: "skip" });
    expect(byId.C8).toMatchObject({ result: "skip" });
    const row = await E.DB_CATALOG.prepare("SELECT status, route FROM submissions WHERE id = ?").bind(sid).first<{ status: string; route: string }>();
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
      body: { plugin_id: "tv.maktaba.community.someone.tags", version: "1.0.0", package_sha256: "0".repeat(64), package_size: 1, source_size: 1 },
    });
    expect(s).toMatchObject({ status: 403, body: { error: "prefix_unverified" } });
  });
});
