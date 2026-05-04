# Implementation Plan — Story 9.13 Collections (Manual, Ordered)

> Companion to [story-09-13-collections-manual.md](story-09-13-collections-manual.md).
> The story states *what* and *why*; this plan states *how*.
> Owns the `collection_items` ordering primitive (sparse positions
> `+10`); the HTTP routes live in Epic 7 Story 7.14.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Position type | `INTEGER` (32-bit signed). Sparse increments of 10 give 2.1M slots before exhaustion; combined with the on-demand `compact-collections` CLI, this is plenty for the lifetime of any reasonable user collection. |
| Tail insert | "Append" returns `MAX(position) + 10` in a `SELECT … FOR UPDATE`-protected window inside the insert transaction. |
| Mid insert | Caller passes explicit `position`; we accept any integer the caller chooses (UI computes a midpoint between two existing positions). |
| Compaction | A single CLI command `maktaba-api compact-collections` per Epic 22 cron OR on demand. Online: the renumber happens in transactions per collection, no global lock. |
| Duplicate prevention | Primary key on `(collection_id, video_id)` — a video can only appear once in a collection. |
| `is_smart=true` rejection | Inserts and reorders against a smart collection return 409 `collection-is-smart`; smart collections are read-only as items, except via Story 9.14's freeze. |
| Out of scope | The HTTP routes (Epic 7 Story 7.14); cycle prevention (collections are flat — not applicable); the `collections` table itself (architecture §8.2). |

## 1. Architecture diagram

```
   POST /api/collections/{id}/items {video_id, position?}
        ↓
   tags.insert_item(collection_id, video_id, position?)
      BEGIN TX
         IF position is null:
             SELECT COALESCE(MAX(position), 0) + 10 FROM collection_items
              WHERE collection_id = $1
              FOR UPDATE                       -- protects the window
         INSERT INTO collection_items (collection_id, video_id, position)
         VALUES ($1, $2, $3)
         ON CONFLICT (collection_id, video_id) DO NOTHING
         RETURNING (xmax = 0) AS inserted, position
      COMMIT
        ↓
   if not inserted: 409 already-in-collection

   PATCH /api/collections/{id}/items/{video_id} {position}
        ↓
   UPDATE collection_items
      SET position = $3, updated_at = now()
    WHERE collection_id = $1 AND video_id = $2

   GET /api/collections/{id}/items?cursor=...
        ↓
   SELECT video_id, position
     FROM collection_items
    WHERE collection_id = $1
      AND position > $cursor
    ORDER BY position
    LIMIT $page_size

   maktaba-api compact-collections
        ↓ for each collection:
   BEGIN TX
     WITH ordered AS (
       SELECT video_id, ROW_NUMBER() OVER (ORDER BY position) AS rn
         FROM collection_items WHERE collection_id = $1
     )
     UPDATE collection_items ci
        SET position = o.rn * 10
       FROM ordered o
       WHERE ci.collection_id = $1 AND ci.video_id = o.video_id
   COMMIT
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/collections/store.go` | `InsertItem`, `MoveItem`, `RemoveItem`, `ListItems`, `Compact`. |
| `api/internal/collections/store_test.go` | Unit tests per §6.1. |
| `api/cmd/maktaba-api/compact_collections.go` | CLI subcommand. |
| `shared/db/migrations/0041_collection_items.sql` | Adds the `collection_items` table (architecture §8.2 sketches it; this migration adds the position constraints). |
| `shared/db/migrations/0041_collection_items.sqlite.sql` | SQLite variant. |
| `shared/db/queries/collection_items.sql` | sqlc input. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/cmd/maktaba-api/main.go` | Register `compact-collections` subcommand. |
| `api/internal/handlers/collections/items.go` | (Epic 7 Story 7.14.) Calls `collections.InsertItem`/`MoveItem`/`RemoveItem`. |
| `specs/epics/09-library-management/README.md` | Tick story 9.13. |

### 2.3 Type definitions

```go
// api/internal/collections/store.go
package collections

const PositionStep = 10  // sparse spacing

