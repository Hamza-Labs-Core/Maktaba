// Story 58.12 T02: BrighterScript compiles the channel with bslint's rules (tools/build.mjs fails
// on any error), the store zip holds what the channel needs and nothing of the tests, stays under
// Roku's size limit, and the device-test package (T04's files) runs the logic tests to a pass.
// The device-test package's stdout is kept in build/logs.

import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";

import JSZip from "jszip";

import { DEVICE_TESTS, MAX_ZIP_BYTES, build, buildVersion } from "../tools/build.mjs";
import { LOGS, ROKU } from "./brs.mjs";

const CLI = join(ROKU, "node_modules", "brs-node", "bin", "brs.cli.js");

test("T02: the store build compiles clean, holds the channel and stays under Roku's limit", async () => {
  const zip = await build(["--out", join(ROKU, "build", "maktaba-roku-store.zip")]);
  const size = statSync(zip).size;
  assert.ok(size < MAX_ZIP_BYTES, `${size} bytes`);
  const files = await JSZip.loadAsync(readFileSync(zip));
  const names = Object.keys(files.files);
  for (const need of [
    "manifest",
    "source/main.brs",
    "components/MainScene.xml",
    "locale/en_US/translations.ts",
    "locale/ar_SA/translations.ts",
    "fonts/IBMPlexSansArabic-Regular.ttf",
    "fonts/IBMPlexSansArabic-SemiBold.ttf",
    "fonts/OFL-IBMPlexSansArabic.txt",
    "images/icon_focus_hd.png",
    "images/icon_focus_fhd.png",
    "images/splash_fhd.png",
  ]) {
    assert.ok(names.includes(need), `${need} in the zip`);
  }
  assert.ok(!names.some((n) => n.startsWith("tests/") || n.endsWith("_test.brs") || n === "source/check.brs"), "no test file ships");
  const manifest = await files.file("manifest").async("string");
  assert.match(manifest, /^bs_const=TEST=false$/m);
  assert.doesNotMatch(manifest, /maktaba_(cloud|probe)/, "no test endpoint in a store build");
  assert.match(manifest, /^splash_color=#0B111C$/m);
});

test("58.13: a release's zip carries the tag's version as Roku's build_version", async () => {
  assert.equal(buildVersion("0.3.150"), 3150);
  assert.equal(buildVersion("0.4.0"), 4000, "a minor release is above every patch of the last");
  assert.throws(() => buildVersion("v0.3.1"), /X\.Y\.Z/);
  assert.throws(() => buildVersion("0.3.1000"), /does not fit/);
  const zip = await build(["--version", "0.3.150", "--out", join(ROKU, "build", "maktaba-roku-0.3.150.zip")]);
  const manifest = await (await JSZip.loadAsync(readFileSync(zip))).file("manifest").async("string");
  assert.match(manifest, /^major_version=1$/m);
  assert.match(manifest, /^minor_version=0$/m);
  assert.match(manifest, /^build_version=3150$/m);
  assert.match(manifest, /^bs_const=TEST=false$/m);
});

test("T04's package: the logic tests side-load as one channel and pass on brs-engine", async () => {
  const zip = await build(["--device-tests"]);
  mkdirSync(LOGS, { recursive: true });
  let out;
  try {
    out = execFileSync(process.execPath, [CLI, zip], { cwd: join(ROKU, "build"), encoding: "utf8", timeout: 120_000 });
  } catch (err) {
    out = `${err.stdout ?? ""}\n${err.stderr ?? ""}\n${err.message}`;
  }
  writeFileSync(join(LOGS, "device-tests.log"), out);
  const results = [...out.matchAll(/RESULT pass=\s*(\d+) fail=\s*(\d+)/g)].map((m) => [Number(m[1]), Number(m[2])]);
  assert.equal(results.length, DEVICE_TESTS.length, out);
  for (const [pass, fail] of results) {
    assert.equal(fail, 0, out);
    assert.ok(pass > 0);
  }
  assert.match(out, /\[device-tests\] done/);
});
