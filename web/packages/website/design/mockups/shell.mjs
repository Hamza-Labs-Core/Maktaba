// The shell's shared parts for the maktaba.tv mockups (story 60.01): the icons, the arch,
// the header, footer and language switch, the page wrapper, code blocks and callouts.
// make.mjs (part A's layouts) and part-b.mjs (part B's) build every page from these.
// `variant: "b"` is part B's shell: the Phase 2 nav (Download, Install, Help, Messages),
// the wordmark drawn as inline SVG with the name in the display face, the footer's
// bilingual lockup, part B's stylesheet (../site.css) and the display face preloaded.

import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const here = dirname(fileURLToPath(import.meta.url));
export const ICON = "../../../../public/icons/icon-192.png";
export const RELEASES = "https://github.com/Hamza-Labs-Core/Maktaba/releases/latest";
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";

// ------------------------------------------------------------------ icons
// One style: 24-unit grid, drawn at 20 px, 1.75 stroke, round caps and joins, currentColor.
// `dir-icon` marks the ones that mean a direction: they mirror on an Arabic page.

export const svg = (body, cls = "") =>
  `<svg${cls ? ` class="${cls}"` : ""} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;
export const I = {
  onward: svg('<path d="M5 12h14M13 6l6 6-6 6"/>', "dir-icon"),
  back: svg('<path d="M15 6l-6 6 6 6"/>', "dir-icon"),
  out: svg('<path d="M14 5h5v5M19 5l-8 8M18 14v4a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h4"/>', "dir-icon"),
  menu: svg('<path d="M4 7h16M4 12h16M4 17h16"/>'),
  lang: svg('<circle cx="12" cy="12" r="8.5"/><path d="M3.5 12h17M12 3.5c2.6 2.6 3.6 5.6 3.6 8.5s-1 5.9-3.6 8.5c-2.6-2.6-3.6-5.6-3.6-8.5s1-5.9 3.6-8.5z"/>'),
  copy: svg('<rect x="8.5" y="8.5" width="11" height="11" rx="2"/><path d="M15.5 8.5V6a1.5 1.5 0 0 0-1.5-1.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5"/>'),
  note: svg('<circle cx="12" cy="12" r="8.5"/><path d="M12 11v5M12 8h.01"/>'),
  warning: svg('<path d="M12 4l9 16H3z"/><path d="M12 10v4M12 17h.01"/>'),
  tip: svg('<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-3.5 10.9c.6.5 1 1.2 1 2.1h5c0-.9.4-1.6 1-2.1A6 6 0 0 0 12 3z"/>'),
  search: svg('<circle cx="11" cy="11" r="6.5"/><path d="M16 16l4 4"/>'),
  person: svg('<circle cx="12" cy="8" r="3.5"/><path d="M5 20c.8-3.6 3.6-5.5 7-5.5s6.2 1.9 7 5.5"/>'),
};

// The arch of the app's icon, drawn as two brass lines with the lamp's light inside: the
// front door's and the 404's one decoration. Symmetric, so it never mirrors; hidden from
// assistive technology and under 1024 px.
export const ARCH = `<svg class="arch" viewBox="0 0 360 460" aria-hidden="true" focusable="false">
  <defs><radialGradient id="lamp" cx="180" cy="372" r="190" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="#E3B252" stop-opacity="0.34"/><stop offset="0.55" stop-color="#E3B252" stop-opacity="0.08"/><stop offset="1" stop-color="#E3B252" stop-opacity="0"/></radialGradient></defs>
  <path d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460Z" fill="url(#lamp)"/>
  <path class="line" d="M24 460V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V460"/>
  <path class="line soft" d="M42 460V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V460"/>
  <path class="line" d="M112 392C140 378 166 378 180 390C194 378 220 378 248 392M180 390V366M112 392V404C140 392 166 392 180 404C194 392 220 392 248 404V392"/>
</svg>`;

// ------------------------------------------------------------------ the wordmark (part B)
// The app icon as a vector (../../static/icon.svg, traced from web/public/icons/icon-512.png),
// defined once per page as a symbol and drawn with <use>. The lockup is the icon and the
// name in the display face: the page's own script in the header, both scripts in the
// footer and on the social card (Q3). The icon never mirrors.

const ICON_SVG = readFileSync(join(here, "..", "..", "static", "icon.svg"), "utf8");
const iconInner = ICON_SVG.replace(/^[\s\S]*?<defs>/, "<defs>").replace(/<\/svg>\s*$/, "");
const iconDefs = iconInner.match(/<defs>[\s\S]*?<\/defs>/)[0];
const iconBody = iconInner.replace(iconDefs, "");

export const SPRITE = `<svg class="sprite" width="0" height="0" aria-hidden="true" focusable="false">${iconDefs}<symbol id="mk-icon" viewBox="0 0 512 512">${iconBody}</symbol></svg>`;

export const icon = (size, cls = "mk-icon") =>
  `<svg class="${cls}" width="${size}" height="${size}" viewBox="0 0 512 512" aria-hidden="true" focusable="false"><use href="#mk-icon"/></svg>`;

/** The lockup: the icon, then the name in the page's script, or in both (`both`). */
export function wordmark(lang, { size = 32, both = false, cls = "wordmark" } = {}) {
  const names = both
    ? lang === "ar"
      ? `<span class="wm-name" lang="ar">مكتبة</span><span class="wm-rule"></span><span class="wm-name" lang="en" dir="ltr">Maktaba</span>`
      : `<span class="wm-name" lang="en">Maktaba</span><span class="wm-rule"></span><span class="wm-name" lang="ar" dir="rtl">مكتبة</span>`
    : `<span class="wm-name">${lang === "ar" ? "مكتبة" : "Maktaba"}</span>`;
  return `<span class="${cls}" style="--wm: ${size}px">${icon(size)}${names}</span>`;
}

// ------------------------------------------------------------------ the shell's words

export const SHELL = {
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
      ["Get Maktaba", [["Install guides", "/install/"], ["Downloads on GitHub", RELEASES, true]]],
      ["Help", [["Help topics", "/help/"], ["Messages and what they mean", "/problems/"]]],
      ["Legal", [["Privacy", "/legal/privacy/"], ["Terms", "/legal/terms/"], ["Copyright", "/legal/copyright/"]]],
      ["Elsewhere", [["maktaba.dev, for developers", "https://maktaba.dev/", true], ["Source on GitHub", GITHUB, true]]],
    ],
    footerNav: "Site",
    copyright: "© 2026 Hamza Labs",
    readIn: "اقرأ هذه الصفحة بالعربية",
    copied: "Copied",
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
      ["الحصول على مكتبة", [["أدلة التثبيت", "/install/"], ["التنزيلات على GitHub", RELEASES, true]]],
      ["المساعدة", [["مواضيع المساعدة", "/help/"], ["الرسائل ومعانيها", "/problems/"]]],
      ["الشؤون القانونية", [["الخصوصية", "/legal/privacy/"], ["شروط الاستخدام", "/legal/terms/"], ["حقوق النشر", "/legal/copyright/"]]],
      ["روابط أخرى", [["موقع المطوّرين maktaba.dev", "https://maktaba.dev/", true], ["الشيفرة المصدرية على GitHub", GITHUB, true]]],
    ],
    footerNav: "روابط الموقع",
    copyright: "© 2026 Hamza Labs",
    readIn: "Read this page in English",
    copied: "تم النسخ",
  },
};

/** Part B's nav and footer groups (the Phase 2 shell). */
export const SHELL_B = {
  en: {
    help: "Help",
    nav: [
      ["download", "/download/"],
      ["install", "/install/"],
      ["help", "/help/"],
      ["messages", "/problems/"],
    ],
    homeName: "Maktaba, home",
    footerAbout:
      "Maktaba keeps a household’s films and series on its own computer, and plays them on every screen at home, in Arabic and English. Free and open source, under the MIT licence.",
    groups: [
      ["Get Maktaba", [["Download", "/download/"], ["Install guides", "/install/"], ["Every file on GitHub", RELEASES, true]]],
      ["Help", [["Help topics", "/help/"], ["Messages and what they mean", "/problems/"]]],
      ["Legal", [["Privacy", "/legal/privacy/"], ["Terms", "/legal/terms/"], ["Copyright", "/legal/copyright/"]]],
      ["Elsewhere", [["maktaba.dev, for developers", "https://maktaba.dev/", true], ["Source on GitHub", GITHUB, true]]],
    ],
  },
  ar: {
    help: "المساعدة",
    nav: [
      ["download", "/download/"],
      ["install", "/install/"],
      ["help", "/help/"],
      ["messages", "/problems/"],
    ],
    homeName: "مكتبة، الصفحة الرئيسية",
    footerAbout:
      "يحفظ برنامج مكتبة أفلام البيت ومسلسلاته على حاسوبه الخاص، ويعرضها على كل شاشة فيه، بالعربية والإنجليزية. وهو برنامج حرّ مفتوح المصدر برخصة MIT.",
    groups: [
      ["الحصول على مكتبة", [["التنزيل", "/download/"], ["أدلة التثبيت", "/install/"], ["كل الملفات على GitHub", RELEASES, true]]],
      ["المساعدة", [["مواضيع المساعدة", "/help/"], ["الرسائل ومعانيها", "/problems/"]]],
      ["الشؤون القانونية", [["الخصوصية", "/legal/privacy/"], ["شروط الاستخدام", "/legal/terms/"], ["حقوق النشر", "/legal/copyright/"]]],
      ["روابط أخرى", [["موقع المطوّرين maktaba.dev", "https://maktaba.dev/", true], ["الشيفرة المصدرية على GitHub", GITHUB, true]]],
    ],
  },
};

export const twin = (lang, path) => (lang === "en" ? `/ar${path}` : path);
export const local = (lang, path) => (lang === "en" || /^https?:/.test(path) ? path : `/ar${path}`);

export function header(lang, current, variant = "a", key = current) {
  if (variant === "b") return headerB(lang, current, key);
  const s = SHELL[lang];
  const item = (key, href, ext) =>
    `<li><a href="${ext ? href : local(lang, href)}"${current === key ? ' aria-current="page"' : ""}>${s[key]}${ext ? ` ${I.out}` : ""}</a></li>`;
  return `<a class="skip" href="#main">${s.skip}</a>
<header class="site-header">
  <div class="frame bar">
    <a class="mark" href="${local(lang, "/")}"><img src="${ICON}" width="32" height="32" alt="" />${s.name}</a>
    <nav class="nav" id="site-nav" aria-label="${s.nav}">
      <ul>
        ${item("install", "/install/")}
        ${item("messages", "/problems/")}
        ${item("download", RELEASES, true)}
      </ul>
    </nav>
    ${langSwitch(lang, current)}
    <button class="menu-button" type="button" aria-expanded="false" aria-controls="site-nav">${I.menu}${s.menu}</button>
  </div>
</header>`;
}

function headerB(lang, current, key) {
  const s = { ...SHELL[lang], ...SHELL_B[lang] };
  const items = s.nav
    .map(
      ([k, href]) =>
        `<li><a href="${local(lang, href)}"${current === k ? ' aria-current="page"' : ""}>${s[k]}</a></li>`
    )
    .join("\n        ");
  return `<a class="skip" href="#main">${s.skip}</a>
${SPRITE}
<header class="site-header">
  <div class="frame bar">
    <a class="mark" href="${local(lang, "/")}" aria-label="${s.homeName}">${wordmark(lang, { size: 32 })}</a>
    <nav class="nav" id="site-nav" aria-label="${SHELL[lang].nav}">
      <ul>
        ${items}
      </ul>
    </nav>
    ${langSwitch(lang, key)}
    <button class="menu-button" type="button" aria-expanded="false" aria-controls="site-nav">${I.menu}${s.menu}</button>
  </div>
</header>`;
}

export function langSwitch(lang, page) {
  const o = SHELL[lang].other;
  return `<a class="lang-switch" href="${twin(lang, PATHS[page] ?? "/")}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${I.lang}<span>${o.label}</span></a>`;
}

