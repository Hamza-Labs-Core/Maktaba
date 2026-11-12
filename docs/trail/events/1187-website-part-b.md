---
schema: 1
kind: pr
pr: 1187
title: "Epic 60 stories 60.01 part B and 60.02 part B: the full design direction, approved, with the chosen tagline"
date: 2026-11-12
session: 112f87b9-bc63-4785-afc3-8b932c0159a5
agent: claude-code
---

## What happened
- #1187 merged as d5795d508: part B of the design. The brand direction, the wordmark (the app
  icon with "Maktaba | مكتبة"), the display face (Amiri Bold, subset and renamed "Maktaba
  Display", OFL), the colour extension with tested contrast, device frames and icons, layouts
  for home, download, install, help, problems and legal at 360, 768 and 1280 px in both
  directions, component specs, the home and download copy in both languages, and a traced
  `static/icon.svg`. 134 renders.
- The owner reviewed the renders through a private review page and approved part B on
  2026-11-12, choosing tagline proposal 1: "Your own library, on every screen at home." /
  «مكتبتكم، على كل شاشة في البيت.» The other four proposals, their hero mockups and social
  cards were removed (copy/README.md keeps them as the record); a test checks the chosen text is
  the hero's headline and the card's title, and that no other proposal is left. Epic 60 §12
  records Q1, Q3 and Q9 decided; A5 stays open for the owner's own read of the Arabic copy (Q2).
- Problems hit and fixed on the way:
  - Three renders showed screenshot frames filled with black: a tall full-page shot left late
    pictures unpainted. `render.mjs` now waits for every picture to load (naming one that never
    does), decodes it and scrolls it into view before the shot.
  - CodeQL (1 high, 5 medium): the mockup generator wrote our own copy into double-quoted
    attributes without escaping quotes, and stripped tags in one regex pass. `attr()` escapes
    quotes and angle brackets; `plain()` strips tags until none is left.
  - After #1192, the site build publishes `design/fonts.css` and refuses a font URL it does not
    publish; part B's display face stopped the build. `scripts/shell.mjs` now publishes any
    `maktaba-display-*` face it names, hashed.
  - `e2e/cls.spec.ts` failed on two Arabic problem pages at 1280 px (one shift, 0.0016, of the
    header's nav). In the Arabic header the nav sits beside the language switch, whose label
    "English" is Latin; CI's Linux image has no Arial, the fallback's base, so the label was laid
    out in another face, and when the preloaded Latin file arrived inside `font-display:
    optional`'s block period it changed width and slid the nav. main passed only by timing. The
    text fallbacks now also name Liberation Sans (Arial's metric twin) and the display fallback
    Liberation Serif Bold; the layout-shift test is unchanged, and a new test checks each
    fallback names both.
  - The branch, opened on the unmerged #1163, needed main merged in three times.

## What we learned
- `font-display: optional` still swaps a face that arrives inside its block period, so a
  fallback that does not measure like the face can move layout on a machine without the
  fallback's base font. Name a metric twin for every platform, not only Arial.
- A full-page screenshot of a tall page can leave late pictures unpainted; wait for each
  picture, decode it and bring it into view before the shot.

## Keep doing
- Prove a "flaky" CI failure before re-running: here the same value on two pages pointed to one
  geometric cause.
- Remove options that were not chosen rather than mark them, so the tests stay honest.

## Stop doing
- Writing copy into HTML attributes without escaping quotes, even our own copy.

## Open
- The owner's read of the Arabic home, download and front-door copy (Q2, A5).
- The front door takes the chosen tagline next; 60.10 (home) and 60.11 (download) build on part B.
