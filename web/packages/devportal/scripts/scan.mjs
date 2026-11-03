// The public-safety scan: maktaba.dev is public, so the built site must carry nothing
// internal. The build (scripts/build.mjs) only copies allow-listed sources; this scan is the
// second wall, run by the build and by the test suite over everything in the assets dir.
//
//   node scripts/scan.mjs dist     exits 1 and lists each finding
//
// A finding is a file and the rule it broke, with the matched text masked.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

// mDNS names the docs use on purpose: the product's own service name, not anyone's machine
export const ALLOWED_LOCAL_HOSTS = new Set(["maktaba.local", "tcp.local", "_tcp.local"]);

export const RULES = [
  { id: "home-path", re: /\/Users\/[A-Za-z0-9._-]+|\/home\/[a-z][a-z0-9_-]*\/(?!\*)|C:\\Users\\/g },
  { id: "claude-trail", re: /claude-trail/gi },
  { id: "internal-audit", re: /docs\/audits\//g },
  {
    id: "private-ip",
    // RFC 1918, carrier-grade NAT (Tailscale), link-local; not inside a longer dotted run
    re: /(?<![\d.])(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|100\.(?:6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3}|169\.254\.\d{1,3}\.\d{1,3})(?![\d.]*\d)/g,
  },
  {
    id: "local-hostname",
    re: /\b[A-Za-z0-9_][A-Za-z0-9_-]*\.local\b/g,
    allow: ALLOWED_LOCAL_HOSTS,
  },
  { id: "tailnet-hostname", re: /\b[a-z0-9-]+\.[a-z0-9-]+\.ts\.net\b/gi },
  { id: "private-key", re: /-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----/g },
  { id: "aws-key", re: /\b(?:AKIA|ASIA)[0-9A-Z]{16}\b/g },
  { id: "github-token", re: /\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b/g },
  { id: "slack-token", re: /\bxox[abpors]-[A-Za-z0-9-]{10,}/g },
  { id: "stripe-key", re: /\b[sr]k_live_[A-Za-z0-9]{10,}/g },
  { id: "google-key", re: /\bAIza[0-9A-Za-z_-]{35}\b/g },
  { id: "openai-anthropic-key", re: /\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{32,}/g },
  { id: "jwt", re: /\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}/g },
  {
    id: "secret-assignment",
    // `api_token = "…"`, `CLOUDFLARE_API_TOKEN: abc…`: a long literal right after a secret's name
    re: /\b[A-Za-z_]*(?:SECRET|TOKEN|PASSWORD|API_KEY|ACCESS_KEY)[A-Za-z_]*\s*[:=]\s*["']?[A-Za-z0-9+/_-]{24,}/g,
  },
  { id: "cloudflare-account-id", re: /\baccount_id\s*[:=]\s*["']?[0-9a-f]{32}\b/gi },
];

// Vendored third-party bundles are scanned too, but only for the rules that can be a leak
// of ours; their minified code trips the generic ones (a JWT parser's test vector, say).
const VENDORED = [/^api\/redoc\.standalone\.js$/];
const VENDORED_RULES = new Set(["home-path", "claude-trail", "internal-audit", "private-key"]);

const TEXT = /\.(html?|js|mjs|css|json|txt|xml|md|svg|drawio|map|webmanifest)$/i;

function walk(dir, base = dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, base, out);
    else if (e.isFile()) out.push(path.relative(base, p).split(path.sep).join("/"));
  }
  return out;
}

function mask(s) {
  return s.length <= 8 ? s : `${s.slice(0, 6)}…(${s.length})`;
}

/** Scan one text; returns [{rule, match}]. */
export function scanText(text, { vendored = false } = {}) {
  const found = [];
  for (const rule of RULES) {
    if (vendored && !VENDORED_RULES.has(rule.id)) continue;
    for (const m of text.matchAll(rule.re)) {
      const hit = m[0];
      if (rule.allow && rule.allow.has(hit.toLowerCase())) continue;
      found.push({ rule: rule.id, match: mask(hit) });
    }
  }
  return found;
}

/** Scan a built site; returns [{file, rule, match}]. */
export function scanDir(dir) {
  const findings = [];
  for (const rel of walk(dir)) {
    if (!TEXT.test(rel)) continue;
    const text = fs.readFileSync(path.join(dir, rel), "utf8");
    const vendored = VENDORED.some((re) => re.test(rel));
    for (const f of scanText(text, { vendored })) findings.push({ file: rel, ...f });
  }
  return findings;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const dir = path.resolve(process.argv[2] ?? "dist");
  if (!fs.existsSync(dir)) {
    console.error(`scan: ${dir} does not exist (run npm run build first)`);
    process.exit(2);
  }
  const findings = scanDir(dir);
  for (const f of findings) console.error(`${f.file}: ${f.rule}: ${f.match}`);
  if (findings.length) {
    console.error(`scan: ${findings.length} finding(s): the public site must not carry these`);
    process.exit(1);
  }
  console.log(`scan: ${walk(dir).length} files, nothing internal`);
}
