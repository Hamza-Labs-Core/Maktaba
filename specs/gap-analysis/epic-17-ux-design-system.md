# Epic 17 — UX Design System: Spec-vs-Implementation Gap Analysis

**Verdict:** Skeleton only. Story 17.1 (tokens) is ~70% real and behaviorally
verified; Story 17.2 (components) ships 2 of 26 required primitives; Stories
17.3–17.11 (motion, loading, error/empty, onboarding, RTL, player, search,
processing, transcript) are entirely unimplemented in any product code. ~6 of
~70 ACs complete.

---

## Evidence base

- Design system root: `web/design-system/` (NOT `design/` as every plan
  specifies — see structural gap below).
- Components present: `web/design-system/components/Button/Button.tsx`,
  `web/design-system/components/ThemeProvider/ThemeProvider.tsx`,
  `web/design-system/components/index.ts` (only these two exported).
- Token sources: `web/design-system/tokens/{tokens,tokens.dark,tokens.high-contrast}.json`.
- Token build: `web/design-system/build/build-tokens.mjs` → emits
  `build/dist/{tokens.css,tokens.ts,tokens.json}`. Verified: `node build-tokens.mjs`
  emits "81 tokens + 2 theme overrides"; `verify-tokens.mjs` passes.
- Web app pages (`web/src/pages/*.tsx`) are Phase-10 Story-11.x scaffolds
  (raw `<video controls>`, `<table>`, `<progress>`), not Epic-17 presentation
  components. They consume the *hand-written* `web/src/styles/tokens.css`, NOT
  the generated design-system output.

---

## Story 17.1 — Design tokens

| AC | Status | Evidence / Gap |
|---|---|---|
| Token domains: color, typography, spacing, radius, elevation, motion, z-index, breakpoints | **complete** | All 8 groups present in `tokens/tokens.json:1-122`. |
| Color: brand + semantic (`bg/fg/accent/success/warn/error`), light+dark | **complete** | `tokens.json:3-40`; dark overrides `tokens.dark.json`; emitted to `build/dist/tokens.css:8-98`. |
| Typography: 4 roles (display/body/mono/transcript), Arabic + Latin font, fallback stack | **complete** | `tokens.json:41-68`; Arabic family + fallback at `tokens.json:64-67`. |
| Spacing: 4px base; 4,8,12,16,24,32,48,64,96 scale | **complete** | `tokens.json:69-80` (keyed 1..24). |
| Single source of truth at `design/tokens/tokens.json`; pipeline generates **four** outputs (CSS, JSON, Swift, Kotlin) | **partial** | Source lives at `web/design-system/tokens/` not `design/tokens/` (plan-17-01 §0). Pipeline emits **only CSS/TS/JSON** (`build-tokens.mjs:248-276`). **No Swift, no Kotlin output** — plan-17-01 §2 mandates `swift/ios`, `swift/tvos`, `kotlin/androidtv` Style-Dictionary platforms. Story AC + TC ("Generate the Swift output: compiles with the tvOS target") unmet. |
| Versioned: token bump bumps semver; clients pin major | **stub** | `package.json` is static `0.1.0`; no semver-bump enforcement, no `tokens.deprecated.json` alias mechanism (plan-17-01 §4), no `.github/workflows/tokens.yml`. |
| TC: change brand color → all 5 targets rebuild | **partial** | Only CSS/TS/JSON rebuild; iOS/Android/tvOS/AndroidTV do not exist. |
| TC: dark→light theme switch resolves per set | **complete** | `tokens.css:92-109` data-theme selectors; `verify-tokens.mjs:62-91` asserts resolution. |
| EC: missing native token → build fails loud | **complete (web only)** | `build-tokens.mjs:103-121` throws on unresolved ref / cycle / group-ref. No native compilers exist to enforce the native half. |
| EC: high-contrast OS mode overrides via `tokens.high-contrast.json` | **partial** | The override JSON exists and emits `[data-theme="high-contrast"]` (`tokens.css:100-109`), but nothing wires OS `prefers-contrast: more` to set that attribute. `grep prefers-contrast` across `web/` = 0 hits. The override is unreachable from an OS preference. |
| EC: token rename → deprecated alias one major | **missing** | No alias/deprecation shim anywhere. |

