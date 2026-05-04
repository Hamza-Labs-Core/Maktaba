# Implementation Plan — Story 7.1 HTTP Server Skeleton

> Companion to [story-07-01-http-server-skeleton.md](story-07-01-http-server-skeleton.md).
> The story states *what* and *why*; this plan states *how*.
> Lays the chassis every later story in Epic 7 plugs into.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Server framework | `github.com/go-chi/chi/v5` for routing; `net/http` for the listener. No `gin`/`echo` (unjustified weight). |
| Logging | `log/slog` stdlib, JSON handler, request-ID baked into every line via a context-aware handler wrapper. |
| Error envelope | RFC 9457 problem+json. One package `api/internal/httperror` owns `Error` and `Write`. |
| Request IDs | UUID v7 (`github.com/google/uuid` v1.6+). Reused if client supplies a syntactically valid one in `X-Request-Id`. |
| Idempotency store | Postgres `idempotency_keys` table; SQLite mirror. TTL-cleaned by a sweep goroutine. |
| Out of scope | Auth (Epic 10), rate-limiting (Story 7.19), business handlers (Stories 7.3+), metrics endpoint (Story 7.20). |

## 1. Architecture diagram

```
       Client
         │  HTTP/1.1 or H/2
         ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ net/http.Server (api/cmd/api/main.go)                       │
   │  - ReadHeaderTimeout 10s                                    │
   │  - shutdown via context.WithTimeout(grace) on SIGTERM/SIGINT│
   └────────────┬────────────────────────────────────────────────┘
                ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ chi.Mux (api/internal/router/router.go)                     │
   │  Middleware chain (executes top-down):                      │
   │   1. middleware.RealIP                                      │
   │   2. middleware.RequestID  →  pulls/mints UUID v7,          │
   │                                attaches to ctx, sets header │
   │   3. middleware.Recoverer  →  rendered as 500 problem+json  │
   │   4. middleware.SLogLogger →  one line per request          │
   │   5. middleware.Idempotency (POST/PUT/PATCH/DELETE w/ key)  │
   │   6. middleware.SingleWriteGuard (catches double-Write bug) │
   │   7. (later: auth, rate-limit, validation)                  │
   └────────────┬────────────────────────────────────────────────┘
                ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ Route table (registered by sub-routers)                     │
   │   /api/healthz   (Story 7.20)                               │
   │   /api/...       (Stories 7.3+)                             │
   │   /metrics       (Story 7.20)                               │
   └─────────────────────────────────────────────────────────────┘
```

The middleware order is canonical and load-bearing — request-ID **must**
precede the logger so every log line carries it; recoverer **must**
precede the logger so the panic gets rendered as a normal response and
logged once, not twice.

## 2. New files

| Path | Purpose |
|---|---|
| `api/cmd/api/main.go` | Process entrypoint: config load, signal handling, graceful shutdown loop. |
| `api/internal/router/router.go` | Constructs the `chi.Mux`, mounts the canonical middleware stack, exposes a `New(deps Deps) http.Handler`. |
| `api/internal/httperror/httperror.go` | `Error` type, `Write(w, err)`, well-known `Type` constants. |
| `api/internal/httperror/types.go` | All problem-type URIs as `const` (`https://maktaba.dev/problems/...`). One source of truth. |
| `api/internal/middleware/requestid.go` | RequestID middleware (UUID v7 mint/parse). |
| `api/internal/middleware/recoverer.go` | Panic-to-500-problem+json. |
| `api/internal/middleware/slog_logger.go` | `slog.Handler` that pulls request-id from context, writes one structured line per request. |
| `api/internal/middleware/idempotency.go` | Idempotency-Key middleware + DB-backed store. |
| `api/internal/middleware/single_write_guard.go` | Wraps `http.ResponseWriter` to drop second `WriteHeader`/`Write` and log a warning. |
| `api/internal/idempotency/store.go` | Postgres + SQLite-backed `Store` interface, sweeps expired keys. |
| `shared/db/migrations/0011_idempotency_keys.sql` | Postgres schema for the key store. |
| `shared/db/migrations/0011_idempotency_keys.sqlite.sql` | SQLite mirror. |
| `tools/cmd/forbidhttperror/main.go` | `go vet`-style analysis pass that fails CI when `http.Error` is called outside `httperror`. |
| `api/internal/.../*_test.go` | Unit + integration tests per §6 below. |

## 3. Go code scaffolding

### 3.1 `httperror` package

