# Implementation Plan — Story 21.8 Telemetry Privacy

> Companion to [story-21-08-telemetry-privacy.md](story-21-08-telemetry-privacy.md).
> Default-off outbound telemetry; canonical redaction list; CI lint;
> runtime middleware redaction; web vitals opt-in only.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Master switch | `[telemetry].outbound_enabled = false` default. Toggles tracing exporters, error webhooks, Sentry. The full `[telemetry]` config block is documented in **architecture §11.6**; this plan implements that block. |
| Redaction list | `shared/redact/list.yaml`. Single source for lint and runtime middleware. |
| CI lint | AST scan of log/trace call sites for known field names from the list. |
| Runtime middleware | Wrap slog handler / OTel span processor / webhook builder; rewrite known keys to `***`. |
| Audit-emitter exemption | `forbidden_in_attrs` (e.g., `transcript_text`) blocks the field on **logs and spans only**. The audit-log emitter (`shared/audit/go/emit.go`, plan-21-06) is **fully blocked** from these fields too: even auditors must read `transcript_text` snippets via the original DB row, not via `audit_log.payload`. The exemption list is therefore **empty**: no emitter is allowed to write `transcript_text` (or other `forbidden_in_attrs`) into any observability surface. |
| Leak detector test | 1,000 representative log lines scanned for synthetic test secrets. |

## 1. Project layout

```
shared/redact/
├── list.yaml                    # canonical
├── go/
│   ├── redactor.go              # field-name rewriter
│   ├── path_masker.go
│   ├── leak_detector_test.go
│   ├── lint/
│   │   └── log_lint.go          # CI lint
│   └── slog_handler.go          # wraps any handler
└── py/
    ├── redactor.py
    └── leak_detector_test.py

tests/integration/network/
├── packet_capture_test.go       # TC1
└── allowlist.txt                # NTP + configured public origin

web/src/lib/
├── logger.ts                    # uses redactor
└── privacy_settings.ts          # shows opt-in for web vitals
```

## 2. Canonical redaction list

```yaml
# shared/redact/list.yaml
version: 1
field_names:
  password:           full
  password_hash:      hash16            # show first 16 hex chars
  api_key:            full
  apikey:             full
  authorization:      full
  bearer:             full
  token:              full
  jwt:                full
  refresh_token:      full
  secret:             full
  ssn:                full
  credit_card:        full
  cookie:             full
  set_cookie:         full
substring_patterns:
  - regex: "(?i)password\\s*=\\s*\\S+"
    replace: "password=***"
  - regex: "Bearer\\s+[A-Za-z0-9_.\\-=]+"
    replace: "Bearer ***"
path_masking:
  enabled: true
  # Roots whose absolute paths must be masked in log/span/webhook output.
  # All three roots are read at startup from the same config sections
  # that other plans use ([cache], [log], and the media root env var)
  # so masking stays in sync as deployments are reconfigured.
  roots:
    - name:    media
      env:     MAKTABA_MEDIA_ROOT
      replace: "<media>"                 # → <media>/<lib>/<rel>
    - name:    cache
      config:  cache.root                # from [cache] block in app config
      env:     MAKTABA_CACHE_ROOT        # env override
      replace: "<cache>"                 # → <cache>/<bucket>/<key>
    - name:    log
      config:  log.root                  # from [log] block in app config
      env:     MAKTABA_LOG_ROOT          # env override
      replace: "<log>"                   # → <log>/<file>
forbidden_in_attrs:
  - request_body
  - response_body
  - file_contents
  - transcript_text
```

## 3. Slog handler wrapper

```go
// shared/redact/go/slog_handler.go
type Redacted struct{ inner slog.Handler; rules Rules }

func (h *Redacted) Handle(ctx context.Context, r slog.Record) error {
    nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
    r.Attrs(func(a slog.Attr) bool {
        nr.AddAttrs(h.redact(a))
        return true
    })
    nr.Message = h.redactStringPatterns(r.Message)
    return h.inner.Handle(ctx, nr)
}

func (h *Redacted) redact(a slog.Attr) slog.Attr {
    if rule, ok := h.rules.FieldNames[strings.ToLower(a.Key)]; ok {
        return slog.String(a.Key, applyRule(rule, a.Value))
    }
    if a.Value.Kind() == slog.KindString {
        return slog.String(a.Key, h.redactStringPatterns(a.Value.String()))
    }
    return a
}
```

