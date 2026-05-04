# Implementation Plan — Story 10.14 Secret loading and redaction

> Companion to [story-10-14-secret-loading.md](story-10-14-secret-loading.md).
> Builds on Epic 7 Story 7.15's settings loader and `/api/settings`
> handler. The architecture rule comes from §11.5: secrets only in env
> or config file, never in DB; never logged; never returned by
> `/api/settings`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Loader precedence | env > file > defaults. Already implemented in Story 7.15; this story adds the *ignored-file-secret* logging path. |
| Redacting logger | `api/internal/logging/redact.go` — `slog.Handler` wrapper that scans attributes by tag (`secret:"true"`) and by name (`token`, `secret`, `key`, `password` substrings). |
| Header redaction | Middleware `api/internal/http/middleware/log_request.go` — strips `Authorization`, `Cookie`, `X-Maktaba-CSRF`. Also normalizes `?sig=...` → `?sig=<redacted>` in the logged URL. |
| Settings redaction | `api/internal/http/settings.go` (Story 7.15) — emits `<redacted>` for tagged secret fields; sibling `<key>_present: bool`. |
| gRPC interceptor | `shared/grpcmw/redact.go` — server-side interceptor strips `authorization` and `*-token` keys before logging or OTel attribute emission. |
| Out of scope | Settings loader itself (Story 7.15). Audit log retention (Story 10.16). |

## 1. Architecture diagram

```
struct ConfigField { *string `secret:"true"` }
                          │
                          ▼
          ┌─────────────────────────────────────────────────┐
          │ slog logging path                                │
          │   handler := redact.Wrap(slog.NewJSONHandler())  │
          │   handler.WithAttrs([..]) → redacted at emit time│
          │     - tagged secret field → "<redacted>"          │
          │     - name match (token|secret|key|password)      │
          │       → "<redacted>" (unless `notsecret:"true"`)  │
          └─────────────────────────────────────────────────┘

          ┌─────────────────────────────────────────────────┐
          │ HTTP request log middleware                      │
          │   url := r.URL.String()                          │
          │   url := redactQueryParam(url, "sig")            │
          │   headers := drop("Authorization","Cookie",      │
          │                    "X-Maktaba-CSRF")              │
          │   slog.Info("req", "url", url, "headers", h)     │
          └─────────────────────────────────────────────────┘

          ┌─────────────────────────────────────────────────┐
          │ /api/settings (Story 7.15) — redacted view       │
          │   for each field with secret:"true":             │
          │     out[k]            = "<redacted>"             │
          │     out[k+"_present"] = (val != "")              │
          └─────────────────────────────────────────────────┘

          ┌─────────────────────────────────────────────────┐
          │ gRPC server interceptor (shared/grpcmw/redact.go)│
          │   on UnaryServerInterceptor:                      │
          │     md := metadata.FromIncomingContext(ctx)       │
          │     for k in {authorization, *-token}:            │
          │        md[k] = ["<redacted>"]                     │
          │     traceAttrs strips same keys                   │
          └─────────────────────────────────────────────────┘
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/logging/redact.go` | `redact.Wrap(slog.Handler) slog.Handler`; struct/attribute introspection. |
| `api/internal/logging/redact_test.go` | Unit tests. |
| `api/internal/http/middleware/log_request.go` | Request logger with header strip + URL redact. |
| `api/internal/http/middleware/log_request_test.go` | Tests. |
| `shared/grpcmw/redact.go` | gRPC interceptor. |
| `shared/grpcmw/redact_test.go` | Interceptor tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Tag every secret field with `secret:"true"` (e.g., `JWTPrivateKeyEnv` is the env var *name*, not a secret; the loaded value is). Confirm tagging on `Auth.AdminToken` (loaded value). |
| `api/internal/http/settings.go` | (Story 7.15) Add the `*_present` siblings; ensure all secret-tagged fields are masked. |
| `api/cmd/api/main.go` | Install redacting slog handler; install gRPC interceptor on the API server's gRPC clients (when logging gRPC traffic). |
| `streaming/cmd/streaming/main.go` | Same. |
| `pipeline/src/maktaba_pipeline/observability.py` | Python equivalent: structlog processor that drops `authorization` from request logs and replaces secret-tagged keys (model: pydantic field metadata `secret=True`). Mirror the Go behavior. |

### 2.3 Type definitions

