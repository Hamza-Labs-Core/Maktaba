# Implementation Plan — Story 7.20 Health, Version, Metrics

> Companion to [story-07-20-health-version-metrics.md](story-07-20-health-version-metrics.md).
> Operational visibility surface; opt-in OTel.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/system/health`, `GET /api/system/version`, `GET /metrics` (separate listener). |
| DB pool | The health-check uses a tiny dedicated `*pgxpool.Pool` (size 2) so saturation of the application pool doesn't cause cascading 503s. |
| Metrics | Prometheus client lib; one default registry per process. |
| OTel | Opt-in via config; otel exporter uses gRPC OTLP. |
| Out of scope | Alert routing (downstream operations); dashboards. |

## 1. Architecture diagram

```
   /api/system/health (cached 1 s)
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ checkAll() → {db, pipeline, streaming}                     │
   │   db:        SELECT 1 FROM goose_db_version LIMIT 1        │
   │              + schema-revision compare                     │
   │   pipeline:  pipelineClient.HealthCheck(ctx, 500ms)        │
   │   streaming: streamingClient.HealthCheck(ctx, 500ms)       │
   │ status = worst(component_statuses)                         │
   │ httpStatus = 200 (ok|degraded) | 503 (down)                │
   └────────────────────────────────────────────────────────────┘

   /api/system/version
        │
        ▼ in-process constants populated by ldflags

   /metrics (port = metrics_listen)
        │
        ▼
   prometheus.DefaultRegisterer collects:
     http_requests_total{route, method, status}
     http_request_duration_seconds (histogram)
     grpc_client_calls_total{service, method, code}
     ws_active_connections
     db_pool_in_use, db_pool_idle (pgx pool gauges)
     job_queue_pending{stage}
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/system/health.go` | Composer + cache. |
| `api/internal/system/version.go` | Build-time vars + handler. |
| `api/internal/system/handler.go` | Routes. |
| `api/internal/metrics/registry.go` | Prom collectors + middleware. |
| `api/internal/metrics/server.go` | Separate `http.Server` for `/metrics`. |
| `api/internal/otel/otel.go` | OTel SDK setup (opt-in). |
| `api/internal/system/handler_test.go` | Integration. |
| `shared/db/queries/system.sql` | sqlc inputs (schema rev). |

## 3. Type definitions

```go
// api/internal/system/health.go
package system

import "time"

type ComponentStatus string

const (
    StatusOK       ComponentStatus = "ok"
    StatusDegraded ComponentStatus = "degraded"
    StatusDown     ComponentStatus = "down"
)

type ComponentReport struct {
    Status     ComponentStatus `json:"status"`
    Detail     string          `json:"detail,omitempty"`
    LatencyMS  int64           `json:"latency_ms,omitempty"`
}

type Health struct {
    Status     ComponentStatus            `json:"status"`
    Components map[string]ComponentReport `json:"components"`
    CheckedAt  time.Time                  `json:"checked_at"`
}

type Version struct {
    Version       string `json:"version"`
    BuildSHA      string `json:"build_sha"`
    BuildTime     string `json:"build_time"`
    GoVersion     string `json:"go_version"`
    SchemaRev     int    `json:"schema_revision"`
}
```

## 4. Health composer

```go
// api/internal/system/health.go
package system

import (
    "context"
    "sync"
    "time"
)

type composer struct {
    mu         sync.Mutex
    cached     *Health
    cachedAt   time.Time
    ttl        time.Duration   // 1s
    pipeline   PipelineHealthChecker
    streaming  StreamingHealthChecker
    db         *pgxpool.Pool
    schemaWant int
}

func (c *composer) check(ctx context.Context) *Health {
    c.mu.Lock(); defer c.mu.Unlock()
    if c.cached != nil && time.Since(c.cachedAt) < c.ttl {
        return c.cached
    }

    out := &Health{
        Components: map[string]ComponentReport{},
        CheckedAt:  time.Now(),
    }

    var wg sync.WaitGroup
    wg.Add(3)
    go func() { defer wg.Done(); out.Components["db"]        = c.checkDB(ctx) }()
    go func() { defer wg.Done(); out.Components["pipeline"]  = c.checkRPC(ctx, c.pipeline) }()
    go func() { defer wg.Done(); out.Components["streaming"] = c.checkRPC(ctx, c.streaming) }()
    wg.Wait()

    out.Status = worst(out.Components)
    c.cached, c.cachedAt = out, time.Now()
    return out
}

