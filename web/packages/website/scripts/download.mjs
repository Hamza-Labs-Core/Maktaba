// maktaba.tv's download page (Epic 60 story 60.11): /download/ and /ar/download/, every device
// with its real release asset, in 60.01 part B's download layout (design/README.md, "Download
// card"; design/mockups/download.*.html) and 60.02 part B's words (copy/download.*.md).
//
// The data, read at build time and never in the browser:
//   - the latest stable release, from the GitHub Releases API (releases/latest): its version and
//     date, and each asset's name, size and digest (sha256), with GITHUB_TOKEN (or GH_TOKEN) when
//     set. The build fails, saying why, when the API cannot be read: a deploy never ships an
//     empty download page, and the live one stays at the version it had. The release's
//     checksums.txt (cosign-signed) is read too: for every asset it lists, the API's digest must
//     equal it, or the build fails naming the file (T04).
//   - src/downloads.json: one entry per platform, in the page's order: the patterns its files
//     are matched by ({v} is the release's version, never typed), the requirements line, the
//     install guide, and the public store listings (owner action A6). A platform with no
//     matching asset and no listing has no card (T02, T03); a fallback pattern takes the
//     button's place when the first one's leg failed.
//   - MAKTABA_PACKAGES_URL and HOMEBREW_TAP_REPO (owner action A7): the server packages' card,
//     with the apt, dnf and Homebrew rows the release notes print, only when one is set (T05).
//
// WEBSITE_RELEASE_FIXTURE (a folder with release.json, the API's answer, and checksums.txt) reads
// a saved release instead of the API: the unit tests do (scripts/fixtures/release/, v0.3.135's),
// and so do the browser tests' visual references, which draw the release's own numbers from it
// (e2e/visual.spec.ts). WEBSITE_EXPECT_RELEASE (the tag a release run dispatched the deploy for)
// fails a build whose latest release is older than it.
//
// The visitor's platform comes first (T06), without a request and without moving anything: the
// head script (src/download-platform.ts) reads the address's #<platform> or the user agent before
// the first paint and marks <html data-platform>, which src/download.css draws as the wide card
// at the top; once the page is parsed it moves that card first in the document too, where it is
// already drawn, so the keyboard and a screen reader meet it first.

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { bLinks, codeBlock, esc, GITHUB, ICONS, inLang, ORIGIN, page } from "./shell.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");

export const REPO_SLUG = "Hamza-Labs-Core/Maktaba";
export const API = `https://api.github.com/repos/${REPO_SLUG}/releases/latest`;
export const FIXTURE = path.join(HERE, "fixtures/release");
export const LISTINGS = ["appStore", "testFlightPublic", "googlePlay", "playTestingPublic"];
/** The platforms a user agent names, in the order the head script tries them. */
export const UA_PLATFORMS = ["mac", "windows", "linux"];
export const LANGS = ["en", "ar"];
export const PATH = "/download/";

// ---- the words ---------------------------------------------------------------------------------

const escHtml = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const inline = (s) =>
  escHtml(s)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
/** A copy string's text, its tags dropped (for an attribute or the <title>). */
// Every caller escapes the result again (esc, the head's attributes), so this is text, not HTML:
// the tags are dropped until none is left, and any < or > that is left goes too.
export const plain = (html) => {
  let out = html;
  for (let prev = ""; prev !== out; ) {
    prev = out;
    out = out.replace(/<[^<>]*>/g, "");
  }
  return out
    .replace(/[<>]/g, "")
    .replace(/&lt;|&gt;/g, "")
    .replace(/&amp;/g, "&");
};

/** copy/download.<lang>.md as { key: html }: each `## key` heading and the text under it,
 * backticks as <code> (the mockups read it the same way, design/mockups/part-b.mjs). */
export function readCopy(lang, name = "download") {
  const text = fs.readFileSync(path.join(PKG, "copy", `${name}.${lang}.md`), "utf8");
  const out = {};
  let key = null;
  let buf = [];
  const flush = () => {
    if (key) out[key] = inline(buf.join(" ").trim());
  };
  for (const line of text.split("\n")) {
    const m = /^## (\S+)\s*$/.exec(line);
    if (m) {
      flush();
      key = m[1];
      buf = [];
    } else if (key && line.trim() && !/^<\/?div\b/.test(line.trim())) buf.push(line.trim());
  }
  flush();
  return out;
}