```go
// api/internal/logging/redact.go
package logging

import (
    "context"
    "log/slog"
    "reflect"
    "strings"
)

const Placeholder = "<redacted>"

// SecretNamePatterns - case-insensitive substring match. A field whose
// name contains any of these is treated as secret unless the field
// also has the `notsecret:"true"` tag.
var SecretNamePatterns = []string{"token", "secret", "key", "password", "api_key"}

// Wrap returns a slog.Handler that redacts attributes by tag and name.
type RedactingHandler struct {
    inner slog.Handler
}

func Wrap(h slog.Handler) slog.Handler { return &RedactingHandler{h} }

func (h *RedactingHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.inner.Enabled(ctx, l) }
func (h *RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
    redacted := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
    r.Attrs(func(a slog.Attr) bool {
        redacted.AddAttrs(redactAttr(a))
        return true
    })
    return h.inner.Handle(ctx, redacted)
}

func (h *RedactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
    out := make([]slog.Attr, len(attrs))
    for i, a := range attrs { out[i] = redactAttr(a) }
    return &RedactingHandler{h.inner.WithAttrs(out)}
}
func (h *RedactingHandler) WithGroup(name string) slog.Handler {
    return &RedactingHandler{h.inner.WithGroup(name)}
}
```

### 2.4 Function signatures

```go
func redactAttr(a slog.Attr) slog.Attr   // in §3
func RedactURL(rawurl string, params ...string) string  // §4
func StripSensitiveHeaders(h http.Header)               // §4
```

## 3. Redacting logger

```go
// api/internal/logging/redact.go (cont.)

func redactAttr(a slog.Attr) slog.Attr {
    if matchesSecretName(a.Key) {
        return slog.String(a.Key, Placeholder)
    }
    switch v := a.Value.Any().(type) {
    case string:
        return a   // unchanged unless name matched above
    case error:
        return slog.String(a.Key, v.Error())   // errors don't carry secret tags
    default:
        // For structs, walk fields by reflection and rebuild the value.
        rv := reflect.ValueOf(v)
        if rv.Kind() == reflect.Pointer { rv = rv.Elem() }
        if rv.Kind() != reflect.Struct { return a }
        return slog.Group(a.Key, redactStructFields(rv)...)
    }
}

func matchesSecretName(name string) bool {
    n := strings.ToLower(name)
    for _, p := range SecretNamePatterns {
        if strings.Contains(n, p) { return true }
    }
    return false
}

func redactStructFields(rv reflect.Value) []any {
    rt := rv.Type()
    var out []any
    for i := 0; i < rv.NumField(); i++ {
        f := rt.Field(i)
        name := f.Name
        if jt := f.Tag.Get("json"); jt != "" {
            name = strings.SplitN(jt, ",", 2)[0]
        }
        secretTag, _ := f.Tag.Lookup("secret")
        notsecretTag, _ := f.Tag.Lookup("notsecret")
        if secretTag == "true" || (matchesSecretName(name) && notsecretTag != "true") {
            out = append(out, slog.String(name, Placeholder))
            continue
        }
        out = append(out, slog.Any(name, rv.Field(i).Interface()))
    }
    return out
}
```

The reflection walk is intentionally shallow (one struct level). Nested
secrets must use the explicit tag; this keeps the hot path fast.

## 4. Request logger

```go
// api/internal/http/middleware/log_request.go
func LogRequests(logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := &responseWriter{ResponseWriter: w, status: 200}

            next.ServeHTTP(ww, r)

            url := redactQueryParam(r.URL.String(), "sig")
            headers := stripSensitiveHeaders(r.Header.Clone())

            logger.Info("http_request",
                "method", r.Method,
                "url", url,
                "status", ww.status,
                "dur_ms", time.Since(start).Milliseconds(),
                "headers", headers,
            )
        })
    }
}

func redactQueryParam(rawurl, name string) string {
    u, err := url.Parse(rawurl)
    if err != nil { return rawurl }
    q := u.Query()
    if q.Get(name) != "" {
        q.Set(name, logging.Placeholder)
        u.RawQuery = q.Encode()
    }
    return u.String()
}

var sensitiveHeaders = []string{"Authorization", "Cookie", "X-Maktaba-CSRF", "X-Maktaba-Admin-Token"}

func stripSensitiveHeaders(h http.Header) http.Header {
    for _, k := range sensitiveHeaders {
        if h.Get(k) != "" { h.Set(k, logging.Placeholder) }
    }
    return h
}
```

Note: we set the header value to `<redacted>` (rather than deleting)
so log readers can confirm the header *was* present. This matters for
debugging "why did auth fail" without exposing the value.

