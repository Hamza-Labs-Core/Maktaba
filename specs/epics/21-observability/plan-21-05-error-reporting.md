# Implementation Plan — Story 21.5 Error Reporting & Alerting Integration

> Companion to [story-21-05-error-reporting.md](story-21-05-error-reporting.md).
> Structured error events with `error_id` UUIDv7, category, stack;
> rate-limited webhook; opt-in Sentry-compatible DSN; cross-service correlation.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| `error_id` | UUIDv7 generated at first emission; propagated across gRPC via metadata key `x-error-id`. |
| Category | Enum `auth | db | ffmpeg | network | ml | unknown`. |
| Webhook | Generic POST; Slack/Discord by URL pattern. Rate-limit 10/min with exponential suppression. |
| Sentry | Opt-in via `[telemetry].sentry_dsn`. Use Sentry-compatible client; never log DSN. |
| Drop file | `/var/log/maktaba/error_drop_log` if webhook unsendable. |

## 1. Project layout

```
shared/errrpt/
├── go/
│   ├── error.go              # Maktaba error wrapper with category + id
│   ├── webhook.go            # generic POST with Slack/Discord adapters
│   ├── circuit.go            # circuit breaker
│   ├── ratelimit.go
│   ├── sentry.go             # opt-in
│   ├── propagator.go         # gRPC metadata
│   ├── shutdown.go           # 5s drain
│   ├── redactor.go
│   └── tests/
└── py/
    ├── error.py
    ├── webhook.py
    ├── propagator.py         # client/server interceptors
    └── tests/

deploy/compose/error_webhook_test.yml    # for TC1/TC3
```

## 2. Error wrapper (Go)

```go
// shared/errrpt/go/error.go
type Category string
const (
    CatAuth    Category = "auth"
    CatDB      Category = "db"
    CatFFmpeg  Category = "ffmpeg"
    CatNetwork Category = "network"
    CatML      Category = "ml"
    CatUnknown Category = "unknown"
)

type E struct {
    ID       uuid.UUID
    Cat      Category
    Wrapped  error
    Stack    []byte
    Sensitive bool
    Fields   map[string]any
}

func New(cat Category, err error, fields map[string]any) *E {
    if err == nil { err = errors.New("nil err wrapped") }
    return &E{
        ID:      uuid.Must(uuid.NewV7()),
        Cat:     cat,
        Wrapped: err,
        Stack:   debug.Stack(),
        Fields:  fields,
    }
}

func (e *E) Error() string { return e.Wrapped.Error() }
func (e *E) Unwrap() error  { return e.Wrapped }
```

Helper:

```go
func Emit(ctx context.Context, e *E) {
    log.From(ctx).Error("error", "error_id", e.ID, "category", e.Cat,
        "fields", e.Fields, "stack", string(e.Stack))
    rpt.Submit(ctx, e)
}
```

## 3. gRPC propagation

```go
// shared/errrpt/go/propagator.go
const ErrorIDMetaKey = "x-error-id"

func ServerInterceptor() grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
        resp, err := handler(ctx, req)
        if err != nil {
            var e *E
            if !errors.As(err, &e) { e = New(CatUnknown, err, nil) }
            md := metadata.New(map[string]string{ErrorIDMetaKey: e.ID.String()})
            _ = grpc.SetTrailer(ctx, md)
            return resp, status.Error(codes.Internal, e.Error())
        }
        return resp, err
    }
}
func ClientInterceptor() grpc.UnaryClientInterceptor {
    return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
        var trailer metadata.MD
        opts = append(opts, grpc.Trailer(&trailer))
        err := invoker(ctx, method, req, reply, cc, opts...)
        if err != nil {
            id := uuid.Nil
            if v := trailer.Get(ErrorIDMetaKey); len(v) > 0 { id, _ = uuid.Parse(v[0]) }
            return &E{ID: id, Cat: classify(err), Wrapped: err}
        }
        return nil
    }
}
```

## 4. Webhook with rate limit + circuit breaker

```go
// shared/errrpt/go/webhook.go
type Webhook struct {
    url    string
    client *http.Client
    rl     *RateLimiter         // 10/min token bucket
    cb     *Circuit             // 5 fails open; 60s half-open
    sup    Suppressor           // counts suppressed
}

func (w *Webhook) Submit(ctx context.Context, e *E) {
    if !w.cb.AllowSend() { w.sup.Inc(); return }
    if !w.rl.Allow() { w.sup.Inc(); return }
    body := buildBody(e, w.sup.DrainCount())
    if err := w.post(ctx, body); err != nil {
        w.cb.RecordFailure()
    } else {
        w.cb.RecordSuccess()
    }
}

func buildBody(e *E, suppressed int) []byte {
    payload := map[string]any{
        "error_id":  e.ID.String(),
        "category":  e.Cat,
        "summary":   redact(e.Error()),
        "service":   svc,
        "ts":        time.Now().UTC().Format(time.RFC3339),
        "fields":    redactedFields(e.Fields),
    }
    if suppressed > 0 { payload["suppressed_since_last"] = suppressed }
    b, _ := json.Marshal(payload)
    return b
}
```

