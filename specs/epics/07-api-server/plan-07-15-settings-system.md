# Implementation Plan — Story 7.15 Settings & System Endpoints

> Companion to [story-07-15-settings-system.md](story-07-15-settings-system.md).
> Owns the `app_settings` table and the redacted-read / runtime-write contract.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/settings`, `PATCH /api/settings`, `GET /api/settings/stt-backends`, `POST /api/settings/stt-test`. |
| Storage | New `app_settings(key, value, updated_at, updated_by)` table; trigger fires `NOTIFY settings_changed, '<key>'` on UPDATE/INSERT. |
| Redaction | Centralised key allowlist: any key whose path contains `api_key`, `token`, `password`, `secret` is redacted. |
| Runtime knobs | Whitelist of writable keys (`runtime` registry); writes outside the list → 403. |
| Out of scope | The user-management surface (Epic 10), the actual STT backends (Pipeline). |

## 1. Architecture diagram

```
   GET /api/settings
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ Merge: file (TOML) ←  env (overrides) ← app_settings (db)   │
   │ Walk merged map, replacing secrets with "<redacted>" + a    │
   │ sibling "*_present: true" key. Return.                      │
   └─────────────────────────────────────────────────────────────┘

   PATCH /api/settings { search.fts_weight: 0.7 }
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ For each key in body:                                       │
   │   if key not in runtime registry → 403 setting-not-runtime  │
   │   else validate via the registry's validator                │
   │ If any validator failed → 422 with all errors               │
   │ Tx:                                                         │
   │   UPSERT app_settings(key, value, updated_at, updated_by)   │
   │ Trigger fires NOTIFY 'settings_changed', '<key>'            │
   │ Re-read merged config and return                            │
   └─────────────────────────────────────────────────────────────┘

   In-process settings refresher (every replica):
   ┌─────────────────────────────────────────────────────────────┐
   │ go func() {                                                  │
   │   sub := pgListen("settings_changed")                        │
   │   tick := time.NewTicker(5 * time.Second)                    │
   │   for { select {                                            │
   │     case <-sub.C: refresh(); tick.Reset(5s)                 │
   │     case <-tick.C: refresh()                                │
   │   } }                                                       │
   │ }()                                                         │
   └─────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/settings/handler.go` | All four routes. |
| `api/internal/settings/registry.go` | Runtime-knob registry: writable keys + validators. |
| `api/internal/settings/redact.go` | Redaction walker. |
| `api/internal/settings/store.go` | DB-backed merged-config loader. |
| `api/internal/settings/listener.go` | NOTIFY + 5 s poll backstop. |
| `api/internal/settings/handler_test.go` | Integration. |
| `api/internal/settings/redact_test.go` | Unit. |
| `api/internal/settings/registry_test.go` | Unit. |
| `shared/db/queries/app_settings.sql` | sqlc inputs. |
| `shared/db/migrations/0019_app_settings.sql` | Schema + trigger. |

## 3. SQL — schema + trigger

`shared/db/migrations/0019_app_settings.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE TABLE app_settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

CREATE OR REPLACE FUNCTION app_settings_notify() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('settings_changed', NEW.key);
    RETURN NEW;
END;
$$;

CREATE TRIGGER app_settings_notify_trg
    AFTER INSERT OR UPDATE ON app_settings
    FOR EACH ROW EXECUTE FUNCTION app_settings_notify();

-- The profiles_changed channel is created here as a convention; the
-- settings table doesn't fire it directly — Streaming's profile registry
-- writes to its own table and emits the notification.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS app_settings_notify_trg ON app_settings;
DROP FUNCTION IF EXISTS app_settings_notify();
DROP TABLE IF EXISTS app_settings;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/settings/types.go
package settings

import (
    "encoding/json"
    "time"
)

type Patch map[string]json.RawMessage

type STTBackend struct {
    Name             string   `json:"name"`
    Available        bool     `json:"available"`
    Version          string   `json:"version"`
    Models           []string `json:"models"`
    HWAccel          string   `json:"hwaccel"`
    CostPerMinuteUSD *float64 `json:"cost_per_minute_usd,omitempty"`
}

type STTTestRequest struct {
    Backend string         `json:"backend" validate:"required"`
    Config  map[string]any `json:"config"`
}

type STTTestResponse struct {
    OK         bool          `json:"ok"`
    LatencyMS  int64         `json:"latency_ms"`
    SampleText string        `json:"sample_text"`
    Error      string        `json:"error,omitempty"`
    StartedAt  time.Time     `json:"started_at"`
}
```

## 5. Registry (runtime-knob allowlist)

```go
// api/internal/settings/registry.go
package settings

import "encoding/json"