type Item struct {
    CollectionID uuid.UUID
    VideoID      uuid.UUID
    Position     int32
    AddedAt      time.Time
}

type InsertResult struct {
    Item     Item
    Inserted bool
}
```

## 3. Database migration

`shared/db/migrations/0041_collection_items.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

CREATE TABLE collection_items (
    collection_id   UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    video_id        UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    added_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, video_id),
    CONSTRAINT collection_items_position_chk CHECK (position > 0)
);

-- The hot read shape is "items in collection ordered by position".
CREATE INDEX collection_items_order
    ON collection_items (collection_id, position);

-- A guard at write time: refuse to insert into a smart collection.
-- (Cleaner as a CHECK on the join, but Postgres CHECK can't do it.
-- We use a BEFORE INSERT/UPDATE trigger.)
CREATE OR REPLACE FUNCTION collection_items_smart_guard() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
DECLARE
    is_smart BOOLEAN;
BEGIN
    SELECT c.is_smart INTO is_smart FROM collections c WHERE c.id = NEW.collection_id;
    IF is_smart THEN
        RAISE EXCEPTION 'collection_is_smart' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER collection_items_smart_guard_ins
    BEFORE INSERT ON collection_items
    FOR EACH ROW EXECUTE FUNCTION collection_items_smart_guard();

CREATE TRIGGER collection_items_smart_guard_upd
    BEFORE UPDATE ON collection_items
    FOR EACH ROW EXECUTE FUNCTION collection_items_smart_guard();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS collection_items_smart_guard_upd ON collection_items;
DROP TRIGGER IF EXISTS collection_items_smart_guard_ins ON collection_items;
DROP FUNCTION IF EXISTS collection_items_smart_guard();
DROP TABLE IF EXISTS collection_items;
-- +goose StatementEnd
```

`shared/db/queries/collection_items.sql`:

```sql
-- name: GetMaxPosition :one
SELECT COALESCE(MAX(position), 0) FROM collection_items
 WHERE collection_id = $1
 FOR UPDATE;

-- name: InsertItemAtPosition :one
INSERT INTO collection_items (collection_id, video_id, position)
VALUES ($1, $2, $3)
ON CONFLICT (collection_id, video_id) DO NOTHING
RETURNING position, (xmax = 0) AS inserted;

-- name: MoveItem :exec
UPDATE collection_items
   SET position = $3, updated_at = now()
 WHERE collection_id = $1 AND video_id = $2;

-- name: RemoveItem :exec
DELETE FROM collection_items
 WHERE collection_id = $1 AND video_id = $2;

-- name: ListItemsAfterCursor :many
SELECT video_id, position
  FROM collection_items
 WHERE collection_id = $1 AND position > $2
 ORDER BY position
 LIMIT $3;

-- name: CompactCollection :exec
WITH ordered AS (
    SELECT video_id, ROW_NUMBER() OVER (ORDER BY position) AS rn
      FROM collection_items WHERE collection_id = $1
)
UPDATE collection_items ci
   SET position = o.rn::int * 10, updated_at = now()
  FROM ordered o
 WHERE ci.collection_id = $1 AND ci.video_id = o.video_id;

