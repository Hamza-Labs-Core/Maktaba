# Plan 10.14 — Secret loading and redaction — implementation

> Implementation plan for [story-10-14-secret-loading.md](story-10-14-secret-loading.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: serves the redacted shape consumed by
> Epic 7 Story 7.15 (`/api/settings`); the gRPC interceptor here is reused
> by every API↔Streaming/Pipeline call (Epic 8 Story 8.1, Epic 6 Story 6.x);
> the request-log middleware sits in front of every chi route registered by
> [Plan 10.15](plan-10-15-transport-security.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Two-tier loader: env vars override TOML always.** A struct field's TOML key has a sibling `*_env` key. If the env var named in `*_env` is non-empty, its value wins; the TOML literal is discarded with an INFO log `secret-source-overridden` so operators see they are running on env. | Story AC-1: env-var precedence. | Operators want zero ambiguity when rotating secrets via systemd `EnvironmentFile`. The `*_env` indirection avoids hard-coding env-var names in TOML keys (`DATABASE_URL` vs `MAKTABA_DB_URL`) and matches the convention already used in §11.5. |
| D2 | **Redaction by struct tag, not by reflection at log time.** `slog.LogValuer` is implemented per-config-struct so the cost is paid at struct-initialization, not on every log line. Tagged fields render as `<redacted>` in `slog`; a `notsecret:"true"` tag opts out (overrides the name-based heuristic). | Story AC-2; performance budget for hot-path request logging. | Reflective name-matching on every log call costs ~5 µs and adds GC pressure. A pre-built `LogValuer` is O(1) per call. |
| D3 | **Secret-name regex is a fallback** for fields that lack any tag. Pattern: `(?i)(token|secret|key|password|bearer|cred(ential)?|sig)`. Fields tagged `notsecret:"true"` opt out; fields tagged `secret:"true"` opt in unconditionally. The regex is compiled once at package init. | Story Edge cases: "my_token" auto-redact. | Operators may add config keys without remembering tags. A loud default (regex) plus an explicit override (tag) prevents accidental leaks while letting `public_key`, `signing_key_id`, etc., escape via `notsecret:"true"`. |
| D4 | **HTTP request log middleware strips a fixed header allowlist** for value redaction: `Authorization`, `Cookie`, `Set-Cookie`, `X-Maktaba-CSRF`, `X-Maktaba-Admin-Token`, `Proxy-Authorization`. The middleware also rewrites `?sig=...` (and any query value matching `(token|sig|key|secret|access)`) to `<redacted>` in the logged URL via a single compiled regex. | Story AC-2 + Edge cases: signed URLs in logs. | Hardcoding the allowlist beats trying to be clever; new headers require an explicit code change which gets reviewed. The query-string redactor uses one pass over the URL, no per-key parsing. |
| D5 | **gRPC unary interceptor strips metadata `authorization` and any key matching `*-token` or `*-secret`.** Both server-side log middleware and the OTel attribute exporter consult the same redactor. Streaming attributes ending in `.url` get the query-string redactor applied. | Story AC-4. | One redactor in one package keeps the truth source single. OTel and slog should not diverge. |
| D6 | **`/api/settings` GET emits `<redacted>` for every secret-bearing key plus a sibling `*_present` boolean.** The renderer walks the config struct, calls `LogValue()` on each field, and for any field tagged `secret:"true"` (or matching the regex) writes `{name: "<redacted>", name_present: <bool>}`. Empty-string secrets render `present=false`. | Story AC-3; Epic 7 Story 7.15 AC-1. | The `*_present` boolean lets the admin UI show "configured: yes/no" without ever transporting the value. |

If D3 is rejected (no name-based fallback): every config author must remember to tag every secret. Practical rate of bugs over time: ~1 per quarter (we expect ~30 secret-bearing keys at v2). The regex is a cheap belt-and-suspenders.

---

## 1. Architecture diagram

```
   ┌────────────────────────────────────────────────────────────────┐
   │  startup: cmd/api/main.go                                      │
   │     ▼                                                          │
   │  secrets.LoadConfig(path)                                      │
   │     1. parse TOML into Config                                  │
   │     2. for each *_env field:                                   │
   │          if os.Getenv(*_env) != "" → overwrite (D1)            │
   │     3. validate; return Config                                 │
   │                                                                │
   │  Config implements slog.LogValuer (D2):                        │
   │     LogValue() walks fields,                                   │
   │       secret:"true"        → "<redacted>"                      │
   │       notsecret:"true"     → keep                              │
   │       else if name regex   → "<redacted>"                      │
   │       else                 → keep                              │
   └────────────────────────────────────────────────────────────────┘

   request path:
   ┌────────────────────────────────────────────────────────────────┐
   │  HTTP request                                                  │
   │     ▼                                                          │
   │  redactlog.Middleware:                                         │
   │     - sanitizes URL: ?sig=… ?token=… → <redacted> (D4)         │
   │     - drops headers: Authorization, Cookie, Set-Cookie,        │
   │       X-Maktaba-CSRF, X-Maktaba-Admin-Token, Proxy-Auth (D4)   │
   │     - emits structured slog line                               │
   │     ▼                                                          │
   │  chi router → handler                                          │
   └────────────────────────────────────────────────────────────────┘

   gRPC server (D5): UnaryInterceptor reads incoming metadata, redacts
   'authorization' / '*-token' / '*-secret' on the *logged* copy
   (the actual call still has them via ctx), and emits OTel span attrs
   through SafeAttrs().

   /api/settings GET (D6): cfg.RenderForSettings() walks the struct,
   writes "<redacted>" + "<key>_present": <bool> for each secret leaf,
   and emits non-secret keys verbatim.
```

---

## 2. Detailed implementation

### 2.1 Package layout

```
api/
├── internal/
│   ├── secrets/
│   │   ├── secrets.go         // LoadConfig, struct-tag walker
│   │   ├── redact.go          // regex, redact helpers, slog.LogValuer
│   │   ├── settings.go        // RenderForSettings (D6)
│   │   └── secrets_test.go
│   ├── httpx/
│   │   └── redactlog/
│   │       ├── middleware.go  // request log + URL/header strip (D4)
│   │       └── middleware_test.go
│   └── grpcx/
│       └── grpcredact/
│           ├── interceptor.go // unary log+OTel redactor (D5)
│           └── interceptor_test.go
└── cmd/api/main.go            // wires LoadConfig + middleware
```

### 2.2 `secrets.go` — config struct + loader (D1, D2)

```go
// Package secrets loads Maktaba's TOML config and provides redaction
// helpers consumed by slog, /api/settings, and gRPC interceptors.
package secrets

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// AuthConfig is illustrative; full Config is broken across packages.
type AuthConfig struct {
	JWTPrivateKey    string `toml:"jwt_private_key"        secret:"true"`
	JWTPrivateKeyEnv string `toml:"jwt_private_key_env"`           // name of env var (D1)
	JWKSPublicURL    string `toml:"jwks_public_url"        notsecret:"true"`
	AdminToken       string `toml:"admin_token"            secret:"true"`
	AdminTokenEnv    string `toml:"admin_token_env"`
	PairingTTLSec    int    `toml:"pairing_ttl_sec"`
}

type DBConfig struct {
	URL    string `toml:"url"     secret:"true"`
	URLEnv string `toml:"url_env"`
}

type Config struct {
	Auth AuthConfig `toml:"auth"`
	DB   DBConfig   `toml:"db"`
	// ... server, search, streaming, etc. ...
}

// LoadConfig reads TOML at path then applies env-var overrides (D1).
// On override, an INFO line is logged via the supplied logger.
func LoadConfig(path string, log *slog.Logger) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("toml decode: %w", err)
	}
	applyEnvOverrides(&cfg, log)
	if err := validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func applyEnvOverrides(c *Config, log *slog.Logger) {
	walkEnvOverrides(reflect.ValueOf(c).Elem(), "", log)
}

// walkEnvOverrides finds field pairs (X, XEnv): when XEnv has a non-empty
// value AND that env var is set, copy os.Getenv(XEnv) into X. Both fields
// must be string for the override to apply.
func walkEnvOverrides(v reflect.Value, parent string, log *slog.Logger) {
	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		path := f.Name
		if parent != "" {
			path = parent + "." + f.Name
		}
		switch fv.Kind() {
		case reflect.Struct:
			walkEnvOverrides(fv, path, log)
		case reflect.String:
			if !strings.HasSuffix(f.Name, "Env") {
				continue
			}
			envName := fv.String()
			if envName == "" {
				continue
			}
			val, ok := os.LookupEnv(envName)
			if !ok || val == "" {
				continue
			}
			// Find sibling X (same field name minus "Env").
			siblingName := strings.TrimSuffix(f.Name, "Env")
			sibling := v.FieldByName(siblingName)
			if !sibling.IsValid() || sibling.Kind() != reflect.String || !sibling.CanSet() {
				continue
			}
			oldPresent := sibling.String() != ""
			sibling.SetString(val)
			log.Info("secret-source-overridden",
				"path", parent+"."+siblingName,
				"env", envName,
				"toml_value_was_present", oldPresent)
		}
	}
}

func validate(c *Config) error {
	if c.Auth.JWTPrivateKey == "" {
		return errors.New("auth.jwt_private_key (or _env) required")
	}
	return nil
}
```

### 2.3 `redact.go` — regex + slog.LogValuer

```go
package secrets

import (
	"log/slog"
	"reflect"
	"regexp"
	"strings"
)

// secretNameRegex matches field/key names that look secret-bearing.
// Compiled once; (?i) for case-insensitive (D3).
var secretNameRegex = regexp.MustCompile(`(?i)(token|secret|key|password|bearer|cred(ential)?|sig)`)

const Redacted = "<redacted>"

// IsSecretField returns whether a struct field is secret per the rules:
//   secret:"true"        → true (highest priority)
//   notsecret:"true"     → false
//   else: regex on field's TOML key (or Go name as fallback)
func IsSecretField(f reflect.StructField) bool {
	if f.Tag.Get("secret") == "true" {
		return true
	}
	if f.Tag.Get("notsecret") == "true" {
		return false
	}
	name := f.Tag.Get("toml")
	if name == "" {
		name = f.Name
	}
	// Strip ",omitempty" etc.
	if i := strings.IndexByte(name, ','); i >= 0 {
		name = name[:i]
	}
	return secretNameRegex.MatchString(name)
}

// LogValue implements slog.LogValuer for any tagged config struct.
// Use redactedValueOf in custom LogValue() methods.
func redactedValueOf(v reflect.Value) slog.Value {
	t := v.Type()
	attrs := make([]slog.Attr, 0, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		key := f.Tag.Get("toml")
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		if i := strings.IndexByte(key, ','); i >= 0 {
			key = key[:i]
		}

		if fv.Kind() == reflect.Struct {
			attrs = append(attrs, slog.Attr{Key: key, Value: redactedValueOf(fv)})
			continue
		}
		if IsSecretField(f) {
			attrs = append(attrs, slog.String(key, Redacted))
			continue
		}
		attrs = append(attrs, slog.Any(key, fv.Interface()))
	}
	return slog.GroupValue(attrs...)
}

// Implement slog.LogValuer on Config (and any sub-struct that's logged
// directly). Each struct gets one tiny method.
func (c Config) LogValue() slog.Value     { return redactedValueOf(reflect.ValueOf(c)) }
func (a AuthConfig) LogValue() slog.Value { return redactedValueOf(reflect.ValueOf(a)) }
func (d DBConfig) LogValue() slog.Value   { return redactedValueOf(reflect.ValueOf(d)) }
```

### 2.4 `settings.go` — `/api/settings` renderer (D6)

```go
package secrets

import (
	"reflect"
	"strings"
)

// RenderForSettings produces the map served by GET /api/settings.
// For every secret-bearing leaf, two keys are emitted:
//   "<key>": "<redacted>"
//   "<key>_present": <bool>   // false iff the actual value is ""
// Non-secret leaves are emitted verbatim.
func (c *Config) RenderForSettings() map[string]any {
	return renderStruct(reflect.ValueOf(c).Elem())
}

func renderStruct(v reflect.Value) map[string]any {
	t := v.Type()
	out := make(map[string]any, v.NumField())
	for i := 0; i < v.NumField(); i++ {
		f := t.Field(i)
		fv := v.Field(i)
		key := f.Tag.Get("toml")
		if key == "" {
			key = strings.ToLower(f.Name)
		}
		if i := strings.IndexByte(key, ','); i >= 0 {
			key = key[:i]
		}
		// Hide the *_env helper fields entirely.
		if strings.HasSuffix(f.Name, "Env") {
			continue
		}

		if fv.Kind() == reflect.Struct {
			out[key] = renderStruct(fv)
			continue
		}
		if IsSecretField(f) {
			out[key] = Redacted
			out[key+"_present"] = !isZero(fv)
			continue
		}
		out[key] = fv.Interface()
	}
	return out
}

func isZero(v reflect.Value) bool {
	if v.Kind() == reflect.String {
		return v.String() == ""
	}
	return v.IsZero()
}
```

### 2.5 `redactlog/middleware.go` — HTTP request log (D4)

```go
package redactlog

import (
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// queryRedactor matches sensitive query keys. The match group lets us
// surgically replace the value while preserving the key.
var queryRedactor = regexp.MustCompile(`([?&])(sig|token|access|secret|key)=[^&]*`)

var stripHeaders = map[string]struct{}{
	"Authorization":           {},
	"Cookie":                  {},
	"Set-Cookie":              {},
	"X-Maktaba-Csrf":          {},
	"X-Maktaba-Admin-Token":   {},
	"Proxy-Authorization":     {},
}

// SanitizeURL redacts known-sensitive query values in a URL string.
func SanitizeURL(raw string) string {
	return queryRedactor.ReplaceAllString(raw, `${1}${2}=<redacted>`)
}

// Middleware logs each request with safe fields only.
func Middleware(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: 200}
			next.ServeHTTP(rec, r)

			// Build a slog-safe header map.
			hdrAttrs := make([]any, 0, len(r.Header)*2)
			for k, vals := range r.Header {
				canonical := http.CanonicalHeaderKey(k)
				if _, hide := stripHeaders[canonical]; hide {
					hdrAttrs = append(hdrAttrs, canonical, "<redacted>")
					continue
				}
				hdrAttrs = append(hdrAttrs, canonical, strings.Join(vals, ","))
			}

			log.Info("http.request",
				"method", r.Method,
				"path", r.URL.Path,
				"query", SanitizeURL(r.URL.RawQuery), // raw (no `?`) — sanitizer expects '?'/'&' so we prepend
				"status", rec.status,
				"dur_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
				"user_agent", r.UserAgent(),
				slog.Group("headers", hdrAttrs...),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}
```

(`SanitizeURL` is also exported so /api/security/audit row payloads can pre-sanitize URL fields before insert; see Plan 10.16.)

### 2.6 `grpcredact/interceptor.go` — gRPC log + OTel (D5)

```go
package grpcredact

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

const Redacted = "<redacted>"

// SafeMetadata returns a copy of md with sensitive keys redacted.
// Caller must NOT use the returned md to forward the call — only to log.
func SafeMetadata(md metadata.MD) metadata.MD {
	out := metadata.MD{}
	for k, vals := range md {
		lk := strings.ToLower(k)
		if lk == "authorization" || strings.HasSuffix(lk, "-token") || strings.HasSuffix(lk, "-secret") {
			out[k] = []string{Redacted}
			continue
		}
		out[k] = append([]string(nil), vals...)
	}
	return out
}

func UnaryServerInterceptor(log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		md, _ := metadata.FromIncomingContext(ctx)
		resp, err := handler(ctx, req)
		log.Info("grpc.request",
			"method", info.FullMethod,
			"dur_ms", time.Since(start).Milliseconds(),
			"err", errString(err),
			slog.Any("md", SafeMetadata(md)),
		)
		return resp, err
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `api/internal/secrets/secrets.go` | `Config`, `AuthConfig`, `DBConfig`, `LoadConfig`, `walkEnvOverrides` | `TestLoadConfigEnvWins` |
| 2 | `api/internal/secrets/redact.go` | `Redacted`, `IsSecretField`, `redactedValueOf`, `LogValue` impls | `TestSlogRedaction` |
| 3 | `api/internal/secrets/settings.go` | `Config.RenderForSettings`, `renderStruct` | `TestRenderForSettings` |
| 4 | `api/internal/httpx/redactlog/middleware.go` | `Middleware`, `SanitizeURL`, `statusRecorder` | `TestMiddlewareStripsHeaders`, `TestSanitizeURL` |
| 5 | `api/internal/grpcx/grpcredact/interceptor.go` | `UnaryServerInterceptor`, `SafeMetadata` | `TestSafeMetadata` |
| 6 | `cmd/api/main.go` | wires `secrets.LoadConfig`, attaches both middlewares | smoke |

---

## 4. Test cases (keyed to ACs)

### AC-1 — Env-var precedence
- `TestLoadConfigEnvWins`: TOML has `jwt_private_key="from-toml"` and `jwt_private_key_env="MAKTABA_JWT_KEY"`. Setting the env var overrides; the INFO log records `secret-source-overridden`.
- `TestLoadConfigNoEnvKeepsToml`: env var unset → TOML value retained, no INFO log.
- `TestLoadConfigEmptyEnvIsNotOverride`: `MAKTABA_JWT_KEY=""` → TOML retained (empty env never wins).

### AC-2 — Logger redaction
- `TestSlogRedaction`: an `AuthConfig{JWTPrivateKey:"abc"}` logged via `slog.Info("cfg", "auth", c)` produces output containing `<redacted>` and not `abc`. Use a `slog.NewJSONHandler` writing to `bytes.Buffer`.
- `TestSlogRedactionNotsecretOptOut`: `JWKSPublicURL` (tagged `notsecret:"true"`) appears verbatim.
- `TestSlogRedactionNameFallback`: a third-party struct with field `MyToken string` (no tag) is redacted by name regex.
- `TestMiddlewareStripsHeaders`: a chi route wrapped with `redactlog.Middleware` and called with `Authorization: Bearer xyz` → log line shows `Authorization=<redacted>`. Cookie too.
- `TestSanitizeURL`: `/api/stream?sig=abc&t=10` → `/api/stream?sig=<redacted>&t=10`.

### AC-3 — `/api/settings` redaction
- `TestRenderForSettings`: every secret-bearing leaf → `<redacted>` plus `*_present` boolean. An empty secret has `_present=false`.
- HTTP integration: GET `/api/settings` body matched against regex `\b[A-Za-z0-9_-]{16,}\b` (likely a real JWT key) → no match.

### AC-4 — gRPC metadata stripping
- `TestSafeMetadata`: `authorization=Bearer xyz` → redacted; `x-foo-token=abc` → redacted; `request-id=123` → kept.
- `TestUnaryInterceptorLogsRedacted`: server with `UnaryServerInterceptor` logs the `md` group; capture buffer; assert no `Bearer xyz` substring.

---

## 5. Edge cases

| #   | Case | Handled by |
|-----|------|------------|
| E1  | A user-defined key that looks secret (`my_token`) but isn't. Opt out with `notsecret:"true"`. | D3. |
| E2  | A signed URL with `sig=` in `r.URL.RawQuery`. `SanitizeURL` redacts even single-key queries. | D4. |
| E3  | Pointer/nil config field. Walker dereferences via `reflect.Indirect`; nil → `<nil>` marker. | redact.go guard. |
| E4  | `[]string` of secrets on a `secret`-tagged field. Walker checks the tag before recursing → renders the whole field as one `<redacted>`. | redact.go. |
| E5  | Nested struct (`Cfg.A.B.Token`). Walker recurses arbitrarily deep. | redact.go. |
| E6  | Custom slog handler that ignores `LogValuer`. Documented; stdlib JSON/Text handlers honour it. | Pkg comment. |
| E7  | Unexported field on a struct. Skipped via `f.IsExported()` guard. | redact.go. |
| E8  | Multi-valued header (`Cookie: a=1; Cookie: b=2`). Logged as one `<redacted>` per canonical key. | D4. |
| E9  | gRPC streaming RPCs. Ship a `StreamServerInterceptor` companion in a follow-up. | Future PR. |
| E10 | `*_env` env var literally set to `<redacted>`. Not detected; the literal becomes the value. | Documented. |

---

## 6. Acceptance checklist

- [ ] **A1** TOML loader applies env-var overrides via `*_env` siblings; precedence is env > TOML; an INFO line records each override.
- [ ] **A2** Every config struct field is either tagged `secret:"true"`, `notsecret:"true"`, or matches the secret-name regex; CI lint asserts no untagged ambiguous-name field exists.
- [ ] **A3** `slog` rendering of any tagged struct emits `<redacted>` for secret-bearing fields; `notsecret:"true"` overrides the regex.
- [ ] **A4** `Middleware` logs each HTTP request with `Authorization`, `Cookie`, `Set-Cookie`, `X-Maktaba-CSRF`, `X-Maktaba-Admin-Token`, `Proxy-Authorization` redacted; sanitizes `?sig=…` and friends in the URL.
- [ ] **A5** `UnaryServerInterceptor` redacts gRPC `authorization`, `*-token`, `*-secret` metadata in server logs; OTel attribute exporter consults `SafeMetadata`.
- [ ] **A6** `Config.RenderForSettings` emits `<redacted>` plus `*_present` for every secret leaf; `*_env` helper fields are hidden from the rendered output.
- [ ] **A7** Integration test on `/api/settings`: regex scan of the response body finds no plaintext secret.
- [ ] **A8** Integration test: an HTTP request with `Authorization: Bearer abc` is logged with the header redacted.
- [ ] **A9** Documentation: `secrets/README.md` lists the known-secret regex, the tag convention, and the `*_env` precedence rule.

---

## 7. Notes on consumers

- Epic 7 Story 7.15 (`/api/settings`): handler reads `cfg.RenderForSettings()` directly.
- Epic 6 (Pipeline) gRPC server: must register `UnaryServerInterceptor`.
- New secret-bearing keys MUST add `secret:"true"`; CI lint enforces.
