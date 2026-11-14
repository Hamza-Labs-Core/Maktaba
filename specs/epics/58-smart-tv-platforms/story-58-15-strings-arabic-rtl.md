# Story 58.15 — English and Arabic on every TV: catalogs, mirroring, fonts and Roku's visual text

> Epic 58 · Phase 1 (catalog checks) and Phase 4 (Roku) · Depends on: — · Needed by: 58.12, 58.13 and every story with strings

## Why

Every user-facing string ships in English and Arabic, and every screen mirrors in Arabic. That
already holds for the Android TV and Apple TV apps (`values-ar`, String Catalogs) and for the
web. The new platforms differ:
- **Fire TV** uses the Android TV app's catalogs as they are.
- **Samsung, LG and the Cast receiver** are web pages, which handle Arabic and bidi well. They
  need catalogs, a check that no key lacks its Arabic, and fonts bundled, since TV system fonts
  vary.
- **Roku does not support right-to-left text** (research.md §6.4). Arabic letters must be shaped
  into their joined forms and put in visual order before Roku draws them, and the layout must be
  mirrored by hand.

## Description

**One check for every catalog.** `tools/i18n/check_tv_catalogs.py` (new) runs in each platform's
CI job and fails on:
- a key present in English and missing in Arabic, or the reverse;
- placeholders that differ (`%1$s` in one, missing in the other);
- an Arabic string with Latin-only words that are not product names or code.

It covers `apps/tv/web/i18n`, `web/packages/castreceiver/i18n` and `apps/tv/roku/i18n`. The
Android and Apple catalogs keep their existing checks (Android lint, `check-string-catalogs.py`).

**Mirroring, by platform:**

| Platform | How |
|---|---|
| Fire TV / Android TV | as today: `supportsRtl`, `values-ar`, Compose's layout direction |
| `/tv/` (Samsung, LG) and `/big` | `dir="rtl"` on `<html>`, logical CSS properties (`margin-inline-start`), `bigscreen`'s focus engine swaps Left and Right; the launcher too |
| Cast receiver | `dir="rtl"` by the sender's `lang`; logical CSS |
| Roku | the channel's own layout direction flag: every screen's components are placed by a layout helper that reads it and mirrors x positions, row order and the focus map; `RowList` and `PosterGrid` are given reversed content and start focus at the right |

**Numbers, times and Latin inside Arabic.** Times (`12:34`), episode codes (`S2 E5`) and
resolutions (`4K`) are wrapped in LRM marks or `<bdi>` on the web, so they read left to right
inside Arabic text. Tier labels follow Epic 57 (‎+13 with an LRM).

**Fonts.**
- **Web TVs and the receiver** bundle IBM Plex Sans Arabic (text) and Amiri (headings) from
  `@fontsource`, the web's own fonts, in WOFF2 (SIL OFL; licence files in the package). Samsung
  supports custom fonts on 2014+ models (research.md §3.6). LG gets the same files.
- **Roku** bundles IBM Plex Sans Arabic as TTF in `pkg:/fonts/`, with Latin glyphs included, used
  for every label so English and Arabic share one font. Roku's system font has no Arabic, which
  is why Arabic subtitles show as boxes without a bundled font (research.md §6.4).

