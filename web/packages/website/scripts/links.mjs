#!/usr/bin/env node
// The maktaba.tv link check (Epic 60, story 60.08).
//
// Every https://maktaba.tv/… link (and every scheme-less maktaba.tv/… one) in the repository's
// tracked files must open a page:
//   - a problem link (/problems/<slug>, /ar/problems/<slug>) names a slug of the registry,
//     shared/api/problems.json (story 60.06), so the API's pull requests need no site build;
//   - every other link names a file of the built site, as dist-manifest.json lists it (the
//     site build regenerates it with --write-manifest), with the Worker's auto-trailing-slash
//     rule (/x, /x/ and /x/index.html are the same page), and a #fragment names an id on it;
//   - a link to one of the Worker's redirects (src/redirects.json, the table src/worker.ts
//     serves: /docs) passes when the page it redirects to opens.
//
// Left out: the trail (docs/trail/**, docs/claude-trail.md), the specs (specs/**: plans, not
// links), the wiki databases built from them, test files (parser inputs, not links; TEST_PATHS
// below) and this check's own data files. links-allow.json lists the links that are not links
// (each with its reason); a new entry is reviewed like code.
//
// THE BASELINE (links-baseline.json) is empty since story 60.06 shipped the problem pages (the
// last of the links that were dead when this check landed). The mechanism stays: the check
// fails on a dead link that is not in the baseline, and on a baseline entry that is no longer
// dead (or no longer in its file), and --prune-baseline only removes entries. A NEW ENTRY IS
// REVIEWED LIKE AN ALLOW-LIST ENTRY: it is a dead link shipped on purpose, with the story whose
// page makes it live, and the pull request says why the link cannot wait for its page.
//
// Usage (Node's standard library only, from any directory):
//   node web/packages/website/scripts/links.mjs                 the check
//   node web/packages/website/scripts/links.mjs --paths         the paths it leaves out
//   node web/packages/website/scripts/links.mjs --dist DIR      check against a built dist/, and
//                                         check the built site itself (siteFindings below)
//   node web/packages/website/scripts/links.mjs --write-manifest DIR   regenerate the manifest
//   node web/packages/website/scripts/links.mjs --prune-baseline       drop entries now live
//   --root DIR runs it over another repository (the tests' fixtures).

import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PACKAGE = path.resolve(HERE, "..");

// Paths left out, as globs (--paths prints them; story 60.03's path-filter test reads them).
export const EXCLUDED_PATHS = [
  "docs/trail/**",
  "docs/claude-trail.md",
  "specs/**",
  "web/wiki-app/public/db/**",
  "docs/wiki/db/**",
  // this check's own files: links as data and patterns, not as links
  "web/packages/website/scripts/links.mjs",
  "web/packages/website/links-baseline.json",
  "web/packages/website/links-allow.json",
  "web/packages/website/dist-manifest.json",
];

// Test files hold parser inputs and fixtures, not links.
export const TEST_PATHS = [
  "**/*_test.go",
  "**/*.test.*",
  "**/*.spec.*",
  "**/*Test.kt",
  "**/*Tests.swift",
  "**/src/test/**",
  "**/__tests__/**",
  "**/test-cases/**",
  "**/test_*.py",
  "**/*-test.sh",
];