```go
// api/internal/httperror/httperror.go
package httperror

import (
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"

    "github.com/google/uuid"
)

// Error is the canonical API error type. All handlers MUST return errors of
// this kind (or wrap into it). Bare strings or stdlib errors are flagged by
// the forbidhttperror analyzer.
type Error struct {
    Type     string         `json:"type"`              // "https://maktaba.dev/problems/<slug>"
    Title    string         `json:"title"`             // human-readable, stable
    Status   int            `json:"status"`            // HTTP status
    Detail   string         `json:"detail,omitempty"`  // human-readable, may carry params
    Instance string         `json:"instance,omitempty"`// request path; filled by Write
    Errors   []FieldError   `json:"errors,omitempty"`  // for 422
    Extras   map[string]any `json:"-"`                 // marshalled flat
}

type FieldError struct {
    Field   string `json:"field"`
    Message string `json:"message"`
}

func (e *Error) Error() string { return e.Title + ": " + e.Detail }

// Constructors used by handlers.
func NotFound(detail string) *Error    { return &Error{Type: TypeNotFound, Title: "not found", Status: 404, Detail: detail} }
func BadRequest(detail string) *Error  { return &Error{Type: TypeBadRequest, Title: "bad request", Status: 400, Detail: detail} }
func InvalidQuery(detail string) *Error{ return &Error{Type: TypeInvalidQueryParam, Title: "invalid query parameter", Status: 400, Detail: detail} }
func Unprocessable(errs []FieldError) *Error {
    return &Error{Type: TypeValidation, Title: "validation failed", Status: 422, Errors: errs}
}
func Conflict(typ, detail string) *Error{ return &Error{Type: typ, Title: "conflict", Status: 409, Detail: detail} }
func Forbidden(typ, detail string) *Error{ return &Error{Type: typ, Title: "forbidden", Status: 403, Detail: detail} }
func Internal(detail string) *Error    { return &Error{Type: TypeInternal, Title: "internal error", Status: 500, Detail: detail} }
func Unavailable(retryAfter int) *Error {
    return &Error{Type: TypeUnavailable, Title: "service unavailable", Status: 503,
        Extras: map[string]any{"retry_after_sec": retryAfter}}
}

// Write renders any error to an RFC 9457 problem+json response.
// If err is not an *Error, it's wrapped as a generic 500 with no details
// (the underlying message is logged but never surfaced).
func Write(w http.ResponseWriter, r *http.Request, err error) {
    var e *Error
    if !errors.As(err, &e) {
        slog.ErrorContext(r.Context(), "unhandled_error", "err", err.Error())
        e = Internal("")
    }
    e.Instance = r.URL.Path

    body := map[string]any{
        "type":      e.Type,
        "title":     e.Title,
        "status":    e.Status,
        "detail":    e.Detail,
        "instance":  e.Instance,
        "requestId": uuid.UUID(RequestIDFromContext(r.Context())).String(),
    }
    if len(e.Errors) > 0 {
        body["errors"] = e.Errors
    }
    for k, v := range e.Extras {
        body[k] = v
    }

    w.Header().Set("Content-Type", "application/problem+json")
    w.WriteHeader(e.Status)
    _ = json.NewEncoder(w).Encode(body)
}
```

```go
// api/internal/httperror/types.go
package httperror

const (
    TypeBadRequest         = "https://maktaba.dev/problems/bad-request"
    TypeInvalidJSON        = "https://maktaba.dev/problems/invalid-json"
    TypeInvalidQueryParam  = "https://maktaba.dev/problems/invalid-query-parameter"
    TypeInvalidCursor      = "https://maktaba.dev/problems/invalid-cursor"
    TypeCursorUnsupported  = "https://maktaba.dev/problems/cursor-unsupported-version"
    TypeNotFound           = "https://maktaba.dev/problems/not-found"
    TypeValidation         = "https://maktaba.dev/problems/validation"
    TypeIdempotencyConflict= "https://maktaba.dev/problems/idempotency-key-conflict"
    TypeConfirmationReq    = "https://maktaba.dev/problems/confirmation-required"
    TypeInternal           = "https://maktaba.dev/problems/internal"
    TypeUnavailable        = "https://maktaba.dev/problems/unavailable"
    // (each later story adds its own constants in this same file.)
)
```

### 3.2 Request-ID middleware

```go
// api/internal/middleware/requestid.go
package middleware

import (
    "context"
    "net/http"

    "github.com/google/uuid"
)

type requestIDKey struct{}

const Header = "X-Request-Id"

func RequestID(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        var id uuid.UUID
        if got := r.Header.Get(Header); got != "" {
            if parsed, err := uuid.Parse(got); err == nil && parsed.Version() == 7 {
                id = parsed
            }
        }
        if id == uuid.Nil {
            id = uuid.Must(uuid.NewV7())
        }
        ctx := context.WithValue(r.Context(), requestIDKey{}, id)
        w.Header().Set(Header, id.String())
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}

func RequestIDFromContext(ctx context.Context) uuid.UUID {
    if v, ok := ctx.Value(requestIDKey{}).(uuid.UUID); ok {
        return v
    }
    return uuid.Nil
}
```