The application's logger is wrapped at init. `*slog.Logger` does **not**
expose a `WithHandler` method — wrap by constructing a new `slog.Logger`
around the redacting handler:

```go
log.Init("api", env)
inner := log.Default.Handler()
wrapped := slog.New(redact.New(inner, rules)).With("service", "api")
log.Default = wrapped
slog.SetDefault(wrapped)
```

`redact.New(inner, rules)` returns the `*Redacted` handler shown above
(it implements `slog.Handler`). `slog.New(...)` produces the new
`*slog.Logger`. The pre-bound base attributes (e.g., `service`) are
re-applied because the wrapping operation creates a fresh logger.

## 4. Lint over log call sites

```go
// shared/redact/go/lint/log_lint.go
//go:build redactlint

func main() {
    rules := loadRules("shared/redact/list.yaml")
    fset := token.NewFileSet()
    var fail bool
    pkgs, _ := packages.Load(...)
    for _, p := range pkgs {
        for _, f := range p.Syntax {
            ast.Inspect(f, func(n ast.Node) bool {
                call, ok := n.(*ast.CallExpr); if !ok { return true }
                if !isLogOrSpanCall(call) { return true }
                for i := 0; i+1 < len(call.Args); i += 2 {
                    if k, ok := call.Args[i].(*ast.BasicLit); ok && k.Kind == token.STRING {
                        kn := strings.Trim(k.Value, `"`)
                        if _, banned := rules.Forbidden[kn]; banned {
                            fmt.Fprintf(os.Stderr, "FAIL: %s — banned attr %q\n", fset.Position(call.Pos()), kn)
                            fail = true
                        }
                    }
                }
                if userDataConcat(call) {
                    fmt.Fprintf(os.Stderr, "FAIL: %s — user data in msg\n", fset.Position(call.Pos()))
                    fail = true
                }
                return true
            })
        }
    }
    if fail { os.Exit(1) }
}
```

`isLogOrSpanCall` covers `slog.*`, `log.From`, `span.SetAttributes`, `span.AddEvent`.

## 5. Leak detector test

```go
// shared/redact/go/leak_detector_test.go
//go:build leakdetect

const testSecret = "TEST_SENTINEL_SECRET_DO_NOT_LOG_8d4f2c1e"
const testToken  = "Bearer DOOMED_TOKEN_XYZ"

func TestNoSecretInLogs(t *testing.T) {
    out := captureStdout(t, func() {
        rig := startServiceRig(t)
        defer rig.Close()
        // Drive 1,000 representative requests using fixtures.
        rig.DriveBaselineWorkload()
        // Emit one deliberate slog with a sensitive value to confirm redaction.
        log.From(rig.Ctx).Info("post-login", "password", testSecret)
    })
    if bytes.Contains(out, []byte(testSecret)) {
        t.Fatalf("leak: testSecret appeared in logs")
    }
    if bytes.Contains(out, []byte("DOOMED_TOKEN_XYZ")) {
        t.Fatalf("leak: bearer leaked in logs")
    }
}
```

## 6. Outbound enable gate

```go
// shared/redact/go/outbound.go
type OutboundCfg struct{ Enabled bool }

func (c OutboundCfg) AllowOTLP(endpoint string) string {
    if !c.Enabled { return "" }                        // tracing init treats empty as disabled
    return endpoint
}
func (c OutboundCfg) AllowWebhook(url string) string  { if !c.Enabled { return "" }; return url }
func (c OutboundCfg) AllowSentry(dsn string) string   { if !c.Enabled { return "" }; return dsn }
```

All outbound modules read from `OutboundCfg`; flipping the master switch silently disables every external surface.

## 7. Path masking

```go
// shared/redact/go/path_masker.go
type Root struct {
    Name    string   // "media", "cache", "log"
    Replace string   // "<media>", "<cache>", "<log>"
    Path    string   // absolute path resolved at startup from env or config
}

// Roots is loaded once at process init from list.yaml + env + the
// [cache] and [log] blocks in app config. Order matters only when one
// root is a prefix of another; the longest match wins.
var Roots []Root

