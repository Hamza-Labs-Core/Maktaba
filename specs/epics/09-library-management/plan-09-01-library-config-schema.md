# Implementation Plan — Story 9.1 Library Config Schema and Validation

> Companion to [story-09-01-library-config-schema.md](story-09-01-library-config-schema.md).
> The story states *what* and *why*; this plan states *how*.
> Aligns with [architecture.md §5](../../architecture.md), the
> [Epic 9 README](README.md), and the per-library config decisions in
> Epic 7 Story 7.3 (the REST surface) and the Pipeline-side
> `pipeline.toml` defaults at §11.4.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Schema authority | A single JSON-Schema document at `shared/schema/library_settings.schema.json` (Draft 2020-12). Both Go (API) and Python (Pipeline) load and validate against the same file at boot — no two parsers diverging. |
| Migration | `shared/db/migrations/0030_libraries_settings.sql` adds the `settings JSONB NOT NULL DEFAULT '{}'::jsonb` column on `libraries` if not already present, plus a CHECK that the blob is an object (`jsonb_typeof = 'object'`). The blob's *internal* shape is enforced in application code, not in SQL. |
| Validation entry point (Go) | `api/internal/libraries/settings.go` — `Validate(raw json.RawMessage) (Effective, []Warning, error)`. Used by the PATCH and POST handlers in Epic 7 Story 7.3. |
| Validation entry point (Python) | `pipeline/src/maktaba_pipeline/config/library.py` — `effective_for(library_id) -> EffectiveLibrarySettings`. Used by every worker that reads per-library config. |
| Defaults inheritance | Three layers: built-in defaults (in code) ← `pipeline.toml` overrides ← per-library `settings` JSONB. Deep-merge per AC-2. |
| NOTIFY channel for change | `library.settings_changed` (added to the canonical channel list in `pipeline/db/pubsub.py`). Payload: `{library_id, changed_keys, effective_after}`. |
| Out of scope | The HTTP routes themselves (Epic 7 Story 7.3); the PATCH-driven re-evaluation of in-flight jobs (Epic 7 Story 7.5); per-library audit emission for settings changes (Story 9.17 wires `category='library', event='settings-changed'`). |

## 1. Architecture diagram

```
   ┌──────────────────────────────────────────────────────────────┐
   │  PATCH /api/libraries/{id}                                   │
   │  body.settings = { ... partial overrides ... }               │
   └────────────────┬─────────────────────────────────────────────┘
                    │
                    ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  api/internal/libraries/settings.go :: Validate              │
   │   1. JSON-Schema validate against                            │
   │      shared/schema/library_settings.schema.json              │
   │   2. Type/range checks beyond schema                         │
   │      (ISO-639-1 set, stt.backend ∈ enum, etc.)              │
   │   3. Collect WARNINGS for unknown keys (preserved verbatim) │
   │   4. Return (Validated, Warnings, nil) or (_, _, *ValidationError)│
   └────────────────┬─────────────────────────────────────────────┘
                    │ if no error
                    ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  Postgres tx:                                                 │
   │   UPDATE libraries                                            │
   │      SET settings = settings || $1::jsonb,                    │
   │          updated_at = now()                                   │
   │    WHERE id = $2                                              │
   │   AFTER UPDATE trigger fires:                                 │
   │      pg_notify('library.settings_changed',                    │
   │                json_build_object(                             │
   │                  'library_id', NEW.id,                        │
   │                  'changed_keys', diff_keys(OLD, NEW),         │
   │                  'effective_after', now())::text)             │
   └────────────────┬─────────────────────────────────────────────┘
                    │ NOTIFY 'library.settings_changed'
                    ▼
   ┌──────────────────────────────────────────────────────────────┐
   │  Pipeline subscribers:                                        │
   │   - orchestrator (uses new model for new jobs)                │
   │   - watcher reload (Story 9.2)                                │
   │  API subscribers: WS broadcast `library:settings`            │
   └──────────────────────────────────────────────────────────────┘
```

The deep-merge layering, per AC-2:

