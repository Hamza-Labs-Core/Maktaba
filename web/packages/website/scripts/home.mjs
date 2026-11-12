// maktaba.tv's home page, `/` and `/ar/` (Epic 60 story 60.10): the layout of 60.01 part B's
// home mockup (design/mockups/part-b.mjs, `home`), its words from 60.02's copy
// (copy/home.<lang>.md, read as the mockups read it), and the apps' real screenshots of 60.09
// (static/shots/, shots-manifest.json) in part B's device frames. It replaces Phase 1's front
// door at the same paths.
//
// The sections, in the mockup's order: the hero (the tagline, Download, How it works, the TV);
// on every screen (the shelf); what it does for you (six features); Arabic and English (the
// pair); your library stays yours; get started (three steps). The page is drawn in part B's
// shell (shell.mjs, `variant: "b"`).
//
// What is linked depends on what the site has built (`resolves`): Download goes to /download/
// once story 60.11 builds it, until then to the latest release; a feature links its help topic
// once story 60.12 builds it, until then the install guides or nothing (FEATURES). The page's own
// script, src/home.ts, preselects the visitor's system on the download page.
//
// Screenshots (SHOTS): each a <picture>, AVIF then WebP, at every width of its srcset, with its
// width, height and alt text in the page's language; the hero's is preloaded and fetched first,
// every other one lazy. Which picture each frame shows, and why, is in design/review-phase-2.md.

import fs from "node:fs";
import path from "node:path";
import { stripTypeScriptTypes } from "node:module";
import crypto from "node:crypto";
import { fileURLToPath } from "node:url";
import { ICONS, inLang, esc } from "./shell.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");

// ---- copy ---------------------------------------------------------------------------------------

/** A copy file (copy/<name>.<lang>.md): `## key` headings, each followed by its text, as HTML
 * (escaped; `code` and **strong** kept). The same reader as design/mockups/part-b.mjs. */
export function readCopy(name, lang) {
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
    } else if (key && line.trim()) {
      buf.push(line.trim());
    }
  }
  flush();
  return out;
}

const escText = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const inline = (s) =>
  escText(s)
    .replace(/`([^`]+)`/g, "<code>$1</code>")
    .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");

/** The text of a copy string, for an attribute or a <title>: its tags dropped, its entities
 * read back (esc() escapes it again where it is written). */
export const plain = (s) => {
  let out = s;
  for (let prev = ""; prev !== out; ) {
    prev = out;
    out = out.replace(/<[^<>]*>/g, "");
  }
  return out.replace(/&lt;/g, "<").replace(/&gt;/g, ">").replace(/&amp;/g, "&");
};

/** A copy string by key, or the build fails naming the file and the key. */
function take(copy, name, lang) {
  return (key) => {
    const v = copy[key];
    if (v === undefined) throw new Error(`copy/${name}.${lang}.md: no "## ${key}"`);
    return v;
  };
}

// ---- icons (part B's, design/mockups/part-b.mjs `P`: the shell's style) -----------------------

const svg = (body) =>
  `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;

export const P = {
  star: svg('<path d="M12 3v18M3 12h18M5.6 5.6l12.8 12.8M18.4 5.6L5.6 18.4"/>'),
  library: svg('<path d="M5 4v16M9 4v16M13.5 5l4.5 14.5M4 20h16"/>'),
  subtitles: svg(
    '<rect x="3.5" y="5.5" width="17" height="13" rx="2"/><path d="M7 14h4M13 14h4M7 11h10"/>'
  ),
  live: svg(
    '<circle cx="12" cy="12" r="1.8"/><path d="M8.2 8.2a5.4 5.4 0 0 0 0 7.6M15.8 8.2a5.4 5.4 0 0 1 0 7.6M5.4 5.4a9.3 9.3 0 0 0 0 13.2M18.6 5.4a9.3 9.3 0 0 1 0 13.2"/>'
  ),
  search: ICONS.search,
  updates: svg('<path d="M19.5 12a7.5 7.5 0 1 1-2.2-5.3M19.5 4.5v4h-4"/>'),
  server: svg(
    '<rect x="4.5" y="3.5" width="15" height="7" rx="1.5"/><rect x="4.5" y="13.5" width="15" height="7" rx="1.5"/><path d="M8 7h.01M8 17h.01"/>'
  ),
  check: svg('<path d="M5 12.5l4.5 4.5L19 7.5"/>'),
  download: svg('<path d="M12 4v11M7 10.5l5 5 5-5M5 19.5h14"/>'),
  lock: svg(
    '<rect x="5.5" y="10.5" width="13" height="9" rx="1.5"/><path d="M8.5 10.5V8a3.5 3.5 0 0 1 7 0v2.5"/>'
  ),
};

