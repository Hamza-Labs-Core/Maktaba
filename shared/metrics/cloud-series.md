# The cloud's application series

What `maktaba-cloud` serves on each role's `/metrics` for Epic 50.10's Prometheus, with names and
labels fixed here (Epic 53 story 53.17 §4). The code is `cloud/internal/obs`; its test
(`obs_test.go`) fails on a series or a label this file does not name. Epic 50.10 owns the alert
rules (`deploy/cloud-ha/monitoring/alerts.yml`) and the dashboards.

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `maktaba_errors_total` | counter | `service`, `code` | error lines, by error code: the 53.01 `err.code`, else the line's `code`, else its `event`, else `unknown` |
| `maktaba_http_requests_total` | counter | `service`, `route`, `status_class` | requests to the API and the relay |
| `maktaba_http_request_seconds` | histogram | `service`, `route`, `status_class` | their latency (buckets 5 ms to 10 s) |
| `maktaba_log_lines_total` | counter | `service`, `level` | log lines written |
| `maktaba_log_dropped_total` | counter | `service` | log lines the log path dropped |
| `maktaba_log_forward_lag_seconds` | gauge | `service` | how far behind forwarding to the witness is |
| `maktaba_problem_reports_total` | counter | `result` | problem reports homes sent (53.16): `created`, `duplicate`, `refused` |

- `service` is the process's: `cloud.api`, `cloud.relay`, `cloud.worker` or `cloud.logs`. The
  node comes from Prometheus' own target labels, not from the series.
- `route` is the route's pattern (`/v1/homes/{id}/reports`; the relay's proxy is `/*`), never
  a path. A request no route matched is `unmatched`.
- `status_class` is `2xx` to `5xx`; `level` is `debug`, `info`, `warn` or `error`.
- No label names a home, a slug, an account, an email or an address. A home's own metrics
  never come here: a home keeps its own (53.12).

## Broadcasting (Epic 50 story 50.28)

The broadcast edge's own numbers, polled from Workers Analytics Engine once a minute by the
worker role into `broadcast_monitor` and served from that snapshot (`cloud/internal/broadcast`,
`Monitor.WriteMetrics`). They carry no labels at all: no channel, publisher or viewer. They are
served only to a scrape addressed by IP (the node's private site that Prometheus scrapes), never
on a public hostname's `/metrics`.

| Series | Type | Labels | Meaning |
|---|---|---|---|
| `maktaba_broadcast_channels_live` | gauge | none | channels that ingested a segment in the last polled minute |
| `maktaba_broadcast_viewers` | gauge | none | viewers watching in that minute (heartbeats) |
| `maktaba_broadcast_viewer_minutes_total` | counter | none | viewer-minutes counted from heartbeats |
| `maktaba_broadcast_ingest_lag_seconds` | histogram | none | from a segment's upload starting to its being in the playlist (buckets 0.25 s to 30 s) |
| `maktaba_broadcast_revocation_list_age_seconds` | gauge | none | seconds since the edge last took the revocation list (50.25); absent until it first does |
| `maktaba_broadcast_edge_cost_estimate_usd` | gauge | none | this month's estimated edge cost so far (README §14.4's model) |
| `maktaba_broadcast_monthly_budget_usd` | gauge | none | `broadcast.monthly_budget_usd`; absent when unset |

The canary broadcast's viewer probe pushes `broadcast_probe_success`,
`broadcast_probe_startup_seconds`, `broadcast_probe_glass_to_glass_seconds` and
`broadcast_probe_segment_age_seconds` to the witness's Pushgateway (job `broadcast_probe`,
instance `actions` or `witness`); `deploy/cloud-ha/monitoring/broadcast-probe.sh`.