```
   built-in defaults (code constants)
     ↑ override by
   pipeline.toml [library.defaults]   ← single-source operator-level config
     ↑ override by
   pipeline.toml [stt.default], [embedding.default], …  (sub-section defaults)
     ↑ override by
   libraries.settings JSONB           ← per-library user config
                ▼
   EffectiveLibrarySettings (frozen at job dispatch time)
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/schema/library_settings.schema.json` | JSON-Schema 2020-12. The authoritative shape, recognized keys, enums, and ranges. Loaded by both Go and Python at boot. |
| `shared/db/migrations/0030_libraries_settings.sql` | Adds `settings JSONB NOT NULL DEFAULT '{}'::jsonb` (idempotent — uses `IF NOT EXISTS`); adds the `jsonb_typeof='object'` CHECK; adds the `library_settings_changed_trg` AFTER UPDATE trigger. |
| `shared/db/migrations/0030_libraries_settings.sqlite.sql` | SQLite variant: column add only; the trigger is replaced by a Python-side publish on `PubsubBus`. |
| `api/internal/libraries/settings.go` | `Validate`, `Effective`, `ValidationError`, `Warning` types. The single source of truth for what a "valid" settings blob looks like on the Go side. |
| `api/internal/libraries/settings_defaults.go` | Built-in default constants. Keep them in code (not config) so a deployment cannot start with no defaults. |
| `pipeline/src/maktaba_pipeline/config/library.py` | `effective_for`, `EffectiveLibrarySettings` (frozen dataclass), the deep-merge helper, JSON-Schema validation against the shared file. |
| `pipeline/src/maktaba_pipeline/config/defaults.py` | Mirror of the built-in default constants for Python; tests in §6 prove parity with the Go side. |
| `api/internal/libraries/settings_test.go` | Unit tests per §6.1. |
| `pipeline/tests/config/test_library_settings.py` | Unit + integration tests per §6.2 + §6.3. |
| `shared/schema/test_fixtures/library_settings/` | A directory of `valid_*.json` and `invalid_*.json` fixtures consumed by both languages' parity tests. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/db/queries/libraries.sql` | Add `UpdateLibrarySettings` (the merge UPDATE) and `GetLibrarySettings` queries. |
| `pipeline/src/maktaba_pipeline/db/pubsub.py` | Add `LIBRARY_SETTINGS_CHANGED = "library.settings_changed"` constant. |
| `pipeline/pyproject.toml` | Add `jsonschema>=4.21` (Draft 2020-12 support). |
| `api/go.mod` | Add `github.com/santhosh-tekuri/jsonschema/v6` (mature, no CGO, supports 2020-12). |
| `specs/epics/09-library-management/README.md` | Tick story 9.1 once landed. |

### 2.3 Type definitions (canonical)

Go side (`api/internal/libraries/settings.go`):

```go
package libraries

import (
    "encoding/json"
    "time"

    "github.com/google/uuid"
)

// STTBackend is the closed enum of supported STT engines.
// Adding a value here is a coordinated change across Go + Python +
// shared/schema/library_settings.schema.json (search "stt.backend.enum").
type STTBackend string

const (
    STTWhisperMLX     STTBackend = "whisper-mlx"
    STTWhisperFaster  STTBackend = "whisper-faster"
    STTWhisperOpenAI  STTBackend = "whisper-openai"
    STTWhisperAPI     STTBackend = "openai-api"
)

type STTSettings struct {
    Backend            STTBackend `json:"backend"`
    Model              string     `json:"model"`
    Profile            string     `json:"profile"`
    InitialPrompt      *string    `json:"initial_prompt,omitempty"`
    MaxUSDPerMonth     *float64   `json:"max_usd_per_month,omitempty"`
}

type EmbeddingSettings struct {
    Model  string `json:"model"`
    Device string `json:"device"` // "cpu" | "mps" | "cuda" | "auto"
}

// Effective is the fully-merged, validated, ready-to-use settings.
// All optional pointer fields are *resolved* — never nil after merge.
type Effective struct {
    Language             string            `json:"language"`             // "auto" | ISO-639-1
    MultiAudio           bool              `json:"multi_audio"`
    STT                  STTSettings       `json:"stt"`
    Embedding            EmbeddingSettings `json:"embedding"`
    Diarize              bool              `json:"diarize"`
    ChapterInference     bool              `json:"chapter_inference"`
    AutoTagTopics        bool              `json:"auto_tag_topics"`
    DefaultSubtitleLang  string            `json:"default_subtitle_lang"` // ISO-639-1
    IgnoreGlobs          []string          `json:"ignore_globs"`
    SweepIntervalSec     int               `json:"sweep_interval_sec"`    // 0 disables
    Unknown              map[string]any    `json:"-"`                     // round-tripped, surfaced as warnings
}

type Warning struct {
    Path    string `json:"path"`
    Message string `json:"message"`
    Code    string `json:"code"` // "unknown-key" | "deprecated" | ...
}

type ValidationError struct {
    Path    string `json:"path"`
    Message string `json:"message"`
    Code    string `json:"code"` // "schema-violation" | "out-of-range" | "unknown-enum"
}

func (v *ValidationError) Error() string { return v.Path + ": " + v.Message }
```

