# Implementation Plan — Story 23.4 Secrets management

> Companion to [story-23-04-secrets-management.md](story-23-04-secrets-management.md).
> Story states *what* and *why*; this plan states *how*.
> Canonical secret list and per-service ownership defined in
> [architecture.md §11.5](../../architecture.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Secret loader | `api/internal/secrets/loader.go`, mirrored in `streaming/internal/secrets/loader.go` and `pipeline/src/maktaba_pipeline/secrets.py`. Each service loads only the secrets it owns. |
| Source precedence | `<KEY>_FILE` env > `<KEY>` env > config file value. Docker secrets always go through the `_FILE` path (per Story 22.3). |
| Settings API | `GET /api/settings` returns metadata only (`{ key, configured, source }`), never values. |
| Redaction middleware | `shared/log/redact.go` (Go) and `pipeline/src/maktaba_pipeline/log/redact.py`. Applied to slog/logging output. |
| Static analysis | `tools/secret-allowlist-lint.go` asserts the streaming binary contains no string referencing JWT private key or STT-backend env names. |
| Out of scope | Specific key formats (Story 23.1 owns JWT keypair); rotation flows (23.1 keys, 23.6 rate-limit, 23.7 deps); rate-limited admin endpoint that triggers reload (referenced in EC3 only). |

## 1. Architecture diagram

```
                 ┌────────────────────────┐
       env ────►│ secrets.Loader         │
       file     │   precedence:          │
       config   │   _FILE > _<NAME> > cfg│
                 └──────────┬─────────────┘
                            │
                ┌───────────┴────────────┐
                ▼                        ▼
   service-specific surfaces      log redaction middleware
   (api: jwt priv+pub, db_url,    (slog hook scrubs values
   admin token, openai key...     matching known names + entropy)
   streaming: jwt PUB only)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/secrets/loader.go` | The precedence loader. |
| `api/internal/secrets/registry.go` | Whitelist of secrets the API may load + each one's owner-service tag. |
| `streaming/internal/secrets/loader.go` | Same shape, narrower whitelist. |
| `pipeline/src/maktaba_pipeline/secrets.py` | Python equivalent. |
| `shared/log/redact.go` | slog `Handler` wrapping that redacts. |
| `pipeline/src/maktaba_pipeline/log/redact.py` | Python `logging.Filter` that redacts. |
| `api/internal/http/settings.go` | `GET /api/settings`, `PUT /api/settings/<key>`. |
| `tools/secret-allowlist-lint.go` | Static check on the streaming binary. |
| `tools/secrets-doctor.sh` | Boot-time helper: detects secrets in plaintext config files. |
| Tests — `_test.go` per file plus `tests/secrets/contains-no-secrets.sh` (string scan on the binary). |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/config/config.go` | Strips secret values from the loaded config; replaces with sentinels. |
| `api/cmd/api/main.go` | Boot calls `secrets.Loader` rather than reading env directly. |
| `streaming/cmd/streaming/main.go` | Same. |
| `pipeline/src/maktaba_pipeline/cli.py` | Same. |
| `api/internal/http/router.go` | Mounts settings handlers behind admin-required middleware (Story 23.2). |

### 2.3 The secret registry

`api/internal/secrets/registry.go`:

```go
package secrets

type Owner string

const (
    OwnerAPI       Owner = "api"
    OwnerStreaming Owner = "streaming"
    OwnerPipeline  Owner = "pipeline"
)

type Spec struct {
    Name      string   // env name
    Owner     Owner
    Required  bool
    Pattern   *regexp.Regexp // optional shape check
}

var AllSpecs = []Spec{
    {Name: "MAKTABA_DATABASE_URL",       Owner: OwnerAPI,       Required: true,
     Pattern: regexp.MustCompile(`^(postgres|sqlite)://`)},
    {Name: "MAKTABA_JWT_PRIVATE_KEY_PEM", Owner: OwnerAPI,       Required: true,
     Pattern: regexp.MustCompile(`-----BEGIN .*PRIVATE KEY-----`)},
    {Name: "MAKTABA_JWT_PUBLIC_KEY_PEM",  Owner: OwnerStreaming, Required: true,
     Pattern: regexp.MustCompile(`-----BEGIN PUBLIC KEY-----`)},
    {Name: "MAKTABA_ADMIN_TOKEN",         Owner: OwnerAPI,       Required: false,
     Pattern: regexp.MustCompile(`^[A-Za-z0-9._-]{32,}$`)},
    {Name: "OPENAI_API_KEY",              Owner: OwnerPipeline,  Required: false,
     Pattern: regexp.MustCompile(`^sk-`)},
    // ...others
}

// SpecsFor returns only the specs whose Owner matches.
func SpecsFor(o Owner) []Spec {
    out := make([]Spec, 0, 8)
    for _, s := range AllSpecs { if s.Owner == o { out = append(out, s) } }
    return out
}
```

### 2.4 Loader

`api/internal/secrets/loader.go`:

```go
type Source string

const (
    SourceEnvFile Source = "env-file"
    SourceEnv     Source = "env"
    SourceConfig  Source = "config"
    SourceUnset   Source = "unset"
)

type Loaded struct {
    Name      string
    Source    Source
    Configured bool
    Value     string  // never JSON-marshalled; the Marshaler omits this field
}

func (l Loaded) MarshalJSON() ([]byte, error) {
    return json.Marshal(struct {
        Name       string `json:"name"`
        Configured bool   `json:"configured"`
        Source     Source `json:"source"`
    }{l.Name, l.Configured, l.Source})
}

func Load(specs []Spec, cfg map[string]string) ([]Loaded, error) {
    out := make([]Loaded, 0, len(specs))
    for _, s := range specs {
        loaded, err := loadOne(s, cfg)
        if err != nil { return nil, err }
        if s.Required && !loaded.Configured {
            return nil, fmt.Errorf("required secret %s missing", s.Name)
        }
        out = append(out, loaded)
    }
    return out, nil
}

func loadOne(s Spec, cfg map[string]string) (Loaded, error) {
    if path := os.Getenv(s.Name + "_FILE"); path != "" {
        b, err := os.ReadFile(path)
        if err != nil { return Loaded{}, err }
        v := strings.TrimSpace(string(b))   // EC2: tolerate trailing newline
        if err := check(s, v); err != nil { return Loaded{}, err }
        return Loaded{s.Name, SourceEnvFile, true, v}, nil
    }
    if v := os.Getenv(s.Name); v != "" {
        if err := check(s, v); err != nil { return Loaded{}, err }
        return Loaded{s.Name, SourceEnv, true, v}, nil
    }
    if v := cfg[s.Name]; v != "" {
        if err := check(s, v); err != nil { return Loaded{}, err }
        return Loaded{s.Name, SourceConfig, true, v}, nil
    }
    return Loaded{s.Name, SourceUnset, false, ""}, nil
}

func check(s Spec, v string) error {
    if s.Pattern != nil && !s.Pattern.MatchString(v) {
        return fmt.Errorf("secret %s does not match expected shape", s.Name)
    }
    return nil
}
```

The `Loaded.Value` field is stripped from `MarshalJSON`; settings
endpoints accidentally serializing the struct still won't leak.

### 2.5 Streaming registry exclusion (AC2)

`streaming/internal/secrets/registry.go` lists *only* what streaming
owns:

```go
var Specs = []secrets.Spec{
    {Name: "MAKTABA_JWT_PUBLIC_KEY_PEM", Owner: OwnerStreaming, Required: true},
    {Name: "MAKTABA_DATABASE_URL_RO",    Owner: OwnerStreaming, Required: false},
    // No JWT_PRIVATE_KEY_PEM. No OPENAI_API_KEY.
}
```

The streaming binary calls `secrets.Load(streaming.Specs, ...)`. Even
if an operator sets `MAKTABA_JWT_PRIVATE_KEY_PEM` on the streaming
container, the loader does not read it and the env name is not
referenced anywhere in the streaming binary's text.

### 2.6 Settings handler (AC3)

`api/internal/http/settings.go`:

```go
// GET /api/settings — admin only.
func (h *SettingsHandler) list(w http.ResponseWriter, r *http.Request) {
    _ = authz.Authorize(r.Context(), authz.AdminLibrary, authz.SystemResource{})
    json.NewEncoder(w).Encode(h.loaded)   // []secrets.Loaded — values stripped
}

// PUT /api/settings/{key} — admin only; write-only.
// Payload: { "value": "..." }
//
// The handler writes the value to the configured secret store (env on
// dev, file on compose, secrets manager later) and reloads the in-memory
// copy. The response body is the metadata struct, never the value.
func (h *SettingsHandler) put(w http.ResponseWriter, r *http.Request) {
    _ = authz.Authorize(r.Context(), authz.AdminLibrary, authz.SystemResource{})
    key := chi.URLParam(r, "key")
    var body struct{ Value string `json:"value"` }
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        problem(w, 400, "invalid-json", "")
        return
    }
    spec, ok := registry.Find(key)
    if !ok || spec.Owner != OwnerAPI {
        problem(w, 404, "unknown-secret", "")
        return
    }
    if err := check(spec, body.Value); err != nil {
        problem(w, 422, "shape-violation", "")
        return
    }
    if err := h.store.Write(key, body.Value); err != nil {
        problem(w, 500, "write-failed", "")
        return
    }
    h.loaded = h.reload()
    json.NewEncoder(w).Encode(h.loaded[key])
}
```

### 2.7 Redaction middleware

`shared/log/redact.go`:

```go
package log

