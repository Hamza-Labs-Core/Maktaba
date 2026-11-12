// Story 60.03 T11: the deploy's steps in order, with a fake `wrangler` (a script that records
// each call and answers from a scenario) and a fake smoke. No call reaches Cloudflare.
import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { bootstrapConfig, deploy, detachDomains, PKG, recordLive, rollback, wranglerRunner } from "./website-deploy.mjs";
import { fakeServer, ok } from "./fake.mjs";

const PREV = "11111111-1111-4111-8111-111111111111";
const OLDER = "00000000-0000-4000-8000-000000000000";
const NEW = "22222222-2222-4222-8222-222222222222";
const PREVIEW = "https://22222222-maktaba-website.example.workers.dev";

const status = (v) => JSON.stringify({ id: "d1", versions: [{ version_id: v, percentage: 100 }], created_on: "2026-11-10T00:00:00Z" });
const NOT_FOUND = { code: 1, out: "✘ [ERROR] A request to the Cloudflare API failed.\n  This Worker does not exist on your account. [code: 10007]\n" };

/** A fake wrangler binary: logs its argv to calls.ndjson, answers scenario[<first two args>]. */
function fakeWrangler(scenario) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "fake-wrangler-"));
  const calls = path.join(dir, "calls.ndjson");
  fs.writeFileSync(path.join(dir, "scenario.json"), JSON.stringify(scenario));
  const bin = path.join(dir, "wrangler");
  fs.writeFileSync(
    bin,
    `#!${process.execPath}
const fs = require("node:fs");
const path = require("node:path");
const args = process.argv.slice(2);
fs.appendFileSync(${JSON.stringify(calls)}, JSON.stringify(["wrangler", ...args]) + "\\n");
const s = JSON.parse(fs.readFileSync(path.join(${JSON.stringify(dir)}, "scenario.json"), "utf8"));
const a = s[args.slice(0, 2).join(" ")] ?? s[args[0]] ?? { code: 0, out: "" };
if (a.file && process.env.WRANGLER_OUTPUT_FILE_PATH) fs.writeFileSync(process.env.WRANGLER_OUTPUT_FILE_PATH, a.file);
process.stdout.write(a.out ?? "");
process.exit(a.code ?? 0);
`,
    { mode: 0o755 }
  );
  const read = () =>
    fs.existsSync(calls) ? fs.readFileSync(calls, "utf8").split("\n").filter(Boolean).map((l) => JSON.parse(l)) : [];
  return { w: wranglerRunner(bin, dir), read, calls };
}

/** A fake smoke: records its call in the same log, answers from `results`. */
function fakeSmoke(calls, results) {
  return (target, base = "") => {
    fs.appendFileSync(calls, JSON.stringify(["smoke", target, ...(base ? [base] : [])]) + "\n");
    return results[target] ?? true;
  };
}

const UPLOAD = {
  code: 0,
  out: `Worker Version ID: ${NEW}\nVersion Preview URL: ${PREVIEW}\n`,
  file: JSON.stringify({ type: "version-upload", version_id: NEW, preview_url: PREVIEW }) + "\n",
};
const quiet = () => {};

test("a deploy records the live version, stages, smokes the preview, promotes, applies domains, smokes live", async () => {
  const f = fakeWrangler({ "deployments status": { code: 0, out: status(PREV) }, "versions upload": UPLOAD });
  let detached = false;
  await deploy({ w: f.w, smoke: fakeSmoke(f.calls, {}), detach: () => (detached = true), sha: "abc123", log: quiet });
  assert.deepEqual(f.read(), [
    ["wrangler", "deployments", "status", "--json"],
    ["wrangler", "versions", "upload", "--message", "abc123"],
    ["smoke", "website-preview", PREVIEW],
    ["wrangler", "versions", "deploy", `${NEW}@100%`, "--yes", "--message", "abc123"],
    ["wrangler", "triggers", "deploy"],
    ["smoke", "website"],
  ]);
  assert.equal(detached, false);
});

