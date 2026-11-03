#!/usr/bin/env node
// tools/ci/catalog_words.mjs — the component catalog's words (Epic 51, story 51.01): every
// component's title_key and desc_key, every phase (components.phase.<phase>) and every
// reason code (components.reason.<code>) and every feature (components.feature.<id>,
// story 51.09) has a non-empty string in both web/src/i18n/en.json and ar.json, and the
// Arabic one is not a copy of the English one (a brand name, SAME_IN_BOTH, may be).
//
// It reads the catalog through shared/components/testdata/catalog.dump.json, which the
// catalog job checks is current first (make catalog-sync).
//
//   node tools/ci/catalog_words.mjs [--dump F] [--en F] [--ar F]
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
  for (const f of dump.features ?? []) keys.push(`components.feature.${f}`);
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

function main(argv) {
  const opt = { dump: "shared/components/testdata/catalog.dump.json", en: "web/src/i18n/en.json", ar: "web/src/i18n/ar.json" };
  for (let i = 0; i < argv.length; i += 2) {
    const k = argv[i].replace(/^--/, "");
    if (!(k in opt) || argv[i + 1] === undefined) {
      console.error(`usage: catalog_words.mjs [--dump F] [--en F] [--ar F] (bad ${argv[i]})`);
      return 2;
    }
    opt[k] = argv[i + 1];
  }
  const read = (p) => JSON.parse(readFileSync(p.startsWith("/") ? p : join(root, p), "utf8"));
  const problems = check(read(opt.dump), read(opt.en), read(opt.ar));
  for (const p of problems) console.error(`catalog_words: ${p}`);
  if (problems.length) return 1;
  console.log(`catalog_words: ${catalogKeys(read(opt.dump)).length} keys have English and Arabic words`);
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  process.exit(main(process.argv.slice(2)));
}
