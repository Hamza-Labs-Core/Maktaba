# Implementation Plan — Story 24.3 Database consistency and constraints

> Companion to [story-24-03-database-constraints.md](story-24-03-database-constraints.md).
> Story states *what* and *why*; this plan states *how*.
> Schema scope and migrations defined per
> [Story 22.4](../22-devops/plan-22-04-database-migrations.md).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Constraint inventory | Catalogued in `shared/db/constraints.md`; one row per FK / unique / check; CI lint compares against actual schema. |
| State enum | `videos.state` and `processing_jobs.state` use SQL CHECK constraints; the enum values come from architecture §3. |
| Soft delete | `deleted_at TIMESTAMPTZ NULL` with a partial unique index on `(<business_key>) WHERE deleted_at IS NULL`. |
| SQLite parity | `PRAGMA foreign_keys = ON` set on every connection; absence detected by a startup probe that fails fast. |
| Out of scope | Migration discipline (22.4); per-Epic schemas (Epics 1–10); state machine logic itself (architecture §3). |

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
| `shared/db/migrations/0050_constraints.sql` (+ sqlite) | Adds the system-wide constraints not owned by a specific Epic; e.g., the `state` CHECK with the full enum from architecture §3. |
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
| videos | (library_id, video_id) | UNIQUE | n/a | A video appears once per library. |
| videos | state IN ('DISCOVERED', ..., 'FAILED') | CHECK | reject | Enum hygiene; full list per architecture §3. |
| videos | library_id → libraries(id) | FK | RESTRICT | Hard delete a library only via the gc path (Story 9.15). |
| segments | (video_id, segment_idx) | UNIQUE | n/a | Resume idempotency (Story 24.2). |
| segments | video_id → videos(id) | FK | CASCADE | Segments are owned by the video. |
| transcripts | (video_id, audio_track_id, backend, model) WHERE is_active | partial UNIQUE | n/a | History rows for re-runs; current per (track, backend, model). |
| processing_jobs | state IN ('QUEUED', ..., 'DONE') | CHECK | reject | Enum hygiene. |
| users | username (lower) | UNIQUE | n/a | Owned by Story 10.1. |
| library_acl | (user_id, library_id) | UNIQUE | n/a | One role per pair. |
| library_acl | user_id → users(id) | FK | CASCADE | ACL gone when user is gone. |
| library_acl | library_id → libraries(id) | FK | CASCADE | ACL gone when library is gone. |
```

The full markdown table runs ~80 rows; the lint reads it.

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

## 3. Test plan

### 3.1 FK enforcement (TC1)

| Test | What it pins |
|---|---|
| `TestDeleteLibraryCascadesToVideos` | Insert library + 5 videos + 50 segments; delete the library; videos and segments rows go to zero. |
| `TestVideoFKRestrictDoesntLeak` | Attempt to delete a library while a `processing_job.state='RUNNING'` references one of its videos; the delete fails or cascades by design — test pins the documented behavior (CASCADE for segments, RESTRICT for in-flight jobs). |
| `TestSqliteForeignKeysOn` | Force-disable the pragma; the bootstrap probe fails; the service refuses to start. |

### 3.2 Unique violation (TC2)

| Test | What it pins |
|---|---|
| `TestContentHashUnique` | Two parallel INSERTs with same hash — one succeeds, one fails with `ErrUnique`; `ConstraintError.Column == "content_hash"`. |
| `TestPartialUniqueOnIsActive` | Two `transcripts` rows with same `(video_id, audio_track_id, backend, model)` and `is_active=false` are allowed; flipping a second row to `is_active=true` fails. |
| `TestSegmentIdxUnique` | Re-insert `(job_id, segment_idx=10)` after the first → `ErrUnique`. |

### 3.3 State enum (TC3)

| Test | What it pins |
|---|---|
| `TestInvalidStateRejected` | `UPDATE videos SET state='unknown'` fails the CHECK; the error is mapped to `ErrCheckViolation`. |
| `TestEnumStringsMatchArchitecture` | A test reads architecture.md §3 and asserts the CHECK constraint contains exactly those values (parses the architecture's enum block). |
| `TestStaleTransitionGuard` | Two writers attempt `RUNNING -> DONE`; one wins, the other returns `ErrStaleTransition`. |

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

**Constraints**
- [ ] FKs declared with explicit `ON DELETE`.
- [ ] Unique constraints enforce all listed business invariants.
- [ ] CHECK constraints enforce `videos.state` and `processing_jobs.state`.

**Soft delete**
- [ ] `deleted_at` columns where called for; partial unique index where applicable.
- [ ] GC path is the only hard-delete route.

**SQLite**
- [ ] `PRAGMA foreign_keys=ON` on every connection.
- [ ] Bootstrap probe refuses startup if the pragma doesn't stick.

**Errors**
- [ ] Constraint errors mapped to typed Go/Python errors.
- [ ] Handlers render appropriate `problem+json` types.
