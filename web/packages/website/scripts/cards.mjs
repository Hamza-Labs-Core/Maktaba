// maktaba.tv's social cards (story 60.04): one 1200×630 PNG per page and language, rendered at
// build time from one template (dark, the page's title in its script, the wordmark), so a link
// shared to a chat or a feed shows the page it opens. Each card is under 200 KB, with no
// metadata (resvg writes none).
//
// The template is an SVG rendered by resvg (no browser). The face is IBM Plex Sans Arabic, the
// apps' face, which carries Arabic and Latin; @fontsource ships it as WOFF, which resvg cannot
// read, so woffToSfnt() unpacks each file (WOFF 1.0 is zlib per table) before it is handed over.
// The colours are the placeholder stylesheet's dark ones (before the part A shell). The home page's
// card is part B's (story 60.01 part B, `social-home` in design/mockups/part-b.mjs; cardSvgB), as
// are the download page's and the help pages' (60.12):
// the both-script wordmark at the top, the tagline in the display face (Amiri Bold, the copy the
// Apple app ships), the arch at the inline end and maktaba.tv with one line at the foot. Every
// page in part B's shell takes it (isPartBCard: the download page, and with Phase 3 the install
// chooser, the problem pages and the legal pages); the install guides keep the first template.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import zlib from "node:zlib";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

export const CARD = { width: 1200, height: 630, maxBytes: 200 * 1024 };

// the placeholder stylesheet's dark colours, until part B restyles the card
const COLOURS = { bg: "#141416", text: "#f2f2f4", muted: "#b4b4ba", accent: "#8ab4f8" };
const FAMILY = "IBM Plex Sans Arabic";
const NAME = { en: "Maktaba", ar: "مكتبة" };
const PAD = 80;

/** A WOFF 1.0 file as the TrueType/OpenType file it packs (the tables inflated, the directory
 * rebuilt). WOFF 2.0 is refused: it transforms tables, and @fontsource ships WOFF 1.0 too. */
export function woffToSfnt(woff) {
  const sig = woff.toString("latin1", 0, 4);
  if (sig !== "wOFF") throw new Error(`not a WOFF 1.0 file (signature ${JSON.stringify(sig)})`);
  const flavor = woff.readUInt32BE(4);
  const numTables = woff.readUInt16BE(12);
  const tables = [];
  for (let i = 0; i < numTables; i++) {
    const at = 44 + i * 20;
    const tag = woff.toString("latin1", at, at + 4);
    const offset = woff.readUInt32BE(at + 4);
    const compLength = woff.readUInt32BE(at + 8);
    const origLength = woff.readUInt32BE(at + 12);
    const checksum = woff.readUInt32BE(at + 16);
    const raw = woff.subarray(offset, offset + compLength);
    const data = compLength < origLength ? zlib.inflateSync(raw) : Buffer.from(raw);
    if (data.length !== origLength) throw new Error(`table ${tag}: ${data.length} ≠ ${origLength}`);
    tables.push({ tag, checksum, data });
  }
  // the sfnt header's search fields (OpenType spec, "Organization of an OpenType Font")
  let pow = 1;
  let log = 0;
  while (pow * 2 <= numTables) {
    pow *= 2;
    log++;
  }
  const head = Buffer.alloc(12 + 16 * numTables);
  head.writeUInt32BE(flavor, 0);
  head.writeUInt16BE(numTables, 4);
  head.writeUInt16BE(pow * 16, 6);
  head.writeUInt16BE(log, 8);
  head.writeUInt16BE(numTables * 16 - pow * 16, 10);
  const body = [];
  let offset = head.length;
  tables.forEach((t, i) => {
    const at = 12 + i * 16;
    head.write(t.tag, at, "latin1");
    head.writeUInt32BE(t.checksum, at + 4);
    head.writeUInt32BE(offset, at + 8);
    head.writeUInt32BE(t.data.length, at + 12);
    const padded = Buffer.alloc((t.data.length + 3) & ~3);
    t.data.copy(padded);
    body.push(padded);
    offset += padded.length;
  });
  return Buffer.concat([head, ...body]);
}

