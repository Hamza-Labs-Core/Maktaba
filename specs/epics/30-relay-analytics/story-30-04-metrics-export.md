# Story 30.4 — Metrics export

> Epic 30 · Cloud Relay Anonymous Analytics · Phase 4 (export & integration)

## Description

Two export surfaces let operators get the data out of the dashboard and
into their own tooling.

- **Operator export.** `GET /v1/admin/metrics/export?format=csv|json&range=`
  (admin-gated, api role) streams the hourly rollup rows for the window
  as CSV or JSON. CSV carries a header row; JSON is an array of
  `{hour, metric, country, sum_value, max_value, samples}`.
- **Prometheus / OpenMetrics endpoint.** `GET /metrics` (relay role, no
  auth — scraped on the internal network) returns the live collector
  state in Prometheus text exposition format for Grafana:
  - `maktaba_relay_connected_servers` (gauge)
  - `maktaba_relay_active_tunnels` (gauge)
  - `maktaba_relay_bandwidth_in_bytes_total` (counter)
  - `maktaba_relay_bandwidth_out_bytes_total` (counter)
  - `maktaba_relay_requests_total` (counter)
  - `maktaba_relay_push_sent_total` / `maktaba_relay_push_failed_total`
    (counters)

  Each series carries `# HELP` and `# TYPE` lines; counters use the
  `_total` suffix per OpenMetrics convention.

## Acceptance criteria

- **Given** an admin caller and hourly rows in range,
  **when** they `GET …/export?format=csv&range=30d`,
  **then** the body is CSV with a header
  `hour,metric,country,sum_value,max_value,samples` and one line per row,
  `Content-Type: text/csv`.

- **Given** the same with `format=json`,
  **then** the body is a JSON array of the same rows and
  `Content-Type: application/json`.

- **Given** the collector has observed 3 servers and moved 1000 bytes in,
  **when** `GET /metrics` is scraped,
  **then** the output contains
  `maktaba_relay_connected_servers 3` and
  `maktaba_relay_bandwidth_in_bytes_total 1000` with their `# TYPE`
  lines, and parses as valid Prometheus exposition.

- **Given** an unknown `format`,
  **when** the export is requested,
  **then** it defaults to JSON (does not error).

## Notes

- The Prometheus exposition is rendered by hand (no client_golang
  dependency) — it is a stable, small text format and keeps the cloud
  module's dependency footprint unchanged.
- Export reads the same hourly rollup the dashboard reads, so exported
  numbers reconcile with the graphs exactly.
