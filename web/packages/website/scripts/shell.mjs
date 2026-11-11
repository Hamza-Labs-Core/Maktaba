// The page shell of maktaba.tv: the document around each hand-made page (head, header with the
// language switch, main, footer). One function per part, so the design of story 60.01 part A
// replaces the parts (and static/assets/site.css) without touching the pages' copy.
//
// Every page carries <meta name="maktaba-site" content="maktaba-website">: the deploy's smoke
// (tools/cloudflare/smoke.mjs) looks for it to tell this Worker's answers from any other's.
//
// Every page also declares whether its shell has a phone menu, <meta name="maktaba-menu">
// (SHELL_MENU): "none" for this placeholder shell, "phone" once the design's shell has one. The
// keyboard tests (e2e/keyboard.spec.ts) open and close the menu on every page that declares
// one, skip the menu test (saying why) on a page that declares none, and fail a page that
// declares nothing or declares none while its header has a menu button.

import { esc, headHtml, inLang, organizationJsonLd } from "./head.mjs";

export { esc, inLang };
export const ORIGIN = "https://maktaba.tv";
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";
export const LATEST_RELEASE = `${GITHUB}/releases/latest`;
export const DEVPORTAL = "https://maktaba.dev/";
/** Help, until the help pages exist (60.12): the install guides' troubleshooting page. */
export const HELP = "/install/troubleshooting/";

export const SITE_MARKER = '<meta name="maktaba-site" content="maktaba-website" />';

/** This shell's phone menu: "none" (the placeholder header has no menu) or "phone". */
export const SHELL_MENU = "none";
export const menuMeta = (menu = SHELL_MENU) => `<meta name="maktaba-menu" content="${menu}" />`;

const T = {
  en: {
    name: "Maktaba",
    nav: "Site",
    install: "Install",
    download: "Download",
    help: "Help",
    developers: "Developers",
    switchLabel: "العربية",
    switchLang: "ar",
    skip: "Skip to the content",
    security: "Security",
    source: "Source code",
  },
  ar: {
    name: "مكتبة",
    nav: "الموقع",
    install: "التثبيت",
    download: "التنزيل",
    help: "المساعدة",
    developers: "للمطوّرين",
    switchLabel: "English",
    switchLang: "en",
    skip: "انتقلوا إلى المحتوى",
    security: "الأمان",
    source: "الشيفرة المصدرية",
  },
};

/** The page's twin in the other language. */
export function twinOf(lang, p) {
  if (lang === "ar") return p.replace(/^\/ar(?=\/)/, "") || "/";
  return `/ar${p}`;
}

export function header(lang, sitePath) {
  const t = T[lang];
  // a page with no address of its own (the 404) switches to the other language's front door
  const switchTo = sitePath === null ? inLang(t.switchLang, "/") : twinOf(lang, sitePath);
  return `<a class="skip-link" href="#main">${esc(t.skip)}</a>
<header class="site-header">
  <a class="wordmark" href="${inLang(lang, "/")}">${esc(t.name)}</a>
  <nav aria-label="${esc(t.nav)}">
    <a href="${inLang(lang, "/install/nas/")}">${esc(t.install)}</a>
    <a href="${LATEST_RELEASE}">${esc(t.download)}</a>
    <a href="${inLang(lang, HELP)}">${esc(t.help)}</a>
    <a id="lang-switch" class="lang" href="${switchTo}" hreflang="${t.switchLang}" lang="${t.switchLang}">${esc(t.switchLabel)}</a>
  </nav>
</header>`;
}

export function footer(lang) {
  const t = T[lang];
  return `<footer class="site-footer">
  <a href="${lang === "ar" ? `${DEVPORTAL}ar/` : DEVPORTAL}">${esc(t.developers)}: maktaba.dev</a>
  <a href="${GITHUB}">${esc(t.source)}</a>
  <a href="/.well-known/security.txt">${esc(t.security)}</a>
</footer>`;
}

/**
 * A whole document. `path` is the page's English path (`/`, `/install/`), or null for a page
 * with no address of its own (the 404 page): no canonical, alternates or card, and the switch
 * goes to the other language's front door. The head's links and social tags are head.mjs's.
 */
export function page({ lang, path, title, description, body, meta = "" }) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const here = path === null ? null : inLang(lang, path);
  const head = headHtml({ lang, path, title, description });
  const jsonLd = path === "/" ? `\n  ${organizationJsonLd(lang)}` : "";
  return `<!doctype html>
<html lang="${lang}" dir="${dir}">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <meta name="color-scheme" content="light dark" />
  <title>${esc(title)}</title>
  <meta name="description" content="${esc(description)}" />
  ${SITE_MARKER}
  ${menuMeta()}${meta ? `\n  ${meta}` : ""}
${head}
  <link rel="stylesheet" href="/assets/site.css" />${jsonLd}
</head>
<body>
${header(lang, here)}
<main id="main">
${body.trim()}
</main>
${footer(lang)}
</body>
</html>
`;
}
