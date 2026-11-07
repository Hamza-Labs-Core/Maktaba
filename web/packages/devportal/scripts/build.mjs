// Assembles maktaba.dev into dist/ (wrangler.toml's [assets] directory).
//
// The site is public, so this is an ALLOW-LIST: nothing reaches dist/ unless it is named
// here. A new doc is published by adding it to DOCS, never by being in a folder. After
// assembling, the public-safety scan (scan.mjs) runs over dist/ and the build fails on any
// finding.
//
// The site carries what outside developers need to extend Maktaba or contribute to it: the
// wiki (every epic, story and plan, built from web/wiki-app), the plugin guides, the
// contributing guides, security, the API reference and the changelog.
//
// Every page is in English and Arabic: /x/ in English, /ar/x/ in Arabic (right to left), each
// linking the other. A guide's Arabic is its twin file (name.ar.md), the changelog's is
// CHANGELOG.ar.md, the API reference's prose is shared/api/openapi.ar.json, the wiki's is its
// Arabic database (web/wiki-app/public/db/ar/, generated from the translation store in
// docs/wiki/db/ar/), and the portal's own pages are scripts/strings.mjs. The parity checks
// (scripts/parity.mjs) fail the build when the two languages drift.
//
//   npm run build            (web/wiki-app needs `npm ci` once first)

import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Marked } from "marked";
// the plugin SDK's flow chart (story 54.41), the one layout every place draws a flow with;
// TypeScript with nothing but erasable types, so Node reads it with type stripping
const { renderSvg } = await import("../../../../tools/plugin-sdk/js/src/flowchart.ts");
import { STRINGS } from "./strings.mjs";
import { APPS, appShots } from "./apps.mjs";
import { pluginFindings, pluginReference, readPluginSources } from "./plugins.mjs";
import { scanDir } from "./scan.mjs";
import { ORIGIN, isSafeUrl } from "./urls.mjs";
import {
  applyOverlay,
  arabicOf,
  checkApps,
  checkArabicMarkdown,
  checkChangelog,
  checkGuides,
  checkOverlay,
  checkPages,
  checkWiki,
} from "./parity.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
export const REPO = path.resolve(PKG, "../../..");
const DIST = path.join(PKG, "dist");
const STAGE = path.join(PKG, ".stage");
const WIKI_APP = path.join(REPO, "web/wiki-app");
const WIKI_DB = "web/wiki-app/public/db";

export { ORIGIN };
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";

// ---- the allow-lists --------------------------------------------------------------------

// Markdown published as pages: repo path -> site path (a folder, served as its index.html).
// The guides are in docs/developers/, each with its Arabic twin (name.ar.md); each group's
// README is its index page.
const GUIDES = {
  plugins: [
    "README.md",
    "manifest.md",
    "sandbox.md",
    "queue-and-api.md",
    "flows.md",
    "installing.md",
    "testing-and-packaging.md",
    "trust.md",
    "publishing.md",
    "selling.md",
    "first-party.md",
  ],
  contributing: [
    "README.md",
    "architecture.md",
    "repo-layout.md",
    "local-setup.md",
    "tests.md",
    "conventions.md",
    "ci.md",
    "pull-requests.md",
    "cloud.md",
  ],
  security: ["conformance.md"],
};
export const DOCS = [
  ...Object.entries(GUIDES).flatMap(([group, files]) =>
    files.map((file) => ({
      src: `docs/developers/${group}/${file}`,
      ar: arabicOf(`docs/developers/${group}/${file}`),
      out: file === "README.md" ? `${group}/` : `${group}/${file.replace(/\.md$/, "")}/`,
      group,
    }))
  ),
  // generated from the plugin system's own files (scripts/plugins.mjs), after the guides
  {
    src: "generated:plugins/reference",
    ar: "generated:plugins/reference.ar",
    out: "plugins/reference/",
    group: "plugins",
    gen: (lang) => pluginReference(readPluginSources(REPO), lang),
    source: "shared/plugins",
  },
  {
    src: "CHANGELOG.md",
    ar: "CHANGELOG.ar.md",
    out: "changelog/",
    titleKey: "changelog.title",
    group: "changelog",
  },
];

// The languages, and the path prefix each one's pages live under.
export const LANGS = ["en", "ar"];
const PREFIX = { en: "", ar: "/ar" };
const DIR = { en: "ltr", ar: "rtl" };
/** A page's path in a language: "/plugins/" -> "/ar/plugins/". */
export const localPath = (lang, p) => PREFIX[lang] + p;

