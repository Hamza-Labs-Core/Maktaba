// Story 60.14 part 2: compose.mjs on synthetic captures (flat PNGs named as the shot tests name
// theirs). Needs the website package's npm install and a Chromium for Playwright: CI's
// `store gallery (compose)` job runs it in the Playwright image.

import { test } from "node:test";
import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import sharp from "sharp";
import { compose, plan, pathOf, captureKey } from "../compose.mjs";
import { BEATS } from "../make.mjs";

const SIZE = {
  iphone: [1206, 2622],
  "android-phone": [1080, 2340],
  "android-tablet": [2560, 1600],
  "android-tablet-7": [1200, 1920],
  androidtv: [1920, 1080],
  firetv: [1920, 1080],
  samsung: [1920, 1080],
};

/** A flat capture for every job of `only`, coloured per capture so contact sheets differ. */
async function fakeCaptures(dir, only) {
  mkdirSync(dir, { recursive: true });
  const made = new Set();
  for (const j of plan({ only })) {
    if (!j.capture || made.has(j.capture)) continue;
    made.add(j.capture);
    const surface =
      Object.keys(SIZE).find(
        (s) => j.capture.startsWith(`${s}-`) && !j.capture.startsWith(`${s}-tablet`)
      ) ?? "androidtv";
    const [w, h] = SIZE[surface];
    const hue = [...j.capture].reduce((a, c) => a + c.charCodeAt(0), 0) % 200;
    await sharp({
      create: {
        width: w,
        height: h,
        channels: 3,
        background: { r: 20 + hue / 4, g: 30, b: 60 + hue / 2 },
      },
    })
      .png()
      .toFile(join(dir, `${j.capture}.png`));
  }
}

test("a capture key names the surface, the Remote and the screen's language", () => {
  const player = BEATS.apps.player.beats;
  assert.equal(captureKey("player", player[0], "iphone", "ar"), "iphone-home-ar");
  // the language beat shows the other language's screen
  const lang = player.find((b) => b.id === "arabic");
  assert.equal(captureKey("player", lang, "iphone", "en"), "iphone-home-ar");
  assert.equal(
    captureKey("remote", BEATS.apps.remote.beats[0], "iphone", "en"),
    "iphone-remote-dashboards-en"
  );
  // the 7-inch set takes the tablet beats' screens, from its own captures
  assert.equal(
    captureKey("player", player[0], "android-tablet-7", "en"),
    "android-tablet-7-home-en"
  );
});

test("the phone's player beat is drawn on the landscape size the store also takes", () => {
  const jobs = plan({ only: ["apple-player-iphone", "google-player"] });
  const sub = jobs.filter((j) => j.name === "subtitles");
  for (const j of sub.filter((j) => j.set.asset === "iphone-6.9"))
    assert.deepEqual([j.W, j.H], [2868, 1320]);
  for (const j of sub.filter((j) => j.set.asset === "iphone-6.3"))
    assert.deepEqual([j.W, j.H], [2622, 1206]);
  for (const j of sub.filter((j) => j.set.asset === "phone"))
    assert.deepEqual([j.W, j.H], [1920, 1080]);
  // the tablets keep their own size
  for (const j of sub.filter((j) => j.set.asset === "tablet-10"))
    assert.deepEqual([j.W, j.H], [2560, 1440]);
  assert.equal(captureKey("player", sub[0].beat, "iphone", "en"), "iphone-player-landscape-en");
});

test("every listing's images go where fastlane and the store folders expect them", () => {
  const paths = plan().map(pathOf);
  assert.ok(
    paths.includes(
      join("apple", "apple-player-iphone", "screenshots", "ar-SA", "01_iphone-6.3_library")
    )
  );
  assert.ok(
    paths.includes(
      join("google", "google-tv", "metadata", "android", "en-US", "images", "tvBanner")
    )
  );
  assert.ok(
    paths.includes(join("google", "google-tv", "metadata", "android", "ar", "images", "icon"))
  );
  assert.ok(
    paths.includes(
      join(
        "google",
        "google-player",
        "metadata",
        "android",
        "ar",
        "images",
        "phoneScreenshots",
        "07_family"
      )
    )
  );
  assert.ok(paths.includes(join("samsung", "samsung", "all", "tv", "04_subtitles")));
  assert.equal(new Set(paths).size, paths.length, "two images share a path");
});

test(
  "compose writes every image of the chosen listings at the store's size, and they pass",
  { timeout: 600000 },
  async () => {
    const root = mkdtempSync(join(tmpdir(), "compose-test-"));
    try {
      const only = ["google-tv", "samsung", "apple-player-iphone"];
      await fakeCaptures(join(root, "in"), only);
      const r = await compose({
        captureDirs: [join(root, "in")],
        out: join(root, "out"),
        only,
        log: () => {},
      });
      assert.deepEqual(r.missing, []);
      assert.deepEqual(r.problems, []);
      assert.ok(r.ok);
      // Google TV: banner, feature, icon, 7 screens per language; Samsung: 4 logos and art, 4 JPEG screens
      const samsungTv = r.report.filter((x) => x.listing === "samsung" && x.asset === "tv");
      assert.equal(samsungTv.length, 4);
      for (const x of samsungTv) assert.equal(x.format, "jpeg");
      const logo = r.report.find((x) => x.listing === "samsung" && x.asset === "logo");
      assert.ok(logo.hasAlpha, "Samsung's logo layer keeps its transparency");
      const iphone = r.report.filter((x) => x.listing === "apple-player-iphone");
      assert.equal(iphone.length, 2 * 2 * 7, "two sizes, two languages, seven beats");
      for (const x of iphone) {
        const turned = x.beat === "subtitles";
        assert.ok(!x.hasAlpha && (turned ? [2622, 2868] : [1206, 1320]).includes(x.width), x.file);
      }
      assert.ok(existsSync(join(root, "out", "contact", "google-tv.png")));
      const checklist = readFileSync(join(root, "out", "checklist.md"), "utf8");
      assert.equal(
        checklist.split("\n").filter((l) => l.startsWith("| `")).length,
        r.report.length
      );
    } finally {
      rmSync(root, { recursive: true, force: true });
    }
  }
);

test("a missing capture is reported, and fails unless allowed", { timeout: 300000 }, async () => {
  const root = mkdtempSync(join(tmpdir(), "compose-test-"));
  try {
    const r = await compose({
      captureDirs: [join(root, "none")],
      out: join(root, "out"),
      only: ["roku"],
      log: () => {},
    });
    assert.ok(r.missing.length >= 12, "each Roku screen in both languages");
    assert.ok(!r.ok);
    const r2 = await compose({
      captureDirs: [join(root, "none")],
      out: join(root, "out2"),
      only: ["roku"],
      allowMissing: true,
      log: () => {},
    });
    assert.ok(r2.ok, "the poster alone is made, and passes");
    assert.equal(r2.report.length, 2);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
