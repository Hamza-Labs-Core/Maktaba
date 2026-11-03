// Assembles maktaba.dev into dist/ (wrangler.toml's [assets] directory).
//
// The site is public, so this is an ALLOW-LIST: nothing reaches dist/ unless it is named
// here. A new doc is published by adding it to DOCS (or the wiki's allow-list), never by
// being in a folder. After assembling, the public-safety scan (scan.mjs) runs over dist/
// and the build fails on any finding.
//
//   npm run build            (web/wiki-app needs `npm ci` once first)

import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import { Marked } from "marked";
import { STRINGS } from "./strings.mjs";
import { scanDir } from "./scan.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
export const REPO = path.resolve(PKG, "../../..");
const DIST = path.join(PKG, "dist");
const STAGE = path.join(PKG, ".stage");
const WIKI_APP = path.join(REPO, "web/wiki-app");

export const ORIGIN = "https://maktaba.dev";
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";

// ---- the allow-lists --------------------------------------------------------------------

// Markdown published as pages: repo path -> site path (a folder, served as its index.html).
// The plugin developer docs are Epic 54's developer-facing parts: the model, the manifest,
// the API, the host, the queue, grants, install, settings, the SDK, signing, review, panels
// and local compute. Its internal build plan (migration waves, parity, repo layout) is not.
const PLUGIN_DOCS = [
  ["README.md", "Epic 54: Plugins"],
  ["story-54-01-manifest-package.md"],
  ["story-54-02-plugin-host.md"],
  ["story-54-04-plugin-api.md"],
  ["story-54-05-media-handles.md"],
  ["story-54-06-queue-stages.md"],
  ["story-54-08-grants-roles.md"],
  ["story-54-10-file-install.md"],
  ["story-54-11-settings.md"],
  ["story-54-12-sdk-harness.md"],
  ["story-54-14-signing-trust.md"],
  ["story-54-16-review.md"],
  ["story-54-20-panels.md"],
  ["story-54-21-local-compute.md"],
];
export const DOCS = [
  ...PLUGIN_DOCS.map(([file, title]) => ({
    src: `specs/epics/54-plugins/${file}`,
    out: file === "README.md" ? "plugins/" : `plugins/${file.replace(/\.md$/, "")}/`,
    title,
    group: "plugins",
  })),
  { src: "CHANGELOG.md", out: "changelog/", title: "Changelog", group: "changelog" },
];

// The wiki's build output: only these shapes are copied (relative to the wiki's outDir).
// No mockups: their sample data (home paths, LAN addresses) is not for a public site, so the
// wiki is built with VITE_WIKI_NO_MOCKUPS=1 and links each mockup's source instead.
export const WIKI_ALLOW = [
  /^index\.html$/,
  /^assets\/[A-Za-z0-9._-]+\.(js|css|woff2?|svg|png)$/,
  /^db\/(wiki|wiki-cross-refs|wiki-epics-\d\d-\d\d)\.json$/,
  /^diagrams\/[A-Za-z0-9/_.-]+\.(drawio|svg|png)$/,
];

