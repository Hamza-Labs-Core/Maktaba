# Story 32.10 — Cost, limits and budget

## Description

*As the owner paying the Cloudflare bill, I want to cap how many channels go out, how many streams
each person can open, and how much I spend each month, and be told before I hit the cap.*

Design: [design.md](design.md) §9; README "Cost".

## Acceptance criteria

- **AC1** Rules section `tv_remote` holds the limits and budget settings of the README table, with
  validation (1 ≤ `max_channels` ≤ 10; 1 ≤ `max_streams_per_user` ≤ 10; 1 ≤ `idle_timeout_minutes`
  ≤ 60; 5 ≤ `token_ttl_minutes` ≤ 1440; 1 ≤ `max_push_hours` ≤ 24; budget ≥ 0; prices ≥ 0;
  countries are ISO 3166-1 alpha-2). Defaults keep remote off.
- **AC2** Hourly, the controller reads `streamMinutesViewedAdaptiveGroups` (`sum.minutesViewed`) for
  the video ids of this month's Maktaba pushes and writes `tv_remote_usage_daily`; without Analytics
  permission it uses session minutes (`Σ (ended_at − started_at)`), flagged "estimated from sessions".
- **AC3** The month's estimate = delivered minutes × `delivery_usd_per_1000_min` / 1000 +
  `storage_plan_minutes` / 1000 × 5; between polls it adds the minutes of running sessions, so it
  errs high.
- **AC4** At 80 % of `monthly_budget_usd`: one admin notification and `event=tv_budget_80`. At
  100 %: with `budget_action = block_new` new tunes get 402 (running sessions continue until they
  end); with `warn` only a notification. A budget of 0 means "no budget check".
- **AC5** The storage guard refuses a new push when the running pushes' age plus `max_push_hours`
  would exceed `storage_plan_minutes`.
- **AC6** The settings page shows, next to the budget, the current month's delivered minutes, the
  estimate, the peak recording minutes and what each limit is doing ("2 of 2 remote channels in use").
- **AC7** The settings page states there is no audio-only saving (Cloudflare bills minutes, not
  bytes) and that the upload use is one channel bitrate per pushed channel.

## Test cases

- **TC1** `TestRemoteSettingsValidation`.
- **TC2** `TestUsageFromAnalytics` (fake GraphQL) and `TestUsageFromSessionsFallback`.
- **TC3** `TestBudgetThresholds` — 79 %, 80 %, 100 %; `block_new` vs `warn`; budget 0.
- **TC4** `TestStorageGuard`.

## Edge cases

- **EC1** Month boundary in UTC vs the home's time zone: the budget month follows Cloudflare
  billing (UTC); the page says so.
- **EC2** Analytics lag (data arriving late): the estimate never decreases within a day.
- **EC3** The admin raises the budget after 100 %: new tunes are allowed at once.

## Dependencies

32.7, 32.8.
