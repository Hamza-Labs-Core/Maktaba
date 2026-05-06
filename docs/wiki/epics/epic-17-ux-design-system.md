# Epic 17 — UX Design System

> **Status:** spec + plans complete. **Source:** `specs/epics/17-ux-design-system/`.
> **Anchors:** [`architecture.md` §6](../../../specs/architecture.md), §11.

## Goal

A coherent visual + interaction language across web, mobile, desktop, and TV. Design tokens are the source of truth; components, motion, copy, and screens reference them. RTL is first-class; the system "doesn't have an Arabic mode" — it has an **Arabic baseline that LTR adapts to** where required. Components always respect OS preferences (dark mode, high contrast, reduced motion, Dynamic Type).

## Stories & Plans

| # | Story | Plan | Summary |
|---|-------|------|---------|
| 17.1 | [Design tokens](../../../specs/epics/17-ux-design-system/story-17-01-design-tokens.md) | [plan-17-01](../../../specs/epics/17-ux-design-system/plan-17-01-design-tokens.md) | `design/tokens/tokens.json` (+ dark, high-contrast variants); Style Dictionary pipeline → CSS / Swift / Kotlin / JSON. |
| 17.2 | [Component library](../../../specs/epics/17-ux-design-system/story-17-02-component-library.md) | [plan-17-02](../../../specs/epics/17-ux-design-system/plan-17-02-component-library.md) | React + Storybook + Radix UI primitives; SwiftUI / Compose parity; react-hook-form + zod for forms; Chromatic visual regression. |
| 17.3 | [Motion / animation guidelines](../../../specs/epics/17-ux-design-system/story-17-03-motion.md) | [plan-17-03](../../../specs/epics/17-ux-design-system/plan-17-03-motion.md) | Easing curves + duration tokens; respect `prefers-reduced-motion`; Lottie/Rive for complex micro-interactions. |
| 17.4 | [Loading states / skeletons](../../../specs/epics/17-ux-design-system/story-17-04-loading-states.md) | [plan-17-04](../../../specs/epics/17-ux-design-system/plan-17-04-loading-states.md) | `<Skeleton>` component, pulse animation, line-count variants, streaming placeholders. |
| 17.5 | [Error / empty states](../../../specs/epics/17-ux-design-system/story-17-05-error-empty-states.md) | [plan-17-05](../../../specs/epics/17-ux-design-system/plan-17-05-error-empty-states.md) | `<ErrorState>` + `<EmptyState>` + error boundaries, retry affordances, i18n strings, no silent failures. |
| 17.6 | [Onboarding flow](../../../specs/epics/17-ux-design-system/story-17-06-onboarding.md) | [plan-17-06](../../../specs/epics/17-ux-design-system/plan-17-06-onboarding.md) | First-launch tour: library scan → transcribe primer → settings tour; RTL-aware; per-platform variant. |
| 17.7 | [Arabic RTL layout](../../../specs/epics/17-ux-design-system/story-17-07-rtl-layout.md) | [plan-17-07](../../../specs/epics/17-ux-design-system/plan-17-07-rtl-layout.md) | All components ship LTR + RTL snapshots; CSS logical properties; Storybook RTL preview; Arabic-fallback typography. |
| 17.8 | [Video player controls](../../../specs/epics/17-ux-design-system/story-17-08-player-controls.md) | [plan-17-08](../../../specs/epics/17-ux-design-system/plan-17-08-player-controls.md) | ARIA-labelled controls; play/pause, seek, volume, captions, fullscreen, speed; full keyboard nav. |
| 17.9 | [Search results](../../../specs/epics/17-ux-design-system/story-17-09-search-results.md) | [plan-17-09](../../../specs/epics/17-ux-design-system/plan-17-09-search-results.md) | Result cards (thumbnail, title, snippet, highlights), filters, pagination, empty states. |
| 17.10 | [Processing progress](../../../specs/epics/17-ux-design-system/story-17-10-processing-progress.md) | [plan-17-10](../../../specs/epics/17-ux-design-system/plan-17-10-processing-progress.md) | Real-time progress bar + ETA, status badges, toast notifications, per-item details. |
| 17.11 | [Subtitle / transcript presentation](../../../specs/epics/17-ux-design-system/story-17-11-transcript-presentation.md) | [plan-17-11](../../../specs/epics/17-ux-design-system/plan-17-11-transcript-presentation.md) | Scrollable transcript with speaker labels, click-to-seek, editing UX, export (PDF/SRT/VTT), search-within. |

## Key technical decisions

- **Single source of truth (tokens).** `design/tokens/tokens.json` is canonical. Every platform regenerates from one Style Dictionary build. No manual duplication anywhere.
- **RTL-first.** Arabic baseline documented in `design/docs/rtl.md`. Every component ships LTR + RTL snapshots in Storybook. No "Arabic mode toggle" — the system is culturally aware by default.
- **Token build pipeline (Style Dictionary 4.x):**
  - Web: `web/src/styles/tokens.css` (CSS custom properties)
  - tvOS: `apps/tvos/Sources/UI/Generated/Tokens.swift`
  - Android TV: `apps/androidtv/src/main/.../Generated/Tokens.kt` (Compose colors)
  - Capacitor mobile: `apps/mobile/plugins/tokens/dist/tokens.json`
- **Design-tokens versioning.** `design/tokens/package.json` enforces SemVer (major = breaking, minor = additive, patch = visual). Clients pin major version; CI enforces alignment.
- **Storybook as contract.** Every shipped component has: a story per variant/size combination, LTR + RTL snapshots, light + dark snapshots, A11y AA audit, Chromatic visual-regression gating.
- **Respect OS preferences.** `prefers-color-scheme`, `prefers-contrast`, `prefers-reduced-motion`, high-DPI, iOS Dynamic Type. No forced light or animations if the user has disabled them.
- **No prose in code.** All user-visible strings live in i18n tables (`api/internal/i18n/locales/{en,ar}.toml`). JSX/Swift/Kotlin contain only structure and logic.
- **Component escape hatch.** Every component accepts `className`, but defaults to tokens. No hard-coded colors or padding.

## Migrations

This epic ships no SQL DDL. (`onboarding_state(user_id, completed_steps, dismissed_at)` is optional and may be deferred or persisted as JSON in users prefs.)

## Dependencies

- **None inbound.** Stories 17.1 and 17.2 are foundational and block every UI epic. Start with these before any client work.

## Downstream consumers

- Epic 11 (Web UI), Epic 12 (Mobile via Capacitor), Epic 13 (Desktop via Tauri), Epic 14 (TV).
- Token variables consumed by every mockup under `web/mockups/`.

## Related mockups

`web/mockups/components/` (theme component library, 10 files, commit `9db892b`).

## Out of scope

- Brand identity (logo, marketing site) — separate effort.
- Marketing motion / hero animations — separate motion budget.
- Print stylesheets beyond "renders something legible".
- A11y compliance for third-party video players (we wrap with our own accessible controls).

## See also

- [Epic 14 — TV Apps](epic-14-tv-apps.md) (TV-specific tokens and 10-foot UI build on the same pipeline).
- [Glossary](../glossary.md) — design token, semantic token, Style Dictionary, RTL baseline, CSS logical properties, Storybook, Chromatic, reduced motion, focus engine, i18n table.
