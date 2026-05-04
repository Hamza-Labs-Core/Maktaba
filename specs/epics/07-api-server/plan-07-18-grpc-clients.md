# Implementation Plan — Story 7.18 gRPC Clients to Pipeline and Streaming

> Companion to [story-07-18-grpc-clients.md](story-07-18-grpc-clients.md).
> The API's outbound gRPC plumbing: timeouts, retries, circuit breaker,
> request-ID propagation.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Generation | `protoc-gen-go` + `protoc-gen-go-grpc` from `shared/proto/`. |
| Wrappers | One package per service: `api/internal/grpc/pipeline` and `api/internal/grpc/streaming`. |
| Resilience | `github.com/sony/gobreaker` + a hand-rolled retry decorator (no external retry interceptor — the policy is small). |
| Tracing | OTel gRPC interceptors, plus a metadata propagator for `maktaba-request-id`. |
| Out of scope | The proto schema itself (lives in `shared/proto`, owned by Pipeline + Streaming epics); the gRPC servers (Pipeline, Streaming). |

## 1. Architecture diagram

```
   handler  →  pipeline.Embed(ctx, text)
                 │
                 ▼
        ┌────────────────────────────────────────────┐
        │ wrapper                                    │
        │  ctx = withDeadline(ctx, cfg.Embed.Timeout)│
        │  metadata = base + request-id              │
        │                                            │
        │  retryDecorator(maxAttempts, backoff):     │
        │    breaker.Execute:                        │
        │      grpcClient.Embed(ctx, request)        │
        │      ↑ classify err: retry or terminal     │
        └────────────────────────────────────────────┘
                 │
                 ▼  unary RPC over a single conn pool
        ┌────────────────────────────────────────────┐
        │ pipeline.PipelineClient (generated)        │
        └────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/grpc/dial.go` | Common dial options: TLS, OTel, metadata propagator. |
| `api/internal/grpc/breaker.go` | `gobreaker` wrapper + classify. |
| `api/internal/grpc/retry.go` | Retry decorator. |
| `api/internal/grpc/pipeline/client.go` | Wrapper struct + each method. |
| `api/internal/grpc/streaming/client.go` | Same for Streaming. |
| `api/internal/grpc/dial_test.go` | Unit. |
| `api/internal/grpc/integration_test.go` | Integration with a fake gRPC server. |

## 3. Type definitions

```go
// api/internal/grpc/pipeline/client.go
package pipeline

import (
    "context"
    "time"

    pb "maktaba/shared/proto/pipeline"
)

type Backend struct {
    Name             string
    Available        bool
    Version          string
    Models           []string
    HWAccel          string
    CostPerMinuteUSD *float64
}

type Vector []float32

type TranscribeEvent struct {
    Type       string  // "segment" | "progress" | "done" | "error"
    SegmentID  string
    StartSec   float64
    EndSec     float64
    Text       string
    Confidence float64
    Progress   float64
    Err        error
}

type Client struct {
    raw     pb.PipelineClient
    cfg     Config
    breaker *grpcx.Breaker
}

// The Pipeline gRPC surface is canonically Embed, Transcribe,
// ListBackends, HealthCheck (architecture §9.9). There is no
// Pipeline.Enqueue, Pipeline.EnqueueChain, Pipeline.ExtractEmbeddedSubtitle,
// or Pipeline.RunSyntheticTranscribe — bulk job control flows through
// Postgres (`INSERT INTO processing_jobs ...`) and dry-run STT uses
// Transcribe with a fixture audio source.
type Config struct {
    EmbedTimeout            time.Duration  // default 2s
    TranscribeTimeout       time.Duration  // default 0 (streaming, parent ctx governs)
    ListBackendsTimeout     time.Duration  // default 5s
    HealthCheckTimeout      time.Duration  // default 1s
    RetryMaxAttempts        int            // default 3
    RetryBaseBackoff        time.Duration  // default 50ms (jittered)
    BreakerFailureRate      float64        // default 0.5
    BreakerWindow           time.Duration  // default 30s
    BreakerOpenDuration     time.Duration  // default 10s
}

func (c *Client) Embed(ctx context.Context, text string) (Vector, error) {
    ctx, cancel := context.WithTimeout(ctx, c.cfg.EmbedTimeout)
    defer cancel()
    var out Vector
    err := c.breaker.Execute(func() error {
        return retry(ctx, c.cfg.RetryMaxAttempts, c.cfg.RetryBaseBackoff, func(ctx context.Context) error {
            r, err := c.raw.Embed(ctx, &pb.EmbedRequest{Text: text})
            if err != nil { return err }
            out = make(Vector, len(r.Vector))
            for i, v := range r.Vector { out[i] = v }
            return nil
        })
    })
    return out, err
}

// Transcribe is a server-streaming RPC; it does not pass through the
// retry decorator (re-running a streaming transcription mid-stream
// would be unsafe). The breaker still observes the call's outcome.
// The settings dry-run path (plan-07-15) uses this same RPC with
// `dry_run: true` and an embedded fixture WAV — there is no separate
// RunSyntheticTranscribe RPC.
func (c *Client) Transcribe(ctx context.Context, req TranscribeRequest) (<-chan TranscribeEvent, error) { /* ... */ }

func (c *Client) ListBackends(ctx context.Context) ([]Backend, error) { /* ... */ }
func (c *Client) HealthCheck(ctx context.Context) (Status, error) { /* ... */ }
```

