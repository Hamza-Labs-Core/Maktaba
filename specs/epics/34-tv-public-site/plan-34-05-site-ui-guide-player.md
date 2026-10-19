# Plan 34.5 — Site UI: guide and player — implementation

> Implementation plan for [story-34-05](story-34-05-site-ui-guide-player.md). Design: [design.md](design.md) §3.3, §6.1.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Vite + React SPA in `web/tv-site`, design tokens from `web/design-system`. | Same stack and look as the Maktaba web app; small separate bundle. |
| D2 | Worker injects meta/JSON-LD into the SPA shell (HTMLRewriter), no SSR framework. | SEO basics at minimal cost and complexity. |
| D3 | Language in the URL (`/ar`, `/en`), Arabic default, logical CSS properties. | Shareable, cacheable, correct RTL. |
| D4 | hls.js lazy-loaded through `@maktaba/tvplayer` (34.6). | Small first load; one player implementation. |

## 1. Files

**Create** (`web/tv-site/`)
- `index.html`, `vite.config.ts`, `src/main.tsx`, `src/routes.tsx`
- `src/pages/Home.tsx` (grid/list), `Guide.tsx` (EPG with virtualised rows, sticky channel column, now line),
  `Watch.tsx` (player + now/next + day schedule + `?at=` logic), `Embed.tsx`, `About.tsx`, `Legal.tsx`
- `src/components/*` — ChannelTile, ProgrammeCell, NowNext, ProgressBar, OffAirCard, ShareButton,
  LanguageSwitch, Header/Footer
- `src/i18n/{ar,en}.json`, `src/lib/time.ts` (zones, digits, clock offset)
- `public/manifest.webmanifest` (PWA), icons generated from branding at publish time
- tests (Vitest + Testing Library), Playwright specs

**Modify**
- `web/design-system` — export tokens as CSS variables usable by `tv-site` (no app coupling)

## 2. Steps

1. Shell, routing, i18n, RTL, theme variables from `site.json`.
2. Grid and list from `lineup.json` + guide blobs; client now/next.
3. Guide EPG with virtualisation (22 channels × 3 days), keyboard navigation, links per cell.
4. Watch page with `?at=` states, off-air/slate states, share.
5. Embed page and CSP; about/legal from Markdown (sanitised, `rehype-sanitize`).
6. Worker meta per route, sitemap/robots.
7. Accessibility and performance passes (axe, Lighthouse in CI).

## 3. Tests

Story TCs.

## 4. Rollout

Phase A with guide-only (watch page shows "Watch at home in the Maktaba app" until video is on).