import (
    "context"
    "log/slog"
    "regexp"
    "strings"
)

var (
    secretSuffixes = []string{"_KEY", "_TOKEN", "_PASSWORD", "_SECRET", "_PEM"}
    // High-entropy regex (rough): 24+ chars of base64-ish or hex.
    entropyRE = regexp.MustCompile(`\b[A-Za-z0-9_+/-]{24,}\b`)
)

type RedactingHandler struct{ slog.Handler }

func (h RedactingHandler) Handle(ctx context.Context, r slog.Record) error {
    r2 := r.Clone()
    r2.Message = redact(r2.Message)
    r2.Attrs(func(a slog.Attr) bool {
        if shouldRedactKey(a.Key) {
            r2.AddAttrs(slog.String(a.Key, "***"))
            return true
        }
        if s, ok := a.Value.Any().(string); ok {
            r2.AddAttrs(slog.String(a.Key, redact(s)))
            return true
        }
        return true
    })
    return h.Handler.Handle(ctx, r2)
}

func shouldRedactKey(k string) bool {
    K := strings.ToUpper(k)
    for _, s := range secretSuffixes {
        if strings.HasSuffix(K, s) { return true }
    }
    return false
}

func redact(s string) string {
    return entropyRE.ReplaceAllString(s, "***")
}
```

The handler is wrapped around the JSON handler in `slog.SetDefault`.
For Python, a `logging.Filter` does the same:

```python
SECRET_RE = re.compile(r"\b[A-Za-z0-9_+\-/]{24,}\b")
SUFFIXES = ("_KEY", "_TOKEN", "_PASSWORD", "_SECRET", "_PEM")

