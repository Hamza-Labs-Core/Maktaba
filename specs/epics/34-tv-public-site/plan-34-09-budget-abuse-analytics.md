# Plan 34.9 — Budget, abuse controls and analytics — implementation

> Implementation plan for [story-34-09](story-34-09-budget-abuse-analytics.md).
> Uses the Epic 34 migration (`tv_public_usage_daily`; slot assigned at implementation). Design: [design.md](design.md) §9; Epic 32 plan-32-10.

## 0. Decisions

| # | Decision | Rationale |
|---|---|---|
| D1 | Enforcement in the Worker (SiteControl), estimation errs high. | Works when the home is offline; never under-counts. |
| D2 | Reconcile with Stream GraphQL hourly (home; Worker cron as fallback). | Cloudflare's minutes are what is billed. |
| D3 | Separate public budget, account-wide total shown. | Public audiences can cost far more than remote family viewing. |
| D4 | No personal data in analytics. | Privacy; nothing to protect or delete. |

## 1. Files

**Create** (Worker)
- `worker/src/budget.ts` (estimate, thresholds, actions), `worker/src/limits.ts`, `worker/src/analytics.ts`
  (Analytics Engine writes), `worker/src/cron.ts` (hourly reconcile fallback, daily salt rotation, GC)
- `worker/src/usage.ts` — `/_home/usage` aggregates (Analytics Engine SQL API through the Worker)

**Create** (Go)
- `api/internal/tvpublic/usage.go` — GraphQL `streamMinutesViewedAdaptiveGroups` for public video ids
  (reuses Epic 32 `tvremote/usage.go` query), `views` poll, `budget{}` messages, `tv_public_usage_daily`
- `web/src/pages/LiveTV/PublicSite/Usage.tsx` — usage panel (minutes, peaks, countries, switch times, errors)

**Modify**
- `api/internal/handlers/rules/schema.json` — budget/limit keys of the README table
- `web/tv-site/wrangler.jsonc` — ratelimits (`play`, `api`, `ws`), analytics dataset, cron triggers

## 2. Steps

1. Limits and rate limits in the play path (34.4) with 429 codes.
2. Live estimate and thresholds; alerts to the home; actions.
3. Home reconcile and views; Worker cron fallback (read-only analytics token secret).
4. Analytics Engine events from Worker and player (via `/api/v1/beat` and a small `/api/v1/ev` endpoint).
5. Usage panel and the cost explanation on the settings page.

## 3. Tests

Story TCs.

## 4. Rollout

Phase B with the default $20 budget and `block_new`; the runbook shows how to size it.