export function footer(lang, page, variant = "a") {
  const s = variant === "b" ? { ...SHELL[lang], ...SHELL_B[lang] } : SHELL[lang];
  const o = s.other;
  const groups = s.groups
    .map(
      ([title, links]) => `<div>
        <h2>${title}</h2>
        <ul>${links
          .map(([t, href, ext]) => `<li><a href="${ext ? href : local(lang, href)}">${t}${ext ? ` ${I.out}` : ""}</a></li>`)
          .join("")}</ul>
      </div>`
    )
    .join("\n      ");
  return `<footer class="site-footer">
  <nav class="frame cols" aria-label="${s.footerNav}">
      <div class="about">
        ${
          variant === "b"
            ? `<a class="mark" href="${local(lang, "/")}" aria-label="${s.homeName}">${wordmark(lang, { size: 28, both: true })}</a>`
            : `<a class="mark" href="${local(lang, "/")}"><img src="${ICON}" width="24" height="24" alt="" />${s.name}</a>`
        }
        <p>${s.footerAbout}</p>
      </div>
      ${groups}
  </nav>
  <div class="frame"><div class="base">
    <p><span dir="ltr">${s.copyright}</span></p>
    <a href="${twin(lang, PATHS[page] ?? "/")}" hreflang="${o.lang}" lang="${o.lang}" dir="${o.dir}">${s.readIn}</a>
  </div></div>
</footer>
<p class="visually-hidden" id="announce" aria-live="polite"></p>`;
}

