// The zone's security settings from deploy/cloudflare/zone.maktaba.tv.json (Epic 50, story
// 50.30): TLS settings, the managed rulesets, custom rules and rate limits.
//
//   node zone.mjs check [file]   offline, every pull request: the file's shape, the named plan's
//                                rule counts, each expression's fields, reasons, no relay name
//   node zone.mjs plan  [file]   main: the zone's current rules and settings, and the difference
//   node zone.mjs apply [file]   main: write them, read them back, fail on any difference
//
// plan and apply need CLOUDFLARE_API_TOKEN (or CLOUDFLARE_API_TOKEN_ZONE); CLOUDFLARE_API_BASE
// points them at a fake in the tests. Nothing here prints a token.
import { readFileSync } from "node:fs";
import { client, tokenFor, DEFAULT_BASE } from "./cf.mjs";

export const DEFAULT_FILE = new URL("../../deploy/cloudflare/zone.maktaba.tv.json", import.meta.url).pathname;

/** Each plan's limits (Cloudflare's plan pages, checked 2026-11-03; README §15.7). */
export const PLANS = {
  free: { custom: 5, rateLimits: 1, periods: [10], timeouts: [10], proFields: false, owasp: false },
  pro: { custom: 20, rateLimits: 2, periods: [10, 60], timeouts: [60, 600], proFields: true, owasp: true },
};
export const PLANS_CHECKED = "2026-11-03";

/** The fields expressions may use, and which need Pro. */
export const FIELDS = {
  "http.host": "string",
  "http.request.uri.path": "string",
  "http.request.method": "string",
  "http.request.body.size": "number",
  "ip.src": "ip",
  "cf.worker.upstream_zone": "string",
};
const PRO_FIELDS = new Set(["http.request.body.size"]);

/** Cloudflare's managed rulesets (their published ids). */
export const MANAGED = {
  cloudflare: "efb7b8c949ac4650a09736fc376e9aee",
  cloudflare_free: "77454fe2d30c4220b5701f6fdfb893ba",
  owasp: "4814384a9e5d4991b9815dcfc25d2f1f",
  owasp_score_rule: "6179ae15870a4bb7b2d480d4843b323c",
};
const SCORE = { low: 60, medium: 40, high: 25 };
const SETTINGS = {
  ssl: ["off", "flexible", "full", "strict"],
  min_tls_version: ["1.0", "1.1", "1.2", "1.3"],
  tls_1_3: ["on", "off", "zrt"],
  always_use_https: ["on", "off"],
  automatic_https_rewrites: ["on", "off"],
  opportunistic_encryption: ["on", "off"],
  browser_check: ["on", "off"],
  security_level: ["off", "essentially_off", "low", "medium", "high", "under_attack"],
};
const ACTIONS = ["block", "managed_challenge", "challenge", "js_challenge", "log"];
const RELAY = /(^|\.)relay\.maktaba\.tv$/i;

// ---- the expression grammar (the subset of Cloudflare's rules language the file uses) ------

function tokens(src) {
  const out = [];
  const re = /\s*(?:(\()|(\))|(\{)|(\})|(,)|("(?:[^"\\]|\\.)*")|(\$[a-z_][a-z0-9_]*)|(\d+)|([a-z_][a-z0-9_.]*))/iy;
  let i = 0;
  while (i < src.length) {
    if (/^\s*$/.test(src.slice(i))) break;
    re.lastIndex = i;
    const m = re.exec(src);
    if (!m) throw new Error(`can't read the expression at "${src.slice(i, i + 20)}"`);
    i = re.lastIndex;
    const [, lp, rp, lb, rb, comma, str, list, num, word] = m;
    if (lp) out.push({ t: "(" });
    else if (rp) out.push({ t: ")" });
    else if (lb) out.push({ t: "{" });
    else if (rb) out.push({ t: "}" });
    else if (comma) out.push({ t: "," });
    else if (str) out.push({ t: "str", v: JSON.parse(str) });
    else if (list) out.push({ t: "list", v: list });
    else if (num) out.push({ t: "num", v: Number(num) });
    else out.push({ t: "word", v: word });
  }
  return out;
}