```go
// api/internal/grpc/streaming/client.go
package streaming

// The Streaming gRPC surface is canonically OpenSession, CloseSession,
// EvictHashCache, GetCapabilities, WatchQueue, HealthCheck (architecture
// §9.9). OpenSession returns *pb.OpenSessionResponse {Session,
// Capabilities}. EvictHashCache returns *pb.EvictHashCacheResponse
// {entries_removed, artifacts}.
type Client struct {
    raw     pb.StreamingClient
    cfg     Config
    breaker *grpcx.Breaker
}

func (c *Client) OpenSession(ctx context.Context, req *pb.OpenSessionRequest) (*pb.OpenSessionResponse, error) { /* timeouts + breaker; no retry (open is not idempotent) */ }
func (c *Client) CloseSession(ctx context.Context, sessionID string) error { /* idempotent → retried */ }
// EvictHashCache returns the canonical typed response with a count of
// removed cache entries plus the list of artifacts that were dropped.
func (c *Client) EvictHashCache(ctx context.Context, hash string) (*pb.EvictHashCacheResponse, error) { /* idempotent → retried */ }
func (c *Client) GetCapabilities(ctx context.Context) (*pb.GetCapabilitiesResponse, error) { /* retried */ }
// WatchQueue is a server-streaming RPC: each event emits a queue snapshot
// (depth, in-flight, slot utilisation). The wrapper does not retry but
// the breaker observes outcomes; the caller iterates until the stream
// ends or ctx is cancelled.
func (c *Client) WatchQueue(ctx context.Context, req *pb.WatchQueueRequest) (<-chan *pb.QueueSnapshot, error) { /* ... */ }
func (c *Client) HealthCheck(ctx context.Context) (Status, error)          { /* retried */ }
```

## 4. Common dial + interceptors

