# Implementation Plan — Story 7.19 Validation, Body Limits, Rate Limiting

> Companion to [story-07-19-validation-rate-limiting.md](story-07-19-validation-rate-limiting.md).
> Cross-cutting middleware every other handler depends on.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Body cap | Default 1 MiB; per-route override via a router-level helper. |
| Content-Type | `application/json` for REST, `application/graphql+json` (or `application/json`) for `/graphql`. |
| Validation | `github.com/go-playground/validator/v10` reading struct tags; the validator package is the single source of error formatting. |
| Rate limit | Token-bucket per identity (user + IP) using `golang.org/x/time/rate`. State held per-process (no Redis); Story 7.20 covers metrics. |
| Auth-specific limits | `/api/auth/login` (10/min/IP) and the broader `/api/auth/*` (30/min/IP) are owned by Epic 10 Story 10.12 — this story implements the underlying middleware they plug into. |
| Out of scope | Auth verification itself (Epic 10), per-tenant pricing-tier limits. |

## 1. Architecture diagram

```
   incoming request
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ middleware chain (order matters):                           │
   │  1. requestID (Story 7.1)                                   │
   │  2. recoverer  (Story 7.1)                                  │
   │  3. logger     (Story 7.1)                                  │
   │  4. ipLimiter   (this story)                                │
   │  5. auth (Epic 10) — populates user; before user-rate-limit │
   │  6. userLimiter  (this story)                               │
   │  7. contentType (this story; mutating routes only)          │
   │  8. bodyLimit   (this story; per-route)                     │
   │  9. validate    (this story; struct-tag binding)            │
   │ 10. handler                                                 │
   └─────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/middleware/bodylimit.go` | `BodyLimit(n)` returning a chi.Middleware. |
| `api/internal/middleware/contenttype.go` | Content-type enforcement. |
| `api/internal/middleware/ratelimit.go` | Per-IP and per-user token-bucket store. |
| `api/internal/middleware/validate.go` | Helper around `go-playground/validator/v10`. |
| `api/internal/middleware/*_test.go` | Unit tests. |

## 3. Body limit

```go
// api/internal/middleware/bodylimit.go
package middleware

import (
    "io"
    "net/http"

    "maktaba/api/internal/httperror"
)

func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            if r.ContentLength > maxBytes {
                httperror.Write(w, r, &httperror.Error{
                    Type: httperror.TypeBodyTooLarge,
                    Title: "payload too large", Status: 413,
                    Detail: "body must be at most "+sizeText(maxBytes),
                })
                return
            }
            r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
            next.ServeHTTP(w, r)
        })
    }
}
```

Per-route override is done via `r.With(BodyLimit(8<<10)).Patch(...)`.
Default at the global level is `1 << 20` (1 MiB).

## 4. Content-Type

```go
// api/internal/middleware/contenttype.go
package middleware

import (
    "net/http"
    "strings"

    "maktaba/api/internal/httperror"
)

func ContentTypeJSON(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        switch r.Method {
        case http.MethodGet, http.MethodHead, http.MethodDelete, http.MethodOptions:
            next.ServeHTTP(w, r); return
        }
        ct := stripParams(r.Header.Get("Content-Type"))
        if ct != "application/json" && ct != "application/graphql+json" {
            httperror.Write(w, r, &httperror.Error{
                Type: httperror.TypeUnsupportedMediaType,
                Title: "unsupported media type", Status: 415,
                Detail: "expected application/json",
            })
            return
        }
        next.ServeHTTP(w, r)
    })
}

func stripParams(s string) string {
    if i := strings.Index(s, ";"); i >= 0 { s = s[:i] }
    return strings.TrimSpace(strings.ToLower(s))
}
```

## 5. Validator helper