// The wiki's build output: only these shapes are published (relative to the wiki's outDir),
// each at the site path it maps to. The English database is /wiki/db/, the Arabic one
// /ar/wiki/db/; /ar/wiki/ is the same bundle, which picks its language from the path. No
// mockups: their sample data (home paths, LAN addresses) is not for a public site, so the
// wiki is built with VITE_WIKI_NO_MOCKUPS=1 and links each mockup's source instead.
const WIKI_DB_FILE = String.raw`(?:wiki|wiki-cross-refs|wiki-epics-\d\d-\d\d)\.json`;
export const WIKI_ALLOW = [
  [/^index\.html$/, (rel) => `wiki/${rel}`],
  [/^assets\/[A-Za-z0-9._-]+\.(?:js|css|woff2?|svg|png)$/, (rel) => `wiki/${rel}`],
  [new RegExp(`^db/${WIKI_DB_FILE}$`), (rel) => `wiki/${rel}`],
  [new RegExp(`^db/ar/${WIKI_DB_FILE}$`), (rel) => `ar/wiki/db/${rel.slice("db/ar/".length)}`],
  [/^diagrams\/[A-Za-z0-9/_.-]+\.(?:drawio|svg|png)$/, (rel) => `wiki/${rel}`],
];
/** Where a file of the wiki's build is published (a dist/ path), or null when it is not. */
export function wikiSitePath(rel) {
  for (const [re, to] of WIKI_ALLOW) if (re.test(rel)) return to(rel);
  return null;
}

/**
 * The Arabic wiki page (/ar/wiki/) from the English one: the same bundle (it reads the path
 * and loads /ar/wiki/db/), with the page's own text in Arabic and right to left from the
 * first paint, and each page naming the other for crawlers.
 */
export function arabicWikiIndex(html) {
  const swap = (re, to, what) => {
    if (!re.test(html)) throw new Error(`build: the wiki's index.html has no ${what}`);
    html = html.replace(re, to);
  };
  swap(/<html lang="en" dir="ltr">/, '<html lang="ar" dir="rtl">', "<html lang dir>");
  swap(/<title>[^<]*<\/title>/, `<title>${esc(tr("ar", "wiki.title"))}</title>`, "<title>");
  swap(
    /<meta name="description" content="[^"]*"/,
    `<meta name="description" content="${esc(tr("ar", "wiki.description"))}"`,
    "description"
  );
  swap(
    /<noscript>[\s\S]*?<\/noscript>/,
    `<noscript>${esc(tr("ar", "wiki.noscript"))}</noscript>`,
    "<noscript>"
  );
  return wikiAlternates(html, "/ar/wiki/");
}

/** A wiki page's canonical link and its two language alternates. */
export function wikiAlternates(html, here) {
  const links =
    `\n    <link rel="canonical" href="${ORIGIN}${here}" />` +
    `\n    <link rel="alternate" hreflang="en" href="${ORIGIN}/wiki/" />` +
    `\n    <link rel="alternate" hreflang="ar" href="${ORIGIN}/ar/wiki/" />`;
  if (!html.includes("</head>")) throw new Error("build: the wiki's index.html has no </head>");
  return html.replace("</head>", `${links}\n  </head>`);
}

// Repo files copied as they are: repo path -> site path.
const FILES = [
  ["shared/api/openapi.json", "api/openapi.json"],
  // the manifest schema at its own $id (https://maktaba.dev/plugins/manifest.schema.json)
  ["shared/plugins/manifest.schema.json", "plugins/manifest.schema.json"],
  // the panel schema at its own $id (https://maktaba.dev/plugins/panel.schema.json, story 54.20)
  ["shared/plugins/panel.schema.json", "plugins/panel.schema.json"],
  // ar/api/openapi.json is written by the build: openapi.json with openapi.ar.json's prose
  [
    "web/packages/devportal/node_modules/redoc/bundles/redoc.standalone.js",
    "api/redoc.standalone.js",
  ],
  [
    "web/packages/devportal/node_modules/redoc/bundles/redoc.standalone.js.LICENSE.txt",
    "api/redoc.standalone.js.LICENSE.txt",
  ],
  ["web/public/icons/favicon-32.png", "assets/favicon-32.png"],
  ["web/public/icons/apple-touch-icon.png", "assets/apple-touch-icon.png"],
];