// Repo files copied as they are: repo path -> site path.
const FILES = [
  ["shared/api/openapi.json", "api/openapi.json"],
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

const t = (k) => {
  const v = STRINGS.en[k];
  if (v === undefined) throw new Error(`no string ${k}`);
  return v;
};
const esc = (s) =>
  String(s).replace(
    /[&<>"']/g,
    (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]
  );
const s = (k, tag = "span", attrs = "") => `<${tag}${attrs} data-i18n="${k}">${esc(t(k))}</${tag}>`;

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
  ["nav.wiki", "/wiki/"],
  ["nav.api", "/api/"],
  ["nav.plugins", "/plugins/"],
  ["nav.changelog", "/changelog/"],
  ["nav.security", "/security/policy/"],
  ["nav.source", GITHUB],
];

export function page({ path: sitePath, titleKey, title, body, head = "" }) {
  const current = (href) =>
    href === "/" ? sitePath === "/" : href.startsWith("/") && sitePath.startsWith(href);
  const nav = NAV.map(
    ([k, href]) =>
      `<a href="${href}"${current(href) ? ' aria-current="page"' : ""} data-i18n="${k}">${esc(t(k))}</a>`
  ).join("\n        ");
  const docTitle = titleKey ? t(titleKey) : title;
  return `<!doctype html>
<html lang="en" dir="ltr">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${esc(docTitle)} · maktaba.dev</title>
  <meta name="description" content="${esc(t("home.lead"))}">
  <link rel="canonical" href="${ORIGIN}${sitePath}">
  <link rel="icon" type="image/png" sizes="32x32" href="/assets/favicon-32.png">
  <link rel="apple-touch-icon" href="/assets/apple-touch-icon.png">
  <link rel="stylesheet" href="${v("/assets/portal.css")}">
  <script src="${v("/assets/i18n.js")}"></script>
  <script src="${v("/assets/lang.js")}"></script>${head}
</head>
<body>
  <a class="skip" href="#content" data-i18n="skip">${esc(t("skip"))}</a>
  <header class="top">
    <div class="wrap">
      <a class="brand" href="/"><img src="/assets/favicon-32.png" alt=""><span data-i18n="site.name">${esc(t("site.name"))}</span></a>
      <nav class="main" aria-label="maktaba.dev">
        ${nav}
      </nav>
      <button id="lang-toggle" type="button" lang="ar" data-i18n="lang.toggle" data-i18n-label="lang.toggle.label" aria-label="${esc(t("lang.toggle.label"))}">${esc(t("lang.toggle"))}</button>
    </div>
  </header>
  <main id="content" class="wrap"${titleKey ? ` data-i18n-title="${titleKey}"` : ""}>
${body}
  </main>
  <footer>
    <div class="wrap">
      ${s("footer.text")}
      <a href="/security/policy/" data-i18n="footer.security">${esc(t("footer.security"))}</a>
      <a href="${GITHUB}">github.com/Hamza-Labs-Core/Maktaba</a>
    </div>
  </footer>
</body>
</html>
`;
}

const card = (key, href) =>
  `      <li><a href="${href}"><strong data-i18n="card.${key}.title">${esc(t(`card.${key}.title`))}</strong>` +
  `<span data-i18n="card.${key}.body">${esc(t(`card.${key}.body`))}</span></a></li>`;

function landing() {
  return page({
    path: "/",
    titleKey: "home.title",
    body: `    <section class="hero">
      ${s("home.title", "h1")}
      ${s("home.lead", "p")}
      ${s("home.lead2", "p")}
      <div class="ctas">
        <a class="btn primary" href="${GITHUB}" data-i18n="home.cta.source">${esc(t("home.cta.source"))}</a>
        <a class="btn" href="/wiki/" data-i18n="home.cta.wiki">${esc(t("home.cta.wiki"))}</a>
      </div>
    </section>
    ${s("home.cards", "h2")}
    <ul class="cards">
${card("source", GITHUB)}
${card("wiki", "/wiki/")}
${card("specs", `${GITHUB}/tree/main/specs/epics`)}
${card("plugins", "/plugins/")}
${card("api", "/api/")}
${card("changelog", "/changelog/")}
${card("security", "/security/policy/")}
    </ul>
    ${s("home.lang.note", "p", ' class="note"')}`,
  });
}

function notFound() {
  return page({
    path: "/404",
    titleKey: "404.title",
    body: `    ${s("404.title", "h1")}
    ${s("404.body", "p")}
    <p><a class="btn primary" href="/" data-i18n="404.home">${esc(t("404.home"))}</a></p>`,
  });
}

function securityPolicy() {
  const sec = (k) => `    ${s(`security.${k}.title`, "h2")}\n    ${s(`security.${k}.body`, "p")}`;
  return page({
    path: "/security/policy/",
    titleKey: "security.title",
    body: `    ${s("security.title", "h1")}
    ${s("security.lead", "p")}
${sec("report")}
${sec("after")}
${sec("rules")}
    <p class="note"><a href="/.well-known/security.txt" data-i18n="security.txt">${esc(t("security.txt"))}</a></p>`,
  });
}

function hallOfFame() {
  return page({
    path: "/security/hall-of-fame/",
    titleKey: "hof.title",
    body: `    ${s("hof.title", "h1")}
    ${s("hof.body", "p")}
    ${s("hof.empty", "p", ' class="note"')}`,
  });
}

function apiPage() {
  return page({
    path: "/api/",
    titleKey: "api.title",
    head: `\n  <script src="${v("/api/redoc.standalone.js")}" defer></script>\n  <script src="${v("/api/init.js")}" defer></script>`,
    body: `    ${s("api.title", "h1")}
    <p><a href="/api/openapi.json" data-i18n="api.download">${esc(t("api.download"))}</a></p>
    <div id="redoc" dir="ltr" lang="en">${s("api.loading", "p")}</div>`,
  });
}

// ---- markdown docs ----------------------------------------------------------------------

function siteUrl(out) {
  return "/" + out;
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
  if (published.has(target)) return siteUrl(published.get(target)) + (hash ? `#${hash}` : "");
  const kind = p.endsWith("/") || !path.posix.extname(p) ? "tree" : "blob";
  return `${GITHUB}/${kind}/main/${target}` + (hash ? `#${hash}` : "");
}

function renderDoc(doc, published, group) {
  const md = scrubMarkdown(fs.readFileSync(path.join(REPO, doc.src), "utf8"));
  const marked = new Marked({ gfm: true });
  marked.use({
    renderer: {
      link({ href, title, tokens }) {
        const text = this.parser.parseInline(tokens);
        const to = rewriteHref(href, doc.src, published);
        if (to === null) return text;
        return `<a href="${esc(to)}"${title ? ` title="${esc(title)}"` : ""}>${text}</a>`;
      },
      // a heading's id is its text, so in-page links (#5-the-no-internet-guarantee) work
      heading({ tokens, depth, text }) {
        return `<h${depth} id="${slug(text)}">${this.parser.parseInline(tokens)}</h${depth}>\n`;
      },
    },
  });
  const html = marked.parse(md);
  const title = doc.title ?? (md.match(/^# (.+)$/m)?.[1] ?? doc.src).replace(/[*`]/g, "");
  const src = `${GITHUB}/blob/main/${doc.src}`;
  const article = `<article class="doc" dir="ltr" lang="en">\n${html}\n<p class="note"><a href="${src}" data-i18n="doc.source">${esc(t("doc.source"))}</a></p>\n</article>`;
  let body;
  if (doc.group === "plugins") {
    const toc = group
      .map(
        (d) =>
          `<li><a href="${siteUrl(d.out)}"${d === doc ? ' aria-current="page"' : ""}>${esc(d.navTitle)}</a></li>`
      )
      .join("\n");
    const intro =
      doc.out === "plugins/"
        ? `${s("plugins.title", "h1")}\n${s("plugins.lead", "p")}\n${s("plugins.status", "p", ' class="note"')}\n`
        : "";
    body = `${intro}<div class="layout">\n<nav class="toc" aria-label="Epic 54"><ul>\n${toc}\n</ul></nav>\n${article}\n</div>`;
  } else {
    body = article;
  }
  return page({ path: siteUrl(doc.out), title, body });
}

// ---- security.txt, robots.txt, sitemap --------------------------------------------------

/** The disclosure policy's fields from the Go source the API serves (Story 23.8). */
export function readDisclosurePolicy() {
  const go = fs.readFileSync(path.join(REPO, "api/internal/security/disclosure.go"), "utf8");
  const fn = go.slice(go.indexOf("func DefaultPolicy()"));
  const list = (field) => {
    const m = fn.match(new RegExp(`${field}:\\s*\\[\\]string\\{([^}]*)\\}`));
    return m ? [...m[1].matchAll(/"([^"]+)"/g)].map((x) => x[1]) : [];
  };
  const e = fn.match(/Expires:\s*time\.Date\((\d+),\s*(\d+),\s*(\d+)/);
  if (!e) throw new Error("disclosure.go: no Expires in DefaultPolicy");
  return {
    contact: list("Contact"),
    expires: new Date(Date.UTC(+e[1], +e[2] - 1, +e[3])),
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

function buildWiki() {
  if (!fs.existsSync(path.join(WIKI_APP, "node_modules"))) {
    throw new Error("web/wiki-app has no node_modules: run `npm ci` in web/wiki-app first");
  }
  const out = path.join(STAGE, "wiki");
  // base /wiki/: the bundle, its db/ and the diagrams load from under /wiki/ wherever the
  // page is opened (/wiki or /wiki/)
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
    if (WIKI_ALLOW.some((re) => re.test(rel))) copy(path.join(out, rel), `wiki/${rel}`);
    else dropped.push(rel);
  }
  if (dropped.length)
    console.log(`build: not published from the wiki build: ${dropped.join(", ")}`);
}

export function sitemap(paths) {
  const urls = paths.map((p) => `  <url><loc>${ORIGIN}${p}</loc></url>`).join("\n");
  return `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls}\n</urlset>\n`;
}

export function build() {
  for (const d of [...DOCS.map((d) => d.src), ...FILES.map((f) => f[0])]) {
    if (NEVER.some((re) => re.test(d))) throw new Error(`build: ${d} is never published`);
  }
  fs.rmSync(DIST, { recursive: true, force: true });
  fs.rmSync(STAGE, { recursive: true, force: true });
  fs.mkdirSync(DIST, { recursive: true });

  // the portal's own static files (authored in this package)
  for (const rel of walk(path.join(PKG, "static"))) copy(path.join(PKG, "static", rel), rel);
  write("assets/i18n.js", `window.MAKTABA_STRINGS = ${JSON.stringify(STRINGS)};\n`);
  for (const [src, out] of FILES) copy(path.join(REPO, src), out);

  const pages = ["/"];
  write("index.html", landing());
  write("404.html", notFound());
  write("api/index.html", apiPage());
  write("security/policy/index.html", securityPolicy());
  write("security/hall-of-fame/index.html", hallOfFame());
  pages.push("/api/", "/security/policy/", "/security/hall-of-fame/");

  const published = new Map(DOCS.map((d) => [d.src, d.out]));
  const group = DOCS.filter((d) => d.group === "plugins").map((d) => {
    const md = fs.readFileSync(path.join(REPO, d.src), "utf8");
    const h1 = (md.match(/^# (.+)$/m)?.[1] ?? d.src).replace(/[*`]/g, "");
    return Object.assign(d, { navTitle: d.title ?? h1.replace(/^Story 54\.(\d+) — /, "54.$1 ") });
  });
  for (const doc of DOCS) {
    write(`${doc.out}index.html`, renderDoc(doc, published, group));
    pages.push(siteUrl(doc.out));
  }

  buildWiki();
  pages.push("/wiki/");

  const policy = readDisclosurePolicy();
  const days = (policy.expires.getTime() - Date.now()) / 86400000;
  if (days < 0)
    throw new Error(
      `security.txt: Expires ${policy.expires.toISOString()} has passed (api/internal/security/disclosure.go)`
    );
  if (days < 30)
    console.warn(
      `build: security.txt expires in ${Math.floor(days)} days: move Expires in api/internal/security/disclosure.go`
    );
  write(".well-known/security.txt", securityTxt(policy));
  write("robots.txt", `User-agent: *\nAllow: /\n\nSitemap: ${ORIGIN}/sitemap.xml\n`);
  write("sitemap.xml", sitemap(pages));

  fs.rmSync(STAGE, { recursive: true, force: true });
  const findings = scanDir(DIST);
  if (findings.length) {
    for (const f of findings) console.error(`${f.file}: ${f.rule}: ${f.match}`);
    throw new Error(`build: the public-safety scan found ${findings.length} problem(s)`);
  }
  console.log(`build: ${walk(DIST).length} files in dist/, scan clean`);
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  try {
    build();
  } catch (e) {
    console.error(e instanceof Error ? e.message : e);
    process.exit(1);
  }
}
