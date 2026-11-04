// The public-safety scan: maktaba.dev is public, so the built site must carry nothing
// internal. The build (scripts/build.mjs) only copies allow-listed sources; this scan is the
// second wall, run by the build and by the test suite over everything in the assets dir.
//
//   node scripts/scan.mjs dist     exits 1 and lists each finding
//
// A finding is a file and the rule it broke, with the matched text masked. Text files are
// read whole; every file's name and path are read too; an image's metadata (EXIF, XMP, a
// PNG's text chunks) is refused or read as text, since a screenshot's can name the machine,
// the person or the place it was taken.

import fs from "node:fs";
import path from "node:path";
import zlib from "node:zlib";
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

// ---- images: what they carry besides pixels ---------------------------------------------

const IMAGE = /\.(png|webp|jpe?g)$/i;
// A WebP's own chunks: the picture, its alpha and animation. Anything else (EXIF, XMP, ICCP
// or an unknown chunk) is metadata, and a screenshot's can name the machine, the person or
// the place it was taken.
const WEBP_PICTURE = new Set(["VP8 ", "VP8L", "VP8X", "ALPH", "ANIM", "ANMF"]);
// a PNG's text chunks (scanned as text) and its EXIF chunk (refused, like a WebP's)
const PNG_TEXT = new Set(["tEXt", "zTXt", "iTXt"]);

/**
 * An image's metadata: `blocks` names each metadata chunk it carries that is refused (EXIF,
 * XMP and the like), `text` is the text of every metadata chunk, for the text rules.
 */
export function imageMetadata(buf) {
  const blocks = [];
  const texts = [];
  const latin1 = (b) => Buffer.from(b).toString("latin1");
  if (
    buf.length >= 12 &&
    buf.toString("ascii", 0, 4) === "RIFF" &&
    buf.toString("ascii", 8, 12) === "WEBP"
  ) {
    for (let at = 12; at + 8 <= buf.length; ) {
      const id = buf.toString("ascii", at, at + 4);
      const size = buf.readUInt32LE(at + 4);
      const body = buf.subarray(at + 8, at + 8 + size);
      if (!WEBP_PICTURE.has(id)) {
        blocks.push(id.trim());
        texts.push(latin1(body));
      }
      at += 8 + size + (size % 2);
    }
  } else if (buf.length >= 8 && buf.readUInt32BE(0) === 0x89504e47) {
    for (let at = 8; at + 12 <= buf.length; ) {
      const size = buf.readUInt32BE(at);
      const id = buf.toString("ascii", at + 4, at + 8);
      const body = buf.subarray(at + 8, at + 8 + size);
      if (id === "eXIf") {
        blocks.push(id);
        texts.push(latin1(body));
      } else if (PNG_TEXT.has(id)) {
        let text = latin1(body);
        if (id === "zTXt") {
          const nul = body.indexOf(0);
          try {
            text =
              latin1(body.subarray(0, nul)) +
              " " +
              zlib.inflateSync(body.subarray(nul + 2)).toString("utf8");
          } catch {
            blocks.push(id);
          }
        } else if (id === "iTXt") {
          // keyword, NUL, compressed flag, method, language, NUL, keyword, NUL, text
          const nul = body.indexOf(0);
          const compressed = body[nul + 1] === 1;
          let rest = nul + 3;
          rest = body.indexOf(0, rest) + 1;
          rest = body.indexOf(0, rest) + 1;
          const data = body.subarray(rest);
          try {
            text =
              latin1(body.subarray(0, nul)) +
              " " +
              (compressed ? zlib.inflateSync(data) : data).toString("utf8");
          } catch {
            blocks.push(id);
          }
        }
        texts.push(text);
      }
      if (id === "IEND") break;
      at += 12 + size;
    }
  } else if (buf.length >= 4 && buf[0] === 0xff && buf[1] === 0xd8) {
    for (let at = 2; at + 4 <= buf.length && buf[at] === 0xff; ) {
      const marker = buf[at + 1];
      if (marker === 0xda || marker === 0xd9) break; // the picture itself, or its end
      const size = buf.readUInt16BE(at + 2);
      const body = buf.subarray(at + 4, at + 2 + size);
      // APP1 (EXIF, XMP), APP13 (IPTC) and comments
      if (marker === 0xe1 || marker === 0xed || marker === 0xfe) {
        blocks.push(marker === 0xfe ? "COM" : `APP${marker - 0xe0}`);
        texts.push(latin1(body));
      }
      at += 2 + size;
    }
  }
  return { blocks, text: texts.join("\n") };
}

// A published file's name and path: plain letters, digits and . _ - / @ only (no spaces, no
// names copied from someone's disk), and the text rules apply to it as to any text.
const SAFE_NAME = /^[A-Za-z0-9._@/-]+$/;

/** Scan a built site; returns [{file, rule, match}]. */
export function scanDir(dir) {
  const findings = [];
  for (const rel of walk(dir)) {
    if (!SAFE_NAME.test(rel)) findings.push({ file: rel, rule: "file-name", match: mask(rel) });
    for (const f of scanText(rel)) findings.push({ file: rel, ...f, rule: `file-name:${f.rule}` });
    if (IMAGE.test(rel)) {
      const meta = imageMetadata(fs.readFileSync(path.join(dir, rel)));
      for (const b of meta.blocks) findings.push({ file: rel, rule: "image-metadata", match: b });
      for (const f of scanText(meta.text))
        findings.push({ file: rel, ...f, rule: `image-metadata:${f.rule}` });
      continue;
    }
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
