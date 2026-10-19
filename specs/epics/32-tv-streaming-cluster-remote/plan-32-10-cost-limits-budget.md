# Plan 32.10 — Cost, limits and budget — implementation

> Implementation plan for [story-32-10](story-32-10-cost-limits-budget.md).
> Uses slot 0121 (`tv_remote_usage_daily`). Design: [design.md](design.md) §9.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | The estimate uses Cloudflare's own minutes (`streamMinutesViewedAdaptiveGroups`) when the token can read analytics, session minutes otherwise. | Cloudflare's number is what is billed; sessions are a safe upper bound. |
| D2 | Between hourly polls, add running sessions' minutes. | The estimate errs high, never low. |
| D3 | At 100 %: refuse new tunes, let running sessions finish (default). | Same rule as Epic 25.12; no mid-programme cut. |
| D4 | All limits are rules settings with today's behaviour (remote off) as default. | "Rules as settings" convention. |

## 1. Files

**Create**
- `api/internal/tvremote/usage.go` — hourly GraphQL poll (video ids of this month's pushes, 31-day span limit respected), session fallback, `tv_remote_usage_daily` upsert
- `api/internal/tvremote/budget.go` — `Estimate(month)`, thresholds, notifications (existing notifications/log events), `Exhausted()`
- `api/internal/tvremote/storage_guard.go`
- tests

**Modify**
- `api/internal/handlers/rules/schema.json`, `schema.go` — section `tv_remote` (all keys of the README settings table, validation ranges of the story, en + ar descriptions)
- `api/internal/tvremote/sessions.go` (32.8) — limits read from settings each call
- `web/src/pages/Settings/*` (the rules settings page) — the `tv_remote` section with the live usage line

## 2. Estimate

```
delivered = Σ minutesViewed(uid ∈ month's push video ids)          # or Σ session minutes
running   = Σ over live sessions (now − max(last_poll, started_at))
cost      = (delivered + running) × delivery_usd_per_1000_min / 1000
          + storage_plan_minutes / 1000 × 5
```

GraphQL (account scope):

```graphql
query($acct: string!, $from: Date!, $to: Date!, $uids: [string!]) {
  viewer { accounts(filter: {accountTag: $acct}) {
    streamMinutesViewedAdaptiveGroups(limit: 1000,
      filter: {date_geq: $from, date_lt: $to, uid_in: $uids}, orderBy: [sum_minutesViewed_DESC]) {
      sum { minutesViewed } dimensions { uid }
    } } } }
```

(`uid_in` per the dataset's filters; if not supported, page with `uid_gt` and filter client-side.)

## 3. Tests

Settings validation; GraphQL fake; fallback; thresholds 79/80/100 with `block_new` and `warn`;
budget 0 = off; storage guard; the monotonic estimate within a day.

## 4. Rollout

With 32.8. The default budget ($10) is deliberately low; the runbook explains how to size it.
