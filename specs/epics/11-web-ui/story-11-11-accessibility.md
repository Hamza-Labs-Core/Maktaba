# Story 11.11 — Accessibility (WCAG 2.1 AA)

Every page meets WCAG 2.1 AA contrast, keyboard navigation, focus order,
ARIA semantics, screen-reader compatibility, and reduced-motion support.

**Anchors:** [`architecture.md` §6.2](../../architecture.md). Depends on
Stories 17.1 (tokens), 17.2 (components), 11.8 (theme).

## AC

- All interactive controls have visible focus rings (≥ 3:1 contrast)
  matching the design tokens.
- All images have `alt` attributes; decorative images use `alt=""`.
- Color is never the sole carrier of meaning (state badges include text
  + icon).
- `prefers-reduced-motion` disables non-essential animation.
- Form fields have `<label>` associations; errors announced via
  `aria-live="polite"`.
- Skip-to-content link at the top of every page.
- Player exposes ARIA roles for play/pause/seek and announces time updates
  via `aria-valuetext`.
- Subtitles can be enabled/disabled with a single keyboard action and are
  announced.
- Automated axe-core scan in CI: 0 violations on every page.
- Manual VoiceOver / NVDA pass each release; checklist documented in
  `docs/a11y.md`.

## TC

- Run axe-core on each route: 0 serious or critical violations.
- VoiceOver navigates `/library` end-to-end without trapping or skipping
  posters.
- A user with reduced motion sees no chrome animations on theme change.
- Tab order on `/watch/{id}` is: Header → Player → Transcript sidebar →
  Footer; `Shift+Tab` reverses correctly.
- Color-blind simulator (Protanopia / Deuteranopia / Tritanopia): no UI
  state becomes ambiguous.

## EC

- A third-party widget (player) with a known a11y issue: we wrap it with
  ARIA scaffolding and document the deviation; we do not silently regress.
- Browser zoom 400% (WCAG 1.4.10 reflow): single-column layouts; no
  horizontal scroll except for charts.
- Screen reader on a transcript with 10,000 segments: virtualized list
  exposes only ±20 items at a time but supports `role="feed"` with
  `aria-busy` during fetches.