func (c *composer) checkDB(ctx context.Context) ComponentReport {
    ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
    defer cancel()
    t0 := time.Now()
    var rev int
    err := c.db.QueryRow(ctx, "SELECT version_id FROM goose_db_version ORDER BY id DESC LIMIT 1").Scan(&rev)
    lat := time.Since(t0).Milliseconds()
    if err != nil {
        return ComponentReport{Status: StatusDown, Detail: err.Error(), LatencyMS: lat}
    }
    if rev < c.schemaWant {
        return ComponentReport{Status: StatusDegraded,
            Detail: "schema-behind", LatencyMS: lat}
    }
    return ComponentReport{Status: StatusOK, LatencyMS: lat}
}

func worst(m map[string]ComponentReport) ComponentStatus {
    s := StatusOK
    for _, c := range m {
        switch c.Status {
        case StatusDown: return StatusDown
        case StatusDegraded: if s != StatusDown { s = StatusDegraded }
        }
    }
    return s
}
```

## 5. Version

```go
// api/internal/system/version.go
package system

import "runtime"

// Populated via:
//   go build -ldflags "-X maktaba/api/internal/system.Version=$(git describe ...)
//                      -X maktaba/api/internal/system.BuildSHA=$(git rev-parse HEAD)
//                      -X maktaba/api/internal/system.BuildTime=$(date -u +%FT%T)"
var (
    Version   = "dev"
    BuildSHA  = "dev"
    BuildTime = "dev"
)

func versionInfo(schemaRev int) Version {
    return Version{
        Version: Version, BuildSHA: BuildSHA, BuildTime: BuildTime,
        GoVersion: runtime.Version(), SchemaRev: schemaRev,
    }
}
```

## 6. Metrics registry

```go
// api/internal/metrics/registry.go
package metrics

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    HTTPRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "http_requests_total",
        Help: "Total HTTP requests processed.",
    }, []string{"route", "method", "status"})

    HTTPRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
        Name: "http_request_duration_seconds",
        Help: "HTTP request duration.",
        Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
    }, []string{"route", "method"})

    GRPCClientCalls = promauto.NewCounterVec(prometheus.CounterOpts{
        Name: "grpc_client_calls_total",
        Help: "Outbound gRPC calls.",
    }, []string{"service", "method", "code"})

    WSActive = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "ws_active_connections",
        Help: "Currently open WebSocket connections.",
    })

    DBPoolInUse = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "db_pool_in_use", Help: "DB connections currently in use.",
    })
    DBPoolIdle = promauto.NewGauge(prometheus.GaugeOpts{
        Name: "db_pool_idle", Help: "DB connections currently idle.",
    })
    JobQueuePending = promauto.NewGaugeVec(prometheus.GaugeOpts{
        Name: "job_queue_pending", Help: "Pending jobs by stage.",
    }, []string{"stage"})
)
```

A middleware records HTTP timings:

```go
func InstrumentHandler(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        sw := &statusWriter{ResponseWriter: w}
        t0 := time.Now()
        next.ServeHTTP(sw, r)
        route := chi.RouteContext(r.Context()).RoutePattern()
        HTTPRequestDuration.WithLabelValues(route, r.Method).Observe(time.Since(t0).Seconds())
        HTTPRequestsTotal.WithLabelValues(route, r.Method, strconv.Itoa(sw.status)).Inc()
    })
}
```

A goroutine snapshots `pgxpool.Pool.Stat()` once per second and sets the
gauges; another snapshots `processing_jobs` pending counts per stage
(reusing Story 7.13's query) once per 10 s.

## 7. Metrics server

```go
// api/internal/metrics/server.go
package metrics

import (
    "net/http"
    "github.com/prometheus/client_golang/prometheus/promhttp"
)

