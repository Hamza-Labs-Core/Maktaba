# Story 34.9 — Budget, abuse controls and analytics

## Description

*As the owner paying for every minute a stranger watches, I want a hard monthly cap, limits per
visitor, protection against bots and hot-linking, and numbers on who watches what — without
collecting personal data.*

Design: [design.md](design.md) §6.3, §9; README R9; Epic 32.10.

## Acceptance criteria

- **AC1** `tv_public.monthly_budget_usd` (default 20) and `budget_action` (`block_new` default,
  `off_air`); the settings page shows public, remote (Epic 32) and storage estimates and their sum for
  the Cloudflare account, with the month's delivered minutes.
- **AC2** Live estimate in SiteControl from session minutes (errs high), reconciled hourly with Stream
  GraphQL minutes for the public inputs' video ids — by the home, or by the Worker cron when the home
  has been offline > 1 h.
- **AC3** 80 % → admin notification once; 100 % → `block_new`: new plays get 503 `off_air(budget)`,
  running sessions end at the end of their programme or after 60 min; `off_air`: everything stops within
  60 s. Raising the budget lifts the block at once. Budget 0 = no check.
- **AC4** Limits: `max_concurrent_viewers`, `max_sessions_per_ip`, `viewer_daily_minutes`,
  `allowed_countries`; Rate Limiting binding namespaces `play` 30/60 s, `api` 120/60 s, `ws` 10/60 s per
  hashed IP → 429 with `Retry-After`.
- **AC5** Hot-linking: a token copied to another IP (with `bind_token_ip`) or another site (origins)
  does not play; a token is never refreshed without a heartbeat from a live session.
- **AC6** Viewer counts per channel from Stream `…/views` every 60 s on the admin page; on the site only
  with `show_viewer_count` (rounded below 50; inputs' `hideLiveViewerCount` follows the setting).
- **AC7** Analytics Engine events (play, beat, switch, error, off_air) with channel, country, device
  class, hashed session — no IP, no user agent string; the admin usage panel shows minutes per channel
  and day, peak concurrent viewers, countries, switch times, errors, for 90 days.
- **AC8** The settings page states the cost model plainly: "$1 per 1,000 minutes watched; 100 viewers ×
  2 h/day ≈ $360/month", and that preloading counts.

## Test cases

- **TC1** `budget.test.ts` (79/80/100 %, both actions, budget 0, raise after block).
- **TC2** `limits.test.ts` (each limit; daily reset at 00:00 UTC).
- **TC3** Go `TestPublicUsageReconcile` (fake GraphQL), Worker `cron-reconcile.test.ts`.
- **TC4** `analytics.test.ts` (no IP or UA in data points).

## Edge cases

- **EC1** Analytics lag: the estimate never decreases within a day.
- **EC2** A crawler opens thousands of guide pages: free static/API reads, rate limited; no Stream cost.
- **EC3** NAT with many legitimate viewers on one IP (a school): `max_sessions_per_ip` can be raised;
  the admin sees "IP limit hit N times".

## Dependencies

34.4; Epic 32.10 (estimate and GraphQL query), 32.11 (metrics).