## 5. Settings handler

`api/internal/http/settings.go` (additions, building on Story 7.15):

```go
func renderSettings(s Settings) map[string]any {
    out := map[string]any{}
    rv := reflect.ValueOf(s)
    rt := rv.Type()
    for i := 0; i < rv.NumField(); i++ {
        f := rt.Field(i)
        name := f.Name
        if jt := f.Tag.Get("json"); jt != "" {
            name = strings.SplitN(jt, ",", 2)[0]
        }
        secretTag := f.Tag.Get("secret") == "true"
        notSecretTag := f.Tag.Get("notsecret") == "true"
        secretByName := logging.MatchesSecretName(name) && !notSecretTag

        val := rv.Field(i).Interface()
        if secretTag || secretByName {
            out[name] = "<redacted>"
            out[name+"_present"] = !isZero(val)
            continue
        }
        out[name] = val
    }
    return out
}
```

Tests use a regex assertion that the response body contains no
plaintext secrets — see §7.

## 6. gRPC interceptor

```go
// shared/grpcmw/redact.go
package grpcmw

import (
    "context"
    "strings"

    "google.golang.org/grpc"
    "google.golang.org/grpc/metadata"
)

var sensitiveMetaKeys = []string{"authorization"}
var sensitiveMetaSuffixes = []string{"-token"}

func redactMD(md metadata.MD) metadata.MD {
    out := metadata.MD{}
    for k, v := range md {
        lk := strings.ToLower(k)
        if isSensitive(lk) {
            out[k] = []string{"<redacted>"}
        } else {
            out[k] = append([]string{}, v...)
        }
    }
    return out
}

func isSensitive(k string) bool {
    for _, s := range sensitiveMetaKeys { if k == s { return true } }
    for _, s := range sensitiveMetaSuffixes { if strings.HasSuffix(k, s) { return true } }
    return false
}

func UnaryServerLoggingInterceptor(logger *slog.Logger) grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
        md, _ := metadata.FromIncomingContext(ctx)
        logger.Info("grpc_call", "method", info.FullMethod, "metadata", redactMD(md))
        return handler(ctx, req)
    }
}
```

The OTel side: an OTel span attribute decorator strips the same keys
before they're attached to spans.

## 7. Test plan

### 7.1 Logger redaction (`redact_test.go`)

| Test | What it pins |
|---|---|
| `TestRedactsTaggedField` | Struct with `Pw string `secret:"true"`` → log shows `"pw":"<redacted>"`. |
| `TestRedactsByName_Token` | Field named `Token` (no tag) → redacted. |
| `TestRedactsByName_Password` | Field named `Password` → redacted. |
| `TestNotSecretTagOptsOut` | Field named `MyToken` with `notsecret:"true"` → not redacted. |
| `TestPlainStringNotRedacted` | Attribute `slog.String("user", "alice")` → unchanged. |
| `TestNestedStructFlattened` | Nested struct passed as a value: top-level fields walked; nested fields require explicit tags (documented limit). |
| `TestErrorAttributeNotRedacted` | `slog.Any("err", errors.New("boom"))` → "boom" emitted (errors are not secrets even when they mention a secret-named key). |

### 7.2 Request logger (`log_request_test.go`)

| Test | What it pins |
|---|---|
| `TestRequestLogStripsAuthorization` | Request with `Authorization: Bearer xyz` → log entry has `Authorization: <redacted>`. |
| `TestRequestLogStripsCookie` | Same for `Cookie`. |
| `TestRequestLogStripsCSRF` | `X-Maktaba-CSRF: …` → redacted. |
| `TestRequestLogRedactsSigQueryParam` | URL `/stream/x?sig=abcd` → log shows `?sig=<redacted>`. |
| `TestRequestLogPreservesNonSensitiveHeaders` | `Accept`, `User-Agent`, etc. → unchanged. |
| `TestRequestLogPreservesNonSensitiveQuery` | `?cursor=abc` → unchanged. |

### 7.3 Settings response

| Test | What it pins |
|---|---|
| `TestSettingsResponseHasNoPlaintextSecrets` | Stand up a config with `MAKTABA_ADMIN_TOKEN=verysecretvalue` set; GET `/api/settings`; assert the response body does NOT contain `verysecretvalue` (regex test on raw response). |
| `TestSettingsExposesPresentBoolean` | Same; assert `admin_token_present == true`. |
| `TestSettingsRedactsByName` | A field named `slack_webhook_url` (URL containing token) is redacted; sibling `slack_webhook_url_present == true`. |
| `TestSettingsNotSecretOptOut` | A field named `public_token` with `notsecret:"true"` → emitted in plaintext (documented opt-out). |

