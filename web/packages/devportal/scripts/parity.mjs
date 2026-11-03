// Every page on maktaba.dev is in English and Arabic: /x/ and /ar/x/. These checks keep the
// two from drifting, and the build (scripts/build.mjs) fails on any finding:
//
//   - the sources: every guide has its Arabic twin (name.md and name.ar.md), and no Arabic
//     file is left without its English one; CHANGELOG.ar.md has the same releases, the same
//     sections and the same entries, in the same order, as CHANGELOG.md; the OpenAPI
//     overlay (shared/api/openapi.ar.json) translates every summary and description the
//     OpenAPI description has, against its current English; the wiki's Arabic database
//     (web/wiki-app/public/db/ar/) has every entry of the English one, translated from its
//     current English, and is Arabic;
//   - the output: every English page has its /ar/ page and the other way round, and an
//     Arabic page is Arabic: its prose outside code is mostly Arabic script.

import fs from "node:fs";
import path from "node:path";
import { createHash } from "node:crypto";

// ---- the share of Latin-script prose ----------------------------------------------------

// An Arabic page fails when more than this share of its prose letters (outside code, URLs
// and identifiers) are Latin. Product names (GitHub, PostgreSQL, Apple TV) stay in Latin, so
// it is not zero; a page left in English is near 1.
export const MAX_LATIN_SHARE = 0.2;

const ARABIC = /[؀-ۿݐ-ݿࢠ-ࣿﭐ-﷿ﹰ-﻿]/g;
const LATIN = /[A-Za-z]/g;

/** The Latin share of the letters in `text`: 0 when it has none. */
export function latinShare(text) {
  const latin = (text.match(LATIN) ?? []).length;
  const arabic = (text.match(ARABIC) ?? []).length;
  return latin + arabic === 0 ? 0 : latin / (latin + arabic);
}

/** The prose of markdown: code blocks, inline code, URLs and link targets dropped. */
export function markdownProse(md) {
  return md
    .replace(/^\s*```[\s\S]*?^\s*```/gm, " ")
    .replace(/`[^`\n]*`/g, " ")
    .replace(/\]\([^)]*\)/g, "]")
    .replace(/https?:\/\/\S+/g, " ")
    .replace(/<[^>]+>/g, " ");
}

/** The prose of a built page: scripts, styles, code, attributes and tags dropped. */
export function htmlProse(html) {
  return html
    .replace(/<(script|style|pre|code)\b[\s\S]*?<\/\1>/gi, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/&[a-z]+;|&#\d+;/gi, " ")
    .replace(/https?:\/\/\S+/g, " ")
    .replace(/\S*[/_.]\S*[A-Za-z]\S*/g, " ");
}

// ---- the sources ------------------------------------------------------------------------

/** The Arabic twin of a source file: guide.md -> guide.ar.md. */
export const arabicOf = (src) => src.replace(/\.md$/, ".ar.md");

/** Each guide has its Arabic twin, and each Arabic guide its English one. */
export function checkGuides(repo, dir, srcs) {
  const findings = [];
  for (const src of srcs) {
    if (!fs.existsSync(path.join(repo, arabicOf(src))))
      findings.push(`${src}: no Arabic version (${arabicOf(src)})`);
  }
  const named = new Set(srcs.map(arabicOf));
  const walk = (d) =>
    fs.readdirSync(path.join(repo, d), { withFileTypes: true }).flatMap((e) => {
      const p = `${d}/${e.name}`;
      return e.isDirectory() ? walk(p) : e.name.endsWith(".ar.md") ? [p] : [];
    });
  for (const ar of walk(dir)) {
    if (!named.has(ar)) findings.push(`${ar}: an Arabic guide with no English page to pair`);
  }
  return findings;
}

/** An Arabic markdown file is Arabic: its prose is mostly Arabic script. */
export function checkArabicMarkdown(file, md) {
  const share = latinShare(markdownProse(md));
  return share > MAX_LATIN_SHARE
    ? [`${file}: ${Math.round(share * 100)}% of its prose is Latin script (most is not translated)`]
    : [];
}

// The changelog's section names, English -> Arabic, and its unreleased heading.
export const CHANGELOG_SECTIONS = {
  Added: "أُضيف",
  Changed: "تغيّر",
  Deprecated: "أُهمل",
  Removed: "أُزيل",
  Fixed: "أُصلح",
  Security: "الأمان",
};
export const UNRELEASED = { en: "[Unreleased]", ar: "[لم يصدر بعد]" };

/** A changelog's outline: per release and section, how many entries (top-level bullets). */
export function changelogOutline(md, lang) {
  const out = [];
  let fence = false;
  for (const line of md.split("\n")) {
    if (/^\s*```/.test(line)) fence = !fence;
    if (fence) continue;
    const release = line.match(/^## (.+)$/);
    const section = line.match(/^### (.+)$/);
    if (release) {
      const r = release[1].trim();
      out.push({ release: r === UNRELEASED[lang] ? "Unreleased" : r, sections: [] });
    } else if (section && out.length) {
      let name = section[1].trim();
      if (lang === "ar") {
        const en = Object.keys(CHANGELOG_SECTIONS).find((k) => CHANGELOG_SECTIONS[k] === name);
        name = en ?? `?${name}`;
      }
      out.at(-1).sections.push({ name, entries: 0 });
    } else if (/^- /.test(line) && out.at(-1)?.sections.length) {
      out.at(-1).sections.at(-1).entries++;
    }
  }
  return out;
}

