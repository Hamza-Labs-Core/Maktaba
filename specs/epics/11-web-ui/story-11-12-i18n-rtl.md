# Story 11.12 — i18n (Arabic RTL + English LTR)

The same React app ships in Arabic and English at parity; adding a third
locale requires only translation files, not code changes.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Story 17.7 (RTL layout system).

## AC

- Locale detection: URL `/ar/...` or `/en/...` if present, else
  `Accept-Language`, else `en` (configurable per server).
- All strings live in `web/src/i18n/{locale}.json`; no string is inlined
  in JSX.
- Arabic uses `dir="rtl"` and Arabic numerals by default (configurable).
- All layouts are mirrored under RTL: navigation chevrons flip, padding
  asymmetries flip, scrollbars are on the appropriate side.
- Date / time / number formatting via `Intl.DateTimeFormat` /
  `Intl.NumberFormat` with the active locale.
- Mixed-direction strings render with `unicode-bidi: isolate` and use
  Unicode bidi-isolate characters (`⁨...⁩`) where escapes are needed in
  templates.
- Transcript snippets in search results render in their source language
  even when the UI is in the opposite direction.
- Pluralization handled via ICU MessageFormat (`{count, plural, one {…}
  other {…}}`).

## TC

- Switch UI to Arabic: the entire shell flips RTL; no element is
  visually clipped.
- Search in Arabic for English content: hits render LTR snippets inside
  an RTL container without bleeding direction.
- Number formatting: `1234567` displays as `1٬234٬567` in Arabic locale,
  `1,234,567` in English.
- Translation key missing in Arabic: falls back to English with a console
  warning in dev; no broken UI.

## EC

- A translation expands by 70% (German placeholder for tests): layouts use
  `min-content` / `auto` and don't truncate.
- Arabic fonts not loaded yet: use a `font-display: swap` fallback that
  renders correctly in both directions.
- A right-aligned scrollbar in RTL conflicting with player chrome: chrome
  is anchored to logical-end, not physical-right.
