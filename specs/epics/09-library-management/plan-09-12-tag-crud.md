# Implementation Plan — Story 9.12 Tag CRUD and Normalization

> Companion to [story-09-12-tag-crud.md](story-09-12-tag-crud.md).
> The story states *what* and *why*; this plan states *how*.
> Owns the schema migration (`tags.display_name` + `normalized_name`)
> and the normalization rules. The HTTP routes live in Epic 7
> Story 7.14.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| ID type | `tags.id BIGSERIAL` (canonical from architecture). The DB auto-assigns; callers never pass an `id` to INSERT. |
| Schema migration | `0040_tags_normalize.sql` — destructive `DROP COLUMN name` and `ADD COLUMN display_name, normalized_name`. Runs the data move (`UPDATE tags SET display_name = name, normalized_name = lower(name)`) inside the same migration; ordering is critical. Tags also gain canonical `name_fold` (= `normalized_name`) and `created_at` per architecture. |
| Normalization | Trim whitespace, NFC-normalize, casefold. Implemented in both Go (`api/internal/tags/normalize.go`) and Python (`pipeline/src/maktaba_pipeline/tags/normalize.py`); fixture parity test ensures same output for every input. |
| Uniqueness | `UNIQUE INDEX tags_normalized_name`. Any insert that would collide is replaced by a SELECT-by-normalized fallback in the upsert helper. |
| Reuse vs. error semantics | Inserting a normalize-equal of an existing tag returns the existing row's id with `outcome='reused'`. Display name is *not* overwritten (preserves the first-seen casing). |
| Rename collision | If a rename's new normalized form collides with another tag, return 409 `tag-name-exists` with the conflicting `tag_id`; the UI offers a "merge" CTA. |
| Out of scope | The HTTP routes themselves (Epic 7 Story 7.14 owns); the merge endpoint (out of scope for this story; future); the audit-log entries on tag operations (Story 9.17). |

## 1. Architecture diagram

