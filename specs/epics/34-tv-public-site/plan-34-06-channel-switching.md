# Plan 34.6 — Channel switching — implementation

> Implementation plan for [story-34-06](story-34-06-channel-switching.md). Design: [design.md](design.md) §8.
> The module `@maktaba/tvplayer` is on main (`web/packages/tvplayer`, PR #87); this plan only adds
> the public site's host-app code.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Consume `@maktaba/tvplayer` as is; no fork, no copy. | One player and one set of tests for the app, remote and the public site. Changes the site needs go into the module with its own tests. |
| D2 | Public resolver over `/api/v1/play`, neighbour tokens cached client-side. | Warm switch without an API round trip. |
| D3 | Telemetry sink → Worker → Analytics Engine. | Same `SwitchEvent` the app keeps in `SwitchLog`; no personal data. |
| D4 | Always-on public channels → the warm path is the default. | Target < 2 s first frame. |

## 1. Files

**Create** (`web/tv-site/src/player/`)
- `publicResolver.ts` — `Resolver` over `/api/v1/play`, neighbour cache, `SwitchError` mapping
- `heartbeat.ts` — 60 s beat (15 s during rotation), URL refresh into `onUrl` / `rewrite`
- `telemetry.ts` — sink posting `SwitchEvent` to `/api/v1/events` (Worker → Analytics Engine)
- tests: `publicResolver.test.ts`, `heartbeat.test.ts`, Playwright `switching.spec.ts`

**Modify**
- `web/tv-site/src/pages/Watch.tsx` — `useTvSwitcher` with the public resolver, `TuningCard`,
  `MiniGuide` fed from the published guide
- `web/tv-site` build config — the same `@maktaba/tvplayer` alias as `web/vite.config.ts` / `web/tsconfig.json`

## 2. Steps

1. Resolver and neighbour cache with a fake API (unit tests first).
2. Heartbeat and URL refresh.
3. Watch page wiring; deep links.
4. Telemetry sink and the Worker events endpoint (with 34.9).
5. Playwright switching timing test against `wrangler dev`.

## 3. Tests

Story TCs; the module's own tests stay as they are on main.

## 4. Rollout

With Phase B.
