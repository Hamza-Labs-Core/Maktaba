# Epic 17 — UX Design System

**Goal.** A coherent visual + interaction language across web, mobile,
desktop, and TV. Design tokens are the source of truth; components,
motion, copy, and screens reference them. RTL is first-class; the
system "doesn't have an Arabic mode", it has an Arabic baseline that
LTR adapts to where required.

**Anchors:** [`architecture.md` §6](../../architecture.md), §11.

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 17.1 | [Design tokens](story-17-01-design-tokens.md) | skeleton landed (slot P3) |
| 17.2 | [Component library](story-17-02-component-library.md) | skeleton landed (slot P3) |
| 17.3 | [Motion / animation guidelines](story-17-03-motion.md) | spec |
| 17.4 | [Loading states & skeleton screens](story-17-04-loading-states.md) | spec |
| 17.5 | [Error states & empty states](story-17-05-error-empty-states.md) | spec |
| 17.6 | [Onboarding flow](story-17-06-onboarding.md) | spec |
| 17.7 | [Arabic RTL layout system](story-17-07-rtl-layout.md) | spec |
| 17.8 | [Video player controls design](story-17-08-player-controls.md) | spec |
| 17.9 | [Search results presentation](story-17-09-search-results.md) | spec |
| 17.10 | [Processing progress visualization](story-17-10-processing-progress.md) | spec |
| 17.11 | [Subtitle & transcript presentation](story-17-11-transcript-presentation.md) | spec |

---

## Dependencies

- **None** — Stories 17.1 and 17.2 are foundational and block every UI
  epic. Start with these before any client work.

## Cross-cutting checklist

- **Single source of truth:** `design/tokens/tokens.json` builds web /
  iOS / Android / TV outputs. No hand-rolled token tables.
- **Storybook is the contract:** every component shipped in 17.2 has a
  Storybook story, an a11y note, an LTR snapshot, and an RTL snapshot.
- **Respect OS preferences:** dark mode, reduced motion, forced colors,
  high contrast, Dynamic Type.
- **No prose in JSX/Swift/Kotlin:** all user-visible strings via i18n
  table.

## Out of scope

- Brand identity (logo, marketing site) — separate effort.
- Marketing motion / hero animations — separate motion budget.
- Print stylesheets beyond "renders something legible".