### 3.3 Recoverer

```go
// api/internal/middleware/recoverer.go
package middleware

import (
    "log/slog"
    "net/http"
    "runtime/debug"

    "maktaba/api/internal/httperror"
)

func Recoverer(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        defer func() {
            if v := recover(); v != nil {
                slog.ErrorContext(r.Context(), "panic",
                    "value", v, "stack", string(debug.Stack()))
                httperror.Write(w, r, httperror.Internal("internal error"))
            }
        }()
        next.ServeHTTP(w, r)
    })
}
```

### 3.4 SingleWriteGuard

```go
// api/internal/middleware/single_write_guard.go
package middleware

import (
    "log/slog"
    "net/http"
)

type guardedWriter struct {
    http.ResponseWriter
    written bool
    ctxLog  *slog.Logger
}

func (g *guardedWriter) WriteHeader(code int) {
    if g.written {
        g.ctxLog.Warn("double_write_header", "second_status", code)
        return
    }
    g.written = true
    g.ResponseWriter.WriteHeader(code)
}

func (g *guardedWriter) Write(b []byte) (int, error) {
    if !g.written {
        g.WriteHeader(http.StatusOK)
    }
    return g.ResponseWriter.Write(b)
}

func SingleWriteGuard(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        next.ServeHTTP(&guardedWriter{ResponseWriter: w, ctxLog: slog.Default()}, r)
    })
}
```

### 3.5 Idempotency middleware + store

```go
// api/internal/middleware/idempotency.go
package middleware

import (
    "bytes"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "net/http"

    "maktaba/api/internal/httperror"
    "maktaba/api/internal/idempotency"
)

func Idempotency(store idempotency.Store) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            key := r.Header.Get("Idempotency-Key")
            if key == "" || !mutating(r.Method) {
                next.ServeHTTP(w, r)
                return
            }
            // Hash the body once; we'll need it to compare on replay.
            body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
            r.Body = io.NopCloser(bytes.NewReader(body))
            sum := sha256.Sum256(body)
            hash := hex.EncodeToString(sum[:])

            user := userFromCtx(r.Context()) // wired by Epic 10 auth; for now, an opaque tag
            if cached, ok := store.Lookup(r.Context(), key, user); ok {
                if cached.RequestHash != hash {
                    httperror.Write(w, r, httperror.Conflict(httperror.TypeIdempotencyConflict,
                        "Idempotency-Key reused with different body"))
                    return
                }
                replay(w, cached)
                return
            }
            cap := newCapturingWriter(w)
            next.ServeHTTP(cap, r)
            store.Save(r.Context(), idempotency.Record{
                Key: key, UserID: user, RequestHash: hash,
                Status: cap.status, Body: cap.body.Bytes(),
            })
        })
    }
}

func mutating(m string) bool {
    return m == http.MethodPost || m == http.MethodPut ||
           m == http.MethodPatch || m == http.MethodDelete
}
```

```go
// api/internal/idempotency/store.go
package idempotency

import (
    "context"
    "time"
)

type Record struct {
    Key         string
    UserID      string
    RequestHash string
    Status      int
    Body        []byte
    CreatedAt   time.Time
}

type Store interface {
    Lookup(ctx context.Context, key, userID string) (Record, bool)
    Save(ctx context.Context, r Record) error
    SweepExpired(ctx context.Context, ttl time.Duration) (int, error)
}
```

### 3.6 Router + main

```go
// api/internal/router/router.go
package router

import (
    "net/http"

    "github.com/go-chi/chi/v5"
    chimw "github.com/go-chi/chi/v5/middleware"

    mw "maktaba/api/internal/middleware"
)

type Deps struct {
    IdempotencyStore idempotency.Store
    // (later stories extend this)
}

func New(d Deps) http.Handler {
    r := chi.NewRouter()
    r.Use(chimw.RealIP)
    r.Use(mw.RequestID)
    r.Use(mw.Recoverer)
    r.Use(mw.SLogLogger)
    r.Use(mw.SingleWriteGuard)
    r.Use(mw.Idempotency(d.IdempotencyStore))
    return r
}
```

