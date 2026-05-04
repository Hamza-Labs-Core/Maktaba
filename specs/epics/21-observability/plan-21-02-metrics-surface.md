# Implementation Plan — Story 21.2 Metrics Surface

> Companion to [story-21-02-metrics-surface.md](story-21-02-metrics-surface.md).
> Per-service `/metrics`; baseline metric set; cardinality lint; native histograms;
> localhost-only by default.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Library | Go: `prometheus/client_golang` v2; Python: `prometheus_client`; TS: opt-in browser POST. |
| Ports | api 9100, streaming 9101, pipeline 9102. Bind `127.0.0.1` by default. |
| Native histograms | Prometheus 2.40+ exponential native; fallback to fixed buckets if `--enable-feature=native-histograms` not advertised. |
| Cardinality lint | Static walker over `MetricVec` `WithLabelValues`/`Labels` plus registration sites. |
| Auth | Localhost binding by default; opt-in `expose_metrics_publicly: true` enables bearer auth. |

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

## 4. HTTP handler with bearer auth (opt-in)

```go
// shared/metrics/go/http.go
type Config struct {
    Bind            string
    Public          bool
    BearerToken     string  // required if Public
}

func Serve(cfg Config) error {
    mux := http.NewServeMux()
    h := promhttp.HandlerFor(reg, promhttp.HandlerOpts{EnableOpenMetrics: true, ProcessStartTime: time.Now()})
    if cfg.Public {
        if cfg.BearerToken == "" { return errors.New("public metrics requires a bearer token") }
        h = bearerWrap(cfg.BearerToken, h)
    }
    mux.Handle("/metrics", h)
    return http.ListenAndServe(cfg.Bind, mux)
}
```

`bearerWrap` checks `Authorization: Bearer <tok>` constant-time.

## 5. Cardinality lint

```go
// shared/metrics/go/lint/cardinality_lint.go
//go:build cardlint

var bannedLabel = regexp.MustCompile(`^(id|user_id|video_id|session_id|path|url|library_id|email)$`)
var bannedSuffix = regexp.MustCompile(`_id$`)

func main() {
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
                // Look for parent NewXxxVec call; arg list contains label names.
                // Walk up via path; details elided for brevity.
                for _, lab := range labelsFor(comp, p) {
                    if bannedLabel.MatchString(lab) || (bannedSuffix.MatchString(lab) && lab != "kid_id") {
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

Allowlist override: a comment `// metrics:allow-label-cardinality reason="bounded set"` on the line registers an exception.

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
