# Implementation Plan — Story 21.2 Metrics Surface

> Companion to [story-21-02-metrics-surface.md](story-21-02-metrics-surface.md).
> Per-service `/metrics`; baseline metric set; cardinality lint; native histograms;
> localhost-only by default.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | Go: `prometheus/client_golang` v2; Python: `prometheus_client`; TS: opt-in browser POST. |
| Ports | api 9100, streaming 9101, pipeline 9102 — **bound by plan-21-04** (admin-port mux owner). This plan only registers `/metrics` against the shared mux. Bind `127.0.0.1` by default. |
| Mux ownership | `shared/admin/mux.go` is owned by **plan-21-04**. `/healthz` and `/readyz` register there too; one process binds the admin port and routes coexist on the same mux. |
| Native histograms | Prometheus 2.40+ exponential native; fallback to fixed buckets if `--enable-feature=native-histograms` not advertised. |
| Cardinality lint | Static walker over `MetricVec` `WithLabelValues`/`Labels` plus registration sites. Banned-label allowlist is config-driven (`shared/metrics/cardinality_allowlist.yaml`), not hard-coded. |
| Auth | Localhost binding by default; opt-in `expose_metrics_publicly: true` enables bearer auth. `/metrics` and `/healthz`/`/readyz` follow the same admin-port auth posture. |

## 1. Project layout

```
shared/metrics/
├── go/
│   ├── registry.go
│   ├── http.go             # /metrics handler + bearer
│   ├── histograms.go       # native + fallback factory
│   ├── lint/
│   │   └── cardinality_lint.go
│   ├── http_test.go
│   └── README.md
└── py/
    ├── registry.py
    ├── http.py
    └── tests/

api/cmd/api/metrics_init.go
streaming/cmd/streaming/metrics_init.go
pipeline/maktaba_pipeline/metrics_init.py
```

## 2. Histogram factory

```go
// shared/metrics/go/histograms.go
package metrics

var FixedMSBuckets = []float64{1,2.5,5,10,25,50,100,250,500,1000,2500,5000,10000}

func NewLatencyHistogram(name, help string, labels []string) *prometheus.HistogramVec {
    return promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
        Name: name + "_seconds", Help: help,
        NativeHistogramBucketFactor: 1.1,
        NativeHistogramMaxBucketNumber: 100,
        NativeHistogramMinResetDuration: 24*time.Hour,
        Buckets: msToS(FixedMSBuckets),
    }, labels)
}
```

When the runtime reports native-histogram support, the buckets are ignored; fallback otherwise.

## 3. Baseline metrics — Go

**Naming convention (canonical).** HTTP-request metrics use the **shared
name** `http_request_duration_seconds` across api/streaming, distinguished
by a `service` label (`service="api"` etc.) that is added at registry
init time via a constant-label option. Per-service prefixes
(`api_request_duration_seconds`, `streaming_request_duration_seconds`)
are explicitly avoided so dashboards and alert rules can use a single
`sum by (service, route_template)(rate(http_request_duration_seconds_bucket[…]))`
expression. Service-internal metrics (transcoding, pipeline) keep their
service-prefixed names because they are not cross-service comparable.

This decision supersedes story AC1's literal phrasing; the AC is met by
the `service` label, which provides identical groupability with lower
cardinality risk than a metric per service.

```go
// shared/metrics/go/registry.go
var (
    HTTPRequestDuration = NewLatencyHistogram(
        "http_request_duration",
        "HTTP request handler duration",
        []string{"method", "route_template", "status_class"},
    )
    HTTPInFlight = promauto.With(reg).NewGauge(prometheus.GaugeOpts{
        Name: "http_in_flight_requests", Help: "In-flight HTTP requests",
    })
    DBQueryDuration = NewLatencyHistogram(
        "db_query_duration", "Database query duration",
        []string{"query_name"},
    )
    DBQueryCount = promauto.With(reg).NewHistogramVec(prometheus.HistogramOpts{
        Name: "db_query_count", Help: "Per-request DB query count",
        Buckets: []float64{0,1,2,3,5,8,13,21,34},
    }, []string{"route"})
    CacheHits = promauto.With(reg).NewCounterVec(
        prometheus.CounterOpts{Name: "cache_hits_total", Help: "Cache hits"},
        []string{"cache"})
    CacheMisses = promauto.With(reg).NewCounterVec(
        prometheus.CounterOpts{Name: "cache_misses_total", Help: "Cache misses"},
        []string{"cache"})
    PipelineJobs = promauto.With(reg).NewCounterVec(
        prometheus.CounterOpts{Name: "pipeline_jobs_total", Help: "Pipeline jobs"},
        []string{"stage", "result"})
    TranscodeActiveSessions = promauto.With(reg).NewGauge(prometheus.GaugeOpts{
        Name: "transcode_active_sessions", Help: "Active transcoding sessions",
    })
    TranscodeQueueDepth = promauto.With(reg).NewGauge(prometheus.GaugeOpts{
        Name: "transcode_queue_depth", Help: "Pending transcodes",
    })
)
```