test("a failed live smoke rolls back to the recorded version, and the deploy fails", async () => {
  const f = fakeWrangler({ "deployments status": { code: 0, out: status(PREV) }, "versions upload": UPLOAD });
  await assert.rejects(
    deploy({ w: f.w, smoke: fakeSmoke(f.calls, { website: false }), detach: () => {}, sha: "abc123", log: quiet }),
    /live site failed its smoke/
  );
  const calls = f.read();
  assert.deepEqual(calls.at(-1), ["wrangler", "rollback", PREV, "--message", "website-deploy: live smoke failed at abc123", "--yes"]);
  // the recorded id, never a bare rollback
  assert.ok(calls.filter((c) => c[1] === "rollback").every((c) => c[2] === PREV));
});

test("a failed triggers deploy rolls back too", async () => {
  const f = fakeWrangler({
    "deployments status": { code: 0, out: status(PREV) },
    "versions upload": UPLOAD,
    "triggers deploy": { code: 1, out: "✘ [ERROR] Authentication error [code: 10000]" },
  });
  await assert.rejects(
    deploy({ w: f.w, smoke: fakeSmoke(f.calls, {}), detach: () => {}, sha: "s", log: quiet }),
    /applying the custom domains failed/
  );
  assert.deepEqual(f.read().at(-1).slice(0, 3), ["wrangler", "rollback", PREV]);
});

test("a failed preview smoke promotes nothing and rolls nothing back", async () => {
  const f = fakeWrangler({ "deployments status": { code: 0, out: status(PREV) }, "versions upload": UPLOAD });
  await assert.rejects(
    deploy({ w: f.w, smoke: fakeSmoke(f.calls, { "website-preview": false }), detach: () => {}, sha: "s", log: quiet }),
    /preview .* failed its smoke; nothing was promoted/
  );
  const verbs = f.read().map((c) => c.slice(0, 3).join(" "));
  assert.ok(!verbs.some((v) => /versions deploy|triggers deploy|rollback/.test(v)), verbs.join("\n"));
});

/** The real wrangler.toml, copied where a test's bootstrap may write beside it. */
function configCopy() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "website-config-"));
  const config = path.join(dir, "wrangler.toml");
  fs.copyFileSync(path.join(PKG, "wrangler.toml"), config);
  return { dir, config };
}

test("a first deploy creates the Worker with no routes, then stages, promotes and applies the domains", async () => {
  const f = fakeWrangler({ "deployments status": NOT_FOUND, "versions upload": UPLOAD });
  const { dir, config } = configCopy();
  await deploy({ w: f.w, smoke: fakeSmoke(f.calls, {}), detach: () => assert.fail("no detach"), sha: "s", config, log: quiet });
  assert.deepEqual(f.read(), [
    ["wrangler", "deployments", "status", "--json"],
    ["wrangler", "deploy", "--message", "website-deploy bootstrap at s", "--config", path.join(dir, ".wrangler-bootstrap.toml")],
    ["wrangler", "versions", "upload", "--message", "s"],
    ["smoke", "website-preview", PREVIEW],
    ["wrangler", "versions", "deploy", `${NEW}@100%`, "--yes", "--message", "s"],
    ["wrangler", "triggers", "deploy"],
    ["smoke", "website"],
  ]);
  // the bootstrap's config is gone
  assert.deepEqual(fs.readdirSync(dir), ["wrangler.toml"]);
});

