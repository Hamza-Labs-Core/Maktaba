# Story 25.21 — Admin: revenue dashboard

> Epic 25 · Cloud relay · Phase 5 (admin)

## Description

The numbers HamzaLabs uses to make business decisions, on one page.
Pulled from Stripe (source of truth) and joined with cloud-side data
for cost-per-user analysis.

Top-line metrics:

- **MRR.** Active subscriptions × monthly equivalent.
- **ARR.** MRR × 12.
- **Net new MRR (last 30 days).** New − churned − contraction.
- **Customer count by plan.** Pro / Family / Yearly variants.
- **Churn rate (monthly, voluntary + involuntary).**
- **LTV (90-day cohort).** ARPU / churn.
- **Active server count.** Distinct `cloud_servers.id` with
  `last_seen_at >= now() - 24h`.
- **MTD bandwidth (TB).** Sum of `cloud_bandwidth_daily` this
  month.
- **Cost per user.** Modeled per the README cost matrix —
  recomputed from real bandwidth + Stripe fees.
- **Gross margin.** (Revenue − cost) / revenue.

Charts:

- Daily MRR (last 90 days, line).
- Daily new sign-ups vs. paid conversions (stacked bar).
- Top 20 servers by bandwidth (table; useful for spotting
  outliers).
- Bandwidth distribution (histogram of users by GB used).
- Geographic distribution (country code from Stripe customer;
  privacy-conscious — bucketed not per-user).

Data sources:

- Stripe `invoice.list`, `subscription.list` cached daily
  (full pull at 03:00 UTC; reactive updates from webhook
  25.14).
- Cloud-side `cloud_bandwidth_monthly` and
  `cloud_bandwidth_daily`.
- Cloud-side `cloud_servers`.

Endpoints:

- `GET /api/admin/revenue` — top-line numbers.
- `GET /api/admin/revenue/timeseries?metric=mrr&from=...` —
  charts data.
- `GET /api/admin/cost-per-user` — modeled costs.

CSV export:

- `GET /api/admin/revenue/export?period=2026-04` — invoice
  rows + bandwidth rows joined; one row per user-month.

Data retention:

- We keep cumulative daily snapshots in
  `cloud_revenue_snapshots(date, mrr_cents, customer_count,
  active_server_count, total_bandwidth_bytes)` so historical
  trends survive Stripe data-retention windows.

## Acceptance criteria

- **Given** an operator opens the revenue page,
  **when** the page loads,
  **then** MRR, ARR, customer count, churn rate are
  displayed and reconcile to ±$5 of Stripe's own dashboard
  for the same period.
- **Given** the daily Stripe sync runs at 03:00 UTC,
  **when** it completes,
  **then** `cloud_revenue_snapshots` has a row for the
  prior day and metrics for the prior day are stable.
- **Given** an operator filters "monthly" plans only,
  **when** the chart re-renders,
  **then** rows for yearly plans are excluded.
- **Given** a CSV export for `2026-04`,
  **when** generated,
  **then** the file contains one row per `(user_id,
  plan, gb_relayed, gross, fees, gross_after_fees)`.
- **Given** Stripe is rate-limiting,
  **when** a chart query fires,
  **then** the cached value is served and a "stale" badge
  is shown.
- **Given** the cost-per-user model produces a negative
  margin for a user,
  **when** they appear in the page,
  **then** they are flagged red ("cost exceeds revenue —
  likely heavy bandwidth user").
- **Given** GDPR-deleted users had revenue,
  **when** historic charts render,
  **then** their contributions remain (anonymized) in the
  totals — we never lose accounting history.

## Test cases

| ID  | Type        | Setup | Action | Expected |
|-----|-------------|-------|--------|----------|
| T01 | unit        | known fixtures | compute MRR | matches expected |
| T02 | integration | run Stripe sync | observe DB | snapshots written |
| T03 | regression  | Stripe API 429 | request | cached value, "stale" |
| T04 | unit        | churn calc on a small cohort | verify | matches manual |
| T05 | integration | CSV export | parse | schema valid, no PII leakage beyond what admin already has |
| T06 | regression  | yearly plan amortization | compute MRR | yearly counted at 1/12 |
| T07 | unit        | refunded invoices | exclude from revenue | excluded |
| T08 | integration | concurrent sync runs | observe locks | one wins via advisory lock |
| T09 | a11y        | charts color-blind safe | inspect | uses patterns + colors |
| T10 | unit        | currency normalization | compute MRR with EUR + GBP | converted to USD daily-rate |

## Edge cases

- **FX conversion.** Stripe charges in user-local currency;
  for MRR we normalize to USD using the daily ECB reference
  rate cached in `cloud_fx_rates`. Document.
- **Refunds.** Subtract refunded amounts from the period
  in which the refund occurred (not the original charge);
  this matches how Stripe reports refunds.
- **Multiple subscriptions per user (legacy).** Sum to one
  per-user row; flag for cleanup.
- **Stripe object types we don't sync.** `payment_method`
  details (PCI scope) are explicitly excluded.
- **Snapshots vs. live numbers.** Today-MRR is "live" —
  computed from current `cloud_subscriptions.status='active'`.
  Yesterday-MRR is from `cloud_revenue_snapshots`. UI labels
  which is which.
- **Privacy of geo data.** Country bucket is k-anonymous —
  if fewer than 5 customers in a country, bucket as "other".
- **Cost model accuracy.** The README's cost matrix is a
  baseline; the actual numbers from this dashboard supersede
  it. We re-evaluate pricing every 6 months.
- **Extreme outliers.** A single user's 5 TB outlier
  shouldn't tilt the average — we use median for typical
  usage and report top-N separately.

## Files / packages

- `cloud/internal/admin/revenue.go`.
- `cloud/internal/jobs/stripe_sync.go` — daily.
- `cloud/internal/jobs/snapshot_revenue.go` — daily.
- `cloud/internal/admin/cost_model.go`.
- `cloud/migrations/00060006_audit_revenue_snapshots.sql`.

## Open questions

- **Cohort retention curves.** Useful but defer — needs
  > 6 months of data anyway.
- **Forecast model.** Out of scope; built later in BI tool.