func Mask(p string) string {
    if !filepath.IsAbs(p) { return p }
    var match Root
    for _, r := range Roots {
        if r.Path != "" && strings.HasPrefix(p, r.Path) && len(r.Path) > len(match.Path) {
            match = r
        }
    }
    if match.Path == "" { return p }
    rel, _ := filepath.Rel(match.Path, p)
    parts := strings.SplitN(rel, string(filepath.Separator), 2)
    if match.Name == "media" && len(parts) == 2 {
        // media keeps <media>/<lib>/<rel> shape
        return fmt.Sprintf("%s/%s/%s", match.Replace, parts[0], parts[1])
    }
    if len(parts) == 2 {
        return fmt.Sprintf("%s/%s/%s", match.Replace, parts[0], parts[1])
    }
    return fmt.Sprintf("%s/%s", match.Replace, parts[0])
}
```

Stack trace post-processor invokes `Mask` over each frame's filename
before sending to error webhook. `Mask` covers media, cache, and log
roots so paths like `/var/maktaba/cache/segments/abcdef…` and
`/var/log/maktaba/api.log` do not leak alongside `<media>/…`.

## 8. Web vitals opt-in surface

```ts
// web/src/lib/privacy_settings.ts
export function PrivacyToggle() {
    const [enabled, setEnabled] = useLocalStorage('telemetry.web_vitals', false);
    return (
        <Toggle label="Send anonymous web performance metrics"
                checked={enabled}
                onChange={setEnabled}
                description="Off by default. When on, only LCP/FID/CLS are sent, no URLs or content." />
    );
}
```

When enabled, the `web-vitals` library's beacon is wired to `POST /api/telemetry/web-vitals` (Story 21.2 §7).

## 9. Test cases

### TC1 — Default-off packet capture
```go
//go:build network
func TestNoUnexpectedOutbound(t *testing.T) {
    rig := startCompose(t)
    defer rig.Close()
    cap := startTcpdump(t, rig.NetIface, 60*time.Second)
    rig.DriveBaselineWorkload()
    cap.Stop()
    for _, dst := range cap.UniqueDestinations() {
        if !allowlist.Contains(dst) {
            t.Errorf("unexpected outbound to %s", dst)
        }
    }
}
```

`allowlist.txt` contains NTP servers and the configured public origin.

### TC2 — Lint catches concat
A test fixture file `bad_log.go` with `slog.Info("password=" + p)` runs `make lint:redact`; assert exit non-zero and message names file:line.

### TC3 — Runtime redaction
At runtime, `slog.Info("debug", "password", "hunter2")` produces a JSON line where `password=***`. Verified by parsing captured stdout.

### EC1 — Settings echo
`GET /api/settings` returns metadata only (e.g., `{ "openai_api_key_set": true }`). Never returns the value. Test asserts response body contains no value matching the configured secret.

### EC2 — Stack with media/cache/log path
Trigger errors in code that handle each root:
- `/srv/media/maktaba/lib1/movie.mp4` → `<media>/lib1/movie.mp4`
- `/var/maktaba/cache/segments/abcdef.ts` → `<cache>/segments/abcdef.ts`
- `/var/log/maktaba/api.log` → `<log>/api.log`

Webhook payload stack frames show only the masked forms; absolute roots
are absent.

### EC3 — Browser console verbose
Production build: console at `info` minimum. Dev build with `VITE_LOG_LEVEL=debug`: shows debug. Asserted by snapshot of network responses to `/index.html` checking for the `VITE_LOG_LEVEL` baked into bundle.

## 10. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 secrets in settings | story | Settings endpoints return `*_set` booleans, never values. |
| EC2 paths in stacks | story | `Mask()` post-processes. |
| EC3 verbose dev logs | story | Build-time `VITE_LOG_LEVEL`. |
| Outbound after flip | impl | Master switch consulted at every emission boundary. |
| List drift | impl | Single `list.yaml`; lint and runtime read same. |

## 11. Configuration

The full `[telemetry]` block lives in **architecture §11.6**; this plan
populates the entries below. Plans 21-02 (`web_vitals_enabled`),
21-03 (`otlp_endpoint`, `sampling`), and 21-05 (`sentry_dsn_env`,
`webhook`) write into the same block; the master `outbound_enabled`
gate here governs all of them.

```yaml
telemetry:
  outbound_enabled: false
  redaction_list: shared/redact/list.yaml
  web_vitals_enabled: false
```

## 12. Dependencies

- Story 21.1 (logger; redactor wraps handler).
- Story 21.2 (web vitals endpoint).
- Story 21.3 (tracing exporter consults `outbound_enabled`).
- Story 21.5 (webhook builder runs through redactor).
- Story 21.6 (audit log not redacted at write — auditors need raw values; reads are scoped to admin).