-- name: ListAllCollectionIDs :many
SELECT id FROM collections WHERE is_smart = false;
```

## 4. Code scaffolding

### 4.1 `InsertItem`

```go
// api/internal/collections/store.go
func InsertItem(ctx context.Context, db DBPool,
                collectionID, videoID uuid.UUID, position *int32) (InsertResult, error) {
    tx, err := db.Begin(ctx)
    if err != nil { return InsertResult{}, err }
    defer tx.Rollback(ctx)
    q := dbq.WithTx(tx)

    pos := int32(0)
    if position != nil {
        pos = *position
    } else {
        max, err := q.GetMaxPosition(ctx, collectionID)
        if err != nil { return InsertResult{}, err }
        pos = int32(max) + PositionStep
    }
    if pos <= 0 {
        return InsertResult{}, &ValidationError{Code: "bad-position",
            Message: "position must be positive"}
    }

    res, err := q.InsertItemAtPosition(ctx, dbq.InsertItemAtPositionParams{
        CollectionID: collectionID, VideoID: videoID, Position: pos,
    })
    if err != nil {
        if isSmartCollectionError(err) {
            return InsertResult{}, &ValidationError{Code: "collection-is-smart"}
        }
        return InsertResult{}, err
    }
    if !res.Inserted {
        return InsertResult{}, &ValidationError{Code: "already-in-collection"}
    }
    if err := tx.Commit(ctx); err != nil { return InsertResult{}, err }

    return InsertResult{
        Item: Item{
            CollectionID: collectionID, VideoID: videoID,
            Position: int32(res.Position),
        },
        Inserted: true,
    }, nil
}
```

### 4.2 `Compact` per collection

```go
func Compact(ctx context.Context, db DBPool, collectionID uuid.UUID) error {
    tx, err := db.Begin(ctx)
    if err != nil { return err }
    defer tx.Rollback(ctx)
    q := dbq.WithTx(tx)

    if err := q.CompactCollection(ctx, collectionID); err != nil {
        return err
    }
    return tx.Commit(ctx)
}

