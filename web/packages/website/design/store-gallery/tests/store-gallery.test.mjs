// Story 60.14, the store gallery's design checks (run by `make test-tokens`, the unit_tokens
// CI job): the size manifest names a source for every asset, every gallery fits its store's
// count, every caption is written in both languages without words the stores refuse, every
// stand-in exists and none shows the old demo library, and the mockups and their PNGs are
// current.

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { BEATS, STORES, LANGS, beatsFor, mockups, here, standinOf } from "../make.mjs";
import { checkPngs } from "../render.mjs";
import { findings } from "../../tests/logical.test.mjs";

const repo = resolve(here, "..", "..", "..", "..", "..");

test("every store asset with a size has a count, formats and a source on the store's own site", () => {
  for (const [id, store] of Object.entries(STORES.stores)) {
    assert.ok(store.sources?.length, `${id}: no sources`);
    for (const u of store.sources) assert.match(u, /^https:\/\//, `${id}: ${u}`);
    if (store.notListed) continue;
    for (const [name, a] of Object.entries(store.assets)) {
      assert.match(a.source ?? "", /^https:\/\//, `${id}.${name}: no source`);
      if (!a.size) continue;
      assert.equal(a.size.length, 2, `${id}.${name}: size`);
      assert.ok(a.count?.length === 2 && a.count[0] <= a.count[1], `${id}.${name}: count`);
      assert.ok(a.formats?.length, `${id}.${name}: formats`);
    }
  }
});

test("every listing names a store asset that exists, and a surface the beats draw", () => {
  const surfaces = new Set();
  for (const app of Object.values(BEATS.apps))
    for (const b of app.beats) for (const s of Object.keys(b.capture ?? {})) surfaces.add(s);
  for (const l of STORES.listings) {
    const store = STORES.stores[l.store];
    assert.ok(store && !store.notListed, `${l.id}: store ${l.store}`);
    assert.ok(existsSync(join(repo, l.built)), `${l.id}: ${l.built} does not exist`);
    for (const set of l.sets) {
      assert.ok(store.assets[set.asset], `${l.id}: asset ${set.asset}`);
      if (set.surface && set.surface !== "android-tablet-7")
        assert.ok(surfaces.has(set.surface), `${l.id}: surface ${set.surface}`);
    }
  }
});

test("every captioned gallery fits its store's count of screenshots", () => {
  for (const l of STORES.listings) {
    for (const set of l.sets.filter((s) => s.style === "captioned")) {
      const surface = set.surface === "android-tablet-7" ? "android-tablet" : set.surface;
      const n = beatsFor(l.app, surface).length;
      const [min, max] = STORES.stores[l.store].assets[set.asset].count;
      assert.ok(
        n >= min && n <= max,
        `${l.id} ${set.asset}: ${n} beats, the store takes ${min}-${max}`
      );
    }
  }
  const tv = BEATS.apps.tv;
  for (const l of STORES.listings.filter((x) => x.app === "tv")) {
    for (const set of l.sets.filter((s) => s.style === "plain")) {
      const n =
        set.langs === "single"
          ? tv.singleSet[l.store].length
          : tv.beats.filter((b) => !tv.drop?.[l.store]?.includes(b.id)).length;
      const [min, max] = STORES.stores[l.store].assets[set.asset].count;
      assert.ok(n >= min && n <= max, `${l.id}: ${n} screens, the store takes ${min}-${max}`);
    }
  }
});

// Words the stores refuse in listing images (Google Play's metadata policy: rankings, prices,
// "new" and "free" badges, calls to action; Apple 2.3.7 and 2.3.14) and the voice guide's own.
const REFUSED_EN =
  /\b(new|best|top|free|#1|number one|award|download|install now|price|sale|simply|just|easy|powerful|seamless|revolutionary)\b|!/i;
const REFUSED_AR = /(جديد|مجان|الأفضل|أفضل|حمّل|نزّل|سعر|خصم)|!/;

test("every beat has a headline and a line under it in both languages, without refused words", () => {
  for (const [app, a] of Object.entries(BEATS.apps)) {
    assert.ok(a.name.en && a.name.ar, `${app}: name`);
    for (const b of a.beats) {
      for (const lang of LANGS) {
        const c = b[lang];
        assert.ok(c?.h && c?.sub, `${app}.${b.id}.${lang}`);
        assert.ok(c.h.length <= 52, `${app}.${b.id}.${lang}: headline over 52 characters`);
      }
      assert.doesNotMatch(`${b.en.h} ${b.en.sub}`, REFUSED_EN, `${app}.${b.id}.en`);
      assert.doesNotMatch(`${b.ar.h} ${b.ar.sub}`, REFUSED_AR, `${app}.${b.id}.ar`);
      assert.doesNotMatch(`${b.ar.h} ${b.ar.sub}`, /[A-Za-z]/, `${app}.${b.id}.ar: Latin letters`);
    }
  }
});

// The old demo library (Blender's open movies) shows women; the new one is being made. The
// maktaba.dev screenshots below were taken on it, as were the Roborazzi `demo/` references.
const OLD_DEMO = [/\/demo\//, /firetv-home-/, /firetv-library-/, /\/static\/shots\//];

test("every stand-in exists in the repository and none shows the old demo library", () => {
  for (const a of Object.values(BEATS.apps)) {
    for (const b of a.beats) {
      for (const surface of Object.keys(b.standin ?? {})) {
        for (const lang of LANGS) {
          const url = standinOf(b, surface, lang);
          if (!url) continue;
          assert.ok(
            existsSync(join(repo, url)),
            `${b.id} ${surface} ${lang}: ${url} does not exist`
          );
          for (const re of OLD_DEMO) assert.doesNotMatch(url, re, `${b.id} ${surface}: ${url}`);
        }
      }
    }
  }
});

test("the mockups are current with make.mjs", () => {
  for (const [f, html] of Object.entries(mockups())) {
    const have = readFileSync(join(here, "mockups", f), "utf8");
    assert.equal(
      have,
      html,
      `${f} is stale: run node web/packages/website/design/store-gallery/make.mjs`
    );
  }
});

test("every mockup PNG is present, under 1 MB and without metadata", async () => {
  assert.deepEqual(await checkPngs(), []);
});

test("the gallery's CSS and mockups use logical properties only (60.01 T04's lint)", () => {
  const files = [
    "gallery.css",
    "sheet.css",
    ...readdirSync(join(here, "mockups")).map((f) => join("mockups", f)),
  ];
  const all = files.flatMap((f) => findings(f, readFileSync(join(here, f), "utf8")));
  assert.deepEqual(all, []);
});
