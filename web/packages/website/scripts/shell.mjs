// The page shell of maktaba.tv: the document around each hand-made page (head, header with the
// language switch, main, footer). One function per part, so the design of story 60.01 part A
// replaces the parts (and static/assets/site.css) without touching the pages' copy.
//
// Every page carries <meta name="maktaba-site" content="maktaba-website">: the deploy's smoke
// (tools/cloudflare/smoke.mjs) looks for it to tell this Worker's answers from any other's.

export const ORIGIN = "https://maktaba.tv";
export const GITHUB = "https://github.com/Hamza-Labs-Core/Maktaba";
export const LATEST_RELEASE = `${GITHUB}/releases/latest`;
export const DEVPORTAL = "https://maktaba.dev/";
/** Help, until the help pages exist (60.12): the install guides' troubleshooting page. */
export const HELP = "/install/troubleshooting/";

export const SITE_MARKER = '<meta name="maktaba-site" content="maktaba-website" />';

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
    security: "الأمان",
    source: "الشيفرة المصدرية",
  },
};

export const esc = (s) =>
  String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

/** A site path in a language: /x/ in English, /ar/x/ in Arabic. */
export const inLang = (lang, p) => (lang === "ar" ? `/ar${p}` : p);

/** The page's twin in the other language. */
export function twinOf(lang, p) {
  if (lang === "ar") return p.replace(/^\/ar(?=\/)/, "") || "/";
  return `/ar${p}`;
}

export function header(lang, sitePath) {
  const t = T[lang];
  // a page with no address of its own (the 404) switches to the other language's front door
  const switchTo = sitePath === null ? inLang(t.switchLang, "/") : twinOf(lang, sitePath);
  return `<header class="site-header">
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
 * with no address of its own (the 404 page): no alternates, and the switch goes to the other
 * language's front door.
 */
export function page({ lang, path, title, description, body, meta = "" }) {
  const dir = lang === "ar" ? "rtl" : "ltr";
  const here = path === null ? null : inLang(lang, path);
  const alternates =
    path === null
      ? ""
      : `
  <link rel="canonical" href="${ORIGIN}${here}" />
  <link rel="alternate" hreflang="en" href="${ORIGIN}${path}" />
  <link rel="alternate" hreflang="ar" href="${ORIGIN}${inLang("ar", path)}" />
  <link rel="alternate" hreflang="x-default" href="${ORIGIN}${path}" />`;
  return `<!doctype html>
<html lang="${lang}" dir="${dir}">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <meta name="color-scheme" content="light dark" />
  <title>${esc(title)}</title>
  <meta name="description" content="${esc(description)}" />
  ${SITE_MARKER}${meta ? `\n  ${meta}` : ""}${alternates}
  <link rel="stylesheet" href="/assets/site.css" />
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
