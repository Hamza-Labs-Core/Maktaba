# Epic 11 — Web UI (React + Vite PWA)

> A single React 18 + TypeScript + Vite SPA that runs as the canonical web client, gets installable as a PWA on iOS / Android / desktop, and is the same code that ships inside the Capacitor (mobile) and Tauri (desktop) shells. RTL-first for Arabic, fully keyboard-navigable, WCAG 2.1 AA, offline-capable for everything except video bytes.

- **Spec README:** [`specs/epics/11-web-ui/README.md`](../../../specs/epics/11-web-ui/README.md)
- **Architecture anchors:** §6.2 (web stack), §9 (REST + GraphQL + WS), §2.1
- **Source bundle for:** [Epic 12 Mobile](epic-12-mobile.md) (Capacitor) and [Epic 13 Desktop](epic-13-desktop.md) (Tauri)
- **Out of scope:** live ingestion (architecture Appendix B), DRM-protected content, multi-tenant SaaS hosting.

## Stories & Plans

| #     | Story                                                         | Plan                                                       | Status |
|-------|---------------------------------------------------------------|------------------------------------------------------------|--------|
| 11.1  | [Library browser (grid/list, sort, filter)](../../../specs/epics/11-web-ui/story-11-01-library-browser.md) | [plan](../../../specs/epics/11-web-ui/plan-11-01-library-browser.md) | spec |
| 11.2  | [Video detail page](../../../specs/epics/11-web-ui/story-11-02-video-detail-page.md) | [plan](../../../specs/epics/11-web-ui/plan-11-02-video-detail-page.md) | spec |
| 11.3  | [Video player](../../../specs/epics/11-web-ui/story-11-03-video-player.md) | [plan](../../../specs/epics/11-web-ui/plan-11-03-video-player.md) | spec |
| 11.4  | [Search interface](../../../specs/epics/11-web-ui/story-11-04-search-interface.md) | [plan](../../../specs/epics/11-web-ui/plan-11-04-search-interface.md) | spec |
| 11.5  | [Processing queue dashboard](../../../specs/epics/11-web-ui/story-11-05-processing-queue-dashboard.md) | [plan](../../../specs/epics/11-web-ui/plan-11-05-processing-queue-dashboard.md) | spec |
| 11.6  | [Settings page](../../../specs/epics/11-web-ui/story-11-06-settings-page.md) | [plan](../../../specs/epics/11-web-ui/plan-11-06-settings-page.md) | spec |
| 11.7  | [Responsive design](../../../specs/epics/11-web-ui/story-11-07-responsive-design.md) | [plan](../../../specs/epics/11-web-ui/plan-11-07-responsive-design.md) | spec |
| 11.8  | [Dark / light theme](../../../specs/epics/11-web-ui/story-11-08-dark-light-theme.md) | [plan](../../../specs/epics/11-web-ui/plan-11-08-dark-light-theme.md) | spec |
| 11.9  | [Keyboard shortcuts](../../../specs/epics/11-web-ui/story-11-09-keyboard-shortcuts.md) | [plan](../../../specs/epics/11-web-ui/plan-11-09-keyboard-shortcuts.md) | spec |
| 11.10 | [Offline (PWA service worker)](../../../specs/epics/11-web-ui/story-11-10-offline-pwa.md) | [plan](../../../specs/epics/11-web-ui/plan-11-10-offline-pwa.md) | spec |
| 11.11 | [Accessibility (WCAG 2.1 AA)](../../../specs/epics/11-web-ui/story-11-11-accessibility.md) | [plan](../../../specs/epics/11-web-ui/plan-11-11-accessibility.md) | spec |
| 11.12 | [i18n (Arabic RTL + English LTR)](../../../specs/epics/11-web-ui/story-11-12-i18n-rtl.md) | [plan](../../../specs/epics/11-web-ui/plan-11-12-i18n-rtl.md) | spec |
| 11.13 | [API: Personal Access Tokens (PAT)](../../../specs/epics/11-web-ui/story-11-13-pat-management-api.md) | [plan](../../../specs/epics/11-web-ui/plan-11-13-pat-management-api.md) | spec (added per REVIEW §3.4) |
| 11.14 | [API: active session listing & revocation](../../../specs/epics/11-web-ui/story-11-14-active-sessions-api.md) | [plan](../../../specs/epics/11-web-ui/plan-11-14-active-sessions-api.md) | spec (added per REVIEW §3.4) |

## DB tables owned

This epic ships only client code — no DB tables of its own. Stories 11.13 and 11.14 add API surface (and accompanying schema) that lives logically in [Epic 10](epic-10-auth-security.md):

- **PATs (11.13):** new table `personal_access_tokens` (token hash, label, scopes, last_used_at, revoked_at).
- **Active sessions (11.14):** reads from `web_sessions` and `refresh_tokens` (owned by Epic 10).

## API endpoints owned

| Endpoint                             | Story  |
|--------------------------------------|--------|
| `GET/POST /auth/tokens`, `DELETE /auth/tokens/{id}` | 11.13 |
| `GET /auth/sessions`, `DELETE /auth/sessions/{id}`  | 11.14 |

> All other endpoints consumed by Epic 11 are owned by [Epic 07](epic-07-api-server.md), [Epic 08](epic-08-streaming.md), or [Epic 10](epic-10-auth-security.md).

## Mockups