// Never published, whatever a list above says (a second check on the lists themselves).
const NEVER = [/claude-trail/, /^docs\/audits\//, /^docs\/security\/advisories\//, /\.env/];

// ---- scrubbing source-language markdown ---------------------------------------------------

// A changelog or spec bullet that points into internal material is dropped whole; a personal
// machine name in an example is replaced.
const DROP = [/claude-trail/i, /docs\/audits\//, /\/Users\//];
const LOCAL_HOST = /\b[A-Za-z0-9_][A-Za-z0-9_-]*\.local\b/g;
const KEEP_HOSTS = new Set(["maktaba.local", "tcp.local", "_tcp.local"]);

/** Drop the blocks (top-level list items or paragraphs) that mention internal material. */
export function scrubMarkdown(md) {
  const lines = md.split("\n");
  const blocks = [];
  let cur = [];
  let fence = false;
  const flush = () => {
    if (cur.length) blocks.push(cur.join("\n"));
    cur = [];
  };
  for (const line of lines) {
    if (/^\s*```/.test(line)) fence = !fence;
    const startsItem = !fence && /^[-*] |^\d+\. /.test(line);
    const startsBlock = !fence && (/^#{1,6} /.test(line) || line.trim() === "");
    if (startsItem || startsBlock) flush();
    cur.push(line);
    if (startsBlock && line.trim() !== "" && /^#/.test(line)) flush();
  }
  flush();
  return blocks
    .filter((b) => !DROP.some((re) => re.test(b)))
    .join("\n")
    .replace(LOCAL_HOST, (h) => (KEEP_HOSTS.has(h.toLowerCase()) ? h : "[name].local"));
}

// ---- pages ------------------------------------------------------------------------------

const tr = (lang, k) => {
  const v = STRINGS[lang][k];
  if (v === undefined) throw new Error(`no ${lang} string ${k}`);
  return v;
};
const esc = (s) =>
  String(s).replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]
  );

// `/assets/x.js?v=<hash of its content>`: the files cache for an hour, so a page names the
// exact version it was built with and a deploy is seen at once. The file is in dist/ already.
const v = (sitePath) => {
  const file = path.join(DIST, sitePath);
  if (!fs.existsSync(file)) throw new Error(`build: a page links ${sitePath}, which is not built`);
  const h = createHash("sha256").update(fs.readFileSync(file)).digest("hex").slice(0, 10);
  return `${sitePath}?v=${h}`;
};

const NAV = [
  ["nav.home", "/"],
  ["nav.apps", "/apps/"],
  ["nav.wiki", "/wiki/"],
  ["nav.plugins", "/plugins/"],
  ["nav.contributing", "/contributing/"],
  ["nav.api", "/api/"],
  ["nav.changelog", "/changelog/"],
  ["nav.security", "/security/policy/"],
  ["nav.source", GITHUB],
];

/**
 * A page in one language. `sitePath` is the page's English path ("/plugins/"); the Arabic page
 * is the same under /ar/, right to left, and each links the other (the language switch, and
 * rel=alternate for crawlers).
 */
export function page({ lang, path: sitePath, titleKey, title, body, head = "" }) {
  const t = (k) => tr(lang, k);
  const other = lang === "ar" ? "en" : "ar";
  const here = localPath(lang, sitePath);
  const current = (href) =>
    href === "/" ? sitePath === "/" : href.startsWith("/") && sitePath.startsWith(href);
  const nav = NAV.map(([k, href]) => {
    const to = href.startsWith("/") ? localPath(lang, href) : href;
    return `<a href="${to}"${current(href) ? ' aria-current="page"' : ""}>${esc(t(k))}</a>`;
  }).join("\n        ");
  const docTitle = titleKey ? t(titleKey) : title;
  const alt = (l) =>
    `\n  <link rel="alternate" hreflang="${l}" href="${ORIGIN}${localPath(l, sitePath)}">`;
  return `<!doctype html>
<html lang="${lang}" dir="${DIR[lang]}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(docTitle)} · maktaba.dev</title>
  <meta name="description" content="${esc(t("home.lead"))}">
  <link rel="canonical" href="${ORIGIN}${here}">${alt("en")}${alt("ar")}
  <link rel="icon" type="image/png" sizes="32x32" href="/assets/favicon-32.png">
  <link rel="apple-touch-icon" href="/assets/apple-touch-icon.png">
  <link rel="stylesheet" href="${v("/assets/portal.css")}">${head}
</head>
<body>
  <a class="skip" href="#content">${esc(t("skip"))}</a>
  <header class="top">
    <div class="wrap">
      <a class="brand" href="${localPath(lang, "/")}"><img src="/assets/favicon-32.png" alt=""><span>${esc(t("site.name"))}</span></a>
      <nav class="main" aria-label="maktaba.dev">
        ${nav}
      </nav>
      <a id="lang-switch" class="lang" href="${localPath(other, sitePath)}" hreflang="${other}" lang="${other}" aria-label="${esc(t("lang.toggle.label"))}">${esc(t("lang.toggle"))}</a>
    </div>
  </header>
  <main id="content" class="wrap">
${body}
  </main>
  <footer>
    <div class="wrap">
      <span>${esc(t("footer.text"))}</span>
      <a href="${localPath(lang, "/security/policy/")}">${esc(t("footer.security"))}</a>
      <a href="${GITHUB}">github.com/Hamza-Labs-Core/Maktaba</a>
    </div>
  </footer>
</body>
</html>
`;
}

/** The helpers a page body is written with, in one language. */
function inLang(lang) {
  const t = (k) => tr(lang, k);
  const s = (k, tag = "span", attrs = "") => `<${tag}${attrs}>${esc(t(k))}</${tag}>`;
  const href = (p) => (p.startsWith("/") ? localPath(lang, p) : p);
  const card = (key, to) =>
    `      <li><a href="${href(to)}"><strong>${esc(t(`card.${key}.title`))}</strong>` +
    `<span>${esc(t(`card.${key}.body`))}</span></a></li>`;
  return { t, s, href, card };
}

function landing(lang) {
  const { t, s, href, card } = inLang(lang);
  return page({
    lang,
    path: "/",
    titleKey: "home.title",
    body: `    <section class="hero">
      ${s("home.title", "h1")}
      ${s("home.lead", "p")}
      ${s("home.lead2", "p")}
      <div class="ctas">
        <a class="btn primary" href="${GITHUB}">${esc(t("home.cta.source"))}</a>
        <a class="btn" href="${href("/wiki/")}">${esc(t("home.cta.wiki"))}</a>
        <a class="btn" href="${href("/plugins/")}">${esc(t("home.cta.plugins"))}</a>
        <a class="btn" href="${href("/contributing/")}">${esc(t("home.cta.contributing"))}</a>
      </div>
    </section>
    ${s("home.cards", "h2")}
    <ul class="cards">
${card("apps", "/apps/")}
${card("wiki", "/wiki/")}
${card("plugins", "/plugins/")}
${card("contributing", "/contributing/")}
${card("api", "/api/")}
${card("changelog", "/changelog/")}
${card("security", "/security/policy/")}
${card("source", GITHUB)}
${card("specs", `${GITHUB}/tree/main/specs/epics`)}
    </ul>
    ${s("home.lang.note", "p", ' class="note"')}`,
  });
}

function notFound(lang) {
  const { t, s, href } = inLang(lang);
  return page({
    lang,
    path: "/404",
    titleKey: "404.title",
    body: `    ${s("404.title", "h1")}
    ${s("404.body", "p")}
    <p><a class="btn primary" href="${href("/")}">${esc(t("404.home"))}</a></p>`,
  });
}

function securityPolicy(lang) {
  const { t, s, href } = inLang(lang);
  const sec = (k) => `    ${s(`security.${k}.title`, "h2")}\n    ${s(`security.${k}.body`, "p")}`;
  return page({
    lang,
    path: "/security/policy/",
    titleKey: "security.title",
    body: `    ${s("security.title", "h1")}
    ${s("security.lead", "p")}
${sec("report")}
${sec("after")}
${sec("rules")}
    ${s("security.conform.title", "h2")}
    ${s("security.conform.body", "p")}
    <p><a href="${href("/security/conformance/")}">${esc(t("security.conform.link"))}</a></p>
    <p class="note"><a href="/.well-known/security.txt">${esc(t("security.txt"))}</a></p>`,
  });
}

function hallOfFame(lang) {
  const { s } = inLang(lang);
  return page({
    lang,
    path: "/security/hall-of-fame/",
    titleKey: "hof.title",
    body: `    ${s("hof.title", "h1")}
    ${s("hof.body", "p")}
    ${s("hof.empty", "p", ' class="note"')}`,
  });
}

// The API reference: Redoc over the OpenAPI description; the Arabic page reads
// ar/api/openapi.json, the same description with its prose in Arabic (openapi.ar.json).
// Redoc's three panels stay left to right (its layout does not mirror); on the Arabic page
// its rendered prose reads right to left (portal.css, #redoc[lang="ar"]).
function apiPage(lang) {
  const { t, s } = inLang(lang);
  const spec = `${localPath(lang, "/api/")}openapi.json`;
  return page({
    lang,
    path: "/api/",
    titleKey: "api.title",
    head: `\n  <script src="${v("/api/redoc.standalone.js")}" defer></script>\n  <script src="${v("/api/init.js")}" defer></script>`,
    body: `    ${s("api.title", "h1")}
    ${s("api.lang", "p", ' class="note"')}
    <p><a href="${spec}">${esc(t("api.download"))}</a></p>
    <div id="redoc" data-spec="${spec}" dir="ltr" lang="${lang}">${s("api.loading", "p")}</div>`,
  });
}

// ---- the Apps pages (scripts/apps.mjs) --------------------------------------------------

// The screenshots: static/assets/apps/<name>-<lang>.webp, or <name>.webp when the app was shot
// in one language only (the name then ends in -en or -ar).
export const APP_IMAGES = path.join(PKG, "static/assets/apps");

/** The site path of a screenshot in a language, or null when there is no such image. */
export function appImage(file, lang) {
  for (const name of [`${file}-${lang}.webp`, `${file}.webp`]) {
    if (fs.existsSync(path.join(APP_IMAGES, name))) return `/assets/apps/${name}`;
  }
  return null;
}

/** A WebP's width and height (from its VP8, VP8L or VP8X header), or null. */
export function webpSize(buf) {
  if (buf.length < 30) return null;
  if (buf.toString("ascii", 0, 4) !== "RIFF" || buf.toString("ascii", 8, 12) !== "WEBP")
    return null;
  const kind = buf.toString("ascii", 12, 16);
  if (kind === "VP8X") return { w: 1 + buf.readUIntLE(24, 3), h: 1 + buf.readUIntLE(27, 3) };
  if (kind === "VP8 ")
    return { w: buf.readUInt16LE(26) & 0x3fff, h: buf.readUInt16LE(28) & 0x3fff };
  if (kind === "VP8L") {
    const b = buf.readUInt32LE(21);
    return { w: 1 + (b & 0x3fff), h: 1 + ((b >> 14) & 0x3fff) };
  }
  return null;
}

/** An Apps page's inline markup, after escaping: **bold**, `code` and [text](href). */
export function inline(text, lang) {
  return esc(text)
    .replace(/`([^`]+)`/g, '<code dir="ltr">$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, (_, label, href) => {
      const raw = href.replace(/&amp;/g, "&");
      if (!isSafeUrl(raw)) return label;
      return `<a href="${raw.startsWith("/") ? localPath(lang, raw) : href}">${label}</a>`;
    });
}
const plain = (text) => text.replace(/\*\*|`/g, "").replace(/\[([^\]]+)\]\([^)]*\)/g, "$1");

function appShotsHtml(shots, lang) {
  // consecutive shots of one kind share a grid: phones side by side, wide ones a row each
  const groups = [];
  for (const sh of shots) {
    if (groups.length && groups.at(-1).kind === sh.kind) groups.at(-1).items.push(sh);
    else groups.push({ kind: sh.kind, items: [sh] });
  }
  return groups
    .map(({ kind, items }) => {
      const figs = items.map((sh) => {
        const src = appImage(sh.file, lang);
        if (!src)
          throw new Error(`build: the Apps pages show ${sh.file}, with no image in ${lang}`);
        const size = webpSize(fs.readFileSync(path.join(PKG, "static", src)));
        if (!size) throw new Error(`build: ${src} is not a WebP image`);
        const alt = esc(sh.alt[lang]);
        return (
          `        <figure><img src="${src}" alt="${alt}" width="${size.w}" height="${size.h}"` +
          ` loading="lazy" decoding="async"><figcaption>${alt}</figcaption></figure>`
        );
      });
      return `      <div class="shots ${kind}">\n${figs.join("\n")}\n      </div>`;
    })
    .join("\n");
}

// a table whose rows become cards on a phone (portal.css, table.stack): each cell carries its
// column's name for that
function appTable({ head, rows }, lang) {
  const th = head.map((c) => `<th scope="col">${inline(c[lang], lang)}</th>`).join("");
  const td = (c, i) =>
    `<td data-label="${esc(plain(head[i][lang]))}">${inline(c[lang], lang)}</td>`;
  const trs = rows.map((r) => `<tr>${r.map(td).join("")}</tr>`).join("\n        ");
  return `      <table class="stack">\n        <thead><tr>${th}</tr></thead>\n        <tbody>\n        ${trs}\n        </tbody>\n      </table>`;
}

/** The site path of an Apps page: "" -> /apps/, "player" -> /apps/player/. */
export const appPath = (slugName) => (slugName ? `/apps/${slugName}/` : "/apps/");

function appPage(lang, pg) {
  const { t } = inLang(lang);
  const block = (b) => {
    if (b.p) return `      <p>${inline(b.p[lang], lang)}</p>`;
    if (b.ul)
      return `      <ul>\n${b.ul.map((li) => `        <li>${inline(li[lang], lang)}</li>`).join("\n")}\n      </ul>`;
    if (b.table) return appTable(b.table, lang);
    if (b.shots) return appShotsHtml(b.shots, lang);
    throw new Error(`build: an Apps block of no known kind on ${appPath(pg.slug)}`);
  };
  const sections = pg.sections
    .map(
      (s) =>
        `    <section>\n      <h2 id="${slug(s.h.en)}">${inline(s.h[lang], lang)}</h2>\n` +
        `${s.blocks.map(block).join("\n")}\n    </section>`
    )
    .join("\n");
  const href = (p) => localPath(lang, appPath(p.slug));
  const cards = pg.slug
    ? ""
    : `\n    <h2>${esc(t("apps.pages"))}</h2>\n    <ul class="cards">\n` +
      APPS.filter((p) => p.slug)
        .map(
          (p) =>
            `      <li><a href="${href(p)}"><strong>${esc(plain(p.title[lang]))}</strong>` +
            `<span>${esc(p.card[lang])}</span></a></li>`
        )
        .join("\n") +
      "\n    </ul>";
  const toc = APPS.map(
    (p) =>
      `<li><a href="${href(p)}"${p === pg ? ' aria-current="page"' : ""}>${esc(plain(p.title[lang]))}</a></li>`
  ).join("\n");
  const article =
    `<article class="apps">\n    <h1>${esc(plain(pg.title[lang]))}</h1>\n` +
    `    <p class="lead">${inline(pg.lead[lang], lang)}</p>${cards}\n${sections}\n</article>`;
  return page({
    lang,
    path: appPath(pg.slug),
    title: plain(pg.title[lang]),
    body: `<div class="layout">\n<nav class="toc" aria-label="${esc(t("apps.toc"))}"><ul>\n${toc}\n</ul></nav>\n<div>\n${article}\n</div>\n</div>`,
  });
}

// ---- markdown docs ----------------------------------------------------------------------

function siteUrl(out, lang = "en") {
  return localPath(lang, "/" + out);
}

/**
 * A heading's id, GitHub-style, from its markdown source: letters, digits and hyphens only
 * (markup and punctuation dropped), so it is safe in an attribute as it is.
 */
export function slug(markdown) {
  return markdown
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .trim()
    .replace(/\s+/g, "-");
}

/** The link a doc's relative href becomes: a published page, else the file on GitHub. */
export function rewriteHref(href, fromSrc, published) {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("#") || href.startsWith("//"))
    return href;
  const [p, hash] = href.split("#");
  const target = path.posix.normalize(path.posix.join(path.posix.dirname(fromSrc), p));
  if (target.startsWith("..")) return null;
  if (NEVER.some((re) => re.test(target))) return null;
  if (published.has(target)) {
    const to = published.get(target);
    return (to.startsWith("/") ? to : siteUrl(to)) + (hash ? `#${hash}` : "");
  }
  const kind = p.endsWith("/") || !path.posix.extname(p) ? "tree" : "blob";
  return `${GITHUB}/${kind}/main/${target}` + (hash ? `#${hash}` : "");
}

/**
 * Markdown to HTML with raw HTML off: an HTML block or tag in the source is shown as escaped
 * text (`<version>` in the changelog reads as written), never passed into the page. Links
 * are rewritten (rewriteHref), and only http(s), mailto and in-site links are kept.
 */
export function markdownToHtml(md, fromSrc, published) {
  const marked = new Marked({ gfm: true });
  marked.use({
    renderer: {
      html({ text }) {
        return esc(text);
      },
      link({ href, title, tokens }) {
        const text = this.parser.parseInline(tokens);
        // the source's own href and the one it is rewritten to must both be allowed
        if (!isSafeUrl(href)) return text;
        const to = rewriteHref(href, fromSrc, published);
        if (to === null || !isSafeUrl(to)) return text;
        return `<a href="${esc(to)}"${title ? ` title="${esc(title)}"` : ""}>${text}</a>`;
      },
      // a heading's id is its text, so in-page links (#5-the-no-internet-guarantee) work
      heading({ tokens, depth, text }) {
        return `<h${depth} id="${slug(text)}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
      },
    },
  });
  return /** @type {string} */ (marked.parse(md, { async: false }));
}

/** The markdown a doc is in, in one language. */
const docSrc = (doc, lang) => (lang === "ar" ? doc.ar : doc.src);

/** A doc's markdown in one language: its file, or what generates it. */
const docMarkdown = (doc, lang) =>
  doc.gen ? doc.gen(lang) : fs.readFileSync(path.join(REPO, docSrc(doc, lang)), "utf8");

function renderDoc(doc, lang, published, groups) {
  const t = (k) => tr(lang, k);
  const srcPath = docSrc(doc, lang);
  const md = scrubMarkdown(docMarkdown(doc, lang));
  const html = markdownToHtml(md, srcPath, published);
  const title = doc.titleKey
    ? t(doc.titleKey)
    : (md.match(/^# (.+)$/m)?.[1] ?? srcPath).replace(/[*`]/g, "");
  const src = doc.gen ? `${GITHUB}/tree/main/${doc.source}` : `${GITHUB}/blob/main/${srcPath}`;
  const article = `<article class="doc">\n${html}\n<p class="note"><a href="${src}">${esc(t("doc.source"))}</a></p>\n</article>`;
  const group = groups.get(doc.group) ?? [];
  let body = article;
  if (group.length > 1) {
    // the group's guides, in order: a table of contents beside the page
    const toc = group
      .map(
        (d) =>
          `<li><a href="${siteUrl(d.out, lang)}"${d === doc ? ' aria-current="page"' : ""}>${esc(d.navTitle[lang])}</a></li>`
      )
      .join("\n");
    body = `<div class="layout">\n<nav class="toc" aria-label="${esc(t(`nav.${doc.group}`))}"><ul>\n${toc}\n</ul></nav>\n<div>\n${article}\n</div>\n</div>`;
  }
  return page({ lang, path: "/" + doc.out, title, body });
}

// ---- security.txt, robots.txt, sitemap --------------------------------------------------

// RFC 9116: an Expires less than a year ahead (MaxExpiryMonths in disclosure.go).
export const MAX_EXPIRY_MONTHS = 11;

/** A rolling expiry: the start of `now`'s UTC day, `months` later. A day past a short
 * month's end rolls over (Mar 31 + 11 months is Mar 3), as Go's time.Date does, so the site
 * and the API agree (DisclosurePolicy.ExpiresAt). */
export function expiresAt(now, months) {
  return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + months, now.getUTCDate()));
}

/** The disclosure policy's fields from the Go source the API serves (Story 23.8), as it reads
 * at `now` (the build's time): its Expires rolls, ExpiryMonths after the build day. */
export function readDisclosurePolicy(now = new Date()) {
  const go = fs.readFileSync(path.join(REPO, "api/internal/security/disclosure.go"), "utf8");
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
    expiryMonths: months,
    expires: expiresAt(now, months),
    preferredLang: list("PreferredLang"),
    policy: list("Policy"),
    acknowledgments: list("Acknowledgments"),
  };
}

export function securityTxt(p) {
  if (!p.contact.length) throw new Error("security.txt: no Contact");
  const lines = [
    ...p.contact.map((c) => `Contact: ${c}`),
    `Expires: ${p.expires.toISOString().replace(/\.\d{3}Z$/, "Z")}`,
    ...p.acknowledgments.map((a) => `Acknowledgments: ${a}`),
    `Preferred-Languages: ${p.preferredLang.join(", ")}`,
    `Canonical: ${ORIGIN}/.well-known/security.txt`,
    ...p.policy.map((u) => `Policy: ${u}`),
  ];
  return lines.join("\n") + "\n";
}

// ---- the build --------------------------------------------------------------------------

function write(rel, content) {
  const p = path.join(DIST, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.writeFileSync(p, content);
}

function copy(srcAbs, rel) {
  const p = path.join(DIST, rel);
  fs.mkdirSync(path.dirname(p), { recursive: true });
  fs.copyFileSync(srcAbs, p);
}

function walk(dir, base = dir, out = []) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) walk(p, base, out);
    else if (e.isFile()) out.push(path.relative(base, p).split(path.sep).join("/"));
  }
  return out;
}

