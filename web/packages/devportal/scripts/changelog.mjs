// The changelog page: CHANGELOG.md (CHANGELOG.ar.md) and the fragments (changelog.d/, one pair
// of files per pull request; changelog.d/README.md).
//
// A fragment is never folded into the files. Its release is the earliest dated vX.Y.Z tag whose tree has
// it, read from git at build time, so no release edits a file, no step waits for a person, and
// the page cannot disagree with what a tag contains. A fragment in no tag is unreleased. With no
// tags fetched (a shallow clone) every fragment reads as unreleased.
//
// The page, newest first: [Unreleased], the fragments no tag has; each release that brought
// fragments; CHANGELOG.md's own [Unreleased] entries, frozen when the fragments began (under
// [Unreleased] until a release brings a fragment, then under their own heading); and the
// file's releases.

import fs from "node:fs";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { STRINGS } from "./strings.mjs";
import {
  CHANGELOG_SECTIONS,
  UNRELEASED,
  arabicOf,
  changelogOutline,
  checkArabicMarkdown,
} from "./parity.mjs";

export const FRAGMENTS = "changelog.d";
const NAME = /^\d{4}-\d\d-\d\d-[a-z0-9-]+\.md$/;
// one short fragment has more product names in it than a whole page has
const FRAGMENT_LATIN_SHARE = 0.5;

/** The fragments, newest first (their names begin with a date): name, English, Arabic. */
export function readFragments(repo) {
  const dir = path.join(repo, FRAGMENTS);
  if (!fs.existsSync(dir)) return [];
  const names = fs.readdirSync(dir).filter((f) => f !== "README.md" && !f.startsWith("."));
  const text = (f) => fs.readFileSync(path.join(dir, f), "utf8").replace(/\r\n/g, "\n");
  const read = (f) => (names.includes(f) ? text(f) : null);
  return [...new Set(names.map((f) => f.replace(/(\.ar)?\.md$/, ".md")))]
    .sort()
    .reverse()
    .map((name) => ({ name, en: read(name), ar: read(arabicOf(name)) }));
}

/** Each fragment is named by its date and is in both languages, with the same sections and
 * entries, in Arabic where it says so. (tools/dev/changelog_fragment.py is the gate that tells
 * the author what to add; this keeps a bad one off the page.) */