test("the bootstrap config has no route and no workers.dev name, and keeps the preview URLs", () => {
  const real = fs.readFileSync(path.join(PKG, "wrangler.toml"), "utf8");
  assert.match(real, /custom_domain = true/);
  const b = bootstrapConfig(real);
  const settings = b.replace(/#.*$/gm, "");
  assert.doesNotMatch(settings, /route|custom_domain|maktaba\.tv/);
  assert.match(b, /^workers_dev = false$/m);
  assert.match(b, /^preview_urls = true/m);
  assert.match(b, /^name = "maktaba-website"$/m);
  assert.match(b, /^\[assets\]$/m);
  assert.throws(() => bootstrapConfig(real.replace(/^preview_urls = true.*$/m, "")), /preview_urls/);
  assert.throws(() => bootstrapConfig(real + '\nroute = "maktaba.tv/*"\n'), /still has a route/);
});

test("a first deploy that fails its live smoke detaches the domains instead of rolling back", async () => {
  const f = fakeWrangler({ "deployments status": NOT_FOUND, "versions upload": UPLOAD });
  const { config } = configCopy();
  let detached = false;
  await assert.rejects(
    deploy({ w: f.w, smoke: fakeSmoke(f.calls, { website: false }), detach: () => (detached = true), sha: "s", config, log: quiet }),
    /live site failed/
  );
  assert.equal(detached, true);
  assert.ok(!f.read().some((c) => c[1] === "rollback"));
});

test("a live version that can't be read stops the deploy before any upload", async () => {
  const f = fakeWrangler({ "deployments status": { code: 1, out: "✘ [ERROR] fetch failed" } });
  assert.throws(() => recordLive(f.w), /could not read the live deployment/);
  await assert.rejects(deploy({ w: f.w, smoke: fakeSmoke(f.calls, {}), detach: () => {}, sha: "s", log: quiet }), /could not read/);
  // recordLive's call, then the deploy's: nothing after either
  assert.deepEqual(f.read(), [
    ["wrangler", "deployments", "status", "--json"],
    ["wrangler", "deployments", "status", "--json"],
  ]);
});

test("an upload without a preview URL fails (preview_urls off)", async () => {
  const f = fakeWrangler({ "deployments status": { code: 0, out: status(PREV) }, "versions upload": { code: 0, out: `Worker Version ID: ${NEW}\n` } });
  await assert.rejects(deploy({ w: f.w, smoke: fakeSmoke(f.calls, {}), detach: () => {}, sha: "s", log: quiet }), /no preview URL/);
});

test("the rollback by hand: the deployment before the current one, or the given version; then the live smoke", async () => {
  const list = JSON.stringify([
    { id: "d2", created_on: "2026-11-10T00:00:00Z", versions: [{ version_id: PREV, percentage: 100 }] },
    { id: "d3", created_on: "2026-11-11T00:00:00Z", versions: [{ version_id: NEW, percentage: 100 }] },
    { id: "d1", created_on: "2026-11-09T00:00:00Z", versions: [{ version_id: OLDER, percentage: 100 }] },
  ]);
  const f = fakeWrangler({ "deployments list": { code: 0, out: list } });
  await rollback({ w: f.w, smoke: fakeSmoke(f.calls, {}), actor: "octo", log: quiet });
  assert.deepEqual(f.read(), [
    ["wrangler", "deployments", "list", "--json"],
    ["wrangler", "rollback", PREV, "--message", "website-deploy rollback by octo", "--yes"],
    ["smoke", "website"],
  ]);
  const g = fakeWrangler({});
  await rollback({ w: g.w, smoke: fakeSmoke(g.calls, {}), version: OLDER, actor: "octo", log: quiet });
  assert.deepEqual(g.read()[0], ["wrangler", "rollback", OLDER, "--message", "website-deploy rollback by octo", "--yes"]);
  const h = fakeWrangler({});
  await assert.rejects(rollback({ w: h.w, smoke: fakeSmoke(h.calls, { website: false }), version: OLDER, actor: "x", log: quiet }), /failed its smoke/);
});

test("detaching touches only this Worker's two domains", async () => {
  const srv = await fakeServer({
    "GET /accounts/acc/workers/domains": () =>
      ok([
        { id: "a", hostname: "maktaba.tv", service: "maktaba-website" },
        { id: "b", hostname: "www.maktaba.tv", service: "maktaba-website" },
        { id: "c", hostname: "app.maktaba.tv", service: "maktaba-cloud-web" },
      ]),
    "DELETE /accounts/acc/workers/domains/a": () => ok(null),
    "DELETE /accounts/acc/workers/domains/b": () => ok(null),
  });
  try {
    const gone = await detachDomains({ token: "t", accountId: "acc", base: srv.url, log: quiet });
    assert.deepEqual(gone, ["maktaba.tv", "www.maktaba.tv"]);
    assert.deepEqual(
      srv.seen.filter((s) => s.key.startsWith("DELETE")).map((s) => s.key),
      ["DELETE /accounts/acc/workers/domains/a", "DELETE /accounts/acc/workers/domains/b"]
    );
  } finally {
    await srv.close();
  }
});
