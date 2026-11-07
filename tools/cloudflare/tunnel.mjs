// The Cloudflare Tunnel's ingress and the names' DNS records, from
// deploy/cloudflare/tunnel.maktaba.tv.json (Epic 50, story 50.31). Node 22, no dependency.
//
//   node tools/cloudflare/tunnel.mjs check [file]   offline: the file's shape (CI, every PR)
//   node tools/cloudflare/tunnel.mjs plan  [file]   what apply would change (needs the token)
//   node tools/cloudflare/tunnel.mjs apply [file]   writes it, then reads it back
//
// plan and apply read CLOUDFLARE_API_TOKEN_TUNNEL (or CLOUDFLARE_API_TOKEN) and
// CLOUDFLARE_ACCOUNT_ID; mode "aop" also reads CLOUDFLARE_AOP_CERT and CLOUDFLARE_AOP_KEY. Only
// cloudflare-zone.yml runs plan and apply, from main; nothing here prints a token or the key.

import { readFileSync } from "node:fs";
import { client, DEFAULT_BASE, tokenFor } from "./cf.mjs";

export const DEFAULT_FILE = new URL("../../deploy/cloudflare/tunnel.maktaba.tv.json", import.meta.url).pathname;

const TOP = ["zone", "tunnel", "mode", "ingress", "aop"];
const RULE = ["hostname", "service", "reason"];
const AOP = ["origin_ipv4", "reason"];
const CATCH_ALL = "http_status:404";
// The names the Tunnel must carry, and where: the api's two loopback listeners (50.32 adds the
// admin one). A file without one of them would leave that name dark.
const REQUIRED = ["api", "app", "admin"];
const LOOPBACK = /^http:\/\/(127\.0\.0\.1|\[::1\]):(\d{1,5})$/;

function isIPv4(s) {
  const p = String(s).split(".");
  return p.length === 4 && p.every((x) => /^\d{1,3}$/.test(x) && Number(x) <= 255);
}

/** A relay name: relay.<zone> or anything under it. Relay names are DNS only, never proxied. */
export function isRelayName(host, zone) {
  const h = String(host).toLowerCase().replace(/\.$/, "");
  const r = `relay.${zone}`;
  return h === r || h.endsWith(`.${r}`);
}

/** Offline checks; returns a list of problems (empty when the file is good). */
export function check(t) {
  const errs = [];
  if (!t || typeof t !== "object") return ["the file is not a JSON object"];
  for (const k of Object.keys(t)) if (!TOP.includes(k)) errs.push(`unknown field "${k}"`);
  if (!t.zone) errs.push("zone is required");
  if (!t.tunnel) errs.push("tunnel (its name) is required");
  if (t.mode !== "tunnel" && t.mode !== "aop") errs.push(`mode must be "tunnel" or "aop", not ${JSON.stringify(t.mode)}`);
  const rules = Array.isArray(t.ingress) ? t.ingress : [];
  if (!rules.length) errs.push("ingress has no rules");
  const seen = new Set();
  for (const [i, r] of rules.entries()) {
    const at = `ingress[${i}]${r?.hostname ? ` (${r.hostname})` : ""}`;
    for (const k of Object.keys(r ?? {})) if (!RULE.includes(k)) errs.push(`${at}: unknown field "${k}"`);
    if (!r?.hostname) {
      errs.push(`${at}: every rule names its hostname (the 404 for everything else is added by the tool)`);
      continue;
    }
    const h = r.hostname.toLowerCase();
    if (h.includes("*")) errs.push(`${at}: no wildcard: each name is listed`);
    if (t.zone && !(h === t.zone || h.endsWith(`.${t.zone}`))) errs.push(`${at}: not in the zone ${t.zone}`);
    if (t.zone && isRelayName(h, t.zone)) errs.push(`${at}: a relay name is never in the Tunnel (relay names are DNS only, README §15.5)`);
    if (seen.has(h)) errs.push(`${at}: listed twice`);
    seen.add(h);
    const m = LOOPBACK.exec(r.service ?? "");
    if (!m || Number(m[2]) < 1 || Number(m[2]) > 65535) errs.push(`${at}: service must be a loopback listener (http://127.0.0.1:<port> or http://[::1]:<port>), not ${JSON.stringify(r.service)}`);
    if (!r.reason) errs.push(`${at}: a reason is required`);
  }
  for (const n of REQUIRED) if (t.zone && !seen.has(`${n}.${t.zone}`)) errs.push(`${n}.${t.zone} is missing from the ingress`);
  if (t.aop !== undefined) {
    for (const k of Object.keys(t.aop ?? {})) if (!AOP.includes(k)) errs.push(`aop: unknown field "${k}"`);
    const ip = t.aop?.origin_ipv4;
    if (ip != null && !isIPv4(ip)) errs.push(`aop.origin_ipv4 is not an IPv4 address: ${JSON.stringify(ip)}`);
  }
  if (t.mode === "aop" && !isIPv4(t.aop?.origin_ipv4)) errs.push('mode "aop" needs aop.origin_ipv4 (the Floating IP the proxied A records point at)');
  return errs;
}