// resvg's fallback for a glyph the first face lacks takes the first face that has it, whatever
// its weight. So the Latin faces get a family of their own (LATIN, a same-length rename in the
// name table) listed first, and Arabic, which the cards only set bold, loads at 700 alone.
const LATIN = "IBM Plex Sans Latin1";
const FAMILIES_ATTR = `${LATIN}, ${FAMILY}`;

/** A font's family renamed in its name table (a same-length name: no offset moves). */
export function renameFamily(sfnt, from, to) {
  if (from.length !== to.length) throw new Error("renameFamily: the names differ in length");
  const out = Buffer.from(sfnt);
  for (const [a, b] of [
    [Buffer.from(from, "latin1"), Buffer.from(to, "latin1")],
    [Buffer.from(from, "utf16le").swap16(), Buffer.from(to, "utf16le").swap16()],
  ]) {
    let i;
    while ((i = out.indexOf(a)) >= 0) b.copy(out, i);
  }
  return out;
}

/** Part B's faces, the TrueType files the Apple app ships (resvg reads them as they are):
 * Amiri Bold for the title and the wordmark's names, Plex for the foot. */
const APPLE_FONTS = path.resolve(
  path.dirname(new URL(import.meta.url).pathname),
  "../../../../apps/apple/MaktabaKit/Sources/MaktabaKit/Resources/Fonts"
);
const DISPLAY = "Amiri";

let fontCache = null;
/** The card's font files, unpacked once into a temporary folder (resvg-js 2.6 reads files;
 * it ignores fontBuffers): Latin at 400 and 700, Arabic at 700. */
export function cardFonts() {
  if (fontCache) return fontCache;
  const dir = path.join(
    path.dirname(require.resolve("@fontsource/ibm-plex-sans-arabic/package.json")),
    "files"
  );
  const read = (subset, weight) =>
    woffToSfnt(
      fs.readFileSync(path.join(dir, `ibm-plex-sans-arabic-${subset}-${weight}-normal.woff`))
    );
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), "maktaba-cards-"));
  const faces = {
    "latin-400.ttf": renameFamily(read("latin", 400), FAMILY, LATIN),
    "latin-700.ttf": renameFamily(read("latin", 700), FAMILY, LATIN),
    "arabic-700.ttf": read("arabic", 700),
  };
  fontCache = Object.entries(faces).map(([name, bytes]) => {
    const p = path.join(tmp, name);
    fs.writeFileSync(p, bytes);
    return p;
  });
  fontCache.push(path.join(APPLE_FONTS, "Amiri-Bold.ttf"));
  return fontCache;
}

const esc = (s) => String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

async function resvg() {
  return (await import("@resvg/resvg-js")).Resvg;
}

function render(Resvg, svg) {
  return new Resvg(svg, {
    font: { fontFiles: cardFonts(), loadSystemFonts: false, defaultFontFamily: LATIN },
    fitTo: { mode: "original" },
  });
}

