// Epic 58 story 58.08 T01 — the TV build's checks fail on what Chromium 68
// cannot run, and the CSS pass lowers what it can.
//
//   node --test tools/web/tv.test.mjs      (web/node_modules installed: es-check)
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { test } from "node:test";
import {
  check,
  cssProblems,
  forEsCheck,
  jsProblems,
  topLevel,
} from "./tvcheck.mjs";
import { lowerDecl, lowerForM68, lowerValue } from "./tvcss.mjs";

function dist(files) {
  const dir = mkdtempSync(path.join(os.tmpdir(), "tv-test-"));
  for (const [name, text] of Object.entries({
    "index.html": "<!doctype html>",
    ...files,
  })) {
    mkdirSync(path.dirname(path.join(dir, name)), { recursive: true });
    writeFileSync(path.join(dir, name), text);
  }
  return dir;
}

test("a build of ES2017 with M68 CSS passes", () => {
  const dir = dist({
    "assets/a.js":
      "export const f = async () => (await import('./b.js')).default + import.meta.url;",
    "assets/a.css": ".a{display:flex}.a>*+*{margin-left:8px}",
  });
  try {
    assert.deepEqual(check(dir), []);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("optional chaining, ?? and :has() fail the build", () => {
  const dir = dist({
    "assets/a.js": "export const x = (o) => o?.a ?? 1;",
    "assets/a.css": ".a:has(.b){color:red}",
  });
  try {
    const problems = check(dir).join("\n");
    assert.match(problems, /es-check es2017 failed/);
    assert.match(problems, /:has\(\)/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("a missing index.html fails", () => {
  const dir = mkdtempSync(path.join(os.tmpdir(), "tv-test-"));
  try {
    assert.match(check(dir)[0], /index\.html is missing/);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
});

test("cssProblems names each rule M68 cannot run", () => {
  assert.deepEqual(cssProblems(".a{width:clamp(1px,2vw,3px)}"), [
    "clamp() (M79)",
  ]);
  assert.deepEqual(cssProblems(".a{inset:0}"), ["inset (M87)"]);
  assert.deepEqual(cssProblems(".a{margin-inline-start:1px}"), [
    "logical margin or padding (M69/M87)",
  ]);
  assert.match(
    cssProblems(".a{display:flex;gap:4px}")[0],
    /gap on a flex container/,
  );
  assert.match(
    cssProblems(".a,.b:focus-visible{color:red}")[0],
    /in a selector list/,
  );
  assert.match(
    cssProblems(".a{aspect-ratio:16/9}")[0],
    /aspect-ratio without its fallback/,
  );
  // alone, or behind its fallback, it is fine
  assert.deepEqual(cssProblems(".b:focus-visible{color:red}"), []);
  assert.deepEqual(cssProblems(".a:not(:is(.b,.c)){color:red}"), []);
  assert.deepEqual(
    cssProblems(
      ".a{aspect-ratio:16/9}@supports not (aspect-ratio:1/1){.a:before{padding-top:56.25%}}",
    ),
    [],
  );
  assert.deepEqual(
    cssProblems("@supports (gap:1px){.a{display:flex;gap:4px}}").length,
    1,
  );
});

test("topLevel splits a selector list outside parentheses", () => {
  assert.deepEqual(topLevel(".a:is(.b,.c), .d"), [".a:is(.b,.c)", ".d"]);
});

test("jsProblems refuses eval and new Function", () => {
  assert.deepEqual(jsProblems("eval('1');new Function('x')"), [
    "eval",
    "new Function",
  ]);
  assert.deepEqual(jsProblems("a.eval(1)"), []);
});

test("forEsCheck keeps dynamic import and import.meta (M63/M64) out of es-check's way", () => {
  assert.equal(
    forEsCheck("import('./a.js');import.meta.url"),
    "__dynamicImport('./a.js');__importMeta.url",
  );
});

test("lowerValue takes clamp's preferred value and min/max's viewport term", () => {
  assert.equal(lowerValue("clamp(1rem, 2vw, 3rem)"), "2vw");
});

test("lowerDecl turns logical properties into physical ones, mirrored for RTL", () => {
  assert.deepEqual(lowerDecl("margin-block", "1px 2px"), [
    [
      ["margin-top", "1px"],
      ["margin-bottom", "2px"],
    ],
    [],
    [],
  ]);
  assert.deepEqual(lowerDecl("padding-inline-start", "4px"), [
    [],
    [["padding-left", "4px"]],
    [["padding-right", "4px"]],
  ]);
  assert.deepEqual(lowerDecl("padding-inline", "4px"), [
    [
      ["padding-left", "4px"],
      ["padding-right", "4px"],
    ],
    [],
    [],
  ]);
  assert.deepEqual(lowerDecl("inset", "0"), [
    [
      ["top", "0"],
      ["right", "0"],
      ["bottom", "0"],
      ["left", "0"],
    ],
    [],
    [],
  ]);
  assert.deepEqual(lowerDecl("color", "red"), [[["color", "red"]], [], []]);
});

test("lowerForM68 output passes the check: gap, logical sides, aspect-ratio, fonts", () => {
  const css = lowerForM68(`
    /* a comment { with: braces; } */
    .row { display: flex; gap: 8px; margin-inline-start: 4px; }
    .art { aspect-ratio: 16 / 9; inset: 0; }
    .wide { aspect-ratio: auto; }
    @font-face { font-family: X; src: url(x.woff2) format("woff2"), url(x.woff) format("woff"); }
  `);
  assert.deepEqual(cssProblems(css), []);
  assert.match(css, /\[dir="rtl"\] \.row \{\s*margin-right: 4px;/);
  assert.match(css, /\.row > \* \+ \*/);
  assert.match(
    css,
    /\.art::before \{ content: ""; float: left; padding-top: 56\.25%; \}/,
  );
  assert.match(css, /\.wide::before, \.wide::after \{ content: none;/);
  assert.doesNotMatch(css, /x\.woff"|format\("woff"\)/);
});
