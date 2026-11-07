// The token check (Epic 50, story 50.34 §2): first in every Cloudflare deploy job. It
// verifies the token and that it can do what each piece being deployed needs, and fails naming
// the missing permission before anything is written.
//
//   node token-check.mjs --pieces zone,tunnel [--zone maktaba.tv]
//
// Env: CLOUDFLARE_ACCOUNT_ID, and the token (CLOUDFLARE_API_TOKEN, or a piece's own
// CLOUDFLARE_API_TOKEN_<PIECE>). When the token may read its own policy (API Tokens: Read), the
// policy's permission groups are compared with the table below; otherwise each permission is
// probed with one read call on its resource (which proves the token reaches it; the write
// itself is then checked by Cloudflare on apply).
import { client, tokenFor, DEFAULT_BASE } from "./cf.mjs";

/** What each piece needs (README §15.6), as Cloudflare's permission group names. */
export const PIECES = {
  workers: { account: ["Workers Scripts Write"], zone: ["Workers Routes Write", "Zone Read"] },
  zone: { account: [], zone: ["Zone WAF Write", "Zone Settings Write", "Zone Read"] },
  tunnel: { account: ["Cloudflare Tunnel Write"], zone: ["DNS Write", "Zone Read"] },
  aop: { account: [], zone: ["SSL and Certificates Write", "Zone Read"] },
};

/** One read call per permission: its path (a = account id, z = zone id). */
const PROBES = {
  "Workers Scripts Write": (a) => `/accounts/${a}/workers/scripts`,
  "Workers Routes Write": (_a, z) => `/zones/${z}/workers/routes`,
  "Zone Read": (_a, z) => `/zones/${z}`,
  "Zone WAF Write": (_a, z) => `/zones/${z}/rulesets`,
  "Zone Settings Write": (_a, z) => `/zones/${z}/settings/ssl`,
  "Cloudflare Tunnel Write": (a) => `/accounts/${a}/cfd_tunnel?per_page=1`,
  "DNS Write": (_a, z) => `/zones/${z}/dns_records?per_page=1`,
  "SSL and Certificates Write": (_a, z) => `/zones/${z}/origin_tls_client_auth/hostnames/certificates`,
};

/** The permissions the pieces need, each with its scope. */
export function needed(pieces) {
  const out = new Map();
  for (const p of pieces) {
    const n = PIECES[p];
    if (!n) throw new Error(`unknown piece ${p} (known: ${Object.keys(PIECES).join(", ")})`);
    for (const g of n.account) out.set(g, "account");
    for (const g of n.zone) out.set(g, "zone");
  }
  return out;
}

/** Whether a policy's resources cover the account or the zone. */
function covers(resources, scope, accountId, zoneId) {
  return Object.keys(resources ?? {}).some((k) => {
    if (k === `com.cloudflare.api.account.${accountId}` || k === "com.cloudflare.api.account.*") return true;
    if (scope === "zone") return k === `com.cloudflare.api.account.zone.${zoneId}` || k === "com.cloudflare.api.account.zone.*";
    return false;
  });
}

/** The permission groups a token's policies grant on the account and the zone. */
export function granted(policies, accountId, zoneId) {
  const out = { account: new Set(), zone: new Set() };
  for (const p of policies ?? []) {
    if (p.effect !== "allow") continue;
    for (const g of p.permission_groups ?? []) {
      if (covers(p.resources, "account", accountId, zoneId)) out.account.add(g.name);
      if (covers(p.resources, "zone", accountId, zoneId)) out.zone.add(g.name);
    }
  }
  return out;
}

/** Runs the check; answers the lines to print and the missing permissions. */
export async function check({ pieces, zoneName, accountId, token, base = DEFAULT_BASE, fetchImpl = fetch }) {
  const cf = client({ token, base, fetchImpl });
  const lines = [];
  // a user token, else an account-owned one
  let verified;
  try {
    verified = await cf.get("/user/tokens/verify");
  } catch {
    verified = await cf.get(`/accounts/${accountId}/tokens/verify`);
  }
  if (verified?.status !== "active") return { lines: [`the token is ${verified?.status ?? "not valid"}`], missing: ["an active token"] };
  lines.push(`token active${verified.expires_on ? `, expires ${verified.expires_on}` : ""}`);
  const want = needed(pieces);
  let zoneId = "";
  try {
    zoneId = await cf.zoneId(zoneName);
  } catch (e) {
    return { lines: [...lines, String(e.message)], missing: ["Zone Read"] };
  }
  const missing = [];
  let policies = null;
  try {
    policies = (await cf.get(`/user/tokens/${verified.id}`))?.policies ?? null;
  } catch {
    policies = null;
  }
  if (policies) {
    const g = granted(policies, accountId, zoneId);
    for (const [name, scope] of want) {
      const ok = g[scope].has(name) || (name.endsWith("Read") && g[scope].has(name.replace(/Read$/, "Write")));
      lines.push(`${ok ? "ok     " : "MISSING"} ${name} (${scope}, from the token's policy)`);
      if (!ok) missing.push(name);
    }
  } else {
    for (const [name, scope] of want) {
      let ok = true;
      try {
        await cf.get(PROBES[name](accountId, zoneId));
      } catch {
        ok = false;
      }
      lines.push(`${ok ? "ok     " : "MISSING"} ${name} (${scope}, probed by a read: the token can't read its own policy)`);
      if (!ok) missing.push(name);
    }
  }
  return { lines, missing };
}

async function main() {
  const args = process.argv.slice(2);
  const arg = (k, d) => {
    const i = args.indexOf(k);
    return i >= 0 ? args[i + 1] : d;
  };
  const pieces = (arg("--pieces", "") || "").split(",").filter(Boolean);
  const zoneName = arg("--zone", "maktaba.tv");
  const accountId = (process.env.CLOUDFLARE_ACCOUNT_ID ?? "").trim();
  const token = tokenFor(process.env, pieces.length === 1 ? pieces[0] : "");
  if (!pieces.length) throw new Error("--pieces is required (workers, zone, tunnel, aop)");
  if (!accountId || !token) throw new Error("CLOUDFLARE_ACCOUNT_ID and CLOUDFLARE_API_TOKEN are required");
  const { lines, missing } = await check({ pieces, zoneName, accountId, token, base: process.env.CLOUDFLARE_API_BASE || DEFAULT_BASE });
  for (const l of lines) console.log(l);
  if (missing.length) {
    console.log(`::error title=Cloudflare token::the token lacks ${missing.join(", ")} (deploy/cloudflare/README.md, the token table)`);
    process.exit(1);
  }
  console.log(`the token can deploy: ${pieces.join(", ")}`);
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((e) => {
    console.log(`::error title=Cloudflare token::${e.message}`);
    process.exit(1);
  });
}
