#!/usr/bin/env node
// Epic 58 story 58.08 T01 — the TV build runs on Chromium 68 (a 2020 Samsung
// or LG TV), or the build fails:
//
//   - every script of dist/tv parses as ES2017 (es-check): no ?., no ??, no
//     top-level await, no class fields a 2020 TV cannot parse;
//   - no script uses eval or new Function (a TV's CSP and the store reviews);
//   - every stylesheet is free of what M68 cannot run (cssProblems below).
//
//   node web/tv-build/tvcheck.mjs [dist/tv]     exit 1 naming each problem
//
// Playwright on a pinned M68 Chromium is not available, so this check stands
// in for running the screens there (story 58.08's acceptance criteria).
import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const HERE = path.dirname(fileURLToPath(import.meta.url));
const WEB = path.resolve(HERE, "..");

// [what, a pattern on the output, why M68 cannot]: each breaks a layout or a rule
const CSS_RULES = [
  [":has()", /:has\(/, "M105"],
  ["container queries", /@container\b|container-type\s*:/, "M105"],
  ["clamp()", /(^|[^\w-])clamp\(/, "M79"],
  ["min() / max() in a value", /:\s*[^;{}]*(^|[^\w-])(min|max)\(/, "M79"],
  ["inset", /(^|[;{\s])inset(-inline|-block)?(-start|-end)?\s*:/, "M87"],
  [
    "logical margin or padding",
    /(^|[;{\s])(margin|padding)-(inline|block)(-start|-end)?\s*:/,
    "M69/M87",
  ],
  [
    "logical border",
    /(^|[;{\s])border-(inline|block)(-start|-end)?(-width|-color|-style)?\s*:/,
    "M69/M87",
  ],
  [":where()", /:where\(/, "M88"],
];

/** A selector list mixing a selector M68 does not know with others drops them all. */
const ALONE_ONLY = [":focus-visible", ":is("];

/** A selector list's selectors: split at commas outside parentheses. */
export function topLevel(sel) {
  const out = [];
  let depth = 0;
  let cur = "";
  for (const ch of sel) {
    if (ch === "(") depth++;
    else if (ch === ")") depth--;
    if (ch === "," && depth === 0) {
      out.push(cur.trim());
      cur = "";
    } else cur += ch;
  }
  out.push(cur.trim());
  return out.filter(Boolean);
}

/** What in a stylesheet M68 cannot run, as "what (why)" lines; [] when nothing. */
export function cssProblems(css) {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, "");
  const out = [];
  // what an @supports fallback guards is fine: it never applies on M68
  const unguarded = text.replace(/@supports\s*\([^{]*\)\s*\{(?:[^{}]*\{[^{}]*\})*[^{}]*\}/g, "");
  for (const [what, re, why] of CSS_RULES) {
    if (re.test(unguarded)) out.push(`${what} (${why})`);
  }
  for (const m of text.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    const sel = m[1].trim();
    const body = m[2];
    if (sel.startsWith("@")) continue;
    const parts = topLevel(sel);
    for (const bad of ALONE_ONLY) {
      if (parts.length > 1 && parts.some((p) => p.includes(bad)))
        out.push(`${bad} in a selector list (M68 drops the whole rule): ${sel.slice(0, 80)}`);
    }
    // aspect-ratio needs its fallback: the rule's ::before holding the height
    const ratio = /(^|[;{\s])aspect-ratio\s*:\s*([^;}]+)/.exec(body);
    if (ratio && ratio[2].trim() !== "auto") {
      const first = parts[0];
      if (!text.includes(`${first}:before`) && !text.includes(`${first}::before`))
        out.push(`aspect-ratio without its fallback (M88): ${sel.slice(0, 80)}`);
    }
    if (/display\s*:\s*(inline-)?flex/.test(body) && /(^|[;\s])(row-|column-)?gap\s*:/.test(body))
      out.push(`gap on a flex container (M84): ${sel.slice(0, 80)}`);
  }
  return out;
}

/** What in a script M68 or a TV's policy refuses, beyond es-check's parse. */
export function jsProblems(js) {
  const out = [];
  if (/(^|[^\w.$])eval\s*\(/.test(js)) out.push("eval");
  if (/new\s+Function\s*\(/.test(js)) out.push("new Function");
  return out;
}

function files(dir, ext) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = path.join(dir, name);
    if (statSync(p).isDirectory()) out.push(...files(p, ext));
    else if (p.endsWith(ext)) out.push(p);
  }
  return out;
}

/**
 * A script as es-check reads it: dynamic import() and import.meta are ES2020 in
 * the standard but Chromium 63 and 64 run them (a module build cannot do without
 * them), so they become plain calls and names; everything else is checked as is.
 */
export function forEsCheck(js) {
  return js
    .replace(/\bimport\s*\(/g, "__dynamicImport(")
    .replace(/\bimport\.meta\b/g, "__importMeta");
}

/** Runs es-check es2017 on the scripts; the failures as lines. */
export function esCheck(scripts) {
  if (scripts.length === 0) return [];
  const bin = path.join(WEB, "node_modules", ".bin", "es-check");
  const tmp = mkdtempSync(path.join(os.tmpdir(), "tvcheck-"));
  try {
    const copies = scripts.map((f, i) => {
      const out = path.join(tmp, `${i}-${path.basename(f)}`);
      writeFileSync(out, forEsCheck(readFileSync(f, "utf8")));
      return out;
    });
    execFileSync(bin, ["es2017", ...copies, "--module", "--noColor"], {
      stdio: "pipe",
    });
    return [];
  } catch (e) {
    const text = `${e.stdout ?? ""}${e.stderr ?? ""}`.trim();
    return [
      `es-check es2017 failed:\n${text
        .split("\n")
        .filter((l) => !/^\s+at /.test(l))
        .join("\n")}`,
    ];
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
}

export function check(dist) {
  const problems = [];
  if (!existsSync(path.join(dist, "index.html"))) return [`${dist}/index.html is missing`];
  const scripts = files(dist, ".js");
  problems.push(...esCheck(scripts));
  for (const f of scripts) {
    for (const p of jsProblems(readFileSync(f, "utf8")))
      problems.push(`${path.relative(dist, f)}: ${p}`);
  }
  for (const f of files(dist, ".css")) {
    for (const p of cssProblems(readFileSync(f, "utf8")))
      problems.push(`${path.relative(dist, f)}: ${p}`);
  }
  return problems;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dist = path.resolve(process.argv[2] ?? path.join(WEB, "dist", "tv"));
  const problems = check(dist);
  if (problems.length > 0) {
    console.error(`tvcheck: ${dist} would not run on Chromium 68:\n- ${problems.join("\n- ")}`);
    process.exit(1);
  }
  console.log(`tvcheck: ${dist} parses as ES2017 and its CSS runs on Chromium 68`);
}
