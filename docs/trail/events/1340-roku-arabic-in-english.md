---
schema: 1
kind: pr
pr: 1340
title: "Roku: shape Arabic inside English lines (Account's language row)"
date: 2026-11-14
session: b26e2555-2e8f-4d71-bcd0-22b0c0d9221f
agent: claude-code
---

## What happened
- #1340 merged as 528ac7130. On Roku's Account screen in English, "Language: العربية" drew the
  Arabic word unjoined and reversed (older than 58.14).
- Root cause: `apps/tv/roku/tools/shape_strings.py` shaped a catalog string only when the catalog was Arabic,
  so Arabic inside an English string reached the Label in logical order. It now shapes every
  string holding a right-to-left letter (left to right in the English catalog).
- The same gap was at runtime: `I18n.Format` shaped only in Arabic, and six callers passed values
  through `ui_Vis` first. In Arabic that kept the whole line in logical order. `Format` now
  shapes in both languages (English only when the line holds Arabic) and takes raw values
  (`ui_Arg`): Account, Start, Find, Compat and the connecting line.

## What we learned
- Presentation forms (U+FE70–FEFF) are outside the shaping table, so `VisualText`/`VisualLines`
  gate any string that holds them and leave it as it is. Shaping a value and then shaping the
  line it sits in silently leaves the rest of the line logical.
- The screens test's pixel compare (tolerance 12 on 480×270) cannot see one wrong word; exact
  string checks in `i18n_test.brs` and `test_shape_strings.py` are what guard shaping.
- `UPDATE_SCREENS=1` rewrites every reference; restore all but the screen meant to change.

## Keep doing
- Decide shaping per string (does it hold Arabic?), never per catalog or per UI language.
- Give `Format` raw values; shape a value only when it is drawn alone.

## Stop doing
- Pre-shaping a value with `ui_Vis` or `VisualText` before `I18n.Format`.

## Open
- Nothing from this fix. A real-device row for an Arabic home name in an English channel (and the
  reverse) would be worth adding to docs/stores/roku.md's checklist.