```go
// api/internal/grpc/dial.go
package grpcx

import (
    "context"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials"
    "google.golang.org/grpc/metadata"

    "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

    apirequestid "maktaba/api/internal/middleware"
)

// Dial returns a *grpc.ClientConn with our standard interceptors.
func Dial(addr string, tls *tls.Config) (*grpc.ClientConn, error) {
    creds := insecure.NewCredentials()
    if tls != nil { creds = credentials.NewTLS(tls) }
    return grpc.NewClient(addr,
        grpc.WithTransportCredentials(creds),
        grpc.WithChainUnaryInterceptor(
            otelgrpc.UnaryClientInterceptor(),
            requestIDInterceptor(),
        ),
        grpc.WithChainStreamInterceptor(
            otelgrpc.StreamClientInterceptor(),
            requestIDStreamInterceptor(),
        ),
    )
}

// requestIDInterceptor adds the inbound HTTP request-ID to outgoing
// gRPC metadata under `maktaba-request-id` so the receiving service
// can log it alongside its own work.
func requestIDInterceptor() grpc.UnaryClientInterceptor {
    return func(ctx context.Context, method string, req, reply any,
        cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
        if id := apirequestid.RequestIDFromContext(ctx); id != uuid.Nil {
            ctx = metadata.AppendToOutgoingContext(ctx, "maktaba-request-id", id.String())
        }
        return invoker(ctx, method, req, reply, cc, opts...)
    }
}
```

## 5. Breaker + retry

```go
// api/internal/grpc/breaker.go
package grpcx

import (
    "github.com/sony/gobreaker"
    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

type Breaker struct {
    cb *gobreaker.CircuitBreaker
}

func NewBreaker(name string, cfg BreakerConfig) *Breaker {
    settings := gobreaker.Settings{
        Name: name,
        Timeout: cfg.OpenDuration, // how long to stay open
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            if counts.Requests < 10 { return false }
            return float64(counts.TotalFailures)/float64(counts.Requests) > cfg.FailureRate
        },
    }
    return &Breaker{cb: gobreaker.NewCircuitBreaker(settings)}
}

func (b *Breaker) Execute(fn func() error) error {
    _, err := b.cb.Execute(func() (any, error) {
        if err := fn(); !shouldCount(err) { return nil, nil } else { return nil, err }
    })
    if errors.Is(err, gobreaker.ErrOpenState) {
        return &Error{Code: ErrCircuitOpen, Wrapped: err}
    }
    return err
}

// shouldCount classifies retry vs. terminal vs. ignore.
func shouldCount(err error) bool {
    if err == nil { return false }
    st, ok := status.FromError(err)
    if !ok { return true }
    switch st.Code() {
    case codes.OK, codes.NotFound, codes.AlreadyExists, codes.InvalidArgument,
         codes.FailedPrecondition, codes.PermissionDenied, codes.Unauthenticated:
        return false  // not transport-level failures
    default:
        return true
    }
}
```

```go
// api/internal/grpc/retry.go
package grpcx

import (
    "context"
    "math/rand"
    "time"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"
)

func retry(ctx context.Context, max int, base time.Duration, fn func(context.Context) error) error {
    var lastErr error
    for attempt := 1; attempt <= max; attempt++ {
        err := fn(ctx)
        if err == nil { return nil }
        lastErr = err
        if !retryable(err) || ctx.Err() != nil { return err }
        sleep := time.Duration(int64(base) * 1<<uint(attempt-1))
        sleep += time.Duration(rand.Int63n(int64(sleep / 2)))
        select {
        case <-ctx.Done(): return lastErr
        case <-time.After(sleep):
        }
    }
    return lastErr
}

func retryable(err error) bool {
    st, ok := status.FromError(err)
    if !ok { return false }
    switch st.Code() {
    case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted:
        return true
    default:
        return false
    }
}
```

## 6. Error mapping

