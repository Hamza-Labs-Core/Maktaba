# Implementation Plan — Story 21.3 Distributed Tracing

> Companion to [story-21-03-distributed-tracing.md](story-21-03-distributed-tracing.md).
> OTel across web → API → gRPC → pipeline/streaming → DB; head sampler;
> opt-in OTLP; PII-safe spans.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| SDK | Go: `go.opentelemetry.io/otel`. Python: `opentelemetry-api`/`-sdk`. TS: `@opentelemetry/sdk-trace-web`. |
| Propagation | W3C `traceparent` and `tracestate`; `b3` not supported in v1. |
| Sampler | Composite: `AlwaysSample` for spans tagged `error=true` or `slow=true`; otherwise `TraceIDRatioBased(0.01)`. |
| Exporter | OTLP/HTTP at `[telemetry].otlp_endpoint`; absent → `NoopExporter`. |
| PII | Span attributes never carry request bodies or full URLs; query strings hashed. |

## 1. Project layout

```
shared/tracing/
├── go/
│   ├── tracer.go          # init + sampler
│   ├── http_middleware.go
│   ├── grpc_interceptor.go
│   ├── pgx_tracer.go
│   ├── sampler.go         # head sampler with slow tagging
│   ├── attrs.go           # safe attr helpers
│   └── tracer_test.go
├── py/
│   ├── tracer.py
│   ├── grpc_server_interceptor.py
│   └── tests/
└── ts/
    └── tracer.ts          # browser

api/internal/middleware/tracing.go
streaming/internal/middleware/tracing.go
pipeline/maktaba_pipeline/tracing/init.py
web/src/lib/tracing.ts
```

## 2. Go init

```go
// shared/tracing/go/tracer.go
func Init(service, env, endpoint string) (func(context.Context) error, error) {
    if endpoint == "" {
        otel.SetTracerProvider(noop.NewTracerProvider())
        return func(context.Context) error { return nil }, nil
    }
    res, _ := resource.New(context.Background(),
        resource.WithAttributes(
            semconv.ServiceName(service),
            semconv.ServiceVersion(buildinfo.Version),
            semconv.DeploymentEnvironmentKey.String(env),
        ),
    )
    exp, err := otlptracehttp.New(context.Background(),
        otlptracehttp.WithEndpoint(endpoint),
        otlptracehttp.WithTimeout(5*time.Second),
    )
    if err != nil { return nil, err }

    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exp,
            sdktrace.WithMaxQueueSize(8192),
            sdktrace.WithMaxExportBatchSize(512),
        ),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(NewSampler()),
    )
    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.TraceContext{})
    return tp.Shutdown, nil
}
```

## 3. Sampler with slow & error always-on

```go
// shared/tracing/go/sampler.go
type composite struct{ ratio sdktrace.Sampler }

func NewSampler() sdktrace.Sampler {
    return &composite{ratio: sdktrace.TraceIDRatioBased(0.01)}
}

func (s *composite) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
    for _, a := range p.Attributes {
        if (a.Key == "error" && a.Value.AsBool()) || (a.Key == "slow" && a.Value.AsBool()) {
            return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
        }
    }
    return s.ratio.ShouldSample(p)
}
func (s *composite) Description() string { return "composite head sampler" }
```

`slow=true` is set by HTTP middleware when wall-clock exceeds the route's p95 budget.

## 4. HTTP middleware tagging slow

```go
// shared/tracing/go/http_middleware.go
func HTTP(routeBudget func(string) time.Duration) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        h := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            t0 := time.Now()
            ww := wrapWriter(w)
            next.ServeHTTP(ww, r)
            dur := time.Since(t0)
            sp := trace.SpanFromContext(r.Context())
            if rb := routeBudget(routeTemplateOf(r)); rb > 0 && dur > rb {
                sp.SetAttributes(attribute.Bool("slow", true))
            }
            if ww.status >= 500 {
                sp.SetAttributes(attribute.Bool("error", true))
            }
            sp.SetAttributes(attribute.String("http.route_template", routeTemplateOf(r)))
            // hash q (EC3)
            if q := r.URL.RawQuery; q != "" {
                h := sha256.Sum256([]byte(q))
                sp.SetAttributes(attribute.String("http.url.query_hash", hex.EncodeToString(h[:8])))
            }
        }), "http")
        return h
    }
}
```

## 5. gRPC interceptors

```go
// shared/tracing/go/grpc_interceptor.go
func ServerOpts() []grpc.ServerOption {
    return []grpc.ServerOption{
        grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
        grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
    }
}
func ClientDialOpts() []grpc.DialOption {
    return []grpc.DialOption{
        grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
        grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
    }
}
```

## 6. Postgres tracer

```go
// shared/tracing/go/pgx_tracer.go
import "github.com/exaring/otelpgx"

func PgxOptions() *pgxpool.Config {
    cfg, _ := pgxpool.ParseConfig(dsn)
    cfg.ConnConfig.Tracer = otelpgx.NewTracer(otelpgx.WithIncludeQueryParameters(false))
    return cfg
}
```