### 7.4 gRPC interceptor

| Test | What it pins |
|---|---|
| `TestInterceptorStripsAuthorizationMD` | Outgoing/incoming metadata `authorization=Bearer xyz` → log emits `<redacted>`. |
| `TestInterceptorStripsAnyTokenSuffix` | Metadata `x-internal-token=abc` → redacted. |
| `TestInterceptorPreservesOtherKeys` | `x-request-id=...` → emitted as-is. |
| `TestOTelAttributesAlsoStripped` | A test span carries the same metadata; attribute extractor produces no `authorization` attribute. |

### 7.5 Env vs file precedence (consumes Story 7.15)

| Test | What it pins |
|---|---|
| `TestEnvWinsOverFile` | TOML has `jwt_private_key_pem = "<file-value>"`; env var with the same logical key set → loaded value matches env; one INFO log line says "ignoring file value for jwt_private_key_pem (env override present)". |
| `TestFileUsedWhenEnvAbsent` | TOML has the value, env empty → loaded value matches file. |
| `TestNeitherDefaultsToEmpty` | Neither set → loaded value empty; downstream consumer (e.g., Keyring) refuses to start with the documented error. |

## 8. Edge cases — handling table

| Case | Behaviour | Where pinned |
|---|---|---|
| User-defined config key `my_token` | Redacted by name match. To opt out, add `notsecret:"true"`. | `TestRedactsByName_Token` |
| Signed URL in the response body of an audit row | The audit row payload is JSON; a URL field there should be tagged `secret:"true"` on the payload struct OR pre-redacted at write time. The audit sink is owned by Story 10.16; it reuses `RedactURL`. | Story 10.16 plan |
| Nested struct with secrets | The redactor walks one level. Deeper nesting must be tagged explicitly. Documented limit. | `TestNestedStructFlattened` |
| Logger called with `slog.With("authorization", token)` (a programmer mistake) | The handler still redacts `authorization` by name match → safe. | `TestRedactsByName_Token` (variant) |
| OTel attribute set directly via `span.SetAttributes(attribute.String("authorization", ...))` | The attribute decorator wraps the OTel `Tracer` so the same name-match list applies. | `TestOTelAttributesAlsoStripped` |
| Logging at WARN/ERROR with a struct that has no tags | Same as INFO — redaction is at the handler layer, not per-call. | n/a |
| A secret value that happens to equal the literal string `<redacted>` | Distinguishable in DB but not in logs; documented as harmless. | n/a |
| TOML with both env and file for the same key | Env wins; file value logged as ignored. | `TestEnvWinsOverFile` |
| File contains the env-var *name* not the value (operator confusion) | The loader reads the env-var-name field, then `os.Getenv(<name>)`; if the env var isn't set, falls back to the literal value. We add a startup WARN if the literal looks like an env-var name (matches `^[A-Z_]+$`). | `TestSuspiciousLiteralWarn` |

## 9. Dependencies

| Dep | Version | Why |
|---|---|---|
| `log/slog` | stdlib | Logger. |
| `reflect` | stdlib | Tag introspection. |
| `google.golang.org/grpc` | already | Interceptor. |
| `go.opentelemetry.io/otel` | already | OTel attribute decoration. |

No new heavy deps.

## 10. Acceptance checklist

**Loader**
- [ ] AC-1: env wins over file; file value logged as ignored.

**Logger**
- [ ] AC-2: `secret:"true"` fields render as `<redacted>` via slog.
- [ ] Sensitive header stripping in the request logger covers `Authorization`, `Cookie`, `X-Maktaba-CSRF`.
- [ ] `?sig=...` rewritten to `?sig=<redacted>` in logged URLs.

**Settings**
- [ ] AC-3: every secret-bearing key in `/api/settings` is `<redacted>`; sibling `*_present` boolean reports presence.

**gRPC**
- [ ] AC-4: `authorization` and `*-token` metadata stripped from server-side logs and OTel attributes.

**Name-match defaults**
- [ ] Names containing `token`, `secret`, `key`, `password`, `api_key` redacted unless `notsecret:"true"`.

**Tests**
- [ ] All §7 tests pass; settings regex test guarantees no plaintext leak.

**Docs**
- [ ] README.md ticks story 10.14.
- [ ] CONTRIBUTING.md updated: "if you add a secret config field, tag it `secret:\"true\"`."
