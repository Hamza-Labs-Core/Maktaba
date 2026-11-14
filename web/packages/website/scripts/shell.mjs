// The page shell of maktaba.tv (story 60.01 part A, approved 2026-11-11, built here): the
// document around every page the site builds. The header with the nav, the phone menu and the
// language switch; the language hint (Epic 60 README §6.5); the footer; the skip link; the
// page's live region. The spec is design/README.md; the look is design/shell.css on
// design/site-tokens.css and design/fonts.css, with what only the built site needs in
// src/site.css; the behaviour is design/shell.js (menu, copy buttons, the hint's Hide) and
// src/shell-head.js (before the first paint: script on, the hint shown or not).
//
// The pages keep their content apart from this file: a hand-made page is its <main> (src/pages/),
// a generated page one render function (renderLegal, renderProblem, renderIndex), and an install
// guide (Epic 59's build) is wrapped as it is (inShell). shellAssets() publishes the shell's
// files under content-hashed names in /assets/ (cached for a year by the Worker): one stylesheet,
// the two scripts, the four fonts of Maktaba Text and the mark.
//
// Every page carries <meta name="maktaba-site" content="maktaba-website">: the deploy's smoke
// (tools/cloudflare/smoke.mjs) looks for it to tell this Worker's answers from any other's.
//
// Every page also declares whether its shell has a phone menu, <meta name="maktaba-menu">
// (SHELL_MENU): "phone" since this shell has one. The keyboard tests (e2e/keyboard.spec.ts)
// open and close the menu on every page that declares one, and fail a page that declares
// nothing or declares none while its header has a menu button.

import crypto from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { esc, headHtml, inLang, organizationJsonLd } from "./head.mjs";

export { esc, inLang };
export const ORIGIN = "https://maktaba.tv";
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";
export const LATEST_RELEASE = `${GITHUB}/releases/latest`;
export const DEVPORTAL = "https://maktaba.dev/";
/** The install guides' troubleshooting page: where part A linked Help until the help pages
 * (60.12), and where a problem page's "Who can help" still sends a household. */
export const TROUBLESHOOTING = "/install/troubleshooting/";
/** Help: the help pages (story 60.12), which every build makes before the pages that link them. */
export const HELP = "/help/";

export const SITE_MARKER = '<meta name="maktaba-site" content="maktaba-website" />';

/** This shell's phone menu: "phone" (a Menu button folds the nav under 768 px), or "none". */
export const SHELL_MENU = "phone";
export const menuMeta = (menu = SHELL_MENU) => `<meta name="maktaba-menu" content="${menu}" />`;

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
const DESIGN = path.join(PKG, "design");

// ---- the shell's files ------------------------------------------------------------------------

/** The fonts each language preloads: an English page its Latin pair (29 KB), an Arabic page all
 * four (83 KB): its Latin names, codes and numbers are set in both weights too, and a weight that
 * arrived after the first paint would move the lines it is in. design/fonts.css has them all. */
export const PRELOAD = {
  en: ["400-latin", "600-latin"],
  ar: ["400-arabic", "600-arabic", "400-latin", "600-latin"],
};
export const FONTS = ["400-latin", "600-latin", "400-arabic", "600-arabic"];

const hashed = (name, ext, content) =>
  `assets/${name}-${crypto.createHash("sha256").update(content).digest("base64url").slice(0, 10)}.${ext}`;

let memo = null;

/**
 * The shell's files as the site serves them: `files` ([{ file, content }], each under /assets/
 * with a content hash), and the paths a page names: `css`, `head` (the head script), `js` (the
 * shell script), `fonts` (by weight and script) and `mark` (the app icon at 64 px, drawn at 32
 * and 24). Computed once per process from the sources.
 */
