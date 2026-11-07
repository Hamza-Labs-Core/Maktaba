// Story 50.32 T06: the user bundle check passes a clean build and names each file that carries
// operator console code. node --test tools/lint/no-admin-in-user-bundle.test.mjs
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { findings } from "./no-admin-in-user-bundle.mjs";

function build(files) {
  const dir = mkdtempSync(path.join(tmpdir(), "dist-cloud-"));
  for (const [name, text] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dir, name)), { recursive: true });
    writeFileSync(path.join(dir, name), text);
  }
  return dir;
}

test("a clean build passes", () => {
  const dir = build({
    "index.html": "<html></html>",
    "assets/index-abc.js": 'fetch("/v1/billing/me")',
    "assets/index-abc.js.map": JSON.stringify({
      sources: ["../../cloud/src/App.tsx"],
    }),
  });
  assert.deepEqual(findings(dir), []);
});

test("an admin path, a staff path, an admin key and an admin module are each named", () => {
  const dir = build({
    "assets/a.js": 'fetch("/v1/admin/accounts")',
    "assets/b.js": 'fetch("/v1/staff/login")',
    "assets/c.js": 'const k = {"admin.nav.accounts": "Accounts"}',
    "assets/d.js.map": JSON.stringify({
      sources: ["../../admin/src/pages/Accounts.tsx"],
    }),
  });
  assert.deepEqual(findings(dir).sort(), [
    "assets/a.js: contains /v1/admin/",
    "assets/b.js: contains /v1/staff/",
    'assets/c.js: contains "admin.',
    "assets/d.js.map: bundles ../../admin/src/pages/Accounts.tsx",
  ]);
});