/** Parses an expression; answers the fields and string values it uses. Throws on anything
 * outside the grammar, naming it. */
export function parseExpression(src) {
  const ts = tokens(src);
  let p = 0;
  const fields = new Set();
  const strings = [];
  const peek = () => ts[p];
  const take = (t, v) => {
    const k = ts[p];
    if (!k || k.t !== t || (v !== undefined && k.v !== v)) throw new Error(`expected ${v ?? t} at token ${p + 1}`);
    p++;
    return k;
  };
  const field = () => {
    const k = take("word");
    if (!(k.v in FIELDS)) throw new Error(`unknown field ${k.v}`);
    fields.add(k.v);
    return k.v;
  };
  const value = () => {
    const k = ts[p];
    if (k?.t === "str") {
      strings.push(k.v);
      p++;
      return;
    }
    if (k?.t === "num" || k?.t === "list") {
      p++;
      return;
    }
    throw new Error(`expected a value at token ${p + 1}`);
  };
  const set = () => {
    take("{");
    while (peek() && peek().t !== "}") value();
    take("}");
  };
  const primary = () => {
    const k = peek();
    if (!k) throw new Error("the expression ends early");
    if (k.t === "(") {
      p++;
      or();
      take(")");
      return;
    }
    if (k.t === "word" && k.v === "not") {
      p++;
      primary();
      return;
    }
    if (k.t === "word" && (k.v === "starts_with" || k.v === "ends_with")) {
      p++;
      take("(");
      field();
      take(",");
      value();
      take(")");
      return;
    }
    if (k.t === "word" && (k.v === "true" || k.v === "false")) {
      p++;
      return;
    }
    field();
    const op = take("word").v;
    if (op === "in") {
      if (peek()?.t === "list") p++;
      else set();
    } else if (["eq", "ne", "gt", "lt", "ge", "le", "contains", "matches"].includes(op)) {
      value();
    } else {
      throw new Error(`unknown operator ${op}`);
    }
  };
  const and = () => {
    primary();
    while (peek()?.t === "word" && peek().v === "and") {
      p++;
      primary();
    }
  };
  function or() {
    and();
    while (peek()?.t === "word" && peek().v === "or") {
      p++;
      and();
    }
  }
  or();
  if (p !== ts.length) throw new Error(`unexpected token ${p + 1}`);
  return { fields, strings };
}

// ---- check ---------------------------------------------------------------------------------

const TOP = ["$schema", "zone", "plan", "settings", "super_bot_fight_mode", "managed_rules", "custom_rules", "rate_limits", "exceptions", "responses"];

function only(obj, allowed, where, errs) {
  for (const k of Object.keys(obj ?? {})) if (!allowed.includes(k)) errs.push(`${where}: unknown field ${k}`);
}