**Roku's visual text.** Arabic must reach Roku's `Label` as already-shaped glyphs in left-to-right
display order:
- **Static strings** are shaped at build time. The build step reads `i18n/ar.json` and runs:
  - the Unicode Bidirectional Algorithm (UAX #9, through `python-bidi`);
  - Arabic shaping to presentation forms (`arabic-reshaper`, limited to the lam-alef
    ligatures), then the L4 mirroring that python-bidi leaves to the renderer and Roku's renderer
    does not do (the reference is `apps/tv/roku/tools/visual.py`).

  It writes Roku's `translations` file with the visual strings. Line breaking happens before
  reordering: the build step wraps each string to the label's width in characters, given in the
  key's metadata, then reorders each line.
- **Text from the home** (titles, descriptions, names, Arabic subtitle cues) is shaped on the
  device by `VisualText(s, maxWidthChars)` in `source/visualtext.brs`, a BrightScript port of the
  same steps, with tables generated from the reference (`gen_tables.py`), so the data cannot
  drift:
  - joining-type tables for the Arabic block (U+0600–U+06FF) and the presentation forms B
    (U+FE70–U+FEFF), with lam-alef ligatures;
  - a one-line UAX #9 pass (W1–W7, N0 bracket pairs, N1–N2, I1–I2, L1, L2, L4) covering Arabic,
    Latin, European and Arabic-Indic digits, and neutral punctuation, which is what Maktaba's
    titles hold.
  - Tashkeel is dropped: Roku has no mark positioning.
- Text outside those scripts (Persian or Urdu letters beyond the table, Hebrew, ZWJ/ZWNJ,
  embedding controls) falls back to the logical order with a log line, never a crash.
- `VisualText` is checked against the Python reference on a shared vector file
  (`shared/i18n/visual-vectors.json`, 312 cases of joining, ligatures, tashkeel, digits, times,
  episode codes, mixed runs, punctuation mirroring, width cuts, wrapping, the fallback and real
  titles, written from the reference by `gen_vectors.py`). A difference fails CI.
- The reading order is also held across clients: `shared/i18n/visual_order.mjs` reads each
  character's drawn position in `/tv/` and the Cast receiver, en and ar, and compares it with the
  NFKC fold of Roku's visual string for the same vector.
- Long descriptions are shaped once per screen and cached. Shaping cost is measured on a Roku
  Express, and the budget is 50 ms per screen. Printable ASCII on a line that is not forced right
  to left skips the steps (each character is its own glyph, in logical order).

**Dark theme.** Every TV is dark by default (Epic 48's Ink & Brass tokens): the web TVs through
the web's tokens, Roku through a generated `theme.brs` from the same token file. Screenshots in
CI are taken dark. Light theme is not offered on TVs, as on Android TV and Apple TV.

## Acceptance criteria

- **Given** a key added to `apps/tv/web/i18n/en.json` without Arabic, **then** CI fails naming the
  key.
- **Given** an Arabic title with a number and an English word ("الحلقة 3 — Intro"), **then**
  Roku, `/tv/` and the receiver all show it in the same reading order, with joined letters.
- **Given** Arabic as the language on Roku, **then** rows start at the right, Left moves forward,
  and focus starts at the right-most card.
- **Given** a Persian title with letters outside the table on Roku, **then** it shows in logical
  order and the channel logs it, without crashing.

## Test cases

| ID | Type | Setup | Action | Expected |
|---|---|---|---|---|
| T01 | Python unit | `check_tv_catalogs.py` fixtures | missing key, placeholder mismatch | fails, naming the entry |
| T02 | Python unit | the Roku build step | shape `ar.json` | visual strings equal the reference; wrapped lines reordered per line |
| T03 | brs-engine unit | `VisualText` on `visual-vectors.json` | each vector | equals the Python reference |
| T04 | brs-engine timing | 20 long descriptions | shape | `tests/timing_test.brs` times the 20 at their best of 5 runs next to a reference loop of 100,000 additions timed the same way, and CI fails when they cost more than 3.5 loops. They cost about 2.5 (207 ms against 83 ms in the Linux container; 4.0 and 331 ms before the hot paths were reworked): a ratio, because brs-engine's speed changes from one machine to the next. brs-engine's own time stays above 50 ms, so the 50 ms budget is held on the device: the device-test package prints the same line on a Roku Express (`docs/stores/roku.md` row 10) |
| T05 | Playwright (`/tv/`, launcher, receiver) | ar | every screen | `dir=rtl`, focus order mirrored, LRM-wrapped times; vector `episode.06` in the same reading order as Roku (`visual_order.mjs`); screenshots dark |

## Strings

This story adds no user-facing strings of its own. It is the checks and the shaping.

## Edge cases

- **Mixed-direction search input on Roku**: `MiniKeyboard` input is shown through `VisualText`
  as it is typed. The query sent to the home is the logical string.
- **A string too long for its label after shaping**: the build step fails for static strings
  (shown in CI). Text from the home is truncated with an ellipsis at the visual end, which is
  the left in Arabic.

## Build plan

| File | Change |
|---|---|
| `tools/i18n/check_tv_catalogs.py` (new) | the catalog check |
| `apps/tv/roku/tools/shape_strings.py` (new) | build-time shaping |
| `apps/tv/roku/tools/visual.py`, `gen_vectors.py`, `gen_tables.py` (new) | the reference, the vectors' expected output, the device tables |
| `apps/tv/roku/source/visualtext.brs`, `visualtext_tables.brs` (new) | device shaping |
| `shared/i18n/visual-vectors.json` (new) | vectors |
| `apps/tv/roku/source/layout.brs` (new) | the mirroring helper, `LayoutNew(rtl, width)` |
| `shared/i18n/visual_order.mjs` (new) | the cross-client reading-order check |
