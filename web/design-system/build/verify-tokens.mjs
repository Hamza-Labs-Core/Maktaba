// `make test-tokens` (Stories 17.1 and 48.2). CI runs it in the unit job.
//
//   1. The builder runs with `--check`: dist/ is written, and the committed Tokens.swift
//      and Tokens.kt must equal what it would write (stale → fail).
//   2. dist/ parses and holds the expected tokens; the dark theme is Ink & Brass.
//   3. web/src/styles/tokens.css matches the JSON (checks.mjs `parity`).
//   4. Every text/background pair is >= 4.5:1 in light and dark (`contrastProblems`).
//   5. Self-tests: a stale native file, a drifted tokens.css colour and a low-contrast
//      pair are each caught, so the checks above cannot pass vacuously.
//
// Filename intentionally avoids `.test.mjs` so vitest's default glob
// (`*.test.{js,mjs,ts,tsx}`) doesn't try to run it as a vitest suite —
// this is a plain node script, not a vitest harness.

import { cp, mkdtemp, readFile, rm, stat, writeFile } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { loadTokens, nativeFiles, resolvedThemes } from "./build-tokens.mjs";
import { contrastPairs, contrastProblems, parity } from "./checks.mjs";
import { KOTLIN_PATH, SWIFT_PATH } from "./native.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const builder = join(here, "build-tokens.mjs");
const distDir = join(here, "dist");
const repoRoot = resolve(here, "..", "..", "..");
const webCssPath = join(repoRoot, "web", "src", "styles", "tokens.css");

function fail(msg) {
  console.error(`test-tokens: ${msg}`);
  process.exit(1);
}

function runBuilder(args) {
  return spawnSync(process.execPath, [builder, ...args], { encoding: "utf8" });
}

// ---------------------------------------------------------------------------
// Step 1 — run the generator in check mode
// ---------------------------------------------------------------------------

const result = runBuilder(["--check"]);
if (result.status !== 0) {
  console.error(result.stdout);
  console.error(result.stderr);
  fail(`builder --check exited ${result.status} (a generated file is stale or the JSON is bad)`);
}
if (!/emitted \d+ tokens \+ \d+ theme overrides/.test(result.stdout)) {
  fail(`unexpected builder output: ${result.stdout}`);
}

// ---------------------------------------------------------------------------
// Step 2 — dist/ outputs exist, parse, and carry Ink & Brass
// ---------------------------------------------------------------------------

for (const file of ["tokens.css", "tokens.ts", "tokens.json"]) {
  const path = join(distDir, file);
  let info;
  try {
    info = await stat(path);
  } catch (err) {
    fail(`missing ${file}: ${err.message}`);
  }
  if (info.size < 100) {
    fail(`${file} suspiciously small (${info.size} bytes)`);
  }
}

