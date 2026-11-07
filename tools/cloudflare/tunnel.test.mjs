// Epic 50 story 50.31 T05, and the Tunnel's plan and apply against a local fake of Cloudflare's
// API (no test calls Cloudflare).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { check, DEFAULT_FILE, desiredIngress, planOrApply } from "./tunnel.mjs";
import { fakeServer, ok } from "./fake.mjs";

const file = () => JSON.parse(readFileSync(DEFAULT_FILE, "utf8"));
const ZONE = "zone0000000000000000000000000001";
const ACCT = "acct0000000000000000000000000001";
const TUN = "c0ffee00-0000-4000-8000-000000000001";

test("T05: the committed file passes", () => {
  assert.deepEqual(check(file()), []);
});

test("T05: a relay name in the ingress fails", () => {
  const t = file();
  t.ingress.push({ hostname: "x.relay.maktaba.tv", service: "http://127.0.0.1:8090", reason: "no" });
  assert.ok(check(t).some((e) => e.includes("x.relay.maktaba.tv") && e.includes("relay name")));
  const u = file();
  u.ingress.push({ hostname: "relay.maktaba.tv", service: "http://127.0.0.1:8090", reason: "no" });
  assert.ok(check(u).some((e) => e.includes("relay name")));
});

test("T05: a file without the 404 for everything else fails", () => {
  // the tool adds the 404 itself: a rule without a hostname (another catch-all) is refused,
  // and what the tool writes always ends with the 404
  const t = file();
  t.ingress.push({ service: "http://127.0.0.1:8090", reason: "everything" });
  assert.ok(check(t).some((e) => e.includes("names its hostname")));
  const want = desiredIngress(file());
  assert.deepEqual(want.at(-1), { service: "http_status:404" });
  assert.ok(want.slice(0, -1).every((r) => r.hostname && r.originRequest.httpHostHeader === r.hostname && r.originRequest.noTLSVerify === false));
});

test("the check refuses a public service, a missing name, a wildcard and an unknown field", () => {
  const cases = [
    [(t) => (t.ingress[0].service = "http://0.0.0.0:8090"), "loopback"],
    [(t) => (t.ingress[0].service = "https://api.example.com"), "loopback"],
    [(t) => t.ingress.splice(2, 1), "admin.maktaba.tv is missing"],
    [(t) => (t.ingress[0].hostname = "*.maktaba.tv"), "wildcard"],
    [(t) => (t.ingress[0].hostname = "api.example.com"), "not in the zone"],
    [(t) => (t.ingress[1].hostname = "api.maktaba.tv"), "listed twice"],
    [(t) => delete t.ingress[0].reason, "reason"],
    [(t) => (t.extra = 1), 'unknown field "extra"'],
    [(t) => (t.mode = "open"), "mode"],
    [(t) => (t.mode = "aop"), "aop.origin_ipv4"],
    [(t) => (t.aop.origin_ipv4 = "300.1.1.1"), "not an IPv4"],
  ];
  for (const [edit, want] of cases) {
    const t = file();
    edit(t);
    const errs = check(t);
    assert.ok(errs.some((e) => e.includes(want)), `${want}: ${JSON.stringify(errs)}`);
  }
});

function cloudflare({ ingress = [], records = {}, tunnels = [{ id: TUN, name: "maktaba-cloud-production" }], aop = {} } = {}) {
  const state = { ingress, records: structuredClone(records), certs: aop.certs ?? [], hostnames: {} };
  let n = 0;
  const routes = {
    "GET /zones": () => ok([{ id: ZONE, name: "maktaba.tv" }]),
    "GET /accounts/acct0000000000000000000000000001/cfd_tunnel": () => ok(tunnels),
    [`GET /accounts/${ACCT}/cfd_tunnel/${TUN}/configurations`]: () => ok({ config: { ingress: state.ingress } }),
    [`PUT /accounts/${ACCT}/cfd_tunnel/${TUN}/configurations`]: (_, body) => {
      state.ingress = JSON.parse(body).config.ingress;
      return ok({});
    },
    [`GET /zones/${ZONE}/dns_records`]: (req) => {
      const name = new URL(req.url, "http://x").searchParams.get("name");
      return ok(state.records[name] ?? []);
    },
    [`POST /zones/${ZONE}/dns_records`]: (_, body) => {
      const r = { id: `rec${++n}`, ...JSON.parse(body) };
      (state.records[r.name] ??= []).push(r);
      return ok(r);
    },
    [`GET /zones/${ZONE}/origin_tls_client_auth/hostnames/certificates`]: () => ok(state.certs),
    [`POST /zones/${ZONE}/origin_tls_client_auth/hostnames/certificates`]: (_, body) => {
      const c = { id: "cert1", certificate: JSON.parse(body).certificate };
      state.certs.push(c);
      return ok(c);
    },
    [`PUT /zones/${ZONE}/origin_tls_client_auth/hostnames`]: (_, body) => {
      for (const h of JSON.parse(body).config) state.hostnames[h.hostname] = h;
      return ok([]);
    },
  };
  for (const host of ["api.maktaba.tv", "app.maktaba.tv", "admin.maktaba.tv"]) {
    routes[`GET /zones/${ZONE}/origin_tls_client_auth/hostnames/${host}`] = () => ok(state.hostnames[host] ?? { hostname: host, enabled: false });
  }
  // a record's update and delete, by the ids the state holds
  const byId = (id) => Object.values(state.records).flat().find((r) => r.id === id);
  for (const id of ["a1", "a2", "rec1", "rec2", "rec3"]) {
    routes[`PUT /zones/${ZONE}/dns_records/${id}`] = (_, body) => {
      Object.assign(byId(id), JSON.parse(body));
      return ok(byId(id));
    };
    routes[`DELETE /zones/${ZONE}/dns_records/${id}`] = () => {
      for (const k of Object.keys(state.records)) state.records[k] = state.records[k].filter((r) => r.id !== id);
      return ok({ id });
    };
  }
  return { routes, state };
}