**Note:** `package.json` `test` script points at non-existent
`build/build-tokens.test.mjs` (actual file is `verify-tokens.mjs`) — `npm test`
in `web/design-system` fails with MODULE_NOT_FOUND. CI must invoke verify-tokens
directly to be green.

## Story 17.2 — Component library

| AC | Status | Evidence / Gap |
|---|---|---|
| 26 components (Button…ContextMenu) | **missing** | Only Button + ThemeProvider exist (`components/index.ts:11-15`). 24 of 26 absent: IconButton, Link, Input, Textarea, Select, Combobox, Toggle, Checkbox, Radio, Card, Modal, Drawer, Toast, Tooltip, Tabs, Pagination, ProgressBar, Skeleton, EmptyState, ErrorState, Avatar, Badge, Chip, Menu, ContextMenu. `index.ts:7-9` admits "each remaining component lands in its own follow-up PR." |
| Button: 5 variants × 3 sizes | **complete** | `Button.tsx:10-59`; CSS classes `mk-btn--{variant/size}`. |
| Every component documented in Storybook w/ a11y | **partial** | Only `Button.stories.tsx` exists. Storybook config (`storybook/.storybook/main.ts`) globs `../../components/**/*.stories.@(ts|tsx)` — 1 story file. `preview.ts:8` imports `@maktaba/design-system/dist/css/tokens.css` but build emits to `build/dist/tokens.css` — **path mismatch, Storybook would fail to resolve token CSS**. |
| className escape hatch, token defaults | **complete (Button)** | `Button.tsx:31,45-52` merges `className`. |
| `<Form>` wrapper: react-hook-form + zod, i18n errors | **missing** | No `Form/`, no react-hook-form/zod dep in `package.json`. |
| Native TV counterparts (SwiftUI/Compose) + visual-regression | **missing** | No `apps/tvos/.../Components/`, no Compose components, no Chromatic. |
| TC: loading button shows spinner & unfocusable | **complete** | `Button.tsx:39,43-44,54` (`disabled` forced, `aria-busy`, inline Spinner). |
| TC: modal traps focus, closes on Esc | **missing** | No Modal. |
| TC: TV Card focus grows 4% | **missing** | No Card, no TV. |
| TC: render Storybook in CI, visual diffs gate | **missing** | No Chromatic/CI wiring; only Button story. |
| EC: card child overflow → scrollbar | **missing** | No Card. |
| EC: outside ThemeProvider → dev warn / prod default | **complete** | `ThemeProvider.tsx:39-49` `useTheme` warns in dev, returns system fallback. |
| EC: heavy component lazy-loaded w/ skeleton fallback | **missing** | No Skeleton, no lazy DataTable. |

## Story 17.3 — Motion / animation

| AC | Status | Evidence / Gap |
|---|---|---|
| Duration scale 100/150/250/400/600ms | **missing (mismatch)** | `tokens.json:94-100` defines `instant/quick/base/slow` = `0/120/200/320ms`, NOT the spec's `100/150/250/400/600ms` named `instant/quick/standard/relaxed/theatrical`. No `motion.pattern` group (plan-17-03 §1 requires page-transition/modal/toast/focus-ring patterns). |
| Easings easeOut/easeIn/easeInOut | **complete (tokens)** | `tokens.json:101-105`. |
| Patterns (page xfade, modal scale+fade, toast, focus-ring) | **missing** | No `motion/` primitives (`useMotion.ts`, `patterns.ts`, `withReducedMotion.tsx`); plan-17-03 §2-3 unimplemented. |
| All motion respects prefers-reduced-motion | **partial** | Only the hand-written `web/src/styles/tokens.css:73` has a `@media (prefers-reduced-motion: reduce)` block; no `useReducedMotion` hook; JS-driven motion uncovered. |
| No spring for layout; lint gate | **missing** | No lint rule (`LintBlocksSpringOutsidePlayer`). |
| All TC/EC (reduced-motion modal, low-end clamp, etc.) | **missing** | No primitives to test against. |

## Story 17.4 — Loading states & skeletons

