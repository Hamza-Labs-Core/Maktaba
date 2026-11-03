// Which URLs the portal may put in an href or src: one allow-list, shared by the markdown
// renderer (build.mjs) and the built-site test (src/site.test.ts) so the two cannot drift.

export const ORIGIN = "https://maktaba.dev";
const SCHEMES = new Set(["http:", "https:", "mailto:"]);

// numeric character references and the named ones that spell a scheme (&colon; &Tab;),
// decoded as a browser would before it reads the attribute
const NAMED = { colon: ":", tab: "\t", newline: "\n", sol: "/", lpar: "(", rpar: ")", amp: "&" };
function decodeEntities(s) {
  return s
    .replace(/&#x([0-9a-f]+);?/gi, (_, h) => String.fromCodePoint(parseInt(h, 16)))
    .replace(/&#(\d+);?/g, (_, d) => String.fromCodePoint(parseInt(d, 10)))
    .replace(/&([a-z]+);/gi, (m, n) => NAMED[n.toLowerCase()] ?? m);
}

/**
 * True for an http(s) or mailto URL, a relative or in-site path, or a #fragment; false for
 * anything else (javascript:, data:, vbscript:, file:, in any case or spacing). The value is
 * read the way a browser reads it: entities decoded, then resolved by the URL parser (which
 * drops tabs, newlines and leading spaces).
 */
export function isSafeUrl(value) {
  if (typeof value !== "string") return false;
  let url;
  try {
    url = new URL(decodeEntities(value), `${ORIGIN}/`);
  } catch {
    return false;
  }
  // a relative path or #fragment resolves against the site itself: https:
  return SCHEMES.has(url.protocol);
}
