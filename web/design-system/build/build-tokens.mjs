// Design-token build (Story 17.1 — HLB-253 / GitHub #32).
//
// Reads `web/design-system/tokens/{tokens,tokens.dark,tokens.high-contrast}.json`
// and emits three derived artifacts under
// `web/design-system/build/dist/`:
//
//   - tokens.css   — light theme on :root, dark/high-contrast variants on
//                    [data-theme="..."] selectors. Each leaf becomes a
//                    `--<group>-<subgroup>-<leaf>` custom property; `{a.b.c}`
//                    references resolve to `var(--a-b-c)` so a downstream
//                    theme override propagates through the cascade.
//   - tokens.ts    — typed export of every leaf as `tokens.color.brand[500]`,
//                    plus a `themes` object holding the dark + high-contrast
//                    override sets. Components can import this for type-
//                    checked token access.
//   - tokens.json  — canonical re-emission with refs resolved and a stable
//                    key order so downstream tools have a deterministic shape.
//
// Story 48.2 adds the apps' committed token files, from the same trees:
//
//   - apps/apple/MaktabaKit/Sources/MaktabaKit/Tokens.swift
//   - apps/android/core/src/main/kotlin/com/hamzalabs/maktaba/core/ui/Tokens.kt
//
// (see native.mjs). The hand-written `web/src/styles/tokens.css` is what the
// web ships; `make test-tokens` fails when its semantic colours, palette or
// scales drift from the JSON (checks.mjs), when a text/background pair is
// under AA, or when a committed native file is stale (`--check`).
//
// `make build-tokens` calls this script; `verify-tokens.mjs` runs it with
// `--check` as part of `make test-tokens`.