```go
// api/cmd/api/main.go
package main

import (
    "context"
    "errors"
    "log/slog"
    "net/http"
    "os/signal"
    "syscall"
    "time"

    "maktaba/api/internal/config"
    "maktaba/api/internal/router"
)

func main() {
    cfg := config.MustLoad()
    deps := buildDeps(cfg) // wires DB, idempotency store

    srv := &http.Server{
        Addr:              cfg.Listen,
        Handler:           router.New(deps),
        ReadHeaderTimeout: 10 * time.Second,
    }

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
    defer stop()

    errCh := make(chan error, 1)
    go func() {
        errCh <- srv.ListenAndServe()
    }()

    select {
    case <-ctx.Done():
        slog.Info("shutdown_initiated", "grace_sec", cfg.ShutdownGraceSec)
        shutdownCtx, cancel := context.WithTimeout(context.Background(),
            time.Duration(cfg.ShutdownGraceSec)*time.Second)
        defer cancel()
        if err := srv.Shutdown(shutdownCtx); err != nil {
            slog.Warn("graceful_shutdown_failed", "err", err)
            _ = srv.Close() // forcibly close any stragglers
        }
    case err := <-errCh:
        if !errors.Is(err, http.ErrServerClosed) {
            slog.Error("listen_failed", "err", err)
        }
    }
}
```

## 4. SQL — idempotency key store

`shared/db/migrations/0011_idempotency_keys.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE idempotency_keys (
    key            TEXT NOT NULL,
    user_id        TEXT NOT NULL,            -- empty string for unauthenticated routes
    request_hash   TEXT NOT NULL,            -- sha256 hex of the request body
    response_status INT  NOT NULL,
    response_body  BYTEA NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (key, user_id)
);

CREATE INDEX idempotency_keys_sweep_idx
    ON idempotency_keys (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS idempotency_keys;
-- +goose StatementEnd
```

The SQLite mirror swaps `BYTEA → BLOB` and `TIMESTAMPTZ → TEXT` per the
project convention. Sweep is one statement: `DELETE FROM idempotency_keys
WHERE created_at < now() - INTERVAL '24 hours'`.

## 5. Static analysis — `forbidhttperror`

A custom `analysis.Analyzer` walks the AST and flags any
`http.Error(...)`, `w.WriteHeader(...)` followed by `w.Write(...)` of a
plain string, or any `fmt.Fprintf(w, ...)` outside the `httperror`
package itself. CI runs `go vet -vettool=tools/bin/forbidhttperror ./...`.
The analyzer ships with a `testdata/` golden test that pins the message.

## 6. Test plan

### 6.1 Unit (`api/internal/httperror/httperror_test.go`)

| Test | What it pins |
|---|---|
| `TestWriteNotFound` | `Write(w, r, NotFound("video"))` → status 404, content-type `application/problem+json`, body has `type: ".../not-found"`, `instance` equals `r.URL.Path`. |
| `TestWriteValidation` | `Write` of `Unprocessable(...)` carries the `errors[]` array verbatim. |
| `TestWriteWrapsUnknown` | Passing a stdlib `errors.New("oops")` → 500 problem+json with empty `detail`; the underlying message is logged at error level. |
| `TestRequestIdRoundTrip` | When the request had a v7 `X-Request-Id`, `Write` echoes it in the body's `requestId`. |

### 6.2 Unit (`api/internal/middleware/requestid_test.go`)

| Test | What it pins |
|---|---|
| `TestMintsWhenAbsent` | No header → middleware sets a v7 UUID in ctx and the response header. |
| `TestReusesValidV7` | Header carries a valid v7 → echoed verbatim. |
| `TestRejectsMalformedID` | Header `"abc"` → ignored; a fresh v7 is minted (no error). |
| `TestRejectsV4` | Non-v7 UUID → ignored; a fresh v7 is minted. |

### 6.3 Integration (`api/internal/router/router_test.go`)

| Test | What it pins |
|---|---|
| `TestPanickingHandlerReturns500` | Mount `/boom` that panics → 500 problem+json, body has no panic stack, log line carries `event=panic` and the request id. |
| `TestSlogLoggerCarriesRequestID` | One request → exactly one log line with `request_id=<uuid>`. |
| `TestDoubleWriteGuard` | Handler that calls `httperror.Write` twice → response body matches the first call; a `WARN double_write_header` log is emitted. |
| `TestIdempotencyReplay` | POST `/x` with `Idempotency-Key: K`, body `B` → 201; replay → identical body and status; only one underlying side-effect (handler is called once). |
| `TestIdempotencyConflict` | Same key with a different body → 409 `idempotency-key-conflict`. |

### 6.4 Integration — graceful shutdown (`api/cmd/api/shutdown_test.go`)