/** A copy key, or the build fails naming it: a card never shows an empty line. */
function word(c, key, lang) {
  if (!(key in c)) throw new Error(`copy/download.${lang}.md: no "## ${key}"`);
  return c[key];
}

const MONTHS = {
  en: "January February March April May June July August September October November December".split(
    " "
  ),
  ar: "يناير فبراير مارس أبريل مايو يونيو يوليو أغسطس سبتمبر أكتوبر نوفمبر ديسمبر".split(" "),
};

/** The release day in words, in UTC, with Western Arabic numerals in both languages (§6.5). */
export function releaseDate(iso, lang) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()))
    throw new Error(`download page: the release date ${iso} is not a date`);
  return `${d.getUTCDate()} ${MONTHS[lang][d.getUTCMonth()]} ${d.getUTCFullYear()}`;
}

/** A file's size as the mockups print it: 203 MB, 13.6 MB, 23 KB, 6.8 KB. */
export function fileSize(bytes, lang) {
  const mb = bytes / 1e6;
  if (mb >= 1)
    return `${mb >= 100 ? Math.round(mb) : mb.toFixed(1)} ${lang === "ar" ? "ميغابايت" : "MB"}`;
  return `${(bytes / 1e3).toFixed(bytes >= 1e4 ? 0 : 1)} ${lang === "ar" ? "كيلوبايت" : "KB"}`;
}

// ---- reading the release -----------------------------------------------------------------------

/** checksums.txt (sha256sum's lines) as name → hex digest. */
export function parseChecksums(text) {
  const out = new Map();
  for (const line of text.split("\n")) {
    if (!line.trim()) continue;
    const m = /^([0-9a-f]{64}) [ *](?:\.\/)?(.+)$/.exec(line.trim());
    if (!m) throw new Error(`checksums.txt: not a sha256sum line: ${line}`);
    out.set(m[2], m[1]);
  }
  return out;
}

function token(env) {
  return env.GITHUB_TOKEN || env.GH_TOKEN || "";
}

async function get(url, { env, fetchImpl, json }) {
  const headers = { "User-Agent": "maktaba-website-build" };
  if (json) {
    headers.Accept = "application/vnd.github+json";
    headers["X-GitHub-Api-Version"] = "2022-11-28";
    if (token(env)) headers.Authorization = `Bearer ${token(env)}`;
  }
  let res;
  try {
    res = await fetchImpl(url, { headers, redirect: "follow" });
  } catch (e) {
    throw new Error(
      `download page: ${url} could not be read (${e.cause?.code || e.message}). The page is built from the latest release and is never shipped without it: retry once GitHub answers`
    );
  }
  if (!res.ok) {
    const why =
      res.status === 403 || res.status === 429
        ? token(env)
          ? " (rate limited)"
          : " (rate limited: set GITHUB_TOKEN)"
        : "";
    throw new Error(
      `download page: ${url} answered ${res.status}${why}; the page is never shipped without the release`
    );
  }
  return json ? res.json() : res.text();
}

/** The latest stable release and its checksums.txt: { release, checksums } (the text). From
 * WEBSITE_RELEASE_FIXTURE's folder when set, else the GitHub Releases API. */
export async function readRelease({ env = process.env, fetchImpl = globalThis.fetch } = {}) {
  if (env.WEBSITE_RELEASE_FIXTURE) {
    const dir = path.resolve(env.WEBSITE_RELEASE_FIXTURE);
    return {
      release: JSON.parse(fs.readFileSync(path.join(dir, "release.json"), "utf8")),
      checksums: fs.readFileSync(path.join(dir, "checksums.txt"), "utf8"),
      source: path.relative(PKG, dir) || dir,
    };
  }
  const release = await get(API, { env, fetchImpl, json: true });
  const sums = (release.assets || []).find((a) => a.name === "checksums.txt");
  if (!sums) throw new Error(`download page: ${release.tag_name} has no checksums.txt`);
  const checksums = await get(sums.browser_download_url, { env, fetchImpl, json: false });
  return { release, checksums, source: API };
}

/** Compares two vX.Y.Z tags: <0, 0, >0. */
export function compareTags(a, b) {
  const pa = a.replace(/^v/, "").split(".").map(Number);
  const pb = b.replace(/^v/, "").split(".").map(Number);
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return pa[i] - pb[i];
  return 0;
}

