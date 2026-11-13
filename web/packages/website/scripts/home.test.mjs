// Story 60.10's build tests: T04 (every feature the home page names is shipped and links where
// it is explained, or nowhere until that page exists), the links as the site grows (Download
// and Help before and after 60.11 and 60.12), story 60.09 T04 on the page (every <img> has
// width, height and an alt text in the page's language), the screenshots the page may not show
// (#1198, #1199), and the apps it may not name (Q5).
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { FEATURES, SHOTS, featureHref, homePage, homeScriptFile, readCopy, shot } from "./home.mjs";
import { bLinks, LATEST_RELEASE, TROUBLESHOOTING } from "./shell.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
const REPO = path.resolve(PKG, "../../..");

const none = () => false;
const all = () => true;

test("T04: each feature is an epic the product has, with the app's help topic in both languages", () => {
  assert.equal(FEATURES.length, 6);
  for (const f of FEATURES) {
    const epic = path.join(REPO, "specs/epics", f.epic, "README.md");
    assert.ok(fs.existsSync(epic), `${f.key}: no specs/epics/${f.epic}/README.md`);
    if (f.topic)
      for (const lang of ["en", "ar"])
        assert.ok(
          fs.existsSync(path.join(REPO, "web/src/help/content", lang, `${f.topic}.md`)),
          `${f.key}: no help topic web/src/help/content/${lang}/${f.topic}.md`
        );
    assert.ok(f.topic || f.fallback, `${f.key}: explained nowhere`);
    for (const lang of ["en", "ar"]) {
      const c = readCopy("home", lang);
      for (const k of ["title", "text", "link"])
        assert.ok(c[`does.${f.key}.${k}`], `copy/home.${lang}.md: no does.${f.key}.${k}`);
    }
  }
});

test("T04: a feature links its help topic once built, else the install guides or nothing", () => {
  for (const f of FEATURES) {
    assert.equal(featureHref(f, none), f.fallback, f.key);
    if (f.topic) assert.equal(featureHref(f, all), `/help/${f.topic}/`, f.key);
  }
  // before 60.12, the page links only what it has: the one feature with a fallback
  const html = homePage("en", { links: bLinks(none), resolves: none }).body;
  assert.equal(html.match(/<li class="feature"/g).length, 6);
  assert.doesNotMatch(html, /href="\/help\//);
  assert.match(html, /data-feature="runs"[\s\S]*?href="\/install\/"/);
  const later = homePage("ar", { links: bLinks(all), resolves: all }).body;
  assert.match(later, /href="\/ar\/help\/search\/"/);
});

test("Download goes to the latest release until /download/ is built, then to /download/", () => {
  assert.deepEqual(bLinks(none), { download: LATEST_RELEASE, help: TROUBLESHOOTING });
  assert.deepEqual(bLinks(all), { download: "/download/", help: "/help/" });
  const before = homePage("en", { links: bLinks(none), resolves: none }).body;
  assert.equal([...before.matchAll(/<a [^>]*data-download/g)].length, 2);
  for (const [a] of before.matchAll(/<a [^>]*data-download[^>]*>/g))
    assert.match(a, new RegExp(`href="${LATEST_RELEASE}"`));
  const after = homePage("ar", { links: bLinks(all), resolves: all }).body;
  for (const [a] of after.matchAll(/<a [^>]*data-download[^>]*>/g))
    assert.match(a, /href="\/ar\/download\/"/);
});

test("the Download button has a label for no system, Mac and Windows", () => {
  for (const lang of ["en", "ar"]) {
    const html = homePage(lang, { links: bLinks(none) }).body;
    const button = html.match(/<a class="button for-platform"[\s\S]*?<\/a>/)[0];
    for (const p of ["any", "mac", "windows"]) assert.match(button, new RegExp(`data-for="${p}"`));
  }
});

test("60.09 T04: every picture has its width, height and an alt text, in the page's language", () => {
  for (const lang of ["en", "ar"]) {
    const html = homePage(lang, { links: bLinks(none) }).body;
    const imgs = [...html.matchAll(/<img\b[^>]*>/g)].map((m) => m[0]);
    assert.equal(imgs.length, 8, "the hero, five screens and the pair");
    for (const img of imgs) {
      assert.match(img, /\swidth="\d+"/, img);
      assert.match(img, /\sheight="\d+"/, img);
      const alt = img.match(/\salt="([^"]+)"/)?.[1];
      assert.ok(alt && alt.length > 20, `${img}: no alt text`);
      // the alt text is the page's language: Arabic letters on the Arabic page, none on the English
      assert.equal(/[؀-ۿ]/.test(alt), lang === "ar", `${lang}: ${alt}`);
    }
    // only the hero is fetched first; every other picture is lazy
    assert.equal(imgs.filter((i) => i.includes('fetchpriority="high"')).length, 1);
    assert.equal(imgs.filter((i) => i.includes('loading="lazy"')).length, 7);
    assert.equal((html.match(/<source type="image\/avif"/g) ?? []).length, 8);
  }
});

test("the screenshots exist, and none the site may not show yet", () => {
  for (const s of Object.values(SHOTS))
    for (const lang of ["en", "ar"]) {
      const key = s.key.replace("{lang}", lang);
      for (const size of shot(key).sizes)
        for (const f of [size.webp, size.avif])
          assert.ok(fs.existsSync(path.join(PKG, "static", f)), f);
      // #1198: the Mac window does not mirror in Arabic; #1199: the Android TV player and home
      // show Eastern Arabic digits in Arabic
      assert.notEqual(key, "mac-library-ar");
      assert.notEqual(key, "mac-player-ar");
      assert.ok(!/^androidtv-(player|home)-ar$/.test(key), key);
      assert.ok(!key.startsWith("iphone-remote"), key);
    }
});

test("Q5: the page names no app that is not publicly installable", () => {
  for (const lang of ["en", "ar"]) {
    const html = homePage(lang, { links: bLinks(none) }).body;
    const text = html.replace(/<[^>]+>/g, " ");
    assert.doesNotMatch(text, /\b(iPhone|iPad|Apple TV|Android|tvOS)\b/i, lang);
  }
});

test("the page's script is a classic script: no import, no export", () => {
  const { file, js } = homeScriptFile();
  assert.match(file, /^assets\/home-[\w-]{10}\.js$/);
  assert.doesNotMatch(js, /^\s*(import|export)\b/m);
  assert.ok(Buffer.byteLength(js) < 5000, `${Buffer.byteLength(js)} bytes`);
});