## 4. `/metrics` registration on the shared admin mux

The admin-port mux (`shared/admin/mux.go`) is owned by **plan-21-04**. This
plan does **not** call `http.ListenAndServe`; instead it registers the
`/metrics` route against the shared mux that plan-21-04 binds. Bearer
auth is applied at the mux's auth layer (also owned by plan-21-04) so
that `/metrics` and `/readyz` share a single auth posture.

```go
// shared/metrics/go/http.go
type Config struct {
    Public      bool
    BearerToken string  // required if Public
}

// Register attaches /metrics to the admin-mux owned by plan-21-04.
// The admin server (one per service) binds 127.0.0.1:9100/9101/9102.
func Register(adminMux *adminmux.Mux, cfg Config) error {
    h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{
        EnableOpenMetrics: true, ProcessStartTime: time.Now(),
    })
    if cfg.Public {
        if cfg.BearerToken == "" { return errors.New("public metrics requires a bearer token") }
        h = bearerWrap(cfg.BearerToken, h)
    }
    adminMux.Handle("/metrics", h)
    return nil
}
```

`bearerWrap` checks `Authorization: Bearer <tok>` constant-time.

Cross-link: `shared/admin/mux.go` declared and bound by
[plan-21-04 §1](plan-21-04-health-readiness-probes.md). Caddyfile / systemd
exposure documented in plan-22-03.

## 5. Cardinality lint

The lint reads its banned-label regex and per-label allowlist from
`shared/metrics/cardinality_allowlist.yaml` so new bounded-cardinality
labels (e.g., `kid_id`) can be added without code changes:

```yaml
# shared/metrics/cardinality_allowlist.yaml
banned_labels:
  - id
  - user_id
  - video_id
  - session_id
  - path
  - url
  - library_id
  - email
banned_suffixes:
  - _id
allow:
  # Bounded sets that pass the suffix filter. Each entry has a reason.
  - label: kid_id
    reason: "DRM key ids are bounded by issuance cadence (≤100/day)."
```

```go
// shared/metrics/go/lint/cardinality_lint.go
//go:build cardlint

type Rules struct {
    BannedLabels   []string `yaml:"banned_labels"`
    BannedSuffixes []string `yaml:"banned_suffixes"`
    Allow          []struct {
        Label  string `yaml:"label"`
        Reason string `yaml:"reason"`
    } `yaml:"allow"`
}

func main() {
    rules := loadRules("shared/metrics/cardinality_allowlist.yaml")
    bannedLabel := regexp.MustCompile("^(" + strings.Join(rules.BannedLabels, "|") + ")$")
    bannedSuffix := regexp.MustCompile("(" + strings.Join(rules.BannedSuffixes, "|") + ")$")
    allowed := map[string]bool{}
    for _, a := range rules.Allow { allowed[a.Label] = true }

    fset := token.NewFileSet()
    pkgs, _ := packages.Load(...)
    var fail bool
    for _, p := range pkgs {
        for _, f := range p.Syntax {
            ast.Inspect(f, func(n ast.Node) bool {
                comp, ok := n.(*ast.CompositeLit); if !ok { return true }
                t := selectorString(comp.Type)
                if t != "prometheus.HistogramOpts" && t != "prometheus.CounterOpts" && t != "prometheus.GaugeOpts" {
                    return true
                }
                for _, lab := range labelsFor(comp, p) {
                    if allowed[lab] { continue }
                    if bannedLabel.MatchString(lab) || bannedSuffix.MatchString(lab) {
                        fmt.Fprintf(os.Stderr, "FAIL: high-cardinality label %q at %s\n", lab, fset.Position(comp.Pos()))
                        fail = true
                    }
                }
                return true
            })
        }
    }
    if fail { os.Exit(1) }
}
```

Allowlist override at the call site: a comment
`// metrics:allow-label-cardinality reason="bounded set"` on the line
registers a one-off exception (in addition to the YAML allowlist).