/** The ingress Cloudflare should hold: each rule with its own Host header, then the 404. */
export function desiredIngress(t) {
  return [
    ...t.ingress.map((r) => ({
      hostname: r.hostname,
      service: r.service,
      originRequest: { httpHostHeader: r.hostname, noTLSVerify: false },
    })),
    { service: CATCH_ALL },
  ];
}

/** The DNS record each name should have: the tunnel's CNAME, or in aop the Floating IP's A. */
export function desiredRecord(t, host, tunnelId) {
  if (t.mode === "aop") return { type: "A", name: host, content: t.aop.origin_ipv4, proxied: true };
  return { type: "CNAME", name: host, content: `${tunnelId}.cfargotunnel.com`, proxied: true };
}

const norm = (rules) =>
  (rules ?? []).map((r) => ({
    hostname: r.hostname ?? "",
    service: r.service ?? "",
    path: r.path ?? "",
    host: r.originRequest?.httpHostHeader ?? "",
    noTLSVerify: !!r.originRequest?.noTLSVerify,
  }));

function ingressChanges(have, want) {
  const a = norm(have);
  const b = norm(want);
  if (JSON.stringify(a) === JSON.stringify(b)) return [];
  const show = (r) => `${r.hostname || "(everything else)"} -> ${r.service}${r.host ? ` (Host ${r.host})` : ""}`;
  return [`ingress: [${a.map(show).join("; ") || "none"}] -> [${b.map(show).join("; ")}]`];
}

function recordChanges(host, have, want) {
  const same = (r) => r.type === want.type && r.content === want.content && !!r.proxied === want.proxied;
  if (have.length === 1 && same(have[0])) return [];
  if (!have.length) return [`dns ${host}: create ${want.type} ${want.content} (proxied)`];
  return [`dns ${host}: ${have.map((r) => `${r.type} ${r.content}${r.proxied ? " (proxied)" : ""}`).join(", ")} -> ${want.type} ${want.content} (proxied)`];
}

async function tunnelId(cf, accountId, name) {
  const r = await cf.get(`/accounts/${accountId}/cfd_tunnel?name=${encodeURIComponent(name)}&is_deleted=false`);
  return r?.length ? r[0].id : null;
}

async function read(cf, t, accountId, zoneId, id) {
  const conf = await cf.get(`/accounts/${accountId}/cfd_tunnel/${id}/configurations`);
  const records = {};
  for (const r of t.ingress) records[r.hostname] = (await cf.get(`/zones/${zoneId}/dns_records?name=${encodeURIComponent(r.hostname)}`)) ?? [];
  return { ingress: conf?.config?.ingress ?? [], records };
}

function changesOf(t, have, id) {
  const out = ingressChanges(have.ingress, desiredIngress(t));
  for (const r of t.ingress) out.push(...recordChanges(r.hostname, have.records[r.hostname], desiredRecord(t, r.hostname, id)));
  return out;
}

