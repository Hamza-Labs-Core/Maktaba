// The token checks `make test-tokens` runs (Story 48.2):
//
//   - parity: the hand-written web/src/styles/tokens.css (what the web ships) holds the
//     same semantic colours, brand palette, spacing, radii and type sizes as the JSON;
//   - contrast: every text colour on every surface it is used on is >= 4.5:1 (WCAG 2.2
//     AA, NFR-8), in light and in dark.
//
// Each returns a list of problems (empty when all is well), so the self-tests in
// verify-tokens.mjs can prove a bad input is caught.

import { composite, contrast, parseColor, sameColor } from "./colors.mjs";

/** Semantic colours only the apps use; the web has no CSS variable for them. */
export const APP_ONLY = new Set(["raised", "nav", "scrim", "chosen"]);

/** Text colours, and the surfaces each must read on. */
export const TEXT = ["fg", "muted", "accent-text", "success", "warn", "error", "running", "info"];
export const SURFACES = ["bg", "subtle", "raised", "nav"];
/** Pairs beyond TEXT × SURFACES. `scrim` is left out: its text sits on the library art
 * behind it, which the brand stories (48.10, 48.11) check in their screenshots. */
export const EXTRA_PAIRS = [
  ["fg", "chosen"],
  ["muted", "chosen"],
  ["accent-fg", "accent"],
];
export const AA = 4.5;

function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, "");
}

/** The custom properties declared in the first block whose selector list matches. */
export function cssBlock(css, selectorRe) {
  const src = stripComments(css);
  const re = /([^{}]+)\{([^{}]*)\}/g;
  let m;
  while ((m = re.exec(src)) !== null) {
    if (selectorRe.test(m[1].trim())) {
      const vars = {};
      for (const d of m[2].matchAll(/(--[a-z0-9-]+)\s*:\s*([^;]+);/gi)) {
        vars[d[1]] = d[2].trim();
      }
      return vars;
    }
  }
  return null;
}

function resolveVar(value, vars, seen = new Set()) {
  const m = /^var\(\s*(--[a-z0-9-]+)\s*\)$/i.exec(value);
  if (!m) return value;
  if (seen.has(m[1]) || !(m[1] in vars)) return value;
  return resolveVar(vars[m[1]], vars, new Set([...seen, m[1]]));
}

/** The light (`:root`) and dark (`[data-theme="dark"]`) variables of tokens.css. */
export function webThemes(css) {
  const light = cssBlock(css, /^:root$/);
  const darkOnly = cssBlock(css, /^\[data-theme="dark"\]/);
  if (!light) throw new Error("tokens.css: no :root block");
  if (!darkOnly) throw new Error('tokens.css: no [data-theme="dark"] block');
  const dark = { ...light, ...darkOnly };
  const resolve = (vars) =>
    Object.fromEntries(Object.entries(vars).map(([k, v]) => [k, resolveVar(v, vars)]));
  return { light: resolve(light), dark: resolve(dark) };
}

/**
 * Compares tokens.css with the resolved JSON trees (`light`: the base; `dark`: the base
 * with the dark overrides on top).
 */
export function parity(css, { light, dark }) {
  const problems = [];
  const web = webThemes(css);

  for (const key of Object.keys(light.color.semantic)) {
    if (APP_ONLY.has(key)) continue;
    const name = `--color-${key}`;
    for (const [theme, json] of [
      ["light", light],
      ["dark", dark],
    ]) {
      const cssValue = web[theme][name];
      const jsonValue = json.color.semantic[key];
      if (cssValue === undefined) {
        problems.push(`${theme}: tokens.css has no ${name} (JSON color.semantic.${key})`);
      } else if (!sameColor(cssValue, jsonValue)) {
        problems.push(
          `${theme}: ${name} is ${cssValue} in tokens.css but ${jsonValue} in the JSON (color.semantic.${key})`
        );
      }
    }
  }

  // Every semantic alias the web's components read must exist in the JSON.
  for (const name of Object.keys(web.light)) {
    const m = /^--color-semantic-(.+)$/.exec(name);
    if (m && !(m[1] in light.color.semantic)) {
      problems.push(`tokens.css aliases ${name} but the JSON has no color.semantic.${m[1]}`);
    }
  }

  // Palettes and scales: whatever tokens.css declares must match the JSON.
  const scales = [
    [/^--color-brand-(.+)$/, light.color.brand, "color.brand", "colour"],
    [/^--color-neutral-(.+)$/, light.color.neutral, "color.neutral", "colour"],
    [/^--space-(.+)$/, light.spacing, "spacing", "length"],
    [/^--radius-(.+)$/, light.radius, "radius", "length"],
    [/^--font-size-(.+)$/, light.type.size, "type.size", "length"],
  ];
  for (const [re, group, path, kind] of scales) {
    for (const [name, cssValue] of Object.entries(web.light)) {
      const m = re.exec(name);
      if (!m) continue;
      const jsonValue = group[m[1]];
      if (jsonValue === undefined) {
        problems.push(`tokens.css has ${name} but the JSON has no ${path}.${m[1]}`);
        continue;
      }
      const same =
        kind === "colour" ? sameColor(cssValue, jsonValue) : cssValue === String(jsonValue);
      if (!same) {
        problems.push(
          `${name} is ${cssValue} in tokens.css but ${jsonValue} in the JSON (${path}.${m[1]})`
        );
      }
    }
  }
  return problems;
}

/** Every text/background pair, per theme, with its contrast ratio. */
export function contrastPairs(themes) {
  const rows = [];
  const pairs = [...TEXT.flatMap((t) => SURFACES.map((s) => [t, s])), ...EXTRA_PAIRS];
  for (const [theme, tree] of Object.entries(themes)) {
    const sem = tree.color.semantic;
    for (const [text, surface] of pairs) {
      if (!(text in sem) || !(surface in sem)) {
        rows.push({ theme, text, surface, ratio: NaN, missing: true });
        continue;
      }
      // A translucent surface is judged as painted on the page.
      const bg = parseColor(sem[surface]).a < 1 ? composite(sem[surface], sem.bg) : sem[surface];
      const fg = parseColor(sem[text]).a < 1 ? composite(sem[text], bg) : sem[text];
      rows.push({ theme, text, surface, ratio: contrast(fg, bg) });
    }
  }
  return rows;
}

export function contrastProblems(themes) {
  return contrastPairs(themes)
    .filter((r) => r.missing || !(r.ratio >= AA))
    .map((r) =>
      r.missing
        ? `${r.theme}: color.semantic.${r.text} or .${r.surface} is missing`
        : `${r.theme}: ${r.text} on ${r.surface} is ${r.ratio.toFixed(2)}:1, under ${AA}:1`
    );
}