// A glob of the lists above as a RegExp: ** any path (or none), * within one segment.
function globRe(glob) {
  let re = "";
  for (let i = 0; i < glob.length; i++) {
    const c = glob[i];
    if (c === "*" && glob[i + 1] === "*") {
      i++;
      if (glob[i + 1] === "/") {
        i++;
        re += "(?:.*/)?";
      } else re += ".*";
    } else if (c === "*") re += "[^/]*";
    else re += c.replace(/[.+?^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp(`^${re}$`);
}
const EXCLUDED_RE = [...EXCLUDED_PATHS, ...TEST_PATHS].map(globRe);

export function excluded(file) {
  return EXCLUDED_RE.some((re) => re.test(file));
}

// A link: the scheme (or none) and the apex or www, not a subdomain (api.maktaba.tv) or a mail
// address (privacy@maktaba.tv). Scheme-less, only with a path: a bare "maktaba.tv" is the name.
const LINK_RE =
  /(?<![A-Za-z0-9._@/-])(?:https?:\/\/(?:www\.)?maktaba\.tv(?![A-Za-z0-9.-])(?:\/[A-Za-z0-9_./#?=%~+-]*)?|(?:www\.)?maktaba\.tv\/[A-Za-z0-9_./#?=%~+-]*)/g;

// findLinks: [{line, link, path, fragment}] in a text. `path` is the link's path, its query
// dropped, "/" for the home page; trailing sentence punctuation is not part of a link.
export function findLinks(text) {
  const out = [];
  const lines = text.split("\n");
  for (let n = 0; n < lines.length; n++) {
    for (const m of lines[n].matchAll(LINK_RE)) {
      let link = m[0].replace(/[.,]+$/, "");
      const at = link.indexOf("maktaba.tv") + "maktaba.tv".length;
      let rest = link.slice(at) || "/";
      let fragment = "";
      const hash = rest.indexOf("#");
      if (hash >= 0) {
        fragment = rest.slice(hash + 1);
        rest = rest.slice(0, hash);
      }
      const q = rest.indexOf("?");
      if (q >= 0) rest = rest.slice(0, q);
      out.push({ line: n + 1, link, path: rest || "/", fragment });
    }
  }
  return out;
}

// The registry's slugs (story 60.06). Its shape is read loosely: any object with a string
// `slug`, wherever it sits. A missing registry is no registered slug.
export function loadSlugs(root) {
  const file = path.join(root, "shared/api/problems.json");
  const slugs = new Set();
  if (!existsSync(file)) return slugs;
  const walk = (v, key) => {
    if (Array.isArray(v)) v.forEach((x) => walk(x));
    else if (v && typeof v === "object") {
      if (typeof v.slug === "string") slugs.add(v.slug);
      for (const [k, x] of Object.entries(v)) walk(x, k);
    }
  };
  walk(JSON.parse(readFileSync(file, "utf8")));
  return slugs;
}

// The manifest: {files: ["/x/index.html", ...], ids: {"/x/index.html": ["a", ...]},
// redirects: {"/docs": "/"}}. The Worker's redirects are not files: --write-manifest copies them
// from the Worker's table (loadRedirects), so the manifest says what the check passed.
export function loadManifest(file) {
  if (!existsSync(file)) return { files: [], ids: {}, redirects: {} };
  const m = JSON.parse(readFileSync(file, "utf8"));
  return { files: m.files || [], ids: m.ids || {}, redirects: m.redirects || {} };
}

// The Worker's redirects, source path → target path: src/redirects.json, the table
// src/worker.ts serves (its "$comment" left out). A missing table is no redirect.
export function loadRedirects(pkg) {
  const file = path.join(pkg, "src/redirects.json");
  if (!existsSync(file)) return {};
  const out = {};
  for (const [from, to] of Object.entries(JSON.parse(readFileSync(file, "utf8")))) {
    if (!from.startsWith("/")) continue;
    if (typeof to !== "string" || !to.startsWith("/"))
      throw new Error(`src/redirects.json: ${from} redirects to ${JSON.stringify(to)}, not a path`);
    out[from] = to;
  }
  return out;
}

// Vite's content-hashed bundles (/install/assets/main-M8RBXfxV.js), as src/headers.ts names
// them: no link names one, and their names change with every change to the bundle, so the
// manifest leaves them out and stays the same while the pages do.
export const HASHED =
  /^\/(install\/)?assets\/.+-[A-Za-z0-9_-]{8,}\.(js|css|woff2?|svg|png|webp|avif)$/;

// manifestOf: the manifest of a built dist/: every file (but the hashed bundles), and every id
// on each HTML page.
export function manifestOf(dist, redirects = {}) {
  const files = [];
  const ids = {};
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.isFile()) {
        const rel = "/" + path.relative(dist, p).split(path.sep).join("/");
        if (HASHED.test(rel)) continue;
        files.push(rel);
        if (rel.endsWith(".html")) {
          const found = [
            ...readFileSync(p, "utf8").matchAll(/\s(?:id|name)\s*=\s*["']([^"']+)["']/g),
          ].map((m) => m[1]);
          if (found.length) ids[rel] = [...new Set(found)].sort();
        }
      }
    }
  };
  walk(dist);
  files.sort();
  return { files, ids, redirects };
}

// resolve: the manifest file a path opens (auto-trailing-slash), or null.
export function resolvePage(manifest, p) {
  const files = new Set(manifest.files);
  const clean = p.replace(/\/+$/, "");
  for (const cand of [p, clean, `${clean}/index.html`, `${clean}.html`]) {
    if (cand && files.has(cand)) return cand;
  }
  if (p === "/" && files.has("/index.html")) return "/index.html";
  return null;
}

// redirectTarget: where the Worker sends a path (the exact source, as worker.ts matches it),
// or null when it is not a redirect.
export function redirectTarget(manifest, p) {
  return Object.hasOwn(manifest.redirects, p) ? manifest.redirects[p] : null;
}

// a redirect whose target opens a page (a target that is itself a redirect is followed, up to
// a few hops, so a loop is no page)
function redirectOpens(manifest, p, hops = 5) {
  const to = redirectTarget(manifest, p);
  if (to === null) return false;
  if (resolvePage(manifest, to)) return true;
  return hops > 0 && redirectOpens(manifest, to, hops - 1);
}

const PROBLEM_RE = /^\/(?:ar\/)?problems\/([^/]+)\/?$/;

// check one link: null when it opens a page, else why not.
export function checkLink(l, slugs, manifest) {
  const pm = l.path.match(PROBLEM_RE);
  if (pm) {
    if (!slugs.has(pm[1])) return `no problem type "${pm[1]}" in shared/api/problems.json`;
    return null;
  }
  if (redirectTarget(manifest, l.path) !== null) {
    if (l.fragment) return "a redirect keeps no #fragment";
    if (!redirectOpens(manifest, l.path))
      return `redirects to ${redirectTarget(manifest, l.path)}, which is no page on maktaba.tv`;
    return null;
  }
  const page = resolvePage(manifest, l.path);
  if (!page) return "no such page on maktaba.tv";
  if (l.fragment && !(manifest.ids[page] || []).includes(l.fragment)) {
    return `no id "${l.fragment}" on ${page}`;
  }
  return null;
}

// ---- the built site's own links (story 60.08, run from _website.yml with --dist) ------------

const SITE_ATTR_RE = /\s(href|src)\s*=\s*"([^"]*)"|\s(href|src)\s*=\s*'([^']*)'/g;
const SITE_META_RE = /<meta\s[^>]*content\s*=\s*"(https:\/\/maktaba\.tv\/[^"]*)"/g;
const ID_RE = /\s(?:id|name)\s*=\s*["']([^"']+)["']/g;

