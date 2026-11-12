// Story 60.01 T01, T02 and the copy rules of 60.02 on the mockups, parts A and B:
//   - every mockup is current with make.mjs, and every PNG the render leaves is there,
//     under 1 MB, with no metadata chunk;
//   - the site's fonts are the text subsets and the display subsets, each licence beside
//     them, inside 150 KB per page per language;
//   - the mockups' English copy has none of the voice guide's "Not" words, and each Arabic
//     page (other than Epic 59's guide, checked by its own build) is under 20% Latin;
//   - each copy file has its other language with the same keys (60.02 T03), five taglines
//     each with its one-liner, and no app named that is not publicly installable (Q5).

import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { checkPngs, expectedPngs, shotsOf } from "../render.mjs";
import { LANGS, MOCKUPS } from "../mockups/make.mjs";
import { readCopy, TAGLINES } from "../mockups/part-b.mjs";
import { pngChunks } from "../png.mjs";

const design = join(dirname(fileURLToPath(import.meta.url)), "..");
const fonts = join(design, "..", "static", "fonts");
const copy = join(design, "..", "copy");

test("the mockups are current with make.mjs", () => {
  const r = spawnSync(process.execPath, [join(design, "mockups", "make.mjs"), "--check"], {
    encoding: "utf8",
  });
  assert.equal(r.status, 0, r.stderr);
});

test("the review sheet is current and shows every PNG", () => {
  const r = spawnSync(process.execPath, [join(design, "review-sheet.mjs"), "--check"], {
    encoding: "utf8",
  });
  assert.equal(r.status, 0, r.stderr);
  const html = readFileSync(join(design, "review-sheet.html"), "utf8");
  for (const f of expectedPngs()) assert.ok(html.includes(`mockups/png/${f}`), f);
});

test("one PNG per mockup, width, language and scheme; each under 1 MB; no metadata", async () => {
  const per = Object.keys(MOCKUPS).reduce((n, name) => n + shotsOf(name).length, 0);
  assert.equal(expectedPngs().length, per * LANGS.length);
  // part A: 9 mockups at three widths, three of them in light too; part B: 9 pages at three
  // widths, home and download in light at 1280, 4 tagline heroes and 6 social cards
  assert.equal(per, 9 * 3 + 3 * 3 + 9 * 3 + 2 + 4 + 6);
  assert.deepEqual(await checkPngs(), []);
});

test("pngChunks reads a PNG's chunks (self-test)", () => {
  const sig = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const chunk = (type, data = Buffer.alloc(0)) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    return Buffer.concat([len, Buffer.from(type, "latin1"), data, Buffer.alloc(4)]);
  };
  const png = Buffer.concat([
    sig,
    chunk("IHDR", Buffer.alloc(13)),
    chunk("tEXt", Buffer.from("a\0b")),
    chunk("IEND"),
  ]);
  assert.deepEqual(pngChunks(png), ["IHDR", "tEXt", "IEND"]);
});

test("the fonts: text and display, Latin and Arabic, the licences beside them, under the budget", () => {
  const files = readdirSync(fonts).sort();
  assert.deepEqual(files, [
    "OFL-Amiri.txt",
    "OFL.txt",
    "README.md",
    "maktaba-display-700-arabic.woff2",
    "maktaba-display-700-latin.woff2",
    "maktaba-text-400-arabic.woff2",
    "maktaba-text-400-latin.woff2",
    "maktaba-text-600-arabic.woff2",
    "maktaba-text-600-latin.woff2",
  ]);
  for (const f of ["OFL.txt", "OFL-Amiri.txt"])
    assert.match(readFileSync(join(fonts, f), "utf8"), /SIL Open Font License, Version 1\.1/, f);
  const size = (f) => statSync(join(fonts, f)).size;
  const en =
    size("maktaba-text-400-latin.woff2") +
    size("maktaba-text-600-latin.woff2") +
    size("maktaba-display-700-latin.woff2");
  const ar =
    en +
    size("maktaba-text-400-arabic.woff2") +
    size("maktaba-text-600-arabic.woff2") +
    size("maktaba-display-700-arabic.woff2");
  // Epic 60's budget: 150 KB per page per language, the display face included. An English
  // page loads the Latin files only (unicode-range); an Arabic page loads both scripts.
  assert.ok(en < 60 * 1024, `English page fonts ${en} bytes`);
  assert.ok(ar < 150 * 1024, `Arabic page fonts ${ar} bytes`);
  for (const f of files.filter((f) => f.endsWith(".woff2"))) {
    assert.equal(readFileSync(join(fonts, f)).subarray(0, 4).toString("latin1"), "wOF2", f);
  }
});

/** A page's visible prose: no head, script, style, svg, code, pre, bdi or the slug column. */
function prose(html) {
  return html
    .replace(/<head>[\s\S]*?<\/head>/, "")
    .replace(/<(script|style|svg|pre|code|bdi)\b[\s\S]*?<\/\1>/g, " ")
    .replace(/<span class="slug">[\s\S]*?<\/span>/g, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&[a-z]+;/g, " ");
}

// The voice guide's words we don't use (copy/voice.md), and its banned sentence habits.
const NOT = [
  /media assets?/i,
  /\bcontent\b/i,
  /\binstance\b/i,
  /server node/i,
  /\bdeployment\b/i,
  /client playback/i,
  /\bASR\b/,
  /transcription pipeline/i,
  /auto-update daemon/i,
  /\bsimply\b/i,
  /\bjust\b/i,
  /\beasy\b/i,
  /!/,
];