| AC | Status | Evidence / Gap |
|---|---|---|
| Skeleton shape-match, ≥200ms min, 5s→spinner | **missing** | No Skeleton component. |
| Spinner only for action waits | **partial** | Button has an inline spinner only; pages use plain `<p>{t("common.loading")}</p>` (`VideoPlayer.tsx:43`, `ProcessingQueue.tsx:55`). |
| Pagination 6 skeleton rows / search shimmer / player buffer spinner | **missing** | None present. |
| All TC/EC | **missing** | — |

## Story 17.5 — Error & empty states

| AC | Status | Evidence / Gap |
|---|---|---|
| Error classified network/server/permission/not_found/validation w/ illustration+copy | **missing** | Pages render a single generic `<div className="mkt-alert" role="alert">{err}</div>` (`Search.tsx:76-80`, `VideoPlayer.tsx:38-42`); no classification, illustration, or copy templates. No `ErrorState` component. |
| Empty classified first_run/filtered_out/cleared w/ CTA | **missing** | Generic `<p className="mkt-empty">{t("common.empty")}</p>` only (`Search.tsx:81`, `ProcessingQueue.tsx:56`); no CTA, no `EmptyState` component. |
| Tone guideline / sticky toasts / idempotent retry dedupe | **missing** | No Toast, no retry/idempotency dedupe layer. |
| All TC/EC | **missing** | — |

## Story 17.6 — Onboarding flow

| AC | Status | Evidence / Gap |
|---|---|---|
| 4-step wizard (password, library, STT, language/theme), skip, progress bar, back-arrow, tour carousel | **missing** | `grep -ri onboard\|wizard web/src` = 0 hits. No wizard component, no resume-setup banner, no tour carousel. Entirely absent. |
| All TC/EC | **missing** | — |

## Story 17.7 — Arabic RTL layout

| AC | Status | Evidence / Gap |
|---|---|---|
| Logical CSS only (`padding-inline-start`) | **unverified/missing** | No design-system component CSS beyond `button.css`; no audit/lint forbidding physical-direction props. |
| `<DirectionalIcon>` w/ RTL-flipped variants | **missing** | `grep DirectionalIcon` = 0 hits. |
| Numeral localization (Arabic-Indic vs Western) setting | **missing** | No setting / formatter. |
| Bidi isolates on mixed-script spans | **missing** | `grep unicode-bidi\|isolate` = 0 hits in product code. |
| Every Storybook story has LTR+RTL snapshots | **partial** | `preview.ts:18-66` defines a `direction` toolbar global + decorator setting `dir`; only Button's `ArabicRTL` story uses it (`Button.stories.tsx:42-45`). 1 component → not "every story". |
| All TC/EC | **missing** | — |

## Story 17.8 — Video player controls

| AC | Status | Evidence / Gap |
|---|---|---|
| Auto-hide 3s, scrubber chapter ticks/sprite/buffered, captions cycle, settings menu, 44px touch, TV variant, subtitle styling persisted, mini-player | **missing** | `VideoPlayer.tsx:50-61` is a raw `<video controls>` element with native browser chrome. No custom control bar, scrubber, sprite preview, settings menu, subtitle styling, or mini-player. Comment at `VideoPlayer.tsx:1-6` confirms "full implementation ships later." |
| All TC/EC | **missing** | — |

## Story 17.9 — Search results presentation

| AC | Status | Evidence / Gap |
|---|---|---|
| Result group (poster/title/flag/duration + hit count + 3 snippets) | **stub** | `Search.tsx:82-99` renders `<li>` with title + single snippet; no poster, flag, duration, hit count, or 3-snippet grouping. |
| Snippet ≤160ch, `<mark>` highlight, ellipsis | **missing** | Raw `{h.snippet}` text, no `<mark>`/truncation. |
| Timestamp chip `[06:12]` clickable | **partial** | Link carries `?t=start_sec` (`Search.tsx:87-91`) but no formatted `[mm:ss]` chip UI. |
| Facet sidebar / "Why this result" / sort modes | **missing** | None present. |
| All TC/EC | **missing** | — |

## Story 17.10 — Processing progress visualization