export function checkFragments(fragments) {
  const findings = [];
  for (const f of fragments) {
    const en = `${FRAGMENTS}/${f.name}`;
    const ar = arabicOf(en);
    if (!NAME.test(f.name)) findings.push(`${en}: not a fragment (YYYY-MM-DD-slug.md)`);
    if (f.en === null) findings.push(`${ar}: no English fragment (${en})`);
    if (f.ar === null) findings.push(`${en}: no Arabic fragment (${ar})`);
    if (f.en === null || f.ar === null) continue;
    const outline = (md, lang) => {
      const bad = md
        .split("\n")
        .filter((l) => l.trim() && !/^(### |- | {2})/.test(l))
        .map((l) => `?${l}`);
      const [{ sections }] = changelogOutline(`## ${UNRELEASED[lang]}\n${md}`, lang);
      for (const s of sections)
        if (!(s.name in CHANGELOG_SECTIONS) || !s.entries) bad.push(`?${s.name}`);
      if (!sections.length) bad.push("?");
      return { bad, text: sections.map((s) => `${s.name}:${s.entries}`).join(", ") };
    };
    const x = outline(f.en, "en");
    const y = outline(f.ar, "ar");
    for (const [file, o] of [
      [en, x],
      [ar, y],
    ])
      if (o.bad.length)
        findings.push(`${file}: not only sections and their entries: ${o.bad[0].slice(1, 80)}`);
    if (!x.bad.length && !y.bad.length && x.text !== y.text)
      findings.push(
        `${ar}: has entries [${y.text}], ${en} has [${x.text}]: every entry is added in both languages`
      );
    findings.push(...checkArabicMarkdown(ar, f.ar, FRAGMENT_LATIN_SHARE));
  }
  return findings;
}

const VERSION = /^v(\d+)\.(\d+)\.(\d+)$/;
const semver = (tag) => tag.match(VERSION).slice(1).map(Number);
const bySemver = (a, b) => {
  const [x, y] = [semver(a), semver(b)];
  return x[0] - y[0] || x[1] - y[1] || x[2] - y[2];
};

/**
 * The release that brought each fragment: name -> {version, date, timestamp}. Read the tagger
 * date for annotated tags and the tagged commit's date for lightweight tags. Sort by time so a
 * later backport to a lower version does not change a fragment's original release. One git process lists the tags, one their changelog.d trees, then one per
 * distinct tree (most releases bring fragments; those that bring none share a tree).
 */
export function fragmentReleases(repo) {
  const git = (args, input) =>
    execFileSync("git", args, {
      cwd: repo,
      input,
      encoding: "utf8",
      maxBuffer: 1 << 28,
      stdio: ["pipe", "pipe", "ignore"],
    });
  const releases = new Map();
  let listed;
  try {
    listed = git([
      "for-each-ref",
      "--format=%(refname:strip=2) %(creatordate:short) %(creatordate:unix)",
      "refs/tags/v*",
    ]);
  } catch {
    return releases; // not a git checkout: nothing is known to be released
  }
  const dates = new Map(
    listed
      .split("\n")
      .filter(Boolean)
      .map((line) => {
        const [tag, date, timestamp] = line.split(" ");
        return [tag, { date, timestamp: Number(timestamp) }];
      })
  );
  const tags = [...dates.keys()]
    .filter((t) => VERSION.test(t))
    .sort((a, b) => dates.get(a).timestamp - dates.get(b).timestamp || bySemver(a, b));
  if (!tags.length) return releases;
  const trees = git(
    ["cat-file", "--batch-check=%(objectname) %(objecttype)"],
    tags.map((t) => `${t}:${FRAGMENTS}\n`).join("")
  ).split("\n");
  const seen = new Set();
  tags.forEach((tag, i) => {
    const m = trees[i].match(/^([0-9a-f]{40,64}) tree$/);
    if (!m || seen.has(m[1])) return;
    seen.add(m[1]);
    for (const name of git(["ls-tree", "--name-only", m[1]]).split("\n"))
      if (NAME.test(name) && !releases.has(name))
        releases.set(name, { version: tag.slice(1), ...dates.get(tag) });
  });
  return releases;
}

/** A fragment's entries in one language: its sections (in English) and their entries' text. */
function entriesOf(md, lang) {
  const out = [];
  let section = "";
  for (const line of (md ?? "").split("\n")) {
    const m = line.match(/^### (.+)$/);
    if (m) {
      const name = m[1].trim();
      section = Object.keys(CHANGELOG_SECTIONS).find(
        (en) => (lang === "ar" ? CHANGELOG_SECTIONS[en] : en) === name
      );
    } else if (/^- /.test(line)) out.push({ section, text: line });
    else if (out.length && section === out.at(-1).section) out.at(-1).text += `\n${line}`;
  }
  return out.map((e) => ({ ...e, text: e.text.trim() }));
}

/**
 * Keep one bilingual entry when fragments share either wording in the same section.
 * Prefer the first release, then the older filename for entries that have yet to ship.
 */
export function pageEntries(fragments, releases) {
  const published = (f) => {
    const r = releases.get(f.name);
    return r ? (r.timestamp ?? Date.parse(r.date) / 1000) : Infinity;
  };
  const byAge = [...fragments].sort((a, b) => {
    const x = releases.get(a.name);
    const y = releases.get(b.name);
    return (
      published(a) - published(b) ||
      (x && y ? bySemver(`v${x.version}`, `v${y.version}`) : 0) ||
      a.name.localeCompare(b.name)
    );
  });
  const seen = new Set();
  const out = new Map();
  for (const f of byAge) {
    const ar = entriesOf(f.ar, "ar");
    const pairs = entriesOf(f.en, "en").map((e, i) => ({
      section: e.section,
      en: e.text,
      ar: ar[i]?.text ?? "", // a missing half is checkFragments' finding, not a crash here
    }));
    out.set(
      f.name,
      pairs.filter((p) => {
        const enKey = JSON.stringify([p.section, "en", p.en]);
        const arKey = JSON.stringify([p.section, "ar", p.ar]);
        if (seen.has(enKey) || (p.ar && seen.has(arKey))) return false;
        seen.add(enKey);
        if (p.ar) seen.add(arKey);
        return true;
      })
    );
  }
  return out;
}

/** Fragments' entries in one language as one list of sections, in the page's order. */
function sectionsOf(fragments, lang, entries) {
  return Object.entries(CHANGELOG_SECTIONS)
    .map(([en, ar]) => {
      const texts = fragments
        .flatMap((f) => entries.get(f.name))
        .filter((e) => e.section === en)
        .map((e) => e[lang]);
      return texts.length ? `### ${lang === "ar" ? ar : en}\n\n${texts.join("\n\n")}\n\n` : "";
    })
    .join("");
}

/** The changelog page's markdown: the file's, with the fragments in their releases. */
export function changelogPage(md, lang, fragments, releases) {
  const heading = `## ${UNRELEASED[lang]}`;
  const start = md.startsWith(`${heading}\n`) ? 0 : md.indexOf(`\n${heading}\n`) + 1;
  if (!md.slice(start).startsWith(heading)) throw new Error(`changelog: no "${heading}" section`);
  const next = md.indexOf("\n## ", start + heading.length);
  const end = next < 0 ? md.length : next + 1;
  const frozen = md
    .slice(start + heading.length, end)
    .trim()
    .concat("\n\n");
  const entries = pageEntries(fragments, releases);
  const groups = new Map();
  for (const f of fragments) {
    const r = releases.get(f.name);
    const key = r ? r.version : "";
    groups.set(key, { ...r, fragments: [...(groups.get(key)?.fragments ?? []), f] });
  }
  const waiting = groups.get("")?.fragments ?? [];
  groups.delete("");
  let out = md.slice(0, start) + `${heading}\n\n` + sectionsOf(waiting, lang, entries);
  const released = [...groups.values()].sort((a, b) => bySemver(`v${b.version}`, `v${a.version}`));
  for (const g of released) {
    const list = sectionsOf(g.fragments, lang, entries);
    if (list) out += `## [${g.version}] - ${g.date}\n\n${list}`;
  }
  if (released.length && frozen.trim()) out += `## ${STRINGS[lang]["changelog.earlier"]}\n\n`;
  return out + (frozen.trim() ? frozen : "") + md.slice(end);
}
