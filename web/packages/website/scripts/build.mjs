// maktaba.tv's build (Epic 60 README §6.1, story 60.03): writes dist/, which the Worker
// (src/worker.ts) serves. In this order:
//
//   1. the install build (`pnpm -C web build:install`, Epic 59's guides): the contents of
//      web/dist-install/ (install/<guide>/, ar/install/<guide>/ and their hashed files under
//      install/assets/) copied to the root of dist/, without the placeholder figures whose
//      screenshot does not exist yet (story 60.05 T07);
//   2. the generated families (FAMILIES: /problems 60.06, /help 60.12, /download 60.11);
//   3. the hand-made pages (src/pages/, each wrapped in the shell, scripts/shell.mjs): the
//      front door, the install chooser (/install/, story 60.05) and the 404 page; and the
//      legal pages (story 60.07) from their copy and sources (scripts/legal.mjs: the privacy
//      page reads privacy.CurrentPolicy() through `go run ./cmd/privacy-json`, so the build
//      needs Go), with the copyright notice form only when WEBSITE_NOTICE_ENDPOINT and
//      WEBSITE_TURNSTILE_SITEKEY are set (src/copyright-form.ts; neither is, until Epic 50's
//      endpoint is live);
//   4. the copied files: static/, the icons and manifest (icons.mjs), the log schema at its
//      $id, security.txt;
//   5. what every page carries (story 60.04): the head tags an install guide's own <head>
//      lacks (head.mjs), a social card per page and language (cards.mjs), sitemap.xml and
//      robots.txt (sitemap.mjs);
//   6. the checks: every page has its twin in the other language, and an Arabic page is
//      Arabic (maktaba.dev's Latin-share check);
//   7. the link check's manifest (links.mjs --write-manifest dist, story 60.08): the committed
//      dist-manifest.json is the build's, and CI fails when it differs (`git diff`).
//
//   node --experimental-strip-types scripts/build.mjs [--no-install]
//
// --no-install skips step 1 for a quick look at the hand-made pages; CI and the deploy never
// pass it, and the twin check then sees no install pages. It also skips step 7, so a quick
// build never rewrites the committed manifest without the install guides.

import crypto from "node:crypto";
import fs from "node:fs";
import { stripTypeScriptTypes } from "node:module";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { page } from "./shell.mjs";
import { completeHead, readHead } from "./head.mjs";
import { cardPath, renderCard } from "./cards.mjs";
import { writeIcons } from "./icons.mjs";
import { ROBOTS, sitemapXml } from "./sitemap.mjs";
import { LEGAL, legalPages, readSources } from "./legal.mjs";
import { PROBLEMS } from "./problems.mjs";
import { noticeConfig } from "../src/copyright-form.ts";

const HERE = path.dirname(fileURLToPath(import.meta.url));
export const PKG = path.resolve(HERE, "..");
export const REPO = path.resolve(PKG, "../../..");
export const DIST = path.join(PKG, "dist");
export const ORIGINS = ["https://maktaba.tv", "https://maktaba.dev"];

/** The install build's output (web/vite.install.config.ts). */
export const INSTALL_OUT = path.join(REPO, "web/dist-install");

/**
 * The generated families, each `{ name, story, build({ write, repo, resolves, script }) }`, run
 * in this order after the install pages and before the hand-made pages: `resolves(href)` says
 * whether a site path (and its #fragment) is already built, so a family links only pages that
 * exist (the problem pages link a help topic once 60.12's pages are built: help.mjs goes before
 * problems.mjs), and `script(src)` publishes a script of src/ under a content-hashed name.
 * Stories 60.12 (help.mjs) and 60.11 (download.mjs) add theirs here.
 */
export const FAMILIES = [PROBLEMS];

/** The hand-made pages: their source in src/pages/ (or, for a legal page, its copy in
 * copy/legal/, scripts/legal.mjs), their English path (null: no address of its own), and the
 * file each becomes. */
export const PAGES = [
  { src: "index.html", lang: "en", path: "/", out: "index.html" },
  { src: "ar/index.html", lang: "ar", path: "/", out: "ar/index.html" },
  { src: "404.html", lang: "en", path: null, out: "404.html" },
  { src: "ar/404.html", lang: "ar", path: null, out: "ar/404.html" },
  { src: "install/index.html", lang: "en", path: "/install/", out: "install/index.html" },
  { src: "ar/install/index.html", lang: "ar", path: "/install/", out: "ar/install/index.html" },
  ...LEGAL.flatMap((name) =>
    ["en", "ar"].map((lang) => ({
      legal: name,
      lang,
      path: `/legal/${name}/`,
      out: `${lang === "ar" ? "ar/" : ""}legal/${name}/index.html`,
    }))
  ),
];

