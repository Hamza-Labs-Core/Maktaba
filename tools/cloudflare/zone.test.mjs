// Story 50.30 T01-T03: the zone file's check, plan and apply; no network, no Cloudflare.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { check, desired, diff, owaspAction, parseExpression, planOrApply, DEFAULT_FILE } from "./zone.mjs";
import { fakeServer, ok } from "./fake.mjs";

const file = () => JSON.parse(readFileSync(DEFAULT_FILE, "utf8"));

test("T01: the committed file passes", () => {
  assert.deepEqual(check(file()), []);
});

test("T01: each broken file fails, naming its rule", () => {
  const cases = [
    ["an unknown field", (z) => (z.custom_rules[0].colour = "red"), /custom_rules admin_paths_only_on_admin_host: unknown field colour/],
    ["a rule without a reason", (z) => delete z.custom_rules[2].reason, /custom_rules methods: no reason/],
    [
      "a rule naming a relay host",
      (z) => (z.custom_rules[2].expression = 'http.host eq "x.relay.maktaba.tv"'),
      /custom_rules methods: names the relay host x\.relay\.maktaba\.tv/,
    ],
    [
      "21 custom rules on Pro",
      (z) => {
        for (let i = 0; i < 17; i++) z.custom_rules.push({ ...z.custom_rules[2], id: `extra_${i}` });
      },
      /custom_rules: 21 rules .* pro allows 20/,
    ],
    [
      "2 rate limits on Free",
      (z) => {
        z.plan = "free";
        z.managed_rules.owasp.enabled = false;
        z.custom_rules = z.custom_rules.filter((r) => r.id !== "oversized_auth_bodies");
        for (const r of z.rate_limits) Object.assign(r, { period: 10, mitigation_timeout: 10 });
      },
      /rate_limits: 2 rules, free allows 1/,
    ],
    ["a Pro field on Free", (z) => ((z.plan = "free"), (z.managed_rules.owasp.enabled = false)), /oversized_auth_bodies: http\.request\.body\.size needs Pro/],
    ["an unknown expression field", (z) => (z.custom_rules[2].expression = 'http.cookie eq "x"'), /methods: unknown field http\.cookie/],
    ["a response without Arabic", (z) => delete z.responses.not_found.body.message_ar, /responses\.not_found: error, message and message_ar/],
  ];
  for (const [name, change, want] of cases) {
    const z = file();
    change(z);
    const errs = check(z);
    assert.ok(errs.some((e) => want.test(e)), `${name}: ${JSON.stringify(errs)}`);
  }
});

test("the expression grammar reads the file's expressions and refuses others", () => {
  const { fields, strings } = parseExpression('http.host ne "admin.maktaba.tv" and (starts_with(http.request.uri.path, "/v1/admin/") or http.request.method in {"TRACE" "CONNECT"})');
  assert.deepEqual([...fields].sort(), ["http.host", "http.request.method", "http.request.uri.path"]);
  assert.ok(strings.includes("/v1/admin/"));
  for (const bad of ['http.host eq', 'http.host == "x"', '(http.host eq "x"', 'lookup_json_string(http.host, "x")']) {
    assert.throws(() => parseExpression(bad), undefined, bad);
  }
});

test("OWASP logs before owasp_block_from, then takes the file's action", () => {
  const o = file().managed_rules.owasp;
  assert.equal(owaspAction(o, "2026-11-10"), "log");
  assert.equal(owaspAction(o, o.owasp_block_from), "block");
  const managed = desired(file(), "2026-11-10").http_request_firewall_managed;
  assert.equal(managed[1].action_parameters.overrides.rules[0].action, "log");
  assert.deepEqual(managed[1].action_parameters.overrides.categories.map((c) => c.category), ["paranoia-level-2", "paranoia-level-3", "paranoia-level-4"]);
});