const kicker = (text) => `<p class="kicker">${P.star}${text}</p>`;

// The arch as a single line, for the hero's stage and the privacy band (symmetric: it never
// mirrors).
const ARCH_LINE = `<svg class="arch-line" data-nomirror viewBox="0 0 360 400" fill="none" stroke="currentColor" stroke-width="1.5" aria-hidden="true" focusable="false"><path vector-effect="non-scaling-stroke" d="M24 400V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V400"/><path vector-effect="non-scaling-stroke" opacity="0.45" d="M42 400V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V400"/></svg>`;

// ---- the screenshots ----------------------------------------------------------------------------

export const SHOTS_MANIFEST = path.join(PKG, "shots-manifest.json");

/**
 * Which capture each frame shows (60.09's, in shots-manifest.json), `{lang}` the picture's
 * language. Chosen for what the screen shows and for the rules the pictures keep: no Arabic
 * screen with Eastern Arabic digits where a clean one exists (#1199: not the Android TV player
 * or home in Arabic), no Mac window in Arabic until it mirrors (#1198: the English one on both
 * pages), and no app named as available that is not publicly installable (Q5: the TV and the
 * phone show the screens, the captions name no app). design/review-phase-2.md has each choice.
 */
export const SHOTS = {
  hero: { kind: "tv", key: "appletv-home-{lang}" },
  tv: { kind: "tv", key: "androidtv-guide-{lang}" },
  phone: { kind: "phone", key: "iphone-home-{lang}" },
  tablet: { kind: "tablet", key: "web-search-{lang}" },
  computer: { kind: "window", key: "mac-library-en", chrome: "own" },
  browser: { kind: "browser", key: "web-home-{lang}" },
  pair: { kind: "browser", key: "web-home-{lang}" },
};

/** How wide each frame's screen draws, for `sizes` (design/site.css: the hero's 7/12 column from
 * 1024 px; the shelf's TV 62%/70%/100%, phone 17%/22%/46%, the others a third, a half or the
 * whole; the pair a half from 768 px). The frame's content box is at most 1200 px. */
export const SIZES = {
  hero: "(min-width: 1024px) 640px, calc(100vw - 40px)",
  tv: "(min-width: 1024px) 680px, (min-width: 768px) 68vw, calc(100vw - 40px)",
  phone: "(min-width: 1024px) 180px, (min-width: 768px) 20vw, 42vw",
  tablet: "(min-width: 1024px) 360px, (min-width: 768px) 46vw, calc(100vw - 40px)",
  // the Mac capture comes at 640, 960 and 1024: a phone at 412 px (Lighthouse's, 1.75×) takes 640
  computer: "(min-width: 1024px) 360px, (min-width: 768px) 46vw, calc(100vw - 48px)",
  browser: "(min-width: 1024px) 360px, (min-width: 768px) 46vw, calc(100vw - 40px)",
  pair: "(min-width: 1024px) 560px, (min-width: 768px) 46vw, calc(100vw - 40px)",
};