/** A page source: a leading comment of `key: value` lines (title, description, meta), then the
 * body. */
export function parsePage(text, name = "page") {
  const m = text.match(/^<!--\n([\s\S]*?)\n-->\n/);
  if (!m) throw new Error(`${name}: no leading <!-- title: … --> comment`);
  const fields = {};
  for (const line of m[1].split("\n")) {
    const i = line.indexOf(":");
    if (i < 0) throw new Error(`${name}: not a "key: value" line: ${line}`);
    fields[line.slice(0, i).trim()] = line.slice(i + 1).trim();
  }
  for (const k of ["title", "description"])
    if (!fields[k]) throw new Error(`${name}: no ${k} in its leading comment`);
  return { ...fields, body: text.slice(m[0].length) };
}

// ---- security.txt (rendered as maktaba.dev renders it) -----------------------------------

// RFC 9116: an Expires less than a year ahead (MaxExpiryMonths in disclosure.go).
export const MAX_EXPIRY_MONTHS = 11;

/** A rolling expiry: the start of `now`'s UTC day, `months` later (Go's time.Date overflow,
 * as maktaba.dev's build and DisclosurePolicy.ExpiresAt agree). */
export function expiresAt(now, months) {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + months, now.getUTCDate()));
}

/** The disclosure policy from the Go source the API serves (story 23.8), as it reads at
 * `now`. The same reader as web/packages/devportal/scripts/build.mjs. */
export function readDisclosurePolicy(now = new Date(), repo = REPO) {
  const go = fs.readFileSync(path.join(repo, "api/internal/security/disclosure.go"), "utf8");
  const start = go.indexOf("func DefaultPolicy()");
  if (start < 0) throw new Error("disclosure.go: no DefaultPolicy");
  const fn = go.slice(start, go.indexOf("\n}\n", start));
  const list = (field) => {
    const m = fn.match(new RegExp(`${field}:\\s*\\[\\]string\\{([^}]*)\\}`));
    return m ? [...m[1].matchAll(/"([^"]+)"/g)].map((x) => x[1]) : [];
  };
  const m = fn.match(/ExpiryMonths:\s*(\d+)/);
  if (!m) throw new Error("disclosure.go: no ExpiryMonths in DefaultPolicy");
  const months = +m[1];
  if (months < 1 || months > MAX_EXPIRY_MONTHS)
    throw new Error(`disclosure.go: ExpiryMonths ${months} is outside 1..${MAX_EXPIRY_MONTHS}`);
  return {
    contact: list("Contact"),
    expires: expiresAt(now, months),
    preferredLang: list("PreferredLang"),
    policy: list("Policy"),
    acknowledgments: list("Acknowledgments"),
  };
}

/** The file, with a Canonical line for each domain that serves it (maktaba.tv and
 * maktaba.dev serve the same policy). */
export function securityTxt(p, origins = ORIGINS) {
  if (!p.contact.length) throw new Error("security.txt: no Contact");
  return (
    [
      ...p.contact.map((c) => `Contact: ${c}`),
      `Expires: ${p.expires.toISOString().replace(/\.\d{3}Z$/, "Z")}`,
      ...p.acknowledgments.map((a) => `Acknowledgments: ${a}`),
      `Preferred-Languages: ${p.preferredLang.join(", ")}`,
      ...origins.map((o) => `Canonical: ${o}/.well-known/security.txt`),
      ...p.policy.map((u) => `Policy: ${u}`),
    ].join("\n") + "\n"
  );
}

export { ROBOTS };

// ---- the checks ---------------------------------------------------------------------------

function walk(dir, base = dir, out = []) {
  if (!fs.existsSync(dir)) return out;
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, base, out);
    else out.push(path.relative(base, p).split(path.sep).join("/"));
  }
  return out;
}

/** Each English page has its Arabic twin (x.html and ar/x.html), and the other way round. */
export function twinFindings(files) {
  const pages = new Set(files.filter((f) => f.endsWith(".html")));
  const out = [];
  for (const f of pages) {
    if (f.startsWith("ar/")) {
      if (!pages.has(f.slice(3))) out.push(`${f}: an Arabic page with no English twin`);
    } else if (!pages.has(`ar/${f}`)) out.push(`${f}: no Arabic twin (ar/${f})`);
  }
  return out.sort();
}

