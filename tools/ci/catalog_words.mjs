#!/usr/bin/env node
// tools/ci/catalog_words.mjs — the component catalog's words (Epic 51, story 51.01): every
// component's title_key and desc_key, every phase (components.phase.<phase>) and every
// reason code (components.reason.<code>) and every feature (components.feature.<id>,
// story 51.09, with its capabilities.off.<id> and capabilities.unavailable.<id> sentences,
// story 51.11) has a non-empty string in both web/src/i18n/en.json and ar.json, and the
// Arabic one is not a copy of the English one (a brand name, SAME_IN_BOTH, may be). The
// Android and Apple apps carry each feature's two sentences in the web's words (checkApps).
//
// It reads the catalog through shared/components/testdata/catalog.dump.json, which the
// catalog job checks is current first (make catalog-sync).
//
//   node tools/ci/catalog_words.mjs [--dump F] [--en F] [--ar F] [--android-en F] [--android-ar F] [--apple F]
//
// Exit 1 names each key and the language it lacks.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");

export function catalogKeys(dump) {
  const keys = [];
  for (const c of dump.components) keys.push(c.title_key, c.desc_key);
  for (const p of dump.phases) keys.push(`components.phase.${p}`);
  for (const r of dump.reasons) keys.push(`components.reason.${r}`);
  // story 51.11: each feature's own "is off" and "isn't available" sentences, so the verb
  // agrees with the name in both languages ("Transcripts are off")
  for (const f of dump.features ?? [])
    keys.push(`components.feature.${f}`, `capabilities.off.${f}`, `capabilities.unavailable.${f}`);
  return keys;
}

// A brand name reads the same in both languages.
export const SAME_IN_BOTH = new Set(["components.feature.plex"]);

export function check(dump, en, ar) {
  const problems = [];
  for (const key of catalogKeys(dump)) {
    const e = en[key];
    const a = ar[key];
    if (typeof e !== "string" || e.trim() === "") problems.push(`${key}: no words in en`);
    if (typeof a !== "string" || a.trim() === "") problems.push(`${key}: no words in ar`);
    else if (typeof e === "string" && a.trim() === e.trim() && !SAME_IN_BOTH.has(key))
      problems.push(`${key}: the ar words are a copy of the en words`);
  }
  return problems;
}

// An Android strings.xml as name -> text, unescaped as Android reads it (\' \").
export function androidStrings(xml) {
  const out = {};
  for (const m of xml.matchAll(/<string name="([^"]+)"[^>]*>([\s\S]*?)<\/string>/g))
    out[m[1]] = m[2].replace(/\\(['"])/g, "$1");
  return out;
}

// Story 51.11: the native apps say a feature is off in the web's words. Each feature's
// capabilities.off.<id> and capabilities.unavailable.<id> are the Android core's
// feature_off_<id> / feature_unavailable_<id> and the Apple MaktabaKit catalog's entry under
// the web's key, in English and Arabic.
export function checkApps(dump, en, ar, androidEn, androidAr, appleCatalog) {
  const problems = [];
  const apple = appleCatalog.strings ?? {};
  const appleWord = (key, lang) => apple[key]?.localizations?.[lang]?.stringUnit?.value;
  for (const f of dump.features ?? []) {
    for (const [web, res] of [
      [`capabilities.off.${f}`, `feature_off_${f}`],
      [`capabilities.unavailable.${f}`, `feature_unavailable_${f}`],
    ]) {
      for (const [lang, dict, android] of [
        ["en", en, androidEn],
        ["ar", ar, androidAr],
      ]) {
        if (android[res] !== dict[web])
          problems.push(`${res} (android ${lang}) is not the web's ${web}: ${JSON.stringify(android[res])}`);
        if (appleWord(web, lang) !== dict[web])
          problems.push(`${web} (apple ${lang}) is not the web's: ${JSON.stringify(appleWord(web, lang))}`);
      }
    }
  }
  return problems;
}

function main(argv) {
  const opt = {
    dump: "shared/components/testdata/catalog.dump.json",
    en: "web/src/i18n/en.json",
    ar: "web/src/i18n/ar.json",
    "android-en": "apps/android/core/src/main/res/values/strings.xml",
    "android-ar": "apps/android/core/src/main/res/values-ar/strings.xml",
    apple: "apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings",
  };
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i].replace(/^--/, "");
    if (!(k in opt) || argv[i + 1] === undefined) {
      console.error(`usage: catalog_words.mjs [--dump F] [--en F] [--ar F] [--android-en F] [--android-ar F] [--apple F] (bad ${argv[i]})`);
      return 2;
    }
    opt[k] = argv[i + 1];
  }
  const text = (p) => readFileSync(p.startsWith("/") ? p : join(root, p), "utf8");
  const read = (p) => JSON.parse(text(p));
  const [dump, en, ar] = [read(opt.dump), read(opt.en), read(opt.ar)];
  const problems = [
    ...check(dump, en, ar),
    ...checkApps(dump, en, ar, androidStrings(text(opt["android-en"])), androidStrings(text(opt["android-ar"])), read(opt.apple)),
  ];
  for (const p of problems) console.error(`catalog_words: ${p}`);
  if (problems.length) return 1;
  console.log(`catalog_words: ${catalogKeys(read(opt.dump)).length} keys have English and Arabic words`);
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  process.exit(main(process.argv.slice(2)));
}