class RedactingFilter(logging.Filter):
    def filter(self, record):
        if isinstance(record.msg, str):
            record.msg = SECRET_RE.sub("***", record.msg)
        if record.args:
            record.args = tuple(self._scrub(a) for a in record.args)
        return True

    def _scrub(self, v):
        if isinstance(v, str): return SECRET_RE.sub("***", v)
        if isinstance(v, dict):
            return {k: ("***" if any(k.upper().endswith(s) for s in SUFFIXES) else self._scrub(val))
                    for k, val in v.items()}
        return v
```

### 2.8 Static analysis on streaming binary

`tools/secret-allowlist-lint.go`:

```go
// Runs as a CI step after `make build`. Reads the streaming binary,
// looks for substrings of forbidden env names. Fails on any hit.

var forbidden = []string{
    "MAKTABA_JWT_PRIVATE_KEY_PEM",
    "OPENAI_API_KEY",
    "ANTHROPIC_API_KEY",
}

func main() {
    bin, _ := os.ReadFile("streaming/bin/maktaba-streaming")
    for _, name := range forbidden {
        if bytes.Contains(bin, []byte(name)) {
            fmt.Fprintf(os.Stderr, "forbidden env name %q present in streaming binary\n", name)
            os.Exit(1)
        }
    }
}
```

This pairs with the registry exclusion: the binary cannot reference
forbidden env names because the streaming code never imports the
package that declares them.

### 2.9 Multi-line PEM env handling (EC2)

The Loader's `_FILE` and `_<NAME>` paths both call `strings.Replace(v,
`\n`, "\n", -1)` after the trim, so values that include literal `\n`
sequences (common when an operator pastes a PEM into a `.env` file)
become real newlines. The regex `Pattern` for keys then matches.

### 2.10 SIGHUP reload (EC3)

Each service installs a `SIGHUP` handler that re-runs `secrets.Load`
and swaps in the new values atomically:

```go
ch := make(chan os.Signal, 1)
signal.Notify(ch, syscall.SIGHUP)
go func() {
    for range ch {
        if reloaded, err := secrets.Load(specs, cfg); err == nil {
            atomicSwap(&secretsRef, reloaded)
        } else {
            slog.Warn("secrets reload failed; keeping previous", "err", err)
        }
    }
}()
```

In-flight requests use the value resolved at request entry; new
requests pick up the new value. Documented in ops guide.

## 3. Test plan

### 3.1 Streaming binary contains no forbidden names (TC1)

| Test | What it pins |
|---|---|
| `TestStreamingNoForbiddenStrings` | `tools/secret-allowlist-lint` exits 0 against the freshly-built streaming binary; an attempted import of the API's secrets registry fails the build. |
| `TestStreamingRegistryHasOnlyOwnedKeys` | Compile-time test: `streaming.Specs` contains only `Owner == OwnerStreaming` entries. |

### 3.2 Settings round-trip (TC2)

| Test | What it pins |
|---|---|
| `TestSettingsListExcludesValues` | `GET /api/settings` returns `[ { name, configured, source } ]`; no `value` field; serialization across the wire never carries the value. |
| `TestSettingsPutWriteOnly` | `PUT /api/settings/OPENAI_API_KEY` with `{value:"sk-..."}` → 200; subsequent `GET` shows `configured: true`, `source: "config"`. The PUT response body excludes the value. |
| `TestSettingsPutShapeViolation` | `PUT` with invalid value (e.g., DB_URL not `postgres://...`) returns 422 with the spec's shape message. |
| `TestSettingsPutNonAdminRefused` | Non-admin caller → 403. |