let manifestMemo = null;
export function shotsManifest() {
  manifestMemo ??= JSON.parse(fs.readFileSync(SHOTS_MANIFEST, "utf8"));
  return manifestMemo;
}

/** A capture's entry in the manifest, or the build fails naming it. */
export function shot(key) {
  const m = shotsManifest().images[key];
  if (!m) throw new Error(`shots-manifest.json: no image ${key}`);
  return m;
}

const srcset = (m, fmt) => m.sizes.map((s) => `${s[fmt]} ${s.width}w`).join(", ");

/** A screenshot as a <picture>: AVIF, then WebP, at every width, with the intrinsic width and
 * height (the frame is sized before it loads: CLS 0), its alt text and its language. The hero
 * is fetched first; every other one is lazy. Screenshots never mirror. */
export function picture(key, { alt, sizes, eager = false }) {
  const m = shot(key);
  const fallback = m.sizes[Math.min(1, m.sizes.length - 1)];
  const load = eager ? 'fetchpriority="high" decoding="async"' : 'loading="lazy" decoding="async"';
  return `<picture><source type="image/avif" srcset="${srcset(m, "avif")}" sizes="${sizes}" /><img src="${fallback.webp}" srcset="${srcset(m, "webp")}" sizes="${sizes}" width="${m.width}" height="${m.height}" alt="${esc(alt)}" lang="${m.lang}" data-nomirror ${load} /></picture>`;
}

/** The hero's preload: the AVIF the <picture> picks, by the same srcset and sizes. */
export function heroPreload(lang) {
  const m = shot(SHOTS.hero.key.replace("{lang}", lang));
  return `<link rel="preload" as="image" type="image/avif" imagesrcset="${srcset(m, "avif")}" imagesizes="${SIZES.hero}" fetchpriority="high" />`;
}

const BAR = {
  window: (lang) =>
    `<div class="bar" aria-hidden="true"><span class="dots"><i></i><i></i><i></i></span><span class="title">${lang === "ar" ? "مكتبة" : "Maktaba"}</span></div>`,
  browser: (lang) =>
    `<div class="bar" aria-hidden="true"><span class="dots"><i></i><i></i><i></i></span><span class="address">${P.lock}${lang === "ar" ? "مكتبة" : "Maktaba"}</span></div>`,
};

/** A screenshot in its device frame (design/README.md, "Imagery and icons"), with a caption:
 * the screen's name and one line. */
function device(slot, lang, { alt, name, caption, shotLang = lang, eager = false }) {
  const s = SHOTS[slot];
  const key = s.key.replace("{lang}", shotLang);
  const img = picture(key, { alt, sizes: SIZES[slot], eager });
  const bar = s.chrome === "own" ? "" : (BAR[s.kind]?.(lang) ?? "");
  const stand = s.kind === "tv" ? '<span class="stand"></span>' : "";
  const cap =
    name || caption
      ? `<figcaption>${name ? `<strong>${name}</strong>` : ""}${caption ?? ""}</figcaption>`
      : "";
  const chrome = s.chrome ? ` data-chrome="${s.chrome}"` : "";
  return `<figure class="device" data-kind="${s.kind}"${chrome} data-nomirror><div class="frame-wrap"><div class="frame-fit"><div class="body">${bar}<div class="screen">${img}</div></div>${stand}</div></div>${cap}</figure>`;
}

// ---- what it does for you (T04) ----------------------------------------------------------------

/**
 * The six features the page names, each with the epic that ships it (specs/epics/<epic>/), the
 * app's help topic that explains it (web/src/help/content/<lang>/<topic>.md, which story 60.12
 * publishes at /help/<topic>/), and, until that page exists, what the feature links instead:
 * the install guides, or nothing (story 60.10, "What it does for you"). home.test.mjs checks
 * each against the epics and the help topics (T04).
 */
