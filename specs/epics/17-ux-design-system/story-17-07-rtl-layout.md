# Story 17.7 — Arabic RTL layout system

RTL layout is a baseline, not an after-the-fact mode. Components are
authored direction-agnostic.

**Anchors:** [`architecture.md` §6](../../architecture.md). Depends on
[Story 17.1](story-17-01-design-tokens.md).

## AC

- Logical CSS only: `padding-inline-start`, not `padding-left`.
  `margin-inline-end`, not `margin-right`.
- Icons: every directional icon (chevron, arrow, play) has an
  RTL-flipped variant; a `<DirectionalIcon>` chooses correctly.
- Numbers: configurable via Settings → Advanced (Arabic-Indic vs.
  Western numerals); times always Western for consistency in scrubbers.
- Mixed-direction text: bidi isolates (`unicode-bidi: isolate`)
  required on every span that may contain the opposite script.
- RTL visual regression: every Storybook story has both LTR and RTL
  snapshots.

## TC

- Switch UI to Arabic: every screen flips correctly; no orphaned
  physical-direction CSS.
- Mixed transcript snippet (Arabic + Latin name): names render LTR
  inside an RTL container.
- Player controls in RTL: skip-back is on the right of skip-forward
  (logically next/previous, not physically).

## EC

- A third-party component without RTL support: wrap with `dir="ltr"`
  and document deviation; do not silently break.
- Arabic font fails to load: fall back to system Arabic (Helvetica
  Arabic, Geeza Pro); never to a Latin font that renders Arabic as
  boxes.
- Numerals localization disabled mid-session: re-render number formats.
