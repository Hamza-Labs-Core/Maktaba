# Story 30.3 — Relay admin dashboard

> Epic 30 · Cloud Relay Anonymous Analytics · Phase 3 (operator surface)

## Description

Operator-only HTTP endpoints expose the collected metrics for a fleet
dashboard. They mount on the **api role** (which has the `RequireUser` +
email-domain admin gate) under `/v1/admin/metrics/*` and read the
`relay_metrics_*` tables the relay role writes (README D8).

Endpoints (all gated by `RequireAdmin`):

- **`GET /v1/admin/metrics/overview`** — live `connected_servers` /
  `active_tunnels` (from the collector gauge when co-located, else the
  latest sample), total servers, and range totals for bandwidth and
  requests.
- **`GET /v1/admin/metrics/bandwidth?range=`** — time series of
  `bandwidth_in_bytes` / `bandwidth_out_bytes` per hour for graphs.
- **`GET /v1/admin/metrics/push?range=`** — push delivery stats
  (sent/failed) aggregated from `push_dispatch_log`.
- **`GET /v1/admin/metrics/subscriptions`** — subscription breakdown by
  plan from `users`.
- **`GET /v1/admin/metrics/geo?range=`** — country-level request totals
  for the geographic heatmap.

`range` accepts `today|7d|30d|90d` (default `7d`); the window is mapped
to a start time server-side.

## Acceptance criteria

- **Given** a caller whose email domain is **not** the configured admin
  domain,
  **when** they hit any `/v1/admin/metrics/*` endpoint,
  **then** they get `403`.

- **Given** an admin caller and hourly rows over the last 7 days,
  **when** they `GET /v1/admin/metrics/bandwidth?range=7d`,
  **then** the response is a JSON time series of hourly in/out byte
  totals within the window.

- **Given** push rows in `push_dispatch_log`,
  **when** an admin `GET /v1/admin/metrics/push`,
  **then** the response carries `sent` and `failed` totals for the range.

- **Given** request rows tagged by country,
  **when** an admin `GET /v1/admin/metrics/geo`,
  **then** the response lists `{country, requests}` rows sorted
  descending, with `''` countries grouped as `"unknown"`.

- **Given** any metrics endpoint,
  **when** the response is inspected,
  **then** it contains **no** server id, user id, or IP — only counts and
  country codes.

## Notes

- The admin gate reuses the `handlers/admin` email-domain pattern so the
  operator allow-list stays in one conceptual place.
- Reads are covering-indexed `GROUP BY`s over bounded tables; no caching
  layer is needed at fleet scale.