function distFiles(dist) {
  const out = [];
  const walk = (dir) => {
    for (const e of readdirSync(dir, { withFileTypes: true })) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.isFile()) out.push("/" + path.relative(dist, p).split(path.sep).join("/"));
    }
  };
  walk(dist);
  return out;
}

/** A built page's twin in the other language (null for a 404 page, which has no address). */
export function twinPath(file) {
  if (/(^|\/)404\.html$/.test(file)) return null;
  const own = file.replace(/index\.html$/, "");
  return own.startsWith("/ar/") ? own.slice(3) || "/" : `/ar${own}`;
}

/**
 * The built site's broken links: every href and src (and every maktaba.tv URL a <meta> names,
 * the card images) on every page resolves to a file of dist/, with the Worker's
 * auto-trailing-slash rule; every #fragment names an id on its page; and every page with an
 * address links its twin in the other language. Returns ["<page>: <why>", ...].
 */
export function siteFindings(dist, redirects = {}) {
  const files = distFiles(dist);
  const all = { files, ids: {}, redirects };
  const html = files.filter((f) => f.endsWith(".html"));
  const text = {};
  for (const f of html) {
    text[f] = readFileSync(path.join(dist, f), "utf8");
    all.ids[f] = [...new Set([...text[f].matchAll(ID_RE)].map((m) => m[1]))];
  }
  const out = [];
  for (const f of html.sort()) {
    const base = new URL(f, "https://maktaba.tv");
    const links = [];
    for (const m of text[f].matchAll(SITE_ATTR_RE))
      links.push({ tag: m[1] || m[3], v: m[2] ?? m[4] });
    for (const m of text[f].matchAll(SITE_META_RE)) links.push({ tag: "meta", v: m[1] });
    const anchors = new Set();
    for (const { tag, v } of links) {
      const raw = v.replace(/&amp;/g, "&").trim();
      if (!raw || /^(mailto|tel|data|blob):/i.test(raw)) continue;
      let u;
      try {
        u = new URL(raw, base);
      } catch {
        out.push(`${f}: ${tag}="${v}" is not a URL`);
        continue;
      }
      if (!/^https?:$/.test(u.protocol) || !/^(www\.)?maktaba\.tv$/.test(u.hostname)) continue;
      const p = decodeURIComponent(u.pathname);
      const frag = decodeURIComponent(u.hash.slice(1));
      if (tag === "href") anchors.add(p.replace(/index\.html$/, ""));
      if (redirectTarget(all, p) !== null) {
        if (frag) out.push(`${f}: ${tag}="${v}": a redirect keeps no #fragment`);
        else if (!redirectOpens(all, p))
          out.push(`${f}: ${tag}="${v}": redirects to ${redirectTarget(all, p)}, no file in dist/`);
        continue;
      }
      const page = resolvePage(all, p);
      if (!page) {
        out.push(`${f}: ${tag}="${v}": no such file in dist/`);
        continue;
      }
      if (frag && !(all.ids[page] || []).includes(frag))
        out.push(`${f}: ${tag}="${v}": no id "${frag}" on ${page}`);
    }
    const twin = twinPath(f);
    if (twin && !anchors.has(twin) && !anchors.has(twin.replace(/\/$/, "")))
      out.push(`${f}: links no twin (${twin})`);
  }
  return out;
}