## 6. Streaming/pipeline-specific metrics

```go
// streaming/internal/metrics/streaming.go
var (
    OpenSessionDuration       = metrics.NewLatencyHistogram("streaming_open_session_duration", "OpenSession", nil)
    ManifestDuration          = metrics.NewLatencyHistogram("streaming_manifest_duration", "manifest build", nil)
    SegmentFirstByte          = metrics.NewLatencyHistogram("streaming_segment_first_byte", "segment first byte", []string{"state"})
    BufferUnderruns           = promauto.With(metrics.Reg).NewCounter(prometheus.CounterOpts{Name: "streaming_buffer_underruns_total", Help: "..."})
)
```

```python
# pipeline/maktaba_pipeline/metrics_init.py
PIPELINE_STAGE_DURATION = Histogram(
    "pipeline_stage_duration_seconds",
    "Per-stage wall-clock duration",
    labelnames=("stage",),
    buckets=(0.1, 0.5, 1, 5, 10, 30, 60, 120, 300, 600, 1800, 3600, 7200),
)
```

## 7. Web vitals endpoint (browser opt-in)

Route registered on the **public API mux** (not the admin mux) so the
browser can reach it. The handler is gated by `WebVitalsEnabled` and a
per-session rate limiter:

```go
// api/internal/router/router.go (excerpt)
r.Post("/api/web-vitals", handlers.WebVitals)   // browser opt-in beacon
```

```go
// api/internal/handlers/web_vitals.go
type WebVitalsIn struct {
    LCPMs float64 `json:"lcp_ms"`
    FIDMs float64 `json:"fid_ms"`
    CLS   float64 `json:"cls"`
    TTFB  float64 `json:"ttfb_ms"`
    Route string  `json:"route_template"`     // "/library/:id/...", never raw
}

func (h *Handler) WebVitals(w http.ResponseWriter, r *http.Request) {
    if !h.cfg.WebVitalsEnabled { http.Error(w, "disabled", 403); return }
    if !h.rateLimiter.Allow(sessionID(r), 1, 5*time.Minute) { http.Error(w, "rate", 429); return }
    var in WebVitalsIn
    _ = json.NewDecoder(r.Body).Decode(&in)
    metrics.WebLCPMs.WithLabelValues(in.Route).Observe(in.LCPMs)
    metrics.WebFIDMs.WithLabelValues(in.Route).Observe(in.FIDMs)
    w.WriteHeader(204)
}
```

`route_template` is server-side validated against a known whitelist to bound cardinality.

## 8. Test cases

### TC1 — Cardinality lint
A registration with label `video_id` runs `make lint:metrics`; assert exit non-zero with file:line.

### TC2 — Schema test
Boot the API with no traffic; `curl localhost:9100/metrics`; parse with `prometheus/parser`; assert each documented metric is present.

### TC3 — Network exposure
Default config: `curl http://example.com:9100/metrics` → connection refused (localhost-only). With `expose_metrics_publicly: true` and bearer `tok`: `curl -H "Authorization: Bearer tok" http://example.com:9100/metrics` → 200.

### EC1 — FFmpeg dropped
Test scenario: open session, FFmpeg starts, client TCP closed. `transcode_active_sessions` reads via session reaper not goroutine; assert gauge stays ≥ 0 and decrements to 0 only after reaper runs.

### EC2 — Counter reset on restart
Restart service; counter starts from 0. Doc note in `shared/metrics/README.md`: prefer `rate(name[5m])` not absolute differences.

### EC3 — Web vitals rate limit
Issue 2 POSTs in 5 min from same session. First → 204. Second → 429.

## 9. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 dropped client / FFmpeg | story | Reaper-driven gauge update. |
| EC2 restart counters | story | Doc note. |
| EC3 web telemetry | story | Opt-in flag + rate limit. |
| Native histograms unsupported on consumer | impl | Fallback fixed buckets always present. |
| Bearer leaked in logs | impl | Token field is `sensitive=true` per Story 21.8. |

## 10. Configuration

```yaml
metrics:
  bind: 127.0.0.1:9100
  expose_metrics_publicly: false
  bearer_token_env: MAKTABA_METRICS_BEARER
  web_vitals_enabled: false
  web_vitals_rate_per_5min: 1
```

## 11. Dependencies

- Story 21.1 (loggers attach `trace_id` for cross-correlation).
- Story 21.4 (`/healthz` and `/readyz` parallel admin port).
- Story 18.x (every perf story registers a metric here).
- Story 21.8 (no PII labels).