type Validator func(v json.RawMessage) error

// runtime is the canonical map of "things the UI may PATCH at runtime."
// Adding a knob here is the single permission gate — no other check.
var runtime = map[string]Validator{
    "search.fts_weight":             floatRange(0, 1),
    "search.semantic_weight":        floatRange(0, 1),
    "search.k":                      intRange(1, 200),
    "session.url_ttl_sec":           intRange(60, 86400),
    "subtitle.url_ttl_sec":          intRange(60, 86400),
    "rate_limit.default_per_min":    intRange(1, 60000),
    "rate_limit.ip_per_min":         intRange(1, 60000),
    "shutdown_grace_sec":            intRange(1, 600),
    "stt.backend":                   stringEnum("whisper-mlx","faster-whisper","openai"),
    "stt.model":                     stringMaxLen(128),
    // ... grow as new knobs land.
}

func IsRuntime(key string) bool { _, ok := runtime[key]; return ok }
func Validate(key string, v json.RawMessage) error {
    fn, ok := runtime[key]
    if !ok { return ErrNotRuntime }
    return fn(v)
}
```

## 6. Redaction

```go
// api/internal/settings/redact.go
package settings

import "regexp"

var secretRE = regexp.MustCompile(`(?i)(api_key|token|password|secret)$`)

// Redact walks a nested map and replaces any leaf at a key matching the
// regex with "<redacted>", adding a sibling "<key>_present" boolean.
func Redact(in map[string]any) map[string]any {
    out := map[string]any{}
    for k, v := range in {
        switch t := v.(type) {
        case map[string]any:
            out[k] = Redact(t)
        default:
            if secretRE.MatchString(k) {
                out[k] = "<redacted>"
                out[k+"_present"] = !isZero(v)
            } else {
                out[k] = v
            }
        }
    }
    return out
}

func isZero(v any) bool {
    switch t := v.(type) {
    case nil: return true
    case string: return t == ""
    default: return false
    }
}
```

## 7. Handler scaffolding

```go
// api/internal/settings/handler.go
package settings

import (
    "encoding/json"
    "errors"
    "net/http"

    "maktaba/api/internal/httperror"
)

func (h *handler) get(w http.ResponseWriter, r *http.Request) {
    merged := h.store.Snapshot()
    json.NewEncoder(w).Encode(Redact(merged))
}

func (h *handler) patch(w http.ResponseWriter, r *http.Request) {
    var p Patch
    if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }

    var fieldErrs []httperror.FieldError
    for k, v := range p {
        if !IsRuntime(k) {
            httperror.Write(w, r, &httperror.Error{
                Type: TypeNotRuntime, Title: "setting-not-runtime",
                Status: 403, Detail: "key '"+k+"' is not runtime-editable",
            })
            return
        }
        if err := Validate(k, v); err != nil {
            fieldErrs = append(fieldErrs, httperror.FieldError{Field: k, Message: err.Error()})
        }
    }
    if len(fieldErrs) > 0 {
        httperror.Write(w, r, httperror.Unprocessable(fieldErrs)); return
    }

    user := userFromCtx(r.Context())
    for k, v := range p {
        if err := h.db.UpsertAppSetting(r.Context(), UpsertAppSettingParams{
            Key: k, Value: v, UpdatedBy: user.ID,
        }); err != nil {
            httperror.Write(w, r, httperror.Internal("upsert")); return
        }
    }
    h.store.Refresh(r.Context())
    json.NewEncoder(w).Encode(Redact(h.store.Snapshot()))
}

func (h *handler) sttBackends(w http.ResponseWriter, r *http.Request) {
    out, err := h.svc.sttBackends(r.Context())
    if err != nil { httperror.Write(w, r, httperror.Unavailable(5)); return }
    json.NewEncoder(w).Encode(out)
}

func (h *handler) sttTest(w http.ResponseWriter, r *http.Request) {
    var in STTTestRequest
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    out, err := h.svc.sttTest(r.Context(), in)
    if err != nil { httperror.Write(w, r, httperror.Internal("dry run")); return }
    json.NewEncoder(w).Encode(out)
}
```

## 8. Listener + poll backstop

```go
// api/internal/settings/listener.go
package settings

import (
    "context"
    "time"
)