function distance(a, b) {
  const d = Array.from({ length: a.length + 1 }, (_, i) => [i]);
  for (let j = 1; j <= b.length; j++) d[0][j] = j;
  for (let i = 1; i <= a.length; i++) {
    for (let j = 1; j <= b.length; j++) {
      d[i][j] = Math.min(
        d[i - 1][j] + 1,
        d[i][j - 1] + 1,
        d[i - 1][j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1)
      );
    }
  }
  return d[a.length][b.length];
}

// nearest: the closest page (or registered problem) to a dead path, when one is close.
export function nearest(p, slugs, manifest) {
  const pages = new Set();
  for (const f of manifest.files) {
    if (!f.endsWith(".html")) continue;
    pages.add(f.endsWith("/index.html") ? f.slice(0, -"index.html".length) : f);
  }
  for (const s of slugs) pages.add(`/problems/${s}/`);
  const want = p.endsWith("/") || p.includes(".") ? p : `${p}/`;
  let best = null;
  let bestD = Infinity;
  for (const cand of pages) {
    const d = distance(want, cand);
    if (d < bestD) {
      best = cand;
      bestD = d;
    }
  }
  return best && bestD > 0 && bestD <= Math.max(2, Math.floor(want.length / 5)) ? best : null;
}

// The baseline's story for a dead link: the story whose page makes it live.
export function storyFor(p) {
  const rules = [
    [/^\/(ar\/)?problems(\/|$)/, "60.06"],
    [/^\/(ar\/)?install(\/|$)/, "60.05"],
    [/^\/(ar\/)?legal(\/|$)/, "60.07"],
    [/^\/(ar\/)?download(\/|$)/, "60.11"],
    [/^\/(ar\/)?help(\/|$)/, "60.12"],
    [/^\/(ar\/)?pricing(\/|$)/, "60.13"],
  ];
  for (const [re, story] of rules) if (re.test(p)) return story;
  return "60.03";
}

function trackedFiles(root) {
  const out = execFileSync("git", ["-C", root, "ls-files", "-z"], {
    maxBuffer: 1 << 28,
  }).toString();
  return out.split("\0").filter(Boolean);
}

function readText(root, file) {
  const p = path.join(root, file);
  let st;
  try {
    st = statSync(p);
  } catch {
    return null; // deleted in the work tree, or a broken link
  }
  if (!st.isFile() || st.size > 5 * 1024 * 1024) return null;
  const buf = readFileSync(p);
  if (buf.subarray(0, 8000).includes(0)) return null; // binary
  return buf.toString("utf8");
}

const key = (file, link) => `${file}\t${link}`;

// scan: every maktaba.tv link of the repository's checked files, with its verdict.
export function scan({ root, slugs, manifest, allow = [] }) {
  const allowed = new Map(allow.map((a) => [key(a.file, a.link), a]));
  const usedAllow = new Set();
  const links = [];
  for (const file of trackedFiles(root)) {
    if (excluded(file)) continue;
    const text = readText(root, file);
    if (text === null || !text.includes("maktaba.tv")) continue;
    for (const l of findLinks(text)) {
      const k = key(file, l.link);
      if (allowed.has(k)) {
        usedAllow.add(k);
        continue;
      }
      links.push({ file, ...l, problem: checkLink(l, slugs, manifest) });
    }
  }
  const staleAllow = allow.filter((a) => !usedAllow.has(key(a.file, a.link)));
  return { links, staleAllow };
}