export function shellAssets() {
  if (memo) return memo;
  const files = [];
  const fonts = {};
  for (const f of FONTS) {
    const content = fs.readFileSync(path.join(PKG, `static/fonts/maktaba-text-${f}.woff2`));
    const file = hashed(`maktaba-text-${f}`, "woff2", content);
    files.push({ file, content });
    fonts[f] = `/${file}`;
  }
  // the text face's files are hashed above (pages preload them); any other face fonts.css
  // names (part B's display face, Maktaba Display) is published, hashed, as it is met
  const published = {};
  const display = {};
  const fontsCss = fs
    .readFileSync(path.join(DESIGN, "fonts.css"), "utf8")
    .replace(
      /url\("\.\.\/static\/fonts\/maktaba-(text|display)-([a-z0-9-]+)\.woff2"\)/g,
      (m, face, f) => {
        if (face === "text") {
          if (!fonts[f]) throw new Error(`design/fonts.css: no font ${f}`);
          return `url("${fonts[f]}")`;
        }
        const name = `maktaba-${face}-${f}`;
        const source = path.join(PKG, `static/fonts/${name}.woff2`);
        if (!fs.existsSync(source)) throw new Error(`design/fonts.css: no font ${name}`);
        if (!published[name]) {
          const content = fs.readFileSync(source);
          const file = hashed(name, "woff2", content);
          files.push({ file, content });
          published[name] = `/${file}`;
          if (face === "display") display[f.replace(/^700-/, "")] = published[name];
        }
        return `url("${published[name]}")`;
      }
    );
  if (/url\("?\.\.\//.test(fontsCss))
    throw new Error("design/fonts.css: a URL the build does not publish");
  const css = [
    fs.readFileSync(path.join(DESIGN, "site-tokens.css"), "utf8"),
    fontsCss,
    fs.readFileSync(path.join(DESIGN, "shell.css"), "utf8"),
    fs.readFileSync(path.join(PKG, "src/site.css"), "utf8"),
  ].join("\n");
  const add = (name, ext, content) => {
    const file = hashed(name, ext, content);
    files.push({ file, content });
    return `/${file}`;
  };
  // part B's stylesheet (design/site.css: the hero, device frames, feature blocks, the
  // wordmark), with what only the built site needs (src/part-b.css), for the part B pages only
  const partB = [
    fs.readFileSync(path.join(DESIGN, "site.css"), "utf8"),
    fs.readFileSync(path.join(PKG, "src/part-b.css"), "utf8"),
  ].join("\n");
  memo = {
    files,
    fonts,
    display,
    css: add("site", "css", css),
    partB: add("part-b", "css", partB),
    head: add("head", "js", fs.readFileSync(path.join(PKG, "src/shell-head.js"), "utf8")),
    js: add("shell", "js", fs.readFileSync(path.join(DESIGN, "shell.js"), "utf8")),
    mark: add("mark", "png", fs.readFileSync(path.join(PKG, "src/mark-64.png"))),
  };
  return memo;
}

/** The shell's last element (the footer's live region), which the first paint waits for. */
export const LAST_ID = "announce";

/**
 * The first paint waits for the whole document: `<link rel="expect" blocking="render">` names
 * the shell's last element (LAST_ID), and the browser draws nothing until it has parsed it. The
 * parser hands the page over in pieces (it yields when a piece takes long, or when the rest has
 * not arrived), and a paint between two pieces drew a header with no language switch yet: the
 * nav sat where the switch belongs and slid over when the switch was parsed (a layout shift of
 * 0.0007 to 0.0016 at 1280 px, on a different page each time: e2e/cls.spec.ts on #1187 and
 * #1204). A document that ends without the element is drawn when its parsing ends, and a
 * browser that does not know `expect` draws as before.
 */
export const RENDER_WHOLE = `<link rel="expect" href="#${LAST_ID}" blocking="render" />`;

/** The display face's files a part B page preloads (design/README.md, "Display face"): an
 * English page its Latin file, an Arabic page both (its Latin names are set in it too). */
export const PRELOAD_DISPLAY = { en: ["latin"], ar: ["arabic", "latin"] };

/** What the shell puts in every <head>: the wait for the whole document (RENDER_WHOLE), the
 * fonts its language preloads, the stylesheet, the head script (before the first paint) and the
 * shell script (deferred). A part B page (`variant: "b"`) also preloads the display face and
 * loads part B's stylesheet after the shell's; `headScripts` are a page's own scripts, run
 * before the first paint. */
export function shellHead(lang, indent = "  ", { variant = "a", headScripts = [] } = {}) {
  const a = shellAssets();
  const b = variant === "b";
  const preload = (href) =>
    `<link rel="preload" href="${href}" as="font" type="font/woff2" crossorigin />`;
  return [
    RENDER_WHOLE,
    ...PRELOAD[lang].map((f) => preload(a.fonts[f])),
    ...(b ? PRELOAD_DISPLAY[lang].map((f) => preload(a.display[f])) : []),
    `<script src="${a.head}"></script>`,
    ...headScripts.map((src) => `<script src="${src}"></script>`),
    `<link rel="stylesheet" href="${a.css}" />`,
    ...(b ? [`<link rel="stylesheet" href="${a.partB}" />`] : []),
    `<script src="${a.js}" defer></script>`,
  ]
    .map((l) => `${indent}${l}`)
    .join("\n");
}

// ---- icons --------------------------------------------------------------------------------------
// design/README.md, "RTL and icons": a 24-unit grid drawn at 20 px, 1.75 stroke, currentColor,
// hidden from assistive technology. `dir-icon` (with data-mirror, which e2e/rtl.spec.ts checks)
// marks the ones that mean a direction: they mirror on an Arabic page.

const svg = (body, mirror = false) =>
  `<svg${mirror ? ' class="dir-icon" data-mirror' : ""} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;

export const ICONS = {
  onward: svg('<path d="M5 12h14M13 6l6 6-6 6"/>', true),
  back: svg('<path d="M15 6l-6 6 6 6"/>', true),
  out: svg(
    '<path d="M14 5h5v5M19 5l-8 8M18 14v4a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h4"/>',
    true
  ),
  menu: svg('<path d="M4 7h16M4 12h16M4 17h16"/>'),
  lang: svg(
    '<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.6 2.6 3.6 5.6 3.6 8.5s-1 5.9-3.6 8.5c-2.6-2.6-3.6-5.6-3.6-8.5s1-5.9 3.6-8.5z"/>'
  ),
  copy: svg(
    '<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6a1.5 1.5 0 0 0-1.5-1.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5"/>'
  ),
  note: svg('<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8h.01"/>'),
  warning: svg('<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>'),
  tip: svg(
    '<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>'
  ),
  search: svg('<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4 4"/>'),
  person: svg(
    '<circle cx="12" cy="8" r="3.5"/><path d="M5 20c.8-3.6 3.6-5.5 7-5.5s6.2 1.9 7 5.5"/>'
  ),
};

/** The arch of the app's icon, the front door's and the 404's one decoration (from 1024 px):
 * symmetric, so it never mirrors; its colours come from shell.css (accent.mark). */
export const ARCH = `<svg class="arch" data-nomirror viewBox="0 0 360 460" aria-hidden="true" focusable="false">
      <defs><radialGradient id="lamp" cx="180" cy="372" r="190" gradientUnits="userSpaceOnUse"><stop offset="0" stop-opacity="0.34"/><stop offset="0.55" stop-opacity="0.08"/><stop offset="1" stop-opacity="0"/></radialGradient></defs>
      <path d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460Z" fill="url(#lamp)"/>
      <path class="line" d="M24 460V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V460"/>
      <path class="line soft" d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460"/>
      <path class="line" d="M112 392C140 378 166 378 180 390C194 378 220 378 248 392M180 390V366M112 392V404C140 392 166 392 180 404C194 392 220 392 248 404V392"/>
    </svg>`;

// ---- the shell's words ----------------------------------------------------------------------

export const T = {
  en: {
    name: "Maktaba",
    skip: "Skip to the page",
    menu: "Menu",
    nav: "Main",
    install: "Install",
    messages: "Messages",
    download: "Download",
    other: { lang: "ar", dir: "rtl", label: "العربية" },
    footerAbout:
      "Maktaba keeps a household’s films and series on its own computer and plays them on every screen at home. Free and open source, under the MIT licence.",
    groups: [
      [
        "Get Maktaba",
        [
          ["Install guides", "/install/"],
          ["Downloads on GitHub", LATEST_RELEASE],
        ],
      ],
      [
        "Help",
        [
          ["Help topics", HELP],
          ["Messages and what they mean", "/problems/"],
        ],
      ],
      [
        "Legal",
        [
          ["Privacy", "/legal/privacy/"],
          ["Terms", "/legal/terms/"],
          ["Copyright", "/legal/copyright/"],
        ],
      ],
      [
        "Elsewhere",
        [
          ["maktaba.dev, for developers", DEVPORTAL],
          ["Source on GitHub", GITHUB],
          ["Security", "/.well-known/security.txt"],
        ],
      ],
    ],
    footerNav: "Site",
    copyright: "© 2026 Hamza Labs",
    readIn: "اقرأ هذه الصفحة بالعربية",
    // the language hint, written in the language it offers
    hint: { text: "هذه الصفحة متوفرة بالعربية أيضًا.", link: "قراءتها بالعربية", hide: "إخفاء" },
  },
  ar: {
    name: "مكتبة",
    skip: "الانتقال إلى محتوى الصفحة",
    menu: "القائمة",
    nav: "الرئيسية",
    install: "التثبيت",
    messages: "الرسائل",
    download: "التنزيل",
    other: { lang: "en", dir: "ltr", label: "English" },
    footerAbout:
      "يحفظ برنامج مكتبة أفلام البيت ومسلسلاته على حاسوبه الخاص، ويعرضها على كل شاشة فيه. وهو برنامج حرّ مفتوح المصدر برخصة MIT.",
    groups: [
      [
        "الحصول على مكتبة",
        [
          ["أدلة التثبيت", "/install/"],
          ["التنزيلات على GitHub", LATEST_RELEASE],
        ],
      ],
      [
        "المساعدة",
        [
          ["مواضيع المساعدة", HELP],
          ["الرسائل ومعانيها", "/problems/"],
        ],
      ],
      [
        "الشؤون القانونية",
        [
          ["الخصوصية", "/legal/privacy/"],
          ["شروط الاستخدام", "/legal/terms/"],
          ["حقوق النشر", "/legal/copyright/"],
        ],
      ],
      [
        "روابط أخرى",
        [
          ["موقع المطوّرين maktaba.dev", `${DEVPORTAL}ar/`],
          ["الشيفرة المصدرية على GitHub", GITHUB],
          ["الأمان", "/.well-known/security.txt"],
        ],
      ],
    ],
    footerNav: "روابط الموقع",
    copyright: "© 2026 Hamza Labs",
    readIn: "Read this page in English",
    hint: null,
  },
};

/** Part B's shell words (design/README.md, part B; design/mockups/shell.mjs's SHELL_B): the
 * Phase 2 nav, the home link's name and the footer's groups. A link to a page the site does not
 * build yet (`/download/` before story 60.11, `/help/` before 60.12) gives way to what part A
 * links instead (bLinks). */
export const T_B = {
  en: {
    help: "Help",
    homeName: "Maktaba, home",
    footerAbout:
      "Maktaba keeps a household’s films and series on its own computer, and plays them on every screen at home, in Arabic and English. Free and open source, under the MIT licence.",
    groups: (l) => [
      [
        "Get Maktaba",
        [
          ...(l.download === LATEST_RELEASE ? [] : [["Download", l.download]]),
          ["Install guides", "/install/"],
          ["Every file on GitHub", LATEST_RELEASE],
        ],
      ],
      [
        "Help",
        [
          l.help === TROUBLESHOOTING ? ["Troubleshooting", l.help] : ["Help topics", l.help],
          ["Messages and what they mean", "/problems/"],
        ],
      ],
      [
        "Legal",
        [
          ["Privacy", "/legal/privacy/"],
          ["Terms", "/legal/terms/"],
          ["Copyright", "/legal/copyright/"],
        ],
      ],
      [
        "Elsewhere",
        [
          ["maktaba.dev, for developers", DEVPORTAL],
          ["Source on GitHub", GITHUB],
          ["Security", "/.well-known/security.txt"],
        ],
      ],
    ],
  },
  ar: {
    help: "المساعدة",
    homeName: "مكتبة، الصفحة الرئيسية",
    footerAbout:
      "يحفظ برنامج مكتبة أفلام البيت ومسلسلاته على حاسوبه الخاص، ويعرضها على كل شاشة فيه، بالعربية والإنجليزية. وهو برنامج حرّ مفتوح المصدر برخصة MIT.",
    groups: (l) => [
      [
        "الحصول على مكتبة",
        [
          ...(l.download === LATEST_RELEASE ? [] : [["التنزيل", l.download]]),
          ["أدلة التثبيت", "/install/"],
          ["كل الملفات على GitHub", LATEST_RELEASE],
        ],
      ],
      [
        "المساعدة",
        [
          l.help === TROUBLESHOOTING ? ["حل المشكلات", l.help] : ["مواضيع المساعدة", l.help],
          ["الرسائل ومعانيها", "/problems/"],
        ],
      ],
      [
        "الشؤون القانونية",
        [
          ["الخصوصية", "/legal/privacy/"],
          ["شروط الاستخدام", "/legal/terms/"],
          ["حقوق النشر", "/legal/copyright/"],
        ],
      ],
      [
        "روابط أخرى",
        [
          ["موقع المطوّرين maktaba.dev", `${DEVPORTAL}ar/`],
          ["الشيفرة المصدرية على GitHub", GITHUB],
          ["الأمان", "/.well-known/security.txt"],
        ],
      ],
    ],
  },
};

/** Where part B's Download and Help go: their pages once built (`resolves`, build.mjs's
 * resolver), else the latest release and the troubleshooting guide. */
export function bLinks(resolves = () => false) {
  return {
    download: resolves("/download/") ? "/download/" : LATEST_RELEASE,
    help: resolves(HELP) ? HELP : TROUBLESHOOTING,
  };
}

// ---- the wordmark (part B, design/README.md "Wordmark") ----------------------------------------
// The app icon (static/icon.svg) defined once per page as a symbol and drawn with <use>, beside
// the name in the display face: the page's script in the header, both in the footer. The icon
// never mirrors.

const ICON_SVG = fs.readFileSync(path.join(PKG, "static/icon.svg"), "utf8");
const iconInner = ICON_SVG.replace(/^[\s\S]*?<defs>/, "<defs>").replace(/<\/svg>\s*$/, "");
const iconDefs = iconInner.match(/<defs>[\s\S]*?<\/defs>/)[0];
const iconBody = iconInner.replace(iconDefs, "");

/** The icon's symbol, once per part B page. */
export const SPRITE = `<svg class="sprite" width="0" height="0" aria-hidden="true" focusable="false">${iconDefs}<symbol id="mk-icon" viewBox="0 0 512 512">${iconBody}</symbol></svg>`;

/** The lockup: the icon, then the name in the page's script, or in both (`both`). */
export function wordmark(lang, { size = 32, both = false } = {}) {
  const names = both
    ? lang === "ar"
      ? `<span class="wm-name" lang="ar">مكتبة</span><span class="wm-rule"></span><span class="wm-name" lang="en" dir="ltr">Maktaba</span>`
      : `<span class="wm-name" lang="en">Maktaba</span><span class="wm-rule"></span><span class="wm-name" lang="ar" dir="rtl">مكتبة</span>`
    : `<span class="wm-name">${lang === "ar" ? "مكتبة" : "Maktaba"}</span>`;
  // the size is a class (src/part-b.css): the site's CSP refuses a style attribute
  return `<span class="wordmark wm-${size}"><svg class="mk-icon" data-nomirror width="${size}" height="${size}" viewBox="0 0 512 512" aria-hidden="true" focusable="false"><use href="#mk-icon"/></svg>${names}</span>`;
}

const external = (href) => /^https?:/.test(href);
const local = (lang, href) =>
  external(href) || href.startsWith("/.well-known/") ? href : inLang(lang, href);

/** The page's twin in the other language. */
export function twinOf(lang, p) {
  if (lang === "ar") return p.replace(/^\/ar(?=\/)/, "") || "/";
  return `/ar${p}`;
}

/** The nav item a page belongs to (aria-current="page"), from its own path. */
export function currentOf(sitePath) {
  if (!sitePath) return null;
  const en = sitePath.replace(/^\/ar(?=\/)/, "");
  if (en.startsWith("/install/")) return "install";
  if (en.startsWith("/problems/")) return "messages";
  if (en === "/download/") return "download";
  if (en.startsWith("/help/")) return "help";
  return null;
}

/** Where the language switch goes: the twin, or (a page with no address of its own, the 404)
 * the other language's front door. */
const switchHref = (lang, sitePath) =>
  sitePath === null ? inLang(T[lang].other.lang, "/") : twinOf(lang, sitePath);

/** The skip link, the header with the nav, the language switch and the Menu button. */
export function header(lang, sitePath, { skipTo = "main", variant = "a", links = bLinks() } = {}) {
  if (variant === "b") return headerB(lang, sitePath, links);
  const t = T[lang];
  const o = t.other;
  const current = currentOf(sitePath);
  const item = (key, href) => {
    const ext = external(href);
    return `<li><a href="${local(lang, href)}"${current === key ? ' aria-current="page"' : ""}>${esc(t[key])}${ext ? ` ${ICONS.out}` : ""}</a></li>`;
  };
  const { mark } = shellAssets();
  return `<a class="skip" href="#${skipTo}">${esc(t.skip)}</a>
<header class="site-header">
  <div class="frame bar">
    <a class="mark" href="${inLang(lang, "/")}"><img src="${mark}" width="32" height="32" alt="" data-nomirror />${esc(t.name)}</a>
    <nav class="nav" id="site-nav" aria-label="${esc(t.nav)}">
      <ul>
        ${item("install", "/install/")}
        ${item("messages", "/problems/")}
        ${item("download", LATEST_RELEASE)}
      </ul>
    </nav>
    <a id="lang-switch" class="lang-switch" href="${switchHref(lang, sitePath)}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${ICONS.lang}<span>${esc(o.label)}</span></a>
    <button class="menu-button" type="button" aria-expanded="false" aria-controls="site-nav">${ICONS.menu}${esc(t.menu)}</button>
  </div>
</header>${hint(lang, sitePath)}`;
}

/** Part B's header (design/README.md, part B): the single-script lockup, then the Phase 2 nav
 * (Download, Install, Help, Messages), the language switch and the Menu button. */
function headerB(lang, sitePath, links) {
  const t = { ...T[lang], ...T_B[lang] };
  const o = t.other;
  const current = currentOf(sitePath);
  const item = (key, href) => {
    const ext = external(href);
    return `<li><a href="${local(lang, href)}"${current === key ? ' aria-current="page"' : ""}>${esc(t[key])}${ext ? ` ${ICONS.out}` : ""}</a></li>`;
  };
  return `<a class="skip" href="#main">${esc(t.skip)}</a>
${SPRITE}
<header class="site-header">
  <div class="frame bar">
    <a class="mark" href="${inLang(lang, "/")}" aria-label="${esc(t.homeName)}">${wordmark(lang, { size: 32 })}</a>
    <nav class="nav" id="site-nav" aria-label="${esc(t.nav)}">
      <ul>
        ${item("download", links.download)}
        ${item("install", "/install/")}
        ${item("help", links.help)}
        ${item("messages", "/problems/")}
      </ul>
    </nav>
    <a id="lang-switch" class="lang-switch" href="${switchHref(lang, sitePath)}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${ICONS.lang}<span>${esc(o.label)}</span></a>
    <button class="menu-button" type="button" aria-expanded="false" aria-controls="site-nav">${ICONS.menu}${esc(t.menu)}</button>
  </div>
</header>${hint(lang, sitePath)}`;
}

/** The language hint (§6.5): on an English page, the line offering the Arabic one, written in
 * Arabic; drawn only when src/shell-head.js finds a browser that prefers Arabic. */
export function hint(lang, sitePath) {
  const h = T[lang].hint;
  if (!h) return "";
  const o = T[lang].other;
  return `
<div class="lang-hint" lang="${o.lang}" dir="${o.dir}">
  <div class="frame"><p>${esc(h.text)} <a href="${switchHref(lang, sitePath)}" hreflang="${o.lang}">${esc(h.link)}</a></p><button class="button quiet" type="button">${esc(h.hide)}</button></div>
</div>`;
}

/** The footer: the mark and what Maktaba is, four groups of links, the copyright line and the
 * language link as a sentence; then the page's polite live region (the copy buttons' "Copied"). */
export function footer(lang, sitePath, { variant = "a", links = bLinks() } = {}) {
  const b = variant === "b";
  const t = b ? { ...T[lang], ...T_B[lang], groups: T_B[lang].groups(links) } : T[lang];
  const o = t.other;
  const { mark } = shellAssets();
  const groups = t.groups
    .map(
      ([title, links]) => `<div>
      <h2>${esc(title)}</h2>
      <ul>${links
        .map(
          ([text, href]) =>
            `<li><a href="${local(lang, href)}">${esc(text)}${external(href) ? ` ${ICONS.out}` : ""}</a></li>`
        )
        .join("")}</ul>
    </div>`
    )
    .join("\n    ");
  return `<footer class="site-footer">
  <nav class="frame cols" aria-label="${esc(t.footerNav)}">
    <div class="about">
      ${
        b
          ? `<a class="mark" href="${inLang(lang, "/")}" aria-label="${esc(t.homeName)}">${wordmark(lang, { size: 28, both: true })}</a>`
          : `<a class="mark" href="${inLang(lang, "/")}"><img src="${mark}" width="24" height="24" alt="" data-nomirror />${esc(t.name)}</a>`
      }
      <p>${esc(t.footerAbout)}</p>
    </div>
    ${groups}
  </nav>
  <div class="frame"><div class="base">
    <p><span dir="ltr">${esc(t.copyright)}</span></p>
    <a href="${switchHref(lang, sitePath)}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${esc(t.readIn)}</a>
  </div></div>
</footer>
<p class="visually-hidden" id="${LAST_ID}" aria-live="polite"></p>`;
}

// ---- the parts pages build with (design/README.md, "Components") -----------------------------

/** A callout: note, warning or tip, its kind said in a word as well as a colour. */
export function callout(kind, label, html) {
  if (!["note", "warning", "tip"].includes(kind)) throw new Error(`callout: no kind ${kind}`);
  return `<div class="callout" data-kind="${kind}" role="note">${ICONS[kind]}<p><span class="label">${esc(label)}</span> ${html}</p></div>`;
}

const COPY = {
  en: { copy: "Copy", done: "Copied" },
  ar: { copy: "نسخ", done: "تم النسخ" },
};

/** A code or compose block: its bar (what it is, and Copy), and the code, always left to right.
 * `copyName` is the button's accessible name ("Copy the command"), which holds its label. */
export function codeBlock(lang, { name, code, copyName }) {
  const t = COPY[lang];
  return `<div class="code">
  <div class="code-bar"><span class="name">${name}</span><button class="copy" type="button" aria-label="${esc(copyName)}" data-done="${esc(t.done)}">${ICONS.copy}<span class="copy-label">${esc(t.copy)}</span></button></div>
  <pre><code>${esc(code)}</code></pre>
</div>`;
}

/** The step list: each step a heading and its HTML, numbered in a brass ring. */
export function steps(items) {
  return `<ol class="steps">
${items.map((s) => `  <li><h3>${esc(s.title)}</h3>${s.html}</li>`).join("\n")}
</ol>`;
}

// ---- the document -----------------------------------------------------------------------------

/**
 * A whole document. `path` is the page's English path (`/`, `/install/`), or null for a page
 * with no address of its own (the 404 page): no canonical, alternates or card, and the switch
 * goes to the other language's front door. The head's links and social tags are head.mjs's.
 * `body` is the content of <main id="main">; `head`, tags a page adds after the shell's (part B's
 * stylesheet and display face, a script of its own, its structured data).
 */
export function page({
  lang,
  path: enPath,
  title,
  description,
  body,
  meta = "",
  mainClass = "",
  variant = "a",
  links = bLinks(),
  social = undefined,
  /** @type {string[]} */ headScripts = [],
  mainAttrs = "",
  head: extraHead = "",
}) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const here = enPath === null ? null : inLang(lang, enPath);
  const head = headHtml({ lang, path: enPath, title, description, social });
  const jsonLd = enPath === "/" ? `\n  ${organizationJsonLd(lang)}` : "";
  return `<!doctype html>
<html lang="${lang}" dir="${dir}">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <meta name="color-scheme" content="dark light" />
  <title>${esc(title)}</title>
  <meta name="description" content="${esc(description)}" />
  ${SITE_MARKER}
  ${menuMeta()}${meta ? `\n  ${meta}` : ""}
${head}
${shellHead(lang, "  ", { variant, headScripts })}${jsonLd}${extraHead ? `\n  ${extraHead}` : ""}
</head>
<body>
${header(lang, here, { variant, links })}
<main id="main"${mainClass ? ` class="${mainClass}"` : ""}${mainAttrs ? ` ${mainAttrs}` : ""}>
${body.trim()}
</main>
${footer(lang, here, { variant, links })}
</body>
</html>
`;
}

// ---- an install guide in the shell (design/README.md, "The install guide in the shell") -------

/**
 * An install guide (Epic 59's build, web/packages/install) in the shell: its own stylesheet
 * first, then the shell's; <body class="in-shell">; the shell's header, hint and footer around
 * the guide, whose own header, <main> and footer go in the frame (.guide-frame) as they are, so
 * its scripts find their elements. The guide's skip link gives way to the shell's, which goes to
 * the guide's <main>; its menu declaration becomes the shell's.
 */
export function inShell(html, { lang, path: enPath, file = enPath }) {
  const here = inLang(lang, enPath);
  const fail = (why) => {
    throw new Error(`${file}: the install guide cannot go in the shell: ${why}`);
  };
  const mainId = html.match(/<main\b[^>]*\bid="([^"]+)"/)?.[1] ?? fail("its <main> has no id");
  if ((html.match(/<main\b/g) ?? []).length !== 1) fail("not exactly one <main>");
  if (!/<meta name="maktaba-menu" content="none" \/>/.test(html))
    fail('no <meta name="maktaba-menu" content="none" /> to replace');
  const bodyAt = html.match(/<body>\n/) ?? fail("no plain <body>");
  const endAt = html.lastIndexOf("</body>");
  if (endAt < 0) fail("no </body>");
  const start = bodyAt.index + bodyAt[0].length;
  const inner = html
    .slice(start, endAt)
    .replace(/[ \t]*<a class="skip-link" href="#[^"]*">[^<]*<\/a>\n/, "");
  if (inner.includes('class="skip-link"')) fail("a skip link the shell does not know");
  const head = html
    .slice(0, bodyAt.index)
    .replace(/<meta name="maktaba-menu" content="none" \/>/, menuMeta())
    .replace(/\n\s*<\/head>\s*$/, `\n${shellHead(lang, "    ")}\n  </head>\n  `);
  if (!head.includes(shellAssets().css)) fail("no </head>");
  return `${head}<body class="in-shell">
${header(lang, here, { skipTo: mainId })}
<div class="frame guide-frame">
${inner.replace(/\s+$/, "")}
</div>
${footer(lang, here)}
</body>
</html>
`;
}