| File | Story | Platform | UI states / contents |
|---|---|---|---|
| [`web/mockups/mockup-11-01-library-browser.html`](../../../web/mockups/mockup-11-01-library-browser.html) | 11.1 | web | Grid + list view, sort/filter rail |
| [`web/mockups/mockup-11-02-video-detail.html`](../../../web/mockups/mockup-11-02-video-detail.html) | 11.2 | web | Hero, metadata, transcript jump, related |
| [`web/mockups/mockup-11-03-video-player.html`](../../../web/mockups/mockup-11-03-video-player.html) | 11.3 | web | Vidstack chrome, captions, chapters, sprite scrubber |
| [`web/mockups/mockup-11-04-search-interface.html`](../../../web/mockups/mockup-11-04-search-interface.html) | 11.4 | web | FTS / semantic / hybrid toggle, facets, transcript hits |
| [`web/mockups/mockup-11-05-processing-queue.html`](../../../web/mockups/mockup-11-05-processing-queue.html) | 11.5 | web | Job pipeline, per-stage counts, retry/cancel actions |
| [`web/mockups/mockup-11-06-settings.html`](../../../web/mockups/mockup-11-06-settings.html) | 11.6 | web | Settings layout (libraries, STT, players) |
| [`web/mockups/mockup-11-07-theme.html`](../../../web/mockups/mockup-11-07-theme.html) | 11.7, 11.8 | web | Dark / light theme demo, responsive breakpoints |
| [`web/mockups/mockup-11-10-offline-pwa.html`](../../../web/mockups/mockup-11-10-offline-pwa.html) | 11.10 | web | Service-worker install banner, offline fallback, sync state |
| [`web/mockups/mockup-11-12-i18n.html`](../../../web/mockups/mockup-11-12-i18n.html) | 11.12 | web | Arabic RTL + English LTR side-by-side |
| [`web/mockups/mockup-17-06-onboarding.html`](../../../web/mockups/mockup-17-06-onboarding.html) | 11.x (consumes 17.6) | web | First-run onboarding wizard |
| [`web/mockups/admin/sessions.html`](../../../web/mockups/admin/sessions.html) | 11.14 | admin (web) | Active sessions list, revoke flow |
| [`web/mockups/theme-library/*.html`](../../../web/mockups/theme-library/) | 11.7, 11.8 | web | Component library: buttons, inputs, cards, modals, tables, navigation, badges-tags, colors, typography, player-controls |

## Diagrams

| Diagram | Type | Coverage |
|---|---|---|
| [`client-stories.drawio`](../../../specs/diagrams/client-stories.drawio) | Story-relationship | All Epic 11 stories grouped with 12/13/17 |
| [`system-architecture.drawio`](../../../specs/diagrams/system-architecture.drawio) | System | Web client in the macro topology |
| [`data-flow.drawio`](../../../specs/diagrams/data-flow.drawio) | Flow | Browser → API + Streaming + WS subscription |
| [`epic-dependencies.drawio`](../../../specs/diagrams/epic-dependencies.drawio) | Story-relationship | Web UI's inbound deps from 7/8/10/17 |

## Dependencies on other epics

- **Epic 17 (Design System) stories 17.1, 17.2** must land first — every component and layout is token-driven.
- **[Epic 07](epic-07-api-server.md) stories 7.1, 7.2, 7.4, 7.6, 7.8, 7.10, 7.11, 7.13, 7.16, 7.17** — CRUD, search, sessions, watch progress, queue stats, WebSocket fan-out, GraphQL.
- **[Epic 08](epic-08-streaming.md) stories 8.1, 8.5, 8.11, 8.12, 8.13** — manifests, HLS, subtitles, chapters, posters/sprites.
- **[Epic 10](epic-10-auth-security.md) stories 10.1–10.5, 10.7, 10.8** — login, refresh, logout, signed-URL minter.
- **Epic 16 story 16.6** — feature-flag layer for tier-gated UI.

## Key decisions

- **Vite, not Next.js.** SPA only; no SSR. Source bundle ships unchanged into Capacitor / Tauri shells.
- **GraphQL types are generated** from `shared/graphql/schema.graphql`. No hand-rolled client types.
- **WebSocket subscriptions live in `lib/ws.ts`**, not duplicated per page.
- **i18n table is the only string source.** No literal strings in JSX. Arabic RTL is first-class, not bolted on.
- **Tokens-only styling.** No hard-coded color, font, or spacing. Every component sources from the design-system tokens (Epic 17).
- **A11y is non-optional.** axe-core in CI; reduced-motion respected; every interactive control reachable by keyboard.
- **Refresh tokens never in `localStorage`.** httpOnly cookies (web) and Keychain/Keystore (Capacitor).
- **PAT issuance** ([story 11.13](../../../specs/epics/11-web-ui/story-11-13-pat-management-api.md)) is the only legitimate `Authorization: Bearer` path for the web SPA — used by CLI and browser-extension integrations.

## Performance budgets

- First contentful paint ≤ **1.5 s** on 50 Mbps LAN, cold cache.
- Search p50 ≤ **250 ms**, p95 ≤ **500 ms** on the 100k-segment fixture (aligned with [Epic 07 story 7.8](../../../specs/epics/07-api-server/story-07-08-search-api.md) and Epic 18 story 18.1).
- Player join time ≤ **2 s** direct, ≤ **4 s** transcoded.
- PWA shell ≤ **350 KB** gzipped.

## Open questions

- **Mini-player across routes.** Persist the Vidstack player instance across React Router transitions, or always re-create with a saved position? Decision affects Epic 17 story 17.8.
