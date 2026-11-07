// Story 50.34 T04: the smoke checks against a local fake of the live hosts.
import { test } from "node:test";
import assert from "node:assert/strict";
import { run } from "./smoke.mjs";
import { fakeServer } from "./fake.mjs";

const quiet = { attempts: 2, waitMs: 1, log: () => {} };

function env(url) {
  return { SMOKE_BASE_APP: url, SMOKE_BASE_ADMIN: url, SMOKE_BASE_API: url };
}

test("the user and admin hosts with the right answers pass", async () => {
  const app = await fakeServer({
    "GET /": () => [200, '<meta name="maktaba-app" content="user">', { "Content-Security-Policy": "default-src 'self'" }],
    "GET /v1/admin/accounts": () => [404, { error: "not_found" }],
  });
  const admin = await fakeServer({
    "GET /": () => [200, '<meta name="maktaba-app" content="admin">', { "Content-Security-Policy": "default-src 'self'", "X-Robots-Tag": "noindex, nofollow" }],
    "GET /v1/admin/accounts": () => [401, { error: "unauthorized" }],
  });
  try {
    const e = { SMOKE_BASE_APP: app.url, SMOKE_BASE_ADMIN: admin.url };
    assert.deepEqual(await run("cloud-web", { ...quiet, env: e }), []);
    assert.deepEqual(await run("admin-web", { ...quiet, env: e }), []);
    // the admin host without noindex fails
    const noRobots = await fakeServer({
      "GET /": () => [200, '<meta name="maktaba-app" content="admin">', { "Content-Security-Policy": "default-src 'self'" }],
      "GET /v1/admin/accounts": () => [401, {}],
    });
    try {
      const fails = await run("admin-web", { ...quiet, env: { SMOKE_BASE_ADMIN: noRobots.url } });
      assert.match(fails[0], /x-robots-tag missing/);
    } finally {
      await noRobots.close();
    }
  } finally {
    await app.close();
    await admin.close();
  }
});

test("a missing CSP fails", async () => {
  const f = await fakeServer({
    "GET /": () => [200, '<meta name="maktaba-app" content="user">'],
    "GET /v1/admin/accounts": () => [404, { error: "not_found" }],
  });
  try {
    const fails = await run("cloud-web", { ...quiet, env: env(f.url) });
    assert.equal(fails.length, 1);
    assert.match(fails[0], /content-security-policy missing/);
  } finally {
    await f.close();
  }
});

test("an admin path answering 200 on the user host fails", async () => {
  const f = await fakeServer({
    "GET /": () => [200, '<meta name="maktaba-app" content="user">', { "Content-Security-Policy": "default-src 'self'" }],
    "GET /v1/admin/accounts": () => [200, { accounts: [] }],
  });
  try {
    const fails = await run("cloud-web", { ...quiet, env: env(f.url) });
    assert.equal(fails.length, 1);
    assert.match(fails[0], /status 200, want 404/);
  } finally {
    await f.close();
  }
});

test("the edge's 404 JSON on an admin path of the API host; readyz; nothing listening", async () => {
  const f = await fakeServer({
    "GET /v1/admin/accounts": () => [404, '{"error":"not_found","message":"Not found.","message_ar":"غير موجود."}'],
    "GET /readyz": () => [200, { ok: true }],
  });
  try {
    assert.deepEqual(await run("zone", { ...quiet, env: env(f.url) }), []);
    assert.deepEqual(await run("api", { ...quiet, env: env(f.url) }), []);
  } finally {
    await f.close();
  }
  const fails = await run("api", { ...quiet, env: { SMOKE_BASE_API: "http://127.0.0.1:1" } });
  assert.equal(fails.length, 1);
});