### 3.3 Redaction (TC3)

| Test | What it pins |
|---|---|
| `TestRedactsKnownKeyName` | `slog.Info("loaded", "OPENAI_API_KEY", "sk-real")` writes `OPENAI_API_KEY=***`. |
| `TestRedactsHighEntropyValue` | `slog.Info("token", "got", "X9aBc1...32chars")` writes `got=***`. |
| `TestPythonRedactingFilter` | Mirror test for the pipeline's `RedactingFilter`. |
| `TestRedactInStackTrace` (EC1) | A panic that includes the secret in the message redacts before emission. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| Secret in stack trace (EC1) | The `recover()` in the api panic-handler runs the message through the redactor before logging. | `TestRedactInStackTrace` |
| Multi-line PEM env (EC2) | `_FILE` reads the file as-is (real newlines); `_<NAME>` env tolerates `\n`-escaped form. Both pass the Pattern check. | `TestPemMultilineEnv`, `TestPemMultilineFile` |
| Secret rotation in flight (EC3) | SIGHUP reload swaps atomically; in-flight requests use old value; new requests use new value. Documented. | `TestSighupReload` |
| Unknown env starting with `MAKTABA_` | The loader logs `unknown_maktaba_env_var=NAME` once on boot but continues — this is informational, not blocking, so adding new keys without updating the registry is non-fatal. | `TestUnknownMaktabaEnvWarn` |
| Both `_FILE` and `_<NAME>` set | `_FILE` wins (deterministic precedence); a warning logs that both are set. | `TestPrecedenceFileOverEnv` |
| Empty secret file | Treated as unset; `Required: true` causes startup refusal. | `TestEmptyFileTreatedAsUnset` |
| Secret in URL query string | Not allowed: any URL the API constructs from a secret URL goes through a helper that strips secrets from log lines before logging the URL. | `TestUrlLogScrubsSecrets` |
| Secret leaks via metric label | The Prometheus exporter (Epic 21) ignores any label whose name matches the redaction patterns. Tested by adding a counter labeled `OPENAI_API_KEY_VALUE` and asserting it's dropped. | `TestMetricLabelDropped` |
| Operator stores secret in `git`-tracked config | `tools/secrets-doctor.sh` runs at boot; if any value in `config.toml` matches a secret pattern, the service refuses to start unless `MAKTABA_ALLOW_INLINE_SECRETS=true`. | `TestSecretsDoctorRefusesInlineSecrets` |
| `cosign verify` keyring used by image-signing on the streaming binary | n/a — that key is held by maintainers, not loaded into the binary. | n/a |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `log/slog` | stdlib (1.21+) | Structured logging + handler. |
| `regexp` | stdlib | Shape + entropy checks. |
| `os/signal` | stdlib | SIGHUP reload. |

## 6. Acceptance checklist

**Loader**
- [ ] `_FILE` > `_<NAME>` > config precedence honored.
- [ ] Required secrets missing → service refuses to start.
- [ ] Pattern checks on all canonical secrets.

**Streaming binary**
- [ ] CI lint asserts no forbidden env-name strings present.
- [ ] Registry contains only `OwnerStreaming` entries.

**Settings**
- [ ] `GET /api/settings` returns metadata only.
- [ ] `PUT /api/settings/<key>` is write-only and admin-only.
- [ ] No secret value appears in any response body.

**Redaction**
- [ ] Known suffix keys (`_KEY`, `_TOKEN`, etc.) redacted.
- [ ] High-entropy values redacted.
- [ ] Both Go (slog) and Python (logging) covered.

**Reload**
- [ ] SIGHUP triggers atomic reload; failures keep previous value.
