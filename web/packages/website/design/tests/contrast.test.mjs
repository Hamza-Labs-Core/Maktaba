// Story 60.01 T03: every text and UI colour pair the shell and part B's pages use, in dark
// and light; the foundations sheet's table is drawn from the same pairs.
// Text >= 4.5:1; large text and UI parts (control edges, focus ring, rules that carry
// meaning, the step rings, callout edges) >= 3:1. Run: node --test design/tests/

import assert from "node:assert/strict";
import { test } from "node:test";
import { loadTokens, resolvedThemes } from "../../../../design-system/build/build-tokens.mjs";
import { composite, contrast, parseColor } from "../../../../design-system/build/colors.mjs";
import { SHOWN_PAIRS } from "../mockups/part-b.mjs";

const TEXT = 4.5;
const UI = 3;

// [foreground, background, minimum, where]. Paths are into the resolved token tree.
export const PAIRS = [
  // body, secondary and link text on every surface the shell paints
  ...[
    "color.semantic.bg",
    "site.surface.hero",
    "site.surface.section",
    "site.surface.code",
  ].flatMap((bg) => [
    ["color.semantic.fg", bg, TEXT, "body text"],
    ["color.semantic.muted", bg, TEXT, "secondary text, the footer, slugs"],
    ["color.semantic.accent-text", bg, TEXT, "links"],
  ]),
  ["site.accent.cta-fg", "site.accent.cta", TEXT, "the call to action"],
  ["site.accent.cta-fg", "site.accent.cta-hover", TEXT, "the call to action, hovered"],
  [
    "color.semantic.fg",
    ["color.semantic.accent-soft", "site.surface.hero"],
    TEXT,
    "the problem page's who-sees-this pill",
  ],
  ["site.callout.tip", "site.surface.code", TEXT, "the Copied label"],
  ["color.semantic.accent-text", "color.semantic.bg", TEXT, "the step number"],
  // UI parts
  ...[
    "color.semantic.bg",
    "site.surface.hero",
    "site.surface.section",
    "site.surface.code",
  ].flatMap((bg) => [
    ["site.line.control", bg, UI, "a control's edge (filter, copy, language switch)"],
    ["site.accent.focus", bg, UI, "the focus ring"],
  ]),
  [
    "site.accent.mark",
    "color.semantic.bg",
    UI,
    "the current page's rule, the step ring, the facts' edge",
  ],
  ["site.accent.mark", "site.surface.hero", UI, "the arch, the language switch's hover edge"],
  ["site.callout.note", "site.surface.section", UI, "a note's edge and icon"],
  ["site.callout.warning", "site.surface.section", UI, "a warning's edge and icon"],
  ["site.callout.tip", "site.surface.section", UI, "a tip's edge and icon"],
  // part B: the screenshot band, the section band and the download cards
  ...["site.surface.shot", "site.surface.section"].flatMap((bg) => [
    ["color.semantic.fg", bg, TEXT, "a caption's name, a card's title"],
    ["color.semantic.muted", bg, TEXT, "captions, a card's requirements line"],
    ["color.semantic.accent-text", bg, TEXT, "kickers, links, a topic's title, a step number"],
    ["site.accent.mark", bg, UI, "the shelf line, the chosen card's edge, a feature ring"],
    ["site.line.control", bg, UI, "a card icon's edge, a disclosure's edge"],
    ["site.accent.focus", bg, UI, "the focus ring"],
  ]),
  ["site.accent.cta-fg", "site.accent.cta", TEXT, "the download card's button"],
  [
    "color.semantic.fg",
    ["color.semantic.accent-soft", "site.surface.section"],
    TEXT,
    "the badge on the chosen card, the platform note",
  ],
  [
    "color.semantic.fg",
    ["site.accent.glow", "site.surface.hero"],
    TEXT,
    "the hero's headline over the glow",
  ],
  [
    "color.semantic.muted",
    ["site.accent.glow", "site.surface.hero"],
    TEXT,
    "the hero's lead over the glow",
  ],
];

const get = (tree, path) => path.split(".").reduce((n, k) => n?.[k], tree);

/** A colour: a token, or [top, bottom] painted one over the other. */
function color(tree, ref) {
  if (Array.isArray(ref)) return composite(get(tree, ref[0]), get(tree, ref[1]));
  const v = get(tree, ref);
  assert.ok(v, `no token ${ref}`);
  return parseColor(v);
}

export function problems(themes, pairs = PAIRS) {
  const out = [];
  for (const [name, tree] of Object.entries(themes)) {
    for (const [fg, bg, min, where] of pairs) {
      const ratio = contrast(color(tree, fg), color(tree, bg));
      if (ratio < min)
        out.push(`${name}: ${fg} on ${bg} (${where}) is ${ratio.toFixed(2)}:1, under ${min}:1`);
    }
  }
  return out;
}

const themes = resolvedThemes(await loadTokens());

test("every pair passes in dark and light", () => {
  assert.deepEqual(problems(themes), []);
});

test("the check catches a weak pair (self-test)", () => {
  const weak = structuredClone(themes);
  weak.dark.site.line.control = "#2A3448";
  assert.ok(problems(weak).some((p) => p.startsWith("dark: site.line.control")));
});

test("every pair the foundations sheet shows is tested here", () => {
  const key = ([fg, bg]) => JSON.stringify([fg, bg]);
  const tested = new Set(PAIRS.map(key));
  assert.deepEqual(SHOWN_PAIRS.filter((p) => !tested.has(key(p))).map(key), []);
});

test("the shell is dark first: the dark page is ink", () => {
  assert.equal(themes.dark.color.semantic.bg, "#0B111C");
});