/**
 * The release as the page uses it: { tag, version, published, url, assets: Map name → { name,
 * size, url, sha256, signed } }. Fails on a draft or a pre-release, a tag that is not vX.Y.Z, a
 * release older than `expect`, and any asset whose API digest differs from checksums.txt (T04).
 */
export function releaseData({ release, checksums }, { expect = "" } = {}) {
  if (release.draft || release.prerelease)
    throw new Error(`download page: ${release.tag_name} is a draft or a pre-release, never shown`);
  const m = /^v(\d+\.\d+\.\d+)$/.exec(release.tag_name || "");
  if (!m)
    throw new Error(`download page: the latest release's tag ${release.tag_name} is not vX.Y.Z`);
  if (expect && compareTags(release.tag_name, expect) < 0)
    throw new Error(
      `download page: the latest release is ${release.tag_name}, older than ${expect}, which this deploy was started for`
    );
  const sums = parseChecksums(checksums);
  const assets = new Map();
  const mismatched = [];
  for (const a of release.assets || []) {
    const digest = /^sha256:([0-9a-f]{64})$/.exec(a.digest || "")?.[1] || "";
    const listed = sums.get(a.name);
    if (listed && digest && listed !== digest)
      mismatched.push(`${a.name}: GitHub's sha256:${digest}, checksums.txt's ${listed}`);
    assets.set(a.name, {
      name: a.name,
      size: a.size,
      url: a.browser_download_url,
      sha256: digest || listed || "",
      signed: Boolean(listed),
    });
  }
  if (mismatched.length)
    throw new Error(
      `download page: ${release.tag_name}'s files differ from its signed checksums.txt:\n${mismatched.join("\n")}`
    );
  return {
    tag: release.tag_name,
    version: m[1],
    published: release.published_at,
    url: release.html_url || `${GITHUB}/releases/tag/${release.tag_name}`,
    assets,
  };
}

// ---- the cards ------------------------------------------------------------------------------------

export const DOWNLOADS = path.join(PKG, "src/downloads.json");

export function readPlatforms(file = DOWNLOADS) {
  return JSON.parse(fs.readFileSync(file, "utf8")).platforms;
}

/** The store links' words (downloads.json's stores), by listing field and language. */
export function readStores(file = DOWNLOADS) {
  return JSON.parse(fs.readFileSync(file, "utf8")).stores;
}

const reEscape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

/** The Homebrew tap's short name (owner/homebrew-tap → owner/tap), as brew and the notes say it. */
export function tapName(repo) {
  const m = /^([A-Za-z0-9-]+)\/(?:homebrew-)?([A-Za-z0-9._-]+)$/.exec(repo);
  if (!m) throw new Error(`HOMEBREW_TAP_REPO ${repo} is not owner/homebrew-name`);
  return `${m[1]}/${m[2]}`.toLowerCase();
}