// judge: the failures, given the scan and the baseline.
export function judge({ links, staleAllow }, baseline, slugs, manifest) {
  const inBaseline = new Set(baseline.map((b) => key(b.file, b.link)));
  const dead = [];
  const deadKeys = new Set();
  const seenKeys = new Set();
  for (const l of links) {
    const k = key(l.file, l.link);
    seenKeys.add(k);
    if (!l.problem) continue;
    deadKeys.add(k);
    if (!inBaseline.has(k)) dead.push({ ...l, near: nearest(l.path, slugs, manifest) });
  }
  const live = baseline.filter((b) => !deadKeys.has(key(b.file, b.link)));
  for (const b of live)
    b.why = seenKeys.has(key(b.file, b.link)) ? "now opens a page" : "no longer in the file";
  return { dead, live, staleAllow };
}

function loadJSON(file, fallback) {
  return existsSync(file) ? JSON.parse(readFileSync(file, "utf8")) : fallback;
}

function main(argv) {
  const args = { root: null, dist: null, writeManifest: null, prune: false, paths: false };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a === "--root") args.root = argv[++i];
    else if (a === "--dist") args.dist = argv[++i];
    else if (a === "--write-manifest") args.writeManifest = argv[++i];
    else if (a === "--prune-baseline") args.prune = true;
    else if (a === "--paths") args.paths = true;
    else {
      console.error(`links.mjs: unknown argument ${a}`);
      return 2;
    }
  }
  if (args.paths) {
    console.log([...EXCLUDED_PATHS, ...TEST_PATHS].join("\n"));
    return 0;
  }
  const root = path.resolve(
    args.root || execFileSync("git", ["-C", HERE, "rev-parse", "--show-toplevel"]).toString().trim()
  );
  const pkg = args.root ? path.join(root, "web/packages/website") : PACKAGE;
  const manifestFile = path.join(pkg, "dist-manifest.json");
  const baselineFile = path.join(pkg, "links-baseline.json");

  if (args.writeManifest) {
    const m = manifestOf(path.resolve(args.writeManifest), loadRedirects(pkg));
    writeFileSync(manifestFile, JSON.stringify(m, null, 2) + "\n");
    console.log(
      `links.mjs: wrote ${path.relative(process.cwd(), manifestFile)}: ${m.files.length} files`
    );
    return 0;
  }

  const slugs = loadSlugs(root);
  // the redirects are always the Worker's own table (a manifest's copy may lag behind it)
  const redirects = loadRedirects(pkg);
  const manifest = args.dist
    ? manifestOf(path.resolve(args.dist), redirects)
    : { ...loadManifest(manifestFile), redirects };
  const allow = loadJSON(path.join(pkg, "links-allow.json"), []);
  const baseline = loadJSON(baselineFile, []);
  for (const a of allow) {
    if (!a.reason || !String(a.reason).trim()) {
      console.error(
        `::error file=web/packages/website/links-allow.json::${a.file} ${a.link}: an allowed link needs a reason`
      );
      return 1;
    }
  }
  let site = [];
  if (args.dist) {
    site = siteFindings(path.resolve(args.dist), manifest.redirects);
    for (const x of site) console.log(`::error::the built site: ${x}`);
    console.log(`links.mjs: the built site: ${site.length} broken link(s)`);
  }
  const scanned = scan({ root, slugs, manifest, allow });
  const result = judge(scanned, baseline, slugs, manifest);

  if (args.prune) {
    const gone = new Set(result.live.map((b) => key(b.file, b.link)));
    const kept = baseline.filter((b) => !gone.has(key(b.file, b.link)));
    writeFileSync(baselineFile, JSON.stringify(kept, null, 2) + "\n");
    console.log(
      `links.mjs: the baseline lost ${baseline.length - kept.length} entries; ${kept.length} left`
    );
    return 0;
  }

  let fail = site.length ? 1 : 0;
  for (const d of result.dead) {
    const hint = d.near ? ` (did you mean https://maktaba.tv${d.near}?)` : "";
    console.log(
      `::error file=${d.file},line=${d.line}::${d.file}:${d.line}: ${d.link}: ${d.problem}${hint}`
    );
    fail = 1;
  }
  for (const b of result.live) {
    console.log(
      `::error file=web/packages/website/links-baseline.json::${b.file}: ${b.link} ${b.why}: ` +
        "remove it from links-baseline.json (node web/packages/website/scripts/links.mjs --prune-baseline)"
    );
    fail = 1;
  }
  for (const a of result.staleAllow) {
    console.log(
      `::error file=web/packages/website/links-allow.json::${a.file}: ${a.link} is no longer in the file: remove its entry`
    );
    fail = 1;
  }
  console.log(
    `links.mjs: ${scanned.links.length} links checked; ${result.dead.length} dead outside the baseline; ` +
      `${baseline.length} in the baseline (empty since 60.06: a new entry is reviewed like an allow-list entry)`
  );
  return fail;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2));
}
