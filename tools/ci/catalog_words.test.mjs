// node --test tools/ci/catalog_words.test.mjs — story 51.01 T07, T08.
import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { androidStrings, check, checkApps } from "./catalog_words.mjs";

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

test("story 51.09 T15: a feature with no Arabic words fails", () => {
  const bad = { ...ar };
  delete bad["components.feature.live_tv"];
  assert.deepEqual(check(dump, en, bad), ["components.feature.live_tv: no words in ar"]);
});

test("story 51.11: a feature's off sentence with no Arabic fails", () => {
  const bad = { ...ar };
  delete bad["capabilities.off.transcripts"];
  assert.deepEqual(check(dump, en, bad), ["capabilities.off.transcripts: no words in ar"]);
});

test("every feature of the catalog has its words, Plex the same in both", () => {
  // the tenth is plugin_panels (story 54.20)
  assert.equal(dump.features.length, 10);
  for (const f of dump.features) {
    assert.ok(en[`components.feature.${f}`], f);
    assert.ok(ar[`components.feature.${f}`], f);
  }
  const copied = { ...ar, "components.feature.youtube": en["components.feature.youtube"] };
  assert.deepEqual(check(dump, en, copied), ["components.feature.youtube: the ar words are a copy of the en words"]);
});

const androidEn = androidStrings(readFileSync(join(root, "apps/android/core/src/main/res/values/strings.xml"), "utf8"));
const androidAr = androidStrings(readFileSync(join(root, "apps/android/core/src/main/res/values-ar/strings.xml"), "utf8"));
const apple = read("apps/apple/MaktabaKit/Sources/MaktabaKit/Localizable.xcstrings");

test("story 51.11: the apps say a feature is off in the web's words", () => {
  assert.deepEqual(checkApps(dump, en, ar, androidEn, androidAr, apple), []);
  assert.equal(androidEn["feature_off_transcripts"], "Transcripts are off on this home.");
  assert.equal(androidEn["feature_unavailable_live_tv"], "Live TV isn't available on this home.");
});

test("story 51.11: an app's sentence that drifts from the web fails, naming it", () => {
  const drifted = { ...androidAr, feature_off_live_tv: "البث المباشر متوقفة على الجهاز الرئيسي." };
  assert.deepEqual(checkApps(dump, en, ar, androidEn, drifted, apple), [
    `feature_off_live_tv (android ar) is not the web's capabilities.off.live_tv: "البث المباشر متوقفة على الجهاز الرئيسي."`,
  ]);
  const strings = { ...apple.strings };
  delete strings["capabilities.unavailable.chapters"];
  assert.deepEqual(checkApps(dump, en, ar, androidEn, androidAr, { ...apple, strings }), [
    "capabilities.unavailable.chapters (apple en) is not the web's: undefined",
    "capabilities.unavailable.chapters (apple ar) is not the web's: undefined",
  ]);
});
