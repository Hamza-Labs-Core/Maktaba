// Story 60.04 T07: the CSS lint refuses physical left/right properties in the site's
// stylesheets. Run: node --test web/packages/website/scripts/lint-css.test.mjs
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { lintCss } from "./lint-css.mjs";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const SCRIPT = path.join(HERE, "lint-css.mjs");

test("T07: margin-left in a fixture stylesheet is refused, naming the line and the fix", () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "lint-css-"));
  const file = path.join(dir, "site.css");
  fs.writeFileSync(file, ".a {\n  color: red;\n  margin-left: 1rem;\n}\n");
  let out = "";
  let code = 0;
  try {
    execFileSync(process.execPath, [SCRIPT, file], { encoding: "utf8" });
  } catch (e) {
    code = e.status;
    out = e.stdout;
  }
  assert.equal(code, 1);
  assert.match(out, /site\.css:3: margin-left: 1rem is physical; write margin-inline-start/);
});

test("every physical form is found: names, border sides, radii and values", () => {
  const css = `
.a { left: 0; right: 0; padding-right: 2px; border-left: 1px solid; }
.b { border-right-width: 2px; border-top-left-radius: 4px; }
.c { text-align: left; float: right; clear: left; }
.d { text-align: right !important; }`;
  const found = lintCss(css).map((f) => `${f.line}:${f.property}`);
  assert.deepEqual(found, [
    "2:left",
    "2:right",
    "2:padding-right",
    "2:border-left",
    "3:border-right-width",
    "3:border-top-left-radius",
    "4:text-align",
    "4:float",
    "4:clear",
    "5:text-align",
  ]);
});

test("logical properties, top and bottom, and comments pass", () => {
  const css = `
/* margin-left: 1rem in a comment is not a rule */
.a { margin-inline-start: 1rem; inset-inline-end: 0; text-align: start; top: 0; bottom: 0; }
.b { margin-top: 1rem; padding-bottom: 2px; border-block-end: 1px solid; float: inline-start; }
.c:hover { color: red; }
.d { --left-gap: 3px; background-position: left top; }`;
  assert.deepEqual(lintCss(css), []);
});

test("a line can opt out, saying why", () => {
  const css =
    ".a {\n  /* physical: the scrollbar sits on the screen's right in both languages */ right: 0;\n}\n";
  assert.deepEqual(lintCss(css), []);
  assert.equal(lintCss(".a {\n  /* physical: */ right: 0;\n}\n").length, 1, "a reason is required");
});

test("the site's own stylesheets pass", () => {
  execFileSync(process.execPath, [SCRIPT], { encoding: "utf8" });
});