func (s *Store) StartListener(ctx context.Context) {
    go func() {
        ev := s.notify.Subscribe(ctx, "settings_changed")
        tick := time.NewTicker(5 * time.Second)
        defer tick.Stop()
        for {
            select {
            case <-ctx.Done(): return
            case <-ev:
                s.Refresh(ctx)
                tick.Reset(5 * time.Second)
            case <-tick.C:
                s.Refresh(ctx) // backstop — covers dropped NOTIFYs
            }
        }
    }()
}
```

## 9. STT backends + dry run

`sttBackends` calls Pipeline gRPC `ListBackends` (Story 7.18) and caches
60 s. `sttTest` calls Pipeline gRPC `RunSyntheticTranscribe(backend, config)`.
Both are stubbed in tests via the gRPC fake.

## 10. SQL — sqlc inputs

`shared/db/queries/app_settings.sql`:

```sql
-- name: UpsertAppSetting :exec
INSERT INTO app_settings (key, value, updated_at, updated_by)
VALUES ($1, $2, now(), $3)
ON CONFLICT (key) DO UPDATE
   SET value      = EXCLUDED.value,
       updated_at = EXCLUDED.updated_at,
       updated_by = EXCLUDED.updated_by;

-- name: ListAppSettings :many
SELECT key, value FROM app_settings;
```

## 11. Test plan

### 11.1 Unit (`redact_test.go`)

| Test | What it pins |
|---|---|
| `TestRedactsApiKey` | `{anthropic_api_key:"x"}` → `<redacted>`; sibling `_present: true`. |
| `TestRedactsNested` | `{stt:{openai:{api_key:"y"}}}` → nested redacted. |
| `TestPreservesNonSecrets` | `{search:{fts_weight:0.7}}` → unchanged. |
| `TestPresentFalseWhenEmpty` | `{anthropic_api_key:""}` → `<redacted>` + `_present: false`. |

### 11.2 Unit (`registry_test.go`)

| Test | What it pins |
|---|---|
| `TestRuntimeRegistryShape` | Every value is a non-nil Validator. |
| `TestValidateOutOfRange` | `search.fts_weight = -1` → error. |
| `TestNotRuntimeRejected` | `database.url` → `ErrNotRuntime`. |

### 11.3 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestGetRedacts` | Seed file with `api_key="abc"` → response has `<redacted>` + `api_key_present: true`. |
| `TestPatchAcceptedRuntime` | PATCH `{search.fts_weight: 0.7}` → 200, value persisted, 5 s poll picks it up on a second replica. |
| `TestPatchNonRuntimeForbidden` | PATCH `{database.url: "..."}` → 403 `setting-not-runtime`. |
| `TestPatchInvalidValueRejected` | PATCH `{search.fts_weight: -1}` → 422 listing the offending key. |
| `TestNotifyReceivedByOtherReplica` | Two replicas connected; PATCH on A → B's `Snapshot()` reflects the new value within 1 s. |
| `TestPollBackstopReconciles` | Drop the LISTEN connection on B; UPDATE happens on A → B converges via the 5 s poll. |
| `TestNeverLeaksSecret` | Regex against the response body for `api_key|token|password|secret` → only `<redacted>` matches the value side. |
| `TestSTTBackendsCached` | 100 calls in 1 s → exactly 1 gRPC call. |
| `TestSTTBackendsRefreshOnNotify` | Publish `profiles_changed` → next call refetches. |
| `TestSTTDryRun` | Synthetic-speech transcription returns `ok=true, latency_ms>0, sample_text!=""`. |

## 12. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Patch a runtime key with a value that bricks search (e.g. `fts_weight=-1`) | Validator rejects with 422; runtime never sees it. | `TestPatchInvalidValueRejected` |
| Two replicas drop NOTIFY at the same time | The 5 s poll backstop reconciles within 5 s. | `TestPollBackstopReconciles` |
| Secret key configured via env var | The merge layer reads env *after* file; redaction still hides it on read. | `TestGetRedacts` (variant) |
| Patch deletes a key (sets to null) | `value=null` is a legitimate JSONB value; the merge resolver treats null as "fall back to env/file." | Documented |
| Patch races a concurrent PATCH on the same key | Last writer wins; `updated_at` records who. No optimistic lock. | Documented |
| Removed runtime key (deleted from registry) | PATCH → 403. The DB row may still hold a stale value; `Snapshot()` includes it; the system tolerates orphaned values. | Documented |
| `setting_changed` payload truncated by Postgres (8000-byte limit) | We only send the key, not the value, so payload is tiny. | Trigger payload is `<key>` |

## 13. Acceptance checklist

- [ ] `app_settings` table + `settings_changed` trigger land in `0019`.
- [ ] `GET /settings` redacts secrets and adds `*_present` flags.
- [ ] `PATCH /settings` enforces the runtime registry (403 outside).
- [ ] Validators run before the upsert.
- [ ] NOTIFY fires; 5 s poll backstop reconciles.
- [ ] `GET /settings/stt-backends` cached 60 s.
- [ ] `POST /settings/stt-test` runs the synthetic-speech round trip.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.15.