// CompactAll iterates every non-smart collection. Online — readers
// continue to work because the UPDATE is one statement per collection
// and Postgres serializes via the index range.
func CompactAll(ctx context.Context, db DBPool) (int, error) {
    q := dbq.New(db)
    ids, err := q.ListAllCollectionIDs(ctx)
    if err != nil { return 0, err }
    for _, id := range ids {
        if err := Compact(ctx, db, id); err != nil { return 0, err }
    }
    return len(ids), nil
}
```

### 4.3 CLI subcommand

```go
// api/cmd/maktaba-api/compact_collections.go
var compactCollectionsCmd = &cobra.Command{
    Use:   "compact-collections",
    Short: "Renumber collection_items.position to a tight 10/20/30/... sequence.",
    RunE: func(cmd *cobra.Command, args []string) error {
        ctx := cmd.Context()
        n, err := collections.CompactAll(ctx, dbPool)
        if err != nil { return err }
        cmd.Printf("compacted %d collections\n", n)
        return nil
    },
}
```

## 5. Test plan

### 5.1 Store unit tests (`store_test.go`)

| Test | What it pins |
|---|---|
| `TestInsertItem_AppendUsesMaxPlus10` | Empty collection → first append at 10; second at 20; third at 30. AC-2. |
| `TestInsertItem_ExplicitPosition` | POST `{position: 25}` → row exists at 25. AC-1. |
| `TestInsertItem_DuplicateRejected` | Second insert with same `(collection_id, video_id)` → `already-in-collection` ValidationError; PK guarantees no second row. |
| `TestInsertItem_SmartCollectionRejected` | Collection with `is_smart=true` → `collection-is-smart` ValidationError; trigger raises. |
| `TestInsertItem_BadPositionZeroRejected` | Caller passes `position: 0` → `bad-position`. |
| `TestInsertItem_BadPositionNegativeRejected` | `position: -10` → `bad-position`; CHECK constraint also enforces. |
| `TestMoveItem_SingleUpdate` | 100-item collection; move item from 50 to 25 → exactly one row UPDATE; result list reflects the new order. AC-1 ("re-ordering one item is a single UPDATE"). |
| `TestRemoveItem_RemovesRow` | DELETE row; count drops by 1; positions unchanged for other rows. |
| `TestListItems_OrderedByPosition` | Insert 10 items in random `position`; list returns them sorted ascending. |
| `TestListItems_CursorPagination` | Cursor at position 100 → returns only rows with position > 100. |
| `TestListItems_EmptyCollection` | List of empty collection returns `[]`, not null. |
| `TestCompact_RenumbersOneCollection` | Pre-state: positions 12, 47, 999 → after Compact: 10, 20, 30. Order preserved. AC-3. |
| `TestCompact_PreservesOrderForTies` | Two items with identical `position` (shouldn't happen but defensive) → ROW_NUMBER orders deterministically by `(position, video_id)` (need ORDER BY video_id tiebreak in the SQL). Fix `CompactCollection` to add the tiebreak. |
| `TestCompact_Idempotent` | Run Compact twice in a row → second run is a no-op (positions already 10n). AC-3. |
| `TestCompact_OnlineReadsDuringCompaction` | While Compact is mid-tx, a concurrent SELECT returns a consistent snapshot (either pre or post) — never partial. |

### 5.2 Migration test

`pipeline/tests/db/test_collection_items_migration.py`:

| Test | What it pins |
|---|---|
| `test_table_and_index_present` | `pg_indexes` shows `collection_items_order`. |
| `test_smart_guard_trigger_blocks_insert` | Smart collection insert raises with code `23514` and message containing `collection_is_smart`. |
| `test_position_check_rejects_zero_and_negative` | INSERT with position 0 or -1 raises CHECK violation. |
| `test_pk_rejects_duplicate` | Second INSERT with same (collection, video) → unique violation. |

### 5.3 Performance gate

| Test | Target |
|---|---|
| `TestInsertItem_p99_under_5ms` | 1000 sequential inserts at the tail → p99 latency under 5 ms (the `FOR UPDATE` + INSERT). |
| `TestMoveItem_p99_under_2ms` | 1000 single-item moves → p99 under 2 ms. |
| `TestCompact_100kItem_under_1s` | 100k-item collection → Compact tx finishes in < 1 s. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Drag-and-drop reordering of 100 items | Ten single UPDATEs (one per item moved) — the UI computes new positions; the store does not need to renumber. AC-1 verbatim. | `TestMoveItem_SingleUpdate` |
| Cycle prevention | Collections are flat (no nesting); not applicable. | Documented |
| Same video added twice | PK rejects. | `TestInsertItem_DuplicateRejected` |
| Position drift to 1e9 | Compact restores to small numbers. | `TestCompact_RenumbersOneCollection` |
| Compact runs during user reorder | Compact takes a `RowExclusiveLock`; a concurrent UPDATE for the same row waits. The user sees a brief pause during compaction; the result is correct either way. Ops doc warns against scheduling Compact during peak hours. | Documented |
| Insert into an empty collection | `MAX(position)` returns NULL → COALESCE to 0 → first position is 10. | `TestInsertItem_AppendUsesMaxPlus10` |
| Insert at user-supplied position that collides with an existing row | The PK (collection, video) doesn't include position, so no constraint blocks duplicate position values. The next List will return both — order between them is by `position` ascending then by insertion id (we don't surface `added_at` in the cursor). The story does not promise unique position values. To fix collisions, run Compact. | Documented |
| Smart collection's items requested via the manual route | Out of scope — the smart route in Story 9.14 uses live computation. | Story 9.14 |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `collections.position_step` (constant) | 10 | Append step. |
| `collections.compact_threshold` (operator-tunable) | 1e8 | If `MAX(position) > threshold`, the cron triggers Compact for that collection. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `collections` table | architecture §8.2 | Parent FK target; this story doesn't recreate it. |
| `videos` table | architecture §8.1 | FK target. |

No new external deps.

## 9. Acceptance checklist

**Migration**
- [ ] `0041_collection_items.sql` creates the table, the index, and the smart-guard trigger.
- [ ] CHECK constraint on `position > 0`.

**Code**
- [ ] `api/internal/collections/store.go` exposes `InsertItem`, `MoveItem`, `RemoveItem`, `ListItems`, `Compact`, `CompactAll`.
- [ ] CLI `maktaba-api compact-collections` runs `CompactAll`.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: items inserted with explicit `position` read back in that order; reorder is a single UPDATE.
- [ ] AC-2: append uses `MAX(position) + 10`.
- [ ] AC-3: Compact renumbers to 10, 20, 30, …; idempotent.

**Observability**
- [ ] Counter `collection_items_inserted_total{outcome=ok|duplicate|smart_rejected}`.
- [ ] Counter `collection_compact_runs_total`.
- [ ] Histogram `collection_compact_duration_seconds{collection_id}`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.13.
- [ ] Operator handbook explains Compact scheduling and the position-collision behaviour.
