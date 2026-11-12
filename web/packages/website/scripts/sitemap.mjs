// maktaba.tv's sitemap.xml and robots.txt (story 60.04): one <url> per page and language, each
// with its xhtml:link alternates (en, ar and x-default, the English page), and a robots.txt
// that allows everything and names the sitemap.

import { ORIGIN, esc, inLang } from "./head.mjs";

/** The sitemap of the pages, given their English paths (each has an Arabic twin). */
export function sitemapXml(paths) {
  const urls = [];
  for (const p of [...new Set(paths)].sort()) {
    const alternates = [
      `    <xhtml:link rel="alternate" hreflang="en" href="${esc(ORIGIN + p)}"/>`,
      `    <xhtml:link rel="alternate" hreflang="ar" href="${esc(ORIGIN + inLang("ar", p))}"/>`,
      `    <xhtml:link rel="alternate" hreflang="x-default" href="${esc(ORIGIN + p)}"/>`,
    ].join("\n");
    for (const lang of ["en", "ar"])
      urls.push(
        `  <url>\n    <loc>${esc(ORIGIN + inLang(lang, p))}</loc>\n${alternates}\n  </url>`
      );
  }
  return `<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9" xmlns:xhtml="http://www.w3.org/1999/xhtml">
${urls.join("\n")}
</urlset>
`;
}

export const ROBOTS = `User-agent: *\nAllow: /\n\nSitemap: ${ORIGIN}/sitemap.xml\n`;