```go
// api/internal/middleware/validate.go
package middleware

import (
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "reflect"
    "strings"

    "github.com/go-playground/validator/v10"

    "maktaba/api/internal/httperror"
)

var v10 = func() *validator.Validate {
    v := validator.New()
    v.RegisterTagNameFunc(func(fld reflect.StructField) string {
        if name, ok := fld.Tag.Lookup("json"); ok {
            return strings.SplitN(name, ",", 2)[0]
        }
        return fld.Name
    })
    v.RegisterValidation("uuid_v7", validateUUIDv7)
    v.RegisterValidation("iso639_1", validateISO639_1)
    return v
}()

// Bind decodes the JSON body and runs struct-tag validation in one go.
// Caller does:  if err := middleware.Bind(r, &in); err != nil { httperror.Write(w, r, err); return }
func Bind(r *http.Request, dst any) *httperror.Error {
    if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
        if errors.Is(err, io.EOF) {
            return httperror.BadRequest("body is empty")
        }
        return &httperror.Error{Type: httperror.TypeInvalidJSON, Title: "invalid json", Status: 400, Detail: err.Error()}
    }
    if err := v10.Struct(dst); err != nil {
        return toFieldErrors(err)
    }
    return nil
}

func toFieldErrors(err error) *httperror.Error {
    var verrs validator.ValidationErrors
    if !errors.As(err, &verrs) {
        return httperror.BadRequest(err.Error())
    }
    fields := make([]httperror.FieldError, 0, len(verrs))
    for _, e := range verrs {
        fields = append(fields, httperror.FieldError{Field: e.Namespace(), Message: msgFor(e)})
    }
    return httperror.Unprocessable(fields)
}

func msgFor(e validator.FieldError) string {
    switch e.Tag() {
    case "required":  return "is required"
    case "uuid", "uuid_v7": return "must be a valid UUID"
    case "min":       return "must be at least " + e.Param()
    case "max":       return "must be at most "  + e.Param()
    case "oneof":     return "must be one of: "  + e.Param()
    default:          return "validation failed: " + e.Tag()
    }
}
```

## 6. Rate limit

```go
// api/internal/middleware/ratelimit.go
package middleware

import (
    "net/http"
    "strconv"
    "sync"
    "time"

    "golang.org/x/time/rate"

    "maktaba/api/internal/httperror"
)

type rlStore struct {
    mu     sync.Mutex
    rate   rate.Limit
    burst  int
    bucket map[string]*entry
    sweep  time.Time
}

type entry struct {
    l    *rate.Limiter
    last time.Time
}

func newStore(perMin int) *rlStore {
    return &rlStore{
        rate:   rate.Limit(float64(perMin) / 60.0),
        burst:  perMin,
        bucket: map[string]*entry{},
    }
}

func (s *rlStore) take(key string) (allow bool, retryAfter int) {
    s.mu.Lock()
    e, ok := s.bucket[key]
    if !ok {
        e = &entry{l: rate.NewLimiter(s.rate, s.burst)}
        s.bucket[key] = e
    }
    e.last = time.Now()
    s.mu.Unlock()

    if e.l.Allow() { return true, 0 }
    // Headroom: how long until the next token is available.
    secs := int(time.Duration(float64(time.Second) / float64(s.rate)).Seconds()) + 1
    return false, secs
}

func PerIP(perMin int) func(http.Handler) http.Handler {
    s := newStore(perMin)
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            ip := clientIP(r)
            if ok, retry := s.take("ip:"+ip); !ok {
                w.Header().Set("Retry-After", strconv.Itoa(retry))
                httperror.Write(w, r, &httperror.Error{
                    Type: httperror.TypeRateLimited,
                    Title: "too many requests", Status: 429,
                    Detail: "per-IP rate limit exceeded",
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

func PerUser(perMin int) func(http.Handler) http.Handler {
    s := newStore(perMin)
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            u := userFromCtx(r.Context())
            key := "user:" + u.ID.String()
            // exempt routes (e.g. progress sync — Story 7.11)
            if isExempt(r.URL.Path) { next.ServeHTTP(w, r); return }
            if ok, retry := s.take(key); !ok {
                w.Header().Set("Retry-After", strconv.Itoa(retry))
                httperror.Write(w, r, &httperror.Error{
                    Type: httperror.TypeRateLimited,
                    Title: "too many requests", Status: 429,
                })
                return
            }
            next.ServeHTTP(w, r)
        })
    }
}

func isExempt(path string) bool {
    return strings.Contains(path, "/progress")
}
```

A background sweeper purges entries idle for more than 10 min so the
map doesn't grow unbounded.

## 7. Wiring (in router)