import { readFile, mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { emitKotlin, emitSwift, KOTLIN_PATH, nativeModel, SWIFT_PATH } from "./native.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const tokensDir = join(here, "..", "tokens");
const outDir = join(here, "dist");
const repoRoot = resolve(here, "..", "..", "..");

const SOURCES = {
  base: "tokens.json",
  dark: "tokens.dark.json",
  highContrast: "tokens.high-contrast.json",
};

async function loadJson(path) {
  const raw = await readFile(path, "utf8");
  try {
    return JSON.parse(raw);
  } catch (err) {
    throw new Error(`invalid JSON in ${path}: ${err.message}`);
  }
}

// ---------------------------------------------------------------------------
// Token-tree walking
// ---------------------------------------------------------------------------
//
// A leaf is any object with a `value` property. Everything else is a group.
// `walk` yields `[pathSegments, leafValue, description]` for every leaf so
// downstream stages don't need to recurse themselves.

function isLeaf(node) {
  return (
    node !== null &&
    typeof node === "object" &&
    !Array.isArray(node) &&
    Object.hasOwn(node, "value")
  );
}

function* walk(node, prefix = []) {
  if (node === null || typeof node !== "object") return;
  for (const [key, child] of Object.entries(node)) {
    if (key.startsWith("$")) continue; // skip JSON-Schema metadata like $schema
    const path = [...prefix, key];
    if (isLeaf(child)) {
      yield { path, value: child.value, description: child.description };
    } else {
      yield* walk(child, path);
    }
  }
}

// ---------------------------------------------------------------------------
// Reference resolution
// ---------------------------------------------------------------------------
//
// A leaf value of the form `{a.b.c}` resolves to the leaf at that path.
// CSS output rewrites references as `var(--a-b-c)` so the cascade can
// override at any level; TS / JSON outputs resolve to concrete values
// (recursively, to handle reference chains).

const REF_RE = /^\{([a-zA-Z0-9_.-]+)\}$/;

function refTarget(value) {
  if (typeof value !== "string") return null;
  const match = value.match(REF_RE);
  return match ? match[1].split(".") : null;
}

function flatPath(segments) {
  return segments.join("-");
}

function resolveConcrete(tree, value, seen = new Set()) {
  const target = refTarget(value);
  if (target === null) return value;
  const key = target.join(".");
  if (seen.has(key)) {
    throw new Error(`token reference cycle: ${[...seen, key].join(" -> ")}`);
  }
  let node = tree;
  for (const seg of target) {
    if (node === null || typeof node !== "object" || !Object.hasOwn(node, seg)) {
      throw new Error(`unresolved token reference: {${key}}`);
    }
    node = node[seg];
  }
  if (!isLeaf(node)) {
    throw new Error(`token reference {${key}} resolves to a group, not a leaf`);
  }
  return resolveConcrete(tree, node.value, new Set([...seen, key]));
}

// ---------------------------------------------------------------------------
// CSS emission
// ---------------------------------------------------------------------------

function escapeCssValue(value) {
  // tokens.json values are primitives; nothing to escape today, but if a
  // future token holds a string with embedded quotes/newlines it'll need
  // proper quoting. Throwing on anything unexpected keeps the gate strict.
  if (typeof value === "string" || typeof value === "number") return String(value);
  if (typeof value === "boolean") return value ? "1" : "0";
  throw new Error(`unsupported token value type: ${typeof value}`);
}

function cssValueFor(value) {
  const target = refTarget(value);
  if (target !== null) return `var(--${flatPath(target)})`;
  return escapeCssValue(value);
}

function emitCss({ base, themes }) {
  const lines = [
    "/*",
    " * Generated by web/design-system/build/build-tokens.mjs from",
    " * web/design-system/tokens/. Do not edit by hand.",
    " *",
    ' * Light theme on :root, dark + high-contrast on [data-theme="..."].',
    " */",
    "",
    ":root {",
  ];
  for (const { path, value } of walk(base)) {
    lines.push(`  --${flatPath(path)}: ${cssValueFor(value)};`);
  }
  lines.push("}");
  for (const [themeName, overrides] of Object.entries(themes)) {
    lines.push("", `[data-theme="${themeName}"] {`);
    for (const { path, value } of walk(overrides)) {
      lines.push(`  --${flatPath(path)}: ${cssValueFor(value)};`);
    }
    lines.push("}");
  }
  return lines.join("\n") + "\n";
}

// ---------------------------------------------------------------------------
// TypeScript emission
// ---------------------------------------------------------------------------
//
// We emit a single `tokens` object (resolved to concrete primitives) plus
// a `themes` map. Type information comes from `as const` so callers get
// literal-narrowed types (e.g. tokens.color.brand[500] is the literal
// `"#1E5AD8"`, not just `string`).

function buildTreeOfResolvedValues(tree, base = tree) {
  if (isLeaf(tree)) return resolveConcrete(base, tree.value);
  const out = {};
  for (const [key, child] of Object.entries(tree)) {
    if (key.startsWith("$")) continue;
    out[key] = buildTreeOfResolvedValues(child, base);
  }
  return out;
}

function emitTs({ base, themes }) {
  const tokens = buildTreeOfResolvedValues(base);
  const themesResolved = Object.fromEntries(
    Object.entries(themes).map(([name, overrides]) => [
      name,
      buildTreeOfResolvedValues(overrides, base),
    ])
  );
  return [
    "// Generated by web/design-system/build/build-tokens.mjs.",
    "// Do not edit by hand.",
    "",
    `export const tokens = ${JSON.stringify(tokens, null, 2)} as const;`,
    "",
    `export const themes = ${JSON.stringify(themesResolved, null, 2)} as const;`,
    "",
    "export type Tokens = typeof tokens;",
    "export type ThemeName = keyof typeof themes;",
    "",
  ].join("\n");
}

// ---------------------------------------------------------------------------
// Canonical JSON emission
// ---------------------------------------------------------------------------
//
// Same tree shape as tokens.json but with references resolved. Keys sorted
// recursively for deterministic output. Schema reference dropped — the
// canonical form is just data, no $schema needed.

function canonicalSort(obj) {
  if (obj === null || typeof obj !== "object" || Array.isArray(obj)) return obj;
  const keys = Object.keys(obj)
    .filter((k) => !k.startsWith("$"))
    .sort();
  return Object.fromEntries(keys.map((k) => [k, canonicalSort(obj[k])]));
}

function emitJson({ base, themes }) {
  const tokens = buildTreeOfResolvedValues(base);
  const themesResolved = Object.fromEntries(
    Object.entries(themes).map(([name, overrides]) => [
      name,
      buildTreeOfResolvedValues(overrides, base),
    ])
  );
  return (
    JSON.stringify(
      {
        tokens: canonicalSort(tokens),
        themes: canonicalSort(themesResolved),
      },
      null,
      2
    ) + "\n"
  );
}

// ---------------------------------------------------------------------------
// Loading and resolving (shared with verify-tokens.mjs)
// ---------------------------------------------------------------------------

function deepMerge(base, overrides) {
  const out = { ...base };
  for (const [key, child] of Object.entries(overrides)) {
    if (key.startsWith("$")) continue;
    const prev = base[key];
    const bothGroups =
      prev &&
      typeof prev === "object" &&
      !isLeaf(prev) &&
      typeof child === "object" &&
      !isLeaf(child);
    out[key] = bothGroups ? deepMerge(prev, child) : child;
  }
  return out;
}

/** The source trees, validated: every reference resolves. */
export async function loadTokens(dir = tokensDir) {
  const base = await loadJson(join(dir, SOURCES.base));
  const themes = {
    dark: await loadJson(join(dir, SOURCES.dark)),
    "high-contrast": await loadJson(join(dir, SOURCES.highContrast)),
  };
  // Validation pass — resolving every reference up front catches typos in
  // the source JSON before we write a partially-good output.
  for (const { value } of walk(base)) {
    resolveConcrete(base, value);
  }
  for (const overrides of Object.values(themes)) {
    for (const { value } of walk(overrides)) {
      resolveConcrete(base, value);
    }
  }
  return { base, themes };
}

/** Concrete light and dark trees: the base, and the base with the dark overrides on top. */
export function resolvedThemes({ base, themes }) {
  const dark = deepMerge(base, themes.dark);
  return {
    light: buildTreeOfResolvedValues(base),
    dark: buildTreeOfResolvedValues(dark, dark),
  };
}

/** Repo-relative path → contents of every committed, generated native file. */
export function nativeFiles(tokens) {
  const { light, dark } = resolvedThemes(tokens);
  const descriptions = Object.fromEntries(
    Object.entries(tokens.base.color.semantic)
      .filter(([, leaf]) => leaf.description)
      .map(([key, leaf]) => [key, leaf.description])
  );
  const model = nativeModel({ light, dark, descriptions });
  return { [SWIFT_PATH]: emitSwift(model), [KOTLIN_PATH]: emitKotlin(model) };
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------
//
//   node build-tokens.mjs                    write dist/ and the native files
//   node build-tokens.mjs --check            write dist/; fail if a native file is stale
//   node build-tokens.mjs --out-root <dir>   where the native files live (default: the repo)

async function main(argv) {
  const check = argv.includes("--check");
  const rootAt = argv.indexOf("--out-root");
  const outRoot = rootAt >= 0 ? resolve(argv[rootAt + 1]) : repoRoot;

  const tokens = await loadTokens();
  const { base, themes } = tokens;

  await mkdir(outDir, { recursive: true });
  await writeFile(join(outDir, "tokens.css"), emitCss({ base, themes }), "utf8");
  await writeFile(join(outDir, "tokens.ts"), emitTs({ base, themes }), "utf8");
  await writeFile(join(outDir, "tokens.json"), emitJson({ base, themes }), "utf8");

  const stale = [];
  for (const [rel, contents] of Object.entries(nativeFiles(tokens))) {
    const path = join(outRoot, rel);
    if (check) {
      const current = await readFile(path, "utf8").catch(() => null);
      if (current !== contents) stale.push(rel);
    } else {
      await mkdir(dirname(path), { recursive: true });
      await writeFile(path, contents, "utf8");
    }
  }

  const leafCount = [...walk(base)].length;
  const themeCount = Object.keys(themes).length;
  console.log(
    `build-tokens: emitted ${leafCount} tokens + ${themeCount} theme overrides ` +
      `→ ${outDir}/{tokens.css,tokens.ts,tokens.json}` +
      (check ? "; native files checked" : `; wrote ${SWIFT_PATH}, ${KOTLIN_PATH}`)
  );
  if (stale.length > 0) {
    console.error(
      `build-tokens: stale generated files (run \`make build-tokens\` and commit):\n` +
        stale.map((p) => `  ${p}`).join("\n")
    );
    process.exit(1);
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch((err) => {
    console.error(`build-tokens: ${err.stack || err.message}`);
    process.exit(1);
  });
}