| Test | What it pins |
|---|---|
| `TestShutdownDrainsInflight` | Spawn the server, fire a 2 s sleep handler, send SIGTERM with `grace=5 s` → the slow request completes, the process exits within 6 s. |
| `TestShutdownForcesAfterGrace` | Slow handler sleeps 10 s, `grace=1 s` → the client sees EOF inside 2 s; the server exits 0 (the post-grace forcible Close drops the connection). |
| `TestNoNewConnectionsAfterSignal` | After SIGTERM, opening a new TCP connection to the listener fails with `connection refused` (or the listener closes). |

### 6.5 Lint test (`tools/cmd/forbidhttperror/forbidhttperror_test.go`)

`analysistest.Run` against a `testdata/` snippet that calls
`http.Error(w, "x", 500)` outside the allowlisted package — fails with the
canonical message `use httperror.Write instead of http.Error`.

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Handler writes body before status | `SingleWriteGuard` infers `WriteHeader(200)` on first `Write`; subsequent `WriteHeader` is dropped. | `TestDoubleWriteGuard` |
| Client sends huge `X-Request-Id` (4 KB) | `uuid.Parse` rejects it; middleware mints a fresh v7. | `TestRejectsMalformedID` |
| Client retries idempotent POST with new key | New key → new processing; the prior key's record stays cached for 24 h. | `TestIdempotencyReplay` (variant) |
| Idempotency table grows unbounded | Sweep goroutine deletes rows older than `idempotency_ttl_sec` (default 86 400) every 5 min. | `TestSweep` (in `idempotency` pkg) |
| Panic in middleware itself (not handler) | Same `Recoverer` catches it (it sits above the user handler in the chain). | `TestPanickingMiddleware` |
| `Idempotency-Key` not a valid UUID v7 | Accepted as an opaque string (the spec doesn't constrain key form); the conflict semantic is per-string. | Integration test `TestIdempotencyOpaqueKey` |
| Request body exceeds 1 MiB during idempotency capture | `LimitReader` truncates to 1 MiB; the cap-induced hash differs from any legitimate body. | Story 7.19 owns the body cap; this story trusts that limit. |
| Concurrent idempotent retries arrive simultaneously | The first to `INSERT` wins (PRIMARY KEY); the second sees the row and returns the cached response. | `TestConcurrentReplay` |
| `SIGINT` instead of `SIGTERM` | Same code path (both registered with `signal.NotifyContext`). | `TestShutdownDrainsInflight` (variant) |
| Forbidden `http.Error` slips into a vendored package | The analyzer is configured to scan `./api/...` only; vendor is excluded by `go vet` defaults. | analyzer unit test |

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/go-chi/chi/v5` | v5.0+ | Tree-based router with middleware chains; matches architecture §1.2. |
| `github.com/google/uuid` | v1.6+ | UUID v7 native. |
| `github.com/jackc/pgx/v5` | already pinned | Postgres driver; idempotency store reuses it. |
| `golang.org/x/tools/go/analysis` | latest | For the lint analyzer. |

No new build-time tooling beyond `go vet` and `goose`.

## 9. Acceptance checklist

**Wiring**
- [ ] `api/cmd/api/main.go` builds and starts the server on the configured port.
- [ ] `chi` middleware order matches §1; out-of-order causes a unit test to fail.
- [ ] `httperror.Write` is the only place that emits `application/problem+json`.

**AC-1 — RFC 9457 envelope**
- [ ] Every error path produces a body with `{type, title, status, detail, instance, requestId}`.
- [ ] `Content-Type: application/problem+json` on every error response.

**AC-2 — Request ID propagation**
- [ ] Missing header → v7 minted; valid v7 → reused.
- [ ] `X-Request-Id` echoed in response.
- [ ] Every `slog` line emitted during the request carries `request_id=<uuid>`.

**AC-3 — Graceful shutdown**
- [ ] SIGTERM/SIGINT trigger `srv.Shutdown(grace)`.
- [ ] In-flight requests finish if within grace; otherwise connections are closed.
- [ ] `TestShutdown*` integration tests pass.

**AC-4 — Idempotency**
- [ ] Same key + same body → cached replay (handler invoked once).
- [ ] Same key + different body → 409 `idempotency-key-conflict`.
- [ ] TTL sweep runs every `idempotency_sweep_interval_sec`.

**Lint**
- [ ] `forbidhttperror` analyzer fires on a synthesized `http.Error` call site.
- [ ] CI fails on a PR that introduces a non-`httperror` error path.

**Docs**
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.1.
- [ ] The problem-type URI registry is exported from `httperror/types.go` and referenced in the API reference doc.