test("the custom rules answer with the file's JSON, in English and Arabic", () => {
  const rule = desired(file()).http_request_firewall_custom.find((r) => r.ref === "admin_paths_only_on_admin_host");
  const body = JSON.parse(rule.action_parameters.response.content);
  assert.equal(rule.action_parameters.response.status_code, 404);
  assert.deepEqual(body, { error: "not_found", message: "Not found.", message_ar: "غير موجود." });
});

// a fake zone holding the rules it is given, or a recorded answer
function zoneApi(state, opts = {}) {
  const routes = {
    "GET /zones": () => ok([{ id: "z1", name: "maktaba.tv" }]),
    "GET /zones/z1/settings": () => ok(Object.entries(state.settings).map(([id, value]) => ({ id, value }))),
    "PATCH /zones/z1/settings": (_req, body) => {
      if (!opts.ignoreWrites) for (const it of JSON.parse(body).items) state.settings[it.id] = it.value;
      return ok([]);
    },
  };
  for (const phase of ["http_request_firewall_custom", "http_ratelimit", "http_request_firewall_managed"]) {
    routes[`GET /zones/z1/rulesets/phases/${phase}/entrypoint`] = () => ok({ rules: state.rules[phase] ?? [] });
    routes[`PUT /zones/z1/rulesets/phases/${phase}/entrypoint`] = (_req, body) => {
      if (!opts.ignoreWrites) state.rules[phase] = JSON.parse(body).rules;
      return ok({ rules: state.rules[phase] });
    };
  }
  return routes;
}

test("T02: plan against a recorded zone shows exactly the changed rule", async () => {
  const today = "2026-12-01";
  const recorded = JSON.parse(readFileSync(new URL("./fixtures/zone-rulesets.json", import.meta.url)));
  const z = file();
  const want = desired(z, today);
  // the recorded zone matches the file except one rule's limit
  const state = { settings: { ...z.settings }, rules: structuredClone(want) };
  state.rules.http_ratelimit[0].ratelimit.requests_per_period = recorded.r1_requests_per_period;
  const lines = [];
  const f = await fakeServer(zoneApi(state));
  try {
    const { changes } = await planOrApply("plan", z, { token: "t", base: f.url, today, log: (l) => lines.push(l) });
    assert.deepEqual(changes, [`~ http_ratelimit r1_auth: log when ${z.rate_limits[0].expression}`]);
    assert.ok(!f.seen.some((s) => s.key.startsWith("PUT") || s.key.startsWith("PATCH")), "plan wrote");
  } finally {
    await f.close();
  }
});

test("T02: a rule only on the zone shows as removed; a setting as changed", () => {
  const z = file();
  const want = desired(z);
  const have = { rules: structuredClone(want), settings: { ...z.settings, min_tls_version: "1.0" } };
  have.rules.http_request_firewall_custom.push({ ref: "made_in_the_dashboard", expression: "true", action: "block" });
  assert.deepEqual(diff(have, want, z.settings), ["- http_request_firewall_custom made_in_the_dashboard", "~ settings.min_tls_version: 1.0 -> 1.2"]);
});

test("T03: apply writes the file's state and reads it back; a zone that keeps another value fails", async () => {
  const z = file();
  const state = { settings: { ...z.settings, ssl: "full" }, rules: {} };
  const f = await fakeServer(zoneApi(state));
  try {
    const { drift } = await planOrApply("apply", z, { token: "t", base: f.url, log: () => {} });
    assert.deepEqual(drift, []);
    assert.equal(state.settings.ssl, "strict");
    assert.equal(state.rules.http_ratelimit.length, 2);
  } finally {
    await f.close();
  }
  const stuck = { settings: { ...z.settings, ssl: "full" }, rules: {} };
  const g = await fakeServer(zoneApi(stuck, { ignoreWrites: true }));
  try {
    const { drift } = await planOrApply("apply", z, { token: "t", base: g.url, log: () => {} });
    assert.ok(drift.length > 0, "a read-back that differs passed");
  } finally {
    await g.close();
  }
});