test("the English mockups use none of the words the voice guide refuses", () => {
  const found = [];
  for (const name of Object.keys(MOCKUPS)) {
    if (name === "install-guide") continue; // Epic 59's text, moved onto the voice in 60.05
    const text = prose(readFileSync(join(design, "mockups", `${name}.en.html`), "utf8"));
    for (const re of NOT) if (re.test(text)) found.push(`${name}.en: ${re}`);
  }
  assert.deepEqual(found, []);
});

// Names stay in Latin and count as code (Epic 60 §6.5).
const NAMES =
  /\b(Maktaba|maktaba\.(tv|dev)|GitHub|Synology|TrueNAS|Unraid|QNAP|UGREEN|Docker|compose|Mac|Windows|Linux|iPhone|iPad|Apple TV|Android TV|Android|Cloudflare|MIT|Hamza Labs|Admin|Updates|Apps|Install|Console|Container Path|Host Path|English|privacy@maktaba\.tv|docker-compose\.yml)\b/g;

export function latinShare(html) {
  const text = prose(html).replace(NAMES, " ");
  const latin = (text.match(/[A-Za-z]/g) ?? []).length;
  const arabic = (text.match(/[؀-ۿ]/g) ?? []).length;
  return latin / Math.max(1, latin + arabic);
}

test("each Arabic mockup is under 20% Latin", () => {
  const over = [];
  for (const name of Object.keys(MOCKUPS)) {
    if (name === "install-guide") continue;
    const share = latinShare(readFileSync(join(design, "mockups", `${name}.ar.html`), "utf8"));
    if (share >= 0.2) over.push(`${name}.ar: ${(share * 100).toFixed(1)}%`);
  }
  assert.deepEqual(over, []);
});

test("the Latin-share check catches an English page (self-test)", () => {
  assert.ok(latinShare("<body><p>This page is all in English words.</p></body>") > 0.9);
  assert.ok(latinShare("<body><p>هذه صفحة عربية فيها Maktaba.</p></body>") < 0.05);
});

test("each copy file has its other language, with the same keys (60.02 T03)", () => {
  const pages = readdirSync(copy)
    .filter((f) => f.endsWith(".en.md"))
    .map((f) => f.slice(0, -".en.md".length))
    .filter((p) => ["home", "download", "front-door"].includes(p));
  assert.deepEqual(pages.sort(), ["download", "front-door", "home"]);
  for (const p of pages) {
    const en = readCopy(p, "en");
    const ar = readCopy(p, "ar");
    assert.deepEqual(Object.keys(ar), Object.keys(en), p);
    for (const [k, v] of Object.entries(ar)) assert.ok(v.trim(), `${p}.ar: ${k} is empty`);
  }
});

test("five tagline proposals per language, each with its one-liner, each on its social card", () => {
  for (const lang of LANGS) {
    const c = readCopy("home", lang);
    for (const n of TAGLINES) {
      assert.ok(c[`tagline.${n}`], `${lang} tagline.${n}`);
      assert.ok(c[`tagline.${n}.says`], `${lang} tagline.${n}.says`);
      const card = readFileSync(join(design, "mockups", `social-home-${n}.${lang}.html`), "utf8");
      assert.ok(card.includes(c[`tagline.${n}`]), `social-home-${n}.${lang}`);
    }
  }
});

// Q5: only apps anyone can install today are named as available. The native phone and TV
// apps are not public yet, so no page or copy names them.
const UNRELEASED = /\b(iPhone|iPad|Apple TV|Android|App Store|Google Play|tvOS)\b/;

test("no mockup or copy names an app that is not publicly installable", () => {
  const found = [];
  const partB = Object.keys(MOCKUPS).filter(
    (n) =>
      ![
        "front-door",
        "legal",
        "problem",
        "problems",
        "install-guide",
        "components",
        "404",
      ].includes(n)
  );
  for (const name of partB) {
    if (name.startsWith("problem")) continue; // the app's messages, quoted as the app says them
    for (const lang of LANGS) {
      const text = prose(readFileSync(join(design, "mockups", `${name}.${lang}.html`), "utf8"));
      if (UNRELEASED.test(text)) found.push(`${name}.${lang}: ${text.match(UNRELEASED)[0]}`);
    }
  }
  for (const p of ["home", "download", "front-door"]) {
    for (const lang of LANGS) {
      for (const [k, v] of Object.entries(readCopy(p, lang)))
        if (UNRELEASED.test(v)) found.push(`copy/${p}.${lang}.md ${k}`);
    }
  }
  assert.deepEqual(found, []);
});

test("the help topic mockup uses the app's own text", () => {
  const md = readFileSync(
    join(design, "..", "..", "..", "src", "help", "content", "en", "search.md"),
    "utf8"
  );
  const first = md
    .split("\n")
    .find((l) => l.trim() && !l.startsWith("#") && !l.startsWith("---"))
    .trim()
    .slice(0, 40);
  const html = readFileSync(join(design, "mockups", "help-topic.en.html"), "utf8");
  assert.ok(prose(html).replace(/\s+/g, " ").includes(first.replace(/[`*]/g, "")), first);
});