| AC | Status | Evidence / Gap |
|---|---|---|
| Bar segments done/current/pending color-coded | **stub** | `ProcessingQueue.tsx:77-81` uses native `<progress max=100>`; no segmented bar. |
| Audio-time annotation `01:23:17 / 04:12:04 (33%)` | **missing** | Only `progress_pct`; no audio-time. |
| ETA after 3 segments | **missing** | — |
| Stage icon strip (scan→…→thumbnail) highlighted | **missing** | Plain text `<td>{j.stage}</td>` (`ProcessingQueue.tsx:73`). |
| Pause/resume/cancel/Force-Pause inline; hover tooltip | **missing** | No controls. |
| All TC/EC (indeterminate stripes, model-upgraded hint, duration-changed warn) | **missing** | — |

## Story 17.11 — Subtitle & transcript presentation

| AC | Status | Evidence / Gap |
|---|---|---|
| Transcript sidebar w/ `[mm:ss]`, speaker badge, live highlight | **missing** | `grep -ri transcript web/src` = 0 hits. No transcript component. `VideoDetail.tsx` does not render a transcript. |
| Click segment → seek; Cmd/Ctrl+F find bar; inline subtitle styling; auto-scroll toggle; copy actions | **missing** | None present. |
| All TC/EC (silence placeholder, bidi isolate, 50k virtualized) | **missing** | — |

---

## AC counts by status (approx, ~70 ACs across 11 stories)

- **complete:** ~6 (mostly 17.1 token domains/color/type/spacing/theme-switch + Button variants/loading + ThemeProvider warn)
- **partial:** ~7 (17.1 SSOT/high-contrast wiring, 17.2 storybook, 17.3 reduced-motion, 17.7 storybook RTL, 17.9 timestamp/group)
- **stub:** ~3 (17.1 versioning, 17.9 result group, 17.10 progress bar)
- **missing:** ~54 (all of 17.4/17.5/17.6/17.8/17.11 + most of 17.2/17.3/17.7/17.9/17.10)

---

## Top gaps by impact

1. **Component library is 2/26 (Story 17.2).** The entire foundational
   primitive set — Input, Select, Modal, Drawer, Toast, Tooltip, Tabs,
   Skeleton, EmptyState, ErrorState, Card, etc. — does not exist. Every
   downstream UI epic (11/12/13/14) depends on this and the README declares
   17.1/17.2 "block every UI epic." `index.ts:7-9` openly defers them.
   `<Form>` (react-hook-form+zod) and native TV parity also absent.

2. **No native token outputs (Story 17.1).** Pipeline emits only CSS/TS/JSON;
   the Swift (tvOS/iOS) and Kotlin (AndroidTV) Style-Dictionary platforms in
   plan-17-01 §2 are unimplemented. "Single source of truth → four target
   outputs" is half-built; TV/mobile apps cannot consume tokens.

3. **High-contrast / reduced-motion never wired to OS preference (17.1/17.3).**
   `tokens.high-contrast.json` emits a `[data-theme="high-contrast"]` block but
   nothing reads `prefers-contrast: more` to apply it (0 grep hits); the only
   `prefers-reduced-motion` rule is in the hand-written legacy
   `web/src/styles/tokens.css`, decoupled from the design system. The accessible
   token sets exist but are unreachable from real OS settings.

4. **Stories 17.4–17.11 are zero-implementation in product code.** Onboarding
   wizard, RTL DirectionalIcon/bidi system, player control bar, transcript
   sidebar, segmented processing bar, and rich search results have no code;
   `web/src/pages/*` are raw Story-11.x scaffolds that don't even consume the
   generated design-system tokens (they use the hand-written legacy CSS).

5. **Structural drift + broken test wiring.** Every plan specifies `design/`
   and Style Dictionary 4.x; the implementation is a bespoke `web/design-system/`
   builder. `web/design-system/package.json` `test` points at a non-existent
   `build-tokens.test.mjs` (real file `verify-tokens.mjs`), and `preview.ts`
   imports a wrong token-CSS path (`dist/css/tokens.css` vs actual
   `build/dist/tokens.css`) — Storybook token styling would not resolve.