const css = await readFile(join(distDir, "tokens.css"), "utf8");
const cssChecks = [
  /:root\s*\{/,
  /\[data-theme="dark"\]\s*\{/,
  /\[data-theme="high-contrast"\]\s*\{/,
  /--color-brand-500:\s*#E3B252;/,
  // semantic reference → var(--…)
  /--color-semantic-bg:\s*var\(--color-neutral-\d+\);/,
  /--color-semantic-accent-text:\s*var\(--color-brand-700\);/,
];
for (const re of cssChecks) {
  if (!re.test(css)) fail(`tokens.css missing expected pattern: ${re}`);
}

const data = JSON.parse(await readFile(join(distDir, "tokens.json"), "utf8"));
if (!data.tokens || !data.themes) {
  fail("tokens.json missing top-level tokens/themes keys");
}
// Reference resolution: color.semantic.bg → color.neutral.0 → "#FFFFFF".
if (data.tokens.color?.semantic?.bg !== "#FFFFFF") {
  fail(`color.semantic.bg should resolve to #FFFFFF, got ${data.tokens.color?.semantic?.bg}`);
}
// The dark theme is Ink & Brass (Story 48.2): ink background, brass accent.
if (data.themes.dark?.color?.semantic?.bg !== "#0B111C") {
  fail(`dark color.semantic.bg should be #0B111C, got ${data.themes.dark?.color?.semantic?.bg}`);
}
if (data.themes.dark?.color?.semantic?.accent !== "#E3B252") {
  fail(
    `dark color.semantic.accent should be #E3B252, got ${data.themes.dark?.color?.semantic?.accent}`
  );
}

const ts = await readFile(join(distDir, "tokens.ts"), "utf8");
// Strip TypeScript-specific tail (`as const`, `export type ...`) so node's
// JS parser accepts the body.
const jsBody = ts.replace(/ as const;/g, ";").replace(/^export type .*$/gm, "");
const mod = await import(`data:text/javascript;base64,${Buffer.from(jsBody).toString("base64")}`);
if (!mod.tokens || mod.tokens.color?.brand?.["500"] !== "#E3B252") {
  fail(`tokens.ts didn't import correctly or missing color.brand.500`);
}
if (mod.themes?.dark?.color?.semantic?.bg !== "#0B111C") {
  fail(`tokens.ts themes.dark.color.semantic.bg should be #0B111C`);
}

// ---------------------------------------------------------------------------
// Step 3 — the web's hand-written tokens.css matches the JSON
// ---------------------------------------------------------------------------

const tokens = await loadTokens();
const themes = resolvedThemes(tokens);
const webCss = await readFile(webCssPath, "utf8");
const drift = parity(webCss, themes);
if (drift.length > 0) {
  fail(`web/src/styles/tokens.css differs from the JSON:\n  ${drift.join("\n  ")}`);
}

// ---------------------------------------------------------------------------
// Step 4 — contrast, light and dark
// ---------------------------------------------------------------------------

const low = contrastProblems(themes);
if (low.length > 0) {
  fail(`contrast under WCAG AA:\n  ${low.join("\n  ")}`);
}
const pairs = contrastPairs(themes);
const weakest = pairs.reduce((a, b) => (b.ratio < a.ratio ? b : a));

// ---------------------------------------------------------------------------
// Step 5 — self-tests: each check catches what it is for
// ---------------------------------------------------------------------------

// 5a. A stale native file fails `--check`; the current ones pass.
const scratch = await mkdtemp(join(tmpdir(), "maktaba-tokens-"));
try {
  const files = nativeFiles(tokens);
  for (const [rel, contents] of Object.entries(files)) {
    const path = join(scratch, rel);
    await cp(join(repoRoot, rel), path, { recursive: true }).catch(async () => {
      await writeFile(path, contents, "utf8");
    });
  }
  if (runBuilder(["--check", "--out-root", scratch]).status !== 0) {
    fail("self-test: --check rejected an up-to-date copy of the native files");
  }
  for (const rel of [SWIFT_PATH, KOTLIN_PATH]) {
    const path = join(scratch, rel);
    const good = await readFile(path, "utf8");
    await writeFile(path, good.replace("E3B252", "E3B253"), "utf8");
    const r = runBuilder(["--check", "--out-root", scratch]);
    if (r.status === 0 || !r.stderr.includes(rel)) {
      fail(`self-test: --check did not catch a hand edit to ${rel}`);
    }
    await writeFile(path, good, "utf8");
  }
  await rm(join(scratch, SWIFT_PATH));
  if (runBuilder(["--check", "--out-root", scratch]).status === 0) {
    fail("self-test: --check did not catch a missing Tokens.swift");
  }
} finally {
  await rm(scratch, { recursive: true, force: true });
}

// 5b. A drifted semantic colour in tokens.css (light and dark) is caught.
for (const [from, to, what] of [
  ["--color-fg: #1c1810;", "--color-fg: #1c1811;", "light --color-fg"],
  ["--color-bg: #0b111c;", "--color-bg: #0b111d;", "dark --color-bg"],
  [
    "--color-semantic-fg: var(--color-fg);",
    "--color-semantic-glow: var(--color-fg);",
    "an unknown alias",
  ],
  ["--space-4: 16px;", "--space-4: 17px;", "--space-4"],
]) {
  if (!webCss.includes(from)) fail(`self-test: tokens.css no longer has "${from}"`);
  if (parity(webCss.replace(from, to), themes).length === 0) {
    fail(`self-test: parity did not catch a change to ${what}`);
  }
}

// 5c. A low-contrast pair is caught.
const dim = structuredClone(themes);
dim.dark.color.semantic.muted = "#3A4252";
if (!contrastProblems(dim).some((p) => p.startsWith("dark: muted on"))) {
  fail("self-test: contrast did not catch dark muted at #3A4252");
}

console.log(
  `test-tokens: OK — native files current, tokens.css matches the JSON, ` +
    `${pairs.length} text/background pairs >= 4.5:1 (weakest: ${weakest.theme} ` +
    `${weakest.text} on ${weakest.surface}, ${weakest.ratio.toFixed(2)}:1)`
);
