# Plan 9.1 — Library config schema and validation — implementation

> Implementation plan for [story-09-01-library-config-schema.md](story-09-01-library-config-schema.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the validated `settings` shape feeds the
> watcher in [Plan 9.2](plan-09-02-filesystem-watcher.md), the sweep in
> [Plan 9.3](plan-09-03-periodic-sweep.md), the ignore-rules engine in
> [Plan 9.5](plan-09-05-ignore-rules.md), and every Pipeline stage that
> reads `library.settings` (transcribe model, embedder, diarize gate,
> chapter inference). The PATCH HTTP surface itself is owned by Epic 7
> Story 7.3; this plan owns the **validator**, the **deep-merge** with
> defaults, the **migration**, and the **LISTEN/NOTIFY** wiring.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Validator lives in the API service (Go), not the Pipeline.** PATCH calls `library.SettingsValidator.Validate` before write; pipeline reads stored JSONB and only does *defaults inheritance* at job-claim time. | Story AC-1 ("validated"); architecture §5 ("API owns config surface"); story-09-01 §"Test cases" mentions PATCH 422 — that's an API-layer concern. | Two validators (one in Go, one in Python) drift. The API is the only writer for `libraries.settings`, so validating there is sufficient and lets the Pipeline trust whatever it reads (it still defends with type assertions but does not re-validate). The Pipeline owns *resolution* (deep-merge with `pipeline.toml`) because that file lives next to the workers. |
| D2 | **Validator is table-driven**, not hand-coded per key. Every recognized key has one row in a `keySpec` slice with `name, type, validate(fn), default`. Unknown keys are stored verbatim and surface as a `warnings[]` array in the PATCH response (AC-1 forward-compat clause). | Story AC-1: "Unknown keys are stored but a warning is emitted to the API response on PATCH." Edge case: "future schema version (forward compat) — keys are preserved on read". | A switch statement per key would balloon as Stories 9.8–9.18 add more knobs (topic, content type, voiceprint thresholds). A table also makes the OpenAPI schema generation in Epic 7 a one-line transform. |
| D3 | **Storage: `libraries.settings` is `JSONB NOT NULL DEFAULT '{}'::jsonb`.** If the column already exists from Epic 1, the migration is a no-op `ALTER TABLE … IF NOT EXISTS`-style guard via a DO block; if not, it adds the column. | Story story-09-01 implies the column exists; architecture §5 confirms. | Idempotent migrations let us land this plan on top of any prior migration history without ordering concerns. `JSONB` (not `JSON`) so we get GIN-indexable + canonicalized storage; the trigger described in D6 is cheaper on `JSONB`. |
| D4 | **Defaults inheritance is a recursive deep-merge** with library overriding `[stt.<profile>]` overriding `[stt.default]` overriding hard-coded constants. Lists *replace*, dicts *merge*. Implemented in Python (`maktaba_pipeline.config.merge.deep_merge`) and consumed by every job claim. | Story AC-2: "missing keys are inherited from `[stt.default]` in `pipeline.toml` (§11.4), recursively." | Lists-replace (vs lists-extend) is the only semantics that lets a library *narrow* `supported_video_exts` rather than only widen it. Documenting this in the validator's help text avoids the most common surprise. |
| D5 | **PATCH semantics is RFC-7396 JSON Merge Patch**, not RFC-6902 JSON Patch. Clients send `{"stt": {"model": "large-v3"}}` and only `stt.model` changes. Setting a key to `null` deletes it (and lets defaults bubble back up). | Story AC-3 ("PATCH succeeds … `library.settings_changed` NOTIFY fires") + practicality. | Merge patch is what every UI form library produces by default and is unambiguous on JSONB. JSON Patch (with `op:"add"`) is more powerful but the API never needs that power for settings — only flat-ish updates land here. |
| D6 | **`library.settings_changed` NOTIFY fires from a row-level AFTER UPDATE trigger** on `libraries`, payload `{"library_id": "...", "version": 7}` (the trigger bumps a `settings_version BIGINT`). The Pipeline subscribes via `pgx`/`asyncpg` `LISTEN`. | Story AC-3 ("a `library.settings_changed` NOTIFY fires"). | Doing the NOTIFY in a trigger guarantees it fires for *any* writer, not just the API handler. The version counter lets a late subscriber detect that it missed an event (compares `settings_version` to its in-memory cache and reloads). |
| D7 | **Effective-config resolution is cached in the Pipeline per `(library_id, settings_version)`** with bounded LRU; invalidated by NOTIFY. The merged dict is what every stage reads — stages never touch raw `settings`. | Pipeline performance: every job claim would otherwise read + merge from disk + DB. | Caching at `(library_id, settings_version)` is correct under D6's invalidation contract: a settings change always bumps the version, so a stale cache hit is impossible. |
| D8 | **Existing videos are NOT reprocessed on settings change.** The orchestrator stamps `processing_jobs.settings_version` at claim time; once stamped, the job runs with that snapshot to completion. Reprocess is an explicit user action (Epic 7 Story 7.5). | Story AC-3: "Existing videos are *not* re-processed automatically — the user must trigger reprocess (Epic 7 Story 7.5)." | Auto-reprocess can blow up cost (e.g., user flips `stt.backend` and inadvertently re-transcribes 50,000 videos with a paid API). Settings-version stamping makes the boundary explicit and gives ops a clear handle for forensics. |

If D2 is rejected (hand-coded validators): every new setting added in Stories
9.8–9.18 needs a touch in the validator + the OpenAPI generator + the unit
tests. We accept the small upfront cost of the keySpec table for that
recurring saving.

If D6 is rejected (no trigger, NOTIFY in handler): a SQL admin who rewrites
`libraries.settings` directly silently breaks the Pipeline cache. Trigger-based
NOTIFY is the only correct option.

---

## 1. Architecture diagram — config write path and consumer wiring

```
   PATCH /api/libraries/{id}        (Epic 7 Story 7.3 handler)
            │
            ▼
   ┌────────────────────────────────────────────────────────────────┐
   │ apps/api/internal/library/settings_validator.go                │
   │   Validate(merged JSONB) → (cleaned JSONB, warnings[], err)    │
   │     - keySpec table walk (D2)                                  │
   │     - per-key validate fn (regex / enum / range)               │
   │     - unknown keys preserved + warning row                     │
   │     - error path returns 422 with offending JSON pointer       │
   └────────────────────────────┬───────────────────────────────────┘
                                │
                                ▼
   ┌────────────────────────────────────────────────────────────────┐
   │ apps/api/internal/library/settings_repo.go                     │
   │   ApplyMergePatch(libraryID, patch) → (newSettings, version)   │
   │     BEGIN;                                                     │
   │       SELECT settings, settings_version                        │
   │         FROM libraries WHERE id = $1 FOR UPDATE;               │
   │       merged = jsonMergePatch(stored, patch)        (D5)       │
   │       UPDATE libraries                                         │
   │          SET settings = $2, settings_version = settings_version+1 │
   │        WHERE id = $1;                                          │
   │     COMMIT;                                                    │
   │   -- AFTER UPDATE trigger fires NOTIFY library.settings_changed│
   │      payload {"library_id":"...","version":N}        (D6)      │
   └────────────────────────────┬───────────────────────────────────┘
                                │
                                ▼
                      Postgres NOTIFY channel
                       library.settings_changed
                                │
   ┌─────────────────┬──────────┴────────────┬────────────────────────┐
   │                 │                       │                        │
   ▼                 ▼                       ▼                        ▼
 Pipeline        Pipeline                 Pipeline                  API
 watcher         sweep scheduler          job claimer               LRU cache
 (Plan 9.2)      (Plan 9.3)               (Pipeline core)           (per Epic 7)
   │                 │                       │
   │   on NOTIFY:    │   on NOTIFY:          │   on claim:
   │   reload        │   reload              │   read settings_version,
   │   ignore_globs  │   sweep_interval_sec  │   resolve effective config
   │                 │                       │   via deep_merge (D4),
   │                 │                       │   stamp the job (D8).
```

The `libraries.settings_version BIGINT` column anchors the contract: the
trigger increments it on every UPDATE; the NOTIFY payload carries it; the
job-claim code stamps it on `processing_jobs.settings_version`.

---

## 2. Detailed implementation

### 2.1 SQL migration

```sql
-- shared/db/migrations/00XX_library_settings.sql
BEGIN;

-- Add settings column if missing (idempotent for environments where
-- Epic 1 already created it).
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'libraries' AND column_name = 'settings'
    ) THEN
        ALTER TABLE libraries
            ADD COLUMN settings JSONB NOT NULL DEFAULT '{}'::jsonb;
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'libraries' AND column_name = 'settings_version'
    ) THEN
        ALTER TABLE libraries
            ADD COLUMN settings_version BIGINT NOT NULL DEFAULT 0;
    END IF;
END
$$;

-- Trigger: bump settings_version on settings change, then NOTIFY.
CREATE OR REPLACE FUNCTION library_settings_notify() RETURNS trigger AS $$
BEGIN
    IF NEW.settings IS DISTINCT FROM OLD.settings THEN
        NEW.settings_version := COALESCE(OLD.settings_version, 0) + 1;
        PERFORM pg_notify(
            'library.settings_changed',
            json_build_object(
                'library_id', NEW.id::text,
                'version',    NEW.settings_version
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS library_settings_notify_trg ON libraries;
CREATE TRIGGER library_settings_notify_trg
    BEFORE UPDATE ON libraries
    FOR EACH ROW
    EXECUTE FUNCTION library_settings_notify();

-- Index on settings_version is unnecessary (PK lookup gets it).

COMMIT;
```

### 2.2 Go package layout (API service)

```
apps/api/internal/library/
├── settings_keys.go            // keySpec table, defaults, key constants
├── settings_validator.go       // Validate(jsonb) -> (clean, warnings, err)
├── settings_repo.go            // ApplyMergePatch via FOR UPDATE
├── settings_merge.go           // RFC-7396 JSON Merge Patch
├── settings_errors.go          // ValidationError with JSON pointer
└── settings_test.go            // unit tests against keySpec
apps/api/cmd/api/wire.go        // wires SettingsValidator into HTTP handler
```

### 2.3 `settings_keys.go` — keySpec table (D2)

```go
package library

import (
    "errors"
    "fmt"
    "regexp"
)

type keyType int

const (
    typeString keyType = iota
    typeBool
    typeInt
    typeStringArray
    typeObject
)

type keySpec struct {
    name     string
    typ      keyType
    required bool
    validate func(v any) error
    // defaultValue is used for *response shaping*, not storage.
    // Defaults at runtime are resolved by the Pipeline (D4); the API
    // emits them only in GET responses if requested.
    defaultValue any
}

var iso6391 = regexp.MustCompile(`^[a-z]{2}$`)

var sttBackends = map[string]struct{}{
    "whisper-mlx": {}, "whisper-cpp": {}, "openai": {},
    "deepgram":    {}, "vosk":        {},
}

var keySpecs = []keySpec{
    {
        name: "language", typ: typeString, defaultValue: "auto",
        validate: func(v any) error {
            s, ok := v.(string)
            if !ok {
                return fmt.Errorf("expected string")
            }
            if s == "auto" || iso6391.MatchString(s) {
                return nil
            }
            return fmt.Errorf("must be \"auto\" or ISO-639-1 code")
        },
    },
    {name: "multi_audio", typ: typeBool, defaultValue: false,
        validate: validateBool},
    {
        name: "stt", typ: typeObject,
        validate: func(v any) error {
            obj, ok := v.(map[string]any)
            if !ok {
                return fmt.Errorf("expected object")
            }
            if backend, has := obj["backend"]; has {
                s, ok := backend.(string)
                if !ok {
                    return errors.New("stt.backend must be string")
                }
                if _, known := sttBackends[s]; !known {
                    return fmt.Errorf("stt.backend %q is not a recognized backend", s)
                }
            }
            // model, profile, initial_prompt, max_usd_per_month are
            // validated by their nested keys if present.
            if mb, has := obj["max_usd_per_month"]; has {
                f, ok := toFloat(mb)
                if !ok || f < 0 {
                    return errors.New("stt.max_usd_per_month must be non-negative number")
                }
            }
            return nil
        },
    },
    {name: "embedding", typ: typeObject, validate: validateEmbedding},
    {name: "diarize", typ: typeBool, defaultValue: false, validate: validateBool},
    {name: "chapter_inference", typ: typeBool, defaultValue: false,
        validate: validateBool},
    {name: "auto_tag_topics", typ: typeBool, defaultValue: false,
        validate: validateBool},
    {
        name: "default_subtitle_lang", typ: typeString, defaultValue: "auto",
        validate: func(v any) error {
            s, ok := v.(string)
            if !ok {
                return fmt.Errorf("expected string")
            }
            if s == "auto" || iso6391.MatchString(s) {
                return nil
            }
            return fmt.Errorf("must be \"auto\" or ISO-639-1 code")
        },
    },
    {
        name: "ignore_globs", typ: typeStringArray,
        defaultValue: []string{},
        validate: func(v any) error {
            arr, ok := v.([]any)
            if !ok {
                return fmt.Errorf("expected array of strings")
            }
            for i, x := range arr {
                if _, ok := x.(string); !ok {
                    return fmt.Errorf("ignore_globs[%d] must be string", i)
                }
            }
            return nil
        },
    },
    {
        name: "sweep_interval_sec", typ: typeInt, defaultValue: 21600,
        validate: func(v any) error {
            n, ok := toInt(v)
            if !ok || n < 0 {
                return fmt.Errorf("must be non-negative integer (0 disables)")
            }
            return nil
        },
    },
}

func validateBool(v any) error {
    if _, ok := v.(bool); !ok {
        return fmt.Errorf("expected bool")
    }
    return nil
}

func validateEmbedding(v any) error {
    obj, ok := v.(map[string]any)
    if !ok {
        return fmt.Errorf("expected object {model, device}")
    }
    if m, has := obj["model"]; has {
        if _, ok := m.(string); !ok {
            return fmt.Errorf("embedding.model must be string")
        }
    }
    if d, has := obj["device"]; has {
        s, ok := d.(string)
        if !ok || (s != "cpu" && s != "cuda" && s != "mps") {
            return fmt.Errorf("embedding.device must be one of cpu|cuda|mps")
        }
    }
    return nil
}
```

### 2.4 `settings_validator.go` — Validate (D2)

```go
package library

import (
    "encoding/json"
    "fmt"
    "log/slog"
    "sort"
)

// Warning is surfaced in the PATCH response when an unknown key is stored.
type Warning struct {
    Path    string `json:"path"`     // JSON pointer
    Message string `json:"message"`
}

type ValidationError struct {
    Path    string // JSON pointer to the offending key
    Message string
}

func (e *ValidationError) Error() string {
    return fmt.Sprintf("settings: %s: %s", e.Path, e.Message)
}

// Validate cleans a settings object: validates every recognized key,
// preserves unknown keys, and returns warnings for them.
func Validate(raw json.RawMessage) (json.RawMessage, []Warning, error) {
    var obj map[string]any
    if err := json.Unmarshal(raw, &obj); err != nil {
        return nil, nil, &ValidationError{Path: "/", Message: "not a JSON object"}
    }

    knownNames := make(map[string]*keySpec, len(keySpecs))
    for i := range keySpecs {
        knownNames[keySpecs[i].name] = &keySpecs[i]
    }

    var warnings []Warning
    keys := make([]string, 0, len(obj))
    for k := range obj {
        keys = append(keys, k)
    }
    sort.Strings(keys) // deterministic output

    for _, k := range keys {
        spec, known := knownNames[k]
        if !known {
            warnings = append(warnings, Warning{
                Path:    "/" + k,
                Message: "unknown key — stored as-is",
            })
            continue
        }
        if err := spec.validate(obj[k]); err != nil {
            return nil, nil, &ValidationError{
                Path:    "/" + k,
                Message: err.Error(),
            }
        }
    }

    out, _ := json.Marshal(obj) // canonical reserialization
    if len(warnings) > 0 {
        slog.Info("library.settings.unknown_keys",
            "count", len(warnings),
            "first", warnings[0].Path)
    }
    return out, warnings, nil
}
```

### 2.5 `settings_merge.go` — RFC-7396 (D5)

```go
package library

import "encoding/json"

// MergePatch applies an RFC-7396 JSON Merge Patch to the target object.
// `null` leaves on the patch delete the corresponding key on the target.
func MergePatch(target, patch json.RawMessage) (json.RawMessage, error) {
    var t, p any
    if err := json.Unmarshal(target, &t); err != nil {
        return nil, err
    }
    if err := json.Unmarshal(patch, &p); err != nil {
        return nil, err
    }
    return json.Marshal(mergeValue(t, p))
}

func mergeValue(target, patch any) any {
    pm, pIsObj := patch.(map[string]any)
    if !pIsObj {
        return patch // arrays and scalars REPLACE (D4 lists-replace)
    }
    tm, tIsObj := target.(map[string]any)
    if !tIsObj {
        tm = map[string]any{}
    }
    for k, v := range pm {
        if v == nil {
            delete(tm, k)
            continue
        }
        tm[k] = mergeValue(tm[k], v)
    }
    return tm
}
```

### 2.6 `settings_repo.go` — ApplyMergePatch (D5, D6)

```go
package library

import (
    "context"
    "encoding/json"
    "errors"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct{ db *pgxpool.Pool }

func NewRepo(db *pgxpool.Pool) *Repo { return &Repo{db: db} }

type ApplyResult struct {
    Settings        json.RawMessage `json:"settings"`
    SettingsVersion int64           `json:"settings_version"`
    Warnings        []Warning       `json:"warnings,omitempty"`
}

// ApplyMergePatch validates + applies a JSON Merge Patch in one txn.
// The trigger from migration 00XX increments settings_version and
// pg_notify's library.settings_changed.
func (r *Repo) ApplyMergePatch(
    ctx context.Context, libraryID uuid.UUID, patch json.RawMessage,
) (*ApplyResult, error) {
    tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
    if err != nil {
        return nil, err
    }
    defer tx.Rollback(ctx)

    var stored json.RawMessage
    err = tx.QueryRow(ctx,
        `SELECT settings FROM libraries WHERE id = $1 FOR UPDATE`,
        libraryID,
    ).Scan(&stored)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            return nil, ErrNotFound
        }
        return nil, err
    }

    merged, err := MergePatch(stored, patch)
    if err != nil {
        return nil, err
    }
    cleaned, warnings, err := Validate(merged)
    if err != nil {
        return nil, err
    }

    var version int64
    err = tx.QueryRow(ctx,
        `UPDATE libraries
            SET settings = $2
          WHERE id = $1
        RETURNING settings_version`,
        libraryID, cleaned,
    ).Scan(&version)
    if err != nil {
        return nil, err
    }

    if err := tx.Commit(ctx); err != nil {
        return nil, err
    }
    return &ApplyResult{
        Settings:        cleaned,
        SettingsVersion: version,
        Warnings:        warnings,
    }, nil
}

var ErrNotFound = errors.New("library not found")
```

### 2.7 Pipeline-side resolution (D4, D7)

```python
# pipeline/src/maktaba_pipeline/config/merge.py
"""Deep-merge defaults inheritance for library.settings.

Resolution order (lowest to highest priority):
  1. hard-coded constants in defaults.py
  2. [stt.default] from pipeline.toml
  3. [stt.<profile>] from pipeline.toml (if settings.stt.profile set)
  4. library.settings as stored in Postgres
"""
from __future__ import annotations
from typing import Any


def deep_merge(*layers: dict | None) -> dict:
    """Merge layers from lowest to highest priority. Lists REPLACE (D4)."""
    out: dict[str, Any] = {}
    for layer in layers:
        if not layer:
            continue
        out = _merge_in(out, layer)
    return out


def _merge_in(target: dict, patch: dict) -> dict:
    for k, v in patch.items():
        if isinstance(v, dict) and isinstance(target.get(k), dict):
            target[k] = _merge_in(dict(target[k]), v)
        else:
            target[k] = v          # lists & scalars REPLACE
    return target


def resolve_effective(
    library_settings: dict,
    pipeline_toml: dict,
    hard_defaults: dict,
) -> dict:
    profile = (library_settings.get("stt") or {}).get("profile")
    layers = [
        hard_defaults,
        (pipeline_toml.get("stt") or {}).get("default") or {},
    ]
    if profile:
        layers.append((pipeline_toml.get("stt") or {}).get(profile) or {})
    layers.append(library_settings)
    return deep_merge(*layers)
```

```python
# pipeline/src/maktaba_pipeline/config/cache.py
"""SettingsCache — keyed by (library_id, settings_version), invalidated by
the LISTEN library.settings_changed channel."""
from __future__ import annotations
import asyncio, json, logging
from collections import OrderedDict
from typing import Any, Awaitable, Callable

log = logging.getLogger(__name__)


class SettingsCache:
    def __init__(self, *, max_entries: int = 256,
                 fetch: Callable[[str], Awaitable[tuple[dict, int]]]):
        self._cache: "OrderedDict[str, tuple[int, dict]]" = OrderedDict()
        self._max = max_entries
        self._fetch = fetch        # async (library_id) -> (settings, version)
        self._lock = asyncio.Lock()

    async def get(self, library_id: str) -> tuple[dict, int]:
        async with self._lock:
            hit = self._cache.get(library_id)
            if hit is not None:
                self._cache.move_to_end(library_id)
                return hit[1], hit[0]
        settings, version = await self._fetch(library_id)
        async with self._lock:
            self._cache[library_id] = (version, settings)
            self._cache.move_to_end(library_id)
            if len(self._cache) > self._max:
                self._cache.popitem(last=False)
            return settings, version

    async def invalidate(self, library_id: str, *, version: int | None = None):
        async with self._lock:
            entry = self._cache.get(library_id)
            if entry is None:
                return
            if version is not None and entry[0] >= version:
                # We already have at least this version cached.
                return
            del self._cache[library_id]


async def listen_loop(conn, cache: SettingsCache):
    await conn.add_listener("library.settings_changed", _on_notify(cache))
    log.info("LISTEN library.settings_changed")


def _on_notify(cache: SettingsCache):
    async def _h(_conn, _pid, _channel, payload: str):
        try:
            obj = json.loads(payload)
            await cache.invalidate(obj["library_id"], version=int(obj["version"]))
            log.debug("invalidated %s@%s", obj["library_id"], obj["version"])
        except Exception as e:
            log.warning("notify parse failed: %s", e)
    return _h
```

### 2.8 Job stamping (D8)

```python
# pipeline/src/maktaba_pipeline/queue/claim.py  (excerpt)
async def claim_next(conn, *, library_id, worker_id) -> Job | None:
    settings, version = await SETTINGS_CACHE.get(library_id)
    row = await conn.fetchrow(
        """
        UPDATE processing_jobs
           SET state = 'claimed',
               worker_id = $2,
               settings_version = $3,
               settings_snapshot = $4::jsonb
         WHERE id = (
             SELECT id FROM processing_jobs
              WHERE library_id = $1 AND state = 'queued'
           ORDER BY priority DESC, created_at
              FOR UPDATE SKIP LOCKED LIMIT 1
         )
        RETURNING *
        """,
        library_id, worker_id, version, json.dumps(settings))
    return Job.from_row(row) if row else None
```

The `processing_jobs.settings_version` and `processing_jobs.settings_snapshot`
columns are added in the same migration as the libraries column above —
included here for completeness:

```sql
-- Same migration file:
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_name = 'processing_jobs' AND column_name = 'settings_version'
    ) THEN
        ALTER TABLE processing_jobs
            ADD COLUMN settings_version BIGINT,
            ADD COLUMN settings_snapshot JSONB;
    END IF;
END
$$;
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/00XX_library_settings.sql` | `libraries.settings`, `libraries.settings_version`, `library_settings_notify` trigger, `processing_jobs.settings_version/snapshot` | `test_migration_idempotent` |
| 2 | `apps/api/internal/library/settings_keys.go` | `keySpec`, `keySpecs`, `validateBool`, `validateEmbedding`, regex constants | `TestKeySpecCoverage` |
| 3 | `apps/api/internal/library/settings_errors.go` | `ValidationError`, `Warning` | (n/a) |
| 4 | `apps/api/internal/library/settings_merge.go` | `MergePatch`, `mergeValue` | `TestMergePatch` |
| 5 | `apps/api/internal/library/settings_validator.go` | `Validate` | `TestValidate*` |
| 6 | `apps/api/internal/library/settings_repo.go` | `Repo`, `Repo.ApplyMergePatch`, `ApplyResult`, `ErrNotFound` | `TestApplyMergePatchHappyPath`, `TestApplyMergePatchInvalid` |
| 7 | `pipeline/src/maktaba_pipeline/config/merge.py` | `deep_merge`, `resolve_effective` | `test_deep_merge`, `test_resolve_effective_inheritance` |
| 8 | `pipeline/src/maktaba_pipeline/config/cache.py` | `SettingsCache`, `listen_loop` | `test_settings_cache_invalidation` |
| 9 | `pipeline/src/maktaba_pipeline/config/defaults.py` (extend) | `HARD_DEFAULTS` dict matching the keySpec defaults | smoke import |

---

## 4. Test cases

### 4.1 `TestValidateLanguage` — AC-1

```go
func TestValidateLanguage(t *testing.T) {
    cases := []struct {
        in      string
        wantErr bool
    }{
        {`{"language":"auto"}`, false},
        {`{"language":"en"}`, false},
        {`{"language":"ar"}`, false},
        {`{"language":"english"}`, true},
        {`{"language":"EN"}`, true},
        {`{"language":42}`, true},
    }
    for _, tc := range cases {
        _, _, err := library.Validate(json.RawMessage(tc.in))
        if (err != nil) != tc.wantErr {
            t.Fatalf("%s: wantErr=%v, got %v", tc.in, tc.wantErr, err)
        }
    }
}
```

### 4.2 `TestValidateSttBackend` — AC-1

```go
func TestValidateSttBackend(t *testing.T) {
    bad := `{"stt":{"backend":"invalid"}}`
    _, _, err := library.Validate(json.RawMessage(bad))
    var ve *library.ValidationError
    if !errors.As(err, &ve) {
        t.Fatalf("expected ValidationError, got %T", err)
    }
    if ve.Path != "/stt" {
        t.Fatalf("path: want /stt got %q", ve.Path)
    }
}
```

### 4.3 `TestUnknownKeysWarn` — AC-1 forward-compat

```go
func TestUnknownKeysWarn(t *testing.T) {
    out, warnings, err := library.Validate(
        json.RawMessage(`{"future_knob":true,"language":"en"}`))
    if err != nil {
        t.Fatal(err)
    }
    if len(warnings) != 1 || warnings[0].Path != "/future_knob" {
        t.Fatalf("expected one warning at /future_knob; got %+v", warnings)
    }
    // Round-trip preserves the unknown key:
    var obj map[string]any
    _ = json.Unmarshal(out, &obj)
    if obj["future_knob"] != true {
        t.Fatalf("unknown key dropped: %v", obj)
    }
}
```

### 4.4 `TestMergePatch` — AC-1, D5

```go
func TestMergePatchSetsAndDeletes(t *testing.T) {
    base := json.RawMessage(`{"stt":{"backend":"whisper-mlx","model":"large-v2"}}`)
    patch := json.RawMessage(`{"stt":{"model":"large-v3"},"diarize":true}`)
    got, err := library.MergePatch(base, patch)
    if err != nil {
        t.Fatal(err)
    }
    var obj map[string]any
    _ = json.Unmarshal(got, &obj)
    if obj["stt"].(map[string]any)["model"] != "large-v3" {
        t.Fatalf("model not updated: %v", obj)
    }
    if obj["stt"].(map[string]any)["backend"] != "whisper-mlx" {
        t.Fatalf("backend lost on merge: %v", obj)
    }

    delPatch := json.RawMessage(`{"stt":{"backend":null}}`)
    got, _ = library.MergePatch(got, delPatch)
    _ = json.Unmarshal(got, &obj)
    if _, has := obj["stt"].(map[string]any)["backend"]; has {
        t.Fatalf("null in patch should delete: %v", obj)
    }
}
```

### 4.5 `TestApplyMergePatchHappyPath` — AC-3 (NOTIFY observable)

```go
func TestApplyMergePatchBumpsVersionAndNotifies(t *testing.T) {
    db := freshDB(t)
    repo := library.NewRepo(db)
    libID := seedLibrary(t, db)

    // Subscribe before write.
    listener := newListener(t, db, "library.settings_changed")
    defer listener.Close()

    res, err := repo.ApplyMergePatch(ctx, libID,
        json.RawMessage(`{"stt":{"backend":"whisper-mlx"}}`))
    if err != nil {
        t.Fatal(err)
    }
    if res.SettingsVersion != 1 {
        t.Fatalf("expected version 1 got %d", res.SettingsVersion)
    }
    payload := listener.WaitOne(t, 2*time.Second)
    if !strings.Contains(payload, libID.String()) ||
       !strings.Contains(payload, `"version":1`) {
        t.Fatalf("notify payload: %s", payload)
    }
}
```

### 4.6 `test_deep_merge` — AC-2

```python
def test_deep_merge_lists_replace_dicts_merge():
    base = {"stt": {"backend": "whisper-mlx", "model": "large-v2",
                    "extra_args": ["--beam=1"]}}
    over = {"stt": {"model": "large-v3", "extra_args": ["--beam=5"]},
            "diarize": True}
    out = deep_merge(base, over)
    assert out["stt"]["backend"] == "whisper-mlx"   # preserved
    assert out["stt"]["model"]   == "large-v3"      # overridden
    assert out["stt"]["extra_args"] == ["--beam=5"] # list REPLACED
    assert out["diarize"] is True                   # added
```

### 4.7 `test_resolve_effective_inheritance` — AC-2

```python
def test_settings_inherit_from_pipeline_toml_default():
    pipeline_toml = {
        "stt": {
            "default": {"backend": "whisper-mlx", "model": "large-v2",
                        "language": "auto"},
            "fast":    {"model": "tiny", "language": "en"},
        }
    }
    library = {"stt": {"profile": "fast"}}
    eff = resolve_effective(library, pipeline_toml, HARD_DEFAULTS)
    # library overrides nothing -> profile values bubble up
    assert eff["stt"]["backend"] == "whisper-mlx"
    assert eff["stt"]["model"]   == "tiny"
    assert eff["stt"]["language"] == "en"
```

### 4.8 `test_settings_cache_invalidation` — AC-3, D7

```python
async def test_cache_invalidates_on_notify(monkeypatch):
    fetch_calls = {"n": 0}
    settings_db = {"L1": ({"language": "en"}, 1)}

    async def fetch(library_id):
        fetch_calls["n"] += 1
        return settings_db[library_id]

    cache = SettingsCache(fetch=fetch)
    s, v = await cache.get("L1"); assert s["language"] == "en"; assert v == 1
    s, v = await cache.get("L1"); assert fetch_calls["n"] == 1   # cached

    settings_db["L1"] = ({"language": "ar"}, 2)
    await cache.invalidate("L1", version=2)
    s, _ = await cache.get("L1"); assert s["language"] == "ar"
    assert fetch_calls["n"] == 2
```

### 4.9 `test_migration_idempotent`

```python
async def test_migration_runs_twice_without_error(empty_db):
    await apply_migration(empty_db, "00XX_library_settings.sql")
    await apply_migration(empty_db, "00XX_library_settings.sql")  # idempotent
    cols = await empty_db.fetch("""
        SELECT column_name FROM information_schema.columns
         WHERE table_name = 'libraries'
           AND column_name IN ('settings', 'settings_version')
    """)
    assert {c["column_name"] for c in cols} == {"settings", "settings_version"}
```

### 4.10 `TestPatchHandler422` — story integration

```go
func TestPatchHandlerReturns422OnInvalidBackend(t *testing.T) {
    srv := newAPIServer(t)
    body := `{"stt":{"backend":"invalid"}}`
    resp := srv.PATCH(t, "/api/libraries/"+libID.String(), body)
    if resp.Code != 422 {
        t.Fatalf("expected 422 got %d", resp.Code)
    }
    var doc struct{ Error struct{ Path, Message string } }
    _ = json.Unmarshal(resp.Body.Bytes(), &doc)
    if doc.Error.Path != "/stt" {
        t.Fatalf("expected /stt; got %q", doc.Error.Path)
    }
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handling |
|-----|-----------|----------|
| E1  | **Future schema version (forward compat).** Library written by a newer Maktaba version with key `transcoding_profile_v2`. Older binary reads the JSONB and validates: unknown key → preserved, surfaced as Warning on next PATCH. Round-trip-equal. | D2 + `TestUnknownKeysWarn`. |
| E2  | **`language` change does not retroactively re-tag old videos.** Job stamping at claim time (D8) means in-flight jobs use the snapshot they were claimed with; queued jobs pick up the new value at claim. | D8 + integration test on `processing_jobs.settings_snapshot`. |
| E3  | **Setting `null` deletes a key**, lets defaults bubble back up. After `{"diarize":null}` the Pipeline reads `diarize` from `pipeline.toml` defaults. | D5 + `TestMergePatchSetsAndDeletes`. |
| E4  | **Concurrent PATCHes (last-writer-wins).** Two clients PATCH at the same version; FOR UPDATE serializes them. The trigger increments `settings_version` for each successful UPDATE, so both NOTIFYs fire. The Pipeline cache receives both and reloads twice — idempotent. | D6 + `SELECT … FOR UPDATE` in `ApplyMergePatch`. |
| E5  | **Validator emits warning even when error returns.** Don't: warnings are only collected when validation succeeds (we return early on first error). The error itself is surfaced via the JSON pointer in `ValidationError.Path`. | `TestValidateSttBackend`. |
| E6  | **PATCH that sets the same value as currently stored.** The trigger uses `IS DISTINCT FROM`, which evaluates JSONB equality. Equal means: no version bump, no NOTIFY. Cheap idempotency. | Trigger SQL in §2.1. |
| E7  | **Pipeline starts before LISTEN connects.** First call to `SettingsCache.get` always fetches; subsequent calls use the cache; missed NOTIFYs during the connect gap are recovered by the version-bumped fetch (a stale entry's `version` is below the DB's, so the next fetch corrects it). | D7 cache contract. |
| E8  | **Empty patch `{}` is a no-op.** `MergePatch` returns the stored object byte-for-byte; the trigger sees `IS DISTINCT FROM = false`; no version bump. Same as E6 path. | Trigger short-circuit. |
| E9  | **Patch with non-object root** (e.g., `[1,2,3]`). Validator returns `ValidationError{Path:"/", Message:"not a JSON object"}`; PATCH 422. | `Validate`. |
| E10 | **Setting stored as `NULL` rather than `{}`.** Migration's `NOT NULL DEFAULT '{}'::jsonb` prevents this for new rows; the migration fixes any pre-existing NULLs (`UPDATE libraries SET settings = '{}'::jsonb WHERE settings IS NULL`). | Migration + DB constraint. |

---

## 6. Acceptance checklist

- [ ] **A1** `Validate` recognizes every key from AC-1: `language`, `multi_audio`, `stt`, `embedding`, `diarize`, `chapter_inference`, `auto_tag_topics`, `default_subtitle_lang`, `ignore_globs`, `sweep_interval_sec`. Each has a positive and negative unit test. (`TestValidate*`)
- [ ] **A2** Unknown keys are stored verbatim and surface as a `warnings[]` entry in the PATCH response. (`TestUnknownKeysWarn`)
- [ ] **A3** `deep_merge` produces an effective config from `hard_defaults` + `[stt.default]` + `[stt.<profile>]` + library settings, with library overriding all. Lists replace, dicts merge. (`test_deep_merge`, `test_resolve_effective_inheritance`)
- [ ] **A4** PATCH succeeds → `library.settings_changed` NOTIFY fires with `{library_id, version}` payload; the trigger bumps `settings_version`. (`TestApplyMergePatchBumpsVersionAndNotifies`)
- [ ] **A5** Existing videos are NOT reprocessed on PATCH; `processing_jobs.settings_snapshot` is stamped at claim time and used to completion. (Pipeline integration test under Story 7.5; this plan owns the column + stamping code.)
- [ ] **A6** Malformed `stt.backend` returns 422 with `path = /stt` from the PATCH handler. (`TestPatchHandlerReturns422OnInvalidBackend`)
- [ ] **A7** `null` in a patch deletes the corresponding key (RFC-7396). (`TestMergePatchSetsAndDeletes`)
- [ ] **A8** Migration is idempotent: applying it twice on a populated database does not error and does not duplicate the trigger. (`test_migration_runs_twice_without_error`)
- [ ] **A9** SettingsCache invalidates on NOTIFY; reads after invalidation re-fetch from DB. (`test_cache_invalidates_on_notify`)
- [ ] **A10** Setting `library.settings` to the same JSONB does not bump the version (no NOTIFY). (Trigger test.)

---

## 7. Performance budget

| Operation | Budget | Notes |
|-----------|--------|-------|
| `Validate(jsonb)` | < 1 ms typical | Pure CPU; ~10 keys × regex/enum check. |
| `MergePatch` | < 0.5 ms | Recursive map walk. |
| `ApplyMergePatch` round-trip | ≤ 5 ms p95 (LAN) | Two indexed queries + commit. |
| NOTIFY delivery to LISTEN | ≤ 50 ms median | Postgres asynchronous notify; backpressure-bounded. |
| `SettingsCache.get` (hit) | < 1 µs | OrderedDict lookup. |
| `SettingsCache.get` (miss) | one fetch round-trip | Bounded by DB latency. |
| Job claim with stamp | unchanged from baseline (one extra column write) | The same UPDATE that claims the job now also writes `settings_snapshot`. |
