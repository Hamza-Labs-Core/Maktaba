# Implementation Plan — Story 30.4 Metrics export

> Companion to [story-30-04-metrics-export.md](story-30-04-metrics-export.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Export endpoint | `GET /v1/admin/metrics/export` in `handlers/metrics` (admin-gated, api role). |
| Prometheus | `metrics.PrometheusHandler(collector)` mounted at `GET /metrics` on the relay role (before the proxy catch-all). |
| Rendering | `metrics/export.go` (CSV/JSON) and `metrics/prometheus.go` (text exposition) — both pure, unit-tested. |

## 1. export.go — CSV/JSON rendering (pure)

```go
type ExportRow struct {
    Hour string `json:"hour"`; Metric string `json:"metric"`
    Country string `json:"country"`; SumValue int64 `json:"sum_value"`
    MaxValue int64 `json:"max_value"`; Samples int `json:"samples"`
}
func RenderCSV(w io.Writer, rows []ExportRow) error   // header + lines
func RenderJSON(w io.Writer, rows []ExportRow) error  // json array
```

Handler: `Store.ExportRows(ctx, start)` then render by `format`
(`csv`→`text/csv` with `Content-Disposition`; anything else→JSON, README
30.4 default).

## 2. prometheus.go — exposition (pure)

`Render(w io.Writer, s PromSnapshot)` emits, for each series, a `# HELP`,
`# TYPE`, and value line:

```
# HELP maktaba_relay_connected_servers Currently connected home servers.
# TYPE maktaba_relay_connected_servers gauge
maktaba_relay_connected_servers 3
# TYPE maktaba_relay_bandwidth_in_bytes_total counter
maktaba_relay_bandwidth_in_bytes_total 1000
…
```

Counter series use the `_total` suffix; gauges do not. `PrometheusHandler`
sets `Content-Type: text/plain; version=0.0.4` and writes `Render`.

## 3. Tests

`export_test.go`: `RenderCSV` header + row formatting + empty set;
`RenderJSON` array shape; unknown-format default path (via the handler's
format switch, exercised as a pure helper). `prometheus_test.go`: output
contains the expected `# TYPE` lines and values; counters carry `_total`;
parses line-by-line.