Python side (`pipeline/src/maktaba_pipeline/config/library.py`):

```python
from __future__ import annotations
from dataclasses import dataclass, field
from typing import Any, Literal
from uuid import UUID


STTBackend = Literal["whisper-mlx", "whisper-faster", "whisper-openai", "openai-api"]


@dataclass(slots=True, frozen=True)
class STTSettings:
    backend: STTBackend
    model: str
    profile: str
    initial_prompt: str | None = None
    max_usd_per_month: float | None = None


@dataclass(slots=True, frozen=True)
class EmbeddingSettings:
    model: str
    device: Literal["cpu", "mps", "cuda", "auto"]


@dataclass(slots=True, frozen=True)
class EffectiveLibrarySettings:
    language: str                       # "auto" | ISO-639-1
    multi_audio: bool
    stt: STTSettings
    embedding: EmbeddingSettings
    diarize: bool
    chapter_inference: bool
    auto_tag_topics: bool
    default_subtitle_lang: str
    ignore_globs: tuple[str, ...]       # tuple → hashable, frozen
    sweep_interval_sec: int
    unknown: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class Warning:
    path: str
    message: str
    code: str
```

### 2.4 Function signatures

```go
// api/internal/libraries/settings.go

// Validate runs the JSON-Schema and the supplemental checks against a
// raw partial blob. The blob may be a full settings object or a partial
// PATCH delta — Validate does not merge defaults.
func Validate(raw json.RawMessage) (validated json.RawMessage,
    warnings []Warning, err *ValidationError)

// MergeWithDefaults returns the Effective settings for the given
// per-library blob, layered over operator (pipeline.toml) and built-in
// defaults. It does NOT call the database; pass the blob in.
func MergeWithDefaults(perLibrary json.RawMessage,
    operatorOverrides map[string]any) (Effective, error)

// EffectiveFor loads the per-library blob from the DB and runs
// MergeWithDefaults. Used by handlers.
func EffectiveFor(ctx context.Context, q *db.Queries,
    libraryID uuid.UUID) (Effective, error)
```

```python
# pipeline/src/maktaba_pipeline/config/library.py

async def effective_for(db, library_id: UUID) -> EffectiveLibrarySettings: ...

def validate(raw: dict[str, Any]) -> tuple[dict[str, Any], list[Warning]]:
    """Schema + range validation. Raises ValidationError on hard failures.
    Returns (validated_blob_with_unknowns_preserved, warnings)."""

def merge_with_defaults(per_library: dict[str, Any],
                        operator: dict[str, Any]) -> EffectiveLibrarySettings: ...
```

## 3. Database migration

### 3.1 Postgres — `shared/db/migrations/0030_libraries_settings.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- 1. Add the column if missing. Existing libraries get an empty object
--    so MergeWithDefaults yields the built-in defaults.
ALTER TABLE libraries
    ADD COLUMN IF NOT EXISTS settings JSONB NOT NULL DEFAULT '{}'::jsonb;

