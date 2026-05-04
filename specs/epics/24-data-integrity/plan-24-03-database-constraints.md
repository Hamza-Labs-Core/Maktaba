# Implementation Plan — Story 24.3 Database consistency and constraints

> Companion to [story-24-03-database-constraints.md](story-24-03-database-constraints.md).
> Story states *what* and *why*; this plan states *how*.
> Schema scope and migrations defined per
> [Story 22.4](../22-devops/plan-22-04-database-migrations.md).
>
> **Owns schema canonicalization** (per `PLAN_REVIEW_18_24.md` §1.1, §1.2).
> This plan ships migration `0050_schema_canonicalization.sql` that
> reconciles the column-name and state-casing drift inherited from
> Epics 07–13 against architecture §8.1 / §7.2. Every other plan in this
> batch (24-02, 24-04, 24-06, 24-07, 24-08) consumes the canonical names
> rendered here.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Constraint inventory | Catalogued in `shared/db/constraints.md`; one row per FK / unique / check; CI lint compares against actual schema. |
| State enum | `videos.state` and `processing_jobs.state` use SQL CHECK constraints; the enum values come from `shared/db/states.yaml` (single source of truth, see §2.9). Values are **lowercase** matching architecture §7.2. |
| Schema canonicalization | Migration `0050_schema_canonicalization.sql` renames drifted columns/tables to their canonical names (architecture §8.1) and pins lowercase state CHECKs. See §2.10. |
| Soft delete | `deleted_at TIMESTAMPTZ NULL` with a partial unique index on `(library_id, path) WHERE deleted_at IS NULL`. |
| SQLite parity | `PRAGMA foreign_keys = ON` set on every connection; absence detected by a startup probe that fails fast. |
| Out of scope | Migration discipline (22.4); per-Epic schemas (Epics 1–10); state machine logic itself (architecture §3 / §7.2). |

## 1. Architecture diagram

```
┌────────────────────────────┐
│ shared/db/constraints.md   │ ◄── inventory
└────────────┬───────────────┘
             │ tools/constraint-lint compares to live schema
             ▼
   migrations apply →   Postgres / SQLite
                            │
                            ▼
   FK CASCADE / RESTRICT, UNIQUE, CHECK enforced by DB
                            │
                            ▼
   app code maps DB errors → typed Go errors → problem+json
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `shared/db/constraints.md` | Inventory: every FK / unique / check, with rationale. |
| `tools/constraint-lint.go` | Compares inventory to live schema; flags drift. |
| `api/internal/db/errors.go` | Maps pgx error codes to typed Go errors (`ErrUnique`, `ErrFkViolation`, `ErrCheckViolation`). |
| `pipeline/src/maktaba_pipeline/db/errors.py` | Same for asyncpg. |
| `shared/db/states.yaml` | **Single source of truth** for `videos.state` and `processing_jobs.state` enums. Architecture §3 / §7.2 references this file; the migration in §2.10 reads it; the lint reads it; tests load it. |
| `shared/db/migrations/0050_schema_canonicalization.sql` (+ sqlite) | **Canonicalizes column/table names and pins lowercase state CHECKs.** See §2.10. |
| `shared/db/migrations/0051_constraints.sql` (+ sqlite) | Adds the system-wide FK / UNIQUE constraints catalogued in §2.3. |
| `api/internal/db/sqlite_pragma.go` | Connection setup that asserts `PRAGMA foreign_keys=ON`. |
| Tests — `tests/integration/constraints_*.py`, `_test.go` per Go file. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/db/conn.go` | On SQLite, exec `PRAGMA foreign_keys=ON` + assertion. |
| `pipeline/src/maktaba_pipeline/db/conn.py` | Same on the asyncpg/aiosqlite side. |

### 2.3 Constraint inventory

`shared/db/constraints.md` (excerpt):

