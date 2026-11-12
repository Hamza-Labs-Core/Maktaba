// Story 60.01 T04: the shell's CSS and the mockups' HTML use logical properties only: no
// physical left/right margin, padding, inset, border, float or text-align. Epic 59's own
// install stylesheet (mockups/vendor/install.css, a copy) is that package's to change.

import assert from "node:assert/strict";
import { readFileSync, readdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";

const design = join(dirname(fileURLToPath(import.meta.url)), "..");

const PHYSICAL = [
  /(?<![-\w])(margin|padding|border)-(left|right)\b/,
  /(?<![-\w])(left|right)\s*:/,
  /(?<![-\w])float\s*:\s*(left|right)/,
  /(?<![-\w])text-align\s*:\s*(left|right)/,
  /(?<![-\w])border-(top|bottom)-(left|right)-radius/,
];

/** The CSS of a file: a stylesheet whole, an HTML file's <style> blocks and style="". */
function cssOf(file, text) {
  const noComments = (s) => s.replace(/\/\*[\s\S]*?\*\//g, "");
  if (file.endsWith(".css")) return noComments(text);
  const blocks = [...text.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]);
  const attrs = [...text.matchAll(/\sstyle="([^"]*)"/g)].map((m) => m[1]);
  return noComments([...blocks, ...attrs].join("\n"));
}

export function findings(file, text) {
  const out = [];
  cssOf(file, text)
    .split("\n")
    .forEach((line, i) => {
      for (const re of PHYSICAL) if (re.test(line)) out.push(`${file}:${i + 1}: ${line.trim()}`);
    });
  return out;
}

function files() {
  const out = ["shell.css", "fonts.css", "site-tokens.css"].map((f) => join(design, f));
  for (const f of readdirSync(join(design, "mockups"))) {
    if (f.endsWith(".html") || f.endsWith(".css")) out.push(join(design, "mockups", f));
  }
  return out;
}

test("no physical property in the shell or the mockups", () => {
  const all = files().flatMap((f) => findings(f.slice(design.length + 1), readFileSync(f, "utf8")));
  assert.deepEqual(all, []);
});

test("the lint catches a physical property (self-test)", () => {
  assert.equal(findings("x.css", "a { margin-left: 4px }").length, 1);
  assert.equal(findings("x.css", ".x { padding-right: 0 }").length, 1);
  assert.equal(findings("x.css", ".x { left: 0 }").length, 1);
  assert.equal(findings("x.css", ".x { text-align: right }").length, 1);
  assert.equal(findings("x.html", '<p style="float: left">').length, 1);
  assert.equal(findings("x.css", ".x { margin-inline-start: 4px; inset-inline-end: 0 }").length, 0);
  assert.equal(findings("x.css", "/* top left in English */").length, 0);
});