// The fallback's client certificate: Maktaba's own, uploaded once, then turned on for each name
// (per-hostname Authenticated Origin Pulls; the zone-level certificate is never used).
async function applyAOP(cf, t, zoneId, cert, key, log) {
  if (!cert || !key) throw new Error('mode "aop" needs CLOUDFLARE_AOP_CERT and CLOUDFLARE_AOP_KEY (the production environment)');
  const certs = (await cf.get(`/zones/${zoneId}/origin_tls_client_auth/hostnames/certificates`)) ?? [];
  let found = certs.find((c) => (c.certificate ?? "").trim() === cert.trim());
  if (!found) {
    found = await cf.post(`/zones/${zoneId}/origin_tls_client_auth/hostnames/certificates`, { certificate: cert, private_key: key });
    log("aop: uploaded the client certificate");
  }
  const config = t.ingress.map((r) => ({ hostname: r.hostname, cert_id: found.id, enabled: true }));
  await cf.put(`/zones/${zoneId}/origin_tls_client_auth/hostnames`, { config });
  const off = [];
  for (const r of t.ingress) {
    const h = await cf.get(`/zones/${zoneId}/origin_tls_client_auth/hostnames/${encodeURIComponent(r.hostname)}`);
    if (!h?.enabled || h.cert_id !== found.id) off.push(`aop: ${r.hostname} does not present Maktaba's certificate`);
  }
  return off;
}

export async function planOrApply(action, t, { token, accountId, base = DEFAULT_BASE, fetchImpl = fetch, env = {}, log = console.log } = {}) {
  if (!accountId) throw new Error("CLOUDFLARE_ACCOUNT_ID is required");
  const cf = client({ token, base, fetchImpl });
  const zoneId = await cf.zoneId(t.zone);
  const id = await tunnelId(cf, accountId, t.tunnel);
  // before the owner creates the tunnel (README §15.7, step 3) there is nothing to write to:
  // nothing is changed, and the run says so
  if (!id) return { changes: [], drift: [], missing: true };
  const have = await read(cf, t, accountId, zoneId, id);
  const changes = changesOf(t, have, id);
  for (const l of changes) log(l);
  if (!changes.length) log(`the tunnel and its names match the file (mode ${t.mode})`);
  if (action === "plan") return { changes, drift: [] };

  const drift = [];
  // aop: the certificate first, so Cloudflare presents it before the names point at Caddy
  if (t.mode === "aop") drift.push(...(await applyAOP(cf, t, zoneId, env.CLOUDFLARE_AOP_CERT, env.CLOUDFLARE_AOP_KEY, log)));
  // the ingress before the records, so a name never reaches a tunnel that doesn't route it
  await cf.put(`/accounts/${accountId}/cfd_tunnel/${id}/configurations`, { config: { ingress: desiredIngress(t) } });
  for (const r of t.ingress) {
    const want = { ...desiredRecord(t, r.hostname, id), comment: "deploy/cloudflare/tunnel.maktaba.tv.json (CI)" };
    const recs = have.records[r.hostname];
    if (!recs.length) await cf.post(`/zones/${zoneId}/dns_records`, want);
    else {
      await cf.put(`/zones/${zoneId}/dns_records/${recs[0].id}`, want);
      for (const extra of recs.slice(1)) await cf.delete(`/zones/${zoneId}/dns_records/${extra.id}`);
    }
  }
  const after = await read(cf, t, accountId, zoneId, id);
  drift.push(...changesOf(t, after, id));
  for (const l of drift) log(`after apply, still different: ${l}`);
  return { changes, drift };
}

async function main() {
  const [action = "check", file = DEFAULT_FILE] = process.argv.slice(2);
  const t = JSON.parse(readFileSync(file, "utf8"));
  const errs = check(t);
  if (errs.length) {
    for (const e of errs) console.log(`::error title=tunnel file::${e}`);
    process.exit(1);
  }
  if (action === "check") {
    console.log(`the tunnel file is good: ${t.ingress.map((r) => r.hostname).join(", ")}, then 404 for everything else (mode ${t.mode})`);
    return;
  }
  if (action !== "plan" && action !== "apply") throw new Error(`unknown action ${action} (check, plan, apply)`);
  const token = tokenFor(process.env, "tunnel");
  if (!token) throw new Error("CLOUDFLARE_API_TOKEN is required for plan and apply");
  const { drift, missing } = await planOrApply(action, t, {
    token,
    accountId: process.env.CLOUDFLARE_ACCOUNT_ID,
    base: process.env.CLOUDFLARE_API_BASE || DEFAULT_BASE,
    env: process.env,
  });
  if (missing) {
    console.log(`::notice title=tunnel::the tunnel ${t.tunnel} does not exist yet, so nothing was written: the owner creates it (Epic 50 README §15.7, step 3)`);
    return;
  }
  if (drift.length) process.exit(1);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((e) => {
    console.log(`::error title=tunnel::${e.message}`);
    process.exit(1);
  });
}