/** CHANGELOG.ar.md has every release, section and entry CHANGELOG.md has, in order. */
export function checkChangelog(en, ar) {
  const a = changelogOutline(en, "en");
  const b = changelogOutline(ar, "ar");
  const findings = [];
  const fmt = (r) => r.sections.map((s) => `${s.name}:${s.entries}`).join(", ");
  const n = Math.max(a.length, b.length);
  for (let i = 0; i < n; i++) {
    const x = a[i];
    const y = b[i];
    if (!x) findings.push(`CHANGELOG.ar.md: release ${y.release} is not in CHANGELOG.md`);
    else if (!y) findings.push(`CHANGELOG.ar.md: release ${x.release} has no Arabic entries`);
    else if (x.release !== y.release)
      findings.push(
        `CHANGELOG.ar.md: release ${i + 1} is ${y.release}, CHANGELOG.md has ${x.release}`
      );
    else if (fmt(x) !== fmt(y))
      findings.push(
        `CHANGELOG.ar.md: ${x.release} has entries [${fmt(y)}], CHANGELOG.md has [${fmt(x)}]: ` +
          "every entry is added in both languages"
      );
  }
  return findings;
}

// ---- the OpenAPI overlay ----------------------------------------------------------------

const pointerPart = (k) => String(k).replace(/~/g, "~0").replace(/\//g, "~1");

/** The OpenAPI description's prose: JSON pointer -> English (summaries, descriptions, the
 * title, the tags' names). */
export function openapiProse(doc) {
  const out = {};
  const walk = (x, p) => {
    if (Array.isArray(x)) x.forEach((v, i) => walk(v, `${p}/${i}`));
    else if (x && typeof x === "object") {
      for (const [k, v] of Object.entries(x)) {
        const q = `${p}/${pointerPart(k)}`;
        if ((k === "summary" || k === "description") && typeof v === "string" && v.trim())
          out[q] = v;
        else if (k === "title" && p === "/info" && typeof v === "string") out[q] = v;
        // a tag's name is an identifier operations refer to: the Arabic is its display name
        else if (k === "name" && /^\/tags\/\d+$/.test(p) && typeof v === "string") out[q] = v;
        else walk(v, q);
      }
    }
  };
  walk(doc, "");
  return out;
}

/** The overlay translates every string, against the current English, and nothing else. */
export function checkOverlay(doc, overlay) {
  const prose = openapiProse(doc);
  const strings = overlay.strings ?? {};
  const findings = [];
  const file = "shared/api/openapi.ar.json";
  for (const [ptr, en] of Object.entries(prose)) {
    const s = strings[ptr];
    if (!s) findings.push(`${file}: ${ptr} has no Arabic`);
    else if (s.en !== en) findings.push(`${file}: ${ptr}: the English changed; translate it again`);
    else if (!s.ar || !(s.ar.match(ARABIC) ?? []).length)
      findings.push(`${file}: ${ptr}: the Arabic is empty or not Arabic`);
  }
  for (const ptr of Object.keys(strings)) {
    if (!(ptr in prose)) findings.push(`${file}: ${ptr} is no longer in the OpenAPI description`);
  }
  const all = Object.values(strings)
    .map((s) => markdownProse(s.ar ?? ""))
    .join("\n");
  const share = latinShare(all);
  if (share > MAX_LATIN_SHARE)
    findings.push(`${file}: ${Math.round(share * 100)}% of its Arabic prose is Latin script`);
  return findings;
}

/** The OpenAPI description with its prose in Arabic. */
export function applyOverlay(doc, overlay) {
  const out = structuredClone(doc);
  for (const [ptr, s] of Object.entries(overlay.strings ?? {})) {
    const keys = ptr
      .split("/")
      .slice(1)
      .map((k) => k.replace(/~1/g, "/").replace(/~0/g, "~"));
    let node = out;
    for (const k of keys.slice(0, -1)) node = node?.[k];
    if (!node || typeof node !== "object" || !(keys.at(-1) in node)) continue;
    // a tag keeps its name (operations refer to it); Redoc shows x-displayName instead
    if (/^\/tags\/\d+\/name$/.test(ptr)) node["x-displayName"] = s.ar;
    else node[keys.at(-1)] = s.ar;
  }
  return out;
}

// ---- the wiki's database ----------------------------------------------------------------

// The wiki (/wiki/ and /ar/wiki/) reads web/wiki-app/public/db/*.json and its Arabic twin
// db/ar/*.json, which docs/wiki/db/generate_wiki.py writes from the translation store
// (docs/wiki/db/ar/, see docs/wiki/db/wiki_ar.py). The generator's --check holds the store to
// the specs; this check holds the database the site serves: every English entry has its
// Arabic, translated from its current English (the hash it records), and the Arabic is
// Arabic. The rules mirror wiki_ar.py.

/** The fields of an entry the wiki shows as text, per type (wiki_ar.FIELDS). */
export const WIKI_FIELDS = { epic: ["title", "content", "engine"], endpoint: ["content"], entity: ["content"] };
const WIKI_DEFAULT_FIELDS = ["title", "content"];

const FENCE = /^[ \t]*```[^\n]*\n[\s\S]*?^[ \t]*```/gm;
const CODE_SPAN = /`(?:[^`\n]|\n(?![ \t]*\n))+`/g;

/** A wiki text's prose (wiki_ar.prose): code, URLs, link targets, paths and identifiers dropped. */
export function wikiProse(text) {
  return text
    .replace(FENCE, " ")
    .replace(/^[ \t]*```[\s\S]*$(?![\s\S])/m, " ")
    .replace(CODE_SPAN, " ")
    .replace(/`[^`]*$(?![\s\S])/, " ")
    .replace(/\]\([^)]*\)/g, "]")
    .replace(/https?:\/\/\S+/g, " ")
    .replace(/<[^>]+>/g, " ")
    .replace(/\S*[/_.]\S*[A-Za-z]\S*/g, " ");
}