/** How wide a line of text draws, in px, at a size and weight (resvg measures it). */
function measure(Resvg, text, size, weight, family = FAMILIES_ATTR) {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="${size * 2}"><text x="0" y="${size * 1.5}" font-family="${family}" font-size="${size}" font-weight="${weight}" fill="#fff">${esc(text)}</text></svg>`;
  const box = render(Resvg, svg).innerBBox();
  return box ? box.width : 0;
}

/** The title's lines: words greedily, each line within `width`; at most `max` lines, the last
 * ending in an ellipsis when the title is longer. */
function wrap(Resvg, text, size, weight, width, max, family = FAMILIES_ATTR) {
  const words = text.split(/\s+/).filter(Boolean);
  const lines = [];
  let line = "";
  for (const w of words) {
    const next = line ? `${line} ${w}` : w;
    if (line && measure(Resvg, next, size, weight, family) > width) {
      lines.push(line);
      line = w;
    } else line = next;
  }
  if (line) lines.push(line);
  if (lines.length > max) {
    const kept = lines.slice(0, max);
    kept[max - 1] = `${kept[max - 1]}…`;
    return kept;
  }
  return lines;
}

/** The card's SVG: the wordmark at the top, the title, maktaba.tv at the foot. Arabic runs
 * right to left from the right edge. */
export async function cardSvg({ lang, title }) {
  const Resvg = await resvg();
  const rtl = lang === "ar";
  const x = rtl ? CARD.width - PAD : PAD;
  const anchor = rtl ? "end" : "start";
  const dir = rtl ? ' direction="rtl"' : "";
  // the page's title without the site name after it ("… · Maktaba")
  const shown = title.replace(/\s+·\s+(Maktaba|مكتبة)\s*$/, "");
  let size = 72;
  let lines = wrap(Resvg, shown, size, 700, CARD.width - 2 * PAD, 3);
  if (lines.length === 3) {
    size = 60;
    lines = wrap(Resvg, shown, size, 700, CARD.width - 2 * PAD, 4);
  }
  const lineHeight = Math.round(size * 1.3);
  const top = 300 - ((lines.length - 1) * lineHeight) / 2;
  const titleText = lines
    .map(
      (l, i) =>
        `<text x="${x}" y="${Math.round(top + i * lineHeight)}" font-size="${size}" font-weight="700" fill="${COLOURS.text}" text-anchor="${anchor}"${dir}>${esc(l)}</text>`
    )
    .join("\n  ");
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${CARD.width}" height="${CARD.height}" viewBox="0 0 ${CARD.width} ${CARD.height}" font-family="${FAMILIES_ATTR}">
  <rect width="100%" height="100%" fill="${COLOURS.bg}"/>
  <rect x="${rtl ? CARD.width - PAD - 96 : PAD}" y="${PAD}" width="96" height="8" rx="4" fill="${COLOURS.accent}"/>
  <text x="${x}" y="${PAD + 70}" font-size="44" font-weight="700" fill="${COLOURS.text}" text-anchor="${anchor}"${dir}>${esc(NAME[lang])}</text>
  ${titleText}
  <text x="${x}" y="${CARD.height - PAD}" font-size="32" font-weight="400" fill="${COLOURS.muted}" text-anchor="${anchor}">maktaba.tv</text>
</svg>`;
}

// ---- part B's card (the pages in part B's shell) -----------------------------------------

/** Part B's dark colours (design/site-tokens.css): the hero's surface, the brass, the text. */
const B = {
  bg: "#0E1624",
  glow: "#E3B252",
  brass: "#E3B252",
  text: "#E8ECF3",
  muted: "#A9B3C4",
};
const B_PAD = { x: 80, y: 64 };

/** The foot's line per page (design/mockups/part-b.mjs, SOCIAL_SUB). */
export const CARD_SUB = {
  "/": {
    en: "Free and open source, in Arabic and English",
    ar: "برنامج حرّ مفتوح المصدر، بالعربية والإنجليزية",
  },
  "/download/": {
    en: "Mac · Windows · Linux · NAS",
    ar: "Mac وWindows وLinux وخوادم التخزين",
  },
  "/install/": {
    en: "A NAS, Docker, or any Linux computer",
    ar: "خادم تخزين أو Docker أو أي حاسوب بنظام Linux",
  },
  // a family: every page under it (CARD_FAMILIES)
  "/problems/": {
    en: "Messages and what they mean",
    ar: "الرسائل ومعانيها",
  },
  "/legal/": {
    en: "Privacy, terms and copyright",
    ar: "الخصوصية وشروط الاستخدام وحقوق النشر",
  },
  // the help index and every topic (story 60.12)
  "/help/": {
    en: "Help, the same as in the app",
    ar: "المساعدة نفسها التي في التطبيق",
  },
};

/** The families whose every page takes its CARD_SUB line: the index and each problem page, each
 * legal page (Epic 60 Phase 3), each help page (60.12). Any other key is one page. */
export const CARD_FAMILIES = ["/problems/", "/legal/", "/help/"];

