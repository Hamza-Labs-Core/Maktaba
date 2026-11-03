// node --test tools/ci/catalog_words.test.mjs — story 51.01 T07, T08.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { check } from "./catalog_words.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = join(here, "..", "..");
const read = (p) => JSON.parse(readFileSync(join(root, p), "utf8"));
const dump = read("shared/components/testdata/catalog.dump.json");
const en = read("web/src/i18n/en.json");
const ar = read("web/src/i18n/ar.json");

test("the real words pass", () => {
  assert.deepEqual(check(dump, en, ar), []);
});

test("T07: an English title missing fails, naming the key and en", () => {
  const dir = mkdtempSync(join(tmpdir(), "catalog-words-"));
  const bad = { ...en };
  delete bad["components.llm.title"];
  writeFileSync(join(dir, "en.json"), JSON.stringify(bad));
  const r = spawnSync(process.execPath, [join(here, "catalog_words.mjs"), "--en", join(dir, "en.json")], { encoding: "utf8" });
  assert.equal(r.status, 1);
  assert.match(r.stderr, /components\.llm\.title: no words in en/);
});

test("T08: Arabic words equal to the English fail", () => {
  const bad = { ...ar, "components.transcription.title": en["components.transcription.title"] };
  const problems = check(dump, en, bad);
  assert.deepEqual(problems, ["components.transcription.title: the ar words are a copy of the en words"]);
});

test("a reason code with no Arabic fails", () => {
  const bad = { ...ar };
  delete bad["components.reason.held"];
  assert.deepEqual(check(dump, en, bad), ["components.reason.held: no words in ar"]);
});