```go
// api/internal/grpc/errors.go
package grpcx

import (
    "errors"

    "google.golang.org/grpc/codes"
    "google.golang.org/grpc/status"

    "maktaba/api/internal/httperror"
)

// Map translates a gRPC error into the closest problem+json shape.
func Map(err error, kind string) *httperror.Error {
    if errors.Is(err, ErrOpenState) {
        return &httperror.Error{Type: TypeCircuitOpen, Title: "circuit open",
            Status: 503, Detail: kind+" circuit is open"}
    }
    st, ok := status.FromError(err)
    if !ok { return httperror.Internal(kind+" call failed") }
    switch st.Code() {
    case codes.Unavailable:        return httperror.Unavailable(5).WithType(TypeUnavailable)
    case codes.DeadlineExceeded:   return httperror.Unavailable(2).WithType(TypeTimeout)
    case codes.ResourceExhausted:  return httperror.Unavailable(5).WithType(TypeResourceExhausted)
    case codes.NotFound:           return httperror.NotFound(st.Message())
    case codes.PermissionDenied:   return httperror.Forbidden(TypeAccessDenied, st.Message())
    case codes.InvalidArgument:    return httperror.BadRequest(st.Message())
    case codes.Internal:           return &httperror.Error{Type: kind+"-internal", Title: "internal", Status: 500}
    default:                       return httperror.Internal(kind+": "+st.Message())
    }
}
```

## 7. Test plan

### 7.1 Integration (`integration_test.go`)

| Test | What it pins |
|---|---|
| `TestRetryThreeAttempts` | Fake server fails twice with `UNAVAILABLE` then succeeds → client returns success after 3 attempts; observed via wire counter. |
| `TestRetryGivesUpAfterMax` | Fake fails 5 times → after `RetryMaxAttempts` returns 503-mapped problem+json. |
| `TestRetryNonRetryableFastFail` | Fake returns `INVALID_ARGUMENT` → no retry; one wire call. |
| `TestBreakerOpens` | Fake fails 50% over 30 s window → breaker opens; subsequent calls fail-fast with `circuit-open`. |
| `TestBreakerCloses` | After `OpenDuration` and a successful probe call → breaker closes. |
| `TestDeadlinePropagation` | Inbound HTTP timeout 100 ms → outbound gRPC deadline ≤ 100 ms (verified by fake server reading the deadline). |
| `TestRequestIDPropagated` | Inbound `X-Request-Id: <uuid>` → fake server sees `maktaba-request-id` metadata = same uuid. |
| `TestOTelTraceParent` | OTel enabled → fake sees `traceparent` metadata; trace IDs match. |
| `TestStreamingCapsP95` | `streaming.GetCapabilities` (in-Streaming cache) → p95 < 50 ms. |
| `TestPipelineInternalMaps` | Fake returns `INTERNAL` → wrapped as 500 problem+json `pipeline-internal`. |
| `TestStreamingResourceExhaustedMaps` | Fake returns `RESOURCE_EXHAUSTED` → 503 with `Retry-After: 5`. |
| `TestProtoForwardCompat` | Server returns a response with an unknown extra field → client decodes the rest cleanly; no panic. |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Server returns `INTERNAL` | Wrapped as 500 problem+json `pipeline-internal`. | `TestPipelineInternalMaps` |
| Server returns `RESOURCE_EXHAUSTED` | 503 + `Retry-After: 5`. Not retried by the client (the user must back off). | `TestStreamingResourceExhaustedMaps` |
| Schema adds new optional field | Decoded silently; old clients keep working. | `TestProtoForwardCompat` |
| Streaming RPC mid-stream failure | Not retried by the decorator; breaker still observes; caller returns the partial events plus the error. | Documented |
| Deadline fires before retry window | `ctx.Err() != nil` exits early; no further retries. | `TestRetryGivesUpAfterMax` (variant) |
| Breaker open during a "probe" call | A single half-open call is allowed; failure → re-open; success → close. | `TestBreakerCloses` |
| OTel disabled | Interceptor still attached but no-op; no trace metadata sent. | Manual |
| Server forces TLS but client config is plaintext | Dial fails fast at startup, not on first RPC. | Startup test |

## 9. Acceptance checklist

- [ ] One client wrapper per service (`pipeline`, `streaming`).
- [ ] Per-call deadlines from config.
- [ ] Retry decorator with jittered exponential backoff for retryable codes only.
- [ ] Circuit breaker opens at 50% failure rate over 30 s; closes after 10 s.
- [ ] `maktaba-request-id` metadata propagated.
- [ ] OTel trace propagation works.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.18.
