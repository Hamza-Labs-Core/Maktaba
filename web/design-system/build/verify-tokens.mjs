// Smoke test for the token builder (Story 17.1).
//
// `make test-tokens` runs this directly via `node`. Invokes the generator
// and asserts each output file exists, parses, and contains the expected
// shape (key tokens that downstream code consumes).
//
// Filename intentionally avoids `.test.mjs` so vitest's default glob
// (`*.test.{js,mjs,ts,tsx}`) doesn't try to run it as a vitest suite —
// this is a plain node script, not a vitest harness.

import { readFile, stat } from "node:fs/promises";
import { spawnSync } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const builder = join(here, "build-tokens.mjs");
const distDir = join(here, "dist");

function fail(msg) {
  console.error(`test-tokens: ${msg}`);
  process.exit(1);
}

// ---------------------------------------------------------------------------
// Step 1 — run the generator
// ---------------------------------------------------------------------------

const result = spawnSync(process.execPath, [builder], { encoding: "utf8" });

if (result.status !== 0) {
  console.error(result.stdout);
  console.error(result.stderr);
  fail(`builder exited ${result.status}`);
}

if (!/emitted \d+ tokens \+ \d+ theme overrides/.test(result.stdout)) {
  fail(`unexpected builder output: ${result.stdout}`);
}

// ---------------------------------------------------------------------------
// Step 2 — each output exists and is non-empty
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

// ---------------------------------------------------------------------------
// Step 3 — tokens.css contains the canonical `:root` block + theme variants
// ---------------------------------------------------------------------------

const css = await readFile(join(distDir, "tokens.css"), "utf8");
const cssChecks = [
  /:root\s*\{/,
  /\[data-theme="dark"\]\s*\{/,
  /\[data-theme="high-contrast"\]\s*\{/,
  /--color-brand-500:\s*#1E5AD8;/,
  // semantic reference → var(--…)
  /--color-semantic-bg:\s*var\(--color-neutral-\d+\);/,
];
for (const re of cssChecks) {
  if (!re.test(css)) fail(`tokens.css missing expected pattern: ${re}`);
}

// ---------------------------------------------------------------------------
// Step 4 — tokens.json round-trips and resolves references
// ---------------------------------------------------------------------------

const data = JSON.parse(await readFile(join(distDir, "tokens.json"), "utf8"));
if (!data.tokens || !data.themes) {
  fail("tokens.json missing top-level tokens/themes keys");
}
// Reference resolution: color.semantic.bg → color.neutral.50 → "#FAFAFA".
const resolvedBg = data.tokens.color?.semantic?.bg;
if (resolvedBg !== "#FAFAFA") {
  fail(`color.semantic.bg should resolve to #FAFAFA, got ${JSON.stringify(resolvedBg)}`);
}
// Theme overrides surface in `themes`.
if (data.themes.dark?.color?.semantic?.bg !== "#18181B") {
  fail(`dark theme should override color.semantic.bg to #18181B (color.neutral.900)`);
}

// ---------------------------------------------------------------------------
// Step 5 — tokens.ts parses as a JS module syntactically (eval via dynamic
// import). Using `data:` URL keeps this hermetic.
// ---------------------------------------------------------------------------

const ts = await readFile(join(distDir, "tokens.ts"), "utf8");
// Strip TypeScript-specific tail (`as const`, `export type ...`) so node's
// JS parser accepts the body. The test below only cares that the runtime
// exports parse, not the type metadata.
const jsBody = ts.replace(/ as const;/g, ";").replace(/^export type .*$/gm, "");
const mod = await import(`data:text/javascript;base64,${Buffer.from(jsBody).toString("base64")}`);
if (!mod.tokens || mod.tokens.color?.brand?.["500"] !== "#1E5AD8") {
  fail(`tokens.ts didn't import correctly or missing color.brand.500`);
}
if (mod.themes?.dark?.color?.semantic?.bg !== "#18181B") {
  fail(`tokens.ts themes.dark.color.semantic.bg should be #18181B`);
}

console.log("test-tokens: builder OK — css/ts/json outputs validated");