/** The Arabic pages whose prose is mostly Latin: maktaba.dev's check, as the install build
 * applies it (web/packages/install/src/latin.ts, which reads devportal's parity.mjs). */
export async function latinFindings(dist) {
  const { latinFindings: find } = await import("../../install/src/latin.ts");
  const pages = {};
  for (const f of walk(dist).filter((f) => f.startsWith("ar/") && f.endsWith(".html")))
    pages[f] = fs.readFileSync(path.join(dist, f), "utf8");
  return find(pages);
}

// ---- the pages -----------------------------------------------------------------------------

/** Every page of a built dist/ with an address of its own (the 404 pages have none): its file,
 * language, own path and English path. */
export function sitePages(files) {
  const out = [];
  for (const file of files) {
    if (!file.endsWith(".html") || /(^|\/)404\.html$/.test(file)) continue;
    const lang = file.startsWith("ar/") ? "ar" : "en";
    const own = `/${file.replace(/index\.html$/, "")}`;
    const path = lang === "ar" ? own.slice(3) || "/" : own;
    out.push({ file, lang, own, path });
  }
  return out.sort((a, b) => a.file.localeCompare(b.file));
}

// ---- the build ------------------------------------------------------------------------------

function writer(dist) {
  return (rel, content) => {
    const p = path.join(dist, rel);
    if (fs.existsSync(p)) throw new Error(`${rel}: written twice (two steps make the same file)`);
    fs.mkdirSync(path.dirname(p), { recursive: true });
    fs.writeFileSync(p, content);
  };
}

function copyTree(from, dist, write, html = (s) => s) {
  for (const rel of walk(from)) {
    const buf = fs.readFileSync(path.join(from, rel));
    write(rel, rel.endsWith(".html") ? html(buf.toString("utf8"), rel) : buf);
  }
}

// ---- the install guides' screenshots still to come (story 60.05 T07) ---------------------

export const INSTALL_PKG = path.join(REPO, "web/packages/install");

const PLACEHOLDER =
  /[ \t]*<figure class="placeholder" data-shot="([^"]+)">[\s\S]*?<\/figure>[ \t]*\n?/g;

/** A guide without each placeholder figure ("Screenshot to come") whose image does not exist
 * yet: a public page shows no "to come" box. The step's text stays, and the figure is kept
 * once its image exists. The install package's own preview keeps every placeholder. */
export function dropMissingFigures(html, exists) {
  return html.replace(PLACEHOLDER, (figure, shot) => (exists(shot) ? figure : ""));
}

/** Whether a placeholder's image (data-shot, from the install site's root) exists: in the
 * install package's site or public files, or in the install build. */
export function shotExists(
  shot,
  roots = [
    path.join(INSTALL_PKG, "site"),
    path.join(INSTALL_PKG, "public"),
    path.join(INSTALL_PKG, "public/install"),
    INSTALL_OUT,
  ]
) {
  if (shot.includes("..")) return false;
  return roots.some((r) => fs.existsSync(path.join(r, shot)));
}

// ---- the copyright notice form's script (story 60.07) ---------------------------------------

/** A script of src/ (TypeScript that imports nothing) with its types stripped, under a
 * content-hashed name: { file: "assets/<name>-<hash>.js", js }. */
export function scriptFile(src) {
  const ts = fs.readFileSync(path.join(PKG, "src", src), "utf8");
  const js = stripTypeScriptTypes(ts);
  const hash = crypto.createHash("sha256").update(js).digest("base64url").slice(0, 10);
  return { file: `assets/${src.replace(/\.ts$/, "")}-${hash}.js`, js };
}

/** src/copyright-form.ts with its types stripped, under a content-hashed name. */
export const noticeScriptFile = () => scriptFile("copyright-form.ts");

/** Whether a site path (/x/, /ar/x/#id) opens a page of dist/ as built so far, with the
 * Worker's auto-trailing-slash rule, and its #fragment names an id on that page. */
export function resolver(dist) {
  return (href) => {
    const u = new URL(href, "https://maktaba.tv");
    const p = decodeURIComponent(u.pathname).replace(/^\/+/, "");
    if (p.split("/").includes("..")) return false;
    const file = [p, `${p.replace(/\/$/, "")}/index.html`, `${p}.html`]
      .map((f) => path.join(dist, f))
      .find((f) => fs.existsSync(f) && fs.statSync(f).isFile());
    if (!file) return false;
    const id = decodeURIComponent(u.hash.slice(1));
    if (!id) return true;
    const html = fs.readFileSync(file, "utf8");
    return [...html.matchAll(/\s(?:id|name)\s*=\s*["']([^"']+)["']/g)].some((m) => m[1] === id);
  };
}

