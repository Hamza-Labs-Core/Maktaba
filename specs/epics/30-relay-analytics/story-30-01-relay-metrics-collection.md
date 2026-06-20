# Story 30.1 — Relay metrics collection

> Epic 30 · Cloud Relay Anonymous Analytics · Phase 1 (collection)

## Description

The relay collects **aggregate-only** operational metrics about its own
traffic and stores them in the cloud Postgres with an hourly rollup. No
individual user data is ever written — see [30.2](story-30-02-gdpr-compliance.md).

Collected metrics:

- **`connected_servers`** / **`active_tunnels`** (gauges) — sampled from
  the live `relay.Registry` on each collector tick.
- **`bandwidth_in_bytes`** / **`bandwidth_out_bytes`** (counters) —
  accumulated from each proxied request alongside the billing meter.
- **`requests`** (counter, dimensioned by **country**) — one increment
  per proxied request, tagged with the edge-derived country code.
- **`push_sent`** / **`push_failed`** (counters) — push delivery
  outcomes. (The dashboard's push card reads the authoritative counts
  from `push_dispatch_log`; these counters feed the live `/metrics`
  endpoint.)

Storage lifecycle:

- The collector accumulates **per-minute deltas** in memory and flushes
  them to `relay_metrics_raw` via an idempotent upsert keyed
  `(bucket, metric, country)`.
- A **rollup** runs hourly: completed hours in `relay_metrics_raw` are
  aggregated into `relay_metrics_hourly` (`sum`, `max`, `samples`) with
  an overwrite upsert, so re-runs are idempotent.
- **Raw rows are purged after 24 h**; hourly rows after 90 days
  ([30.2](story-30-02-gdpr-compliance.md) retention).

## Acceptance criteria

- **Given** three servers are connected,
  **when** the collector samples the gauges and flushes,
  **then** a `relay_metrics_raw` row exists for `connected_servers` with
  `value=3, samples=1` in the current minute bucket, and **no** row
  carries a server id or IP.

- **Given** two proxied requests from `DE` and one from `US` in a minute,
  **when** the collector flushes,
  **then** `relay_metrics_raw` has `requests` rows `(country=DE,value=2)`
  and `(country=US,value=1)`, and the `bandwidth_*` counters reflect the
  bytes moved.

- **Given** raw rows spanning two completed hours,
  **when** the rollup runs twice,
  **then** `relay_metrics_hourly` holds one row per `(hour, metric,
  country)` with the summed value, and the second run does not double it
  (idempotent overwrite).

- **Given** raw rows older than 24 h and hourly rows older than 90 days,
  **when** the purge runs,
  **then** the stale raw and hourly rows are deleted and fresher rows
  remain.

- **Given** the collector has recorded traffic,
  **when** `GET /metrics` is scraped,
  **then** it returns Prometheus exposition with monotonic counters and
  the current gauges (see [30.4](story-30-04-metrics-export.md)).

## Notes

- The collector keeps **process-cumulative atomics** for the Prometheus
  view and a **reset-on-flush delta map** for the DB rows (README D7), so
  a scrape and a flush never disagree.
- All collection is best-effort: a DB flush error is logged and the next
  tick retries; it never blocks the proxy hot path.