/** Checks a zone file; answers the problems, each naming its place (empty: good). */
export function check(z) {
  const errs = [];
  only(z, TOP, "the file", errs);
  const plan = PLANS[z.plan];
  if (!plan) {
    errs.push(`plan: "${z.plan}" is not one of ${Object.keys(PLANS).join(", ")}`);
    return errs;
  }
  if (z.zone !== "maktaba.tv") errs.push(`zone: "${z.zone}", want maktaba.tv`);
  only(z.settings, Object.keys(SETTINGS), "settings", errs);
  for (const [k, allowed] of Object.entries(SETTINGS)) {
    if (!allowed.includes(z.settings?.[k])) errs.push(`settings.${k}: "${z.settings?.[k]}" is not one of ${allowed.join(", ")}`);
  }
  only(z.super_bot_fight_mode, ["value", "reason"], "super_bot_fight_mode", errs);
  if (!["on", "off"].includes(z.super_bot_fight_mode?.value)) errs.push("super_bot_fight_mode.value: on or off");
  if (!z.super_bot_fight_mode?.reason) errs.push("super_bot_fight_mode: no reason");
  const m = z.managed_rules ?? {};
  only(m, ["cloudflare", "owasp"], "managed_rules", errs);
  only(m.cloudflare, ["enabled"], "managed_rules.cloudflare", errs);
  if (m.owasp) {
    only(m.owasp, ["enabled", "paranoia_level", "score_threshold", "action", "owasp_block_from", "reason"], "managed_rules.owasp", errs);
    if (m.owasp.enabled && !plan.owasp) errs.push(`managed_rules.owasp: the OWASP ruleset needs Pro (plan is ${z.plan})`);
    if (![1, 2, 3, 4].includes(m.owasp.paranoia_level)) errs.push("managed_rules.owasp.paranoia_level: 1 to 4");
    if (!(m.owasp.score_threshold in SCORE)) errs.push("managed_rules.owasp.score_threshold: low, medium or high");
    if (!["block", "log", "managed_challenge"].includes(m.owasp.action)) errs.push("managed_rules.owasp.action: block, log or managed_challenge");
    if (m.owasp.owasp_block_from && !/^\d{4}-\d{2}-\d{2}$/.test(m.owasp.owasp_block_from)) errs.push("managed_rules.owasp.owasp_block_from: a date, YYYY-MM-DD");
    if (!m.owasp.reason) errs.push("managed_rules.owasp: no reason");
  }
  const rules = [
    ...(z.custom_rules ?? []).map((r) => ["custom_rules", r, ["id", "description", "reason", "expression", "action", "response", "enabled"]]),
    ...(z.rate_limits ?? []).map((r) => ["rate_limits", r, ["id", "description", "reason", "expression", "action", "response", "enabled", "requests_per_period", "period", "mitigation_timeout"]]),
    ...(z.exceptions ?? []).map((r) => ["exceptions", r, ["id", "description", "reason", "expression", "skip"]]),
  ];
  const ids = new Set();
  for (const [kind, r, allowed] of rules) {
    const where = `${kind} ${r.id ?? "(no id)"}`;
    only(r, allowed, where, errs);
    if (!/^[a-z0-9_]{1,64}$/.test(r.id ?? "")) errs.push(`${where}: an id of a-z, 0-9 and _`);
    if (ids.has(r.id)) errs.push(`${where}: the id is used twice`);
    ids.add(r.id);
    if (!r.reason || r.reason.length < 10) errs.push(`${where}: no reason`);
    if (kind !== "exceptions" && !ACTIONS.includes(r.action)) errs.push(`${where}: action ${r.action}`);
    if (r.response && !z.responses?.[r.response]) errs.push(`${where}: no response ${r.response}`);
    try {
      const { fields, strings } = parseExpression(r.expression ?? "");
      for (const f of fields) if (PRO_FIELDS.has(f) && !plan.proFields) errs.push(`${where}: ${f} needs Pro (plan is ${z.plan})`);
      for (const s of strings) if (RELAY.test(s)) errs.push(`${where}: names the relay host ${s} (relay names are DNS only and never reach the proxy)`);
    } catch (e) {
      errs.push(`${where}: ${e.message}`);
    }
    if (kind === "rate_limits") {
      if (!plan.periods.includes(r.period)) errs.push(`${where}: period ${r.period} s is not one of ${plan.periods.join(", ")} on ${z.plan}`);
      if (!plan.timeouts.includes(r.mitigation_timeout)) errs.push(`${where}: mitigation_timeout ${r.mitigation_timeout} s is not one of ${plan.timeouts.join(", ")} on ${z.plan}`);
      if (!Number.isInteger(r.requests_per_period) || r.requests_per_period < 1) errs.push(`${where}: requests_per_period`);
    }
    if (kind === "exceptions" && !(Array.isArray(r.skip) && r.skip.length)) errs.push(`${where}: skip names no phase`);
  }
  const custom = (z.custom_rules ?? []).length + (z.exceptions ?? []).length;
  if (custom > plan.custom) errs.push(`custom_rules: ${custom} rules (with exceptions), ${z.plan} allows ${plan.custom} (checked ${PLANS_CHECKED})`);
  if ((z.rate_limits ?? []).length > plan.rateLimits)
    errs.push(`rate_limits: ${(z.rate_limits ?? []).length} rules, ${z.plan} allows ${plan.rateLimits} (checked ${PLANS_CHECKED})`);
  for (const [k, r] of Object.entries(z.responses ?? {})) {
    only(r, ["status", "body"], `responses.${k}`, errs);
    if (!r.body?.message || !r.body?.message_ar || !r.body?.error) errs.push(`responses.${k}: error, message and message_ar (English and Arabic)`);
  }
  return errs;
}

