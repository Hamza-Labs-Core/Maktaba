// Story 50.34 T04: the smoke checks against a local fake of the live hosts.
import { test } from "node:test";
import assert from "node:assert/strict";
import { once, run } from "./smoke.mjs";
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

// Story 60.03 T06: the website's targets against a local fake of maktaba.tv.
const SITE = '<meta name="maktaba-site" content="maktaba-website" />';
const HEADERS = { "Strict-Transport-Security": "max-age=31536000", "Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'" };
function website(over = {}) {
  return {
    "GET /": () => [200, `<html lang="en" dir="ltr"><a id="lang-switch" class="lang" href="/ar/">العربية</a>${SITE}`, HEADERS],
    "GET /ar/": () => [200, `<html lang="ar" dir="rtl">${SITE}`, HEADERS],
    "GET /install/docker/": () => [200, '<main id="app"></main>'],
    "GET /this-does-not-exist": () => [404, `<meta name="maktaba-page" content="404" />${SITE}`],
    "GET /ar/this-does-not-exist": () => [404, '<html lang="ar" dir="rtl"><meta name="maktaba-page" content="404" />'],
    "GET /schema/log-line.json": () => [200, '{\n  "$id": "https://maktaba.tv/schema/log-line.json"\n}'],
    "GET /.well-known/security.txt": () => [200, "Expires: 2027-10-11T00:00:00Z\nCanonical: https://maktaba.tv/.well-known/security.txt\n"],
    ...over,
  };
}

test("website-preview passes on a good site reached through --base", async () => {
  const f = await fakeServer(website());
  try {
    assert.deepEqual(await run("website-preview", { ...quiet, env: {}, base: `${f.url}/` }), []);
    // only the target's own host was asked
    assert.ok(f.seen.every((s) => s.host === new URL(f.url).host));
  } finally {
    await f.close();
  }
});

test("website passes with the redirects, and app. not answering (a refused connection)", async () => {
  const f = await fakeServer(website());
  const www = await fakeServer({ "GET /install/": () => [301, "", { Location: "https://maktaba.tv/install/" }] });
  const http = await fakeServer({ "GET /": () => [301, "", { Location: "https://maktaba.tv/" }] });
  try {
    const env = { SMOKE_BASE_MAKTABA: f.url, SMOKE_BASE_WWW: www.url, SMOKE_BASE_MAKTABA_HTTP: http.url, SMOKE_BASE_APP: "http://127.0.0.1:1" };
    assert.deepEqual(await run("website", { ...quiet, env }), []);
    // www pointing anywhere else fails
    const bad = await fakeServer({ "GET /install/": () => [301, "", { Location: "https://www.maktaba.tv/install/" }] });
    try {
      const fails = await run("website", { ...quiet, env: { ...env, SMOKE_BASE_WWW: bad.url } });
      assert.equal(fails.length, 1);
      assert.match(fails[0], /header location is https:\/\/www/);
    } finally {
      await bad.close();
    }
  } finally {
    await f.close();
    await www.close();
    await http.close();
  }
});

test("a 200 on the 404 path fails, and so does HSTS with includeSubDomains", async () => {
  const f = await fakeServer(
    website({
      "GET /this-does-not-exist": () => [200, `<meta name="maktaba-page" content="404" />${SITE}`],
      "GET /": () => [200, `<html lang="en" dir="ltr"><a id="lang-switch" class="lang" href="/ar/"></a>${SITE}`, { ...HEADERS, "Strict-Transport-Security": "max-age=31536000; includeSubDomains" }],
    })
  );
  try {
    const fails = await run("website-preview", { ...quiet, env: {}, base: f.url });
    assert.equal(fails.length, 2);
    assert.match(fails[0], /strict-transport-security is max-age=31536000; includeSubDomains/);
    assert.match(fails[1], /this-does-not-exist: status 200, want 404/);
  } finally {
    await f.close();
  }
});

test("notMarker passes on a refused connection or another answer, and fails on the marker", async () => {
  const c = { host: "app.maktaba.tv", path: "/", notMarker: "maktaba-website" };
  assert.equal(await once(c, { SMOKE_BASE_APP: "http://127.0.0.1:1" }), "");
  const other = await fakeServer({ "GET /": () => [200, '<meta name="maktaba-app" content="user">'] });
  const taken = await fakeServer({ "GET /": () => [200, SITE] });
  try {
    assert.equal(await once(c, { SMOKE_BASE_APP: other.url }), "");
    assert.match(await once(c, { SMOKE_BASE_APP: taken.url }), /answers with maktaba-website/);
  } finally {
    await other.close();
    await taken.close();
  }
});