export const FEATURES = [
  {
    key: "library",
    icon: "library",
    epic: "09-library-management",
    topic: "library",
    fallback: null,
  },
  {
    key: "subtitles",
    icon: "subtitles",
    epic: "03-transcription",
    topic: "transcription",
    fallback: null,
  },
  { key: "live", icon: "live", epic: "27-live-channels", topic: "watch-tv", fallback: null },
  { key: "search", icon: "search", epic: "05-search-indexing", topic: "search", fallback: null },
  { key: "updates", icon: "updates", epic: "28-auto-update", topic: "updates", fallback: null },
  { key: "runs", icon: "server", epic: "59-nas", topic: null, fallback: "/install/" },
];

/** Where a feature links: its help topic once built, else its fallback, else nowhere (null). */
export function featureHref(f, resolves) {
  if (f.topic && resolves(`/help/${f.topic}/`)) return `/help/${f.topic}/`;
  return f.fallback;
}

// ---- the page -----------------------------------------------------------------------------------

/** The systems the Download button names when src/home.ts finds one (copy/download.*.md's
 * label for each); any other, or none: "Download Maktaba". */
const NAMED = ["mac", "windows"];

/**
 * The home page in a language: `{ title, description, social, meta, body, mainAttrs }` for
 * shell.mjs's page(). `links` are part B's (shell.mjs bLinks); `resolves` says whether a site
 * path is built.
 */
