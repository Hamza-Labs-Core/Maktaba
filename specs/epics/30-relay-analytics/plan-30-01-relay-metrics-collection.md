# Implementation Plan — Story 30.1 Relay metrics collection

> Companion to [story-30-01-relay-metrics-collection.md](story-30-01-relay-metrics-collection.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Migration | `cloud/migrations/00110001_relay_metrics.sql`: `relay_metrics_raw` + `relay_metrics_hourly`. |
| Package | `cloud/internal/metrics/` (collector, store, rollup, names, prometheus, export). |
| Wiring | `cmd/maktaba-cloud/role_relay.go`: build `Store` + `Collector` + `Runner`; pass `Collector` to the relay proxy `Deps`. |
| Hot path | `handlers/relay/proxy.go` records bytes + request(country) into the collector after each proxied request. |

## 1. Migration (00110001)

Two tables per README data model. `relay_metrics_raw` has `UNIQUE
(bucket, metric, country)` for the upsert; index on `bucket` for purge.
`relay_metrics_hourly` PK `(hour, metric, country)`; index `(metric,
hour)` for series reads. Down drops both.

## 2. names.go — metric identifiers

```go
const (
    MetricConnectedServers = "connected_servers"  // gauge
    MetricActiveTunnels    = "active_tunnels"      // gauge
    MetricBandwidthIn      = "bandwidth_in_bytes"  // counter
    MetricBandwidthOut     = "bandwidth_out_bytes" // counter
    MetricRequests         = "requests"            // counter (by country)
    MetricPushSent         = "push_sent"           // counter
    MetricPushFailed       = "push_failed"         // counter
)
```

## 3. collector.go — the in-memory core (unit-tested, no DB)

- Process-cumulative `atomic.Int64` per counter + gauge (Prometheus view, D7).
- `mu`-guarded `delta map[key]*acc` where `key{metric,country}` and
  `acc{value int64; samples int}` (DB view, resets on snapshot).
- `Record*` methods update both views:
  `RecordBandwidth(in,out)`, `RecordRequest(country)`, `RecordPush(ok)`,
  `ObserveConnections(servers,tunnels)`.
- `Snapshot(now) []RawRow` — copies + resets the delta map, stamping
  `bucket = now.Truncate(time.Minute)`.
- `PromSnapshot()` — reads the atomics/gauges for the exposition.

## 4. store.go — DB layer (thin, not unit-tested without a DB)

- `FlushRaw(ctx, rows)` — upsert into `relay_metrics_raw`
  `ON CONFLICT (bucket,metric,country) DO UPDATE SET value=value+EXCLUDED.value, samples=samples+EXCLUDED.samples`.
- `Rollup(ctx, now)` — aggregate completed-hour raw → hourly (overwrite
  upsert); idempotent.
- `PurgeRaw(ctx, now)` — delete raw `WHERE bucket < now-24h`.
- Read helpers for 30.3/30.4 live in store.go too (series, geo, totals,
  export).

## 5. rollup.go — background Runner

`Runner.Run(ctx)` loops on a ticker (default 60 s): sample gauges from a
`GaugeSource` (`func() (servers, tunnels int)` backed by
`relay.Registry.Len()`), `Snapshot` + `FlushRaw`; every hour `Rollup` +
`PurgeRaw`; daily `PurgeHourly` (Story 30.2). Errors logged, loop
continues. Final flush on `ctx.Done()`.

## 6. Tests

`collector_test.go`: record/snapshot accumulation, reset-on-flush,
country tagging, cumulative atomics. `names_test.go`: kind mapping.
DB-touching code mirrors the rest of the cloud module (no live-DB unit
tests); correctness of SQL is covered by the build + integration.
