// maktaba.tv's social cards (story 60.04): one 1200×630 PNG per page and language, rendered at
// build time from one template (dark, the page's title in its script, the wordmark), so a link
// shared to a chat or a feed shows the page it opens. Each card is under 200 KB, with no
// metadata (resvg writes none).
//
// The template is an SVG rendered by resvg (no browser). The face is IBM Plex Sans Arabic, the
// apps' face, which carries Arabic and Latin; @fontsource ships it as WOFF, which resvg cannot
// read, so woffToSfnt() unpacks each file (WOFF 1.0 is zlib per table) before it is handed over.
// The colours are the placeholder stylesheet's dark ones (static/assets/site.css); the design of
// story 60.01 part B restyles this template.

import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import zlib from "node:zlib";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);

export const CARD = { width: 1200, height: 630, maxBytes: 200 * 1024 };

// site.css, dark
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
function measure(Resvg, text, size, weight) {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="4000" height="${size * 2}"><text x="0" y="${size * 1.5}" font-family="${FAMILIES_ATTR}" font-size="${size}" font-weight="${weight}" fill="#fff">${esc(text)}</text></svg>`;
  const box = render(Resvg, svg).innerBBox();
  return box ? box.width : 0;
}

/** The title's lines: words greedily, each line within `width`; at most `max` lines, the last
 * ending in an ellipsis when the title is longer. */
function wrap(Resvg, text, size, weight, width, max) {
  const words = text.split(/\s+/).filter(Boolean);
  const lines = [];
  let line = "";
  for (const w of words) {
    const next = line ? `${line} ${w}` : w;
    if (line && measure(Resvg, next, size, weight) > width) {
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

/** The card as PNG bytes. */
export async function renderCard(page) {
  const Resvg = await resvg();
  const png = render(Resvg, await cardSvg(page))
    .render()
    .asPng();
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