-- 2. Outer shape: must be an object. Inner shape is checked in app code.
ALTER TABLE libraries
    ADD CONSTRAINT libraries_settings_is_object_chk
    CHECK (jsonb_typeof(settings) = 'object');

-- 3. NOTIFY trigger: fires when settings changes (no-op when unchanged).
CREATE OR REPLACE FUNCTION libraries_settings_changed_notify()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    diff TEXT[];
BEGIN
    IF OLD.settings IS DISTINCT FROM NEW.settings THEN
        SELECT COALESCE(array_agg(key), ARRAY[]::TEXT[]) INTO diff
          FROM (
              SELECT key FROM jsonb_each(NEW.settings)
              EXCEPT
              SELECT key FROM jsonb_each(OLD.settings)
              UNION
              SELECT key FROM jsonb_each(OLD.settings)
              EXCEPT
              SELECT key FROM jsonb_each(NEW.settings)
              UNION
              SELECT key FROM (
                  SELECT n.key
                    FROM jsonb_each(NEW.settings) n
                    JOIN jsonb_each(OLD.settings) o USING (key)
                   WHERE n.value IS DISTINCT FROM o.value
              ) changed
          ) k;

        PERFORM pg_notify(
            'library.settings_changed',
            json_build_object(
                'library_id', NEW.id,
                'changed_keys', diff,
                'effective_after', extract(epoch from now())
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS library_settings_changed_trg ON libraries;
CREATE TRIGGER library_settings_changed_trg
    AFTER UPDATE OF settings ON libraries
    FOR EACH ROW
    EXECUTE FUNCTION libraries_settings_changed_notify();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS library_settings_changed_trg ON libraries;
DROP FUNCTION IF EXISTS libraries_settings_changed_notify();
ALTER TABLE libraries DROP CONSTRAINT IF EXISTS libraries_settings_is_object_chk;
-- Intentionally do NOT drop the settings column on Down; the data is
-- not recoverable if mis-applied. Operations doc covers manual rollback.
-- +goose StatementEnd
```

### 3.2 SQLite — `shared/db/migrations/0030_libraries_settings.sqlite.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- SQLite cannot ADD COLUMN with NOT NULL+DEFAULT in old versions; we
-- rely on 3.35+ (which the project pins).
ALTER TABLE libraries ADD COLUMN settings TEXT NOT NULL DEFAULT '{}';

-- jsonb_typeof has no SQLite equivalent; use json_type which the
-- json1 extension provides (built into SQLite ≥3.38 by default).
-- The CHECK is the same intent as Postgres.
-- We reapply the table CHECK via a trigger because SQLite cannot ADD
-- CHECK to an existing table without a table rebuild.
CREATE TRIGGER libraries_settings_is_object_chk
BEFORE INSERT ON libraries
BEGIN
    SELECT CASE
        WHEN json_type(NEW.settings) IS NOT 'object'
        THEN RAISE(ABORT, 'libraries.settings must be a JSON object')
    END;
END;

CREATE TRIGGER libraries_settings_is_object_chk_upd
BEFORE UPDATE OF settings ON libraries
BEGIN
    SELECT CASE
        WHEN json_type(NEW.settings) IS NOT 'object'
        THEN RAISE(ABORT, 'libraries.settings must be a JSON object')
    END;
END;

-- No NOTIFY in SQLite; the Python helper publishes on PubsubBus after
-- the UPDATE commits.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS libraries_settings_is_object_chk_upd;
DROP TRIGGER IF EXISTS libraries_settings_is_object_chk;
-- Column drop omitted as in §3.1.
-- +goose StatementEnd
```

## 4. JSON-Schema (`shared/schema/library_settings.schema.json`)

The schema is the lockstep contract between Go and Python.

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://maktaba.local/schema/library_settings.json",
  "title": "Library Settings",
  "type": "object",
  "additionalProperties": true,
  "properties": {
    "language": {
      "type": "string",
      "description": "Forced language; 'auto' delegates to STT detection.",
      "anyOf": [
        { "const": "auto" },
        { "$ref": "#/$defs/iso639_1" }
      ]
    },
    "multi_audio":          { "type": "boolean", "default": false },
    "diarize":              { "type": "boolean", "default": false },
    "chapter_inference":    { "type": "boolean", "default": true  },
    "auto_tag_topics":      { "type": "boolean", "default": true  },
    "default_subtitle_lang": { "$ref": "#/$defs/iso639_1" },

    "ignore_globs": {
      "type": "array",
      "items": { "type": "string", "minLength": 1, "maxLength": 1024 },
      "maxItems": 256,
      "uniqueItems": true,
      "default": []
    },

    "sweep_interval_sec": {
      "type": "integer",
      "minimum": 0,
      "maximum": 604800,
      "default": 21600
    },

    "stt": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "backend": {
          "type": "string",
          "enum": [
            "whisper-mlx", "whisper-faster",
            "whisper-openai", "openai-api"
          ]
        },
        "model":   { "type": "string", "minLength": 1, "maxLength": 64 },
        "profile": { "type": "string", "enum": ["fast", "balanced", "quality"] },
        "initial_prompt": { "type": "string", "maxLength": 1024 },
        "max_usd_per_month": { "type": "number", "minimum": 0 }
      },
      "required": ["backend"]
    },

    "embedding": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "model": { "type": "string", "minLength": 1, "maxLength": 128 },
        "device": { "type": "string",
                    "enum": ["cpu", "mps", "cuda", "auto"] }
      },
      "required": ["model"]
    }
  },
  "$defs": {
    "iso639_1": {
      "type": "string",
      "pattern": "^[a-z]{2}$",
      "description": "Lowercase ISO-639-1 (alpha-2) code."
    }
  }
}
```

`additionalProperties: true` is intentional — AC-1 says unknown keys
round-trip and only emit a warning. The supplemental Go/Python checks
strip them off `Effective` and stash in `Unknown`.

## 5. Code scaffolding

### 5.1 Go validator (excerpt)

```go
// api/internal/libraries/settings.go
package libraries

import (
    _ "embed"
    "encoding/json"
    "fmt"

    "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed ../../../shared/schema/library_settings.schema.json
var schemaBytes []byte

var compiled = func() *jsonschema.Schema {
    c := jsonschema.NewCompiler()
    if err := c.AddResource("library_settings.json",
        bytes.NewReader(schemaBytes)); err != nil {
        panic(err)
    }
    s, err := c.Compile("library_settings.json")
    if err != nil {
        panic(err)
    }
    return s
}()

// known is the set of recognized top-level keys.
var known = map[string]struct{}{
    "language": {}, "multi_audio": {}, "diarize": {},
    "chapter_inference": {}, "auto_tag_topics": {},
    "default_subtitle_lang": {}, "ignore_globs": {},
    "sweep_interval_sec": {}, "stt": {}, "embedding": {},
}

func Validate(raw json.RawMessage) (json.RawMessage, []Warning, *ValidationError) {
    var v any
    if err := json.Unmarshal(raw, &v); err != nil {
        return nil, nil, &ValidationError{
            Path: "$", Code: "schema-violation",
            Message: "not valid JSON: " + err.Error(),
        }
    }
    if err := compiled.Validate(v); err != nil {
        // Walk the error tree → first hard violation.
        return nil, nil, asValidationError(err)
    }

    obj, _ := v.(map[string]any)
    var warnings []Warning
    for k := range obj {
        if _, ok := known[k]; !ok {
            warnings = append(warnings, Warning{
                Path: "$." + k, Code: "unknown-key",
                Message: fmt.Sprintf("unknown key %q preserved verbatim", k),
            })
        }
    }
    return raw, warnings, nil
}
```

### 5.2 Python validator (excerpt)

```python
# pipeline/src/maktaba_pipeline/config/library.py
from __future__ import annotations

import json
from importlib.resources import files
from typing import Any
from uuid import UUID

from jsonschema import Draft202012Validator
from jsonschema.exceptions import best_match

from .defaults import BUILTIN_DEFAULTS

_SCHEMA = json.loads(
    files("maktaba_pipeline").joinpath(
        "../../shared/schema/library_settings.schema.json"
    ).read_text()
)
_VALIDATOR = Draft202012Validator(_SCHEMA)

_KNOWN = {
    "language", "multi_audio", "diarize", "chapter_inference",
    "auto_tag_topics", "default_subtitle_lang", "ignore_globs",
    "sweep_interval_sec", "stt", "embedding",
}


class ValidationError(ValueError):
    def __init__(self, path: str, code: str, message: str) -> None:
        super().__init__(f"{path}: {message}")
        self.path, self.code, self.message = path, code, message


def validate(raw: dict[str, Any]) -> tuple[dict[str, Any], list[Warning]]:
    err = best_match(_VALIDATOR.iter_errors(raw))
    if err is not None:
        path = "$" + "".join(f"[{p!r}]" for p in err.absolute_path)
        raise ValidationError(path, "schema-violation", err.message)

    warnings = [
        Warning(path=f"$.{k}", code="unknown-key",
                message=f"unknown key {k!r} preserved verbatim")
        for k in raw if k not in _KNOWN
    ]
    return raw, warnings


def _deep_merge(base: dict[str, Any], top: dict[str, Any]) -> dict[str, Any]:
    out = dict(base)
    for k, v in top.items():
        if isinstance(v, dict) and isinstance(out.get(k), dict):
            out[k] = _deep_merge(out[k], v)
        else:
            out[k] = v
    return out


def merge_with_defaults(per_library: dict[str, Any],
                        operator: dict[str, Any]) -> EffectiveLibrarySettings:
    merged = _deep_merge(_deep_merge(BUILTIN_DEFAULTS, operator), per_library)
    unknown = {k: v for k, v in merged.items() if k not in _KNOWN}
    return EffectiveLibrarySettings(
        language=merged["language"],
        multi_audio=merged["multi_audio"],
        stt=STTSettings(**merged["stt"]),
        embedding=EmbeddingSettings(**merged["embedding"]),
        diarize=merged["diarize"],
        chapter_inference=merged["chapter_inference"],
        auto_tag_topics=merged["auto_tag_topics"],
        default_subtitle_lang=merged["default_subtitle_lang"],
        ignore_globs=tuple(merged["ignore_globs"]),
        sweep_interval_sec=merged["sweep_interval_sec"],
        unknown=unknown,
    )
```

### 5.3 Built-in defaults — single source per language, parity-tested

```python
# pipeline/src/maktaba_pipeline/config/defaults.py
BUILTIN_DEFAULTS: dict = {
    "language": "auto",
    "multi_audio": False,
    "diarize": False,
    "chapter_inference": True,
    "auto_tag_topics": True,
    "default_subtitle_lang": "en",
    "ignore_globs": [],
    "sweep_interval_sec": 21600,
    "stt": {
        "backend": "whisper-faster",
        "model":   "large-v3",
        "profile": "balanced",
    },
    "embedding": {
        "model":  "intfloat/multilingual-e5-base",
        "device": "auto",
    },
}
```

```go
// api/internal/libraries/settings_defaults.go
package libraries

var BuiltinDefaults = map[string]any{
    "language": "auto",
    "multi_audio": false,
    "diarize": false,
    "chapter_inference": true,
    "auto_tag_topics": true,
    "default_subtitle_lang": "en",
    "ignore_globs": []string{},
    "sweep_interval_sec": 21600,
    "stt": map[string]any{
        "backend": "whisper-faster",
        "model":   "large-v3",
        "profile": "balanced",
    },
    "embedding": map[string]any{
        "model":  "intfloat/multilingual-e5-base",
        "device": "auto",
    },
}
```

A parity test (`pipeline/tests/config/test_defaults_parity.py`) loads
`api/internal/libraries/settings_defaults.go` via a tiny Go subprocess
that prints the JSON, and asserts byte-equal with the Python dict.
That keeps the two halves honest.

## 6. Test plan

### 6.1 Go validator tests (`api/internal/libraries/settings_test.go`)

| Test | What it pins |
|---|---|
| `TestValidate_AcceptsAllRecognizedKeys_Positive` | Loads `valid_full.json` from the shared fixture dir; no error, no warnings, raw round-trips. |
| `TestValidate_RejectsUnknownEnum_STTBackend` | `stt.backend = "elephant"` → ValidationError with `Path = "$.stt.backend"`, `Code = "unknown-enum"`. |
| `TestValidate_RejectsOutOfRange_SweepInterval` | `sweep_interval_sec = 10000000` → ValidationError with `Code = "out-of-range"`. |
| `TestValidate_AcceptsAutoLanguage` | `language = "auto"` → no error. |
| `TestValidate_RejectsBadISO639` | `language = "ARB"` → ValidationError. |
| `TestValidate_PreservesUnknownKey_AsWarning` | `{"chapter_inference": true, "future_key": 123}` → no error; one warning with `Path = "$.future_key"`, `Code = "unknown-key"`. |
| `TestValidate_RejectsNonObjectRoot` | `[1,2,3]` → ValidationError with `Code = "schema-violation"`, `Path = "$"`. |
| `TestMergeWithDefaults_LayersStt` | Per-library `{stt:{backend:"whisper-mlx"}}` + operator empty → effective.STT.Model == defaults.STT.Model, effective.STT.Backend == "whisper-mlx". |
| `TestMergeWithDefaults_OperatorOverridesDefaults` | Operator `{stt:{model:"medium"}}` and per-library empty → effective.STT.Model == "medium". |
| `TestEffective_TupleVsList_StableJSON` | Marshalling Effective produces stable JSON ordering (used in WS broadcasts). |

### 6.2 Python validator tests (`pipeline/tests/config/test_library_settings.py`)

| Test | What it pins |
|---|---|
| `test_validate_accepts_full_blob` | Same `valid_full.json` fixture as Go; no warnings. |
| `test_validate_rejects_invalid_stt_backend` | Mirrors the Go test; same Path/Code. |
| `test_validate_emits_warning_on_unknown_key` | Same as Go. |
| `test_merge_layers_three_levels` | Built-in + operator + per-library all contribute distinct fields; final Effective reflects all three. |
| `test_effective_unknowns_round_trip` | `{"x_custom": {"a":1}}` survives `merge_with_defaults` in `effective.unknown`. |
| `test_iso639_arabic` | `language="ar"` accepted; `language="ARB"` rejected. |

### 6.3 Integration test: PATCH triggers NOTIFY

`pipeline/tests/config/test_settings_notify.py`:

| Test | What it pins |
|---|---|
| `test_patch_settings_emits_notify_pg` | Listen on `library.settings_changed`; PATCH a library's `stt.model`; expect exactly one notification with `library_id` and `changed_keys = ["stt"]`. |
| `test_patch_settings_no_notify_on_noop` | PATCH with the same value → trigger sees `OLD = NEW` → no notify. |
| `test_patch_settings_pubsub_sqlite` | Same shape over `PubsubBus` in SQLite mode. |
| `test_patch_settings_returns_warnings_for_unknown` | API integration: PATCH body containing `{"future_key": 1}` returns 200 with `warnings: [{path: "$.future_key", code: "unknown-key"}]`. |

### 6.4 Cross-language parity (`shared/schema/test_fixtures/library_settings/`)

A `_parity.py` test runs every fixture file through both validators and
asserts identical (validation-error vs. ok) outcomes. Adds a new fixture
when an edge case is found in either language; both must agree before
merge.

Fixture set (initial):

- `valid_full.json` — every recognized key present.
- `valid_empty.json` — `{}`; passes (defaults supplied later).
- `valid_only_stt_backend.json` — exercises AC-2 inheritance.
- `valid_unknown_key.json` — validates with one warning.
- `invalid_stt_backend.json`
- `invalid_language_caps.json`
- `invalid_sweep_interval_too_large.json`
- `invalid_root_is_array.json`
- `invalid_ignore_globs_too_many.json` — 257 globs, max is 256.

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Unknown key | Stored verbatim in `settings`; `Effective.Unknown` populated; PATCH response includes a warning. | `TestValidate_PreservesUnknownKey_AsWarning` + `test_patch_settings_returns_warnings_for_unknown` |
| Future schema version (forward-compat) | Same as unknown key — no error, blob round-trips. The `additionalProperties: true` on the schema is the mechanism. | `valid_unknown_key.json` parity |
| `language` change after videos exist | Stored; existing videos *not* re-tagged (Story 9.8 covers re-tag triggering). The setting only governs *future* videos. | Documented in Story 9.8 plan §8 |
| PATCH with same value (no-op) | Trigger sees `OLD = NEW` and emits no notify. Saves the WS bus from spurious updates. | `test_patch_settings_no_notify_on_noop` |
| Unknown nested key inside `stt` | Schema sets `additionalProperties: false` for `stt` → ValidationError. Top-level extension is the only "future-compat" surface. | `TestValidate_RejectsUnknownEnum_STTBackend` (variant) |
| Empty per-library blob | `Effective` equals `MergeWithDefaults({}, operator) = operator-merged-defaults`; never crashes. | `valid_empty.json` parity |
| Operator `pipeline.toml` contains an unrecognized key | Logged as a startup WARN; merged through verbatim into `Effective.Unknown`. Operator config is trusted but warned. | Pipeline boot path tests (out of scope for this story; covered by Epic 6 Story 6.9 plan) |
| `ignore_globs` with 257 entries | Schema rejects (`maxItems: 256`); ValidationError. Matches a 1 KiB upper bound on the JSONB row's growth. | `invalid_ignore_globs_too_many.json` parity |
| SQLite missing `json1` extension | Boot-time check in `pipeline/db/__init__.py` prints a clear error and exits; the trigger needs `json_type`. | Documented in `docs/operations/sqlite.md`; not a code path here |

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/santhosh-tekuri/jsonschema/v6` | latest | Mature, maintained, supports Draft 2020-12, no CGO. |
| `jsonschema` (Python) | ≥ 4.21 | Supports Draft 2020-12; widely used. |
| `goose` | already in repo | Migration runner. |
| `Draft202012Validator` from `jsonschema` | included | Same draft as Go side; the parity tests would catch a draft drift. |

No new heavy deps.

## 9. Acceptance checklist

**Schema**
- [ ] `shared/schema/library_settings.schema.json` exists; loads in both languages without error.
- [ ] All 10 recognized keys validate the positive fixtures.
- [ ] Each negative fixture in §6.4 fails identically in both languages.

**Migration**
- [ ] `0030_libraries_settings.sql` adds the column, the CHECK, and the trigger; idempotent.
- [ ] `0030_libraries_settings.sqlite.sql` adds the column and the BEFORE INSERT/UPDATE triggers.

**Code**
- [ ] `api/internal/libraries/settings.go` exposes `Validate`, `MergeWithDefaults`, `EffectiveFor`, `Effective`, `Warning`, `ValidationError`.
- [ ] `pipeline/src/maktaba_pipeline/config/library.py` exposes `validate`, `merge_with_defaults`, `effective_for`, `EffectiveLibrarySettings`.
- [ ] `BUILTIN_DEFAULTS` in Python equals `BuiltinDefaults` in Go (parity test passes).
- [ ] `LIBRARY_SETTINGS_CHANGED` channel constant added to `pipeline/db/pubsub.py`.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: every recognized key validates; unknown keys round-trip with a warning.
- [ ] AC-2: per-library overrides operator overrides built-in defaults; missing keys inherit recursively.
- [ ] AC-3: a settings change emits exactly one `library.settings_changed` NOTIFY with the changed-keys list and `effective_after`.

**Observability**
- [ ] INFO log `library_settings_updated library_id=… changed_keys=… by_user=…` from the Go handler.
- [ ] Counter `maktaba_library_settings_validations_total{outcome=ok|warning|error}` exported.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.1.
- [ ] `docs/api-reference.md` lists every recognized key with type, range, and default; the schema file is the authoritative source.