/** A page's foot line, or undefined when its card is part A's. */
export function cardSub(sitePath) {
  if (CARD_SUB[sitePath]) return CARD_SUB[sitePath];
  const family = CARD_FAMILIES.find((f) => sitePath.startsWith(f));
  return family ? CARD_SUB[family] : undefined;
}

/** Whether a page's card is part B's: the pages in part B's shell. */
export const isPartBCard = (sitePath) => cardSub(sitePath) !== undefined;

const ICON_SVG = fs.readFileSync(
  path.resolve(path.dirname(new URL(import.meta.url).pathname), "../static/icon.svg"),
  "utf8"
);

/** The app icon at (x, y), `size` px: static/icon.svg nested as it is. */
function iconAt(x, y, size) {
  return ICON_SVG.replace(/<\?xml[^>]*>/, "")
    .replace(/<title>[\s\S]*?<\/title>/, "")
    .replace(
      /<svg\b[^>]*>/,
      `<svg x="${x}" y="${y}" width="${size}" height="${size}" viewBox="0 0 512 512">`
    );
}

/** The lines a title breaks into at `size`, balanced: the narrowest width that keeps the
 * fewest lines (CSS's text-wrap: balance), at most `max`. */
function balance(Resvg, text, size, width, max) {
  const fewest = wrap(Resvg, text, size, 700, width, max, DISPLAY);
  let lo = width / 2;
  let hi = width;
  let best = fewest;
  for (let i = 0; i < 12; i++) {
    const mid = (lo + hi) / 2;
    const lines = wrap(Resvg, text, size, 700, mid, max, DISPLAY);
    if (lines.length === fewest.length && !lines.at(-1).endsWith("…")) {
      best = lines;
      hi = mid;
    } else lo = mid;
  }
  return best;
}

/** Part B's card (design/site.css, `.card-canvas`): the both-script wordmark (56 px icon), the
 * title in Amiri Bold at 68/80 (Arabic 72/108), at most three lines in 760 px, the arch at the
 * inline end with the lamp's glow, and maktaba.tv in brass with the page's line at the foot.
 * Arabic runs right to left from the right edge. */