func Start(addr, adminToken string) (*http.Server, error) {
    mux := http.NewServeMux()
    h := promhttp.Handler()
    if adminToken != "" {
        h = withToken(h, adminToken)
    }
    mux.Handle("/metrics", h)
    s := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5*time.Second}
    go func() { _ = s.ListenAndServe() }()
    return s, nil
}
```

`metrics_listen` defaults to `127.0.0.1:9090`. When `metrics_admin_token`
is set, callers must provide `Authorization: Bearer <token>`.

## 8. OTel setup

```go
// api/internal/otel/otel.go
package otel

import (
    "context"

    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/resource"
    sdktrace "go.opentelemetry.io/otel/sdk/trace"
    semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func Init(ctx context.Context, endpoint string) (func(context.Context) error, error) {
    exp, err := otlptracegrpc.New(ctx, otlptracegrpc.WithEndpoint(endpoint), otlptracegrpc.WithInsecure())
    if err != nil { return nil, err }
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exp),
        sdktrace.WithResource(resource.NewSchemaless(
            semconv.ServiceName("maktaba-api"),
        )),
    )
    otel.SetTracerProvider(tp)
    return tp.Shutdown, nil
}
```

A chi middleware wraps each route with an `otelhttp` span that adds
`http.route` and `http.status_code` attributes; gRPC is instrumented in
Story 7.18 already.

## 9. Test plan

### 9.1 Integration

| Test | What it pins |
|---|---|
| `TestHealthOK` | Live DB, fake Pipeline + Streaming OK → 200, all components ok. |
| `TestHealthPipelineDown` | Kill Pipeline gRPC → `pipeline: down`, overall `degraded`, HTTP 200. |
| `TestHealthDBDown` | Block the dedicated health pool's connection → `db: down`, overall `down`, HTTP 503; the listener still returns. |
| `TestHealthCached` | 100 calls in 1 s → exactly 1 Pipeline gRPC call. |
| `TestHealthSchemaBehind` | `goose_db_version` shows v14 but config wants v15 → `db: degraded` with `detail: schema-behind`. |
| `TestVersionShape` | Build flags set → response has populated fields; `go_version` matches `runtime.Version()`. |
| `TestMetricsExposes` | After 10 requests, `/metrics` shows `http_requests_total` with appropriate label values. |
| `TestMetricsAdminToken` | Token configured → `/metrics` 401 without it; 200 with it. |
| `TestMetricsLocalhostOnly` | Default config binds to `127.0.0.1`; test asserts external interface refused. |
| `TestOTelSpan` | Single request → one span with `http.route`, `http.status_code`; a downstream gRPC call inherits as child span. |
| `TestSchemaMismatchBlocksWrites` | DB at v14, binary expects v15 → PATCH returns 503 `schema-out-of-date`; GET still works. |

## 10. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Health-check storm (k8s liveness) | 1 s cache; 100 calls / 1 s → one downstream call. | `TestHealthCached` |
| Postgres down but listener still up | Health returns 503 with `db: down`; the dedicated 2-conn pool prevents app-pool starvation cascade. | `TestHealthDBDown` |
| OTel exporter endpoint unreachable | Init logs a warning; spans are dropped at the batcher; the API does not block. | Manual |
| `metrics` port collision | `Start` returns error; `main` aborts startup. | Startup test |
| `version.go` ldflags omitted | Defaults remain `"dev"`; the response carries them. | `TestVersionShape` (variant) |
| Schema rev ahead of binary | We treat `rev > want` as ok (no degradation); reverse triggers `degraded`. | Documented |

## 11. Acceptance checklist

- [ ] Health composes db + pipeline + streaming; HTTP status reflects worst component (200/200/503).
- [ ] Health cached 1 s; storms don't multiply downstream calls.
- [ ] Schema-revision mismatch surfaces as degraded; PATCH returns 503 `schema-out-of-date`.
- [ ] Version endpoint returns `version, build_sha, build_time, go_version, schema_revision`.
- [ ] `/metrics` listens on `metrics_listen` (default `127.0.0.1:9090`).
- [ ] OTel opt-in via `[telemetry].otel_endpoint`.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.20.