```
| Table | Constraint | Type | Action | Rationale |
|---|---|---|---|---|
| videos | content_hash | UNIQUE | n/a | Identity (Story 24.8). Global per architecture §8.1. |
| videos | (library_id, path) WHERE deleted_at IS NULL | partial UNIQUE | n/a | A live path appears once per library; soft-deleted rows excluded. See §2.8. |
| videos | state IN ('discovered','probed','audio_extracted','transcribed','subtitle_gen','indexed','thumbnailed','ready','failed','ready_no_audio','missing','superseded','corrupted') | CHECK | reject | Enum hygiene; lowercase per architecture §7.2 + extension states from `shared/db/states.yaml`. |
| videos | library_id → libraries(id) | FK | RESTRICT | Hard delete a library only via the gc path (Story 9.15). |
| videos | superseded_by → videos(id) | FK | SET NULL | Modify-in-place chain (Story 24.8). New column added by `0050_schema_canonicalization.sql`; documented in arch §8.7 as plan-introduced extension. |
| videos | deleted_at | column | n/a | Soft-delete marker (architecture §8.1; confirmed slot). |
| transcript_segments | (transcript_id, seq) | UNIQUE | n/a | Canonical per architecture lines 1410–1411. Note: table name is `transcript_segments` (canonical) — older drafts called it `segments`. |
| transcript_segments | transcript_id → transcripts(id) | FK | CASCADE | Segments owned by the transcript. |
| processing_jobs_segments | (video_id, segment_idx) | UNIQUE | n/a | Resume idempotency for plan-24-02's per-segment commit (helper table, not `processing_jobs` itself). Distinct concern from `transcript_segments(transcript_id, seq)`: this enforces "one job-segment commit per (video, segment)", not "one transcript row per (transcript, seq)". |
| transcripts | (video_id, audio_track_id, backend, model) WHERE is_active | partial UNIQUE | n/a | History rows for re-runs; current per (track, backend, model). |
| transcripts | superseded_at, detected_language, language_confidence | columns | n/a | Per architecture §8.1 (confirmed slots). |
| processing_jobs | state IN ('pending','claimed','running','paused','resuming','done','failed','cancelled') | CHECK | reject | Lowercase per architecture §7.2 + Story 24.2's `paused`/`resuming` transitions. |
| users | username (lower) | UNIQUE | n/a | Owned by Story 10.1. |
| library_acl | (user_id, library_id) | UNIQUE | n/a | One role per pair. |
| library_acl | user_id → users(id) | FK | CASCADE | ACL gone when user is gone. |
| library_acl | library_id → libraries(id) | FK | CASCADE | ACL gone when library is gone. |
```

The full markdown table runs ~80 rows; the lint reads it.

**Canonical column names confirmed (architecture §8.1):**
`videos.size_bytes` (not `size`), `videos.duration_sec` (not `duration_s`),
`videos.mtime` is `TIMESTAMPTZ` (not integer-ns).
**Canonical FTS table:** `transcripts_fts` (not `segments_fts`).

### 2.4 The lint

`tools/constraint-lint.go`:

```go
// Reads shared/db/constraints.md; introspects a live DB
// (`information_schema` for Postgres; `sqlite_master` for SQLite);
// reports drift in either direction:
//   - inventory entry without matching DB constraint
//   - DB constraint without inventory entry
//
// Run in CI's lint gate against an in-memory SQLite seeded from all
// migrations + against an ephemeral Postgres container.

func main() {
    pg := openPg()
    inv := parseInventory("shared/db/constraints.md")
    found := introspectPg(pg)
    for _, want := range inv {
        if !found.HasMatching(want) {
            log.Fatalf("inventory %s missing in DB", want)
        }
    }
    for _, got := range found {
        if !inv.HasMatching(got) {
            log.Fatalf("DB has %s but no inventory entry — document or remove", got)
        }
    }
}
```

### 2.5 Error mapping

`api/internal/db/errors.go`:

```go
import (
    "errors"
    "github.com/jackc/pgerrcode"
    "github.com/jackc/pgx/v5/pgconn"
)

var (
    ErrUnique         = errors.New("db: unique violation")
    ErrFkViolation    = errors.New("db: fk violation")
    ErrCheckViolation = errors.New("db: check violation")
    ErrStaleTransition = errors.New("db: stale state transition")
)

type ConstraintError struct {
    Kind     error  // one of the above
    Table    string
    Column   string
    Message  string
}

func Wrap(err error) error {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) {
        return mapPg(pgErr)
    }
    var sqliteErr *sqliteError
    if errors.As(err, &sqliteErr) {
        return mapSqlite(sqliteErr)
    }
    return err
}

func mapPg(e *pgconn.PgError) error {
    switch e.Code {
    case pgerrcode.UniqueViolation:
        return &ConstraintError{Kind: ErrUnique, Table: e.TableName, Column: e.ColumnName, Message: e.Detail}
    case pgerrcode.ForeignKeyViolation:
        return &ConstraintError{Kind: ErrFkViolation, Table: e.TableName, Message: e.Detail}
    case pgerrcode.CheckViolation:
        return &ConstraintError{Kind: ErrCheckViolation, Table: e.TableName, Message: e.Detail}
    }
    return e
}
```

