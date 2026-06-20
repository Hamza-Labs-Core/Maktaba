# Implementation Plan — Story 30.3 Relay admin dashboard

> Companion to [story-30-03-relay-admin-dashboard.md](story-30-03-relay-admin-dashboard.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Package | `cloud/internal/handlers/metrics/` (handler + admin gate). |
| Mount | `cmd/maktaba-cloud/role_api.go` inside the `RequireUser` group. |
| Auth | `RequireAdmin` — same email-domain check as `handlers/admin`. |
| Reads | `metrics.Store` query helpers over `relay_metrics_hourly`, `relay_metrics_raw`, `push_dispatch_log`, `users`. |

## 1. Deps + Mount

```go
type Deps struct {
    DB            *sql.DB
    Users         *stores.Users
    AllowedDomain string
    Store         *metrics.Store
    Live          func() (servers, tunnels int) // optional collector gauge
}
func Mount(r chi.Router, d Deps)
```

Routes (all `d.RequireAdmin(...)`):
`GET /v1/admin/metrics/overview`, `/bandwidth`, `/push`,
`/subscriptions`, `/geo`, plus `/export` (Story 30.4) and
`/v1/admin/privacy/processing-records` (Story 30.2).

## 2. Range parsing

`parseRange(q) (start time.Time, key string)` mapping
`today|7d|30d|90d` → `now - window` (default `7d`). Shared with export.

## 3. Store reads

- `Overview(ctx, start)` → totals: `SUM(value)` per counter metric from
  hourly+raw since `start`; total servers `SELECT count(*) FROM servers`.
- `Series(ctx, metrics, start)` → `[]{hour, metric, sum}` for bandwidth
  graph.
- `Geo(ctx, start)` → `[]{country, requests}` from `requests` rows,
  `''`→`"unknown"`, ordered desc.
- `PushStats(ctx, start)` → sent/failed from `push_dispatch_log`
  (`status` grouping) — authoritative source (README D6).
- `Subscriptions(ctx)` → `plan,count` from `users`.

## 4. Handlers

Thin: parse range, call store, `writeJSON`. The `RequireAdmin` gate
loads the caller via `Users.ByID(GetUserID(ctx))` and compares the email
domain; `401` on lookup failure, `403` on domain mismatch — identical to
`handlers/admin`.

## 5. Tests

`metrics_test.go`: `parseRange` table (incl. default + unknown); the
admin-gate domain comparison via the pure `domainOf` helper. JSON shape
of pure mappers. DB reads follow the no-live-DB convention.