export async function cardSvgB({ lang, title, path: sitePath }) {
  const Resvg = await resvg();
  const W = CARD.width;
  const H = CARD.height;
  const rtl = lang === "ar";
  const start = rtl ? W - B_PAD.x : B_PAD.x;
  const anchor = rtl ? "end" : "start";
  const dir = rtl ? ' direction="rtl"' : "";
  // inline-start to inline-end: x moves by +n in English, -n in Arabic
  const step = (x, n) => (rtl ? x - n : x + n);

  // the wordmark: icon, the page's name, a brass rule, the other name (0.34 × 56 apart)
  const icon = 56;
  const gap = Math.round(icon * 0.34);
  const nameSize = { en: Math.round(icon * 0.72), ar: Math.round(icon * 0.72 * 1.12) };
  const names = rtl
    ? [
        ["ar", "مكتبة"],
        ["en", "Maktaba"],
      ]
    : [
        ["en", "Maktaba"],
        ["ar", "مكتبة"],
      ];
  const mid = B_PAD.y + icon / 2;
  const parts = [iconAt(rtl ? start - icon : start, B_PAD.y, icon)];
  let x = step(start, icon + gap);
  names.forEach(([l, n], i) => {
    const size = nameSize[l];
    const w = measure(Resvg, n, size, 700, DISPLAY);
    const left = rtl ? x - w : x;
    parts.push(
      `<text x="${left.toFixed(1)}" y="${(mid + size * 0.34).toFixed(1)}" font-family="${DISPLAY}" font-size="${size}" font-weight="700" fill="${B.text}">${esc(n)}</text>`
    );
    x = step(x, w + gap);
    if (i === 0) {
      const h = Math.round(icon * 0.7);
      parts.push(
        `<rect x="${(rtl ? x : x - 1).toFixed(1)}" y="${mid - h / 2}" width="1" height="${h}" fill="${B.brass}" opacity="0.6"/>`
      );
      x = step(x, gap);
    }
  });

  // the title: centred between the wordmark and the foot
  const size = rtl ? 72 : 68;
  const lineHeight = rtl ? 108 : 80;
  const lines = balance(Resvg, title.replace(/\s+·\s+(Maktaba|مكتبة)\s*$/, ""), size, 760, 3);
  const top = B_PAD.y + icon;
  const footTop = H - B_PAD.y - 32;
  const blockTop = (top + footTop) / 2 - (lines.length * lineHeight) / 2;
  const baseline = (i) => blockTop + i * lineHeight + lineHeight / 2 + size * 0.32;
  const titleText = lines
    .map(
      (l, i) =>
        `<text x="${start}" y="${baseline(i).toFixed(1)}" font-family="${DISPLAY}" font-size="${size}" font-weight="700" fill="${B.text}" text-anchor="${anchor}"${dir}>${esc(l)}</text>`
    )
    .join("\n  ");

  // the foot: maktaba.tv (brass, 600), then the page's line (muted), 24 px apart
  const footBase = H - B_PAD.y - 9;
  const tvW = measure(Resvg, "maktaba.tv", 24, 700);
  const sub = cardSub(sitePath)?.[lang] ?? "";
  const foot = `<text x="${rtl ? start - tvW : start}" y="${footBase}" font-size="24" font-weight="700" fill="${B.brass}">maktaba.tv</text>
  <text x="${step(start, tvW + 24)}" y="${footBase}" font-size="24" font-weight="400" fill="${B.muted}" text-anchor="${anchor}"${dir}>${esc(sub)}</text>`;

  // the arch at the inline end (300 × 380, 72 px in), with the lamp's glow behind it
  const archX = rtl ? 72 : W - 72 - 300;
  const glowX = rtl ? W * 0.16 : W * 0.84;
  const arch = `<g transform="translate(${archX} ${H - 380 + 2}) scale(${300 / 360} ${380 / 400})" fill="none" stroke="${B.brass}" stroke-width="1.5" opacity="0.6">
    <path vector-effect="non-scaling-stroke" d="M24 400V214C24 150 76 118 128 92C158 77 176 52 180 22C184 52 202 77 232 92C284 118 336 150 336 214V400"/>
    <path vector-effect="non-scaling-stroke" opacity="0.45" d="M42 400V220C42 164 90 136 138 112C164 99 177 80 180 60C183 80 196 99 222 112C270 136 318 164 318 220V400"/>
  </g>`;

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${W}" height="${H}" viewBox="0 0 ${W} ${H}" font-family="${FAMILIES_ATTR}">
  <defs><radialGradient id="glow" cx="${glowX}" cy="${H * 0.7}" r="${W * 0.4}" gradientTransform="translate(${glowX} ${H * 0.7}) scale(1 ${(H * 0.7) / (W * 0.4)}) translate(${-glowX} ${-H * 0.7})" gradientUnits="userSpaceOnUse"><stop offset="0" stop-color="${B.glow}" stop-opacity="0.14"/><stop offset="0.7" stop-color="${B.glow}" stop-opacity="0"/></radialGradient></defs>
  <rect width="100%" height="100%" fill="${B.bg}"/>
  <rect width="100%" height="100%" fill="url(#glow)"/>
  ${arch}
  ${parts.join("\n  ")}
  ${titleText}
  ${foot}
</svg>`;
}

/** The card as PNG bytes: part B's for the pages in part B's shell (isPartBCard). */
export async function renderCard(page) {
  const Resvg = await resvg();
  const svg = isPartBCard(page.path) ? await cardSvgB(page) : await cardSvg(page);
  const png = render(Resvg, svg).render().asPng();
  if (png.length > CARD.maxBytes)
    throw new Error(`the card for ${page.title} is ${png.length} bytes, over ${CARD.maxBytes}`);
  return png;
}

/** Where a page's card lives: /cards/<path>.png in its language (/cards/index.png,
 * /cards/ar/install/nas.png). */
export function cardPath(lang, sitePath) {
  const slug = sitePath.replace(/^\/|\/$/g, "") || "index";
  return `/cards/${lang === "ar" ? "ar/" : ""}${slug}.png`;
}