```go
// router.New()
r.Use(mw.RequestID)
r.Use(mw.Recoverer)
r.Use(mw.SLogLogger)
r.Use(mw.PerIP(cfg.IPRatePerMin))            // 6000/min default
r.Use(mw.MaybeAuth)                           // Epic 10
r.Use(mw.PerUser(cfg.DefaultRatePerMin))      // 600/min default
r.Group(func(r chi.Router) {
    r.Use(mw.ContentTypeJSON)
    r.Use(mw.BodyLimit(1 << 20))              // 1 MiB default
    libraries.Mount(r, ...)
    videos.Mount(r, ...)
    /* per-route overrides */
    r.Route("/api/videos/{id}", func(r chi.Router) {
        r.With(mw.BodyLimit(8 << 10)).Patch("/", videos.PatchHandler)
    })
    r.Route("/api/search", func(r chi.Router) {
        r.With(mw.BodyLimit(16 << 10)).Post("/", search.SearchHandler)
    })
})
```

## 8. Test plan

### 8.1 Unit (`*_test.go`)

| Test | What it pins |
|---|---|
| `TestBodyLimitContentLength` | `Content-Length: 2_000_000` with cap 1 MiB → 413 before handler runs. |
| `TestBodyLimitFakeContentLength` | `Content-Length: 1_000_000_000` with body of 1 KB → `MaxBytesReader` truncates; handler reads 0 bytes after trip. | 
| `TestContentTypeRejected` | POST without `Content-Type` → 415. |
| `TestContentTypeAcceptsCharset` | `application/json; charset=utf-8` → accepted. |
| `TestValidateRequiredField` | `{}` for a struct with `validate:"required"` → 422 with `field`/`message`. |
| `TestValidateUUIDInvalid` | `{id:"abc"}` for `validate:"uuid"` → 422. |
| `TestPerIPLimitBlocks` | 7000 reqs in 60 s from one IP, cap 6000 → ~1000 are 429. |
| `TestPerUserLimitBlocks` | 700 reqs/min/user, cap 600 → ~100 are 429. |
| `TestPerUserLimitProgressExempt` | Reqs to `/api/stream/sessions/{id}/progress` → never 429 from this middleware. |
| `TestRetryAfterPresent` | 429 response always includes `Retry-After`. |

### 8.2 Integration

| Test | What it pins |
|---|---|
| `TestPatchVideoSizeOverride` | PATCH with 16 KB body → 413 because the route has 8 KB cap; PATCH with 4 KB body → 200. |
| `TestSearchSizeOverride` | POST `/api/search` with 32 KB body → 413 (cap 16 KB); 8 KB → 200. |
| `TestMalformedJSON` | POST with `{not json}` → 400 `invalid-json`, not 500. |
| `TestSecurityFakeContentLength` | Header lies about size; `LimitReader` still caps bytes read. | 

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Body equals exactly the cap | Accepted (`<= max`). | Unit |
| Per-IP cap shared across NAT | Per-IP cap is 10× the per-user cap, sized for legitimate office NAT. | Documented |
| Per-user limit reached during a long request | Already-in-flight request continues; the next one starts at 429. | Documented |
| Progress endpoint flooded | Excluded from per-user limit; debouncer (Story 7.11) handles its own backpressure. | `TestPerUserLimitProgressExempt` |
| Login endpoint specific cap | Owned by Epic 10 Story 10.12; built on top of `PerIP(...)` with 10/min. The middleware here is reusable. | Documented |
| `Content-Length` missing on chunked transfer | `MaxBytesReader` still caps the actual bytes read. | `TestBodyLimitFakeContentLength` |
| Invalid `Retry-After` would arise from rate=0 | Guarded: rate-limit middleware rejects construction with `perMin <= 0`. | Constructor unit |
| GraphQL endpoint sends `application/json` | Accepted (we allow both `application/json` and `application/graphql+json`). | Documented |

## 10. Acceptance checklist

- [ ] Body cap default 1 MiB; per-route overrides supported.
- [ ] `Content-Type` enforcement on mutating routes.
- [ ] Struct-tag validation produces `422` with `errors[]`.
- [ ] Per-user 600/min and per-IP 6000/min defaults.
- [ ] Progress-sync exempt from per-user limit.
- [ ] `Retry-After` header on every 429.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.19.