export async function build({
  now = new Date(),
  install = true,
  dist = DIST,
  log = console.log,
  notice = noticeConfig(process.env),
} = {}) {
  fs.rmSync(dist, { recursive: true, force: true });
  fs.mkdirSync(dist, { recursive: true });
  const write = writer(dist);

  // 1. the install guides
  if (install) {
    log("install build: pnpm -C web build:install");
    execFileSync("pnpm", ["-C", path.join(REPO, "web"), "build:install"], { stdio: "inherit" });
    copyTree(INSTALL_OUT, dist, write, (html) => dropMissingFigures(html, (s) => shotExists(s)));
  }

  // 2. the generated families
  const resolves = resolver(dist);
  const script = (src) => {
    const s = scriptFile(src);
    write(s.file, s.js);
    return `/${s.file}`;
  };
  for (const f of FAMILIES) {
    const n = await f.build({ write, repo: REPO, resolves, script });
    log(`family: ${f.name} (${f.story}): ${n} pages`);
  }

  // 3. the hand-made pages, and the legal pages from their copy (legal.mjs). The copyright
  // page's notice form is there only when its endpoint is configured (copyright-form.ts).
  let noticeScript = "";
  if (notice) {
    const s = noticeScriptFile();
    write(s.file, s.js);
    noticeScript = `/${s.file}`;
    log(`the copyright notice form posts to ${notice.endpoint}`);
  }
  const legal = legalPages({ sources: readSources(), notice, noticeScript });
  for (const p of PAGES) {
    const src = p.legal
      ? legal.find((l) => l.name === p.legal && l.lang === p.lang)
      : parsePage(fs.readFileSync(path.join(PKG, "src/pages", p.src), "utf8"), p.src);
    write(
      p.out,
      page({
        lang: p.lang,
        path: p.path,
        title: src.title,
        description: src.description,
        body: src.body,
        meta: src.meta,
      })
    );
  }

  // 4. the copied files
  copyTree(path.join(PKG, "static"), dist, write);
  const schema = fs.readFileSync(path.join(REPO, "shared/log/schema/line.schema.json"));
  const id = JSON.parse(schema.toString("utf8")).$id;
  if (id !== "https://maktaba.tv/schema/log-line.json")
    throw new Error(`the log schema's $id is ${id}, not https://maktaba.tv/schema/log-line.json`);
  write("schema/log-line.json", schema);
  const policy = readDisclosurePolicy(now);
  if (policy.expires <= now) throw new Error("security.txt: Expires is not ahead");
  write(".well-known/security.txt", securityTxt(policy));
  writeIcons(REPO, write);

  // 5. what every page carries: the head tags, a card, the sitemap
  const pages = sitePages(walk(dist));
  for (const p of pages) {
    const file = path.join(dist, p.file);
    const html = fs.readFileSync(file, "utf8");
    const head = readHead(html, p.file);
    if (head.lang !== p.lang)
      throw new Error(`${p.file}: <html lang="${head.lang}">, not ${p.lang}`);
    const done = completeHead(html, { ...p, ...head });
    if (done !== html) fs.writeFileSync(file, done);
    write(cardPath(p.lang, p.path).slice(1), await renderCard({ lang: p.lang, title: head.title }));
  }
  write("sitemap.xml", sitemapXml(pages.map((p) => p.path)));
  write("robots.txt", ROBOTS);

  // 6. the checks
  const findings = [...twinFindings(walk(dist)), ...(await latinFindings(dist))];
  if (findings.length)
    throw new Error(`the site has ${findings.length} problem(s):\n${findings.join("\n")}`);
  log(`dist/: ${walk(dist).length} files, ${pages.length} pages`);

  // 7. the link check's manifest (only for a whole build)
  if (install && dist === DIST) {
    execFileSync(process.execPath, [path.join(HERE, "links.mjs"), "--write-manifest", dist], {
      stdio: "inherit",
    });
  }
}

if (import.meta.url === `file://${process.argv[1]}`) {
  build({ install: !process.argv.includes("--no-install") }).catch((e) => {
    console.error(e.message);
    process.exit(1);
  });
}
