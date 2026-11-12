// What every page of maktaba.tv carries in its <head> (story 60.04): the canonical link, the
// hreflang alternates (en, ar, x-default → the English page), Open Graph and Twitter's card with
// the page's card image in its language (scripts/cards.mjs), and the icons and manifest
// (scripts/icons.mjs). The shell (scripts/shell.mjs) writes them into the hand-made pages; the
// build adds the ones an install guide's own <head> lacks (completeHead), so both read alike.

import { cardPath, CARD } from "./cards.mjs";

export const ORIGIN = "https://maktaba.tv";

const SITE_NAME = { en: "Maktaba", ar: "مكتبة" };
const LOCALE = { en: "en_US", ar: "ar_AR" };

export const esc = (s) =>
  String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");

/** A site path in a language: /x/ in English, /ar/x/ in Arabic. */
export const inLang = (lang, p) => (lang === "ar" ? `/ar${p}` : p);

/** The icons and the manifest, on every page (a 404 too). */
export const ICON_TAGS = [
  '<link rel="icon" href="/favicon.ico" sizes="32x32" />',
  '<link rel="icon" href="/icons/icon-192.png" type="image/png" sizes="192x192" />',
  '<link rel="apple-touch-icon" href="/apple-touch-icon.png" />',
  '<link rel="manifest" href="/manifest.webmanifest" />',
];

/**
 * The tags of a page, each with the pattern that finds it already in a <head>. `path` is the
 * page's English path (`/`, `/install/nas/`), or null for a page with no address of its own (the
 * 404 page): it gets the icons only. `social` is the title the card and the shared link show,
 * when it is not the page's <title> (the home page's is its tagline).
 */
export function headTags({ lang, path, title, description, social = title }) {
  const tags = ICON_TAGS.map((tag) => ({ find: tagKey(tag), tag }));
  if (path === null) return tags;
  const here = `${ORIGIN}${inLang(lang, path)}`;
  const other = lang === "ar" ? "en" : "ar";
  const card = `${ORIGIN}${cardPath(lang, path)}`;
  const meta = (attr, name, content) => `<meta ${attr}="${name}" content="${esc(content)}" />`;
  const list = [
    `<link rel="canonical" href="${here}" />`,
    `<link rel="alternate" hreflang="en" href="${ORIGIN}${path}" />`,
    `<link rel="alternate" hreflang="ar" href="${ORIGIN}${inLang("ar", path)}" />`,
    `<link rel="alternate" hreflang="x-default" href="${ORIGIN}${path}" />`,
    meta("property", "og:type", "website"),
    meta("property", "og:site_name", SITE_NAME[lang]),
    meta("property", "og:title", social),
    meta("property", "og:description", description),
    meta("property", "og:url", here),
    meta("property", "og:image", card),
    meta("property", "og:image:width", String(CARD.width)),
    meta("property", "og:image:height", String(CARD.height)),
    meta("property", "og:image:alt", social),
    meta("property", "og:locale", LOCALE[lang]),
    meta("property", "og:locale:alternate", LOCALE[other]),
    meta("name", "twitter:card", "summary_large_image"),
    meta("name", "twitter:title", social),
    meta("name", "twitter:description", description),
    meta("name", "twitter:image", card),
  ];
  return [...list.map((tag) => ({ find: tagKey(tag), tag })), ...tags];
}

/** What makes a tag the same tag: its rel (with hreflang and sizes), or its property/name. */
function tagKey(tag) {
  const attr = (name) => tag.match(new RegExp(`\\s${name}="([^"]*)"`))?.[1];
  if (tag.startsWith("<link")) {
    const parts = [`rel="${attr("rel")}"`];
    if (attr("hreflang")) parts.push(`hreflang="${attr("hreflang")}"`);
    if (attr("sizes")) parts.push(`sizes="${attr("sizes")}"`);
    return parts;
  }
  const name = attr("property") ? `property="${attr("property")}"` : `name="${attr("name")}"`;
  return [name];
}

/** Whether a <head> already has a tag (every part of its key on one <link> or <meta>). */
function has(head, key) {
  for (const m of head.matchAll(/<(?:link|meta)\b[^>]*>/g))
    if (key.every((k) => m[0].includes(k))) return true;
  return false;
}

/** The page's tags as lines of its <head>. */
export function headHtml(page, indent = "  ") {
  return headTags(page)
    .map((t) => `${indent}${t.tag}`)
    .join("\n");
}

/** A built page with the tags its <head> lacks added before </head>. */
export function completeHead(html, page) {
  const end = html.indexOf("</head>");
  if (end < 0) throw new Error(`${page.file || page.path}: no </head>`);
  const head = html.slice(0, end);
  const missing = headTags(page).filter((t) => !has(head, t.find));
  if (!missing.length) return html;
  return `${head}  ${missing.map((t) => t.tag).join("\n    ")}\n  ${html.slice(end)}`;
}

/** The title and description a built page declares. */
export function readHead(html, file = "page") {
  const title = html.match(/<title>([^<]*)<\/title>/)?.[1];
  const description = html.match(/<meta\s+name="description"\s+content="([^"]*)"/)?.[1];
  const lang = html.match(/<html[^>]*\slang="([^"]+)"/)?.[1];
  const og = html.match(/<meta\s+property="og:title"\s+content="([^"]*)"/)?.[1];
  if (!title) throw new Error(`${file}: no <title>`);
  if (!description) throw new Error(`${file}: no <meta name="description">`);
  const unesc = (s) =>
    s
      .replace(/&quot;/g, '"')
      .replace(/&lt;/g, "<")
      .replace(/&gt;/g, ">")
      .replace(/&#39;/g, "'")
      .replace(/&amp;/g, "&");
  return {
    title: unesc(title.trim()),
    description: unesc(description.trim()),
    lang,
    social: og ? unesc(og.trim()) : undefined,
  };
}

/** The Organization a home page names (JSON-LD). */
export function organizationJsonLd(lang) {
  const data = {
    "@context": "https://schema.org",
    "@type": "Organization",
    name: SITE_NAME[lang],
    alternateName: SITE_NAME[lang === "ar" ? "en" : "ar"],
    url: `${ORIGIN}${inLang(lang, "/")}`,
    logo: `${ORIGIN}/icons/icon-512.png`,
    sameAs: ["https://github.com/Hamza-Labs-Core/Maktaba", "https://maktaba.dev/"],
  };
  return `<script type="application/ld+json">${JSON.stringify(data).replace(/</g, "\\u003c")}</script>`;
}