/** The hash a translation records of the English it translates (wiki_ar.text_hash). */
export const wikiHash = (text) => createHash("sha256").update(text, "utf8").digest("hex").slice(0, 16);

/** The translation store file an entry's Arabic is kept in (wiki_ar.store_file). */
export const wikiStoreFile = (e) =>
  `docs/wiki/db/ar/${["epic", "story", "plan"].includes(e.type) && e.epic ? `epic-${e.epic.slice(0, 2)}` : "common"}.json`;

/**
 * The served database against its Arabic: `en` maps a file name (wiki.json,
 * wiki-epics-01-09.json) to its parsed English, `ar` the same for db/ar/.
 */
export function checkWiki(en, ar, dir = "web/wiki-app/public/db") {
  const findings = [];
  const regen = "run python3 docs/wiki/db/generate_wiki.py";
  for (const name of Object.keys(en))
    if (!(name in ar)) findings.push(`${dir}/ar/${name}: missing (the Arabic of ${dir}/${name}): ${regen}`);
  for (const name of Object.keys(ar))
    if (!(name in en)) findings.push(`${dir}/ar/${name}: an Arabic file with no English one`);
  for (const [name, doc] of Object.entries(en)) {
    const arDoc = ar[name];
    if (!arDoc || !Array.isArray(doc.entries)) continue;
    if (arDoc.lang !== "ar") findings.push(`${dir}/ar/${name}: not marked "lang": "ar"`);
    const arById = new Map((arDoc.entries ?? []).map((e) => [e.id, e]));
    const prose = [];
    for (const e of doc.entries) {
      const a = arById.get(e.id);
      const store = wikiStoreFile(e);
      if (!a) {
        findings.push(`${dir}/ar/${name}: ${e.id} has no Arabic entry: ${regen}`);
        continue;
      }
      arById.delete(e.id);
      for (const field of WIKI_FIELDS[e.type] ?? WIKI_DEFAULT_FIELDS) {
        const text = e[field];
        if (typeof text !== "string" || !/[A-Za-z]/.test(wikiProse(text))) continue;
        const src = a.i18n?.source?.[field];
        if (!src)
          findings.push(
            `${dir}/ar/${name}: ${e.id} ${field} has no Arabic: translate it in ${store}, then ${regen}`
          );
        else if (src !== wikiHash(text))
          findings.push(
            `${dir}/ar/${name}: ${e.id} ${field}: the English changed since it was translated: ` +
              `translate it again in ${store}, then ${regen}`
          );
        else if (a[field] === text || !(String(a[field]).match(ARABIC) ?? []).length)
          findings.push(`${dir}/ar/${name}: ${e.id} ${field}: the Arabic is English or empty (${store})`);
        else prose.push(wikiProse(String(a[field])));
      }
    }
    for (const id of arById.keys()) findings.push(`${dir}/ar/${name}: ${id} is not in the English database`);
    const share = latinShare(prose.join("\n"));
    if (share > MAX_LATIN_SHARE)
      findings.push(`${dir}/ar/${name}: ${Math.round(share * 100)}% of its Arabic prose is Latin script`);
  }
  return findings;
}

// ---- the output -------------------------------------------------------------------------

/** Every English page has its /ar/ page and the other way round; every Arabic page is Arabic. */
export function checkPages(files, read) {
  const html = files.filter((f) => f.endsWith(".html"));
  const set = new Set(html);
  const findings = [];
  for (const f of html) {
    if (f.startsWith("ar/")) {
      if (!set.has(f.slice(3))) findings.push(`${f}: an Arabic page with no English page`);
      const share = latinShare(htmlProse(read(f)));
      if (share > MAX_LATIN_SHARE)
        findings.push(`${f}: ${Math.round(share * 100)}% of its prose is Latin script`);
    } else if (!set.has(`ar/${f}`)) findings.push(`${f}: no Arabic page (ar/${f})`);
  }
  return findings;
}