`IncludeQueryParameters(false)` ensures DB args (which can be PII) are not put in span attributes.

## 7. Python tracer

```python
# pipeline/maktaba_pipeline/tracing/init.py
def init(service: str, env: str, endpoint: str | None) -> None:
    if not endpoint:
        trace.set_tracer_provider(NoOpTracerProvider())
        return
    resource = Resource.create({
        SERVICE_NAME: service,
        SERVICE_VERSION: VERSION,
        DEPLOYMENT_ENVIRONMENT: env,
    })
    provider = TracerProvider(resource=resource, sampler=_make_sampler())
    provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint)))
    trace.set_tracer_provider(provider)
    set_global_textmap(TraceContextTextMapPropagator())
    GrpcInstrumentorServer().instrument()
    Psycopg2Instrumentor().instrument(skip_dep_check=True, enable_commenter=False)
```

## 8. Browser tracer

```ts
// web/src/lib/tracing.ts
import { WebTracerProvider } from '@opentelemetry/sdk-trace-web';
import { OTLPTraceExporter } from '@opentelemetry/exporter-trace-otlp-http';
import { BatchSpanProcessor } from '@opentelemetry/sdk-trace-base';
import { W3CTraceContextPropagator } from '@opentelemetry/core';
import { propagation } from '@opentelemetry/api';

export function initTracing(endpoint?: string) {
    if (!endpoint) return;
    const provider = new WebTracerProvider();
    provider.addSpanProcessor(new BatchSpanProcessor(new OTLPTraceExporter({ url: endpoint })));
    provider.register();
    propagation.setGlobalPropagator(new W3CTraceContextPropagator());
}

export function instrumentSearch(fn: (q: string) => Promise<unknown>) {
    return async (q: string) => {
        const tracer = getTracer('web');
        return await tracer.startActiveSpan('search', async (span) => {
            span.setAttribute('q.length', q.length);              // size only, not content
            try { return await fn(q); }
            catch (e) { span.recordException(e as Error); span.setAttribute('error', true); throw e; }
            finally { span.end(); }
        });
    };
}
```

## 9. Buffer cap & circuit (EC1)

```go
// shared/tracing/go/tracer.go (excerpt)
otlptracehttp.WithRetry(otlptracehttp.RetryConfig{
    Enabled:         true,
    InitialInterval: 1 * time.Second,
    MaxInterval:     10 * time.Second,
    MaxElapsedTime:  60 * time.Second,
}),
```

`MaxQueueSize` (8192 spans × ~1 KiB each ≈ 8 MiB) bounds memory; once full, further spans dropped with metric `otel_traces_dropped_total` increment. Never blocks the calling goroutine.

## 10. Test cases

### TC1 — End-to-end
Compose stack with OTLP collector echoing to a file. Issue web search. Inspect collector output: a single trace contains spans `web.search → POST /api/search → pipeline.Embed → postgres exec → chroma.query`.

### TC2 — Sampling
Enable tracing. Issue 1,000 fast 200-OK requests. Collector receives ~10 traces (within Poisson tolerance). Then issue one slow request (force 800 ms). Receive an additional trace tagged `slow=true`.

### TC3 — Disabled-by-default
Boot fresh; `[telemetry].otlp_endpoint` empty. `lsof -i -P` on each service shows no outbound conn to any OTLP host. `netstat -na | grep -E "443|4317|4318"` shows no ESTABLISHED beyond expected (DB, gRPC peers).

### EC1 — Endpoint unreachable
Block OTLP host via firewall. Drive load. Observe: spans drop after queue full; `otel_traces_dropped_total` increments; service latency unaffected (within 1 ms p99).

### EC2 — Body bloat
Add a request body of 10 MiB. Inspect span attrs: only `http.request_body.size=10485760`; no body content present.

### EC3 — Query hash
Issue search `?q=بسم الله`. Span attribute `http.url.query_hash` is sha256-prefix; `http.url.full` is absent.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 OTLP unreachable | story | Buffered queue + retry + drop counter. |
| EC2 body in span | story | Helper enforces; lint forbids attr names like `body`/`payload`/`request`. |
| EC3 PII in query | story | Hashed. |
| Cross-service propagation | impl | All HTTP middleware uses W3C `traceparent`. |
| Browser sampler over-samples on errors | impl | Browser also implements composite sampler with same logic. |

## 12. Configuration

```yaml
telemetry:
  outbound_enabled: false
  otlp_endpoint: ""              # e.g., https://otel-collector.local:4318
  sampling:
    base_ratio: 0.01
    always_on:
      - error
      - slow
  resource:
    deployment_environment: prod
```

## 13. Dependencies

- Story 21.1 (logger injects `trace_id`/`span_id` from current span).
- Story 21.4 (`/healthz` excluded from tracing to avoid noise).
- Story 21.8 (privacy: outbound disabled by default; PII redaction).
- Epic 7 (web client owns search instrumentation hook).
