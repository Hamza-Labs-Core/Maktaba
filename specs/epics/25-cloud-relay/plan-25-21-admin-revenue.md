# Implementation Plan — Story 25.21 Admin: revenue dashboard

> Companion to [story-25-21-admin-revenue.md](story-25-21-admin-revenue.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Data sources | Stripe API (cached daily), local `subscriptions`, `invoices`, `bandwidth_monthly`, `servers`. |
| FX | Daily ECB rates pulled by a cron into `fx_rates`. |
| Snapshot | `revenue_snapshots` daily row preserves history through Stripe retention. |
| Endpoints | `GET /api/admin/revenue`, `/timeseries`, `/cost-per-user`, `/revenue/export?period=YYYY-MM`. |
| Caching | Stripe pull cached 1h on `stripe_cache` table; UI page renders from local rollups, never live Stripe per-request. |
| Out of scope | Cohort retention curves (deferred). Forecast model (BI tool). |

## 1. Migration `00060002_revenue.sql` (slot 0006 sub-sequence)

Sub-sequence file in slot 0006 (billing). The canonical CREATEs in
`00060001_billing.sql` (plan-25-13) are not touched.

```sql
-- +goose Up
CREATE TABLE revenue_snapshots (
    date                  DATE PRIMARY KEY,
    mrr_cents             BIGINT NOT NULL,
    arr_cents             BIGINT NOT NULL,
    customer_count        INT NOT NULL,
    active_server_count   INT NOT NULL,
    total_bandwidth_bytes BIGINT NOT NULL,
    plan_mix              JSONB NOT NULL,         -- {"pro_monthly": n, "pro_yearly": n, "family_monthly": n, "family_yearly": n}
    fx_basis              TEXT NOT NULL DEFAULT 'usd-daily-ecb'
);

CREATE TABLE fx_rates (
    date         DATE NOT NULL,
    currency     TEXT NOT NULL,           -- 'eur','gbp', etc.
    usd_per_unit NUMERIC(20,10) NOT NULL,
    PRIMARY KEY (date, currency)
);

CREATE TABLE stripe_cache (
    key        TEXT PRIMARY KEY,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload    JSONB NOT NULL
);

-- +goose Down
DROP TABLE IF EXISTS stripe_cache, fx_rates, revenue_snapshots;
```

`plan_mix` keys remain `pro_monthly`/`pro_yearly`/`family_monthly`/
`family_yearly` for backwards-compatible reporting — the source-of-
truth canonical form is `(tier, interval)` pairs on `subscriptions`;
the JSONB shape here is the reporting projection.

## 2. Daily Stripe sync (03:00 UTC)

```go
// cloud/internal/jobs/stripe_sync.go
func StripeSync(ctx context.Context, db *pgxpool.Pool) error {
    if !acquireAdvisoryLock(ctx, db, 8472614) { return nil }
    defer releaseAdvisoryLock(ctx, db, 8472614)

    // Pull subscriptions
    iter := subscription.List(&stripe.SubscriptionListParams{Limit: stripe.Int64(100)})
    for iter.Next() {
        sub := iter.Subscription()
        _, _ = db.Exec(ctx, `INSERT INTO stripe_cache(key, payload) VALUES ($1, $2)
            ON CONFLICT(key) DO UPDATE SET fetched_at=now(), payload=EXCLUDED.payload`,
            "sub:"+sub.ID, mustJSON(sub))
        applySubscriptionToLocal(db, sub)
    }
    // Pull invoices last 30 days (incremental)
    invIter := invoice.List(&stripe.InvoiceListParams{...})
    for invIter.Next() { applyInvoiceToLocal(db, invIter.Invoice()) }
    return nil
}
```

## 3. Snapshot job (03:30 UTC)

```go
func SnapshotRevenue(ctx context.Context, db *pgxpool.Pool, day time.Time) error {
    var mrrCents int64
    db.QueryRow(ctx, `
        SELECT COALESCE(SUM(
            CASE
              WHEN interval = 'yearly' THEN priceCents / 12
              ELSE priceCents
            END
        ), 0)
        FROM subscriptions WHERE status='active'
    `).Scan(&mrrCents)
    // active_server_count
    var serverCount int
    db.QueryRow(ctx, `SELECT count(*) FROM servers WHERE last_seen_at >= now()-INTERVAL '24 hours' AND deleted_at IS NULL`).Scan(&serverCount)
    var totalBW int64
    db.QueryRow(ctx, `SELECT COALESCE(SUM(bytes_in+bytes_out), 0) FROM bandwidth_samples WHERE date = $1`, day).Scan(&totalBW)
    planMix, _ := planMixJSON(ctx, db)
    _, _ = db.Exec(ctx, `INSERT INTO revenue_snapshots(date, mrr_cents, arr_cents, customer_count, active_server_count, total_bandwidth_bytes, plan_mix)
        VALUES($1,$2,$3,(SELECT count(*) FROM subscriptions WHERE status='active'),$4,$5,$6)
        ON CONFLICT(date) DO UPDATE SET mrr_cents=EXCLUDED.mrr_cents, arr_cents=EXCLUDED.arr_cents, customer_count=EXCLUDED.customer_count, active_server_count=EXCLUDED.active_server_count, total_bandwidth_bytes=EXCLUDED.total_bandwidth_bytes, plan_mix=EXCLUDED.plan_mix`,
        day, mrrCents, mrrCents*12, serverCount, totalBW, planMix)
    return nil
}
```

`priceCents` is looked up via plan key from `Plans` registry (25.13).

## 4. FX rate fetch

`ECB` daily reference rates published at https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml. Fetch every UTC 14:00 (post-ECB release), convert to USD-base.

## 5. API surfaces

```go
// GET /api/admin/revenue
func revenueOverview(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        latest, _ := s.repo.LatestSnapshot(r.Context())
        live, _ := s.repo.LiveActiveSubsCount(r.Context())
        churn30 := s.calc.Churn30Day(r.Context())
        ltv := s.calc.LTV(r.Context())
        writeJSON(w, 200, map[string]any{
            "mrr_cents":         latest.MRRCents,
            "arr_cents":         latest.ARRCents,
            "customer_count":    latest.CustomerCount,
            "active_server_count": latest.ActiveServerCount,
            "churn_30d":          churn30,
            "ltv":                ltv,
            "plan_mix":           latest.PlanMix,
            "as_of":              latest.Date,
            "live_active_subs":   live,  // labeled "live"; differs from snapshot for today
        })
    }
}
```

```go
// GET /api/admin/revenue/timeseries?metric=mrr&from=...&to=...
func revenueTimeseries(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        rows, _ := s.repo.Timeseries(r.Context(), r.URL.Query().Get("metric"), parseRange(r, 90))
        writeJSON(w, 200, map[string]any{"points": rows})
    }
}
```

```go
// GET /api/admin/cost-per-user?period=2026-04
func costPerUser(s *Service) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        period := r.URL.Query().Get("period")
        users, _ := s.repo.UsersWithBilling(r.Context(), period)
        var out []userCost
        for _, u := range users {
            cost := s.calc.ModelCost(u)
            out = append(out, userCost{
                UserID: u.ID, Plan: u.Plan, Revenue: u.Revenue,
                StripeFees: u.StripeFees, Cogs: cost, Margin: u.Revenue - cost,
            })
        }
        writeJSON(w, 200, map[string]any{"period": period, "rows": out})
    }
}
```

```go
// GET /api/admin/revenue/export?period=2026-04
func revenueExport(s *Service) http.HandlerFunc { /* CSV streaming join */ }
```

## 6. Cost model

```go
// cloud/internal/admin/cost_model.go
type CostInputs struct {
    BandwidthBytes int64
    EgressPricePerTBUSD float64    // €1.20 / TB → USD-converted
    BasePerUser float64            // Hetzner CCX23 share + PG share
    StripeFeeCents int64           // computed from actual revenue
}

func ModelCost(in CostInputs) float64 {
    base := in.BasePerUser
    egress := float64(in.BandwidthBytes) / 1e12 * in.EgressPricePerTBUSD
    return base + egress + float64(in.StripeFeeCents)/100
}
```

`BasePerUser`:

- Pro: 0.40 (compute) + 0.05 (PG) = 0.45.
- Family: 0.80 + 0.05 = 0.85.

Stripe fees = 2.9% × revenue + 0.30 (per charge).

## 7. UI charts

Front-end uses Chart.js or Recharts; renders points from `/timeseries`. Pattern + color for color-blind safety. "Stale" badge when `latest.as_of < today`.

## 8. CSV export

```go
func revenueExportCSV(ctx context.Context, w io.Writer, db *pgxpool.Pool, period string) error {
    cw := csv.NewWriter(w)
    cw.Write([]string{"user_id","plan","gb_relayed","gross_cents","fees_cents","cogs_cents","margin_cents"})
    rows, _ := db.Query(ctx, `
      SELECT s.user_id, s.plan,
             COALESCE(m.bytes_out + m.bytes_in, 0)/1e9 AS gb,
             COALESCE(i.total_cents, 0)
      FROM subscriptions s
      LEFT JOIN bandwidth_monthly m ON m.user_id=s.user_id AND m.year_month=$1
      LEFT JOIN invoices i ON i.user_id=s.user_id AND to_char(i.period_start, 'YYYY-MM')=$1
      WHERE s.status='active'
    `, period)
    for rows.Next() { /* write row */ }
    cw.Flush()
    return nil
}
```

## 9. Test plan

### 9.1 Unit

| Test | Pins |
|---|---|
| `TestMRRYearlyAmortization` | yearly counted at 1/12. |
| `TestChurnCohort` | known fixtures → expected churn. |
| `TestCostModelExample` | (BW 100GB, Pro) ≈ $1.16 ± rounding. |
| `TestFXConvertEURtoUSD` | known rate → correct value. |
| `TestRefundsExcluded` | invoice status=refunded omitted from revenue. |

### 9.2 Integration

| Test | Pins |
|---|---|
| `TestStripeSyncIdempotent` | Re-run is a no-op. |
| `TestSnapshotTakesAdvisoryLock` | Concurrent runs → one wins. |
| `TestStripe429Cached` | Stripe rate-limited → cache served + stale badge. |
| `TestCSVExportSchemaStable` | Header row stable across versions. |
| `TestAnonymizedRevenueHistory` | GDPR-deleted user's revenue still in totals. |
| `TestEdgeCountryK5Anonymity` | <5 customers in a country → "other". |

## 10. Edge cases — handling table

| Case | Behaviour | Pinned |
|---|---|---|
| FX rate missing day | Use last known. | Implementation. |
| Refunds | Subtract in period of refund. | `TestRefundsExcluded`. |
| Multiple subs per user | Sum to one row; flag in admin. | Spec. |
| PCI scope | Strip last4/fingerprint before storage. | 25.14 cross-story. |
| Live vs snapshot | UI labels live "as of now"; snapshot "as of YYYY-MM-DD". | UX. |
| Privacy of geo | k=5 anonymity. | `TestEdgeCountryK5Anonymity`. |
| Cost model accuracy | UI shows dashboard; pricing reviewed every 6 months. | Spec. |
| Outliers | Median + top-N separately. | UI. |

## 11. Dependencies

- 25.13 (`subscriptions`, `invoices`).
- 25.14 (webhook keeps state current).
- 25.11 (`bandwidth_monthly`).
- 25.20 (admin auth + audit).

## 12. Acceptance checklist

- [ ] Migration 00060002 applies.
- [ ] Daily Stripe sync + snapshot crons.
- [ ] FX rate fetch.
- [ ] Endpoints: overview / timeseries / cost-per-user / export.
- [ ] Reconciles to ±$5 of Stripe.
- [ ] CSV export schema stable.
- [ ] Tests in §9 pass.