// A 404 answers the address that was asked for, which is no page: it carries no canonical or
// hreflang alternates (they would name a dead address, and the link check rightly refuses one),
// and asks not to be indexed.
// Part B's pages that maktaba.tv does not serve yet (60.10, 60.11, 60.12 build them).
const UNBUILT = ["download", "help", "help-topic"];

const NOT_FOUND_HEAD = '<meta name="robots" content="noindex" />';

export const PATHS = {
  front: "/",
  legal: "/legal/privacy/",
  problem: "/problems/home-unreachable/",
  problems: "/problems/",
  install: "/install/unraid/",
  lost: "/instal/synology/",
  components: "/",
  home: "/",
  download: "/download/",
  "install-p3": "/install/synology/",
  help: "/help/",
  "help-topic": "/help/search/",
  wordmark: "/",
  foundations: "/",
  "components-b": "/",
};

export function page({
  lang,
  key,
  title,
  description,
  body,
  current,
  bodyClass = "",
  extraCss = [],
  menuOpen = false,
  variant = "a",
  social,
}) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const path = PATHS[key];
  // A page the site does not have yet names itself root-relative, so the head names no dead
  // maktaba.tv address (the link check refuses one); its build makes the address absolute.
  const origin = UNBUILT.includes(key) ? "" : "https://maktaba.tv";
  const en = `${origin}${path}`;
  const ar = `${origin}/ar${path}`;
  const b = variant === "b";
  const textFonts = (lang === "ar" ? ["400-arabic", "600-arabic", "400-latin"] : ["400-latin", "600-latin"]).map(
    (f) => `maktaba-text-${f}`
  );
  const displayFonts = b ? (lang === "ar" ? ["arabic", "latin"] : ["latin"]).map((f) => `maktaba-display-700-${f}`) : [];
  const fonts = [...textFonts, ...displayFonts]
    .map((f) => `<link rel="preload" href="../../static/fonts/${f}.woff2" as="font" type="font/woff2" crossorigin />`)
    .join("\n    ");
  const css = [...extraCss, "../site-tokens.css", "../fonts.css", "../shell.css", ...(b ? ["../site.css"] : []), "mockup.css"]
    .map((h) => `<link rel="stylesheet" href="${h}" />`)
    .join("\n    ");
  return `<!doctype html>
${
    b
      ? "<!-- Generated by design/mockups/make.mjs (story 60.01 part B). Edit part-b.mjs or shell.mjs, not this file. -->"
      : "<!-- Generated by design/mockups/make.mjs (story 60.01 part A). Edit make.mjs, not this file. -->"
  }
<html lang="${lang}" dir="${dir}"${menuOpen ? ' data-menu="open"' : ""}>
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <meta name="color-scheme" content="dark light" />
    <title>${title}</title>
    <meta name="description" content="${description}" />
    ${key === "lost" ? NOT_FOUND_HEAD : `<link rel="canonical" href="${lang === "en" ? en : ar}" />
    <link rel="alternate" hreflang="en" href="${en}" />
    <link rel="alternate" hreflang="ar" href="${ar}" />
    <link rel="alternate" hreflang="x-default" href="${en}" />`}${
      social
        ? `
    <meta property="og:type" content="website" />
    <meta property="og:site_name" content="${lang === "ar" ? "مكتبة" : "Maktaba"}" />
    <meta property="og:title" content="${social}" />
    <meta property="og:description" content="${description}" />
    <meta property="og:locale" content="${lang === "ar" ? "ar_AR" : "en_US"}" />
    <meta property="og:image" content="png/social-${key === "home" ? "home-1" : key}.${lang}.1200.dark.png" />
    <meta property="og:image:width" content="1200" />
    <meta property="og:image:height" content="630" />
    <meta name="twitter:card" content="summary_large_image" />`
        : ""
    }
    ${fonts}
    ${css}
    <script>document.documentElement.classList.add("js");</script>
    <script src="../shell.js" defer></script>
  </head>
  <body${bodyClass ? ` class="${bodyClass}"` : ""}>
${header(lang, current, variant, key)}
${body}
${footer(lang, key, variant)}
  </body>
</html>
`;
}

// ------------------------------------------------------------------ shared parts

export function codeBlock(lang, { name, code, copyName, copied = false }) {
  const t = lang === "ar" ? { copy: "نسخ", done: "تم النسخ" } : { copy: "Copy", done: "Copied" };
  return `<div class="code">
  <div class="code-bar"><span class="name">${name}</span><button class="copy" type="button" aria-label="${copyName}" data-done="${t.done}"${copied ? " data-show-copied" : ""}>${I.copy}<span class="copy-label">${t.copy}</span></button></div>
  <pre><code>${code}</code></pre>
</div>`;
}

export function callout(kind, label, text) {
  return `<div class="callout" data-kind="${kind}" role="note">${I[kind]}<p><span class="label">${label}</span> ${text}</p></div>`;
}