// ---- the rulesets the file asks for ---------------------------------------------------------

function respond(z, name) {
  const r = z.responses[name];
  return { response: { status_code: r.status, content_type: "application/json", content: JSON.stringify(r.body) } };
}

/** The OWASP ruleset's action today: log before owasp_block_from, the file's action after. */
export function owaspAction(o, today) {
  if (o.owasp_block_from && today < o.owasp_block_from) return "log";
  return o.action;
}

/** The three phases' rules the file asks for (ref = the file's id). */
export function desired(z, today = new Date().toISOString().slice(0, 10)) {
  const custom = [
    ...(z.exceptions ?? []).map((e) => ({
      ref: e.id,
      description: e.description ?? e.id,
      expression: e.expression,
      action: "skip",
      action_parameters: { phases: e.skip },
      enabled: true,
    })),
    ...(z.custom_rules ?? []).map((r) => ({
      ref: r.id,
      description: r.description,
      expression: r.expression,
      action: r.action,
      ...(r.response && r.action === "block" ? { action_parameters: respond(z, r.response) } : {}),
      enabled: r.enabled !== false,
    })),
  ];
  const ratelimit = (z.rate_limits ?? []).map((r) => ({
    ref: r.id,
    description: r.description,
    expression: r.expression,
    action: r.action,
    ...(r.response && r.action === "block" ? { action_parameters: respond(z, r.response) } : {}),
    ratelimit: {
      characteristics: ["cf.colo.id", "ip.src"],
      period: r.period,
      requests_per_period: r.requests_per_period,
      mitigation_timeout: r.mitigation_timeout,
    },
    enabled: r.enabled !== false,
  }));
  const managed = [];
  const m = z.managed_rules ?? {};
  if (m.cloudflare?.enabled) {
    managed.push({
      ref: "cloudflare_managed",
      description: "Cloudflare managed ruleset",
      expression: "true",
      action: "execute",
      action_parameters: { id: z.plan === "free" ? MANAGED.cloudflare_free : MANAGED.cloudflare },
      enabled: true,
    });
  }
  if (m.owasp?.enabled) {
    const disabled = [2, 3, 4].filter((l) => l > m.owasp.paranoia_level).map((l) => ({ category: `paranoia-level-${l}`, enabled: false }));
    managed.push({
      ref: "owasp_core",
      description: "OWASP core ruleset",
      expression: "true",
      action: "execute",
      action_parameters: {
        id: MANAGED.owasp,
        overrides: {
          categories: disabled,
          rules: [{ id: MANAGED.owasp_score_rule, score_threshold: SCORE[m.owasp.score_threshold], action: owaspAction(m.owasp, today) }],
        },
      },
      enabled: true,
    });
  }
  return {
    http_request_firewall_custom: custom,
    http_ratelimit: ratelimit,
    http_request_firewall_managed: managed,
  };
}

// ---- plan and apply --------------------------------------------------------------------------

const KEEP = ["ref", "description", "expression", "action", "action_parameters", "ratelimit", "enabled"];
const pick = (r) => Object.fromEntries(KEEP.filter((k) => r[k] !== undefined).map((k) => [k, r[k]]));
const same = (a, b) => JSON.stringify(sortKeys(a)) === JSON.stringify(sortKeys(b));
function sortKeys(v) {
  if (Array.isArray(v)) return v.map(sortKeys);
  if (v && typeof v === "object") return Object.fromEntries(Object.keys(v).sort().map((k) => [k, sortKeys(v[k])]));
  return v;
}