```
   API caller (POST /api/tags {display_name})
        ↓
   normalize(display_name)
        = NFC( casefold( strip( s ) ) )
        ↓
   tags.upsert_tag(library_id?, display_name, normalized)
      -- tags.id is BIGSERIAL — DB auto-assigns; we never pass id.
      INSERT INTO tags (display_name, normalized_name, name_fold, created_at)
      VALUES ($1, $2, $2, now())
      ON CONFLICT (normalized_name) DO NOTHING
      RETURNING id;
        ↓ if no row returned (conflict)
      SELECT id FROM tags WHERE normalized_name = $2;
        ↓
      → Tag{ id, display_name (existing), outcome }

   Rename (PATCH /api/tags/{id} {display_name})
        ↓
   new_norm = normalize(display_name)
   if new_norm == cur.normalized_name:
       UPDATE tags SET display_name = $1, updated_at = now() WHERE id = $2
       return 200 OK
   else:
       SELECT id FROM tags WHERE normalized_name = new_norm AND id != $tag_id
       if found: return 409 tag-name-exists
       UPDATE tags SET display_name = $1, normalized_name = $2,
                       updated_at = now()
        WHERE id = $tag_id
       return 200 OK
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/tags/normalize.go` | `Normalize(s string) string`, `NormalizeAndDisplay(s string) (display, norm string)`, error type. |
| `api/internal/tags/store.go` | `UpsertTag`, `RenameTag`, `GetTagByNormalized`. |
| `api/internal/tags/normalize_test.go` | Fixture parity. |
| `pipeline/src/maktaba_pipeline/tags/normalize.py` | Python mirror. |
| `pipeline/tests/tags/test_normalize_parity.py` | Cross-language parity check. |
| `shared/db/migrations/0040_tags_normalize.sql` | The migration. |
| `shared/db/migrations/0040_tags_normalize.sqlite.sql` | SQLite variant. |
| `shared/db/queries/tags.sql` | sqlc input. |
| `shared/db/test_fixtures/tags_normalize/` | Fixture file `cases.json` consumed by both languages. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/handlers/tags/*.go` | (Owned by Epic 7 Story 7.14.) This story adds the helpers; the handlers call them. |
| `specs/epics/09-library-management/README.md` | Tick story 9.12. |

### 2.3 Type definitions

```go
// api/internal/tags/normalize.go
package tags

const (
    MaxDisplayLen = 64
    MaxNormLen    = 64
)

type ValidationError struct {
    Code, Message string
}
```

```go
// api/internal/tags/store.go
type UpsertResult struct {
    ID          int64  // tags.id is BIGSERIAL
    DisplayName string
    Outcome     string // "inserted" | "reused"
}
```

## 3. Database migration

### 3.1 Postgres — `0040_tags_normalize.sql`

```sql
-- +goose Up
-- +goose StatementBegin

-- Architecture canonicalizes tags as
--   (id BIGSERIAL PK, name, name_fold, created_at).
-- This story renames the storage to display_name (= name) and
-- normalized_name (= name_fold) and enforces casefold uniqueness via
-- normalized_name. We keep name_fold as a synonymous canonical column
-- that mirrors normalized_name (kept in sync by triggers/queries).

ALTER TABLE tags
    ADD COLUMN display_name    TEXT,
    ADD COLUMN normalized_name TEXT,
    ADD COLUMN name_fold       TEXT,
    ADD COLUMN created_at      TIMESTAMPTZ NOT NULL DEFAULT now();

-- The Postgres lower() does ASCII casefold; for true Unicode casefold
-- (Turkish dotless i, German ß) we'd need icu or a Python preprocess.
-- AC-2 says NFC + casefold; for the migration we approximate with
-- lower() on existing rows and run a one-shot Python normalize pass
-- that callers (Pipeline) use henceforth. The column is defined for
-- future correctness; the casefold in DB is best-effort.
UPDATE tags SET
    display_name    = TRIM(name),
    normalized_name = lower(TRIM(name)),
    name_fold       = lower(TRIM(name));

ALTER TABLE tags ALTER COLUMN display_name    SET NOT NULL;
ALTER TABLE tags ALTER COLUMN normalized_name SET NOT NULL;
ALTER TABLE tags ALTER COLUMN name_fold       SET NOT NULL;

ALTER TABLE tags
    ADD CONSTRAINT tags_display_name_len_chk
    CHECK (char_length(display_name) BETWEEN 1 AND 64);

ALTER TABLE tags
    ADD CONSTRAINT tags_normalized_name_len_chk
    CHECK (char_length(normalized_name) BETWEEN 1 AND 64);

ALTER TABLE tags DROP COLUMN name;

CREATE UNIQUE INDEX tags_normalized_name
    ON tags (normalized_name);

CREATE INDEX tags_display_lookup
    ON tags (display_name text_pattern_ops);

-- A maintenance step that the Pipeline runs once after deploy:
-- it walks every tag, recomputes normalized_name = NFC + casefold via
-- Python, and updates rows where the result differs from lower(name).
-- The CLI command `maktaba-pipeline tags-renormalize` is shipped here.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS tags_display_lookup;
DROP INDEX IF EXISTS tags_normalized_name;
ALTER TABLE tags DROP CONSTRAINT IF EXISTS tags_normalized_name_len_chk;
ALTER TABLE tags DROP CONSTRAINT IF EXISTS tags_display_name_len_chk;
-- Restore name column from display_name; lossy if names diverged.
ALTER TABLE tags ADD COLUMN name TEXT;
UPDATE tags SET name = display_name;
ALTER TABLE tags ALTER COLUMN name SET NOT NULL;
ALTER TABLE tags DROP COLUMN name_fold;
ALTER TABLE tags DROP COLUMN created_at;
ALTER TABLE tags DROP COLUMN normalized_name;
ALTER TABLE tags DROP COLUMN display_name;
-- +goose StatementEnd
```

### 3.2 SQLite variant

SQLite cannot DROP COLUMN before 3.35 (we pin newer). The variant is
the same `ALTER TABLE` sequence, with `lower()` as the normalize
approximation, plus the same unique index.

### 3.3 sqlc queries (`shared/db/queries/tags.sql`)

```sql
-- tags.id is BIGSERIAL — INSERTs never pass id; the DB auto-assigns.
-- name: InsertTagOnConflictNothing :one
INSERT INTO tags (display_name, normalized_name, name_fold)
VALUES ($1, $2, $2)
ON CONFLICT (normalized_name) DO NOTHING
RETURNING id;

-- name: GetTagByNormalized :one
SELECT id, display_name, normalized_name
  FROM tags WHERE normalized_name = $1;

-- name: GetTagByID :one
SELECT id, display_name, normalized_name
  FROM tags WHERE id = $1;

-- name: RenameTag :exec
UPDATE tags
   SET display_name    = $2,
       normalized_name = $3,
       name_fold       = $3,
       updated_at      = now()
 WHERE id = $1;

-- name: ListTags :many
SELECT id, display_name, normalized_name
  FROM tags
 ORDER BY display_name;
```

## 4. Code scaffolding

### 4.1 Go — `Normalize`

```go
// api/internal/tags/normalize.go
package tags

import (
    "errors"
    "strings"
    "unicode"

    "golang.org/x/text/unicode/norm"
    "golang.org/x/text/cases"
    "golang.org/x/text/language"
)

var foldCaser = cases.Fold()

func NormalizeAndDisplay(raw string) (display, normalized string, err error) {
    display = strings.TrimSpace(raw)
    if display == "" {
        return "", "", &ValidationError{Code: "tag-empty",
            Message: "tag must contain non-whitespace characters"}
    }
    if len(display) > MaxDisplayLen*4 {  // 4 byte upper bound on UTF-8 width
        return "", "", &ValidationError{Code: "tag-too-long",
            Message: "tag exceeds 64 characters"}
    }
    if charLen(display) > MaxDisplayLen {
        return "", "", &ValidationError{Code: "tag-too-long", ...}
    }
    nfc := norm.NFC.String(display)
    normalized = foldCaser.String(nfc)
    return nfc, normalized, nil
}

func charLen(s string) int {
    n := 0
    for range s { n++ }
    return n
}
```

### 4.2 Python — `normalize`

```python
# pipeline/src/maktaba_pipeline/tags/normalize.py
import unicodedata


MAX_DISPLAY_LEN = 64


class TagValidationError(ValueError):
    def __init__(self, code: str, message: str) -> None:
        super().__init__(message)
        self.code, self.message = code, message


def normalize_and_display(raw: str) -> tuple[str, str]:
    display = raw.strip()
    if not display:
        raise TagValidationError("tag-empty",
            "tag must contain non-whitespace characters")
    if len(display) > MAX_DISPLAY_LEN:
        raise TagValidationError("tag-too-long",
            "tag exceeds 64 characters")
    nfc = unicodedata.normalize("NFC", display)
    normalized = nfc.casefold()
    return nfc, normalized
```

### 4.3 Go — `UpsertTag` helper

```go
func UpsertTag(ctx context.Context, q *db.Queries, raw string) (UpsertResult, error) {
    display, norm, err := NormalizeAndDisplay(raw)
    if err != nil { return UpsertResult{}, err }

    // tags.id is BIGSERIAL — DB assigns; we read the assigned id from RETURNING.
    res, err := q.InsertTagOnConflictNothing(ctx, db.InsertTagOnConflictNothingParams{
        DisplayName: display, NormalizedName: norm,
    })
    if err == nil {
        return UpsertResult{ID: res, DisplayName: display, Outcome: "inserted"}, nil
    }
    if !errors.Is(err, pgx.ErrNoRows) {
        return UpsertResult{}, err
    }
    existing, err := q.GetTagByNormalized(ctx, norm)
    if err != nil { return UpsertResult{}, err }
    return UpsertResult{
        ID: existing.ID, DisplayName: existing.DisplayName, Outcome: "reused",
    }, nil
}
```

### 4.4 Go — `RenameTag` helper

```go
func RenameTag(ctx context.Context, q *db.Queries, id int64, raw string) error {
    display, newNorm, err := NormalizeAndDisplay(raw)
    if err != nil { return err }

    cur, err := q.GetTagByID(ctx, id)
    if err != nil { return err }
    if cur.NormalizedName == newNorm {
        return q.RenameTag(ctx, db.RenameTagParams{
            ID: id, DisplayName: display, NormalizedName: newNorm,
        })
    }
    other, err := q.GetTagByNormalized(ctx, newNorm)
    if err == nil && other.ID != id {
        return &ConflictError{Code: "tag-name-exists", ConflictID: other.ID}
    }
    if !errors.Is(err, pgx.ErrNoRows) {
        return err
    }
    return q.RenameTag(ctx, db.RenameTagParams{
        ID: id, DisplayName: display, NormalizedName: newNorm,
    })
}
```

### 4.5 `tags-renormalize` CLI

```python
# pipeline/src/maktaba_pipeline/cli/tags.py — subcommand
@click.command("tags-renormalize")
def tags_renormalize():
    """Walk every tag; recompute normalized_name with NFC+casefold; UPDATE
    rows whose stored normalized_name differs. Idempotent."""
    rows = db.fetch("SELECT id, display_name, normalized_name FROM tags")
    for r in rows:
        _, want = normalize_and_display(r["display_name"])
        if want != r["normalized_name"]:
            try:
                db.execute(
                    "UPDATE tags SET normalized_name=$1, updated_at=now() WHERE id=$2",
                    want, r["id"],
                )
            except UniqueViolation:
                # collision with another row — flag for manual merge
                log.warning("renormalize_collision id=%s want=%s", r["id"], want)
```

## 5. Test plan

### 5.1 Cross-language parity (`shared/db/test_fixtures/tags_normalize/cases.json`)

```json
[
  {"in": "  Tafsir  ",   "display": "Tafsir",   "norm": "tafsir"},
  {"in": "Tafsir",       "display": "Tafsir",   "norm": "tafsir"},
  {"in": "tafsir",       "display": "tafsir",   "norm": "tafsir"},
  {"in": "TAFSIR",       "display": "TAFSIR",   "norm": "tafsir"},
  {"in": "حديث",        "display": "حديث",     "norm": "حديث"},
  {"in": "حَدِيث",       "display": "حَدِيث",   "norm": "حَدِيث"},   // NFC of NFD diacritics
  {"in": "Türk",         "display": "Türk",     "norm": "türk"},
  {"in": "İSTANBUL",     "display": "İSTANBUL", "norm": "i̇stanbul"}, // Turkish-i
  {"in": "ß",            "display": "ß",        "norm": "ss"},        // German sharp-s casefold
  {"in": "finance/2024", "display": "finance/2024", "norm": "finance/2024"},
  {"in": "  ",           "error": "tag-empty"},
  {"in": "x"*65,         "error": "tag-too-long"}
]
```

The Go and Python tests both load this file and assert identical output.
Where the two normalizers diverge (e.g., locale-specific Turkish-i),
the test pins what the platform actually produces — discovered drift
becomes a visible CI failure, not a silent inconsistency.

### 5.2 Go tests (`normalize_test.go`, `store_test.go`)

| Test | What it pins |
|---|---|
| `TestNormalize_FixtureParity` | Loads `cases.json`; for each entry, asserts `display` and `norm` match; for entries with `"error"`, asserts the error code. |
| `TestUpsertTag_FirstInsertReturnsInserted` | Empty table → returns `outcome="inserted"`. |
| `TestUpsertTag_SecondInsertReturnsReused` | Two calls with `"Tafsir"` → second returns `outcome="reused"`, same id; one row in DB. AC-3. |
| `TestUpsertTag_NormalizeEqualReusesIgnoringCasing` | First `"Tafsir"`, second `"tafsir"` → second reused; `display_name` unchanged ("Tafsir"). AC-2 + AC-3. |
| `TestRename_NoCollisionUpdates` | Rename existing tag to a unique new name → 200; row updated. |
| `TestRename_CollisionReturns409` | Rename to an existing tag's normalized form → ConflictError; no DB write. AC-4. |
| `TestRename_SameNormalizedAllowsCaseChange` | Existing display "Tafsir"; PATCH "TAFSIR" → 200; display updated; `normalized_name` unchanged. |
| `TestUpsertTag_EmptyRejected` | `""` or `"   "` → ValidationError `tag-empty`. |
| `TestUpsertTag_TooLong` | 65-char string → ValidationError `tag-too-long`. |
| `TestUpsertTag_SlashAllowed` | `"finance/2024"` accepted; the slash is treated as content (architecture flat tags only). |

### 5.3 Migration tests

`pipeline/tests/db/test_tags_migration.py`:

| Test | What it pins |
|---|---|
| `test_old_name_column_dropped` | After migration, `name` column is gone; `display_name`/`normalized_name` exist. |
| `test_existing_rows_backfilled` | Pre-migration `name='Foo'` → post-migration `display_name='Foo'`, `normalized_name='foo'`. |
| `test_unique_index_present` | `pg_indexes` shows `tags_normalized_name` unique. |
| `test_check_constraints_reject_empty_and_too_long` | INSERT with `display_name=''` or 65-char → CHECK violation. |

### 5.4 `tags-renormalize` test

| Test | What it pins |
|---|---|
| `test_renormalize_idempotent` | Run twice; second run logs "0 updates" and no DB writes. |
| `test_renormalize_handles_collision` | Synthesize two rows whose proper NFC+casefold collide (e.g., German ß and ss); the CLI logs `renormalize_collision`; both rows preserved (no destructive action). |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Whitespace-only input | 422 `tag-empty`. | `TestUpsertTag_EmptyRejected` |
| Slash in name | Accepted; flat string in v1 (no hierarchy). | `TestUpsertTag_SlashAllowed` |
| Arabic with diacritics in NFD | `unicodedata.normalize("NFC", ...)` recombines; `casefold()` is a no-op for Arabic; final norm matches the same word in NFC. | `cases.json` `"حَدِيث"` entry |
| Turkish dotless-i | Casefold is locale-independent in both Go's `cases.Fold()` and Python's `casefold()`; the visible result includes the dot-above sequence (`İ` → `i̇`). The test pins this exactly. | `cases.json` Turkish entry |
| German sharp-s | Casefold expands `ß` → `ss`. Pinned. | `cases.json` |
| Rename collision | 409 `tag-name-exists` with `conflict_tag_id` payload; UI surfaces "Merge with existing?". | `TestRename_CollisionReturns409` |
| Display-name change without normalize change | UPDATE only `display_name`; `normalized_name` unchanged → no unique-index check. | `TestRename_SameNormalizedAllowsCaseChange` |
| Pre-migration `name` had whitespace edges | TRIMmed in the migration; round-trip lossless. | `test_existing_rows_backfilled` (extended) |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `MaxDisplayLen` (constant) | 64 | Length cap; matches schema CHECK. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `golang.org/x/text/unicode/norm` | stdlib-adjacent | NFC. |
| `golang.org/x/text/cases` | stdlib-adjacent | Unicode casefold. |
| Python `unicodedata` | stdlib | NFC + casefold. |

## 9. Acceptance checklist

**Migration**
- [ ] `0040_tags_normalize.sql` adds `display_name`, `normalized_name`, drops `name`, adds CHECKs, adds UNIQUE on `normalized_name`. Existing rows backfilled.

**Code**
- [ ] `api/internal/tags/normalize.go` and `pipeline/src/maktaba_pipeline/tags/normalize.py` produce identical output for the fixture cases.
- [ ] `tags-renormalize` CLI subcommand exists and is idempotent.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: schema migration applies cleanly.
- [ ] AC-2: insert of `"  Tafsir  "` stores `"Tafsir"` and `"tafsir"`.
- [ ] AC-3: re-insert `"tafsir"` reuses the row; display unchanged.
- [ ] AC-4: rename collision returns 409 with the conflicting id.

**Observability**
- [ ] Counter `tags_upsert_total{outcome=inserted|reused}`.
- [ ] Counter `tags_rename_total{outcome=ok|collision}`.
- [ ] Counter `tags_renormalize_collision_total` (incremented by the CLI).

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.12.
- [ ] API reference documents the normalization rule and the `409 tag-name-exists` shape.