export function homePage(lang, { links, resolves = () => false }) {
  const c = take(readCopy("home", lang), "home", lang);
  const d = take(readCopy("download", lang), "download", lang);
  const other = lang === "ar" ? "en" : "ar";
  const local = (href) => (/^https?:/.test(href) ? href : inLang(lang, href));
  // until story 60.11 builds /download/, Download goes to the latest release on GitHub
  const download = local(links.download);
  const ext = /^https?:/.test(links.download) ? ` ${ICONS.out}` : "";
  const dl = (cls, label) =>
    `<a class="${cls}" href="${download}" data-download>${P.download}${label}${ext}</a>`;

  const hero = `<section class="hero" aria-labelledby="hero-title">
  <div class="frame grid">
    <div>
      <h1 class="display-1" id="hero-title">${c("tagline")}</h1>
      <p class="lead">${c("hero.lead")}</p>
      <div class="actions">
        ${dl(
          "button for-platform",
          `<span data-for="any">${c("start.download")}</span>${NAMED.map(
            (p) => `<span data-for="${p}">${d(`${p}.primary`)}</span>`
          ).join("")}`
        )}
        <a class="button quiet" href="#how">${c("hero.how")}</a>
      </div>
      <p class="fine">${c("hero.fine")}</p>
    </div>
    <div class="stage">
      ${ARCH_LINE}
      ${device("hero", lang, { alt: plain(c("hero.shot.alt")), eager: true })}
    </div>
  </div>
</section>`;

  const screens = ["tv", "phone", "tablet", "computer", "browser"];
  const shelf = `<section class="band shot" id="how" aria-labelledby="screens-title">
  <div class="frame">
    <div class="section-head center">
      ${kicker(c("screens.kicker"))}
      <h2 class="display-2" id="screens-title">${c("screens.h2")}</h2>
      <p>${c("screens.lead")}</p>
    </div>
    <ul class="shelf">
      ${screens
        .map(
          (k) =>
            `<li data-kind="${SHOTS[k].kind}">${device(k, lang, {
              alt: plain(c(`screens.${k}.alt`)),
              name: c(`screens.${k}.name`),
              caption: c(`screens.${k}.caption`),
            })}</li>`
        )
        .join("\n      ")}
    </ul>
  </div>
</section>`;

  const does = `<section class="band" aria-labelledby="does-title">
  <div class="frame">
    <div class="section-head">
      ${kicker(c("does.kicker"))}
      <h2 class="display-2" id="does-title">${c("does.h2")}</h2>
    </div>
    <ul class="features">
      ${FEATURES.map((f) => {
        const href = featureHref(f, resolves);
        const link = href
          ? `\n        <a class="onward" href="${local(href)}">${c(`does.${f.key}.link`)}${ICONS.onward}</a>`
          : "";
        return `<li class="feature" data-feature="${f.key}">
        <span class="ring">${P[f.icon]}</span>
        <h3>${c(`does.${f.key}.title`)}</h3>
        <p>${c(`does.${f.key}.text`)}</p>${link}
      </li>`;
      }).join("\n      ")}
    </ul>
  </div>
</section>`;

  const pair = `<section class="band shot" aria-labelledby="arabic-title">
  <div class="frame">
    <div class="section-head">
      ${kicker(c("arabic.kicker"))}
      <h2 class="display-2" id="arabic-title">${c("arabic.h2")}</h2>
      <p>${c("arabic.lead")}</p>
    </div>
    <ul class="pair">
      ${[lang, other]
        .map(
          (l) =>
            `<li>${device("pair", lang, {
              shotLang: l,
              alt: plain(c(`arabic.${l}.alt`)),
              caption: c(`arabic.${l}.caption`),
            })}</li>`
        )
        .join("\n      ")}
    </ul>
  </div>
</section>`;

  const stays = `<section class="band" aria-labelledby="stays-title">
  <div class="frame stays">
    <div>
      <div class="section-head">
        ${kicker(c("stays.kicker"))}
        <h2 class="display-2" id="stays-title">${c("stays.h2")}</h2>
      </div>
      <ul class="promises">
        ${["home", "say", "ads", "open"]
          .map(
            (k) =>
              `<li>${P.check}<div><strong>${c(`stays.${k}.title`)}</strong><span>${c(`stays.${k}.text`)}</span></div></li>`
          )
          .join("\n        ")}
      </ul>
      <p class="more"><a class="onward" href="${local("/legal/privacy/")}">${c("stays.link")}${ICONS.onward}</a></p>
    </div>
    ${ARCH_LINE}
  </div>
</section>`;

  const start = `<section class="band section" aria-labelledby="start-title">
  <div class="frame">
    <div class="section-head center">
      ${kicker(c("start.kicker"))}
      <h2 class="display-2" id="start-title">${c("start.h2")}</h2>
    </div>
    <ol class="start-steps">
      ${[1, 2, 3]
        .map((n) => `<li><h3>${c(`start.${n}.title`)}</h3><p>${c(`start.${n}.text`)}</p></li>`)
        .join("\n      ")}
    </ol>
    <div class="start-actions">
      ${dl("button", c("start.download"))}
      <a class="button quiet" href="${local("/install/")}">${c("start.guides")}${ICONS.onward}</a>
    </div>
  </div>
</section>`;

  return {
    title: plain(c("meta.title")),
    description: plain(c("meta.description")),
    social: plain(c("social.title")),
    meta: heroPreload(lang),
    mainAttrs: 'class="home" data-page="home"',
    body: [hero, shelf, does, pair, stays, start].join("\n"),
  };
}

/** The home page's social card title: the tagline (part B's social-home card). */
export const homeCardTitle = (lang) => plain(take(readCopy("home", lang), "home", lang)("tagline"));

// ---- the page's script ----------------------------------------------------------------------------

/** src/home.ts as the classic script the <head> runs before the first paint: its types and its
 * `export`s stripped, under a content-hashed name. `{ file, js }`. */
export function homeScriptFile() {
  const ts = fs.readFileSync(path.join(PKG, "src/home.ts"), "utf8");
  const js = stripTypeScriptTypes(ts).replace(/^export /gm, "");
  if (/\b(import|export)\b/.test(js.replace(/\/\/.*$/gm, "")))
    throw new Error("src/home.ts: a classic script may not import or export");
  const hash = crypto.createHash("sha256").update(js).digest("base64url").slice(0, 10);
  return { file: `assets/home-${hash}.js`, js };
}