async function current(cf, zoneId) {
  const out = {};
  for (const phase of ["http_request_firewall_custom", "http_ratelimit", "http_request_firewall_managed"]) {
    try {
      const rs = await cf.get(`/zones/${zoneId}/rulesets/phases/${phase}/entrypoint`);
      out[phase] = (rs?.rules ?? []).map(pick);
    } catch (e) {
      if (e.status === 404) out[phase] = [];
      else throw e;
    }
  }
  const settings = {};
  for (const s of (await cf.get(`/zones/${zoneId}/settings`)) ?? []) if (s.id in SETTINGS) settings[s.id] = s.value;
  return { rules: out, settings };
}

/** The difference between what the zone has and what the file asks for, one line a change. */
export function diff(have, want, wantSettings) {
  const lines = [];
  for (const [phase, rules] of Object.entries(want)) {
    const before = new Map((have.rules[phase] ?? []).map((r) => [r.ref ?? r.description, r]));
    const after = new Map(rules.map((r) => [r.ref, r]));
    for (const [ref, r] of after) {
      const b = before.get(ref);
      if (!b) lines.push(`+ ${phase} ${ref}: ${r.action} when ${r.expression}`);
      else if (!same(pick(b), pick(r))) lines.push(`~ ${phase} ${ref}: ${r.action} when ${r.expression}`);
    }
    for (const ref of before.keys()) if (!after.has(ref)) lines.push(`- ${phase} ${ref}`);
    const order = (list) => list.map((r) => r.ref ?? r.description).join(",");
    if (!lines.some((l) => l.includes(phase)) && order(have.rules[phase] ?? []) !== order(rules)) lines.push(`~ ${phase}: the rules' order`);
  }
  for (const [k, v] of Object.entries(wantSettings)) if (have.settings[k] !== v) lines.push(`~ settings.${k}: ${have.settings[k]} -> ${v}`);
  return lines;
}

export async function planOrApply(action, z, { token, base = DEFAULT_BASE, fetchImpl = fetch, today, log = console.log } = {}) {
  const cf = client({ token, base, fetchImpl });
  const zoneId = await cf.zoneId(z.zone);
  const want = desired(z, today);
  const have = await current(cf, zoneId);
  const changes = diff(have, want, z.settings);
  for (const l of changes) log(l);
  if (!changes.length) log("the zone matches the file");
  if (action === "plan") return { changes, drift: [] };
  for (const [phase, rules] of Object.entries(want)) {
    await cf.put(`/zones/${zoneId}/rulesets/phases/${phase}/entrypoint`, { rules });
  }
  await cf.patch(`/zones/${zoneId}/settings`, { items: Object.entries(z.settings).map(([id, value]) => ({ id, value })) });
  const after = await current(cf, zoneId);
  const drift = diff(after, want, z.settings);
  for (const l of drift) log(`after apply, still different: ${l}`);
  return { changes, drift };
}

async function main() {
  const [action = "check", file = DEFAULT_FILE] = process.argv.slice(2);
  const z = JSON.parse(readFileSync(file, "utf8"));
  const errs = check(z);
  if (errs.length) {
    for (const e of errs) console.log(`::error title=zone file::${e}`);
    process.exit(1);
  }
  if (action === "check") {
    console.log(`the zone file is good for the ${z.plan} plan: ${(z.custom_rules ?? []).length} custom rules, ${(z.rate_limits ?? []).length} rate limits`);
    return;
  }
  if (action !== "plan" && action !== "apply") throw new Error(`unknown action ${action} (check, plan, apply)`);
  const token = tokenFor(process.env, "zone");
  if (!token) throw new Error("CLOUDFLARE_API_TOKEN is required for plan and apply");
  const { drift } = await planOrApply(action, z, { token, base: process.env.CLOUDFLARE_API_BASE || DEFAULT_BASE });
  if (drift.length) process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((e) => {
    console.log(`::error title=zone::${e.message}`);
    process.exit(1);
  });
}