/**
 * The wiki, built once with base /wiki/ and published in both languages: the English page at
 * /wiki/, the Arabic at /ar/wiki/ (the same bundle and diagrams; its own database).
 */
function buildWiki() {
  if (!fs.existsSync(path.join(WIKI_APP, "node_modules"))) {
    throw new Error("web/wiki-app has no node_modules: run `npm ci` in web/wiki-app first");
  }
  const out = path.join(STAGE, "wiki");
  execFileSync(
    "npm",
    ["run", "build", "--", "--base", "/wiki/", "--outDir", out, "--emptyOutDir"],
    {
      cwd: WIKI_APP,
      env: { ...process.env, VITE_WIKI_NO_MOCKUPS: "1" },
      stdio: ["ignore", "inherit", "inherit"],
    }
  );
  const dropped = [];
  for (const rel of walk(out)) {
    const to = wikiSitePath(rel);
    if (to) copy(path.join(out, rel), to);
    else dropped.push(rel);
  }
  const index = fs.readFileSync(path.join(out, "index.html"), "utf8");
  write("wiki/index.html", wikiAlternates(index, "/wiki/"));
  write("ar/wiki/index.html", arabicWikiIndex(index));
  if (dropped.length)
    console.log(`build: not published from the wiki build: ${dropped.join(", ")}`);
}

