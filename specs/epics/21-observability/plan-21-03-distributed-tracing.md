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

    // Story 21.3 sets the OTLP buffer cap at 10 MiB.
    // 10 MiB / ~1 KiB per span ≈ 10240 spans queued.
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exp,
            sdktrace.WithMaxQueueSize(10_240),
            sdktrace.WithMaxExportBatchSize(512),
        ),
        sdktrace.WithResource(res),
        sdktrace.WithSampler(NewSampler()),
    )
    otel.SetTracerProvider(tp)
    // Composite propagator so both W3C trace context and Baggage flow
    // across HTTP/gRPC and (manually) NOTIFY payloads. Baggage carries
    // optional auxiliary key/value pairs (e.g., `tenant`, `library_id`)
    // that downstream spans can sample on.
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))
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
        handler := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
                sum := sha256.Sum256([]byte(q))
                sp.SetAttributes(attribute.String("http.url.query_hash", hex.EncodeToString(sum[:8])))
            }
        }), "http")
        return handler
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
    # Pipeline DB driver is asyncpg per architecture §2 (line 231); use
    # the matching instrumentor. AsyncPGInstrumentor wraps the asyncpg
    # connection methods to emit DB spans.
    AsyncPGInstrumentor().instrument()
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

## 8.1 Postgres LISTEN/NOTIFY trace continuity

Architecture §1.4 + §7.10 drive WebSocket fan-out via Postgres
`LISTEN/NOTIFY`. The pgx tracer wraps query execution but does not
propagate `traceparent` through NOTIFY payloads, so job-progress traces
break at the bus. To preserve trace continuity end-to-end (worker →
NOTIFY → API listener → WS client) the API encodes `traceparent` (and
`tracestate`/`baggage` if present) into the JSON NOTIFY payload, and the
LISTEN side reconstitutes the span context via
`propagation.TraceContext.Extract`.

```go
// shared/tracing/go/notify.go

// EmitNotify publishes a JSON-encoded NOTIFY payload that carries the
// active span's traceparent. The structural shape is:
//   {"traceparent": "...", "tracestate": "...", "data": {…}}
func EmitNotify(ctx context.Context, db *sql.DB, channel string, data any) error {
    carrier := propagation.MapCarrier{}
    otel.GetTextMapPropagator().Inject(ctx, carrier)
    env := struct {
        Traceparent string         `json:"traceparent,omitempty"`
        Tracestate  string         `json:"tracestate,omitempty"`
        Baggage     string         `json:"baggage,omitempty"`
        Data        any            `json:"data"`
    }{
        Traceparent: carrier["traceparent"],
        Tracestate:  carrier["tracestate"],
        Baggage:     carrier["baggage"],
        Data:        data,
    }
    body, _ := json.Marshal(env)
    _, err := db.ExecContext(ctx, "SELECT pg_notify($1, $2)", channel, string(body))
    return err
}

// HandleListen extracts the trace context from a NOTIFY payload and
// returns a context whose active span continues the originating trace.
// Callers wrap their listener loop with this so emitted log lines and
// downstream HTTP/gRPC calls inherit the same trace_id.
func HandleListen(ctx context.Context, payload string) (context.Context, json.RawMessage, error) {
    var env struct {
        Traceparent string          `json:"traceparent,omitempty"`
        Tracestate  string          `json:"tracestate,omitempty"`
        Baggage     string          `json:"baggage,omitempty"`
        Data        json.RawMessage `json:"data"`
    }
    if err := json.Unmarshal([]byte(payload), &env); err != nil {
        return ctx, nil, err
    }
    carrier := propagation.MapCarrier{
        "traceparent": env.Traceparent,
        "tracestate":  env.Tracestate,
        "baggage":     env.Baggage,
    }
    return otel.GetTextMapPropagator().Extract(ctx, carrier), env.Data, nil
}
```

Smoke test (`TC4` below): a worker emits `pg_notify` while inside a
traced span; the API listener consumes the NOTIFY, fans out to a WS
client; the test asserts the WS client's recorded `trace_id` equals the
worker's originating `trace_id`.

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

`MaxQueueSize` (10240 spans × ~1 KiB each ≈ 10 MiB) bounds memory per the
story; once full, further spans dropped with metric
`otel_traces_dropped_total` increment. Never blocks the calling goroutine.

## 10. Test cases

### TC1 — End-to-end
Compose stack with OTLP collector echoing to a file. Issue web search. Inspect collector output: a single trace contains spans `web.search → POST /api/search → pipeline.Embed → postgres exec → chroma.query`.

### TC2 — Sampling
Enable tracing. Issue 1,000 fast 200-OK requests. Collector receives ~10 traces (within Poisson tolerance). Then issue one slow request (force 800 ms). Receive an additional trace tagged `slow=true`.

### TC3 — Disabled-by-default
Boot fresh; `[telemetry].otlp_endpoint` empty. `lsof -i -P` on each service shows no outbound conn to any OTLP host. `netstat -na | grep -E "443|4317|4318"` shows no ESTABLISHED beyond expected (DB, gRPC peers).

### TC4 — gRPC propagation (both legs)
Stand up an in-process server with `otelgrpc.UnaryServerInterceptor` and
a client dialed with `otelgrpc.UnaryClientInterceptor`. The client opens
a span, calls `Pipeline.HealthCheck`. Assert: (a) the server-side
recorded span's `parent_span_id` equals the client-side span's
`span_id`; (b) `trace_id` is identical on both sides; (c) the streaming
flavor (`Pipeline.Transcribe`) preserves the same trace across each
streamed message in both directions.

### TC5 — LISTEN/NOTIFY trace continuity
A pipeline worker, inside an active span, calls `EmitNotify(ctx, db,
"job_progress", …)`. The API LISTEN loop consumes the payload, calls
`HandleListen`, then forwards a JSON message over `WS /ws/jobs?job_id=…`
to a connected client. The client (also instrumented) records the
received message with the trace context decoded from the in-message
fields. Assert the worker's originating `trace_id` equals the trace_id
visible at the WS client. Cross-cutting reference:
[PLAN_REVIEW_18_24.md §5](../../PLAN_REVIEW_18_24.md).

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
