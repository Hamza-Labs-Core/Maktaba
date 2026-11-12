#!/usr/bin/env node
// maktaba.tv's CSS lint (story 60.04, README §6.5): the site's stylesheets lay out the English
// and the Arabic pages with the same rules, so they use logical properties only
// (margin-inline-start, inset-inline-end, text-align: start) and never a physical left or right.
// Top and bottom are left alone: they do not flip between the languages.
//
//   node scripts/lint-css.mjs [FILE.css ...]
//
// With no file it reads the site's stylesheets (static/**/*.css, the shell's design/shell.css and
// design/fonts.css, src/site.css) and the install guides' (web/packages/install/site/**/*.css),
// which maktaba.tv serves too. Node's standard library
// only. A line can opt out with a comment naming why, `/* physical: <why> */`, for a rule that
// is about the screen and not the text (none today).

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const PKG = path.resolve(HERE, "..");
export const DEFAULT_DIRS = [
  path.join(PKG, "static"),
  path.resolve(PKG, "../install/site"),
  // the shell the site serves (story 60.01 part A, scripts/shell.mjs): its stylesheets, not the
  // mockups' copy of Epic 59's (design/mockups/vendor/install.css, which stays outside T04)
  path.join(PKG, "design/shell.css"),
  path.join(PKG, "design/fonts.css"),
  path.join(PKG, "src/site.css"),
  // part B's components and the download page's own (story 60.11), which it serves
  path.join(PKG, "design/site.css"),
  path.join(PKG, "src/download.css"),
];

// properties whose very name is physical, and what to write instead
const PHYSICAL_PROPS = new Map([
  ["left", "inset-inline-start"],
  ["right", "inset-inline-end"],
  ["margin-left", "margin-inline-start"],
  ["margin-right", "margin-inline-end"],
  ["padding-left", "padding-inline-start"],
  ["padding-right", "padding-inline-end"],
  ["border-left", "border-inline-start"],
  ["border-right", "border-inline-end"],
  ["scroll-margin-left", "scroll-margin-inline-start"],
  ["scroll-margin-right", "scroll-margin-inline-end"],
  ["scroll-padding-left", "scroll-padding-inline-start"],
  ["scroll-padding-right", "scroll-padding-inline-end"],
  ["border-top-left-radius", "border-start-start-radius"],
  ["border-top-right-radius", "border-start-end-radius"],
  ["border-bottom-left-radius", "border-end-start-radius"],
  ["border-bottom-right-radius", "border-end-end-radius"],
]);
// border-left-width, border-right-color, ...
const PHYSICAL_BORDER = /^border-(left|right)-(width|style|color)$/;
// properties whose physical value is left or right
const PHYSICAL_VALUES = new Set(["text-align", "float", "clear", "caption-side", "resize"]);

/** Strips comments, keeping line breaks (so line numbers hold) and noting opt-outs. */
function stripComments(css) {
  const optOut = new Set();
  const out = css.replace(/\/\*[\s\S]*?\*\//g, (c, at) => {
    if (/^\/\*\s*physical:\s*[^*\s]/.test(c)) optOut.add(css.slice(0, at).split("\n").length);
    return c.replace(/[^\n]/g, " ");
  });
  return { css: out, optOut };
}

/** The physical properties in a stylesheet: [{line, property, value, instead}]. */
export function lintCss(text) {
  const { css, optOut } = stripComments(text);
  const findings = [];
  const lines = css.split("\n");
  // declarations: `name: value` up to ; or } (custom properties, --x, are values, not layout)
  const decl = /(^|[{;\s])([a-zA-Z-]+)\s*:\s*([^;{}]*)/g;
  lines.forEach((line, i) => {
    const n = i + 1;
    if (optOut.has(n)) return;
    for (const m of line.matchAll(decl)) {
      const prop = m[2].toLowerCase();
      const value = m[3].trim();
      // a selector's pseudo-class (a:hover {) is not a declaration
      if (/^[a-z-]+\s*\{/.test(m[3]) || line.trim().endsWith("{")) continue;
      if (PHYSICAL_PROPS.has(prop))
        findings.push({ line: n, property: prop, value, instead: PHYSICAL_PROPS.get(prop) });
      else if (PHYSICAL_BORDER.test(prop))
        findings.push({
          line: n,
          property: prop,
          value,
          instead: prop.replace(
            /-(left|right)-/,
            (_, s) => `-inline-${s === "left" ? "start" : "end"}-`
          ),
        });
      else if (PHYSICAL_VALUES.has(prop) && /\b(left|right)\b/i.test(value))
        findings.push({
          line: n,
          property: prop,
          value,
          instead: `${prop}: ${prop.startsWith("text") ? "start / end" : "inline-start / inline-end"}`,
        });
    }
  });
  return findings;
}

function cssFiles(dir, out = []) {
  if (!fs.existsSync(dir)) return out;
  if (fs.statSync(dir).isFile()) return [...out, dir];
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) cssFiles(p, out);
    else if (e.name.endsWith(".css")) out.push(p);
  }
  return out.sort();
}

export function main(argv) {
  const files = argv.length ? argv : DEFAULT_DIRS.flatMap((d) => cssFiles(d));
  if (!files.length) {
    console.error("lint-css: no stylesheet found");
    return 1;
  }
  let bad = 0;
  for (const f of files) {
    for (const x of lintCss(fs.readFileSync(f, "utf8"))) {
      const rel = path.relative(process.cwd(), f);
      console.log(
        `::error file=${rel},line=${x.line}::${rel}:${x.line}: ${x.property}: ${x.value} is physical; write ${x.instead}`
      );
      bad++;
    }
  }
  console.log(
    `lint-css: ${files.length} stylesheet(s), ${bad} physical propert${bad === 1 ? "y" : "ies"}`
  );
  return bad ? 1 : 0;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2));
}
