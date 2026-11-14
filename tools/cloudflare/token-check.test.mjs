// Story 50.34 T02: the token check against recorded API answers on a local fake.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { check, needed } from "./token-check.mjs";
import { fakeServer, ok, denied } from "./fake.mjs";

const ACC = "acc0123";
const ZONE = "zone456";
const verify = JSON.parse(readFileSync(new URL("./fixtures/tokens-verify.json", import.meta.url)));
const policy = JSON.parse(readFileSync(new URL("./fixtures/token-policy.json", import.meta.url)));

function routes(policies) {
  return {
    "GET /user/tokens/verify": () => ok(verify),
    "GET /zones": () => ok([{ id: ZONE, name: "maktaba.tv" }]),
    [`GET /user/tokens/${verify.id}`]: () => (policies ? ok({ ...policy, policies }) : denied()),
  };
}

test("a full token passes for every piece, from its policy", async () => {
  const f = await fakeServer(routes(policy.policies));
  try {
    const r = await check({ pieces: ["workers", "zone", "tunnel", "aop"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing, []);
    assert.ok(r.lines.some((l) => l.includes("Zone WAF Write")));
    // the token never reaches the output
    assert.ok(!r.lines.join("\n").includes("Bearer"));
  } finally {
    await f.close();
  }
});

test("a token without Zone WAF Write fails naming it", async () => {
  const without = policy.policies.map((p) => ({ ...p, permission_groups: p.permission_groups.filter((g) => g.name !== "Zone WAF Write") }));
  const f = await fakeServer(routes(without));
  try {
    const r = await check({ pieces: ["zone"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing, ["Zone WAF Write"]);
  } finally {
    await f.close();
  }
});

test("a policy on another zone grants nothing here", async () => {
  const other = policy.policies.map((p) => ({ ...p, resources: { "com.cloudflare.api.account.zone.someoneelse": "*" } }));
  const f = await fakeServer(routes(other));
  try {
    const r = await check({ pieces: ["zone"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing.sort(), ["Zone Read", "Zone Settings Write", "Zone WAF Write"]);
  } finally {
    await f.close();
  }
});

test("a token that can't read its own policy is probed by reads; a refused read names the permission", async () => {
  const f = await fakeServer({
    ...routes(null),
    [`GET /zones/${ZONE}`]: () => ok({ id: ZONE }),
    [`GET /zones/${ZONE}/rulesets`]: () => ok([]),
    [`GET /zones/${ZONE}/settings/ssl`]: () => denied(),
  });
  try {
    const r = await check({ pieces: ["zone"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing, ["Zone Settings Write"]);
    assert.ok(r.lines.some((l) => l.includes("probed by a read")));
  } finally {
    await f.close();
  }
});

test("an expired token, a zone not in the account, an unknown piece", async () => {
  const f = await fakeServer({ "GET /user/tokens/verify": () => ok({ ...verify, status: "expired" }) });
  try {
    const r = await check({ pieces: ["zone"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing, ["an active token"]);
  } finally {
    await f.close();
  }
  const g = await fakeServer({ "GET /user/tokens/verify": () => ok(verify), "GET /zones": () => ok([]) });
  try {
    const r = await check({ pieces: ["zone"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: g.url });
    assert.deepEqual(r.missing, ["Zone Read"]);
  } finally {
    await g.close();
  }
  assert.throws(() => needed(["dnz"]), /unknown piece dnz/);
});

// The website's deploy checks the workers piece only: Cloudflare creates a custom domain's DNS
// record itself, so DNS Write is not needed (maktaba.dev attaches its custom domains with the
// same shared token, which has none). The shared token as it is on maktaba.tv: Workers Scripts,
// Workers Routes, Zone Read and Zone Settings pass; the DNS and Zone WAF reads are refused.
test("the website's check passes a token without DNS Write; there is no dns piece", async () => {
  assert.deepEqual([...needed(["workers"])], [
    ["Workers Scripts Write", "account"],
    ["Workers Routes Write", "zone"],
    ["Zone Read", "zone"],
  ]);
  assert.throws(() => needed(["workers", "dns"]), /unknown piece dns/);
  const noDns = policy.policies.map((p) => ({ ...p, permission_groups: p.permission_groups.filter((g) => g.name !== "DNS Write") }));
  const f = await fakeServer(routes(noDns));
  try {
    const r = await check({ pieces: ["workers"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.deepEqual(r.missing, []);
    assert.ok(!r.lines.join("\n").includes("DNS"));
  } finally {
    await f.close();
  }
  const g = await fakeServer({
    ...routes(null),
    [`GET /accounts/${ACC}/workers/scripts`]: () => ok([]),
    [`GET /zones/${ZONE}/workers/routes`]: () => ok([]),
    [`GET /zones/${ZONE}`]: () => ok({ id: ZONE }),
    [`GET /zones/${ZONE}/settings/ssl`]: () => ok({ value: "strict" }),
    [`GET /zones/${ZONE}/dns_records`]: () => denied(),
    [`GET /zones/${ZONE}/rulesets`]: () => denied(),
  });
  try {
    const r = await check({ pieces: ["workers"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: g.url });
    assert.deepEqual(r.missing, []);
    assert.ok(r.lines.every((l) => !l.startsWith("MISSING")));
  } finally {
    await g.close();
  }
});

// Story 50.21: the broadcast edge's deploy also needs R2 (its segment and guide buckets); a token
// without Workers R2 Storage Write fails naming it, before anything is written.
test("the broadcast piece needs Workers R2 Storage Write", async () => {
  assert.deepEqual([...needed(["broadcast"])], [
    ["Workers Scripts Write", "account"],
    ["Workers R2 Storage Write", "account"],
    ["Workers Routes Write", "zone"],
    ["Zone Read", "zone"],
  ]);
  const f = await fakeServer(routes(policy.policies));
  try {
    const r = await check({ pieces: ["broadcast"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: f.url });
    assert.ok(r.missing.includes("Workers R2 Storage Write"), JSON.stringify(r.missing));
  } finally {
    await f.close();
  }
  const withR2 = policy.policies.map((p, i) =>
    i === 0 ? { ...p, permission_groups: [...p.permission_groups, { id: "r2w", name: "Workers R2 Storage Write" }] } : p
  );
  const g = await fakeServer(routes(withR2));
  try {
    const r = await check({ pieces: ["broadcast"], zoneName: "maktaba.tv", accountId: ACC, token: "t", base: g.url });
    assert.deepEqual(r.missing, []);
  } finally {
    await g.close();
  }
});