Handlers use `errors.As` to render `problem+json` with the right
`type` URI.

### 2.6 SQLite pragma assertion

`api/internal/db/sqlite_pragma.go`:

```go
func setupSqlite(ctx context.Context, conn *sql.Conn) error {
    if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
        return err
    }
    var v int
    if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&v); err != nil {
        return err
    }
    if v != 1 {
        return errors.New("sqlite foreign_keys pragma did not stick — refusing to start")
    }
    return nil
}
```

The connection pool wires this as a `ConnInitFunc` so every connection
runs the pragma on borrow (SQLite's pragma is per-connection).

### 2.7 State-machine guard

State transitions use:

```sql
UPDATE videos
SET state = $new_state, updated_at = now()
WHERE id = $1 AND state = $expected_state
```

Returning rows-affected = 0 → `ErrStaleTransition`. Combined with the
`CHECK (state IN (...))` on the column, the database refuses both
unknown states and stale transitions.

### 2.8 Soft-delete pattern

For tables that soft-delete (e.g., `videos`, per architecture):

```sql
ALTER TABLE videos ADD COLUMN deleted_at TIMESTAMPTZ NULL;

CREATE UNIQUE INDEX videos_path_active_unique
    ON videos (library_id, path)
    WHERE deleted_at IS NULL;
```

Hard delete is restricted to the GC path:

```sql
-- name: GcDeleteVideos :exec
DELETE FROM videos
WHERE deleted_at IS NOT NULL
  AND deleted_at < now() - INTERVAL '30 days';
```

Reads filter `WHERE deleted_at IS NULL` by default; the GC view
excludes rows by definition.

### 2.9 `shared/db/states.yaml` — single source of truth

Architecture §3 (FSM) and §7.2 (job state machine) both reference this
file. The migration in §2.10 reads it; the lint reads it; tests load it.
There is no prose parsing of `architecture.md`.

```yaml
# shared/db/states.yaml
videos:
  states:
    - discovered
    - probed
    - audio_extracted
    - transcribed
    - subtitle_gen
    - indexed
    - thumbnailed
    - ready
    - failed
    # Extension states introduced by Epic 24 / earlier epics:
    - ready_no_audio   # audio extraction skipped (silent video)
    - missing          # file present in DB, gone on disk
    - superseded       # modify-in-place chain (Story 24.8)
    - corrupted        # content_hash mismatch (Story 24.6)

processing_jobs:
  states:
    - pending
    - claimed
    - running
    - paused
    - resuming
    - done
    - failed
    - cancelled
```

### 2.10 Schema-canonicalization migration

`shared/db/migrations/0050_schema_canonicalization.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- 1. Column renames to architecture-canonical names. Each is wrapped in
--    a defensive check: if the canonical column already exists, the
--    rename is a no-op.

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='videos' AND column_name='size'
                 AND NOT EXISTS (SELECT 1 FROM information_schema.columns
                                 WHERE table_name='videos' AND column_name='size_bytes')) THEN
        ALTER TABLE videos RENAME COLUMN size TO size_bytes;
    END IF;

    IF EXISTS (SELECT 1 FROM information_schema.columns
               WHERE table_name='videos' AND column_name='duration_s'
                 AND NOT EXISTS (SELECT 1 FROM information_schema.columns
                                 WHERE table_name='videos' AND column_name='duration_sec')) THEN
        ALTER TABLE videos RENAME COLUMN duration_s TO duration_sec;
    END IF;
END$$;

-- 2. Table rename: segments -> transcript_segments (canonical per
--    architecture lines 1410-1411). No-op if `transcript_segments`
--    already exists (earlier plans may have created the canonical name
--    directly).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.tables
               WHERE table_name='segments'
                 AND NOT EXISTS (SELECT 1 FROM information_schema.tables
                                 WHERE table_name='transcript_segments')) THEN
        ALTER TABLE segments RENAME TO transcript_segments;
    END IF;
END$$;

-- 3. videos.deleted_at slot (architecture §8.1; idempotent).
ALTER TABLE videos ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ NULL;

-- 4. videos.superseded_by — plan-introduced extension (cross-ref arch §8.7).
ALTER TABLE videos ADD COLUMN IF NOT EXISTS superseded_by UUID NULL
    REFERENCES videos(id) ON DELETE SET NULL;

-- 5. transcripts extension columns (architecture §8.1; idempotent).
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS superseded_at TIMESTAMPTZ NULL;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS detected_language TEXT NULL;
ALTER TABLE transcripts ADD COLUMN IF NOT EXISTS language_confidence REAL NULL;

-- 6. Pin the videos.state CHECK to the lowercase canonical enum.
--    Drop any existing CHECK constraint on `state` first.
DO $$
DECLARE
    cn TEXT;
BEGIN
    FOR cn IN
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'videos'::regclass
           AND contype  = 'c'
           AND pg_get_constraintdef(oid) ILIKE '%state%'
    LOOP
        EXECUTE 'ALTER TABLE videos DROP CONSTRAINT ' || quote_ident(cn);
    END LOOP;
END$$;

ALTER TABLE videos ADD CONSTRAINT videos_state_check
    CHECK (state IN (
        'discovered','probed','audio_extracted','transcribed',
        'subtitle_gen','indexed','thumbnailed','ready','failed',
        'ready_no_audio','missing','superseded','corrupted'));

-- 7. Pin processing_jobs.state CHECK.
DO $$
DECLARE cn TEXT;
BEGIN
    FOR cn IN
        SELECT conname FROM pg_constraint
         WHERE conrelid = 'processing_jobs'::regclass
           AND contype  = 'c'
           AND pg_get_constraintdef(oid) ILIKE '%state%'
    LOOP
        EXECUTE 'ALTER TABLE processing_jobs DROP CONSTRAINT ' || quote_ident(cn);
    END LOOP;
END$$;

ALTER TABLE processing_jobs ADD CONSTRAINT processing_jobs_state_check
    CHECK (state IN (
        'pending','claimed','running','paused','resuming',
        'done','failed','cancelled'));

-- +goose StatementEnd
```

**Note on slot.** This migration uses slot `0050` per arch §8.7's
manifest; if an existing `0050_constraints.sql` predates this work, it
is renumbered to `0051` (see §2.1). Plan-22-04's CI gate (boot the full
migration set against an empty DB) catches drift.