const httpsUrl = (where, u) => {
  if (!/^https:\/\/[^\s"<>]+$/.test(u)) throw new Error(`${where}: ${u} is not an https address`);
  return u;
};

/** The package repositories' rows (owner action A7), as the release notes print them. */
export function packageRows(env = process.env) {
  const rows = [];
  if (env.MAKTABA_PACKAGES_URL) {
    const url = httpsUrl("MAKTABA_PACKAGES_URL", env.MAKTABA_PACKAGES_URL);
    rows.push({ key: "server.apt", url });
    rows.push({ key: "server.dnf", url });
  }
  if (env.HOMEBREW_TAP_REPO)
    rows.push({
      key: "server.brew",
      code: `brew install ${tapName(env.HOMEBREW_TAP_REPO)}/maktaba`,
    });
  return rows;
}

/**
 * The cards the release has, in downloads.json's order: { id, icon, ua, primary, others,
 * listings, packages, href, needs, guide, verify, shell, sig }. A platform with no matching
 * asset, no listing and nothing else to offer is left out.
 */
export function cards(data, platforms = readPlatforms(), env = process.env, stores = readStores()) {
  const v = reEscape(data.version);
  const find = (pattern) => {
    const re = new RegExp(pattern.replaceAll("{v}", v));
    return [...data.assets.values()].find((a) => re.test(a.name)) || null;
  };
  const out = [];
  for (const p of platforms) {
    const primary = (p.primary || []).map(find).find(Boolean) || null;
    const others = (p.other || [])
      .map((o) => ({ key: o.key, asset: find(o.pattern) }))
      .filter((o) => o.asset && o.asset !== primary);
    const listings = LISTINGS.filter((k) => p[k]).map((k) => {
      if (!stores[k]?.en || !stores[k]?.ar)
        throw new Error(`src/downloads.json: no stores.${k} words`);
      return { key: k, url: httpsUrl(`src/downloads.json: ${p.id}.${k}`, p[k]), label: stores[k] };
    });
    const packages = p.packages ? packageRows(env) : [];
    const linked = Boolean(p.href) && (p.requires || []).every((r) => find(r));
    if (!primary && !linked && !packages.length && !listings.length) continue;
    for (const a of [primary, ...others.map((o) => o.asset)].filter(Boolean))
      if (!a.sha256) throw new Error(`download page: ${data.tag}'s ${a.name} has no sha256 digest`);
    out.push({
      id: p.id,
      name: p.name || null,
      icon: p.icon,
      ua: p.ua || null,
      primary,
      others,
      listings,
      packages,
      href: linked ? p.href : null,
      needs: p.needs,
      guide: p.guide || null,
      verify: p.verify || null,
      shell: p.shell || null,
      sig: primary ? data.assets.get(`${primary.name}.sig`) || null : null,
    });
  }
  return out;
}

// ---- icons (part B's, design/mockups/part-b.mjs: 24-unit grid, 1.75 stroke) ----------------------

const svg = (body, cls = "") =>
  `<svg${cls ? ` class="${cls}"` : ""} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;

export const P = {
  download: svg('<path d="M12 4v11M7 10.5l5 5 5-5M5 19.5h14"/>'),
  check: svg('<path d="M5 12.5l4.5 4.5L19 7.5"/>'),
  laptop: svg('<rect x="5" y="5" width="14" height="10" rx="1.2"/><path d="M3 18.5h18"/>'),
  window: svg('<rect x="3.5" y="4.5" width="17" height="15" rx="1.5"/><path d="M3.5 8.5h17"/>'),
  terminal: svg(
    '<rect x="3.5" y="4.5" width="17" height="15" rx="1.5"/><path d="M7.5 10l2.5 2-2.5 2M12.5 14.5h4"/>'
  ),
  box: svg(
    '<rect x="5" y="3.5" width="14" height="17" rx="1.5"/><path d="M9 8h6M9 11.5h6M9 17h.01M12 17h.01"/>'
  ),
  server: svg(
    '<rect x="4.5" y="3.5" width="15" height="7" rx="1.5"/><rect x="4.5" y="13.5" width="15" height="7" rx="1.5"/><path d="M8 7h.01M8 17h.01"/>'
  ),
  tv: svg('<rect x="3" y="4.5" width="18" height="12" rx="1.5"/><path d="M9 20h6M12 16.5V20"/>'),
  phone: svg('<rect x="7" y="3" width="10" height="18" rx="2.2"/><path d="M11 18h2"/>'),
  lock: svg(
    '<rect x="5.5" y="10.5" width="13" height="9" rx="1.5"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>'
  ),
  star: svg('<path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6L5.6 18.4"/>'),
  // the disclosure chevron: it points along the reading direction (mirrored on an Arabic page)
  chev: `<svg class="chev dir-icon" data-mirror width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><path d="M9 6l6 6-6 6"/></svg>`,
};

// ---- rendering ------------------------------------------------------------------------------------

const HASH_COMMAND = {
  mac: (f) => `shasum -a 256 ${f}`,
  windows: (f) => `Get-FileHash .\\${f} -Algorithm SHA256`,
  linux: (f) => `sha256sum ${f}`,
};
const SHELL_NAME = {
  mac: { en: "Terminal", ar: "Terminal" },
  windows: { en: "PowerShell", ar: "PowerShell" },
  linux: { en: "In a terminal", ar: "في الطرفية" },
};
const COPY_NAME = {
  one: { en: "Copy the command", ar: "نسخ الأمر" },
  many: { en: "Copy the commands", ar: "نسخ الأوامر" },
};

/** The cosign check of checksums.txt, as the release notes print it for this tag. */
export function cosignCommand(tag) {
  return `cosign verify-blob --bundle checksums.txt.sigstore.json \\
  --certificate-identity ${GITHUB}/.github/workflows/release.yml@refs/tags/${tag} \\
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\
  checksums.txt
sha256sum --ignore-missing -c checksums.txt`;
}

const fileLink = (a, inner) => `<a href="${esc(a.url)}">${inner}</a>`;

/** One card. Every value the release sets sits in an element marked data-release (its key), so
 * the visual references can draw the fixture's numbers in place of the live release's. */
function card(k, data, { c, lang, resolves }) {
  const t = (key) => word(c, key, lang);
  const facts = k.primary
    ? `<dl class="facts-row" data-release="${k.id}.facts"><dt>${t("version.label")}</dt><dd>${t("version.label")} ${data.version}</dd><dt>${t("date.label")}</dt><dd>${releaseDate(data.published, lang)}</dd><dt>${t("size.label")}</dt><dd id="${k.id}-size">${fileSize(k.primary.size, lang)}</dd></dl>`
    : "";
  let action = "";
  if (k.primary)
    action = `<a class="button" href="${esc(k.primary.url)}" download="${esc(k.primary.name)}" aria-describedby="${k.id}-size">${P.download}${t(`${k.id}.primary`)}</a>`;
  else if (k.href)
    action = `<a class="button" href="${inLang(lang, k.href)}">${t(`${k.id}.primary`)}${ICONS.onward}</a>`;
  const listings = k.listings.length
    ? `<ul class="stores">${k.listings
        .map(
          (l) =>
            `<li><a class="button${k.primary || k.href ? " quiet" : ""}" href="${esc(l.url)}">${esc(l.label[lang])}${ICONS.out}</a></li>`
        )
        .join("")}</ul>`
    : "";
  const packages = k.packages.length
    ? `<ul class="packages">${k.packages
        .map(
          (r) =>
            `<li><p>${t(r.key)}${r.url ? ` <a href="${esc(r.url)}">${esc(r.url.replace(/^https:\/\//, ""))}</a>` : ""}</p>${r.code ? codeBlock(lang, { name: "Homebrew", code: r.code, copyName: COPY_NAME.one[lang] }) : ""}</li>`
        )
        .join("")}</ul>`
    : "";
  const guide =
    k.guide && resolves(inLang(lang, k.guide))
      ? `<a class="onward" href="${inLang(lang, k.guide)}">${t("guide")}${ICONS.onward}</a>`
      : "";
  const badge = k.ua ? `<span class="badge">${P.check}${t("mine")}</span>` : "";
  const main = `<div class="card-main">
          ${badge}
          <div class="card-head"><span class="ring">${P[k.icon]}</span><h2 id="${k.id}-name">${k.name ? esc(k.name[lang]) : t(`${k.id}.name`)}</h2></div>
          <p class="needs">${esc(k.needs[lang])}</p>
          ${[facts, action, listings, packages, guide].filter(Boolean).join("\n          ")}
        </div>`;

  const rows = k.others
    .map(
      (o) =>
        `<li>${fileLink(o.asset, `<span>${t(o.key)}</span>`)}<span class="size"><code>${esc(o.asset.name)}</code> · ${fileSize(o.asset.size, lang)}</span></li>`
    )
    .join("");
  const other = rows
    ? `<details class="disclose"><summary>${P.chev}${t("other")}</summary><ul data-release="${k.id}.other">${rows}</ul></details>`
    : "";

  const parts = [];
  if (k.primary && k.verify === "terminal") {
    const f = k.primary.name;
    parts.push(
      `<p>${t(`${k.shell}.verify.text`)}</p><code class="hash">sha256:${k.primary.sha256}</code>${codeBlock(
        lang,
        {
          name: SHELL_NAME[k.shell][lang],
          code: HASH_COMMAND[k.shell](f),
          copyName: COPY_NAME.one[lang],
        }
      )}`
    );
    if (k.sig)
      parts.push(
        `<p>${t("verify.sig").replace("{file}", fileLink(k.sig, `<code>${esc(k.sig.name)}</code>`))}</p>`
      );
    else if (k.shell === "mac") parts.push(`<p>${t("mac.verify.after")}</p>`);
  }
  const signed = k.verify === "cosign" || (k.primary && k.primary.signed);
  if (signed)
    parts.push(
      `<p>${t("nas.verify.text")}</p>${codeBlock(lang, {
        name: SHELL_NAME.linux[lang],
        code: cosignCommand(data.tag),
        copyName: COPY_NAME.many[lang],
      })}`
    );
  const verify = parts.length
    ? `<details class="disclose"><summary>${P.chev}${t("verify")}</summary><div data-release="${k.id}.verify">${parts.join("")}</div></details>`
    : "";
  const more =
    other || verify
      ? `<div class="card-more">\n          ${[other, verify].filter(Boolean).join("\n          ")}\n        </div>`
      : "";
  return `<li class="card" id="${k.id}" aria-labelledby="${k.id}-name">${main}${more ? `\n        ${more}` : ""}
      </li>`;
}

/** The web app's home in a browser frame (60.09's screenshot, in the language asked for). */
function shot(lang, shotLang, { alt, caption, manifest }) {
  const m = manifest.images[`web-home-${shotLang}`];
  if (!m) throw new Error(`shots-manifest.json: no web-home-${shotLang}`);
  const set = (kind) => m.sizes.map((s) => `${s[kind]} ${s.width}w`).join(", ");
  const sizes = "(min-width: 1280px) 600px, (min-width: 768px) 46vw, 100vw";
  const small = m.sizes[0];
  return `<figure class="device" data-kind="browser"><div class="frame-wrap"><div class="frame-fit"><div class="body"><div class="bar" aria-hidden="true"><span class="dots"><i></i><i></i><i></i></span><span class="address">${P.lock}${lang === "ar" ? "مكتبة" : "Maktaba"}</span></div><div class="screen"><picture><source type="image/avif" srcset="${set("avif")}" sizes="${sizes}" /><img src="${small.webp}" srcset="${set("webp")}" sizes="${sizes}" width="${m.width}" height="${m.height}" alt="${esc(plain(alt))}" lang="${shotLang}" data-nomirror loading="lazy" decoding="async" /></picture></div></div></div></div><figcaption>${caption}</figcaption></figure>`;
}

/** The structured data: Maktaba as a SoftwareApplication of this release. */
export function softwareJsonLd(data, ks, lang) {
  const os = { mac: "macOS", windows: "Windows", linux: "Linux", nas: "Docker" };
  const ld = {
    "@context": "https://schema.org",
    "@type": "SoftwareApplication",
    name: lang === "ar" ? "مكتبة" : "Maktaba",
    alternateName: lang === "ar" ? "Maktaba" : "مكتبة",
    applicationCategory: "MultimediaApplication",
    operatingSystem: ks
      .map((k) => os[k.id])
      .filter(Boolean)
      .join(", "),
    softwareVersion: data.version,
    datePublished: data.published.slice(0, 10),
    downloadUrl: `${ORIGIN}${inLang(lang, PATH)}`,
    url: `${ORIGIN}${inLang(lang, "/")}`,
    inLanguage: lang,
    license: "https://opensource.org/licenses/MIT",
    isAccessibleForFree: true,
    offers: { "@type": "Offer", price: "0", priceCurrency: "USD" },
  };
  return `<script type="application/ld+json">${JSON.stringify(ld).replace(/</g, "\\u003c")}</script>`;
}

/** The page's <main> content. */
export function renderBody(data, ks, { lang, resolves = () => false, manifest }) {
  const c = readCopy(lang);
  const h = readCopy(lang, "home");
  const t = (key) => word(c, key, lang);
  const notes = ks
    .filter((k) => k.ua)
    .map((k) => `<span data-for="${k.id}">${t(k.id === "mac" ? "note" : `note.${k.id}`)}</span>`)
    .join("");
  const other = lang === "ar" ? "en" : "ar";
  const pair = [lang, other]
    .map(
      (l) =>
        `<li>${shot(lang, l, { alt: word(h, `arabic.${l}.alt`, lang), caption: word(h, `arabic.${l}.caption`, lang), manifest })}</li>`
    )
    .join("\n        ");
  return `<section class="page-head" aria-labelledby="dl-title">
  <div class="frame">
    <h1 id="dl-title">${t("h1")}</h1>
    <p class="lead">${t("lead")}</p>
    <p class="meta" data-release="version">${t("version").replace("{version}", data.version).replace("{date}", releaseDate(data.published, lang))}</p>
    ${notes ? `<p class="platform-note">${P.laptop}${notes}</p>` : ""}
  </div>
</section>
<section class="band">
  <div class="frame">
    <ul class="cards">
      ${ks.map((k) => card(k, data, { c, lang, resolves })).join("\n      ")}
    </ul>
    <p class="all-files"><a href="${esc(data.url)}">${t("all")}${ICONS.out}</a></p>
  </div>
</section>
<section class="band flush-start" aria-labelledby="need-title">
  <div class="frame">
    <div class="need">
      <div>
        <h2 class="display-2" id="need-title">${t("need.h2")}</h2>
        <p>${t("need.p1")}</p>
        <p>${t("need.p2")}</p>
        <a class="onward" href="${inLang(lang, "/install/")}">${t("need.link")}${ICONS.onward}</a>
      </div>
      <ul class="diagram" aria-hidden="true">
        <li class="node home"><span class="ring">${P.server}</span>${t("need.home")}</li>
        <li class="arrow">${ICONS.onward}</li>
        <li><ul class="screens">
          <li>${P.tv}${t("need.tv")}</li>
          <li>${P.phone}${t("need.phone")}</li>
          <li>${P.laptop}${t("need.computer")}</li>
        </ul></li>
      </ul>
    </div>
  </div>
</section>
<section class="band shot" aria-labelledby="arabic-title">
  <div class="frame">
    <div class="section-head">
      <p class="kicker">${P.star}${word(h, "arabic.kicker", lang)}</p>
      <h2 class="display-2" id="arabic-title">${word(h, "arabic.h2", lang)}</h2>
      <p>${word(h, "arabic.lead", lang)}</p>
    </div>
    <ul class="pair">
        ${pair}
    </ul>
  </div>
</section>`;
}

const hashed = (name, ext, content) =>
  `assets/${name}-${crypto.createHash("sha256").update(content).digest("base64url").slice(0, 10)}.${ext}`;

/** The page's own stylesheet: part B's components (design/site.css), then what the built page
 * adds (src/download.css). Loaded after the shell's, on this page only, as design/site.css says. */
export function styleFile() {
  // part B's stylesheet (design/site.css, src/part-b.css) comes with part B's shell; this page
  // adds only its own
  const css = fs.readFileSync(path.join(PKG, "src/download.css"), "utf8");
  return { file: hashed("download", "css", css), content: css };
}

/** The pages, { lang, path, out, title, description, body, head, meta }, for page(). */
export function downloadPages(data, ks, { resolves = () => false, styles, script, manifest }) {
  // part B's shell (as the home page): its header and footer, the wordmark, the display face's
  // preloads and part B's stylesheet; Download and Help go to their pages once built (bLinks)
  const links = bLinks(resolves);
  const uaCards = ks
    .filter((k) => k.ua)
    .map((k) => k.id)
    .join(" ");
  return LANGS.map((lang) => {
    const c = readCopy(lang);
    return {
      lang,
      path: PATH,
      out: `${lang === "ar" ? "ar/" : ""}download/index.html`,
      title: `${plain(word(c, "meta.title", lang))} · ${lang === "ar" ? "مكتبة" : "Maktaba"}`,
      description: plain(word(c, "meta.description", lang)),
      social: plain(word(c, "social.title", lang)),
      variant: "b",
      links,
      meta: `<meta name="maktaba-release" content="${data.tag}" />`,
      head: [
        `<script src="${script}" data-cards="${uaCards}"></script>`,
        `<link rel="stylesheet" href="${styles}" />`,
        softwareJsonLd(data, ks, lang),
      ].join("\n  "),
      body: renderBody(data, ks, { lang, resolves, manifest }),
    };
  });
}

export function readManifest() {
  return JSON.parse(fs.readFileSync(path.join(PKG, "shots-manifest.json"), "utf8"));
}

/** build.mjs's family: the release read (or the build fails), the cards, both pages. */
export const DOWNLOAD = {
  name: "download",
  story: "60.11",
  async build({ write, resolves, script, env = process.env, log = console.log }) {
    const got = await readRelease({ env });
    const data = releaseData(got, { expect: env.WEBSITE_EXPECT_RELEASE || "" });
    const ks = cards(data, readPlatforms(), env);
    for (const need of ["mac", "windows", "linux"])
      if (!ks.some((k) => k.id === need))
        log(`::warning::download page: ${data.tag} has no ${need} file; its card is left out`);
    const s = styleFile();
    write(s.file, s.content);
    const pages = downloadPages(data, ks, {
      resolves,
      styles: `/${s.file}`,
      script: script("download-platform.ts"),
      manifest: readManifest(),
    });
    for (const p of pages) write(p.out, page(p));
    log(`download page: ${data.tag} (${got.source}), cards: ${ks.map((k) => k.id).join(", ")}`);
    return pages.length;
  },
};