`redact` strips known-sensitive substrings (passwords, paths, tokens). Field-level redaction by `sensitive=true` tag in the `Fields` map.

## 5. Sentry

```go
// shared/errrpt/go/sentry.go
func InitSentry(dsn string) error {
    if dsn == "" { return nil }
    err := sentry.Init(sentry.ClientOptions{
        Dsn:              dsn,
        TracesSampleRate: 0.0,                        // we use OTel for tracing
        AttachStacktrace: true,
        BeforeSend: func(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
            ev.Request = nil                          // strip URLs/headers
            ev.User = sentry.User{}
            return ev
        },
    })
    if err != nil {
        // EC2: log warning, don't crash
        slog.Warn("sentry init failed; continuing without remote error reporting", "err", err)
        return nil
    }
    return nil
}
```

`dsn` read only from env var `MAKTABA_SENTRY_DSN`; the value is never logged.

## 6. Drain on shutdown (EC3)

```go
// shared/errrpt/go/shutdown.go
func Shutdown(ctx context.Context) {
    deadline, cancel := context.WithTimeout(ctx, 5*time.Second)
    defer cancel()
    flushSentry := make(chan struct{})
    go func() { sentry.Flush(5 * time.Second); close(flushSentry) }()
    select { case <-deadline.Done(): case <-flushSentry: }
    if remaining := webhook.PendingCount(); remaining > 0 {
        f, _ := os.OpenFile("/var/log/maktaba/error_drop_log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
        defer f.Close()
        webhook.DrainTo(f)
    }
}
```

## 7. Test cases

### TC1 — Burst suppression
Spawn 1,000 errors in a 5-second loop. The webhook test server records ≤ 10 POSTs. The 10th payload's `suppressed_since_last` ≥ 990. After 60 s without further errors, suppression is reset.

### TC2 — Cross-service correlation
Drive a transcribe job that fails inside Pipeline. Capture:
- Pipeline log: `error_id=abc...`, `category=ml`.
- API job-status row: `last_error_id=abc...`.
- Client-visible error response: `{"error_id":"abc..."}`.
All three share the same UUID.

### TC3 — Redaction
Configure a webhook test server. Trigger an error with field `password=hunter2` (using `Sensitive=true` flag and field name). Assert payload omits the value (`fields.password=***`); paths under media root replaced by `<media>/<library>/<rel>`.

### EC1 — Circuit breaker
Webhook server returns 502 on every other call. Drive 10 errors. Observe: after 5 consecutive failures the circuit opens for 60 s. While open, errors are suppressed (counted). After 60 s, half-open: one probe sent; if success, fully closed.

### EC2 — Sentry typo
Set `MAKTABA_SENTRY_DSN=not-a-dsn`. Boot service. Log captured: one `WARN sentry init failed`. No crash. Subsequent errors are still emitted to log + webhook.

### EC3 — Shutdown drain
While webhook is unreachable, drive 100 errors. Send SIGTERM. After 5 s, the service exits; `/var/log/maktaba/error_drop_log` contains 100 lines.

## 8. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 webhook flap | story | Circuit breaker. |
| EC2 Sentry DSN typo | story | Log once, continue. |
| EC3 shutdown | story | 5 s drain → drop file. |
| Webhook secret in URL | impl | URL is treated as `sensitive=true`; only host logged. |
| UUIDv7 unsupported on stdlib version | impl | Use `github.com/gofrs/uuid/v5`; pinned. |

## 9. Configuration

```yaml
errors:
  webhook:
    url: ""                     # empty disables
    rate_per_minute: 10
    circuit_threshold: 5
    circuit_open_seconds: 60
  sentry:
    dsn_env: MAKTABA_SENTRY_DSN
  drop_file: /var/log/maktaba/error_drop_log
```

## 10. Dependencies

- Story 21.1 (logger emits the error line; propagates `error_id`).
- Story 21.6 (audit log for security errors).
- Story 21.8 (privacy redaction list).
- Epic 6 (job rows include `last_error_id` column).