## 3. Test plan

### 3.1 FK enforcement (TC1)

| Test | What it pins |
|---|---|
| `TestDeleteLibraryCascadesToVideos` | Insert library + 5 videos + 50 transcript_segments; delete the library; videos and transcript_segments rows go to zero. |
| `TestVideoFKRestrictDoesntLeak` | Attempt to delete a library while a `processing_job.state='running'` references one of its videos; the delete fails or cascades by design — test pins the documented behavior (CASCADE for transcript_segments, RESTRICT for in-flight jobs). |
| `TestSqliteForeignKeysOn` | Force-disable the pragma; the bootstrap probe fails; the service refuses to start. |

### 3.2 Unique violation (TC2)

| Test | What it pins |
|---|---|
| `TestContentHashUnique` | Two parallel INSERTs with same hash — one succeeds, one fails with `ErrUnique`; `ConstraintError.Column == "content_hash"`. |
| `TestPartialUniqueOnIsActive` | Two `transcripts` rows with same `(video_id, audio_track_id, backend, model)` and `is_active=false` are allowed; flipping a second row to `is_active=true` fails. |
| `TestProcessingJobsSegmentIdxUnique` | Re-insert into `processing_jobs_segments` with `(video_id, segment_idx=10)` after the first → `ErrUnique`. (Plan-24-02's idempotency unique constraint on the helper table.) |
| `TestTranscriptSegmentsSeqUnique` | Re-insert `transcript_segments(transcript_id, seq=10)` → `ErrUnique`. (Architecture-canonical unique.) |

### 3.3 State enum (TC3)

| Test | What it pins |
|---|---|
| `TestInvalidStateRejected` | `UPDATE videos SET state='unknown'` fails the CHECK; the error is mapped to `ErrCheckViolation`. |
| `TestStateEnumMatchesStatesYaml` | A test loads `shared/db/states.yaml` and asserts each CHECK constraint (`videos.state`, `processing_jobs.state`) contains exactly the values listed there. The YAML file is the single source of truth referenced by architecture §3 / §7.2; no prose parsing. |
| `TestStaleTransitionGuard` | Two writers attempt `running -> done`; one wins, the other returns `ErrStaleTransition`. |

### 3.4 Lint

| Test | What it pins |
|---|---|
| `TestConstraintLintCatchesDrift` | A fixture migration adds a CHECK that's not in `constraints.md` → lint fails. Removing one from the markdown also fails. |
| `TestConstraintLintAcceptsAlignment` | Clean tree → exit 0. |

## 4. Edge cases

| Case | Behaviour | Where pinned |
|---|---|---|
| SQLite missing FK enforcement (EC1) | Bootstrap probe asserts `PRAGMA foreign_keys`; service refuses to start. | `TestSqliteForeignKeysOn` |
| Concurrent state transitions (EC2) | `UPDATE ... WHERE state = expected_prev` makes the loser see 0 rows affected; mapped to `ErrStaleTransition` (409 with `type: stale-transition`). | `TestStaleTransitionGuard` |
| NOT NULL on populated column (EC3) | Documented playbook in 22.4 (ship nullable → backfill → set NOT NULL). The lint enforces this is not done in a single migration. | n/a (22.4) |
| Cascading delete touching very large child set | `DELETE FROM library` with 1 M videos: cascade is performed in one tx and may take minutes. The doctor's row-count warning (Story 22.6) surfaces this. | `TestLargeCascadeReported` |
| FK with `ON DELETE NO ACTION` | Reserved for cross-aggregate refs that should never cascade. The inventory documents `RESTRICT` vs `NO ACTION` (we standardize on RESTRICT for clarity). | n/a |
| SQLite check constraint syntax differences | The migration parity helper from 22.4 verifies SQLite uses the same enum string list. | `TestSqliteCheckParity` |
| Postgres column-name in error vs SQLite | `ConstraintError.Column` may be empty on SQLite (its error format doesn't always carry the column). The mapping is best-effort; tests pin the Postgres path. | `TestConstraintErrorColumnPgOnly` |
| Migration that adds a CHECK to existing data | The lint runs against fresh DBs only; migrations that add CHECKs against existing data must include a backfill or NOT VALID + VALIDATE pattern, documented in 22.4. | `TestAddCheckRequiresValid` |
| Soft-delete row resurrection | Re-inserting a `(library_id, path)` whose previous row is soft-deleted succeeds because the partial unique index excludes deleted rows. The new row gets a fresh primary key. | `TestSoftDeleteResurrection` |
| `is_active` flips race | Updating `is_active` flag races: serialize via `UPDATE … SET is_active=true WHERE id=$1; UPDATE … SET is_active=false WHERE video_id=$2 AND id != $1` in a tx; the partial unique index protects atomicity if both writers commit. | `TestIsActiveFlipRace` |

## 5. Dependencies

| Dep | Version | Why |
|---|---|---|
| `github.com/jackc/pgx/v5` | already | Postgres driver + error codes. |
| `github.com/mattn/go-sqlite3` | already | SQLite driver. |
| `pgerrcode` | latest | SQLSTATE constants. |

## 6. Acceptance checklist

**Inventory**
- [ ] `shared/db/constraints.md` exists; covers all FK/unique/check.
- [ ] `tools/constraint-lint` runs in CI lint and asserts both directions.
- [ ] `shared/db/states.yaml` exists; arch §3 / §7.2 reference it; tests load it.

**Constraints**
- [ ] FKs declared with explicit `ON DELETE`.
- [ ] Unique constraints enforce all listed business invariants.
- [ ] CHECK constraints enforce `videos.state` and `processing_jobs.state` (lowercase).
- [ ] Migration `0050_schema_canonicalization.sql` renames drifted columns/tables and pins lowercase state CHECKs.
- [ ] `videos.superseded_by`, `videos.deleted_at`, `transcripts.superseded_at`, `transcripts.detected_language`, `transcripts.language_confidence` slots exist.
- [ ] `processing_jobs_segments(video_id, segment_idx)` UNIQUE present (plan-24-02 idempotency helper table).
- [ ] `transcript_segments(transcript_id, seq)` UNIQUE present (architecture canonical).

**Soft delete**
- [ ] `deleted_at` columns where called for; partial unique index where applicable.
- [ ] GC path is the only hard-delete route.

**SQLite**
- [ ] `PRAGMA foreign_keys=ON` on every connection.
- [ ] Bootstrap probe refuses startup if the pragma doesn't stick.

**Errors**
- [ ] Constraint errors mapped to typed Go/Python errors.
- [ ] Handlers render appropriate `problem+json` types.