export function sitemap(paths) {
  const urls = paths.map((p) => `  <url><loc>${ORIGIN}${p}</loc></url>`).join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`;
}

/** The checks that the two languages' sources agree (scripts/parity.mjs). */
export function checkSources() {
  const read = (f) => fs.readFileSync(path.join(REPO, f), "utf8");
  const guides = DOCS.filter((d) => d.src.startsWith("docs/developers/")).map((d) => d.src);
  const findings = checkGuides(REPO, "docs/developers", guides);
  for (const d of DOCS) {
    if (d.gen) findings.push(...checkArabicMarkdown(d.ar, d.gen("ar")));
    else if (fs.existsSync(path.join(REPO, d.ar)))
      findings.push(...checkArabicMarkdown(d.ar, read(d.ar)));
  }
  findings.push(...pluginFindings(readPluginSources(REPO)));
  findings.push(...checkApps(APPS, appShots(), appImage));
  findings.push(...checkChangelog(read("CHANGELOG.md"), read("CHANGELOG.ar.md")));
  findings.push(
    ...checkOverlay(
      JSON.parse(read("shared/api/openapi.json")),
      JSON.parse(read("shared/api/openapi.ar.json"))
    )
  );
  const wikiDb = (dir) =>
    Object.fromEntries(
      fs
        .readdirSync(path.join(REPO, dir))
        .filter((f) => f.endsWith(".json"))
        .map((f) => [f, JSON.parse(read(`${dir}/${f}`))])
    );
  findings.push(...checkWiki(wikiDb(WIKI_DB), wikiDb(`${WIKI_DB}/ar`), WIKI_DB));
  return findings;
}

const fail = (what, findings) => {
  for (const f of findings) console.error(f);
  throw new Error(`build: ${what} found ${findings.length} problem(s)`);
};

export function build() {
  for (const d of [
    ...DOCS.filter((d) => !d.gen).flatMap((d) => [d.src, d.ar]),
    ...FILES.map((f) => f[0]),
  ]) {
    if (NEVER.some((re) => re.test(d))) throw new Error(`build: ${d} is never published`);
  }
  const drift = checkSources();
  if (drift.length) fail("the English/Arabic parity check", drift);

  fs.rmSync(DIST, { recursive: true, force: true });
  fs.rmSync(STAGE, { recursive: true, force: true });
  fs.mkdirSync(DIST, { recursive: true });

  // the portal's own static files (authored in this package)
  for (const rel of walk(path.join(PKG, "static"))) copy(path.join(PKG, "static", rel), rel);
  for (const [src, out] of FILES) copy(path.join(REPO, src), out);
  // the Flows guide's example chart, drawn at build time from the flow corpus's two-plugin
  // chain, in each language
  const chain = JSON.parse(
    fs.readFileSync(
      path.join(REPO, "shared/plugins/testdata/flows/chain_two_home/expect.json"),
      "utf8"
    )
  ).graph;
  for (const lang of LANGS) {
    const words =
      lang === "ar" ? { "audio.ready": "صوته جاهز" } : { "audio.ready": "Its audio is ready" };
    const svg = renderSvg(chain, {
      lang,
      highlight: "dev.example.chapter-writer",
      coreLabel: (e) => words[e] ?? e,
      title: lang === "ar" ? "مثال تدفّق من إضافتين" : "An example flow of two plugins",
    });
    write((lang === "ar" ? "ar/" : "") + "plugins/flows/example.svg", svg + "\n");
  }
  const openapi = JSON.parse(fs.readFileSync(path.join(REPO, "shared/api/openapi.json"), "utf8"));
  const overlay = JSON.parse(
    fs.readFileSync(path.join(REPO, "shared/api/openapi.ar.json"), "utf8")
  );
  write("ar/api/openapi.json", JSON.stringify(applyOverlay(openapi, overlay), null, 2) + "\n");

  // the site path each published source is at: an English source its page, an Arabic one
  // its /ar/ page (so a guide's links stay in its language)
  const published = new Map([
    ...DOCS.map((d) => [d.src, d.out]),
    ...DOCS.map((d) => [d.ar, siteUrl(d.out, "ar")]),
    // a guide links the generated reference as if it were a file beside it
    ["docs/developers/plugins/reference.md", "plugins/reference/"],
    ["docs/developers/plugins/reference.ar.md", siteUrl("plugins/reference/", "ar")],
  ]);
  const groups = new Map();
  for (const d of DOCS) {
    const h1 = (lang) => {
      if (d.titleKey) return tr(lang, d.titleKey);
      const md = docMarkdown(d, lang);
      return (md.match(/^# (.+)$/m)?.[1] ?? d.src).replace(/[*`]/g, "");
    };
    d.navTitle = { en: h1("en"), ar: h1("ar") };
    groups.set(d.group, [...(groups.get(d.group) ?? []), d]);
  }

  const pages = [];
  for (const lang of LANGS) {
    const pre = lang === "en" ? "" : "ar/";
    write(`${pre}index.html`, landing(lang));
    write(`${pre}404.html`, notFound(lang));
    write(`${pre}api/index.html`, apiPage(lang));
    write(`${pre}security/policy/index.html`, securityPolicy(lang));
    write(`${pre}security/hall-of-fame/index.html`, hallOfFame(lang));
    pages.push(
      ...["/", "/api/", "/security/policy/", "/security/hall-of-fame/"].map((p) =>
        localPath(lang, p)
      )
    );
    for (const pg of APPS) {
      write(`${pre}${appPath(pg.slug).slice(1)}index.html`, appPage(lang, pg));
      pages.push(localPath(lang, appPath(pg.slug)));
    }
    for (const doc of DOCS) {
      write(`${pre}${doc.out}index.html`, renderDoc(doc, lang, published, groups));
      pages.push(siteUrl(doc.out, lang));
    }
  }

  buildWiki();
  pages.push(...LANGS.map((lang) => localPath(lang, "/wiki/")));

  // Expires rolls with each build, and devportal-deploy.yml deploys monthly, so the live
  // file is always 10 to 11 months from expiring. No date to edit by hand.
  const policy = readDisclosurePolicy();
  if (policy.expires.getTime() <= Date.now())
    throw new Error(`security.txt: Expires ${policy.expires.toISOString()} is not ahead`);
  write(".well-known/security.txt", securityTxt(policy));
  write("robots.txt", `User-agent: *\nAllow: /\n\nSitemap: ${ORIGIN}/sitemap.xml\n`);
  write("sitemap.xml", sitemap(pages));
  fs.rmSync(STAGE, { recursive: true, force: true });

  const built = walk(DIST);
  const unpaired = checkPages(built, (f) => fs.readFileSync(path.join(DIST, f), "utf8"));
  if (unpaired.length) fail("the English/Arabic page check", unpaired);
  const findings = scanDir(DIST);
  if (findings.length) {
    for (const f of findings) console.error(`${f.file}: ${f.rule}: ${f.match}`);
    throw new Error(`build: the public-safety scan found ${findings.length} problem(s)`);
  }
  console.log(`build: ${walk(DIST).length} files in dist/, English and Arabic paired, scan clean`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    build();
  } catch (e) {
    console.error(e instanceof Error ? e.message : e);
    process.exit(1);
  }
}