const run = async (action, t, cf, extra = {}) => {
  const srv = await fakeServer(cf.routes);
  const lines = [];
  try {
    const out = await planOrApply(action, t, { token: "test-token", accountId: ACCT, base: srv.url, log: (l) => lines.push(l), ...extra });
    return { ...out, lines, seen: srv.seen };
  } finally {
    await srv.close();
  }
};

test("plan on an empty tunnel lists the ingress and the three records, and writes nothing", async () => {
  const cf = cloudflare();
  const r = await run("plan", file(), cf);
  assert.equal(r.changes.length, 4);
  assert.match(r.changes[0], /api\.maktaba\.tv -> http:\/\/127\.0\.0\.1:8090/);
  assert.match(r.changes[0], /\(everything else\) -> http_status:404/);
  assert.ok(r.changes.some((l) => l === `dns admin.maktaba.tv: create CNAME ${TUN}.cfargotunnel.com (proxied)`));
  assert.ok(r.seen.every((s) => s.key.startsWith("GET ")), "plan only reads");
  assert.ok(r.seen.every((s) => s.auth === "Bearer test-token"));
});

test("apply writes the ingress and the records, replaces an old A record, and reads them back", async () => {
  const cf = cloudflare({
    records: {
      "api.maktaba.tv": [
        { id: "a1", type: "A", name: "api.maktaba.tv", content: "203.0.113.10", proxied: false },
        { id: "a2", type: "AAAA", name: "api.maktaba.tv", content: "2001:db8::10", proxied: false },
      ],
    },
  });
  const r = await run("apply", file(), cf);
  assert.deepEqual(r.drift, []);
  assert.deepEqual(cf.state.ingress, desiredIngress(file()));
  for (const h of ["api.maktaba.tv", "app.maktaba.tv", "admin.maktaba.tv"]) {
    assert.equal(cf.state.records[h].length, 1, h);
    assert.equal(cf.state.records[h][0].type, "CNAME");
    assert.equal(cf.state.records[h][0].content, `${TUN}.cfargotunnel.com`);
    assert.equal(cf.state.records[h][0].proxied, true);
  }
  // the ingress goes in before any record points a name at the tunnel
  const putIngress = r.seen.findIndex((s) => s.key.startsWith("PUT ") && s.key.includes("/configurations"));
  const firstRecord = r.seen.findIndex((s) => /^(POST|PUT) .*dns_records/.test(s.key));
  assert.ok(putIngress >= 0 && putIngress < firstRecord);
  // and a second plan has nothing to do
  const again = await run("plan", file(), cf);
  assert.deepEqual(again.changes, []);
});

test("a zone that keeps another value after apply is drift", async () => {
  const cf = cloudflare();
  cf.routes[`PUT /accounts/${ACCT}/cfd_tunnel/${TUN}/configurations`] = () => ok({});
  const r = await run("apply", file(), cf);
  assert.equal(r.drift.length, 1);
  assert.match(r.drift[0], /^ingress:/);
});

test("before the owner creates the tunnel, nothing is written", async () => {
  const cf = cloudflare({ tunnels: [] });
  const r = await run("apply", file(), cf);
  assert.equal(r.missing, true);
  assert.ok(r.seen.every((s) => s.key.startsWith("GET ")));
});

test("aop: the certificate is uploaded and turned on for each name, then the A records", async () => {
  const t = file();
  t.mode = "aop";
  t.aop.origin_ipv4 = "203.0.113.10";
  assert.deepEqual(check(t), []);
  const cf = cloudflare();
  const env = { CLOUDFLARE_AOP_CERT: "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----", CLOUDFLARE_AOP_KEY: "test-key" };
  const r = await run("apply", t, cf, { env });
  assert.deepEqual(r.drift, []);
  assert.equal(cf.state.certs.length, 1);
  for (const h of ["api.maktaba.tv", "app.maktaba.tv", "admin.maktaba.tv"]) {
    assert.deepEqual(cf.state.hostnames[h], { hostname: h, cert_id: "cert1", enabled: true });
    assert.deepEqual(
      cf.state.records[h].map((x) => [x.type, x.content, x.proxied]),
      [["A", "203.0.113.10", true]],
    );
  }
  const certAt = r.seen.findIndex((s) => s.key.includes("origin_tls_client_auth/hostnames/certificates") && s.key.startsWith("POST"));
  const recordAt = r.seen.findIndex((s) => /^POST .*dns_records/.test(s.key));
  assert.ok(certAt >= 0 && certAt < recordAt, "the certificate before the names move");
  // the key never shows in the tool's output
  assert.ok(!r.lines.join("\n").includes("test-key"));
  // a second apply finds the certificate and uploads nothing
  const again = await run("apply", t, cf, { env });
  assert.ok(!again.seen.some((s) => s.key.startsWith("POST") && s.key.includes("certificates")));

  // without the certificate's secrets, aop refuses before writing
  const cf2 = cloudflare();
  await assert.rejects(run("apply", t, cf2, { env: {} }), /CLOUDFLARE_AOP_CERT/);
});
