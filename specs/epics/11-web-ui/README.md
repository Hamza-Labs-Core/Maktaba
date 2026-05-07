# Epic 11 — Web UI (React/Next.js PWA)

**Goal.** A single React 18 + TypeScript + Vite SPA that runs as the
canonical web client, gets installable as a PWA on iOS / Android / desktop,
and is the same code that ships inside the Capacitor (mobile) and Tauri
(desktop) shells. RTL-first for Arabic, fully keyboard-navigable, WCAG 2.1
AA, and offline-capable for everything except video bytes.

**Anchors:** [`architecture.md`](../../architecture.md) §6.2 (web stack), §9
(REST + GraphQL + WS), §2.1.

---

## Stories

| # | Story | Status |
|---|-------|--------|
| 11.1 | [Library browser (grid/list, sort, filter)](story-11-01-library-browser.md) | spec |
| 11.2 | [Video detail page](story-11-02-video-detail-page.md) | spec |
| 11.3 | [Video player](story-11-03-video-player.md) | spec |
| 11.4 | [Search interface](story-11-04-search-interface.md) | spec |
| 11.5 | [Processing queue dashboard](story-11-05-processing-queue-dashboard.md) | spec |
| 11.6 | [Settings page](story-11-06-settings-page.md) | spec |
| 11.7 | [Responsive design](story-11-07-responsive-design.md) | spec |
| 11.8 | [Dark / light theme](story-11-08-dark-light-theme.md) | spec |
| 11.9 | [Keyboard shortcuts](story-11-09-keyboard-shortcuts.md) | spec |
| 11.10 | [Offline (PWA service worker)](story-11-10-offline-pwa.md) | spec |
| 11.11 | [Accessibility (WCAG 2.1 AA)](story-11-11-accessibility.md) | spec |
| 11.12 | [i18n (Arabic RTL + English LTR)](story-11-12-i18n-rtl.md) | spec |
| 11.13 | [API: Personal Access Tokens (PAT)](story-11-13-pat-management-api.md) | spec (added per REVIEW §3.4) |
| 11.14 | [API: active session listing & revocation](story-11-14-active-sessions-api.md) | spec (added per REVIEW §3.4) |

---

## Dependencies

- **Epic 17** (Design System) Stories 17.1, 17.2 must land first — every
  component and layout is token-driven.
- **Epic 7** (API) Stories 7.1, 7.2, 7.4, 7.6, 7.8, 7.10, 7.11, 7.13,
  7.16, 7.17 — all CRUD, search, sessions, watch progress, queue stats,
  WebSocket fan-out, and GraphQL endpoints.
- **Epic 8** (Streaming) Stories 8.1, 8.5, 8.11, 8.12, 8.13 — manifests,
  HLS, subtitles, chapters, posters/sprites.
- **Epic 10** (Auth) Stories 10.1–10.5, 10.7, 10.8 — login, refresh,
  logout, signed-URL minter.
- **Epic 16** Story 16.6 — feature-flag layer for tier-gated UI.

## Cross-cutting checklist (one-page sanity sweep)

- **API contract:** every story consumes only documented endpoints from
  [`architecture.md` §9](../../architecture.md). New endpoints require a §9
  amendment **and** an owning story (see Stories 11.13, 11.14).
- **GraphQL types:** every TypeScript client uses generated types from
  `shared/graphql/schema.graphql`. No hand-rolled types.
- **WebSocket fan-in:** subscriptions live in `lib/ws.ts`; not duplicated
  per page.
- **i18n:** no string in JSX. All through the i18n table.
- **Tokens:** no hard-coded color, font, or spacing.
- **A11y:** every interactive control keyboard-reachable; every page
  passes axe-core; every motion respects reduced-motion.
- **Telemetry:** strictly opt-in (Epic 16); never shipping content text or
  filenames.
- **Performance budgets:**
  - First contentful paint ≤ 1.5 s on a 50 Mbps LAN, cold cache.
  - Search p50 ≤ 250 ms, p95 ≤ 500 ms on the 100k-segment fixture
    (aligned with Epic 7 Story 7.8 & Epic 18 Story 18.1; see
    [REVIEW §1.4.d](../../REVIEW.md)).
  - Player join time ≤ 2 s direct, ≤ 4 s transcoded.
  - PWA shell ≤ 350 KB gzipped.
- **Security:** no secret in `localStorage`; refresh tokens in `httpOnly`
  cookies (web). PAT issuance and storage covered by Story 11.13.

## Out of scope

- Live ingestion (architecture Appendix B).
- DRM-protected content.
- Multi-tenant SaaS hosting.

## Open questions

1. **Mini-player across routes.** Persist the Vidstack player instance
   across React Router transitions, or always re-create with a saved
   position? Decision affects [Story 17.8](../17-ux-design-system/story-17-08-player-controls.md).
