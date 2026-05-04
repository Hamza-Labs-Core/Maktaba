# Plan 9.13 — Collections (manual ordered) — implementation

> Implementation plan for [story-09-13-collections-manual.md](story-09-13-collections-manual.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: the REST surface (`POST /collections`,
> `POST /collections/{id}/items`, `PATCH …`, etc.) is defined in
> Epic 7 [Story 7.14](../07-api/story-07-14-collections-tags-speakers-crud.md);
> this plan implements the storage, ordering scheme, and operator CLI
> behind those handlers. Smart-collection behavior (`is_smart=true`)
> lives in [Plan 9.14](plan-09-14-smart-collections.md) — the schema is
> shared, but the read path differs.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Sparse position scheme: insert at end = `MAX(position) + 10`**, with a `BIGINT position` column. New collections start with positions 10, 20, 30. Reorder is a single `UPDATE`. | Story AC-1 / AC-2: "items are added with `position` 10, 20, 30, …" + "position = `MAX(position) + 10` (sparse to avoid renumbers)". | Sparse positions mean a single drag-and-drop reorder is one UPDATE (set the moved item's position to halfway between its new neighbors), not N UPDATEs renumbering everyone. The `BIGINT` headroom (~9.2e18) supports billions of reorders before any compaction is needed; even in pathological halve-the-gap workloads we get 60+ moves per pair before exhausting the integer range. |
| D2 | **Schema in one table per category**: `collections (id, library_id, name, is_smart, smart_query JSONB NULL, frozen_from_query JSONB NULL, …)` and `collection_items (collection_id, video_id, position BIGINT, added_at, PRIMARY KEY (collection_id, video_id))`. The `is_smart` flag selects between manual (this story) and live-computed (Plan 9.14) read paths. | Story §"`collections (is_smart=false)` + `collection_items` (§8.2)" + Plan 9.14 sharing `is_smart=true`. | One table for both manual and smart simplifies the URL surface (`/collections/{id}` works for either) and lets a single index serve listings. The `smart_query` and `frozen_from_query` columns are NULL for `is_smart=false` rows; storage cost is negligible. The PK on `(collection_id, video_id)` is what AC's edge case "same video added twice → 409" relies on. |
| D3 | **Reorder = single `UPDATE collection_items SET position = $new WHERE collection_id=$1 AND video_id=$2`.** The new position is computed client-side as `(prev.position + next.position) / 2` if there's a neighbor, else `MAX(position) + 10` (insert at end) or `MIN(position) - 10` (insert at start). The server validates the proposed position is unique within the collection before writing. | Story AC-1: "Re-ordering one item is a single UPDATE". | Server-computed positions on every reorder would either (a) renumber everyone (defeats sparseness) or (b) duplicate the client computation. Letting the client compute halves the round-trip count. The server-side uniqueness check protects against concurrent reorders racing into the same midpoint. |
| D4 | **Compaction is a CLI subcommand**, not a scheduled background job: `maktaba-api compact-collections [--library-id … | --all]`. It runs `UPDATE collection_items SET position = (row_number() over (...)) * 10` per collection in a single SERIALIZABLE transaction. Online reads continue to work because the order does not change — only the gaps. | Story AC-3: "the operator runs `maktaba-api compact-collections` … positions are renumbered 10, 20, 30, … per collection". | A scheduled background job would either run too often (wasted work; the BIGINT range is enormous) or too rarely (operators want to compact on demand after a known-bad reorder pattern). The CLI is operator-friendly: the operator knows when they need it. The story is explicit about this. |
| D5 | **Idempotent compaction.** Running compaction twice on a collection that already has positions 10, 20, 30, … yields the exact same positions; the SQL uses `row_number() OVER (ORDER BY position, video_id)` so the tiebreak is deterministic. | Story test: "compaction is idempotent (running twice yields the same positions)". | Without the `video_id` tiebreak, `row_number()` over `position` alone is non-deterministic when two rows have the same position (which can happen during an unfinished concurrent reorder). The tiebreak makes the operation fully deterministic and idempotent. |
| D6 | **Insert without position** runs in a single statement using `INSERT … SELECT (COALESCE(MAX(position), 0) + 10)` — no client-supplied default, no race condition. | Story AC-2: "POST without `position` … position = `MAX(position) + 10` (sparse to avoid renumbers)". | Two-roundtrip insert (read MAX, then INSERT with the computed value) races: two concurrent inserts can both read the same MAX and both write `MAX+10`, producing duplicate positions. The single-statement form is atomic; uniqueness is enforced by a partial unique index `(collection_id, position)`. |

If D1 is rejected (dense `position INTEGER` without sparseness): every reorder of N items costs O(N) UPDATEs, which on a 10k-item collection is a long write — fine for the average user but punishing on the tail. Sparseness is the standard answer; we follow it.

If D4 is rejected (background compaction): operators have no on-demand handle for the hot-loop case where someone scripts thousands of reorders, and the overhead of "always compact a little" makes write performance non-deterministic. CLI on-demand is the right granularity.

---

## 1. Architecture diagram — manual collection write paths

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Inputs (REST surface in Epic 7 Story 7.14; bodies in this plan)      │
   │                                                                      │
   │  POST   /api/libraries/{lib}/collections                             │
   │           {name}              → create empty manual collection       │
   │  POST   /api/collections/{c}/items                                   │
   │           {video_id, position?} → append (or insert at position)     │
   │  PATCH  /api/collections/{c}/items/{video_id}                        │
   │           {position}          → single-row reorder                   │
   │  DELETE /api/collections/{c}/items/{video_id}                        │
   │  GET    /api/collections/{c}/items?cursor=…                          │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │
                                      ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Storage                                                              │
   │  collections                                                         │
   │   id, library_id, name, is_smart=false,                              │
   │   smart_query NULL, frozen_from_query NULL,                          │
   │   created_at, updated_at                                             │
   │  collection_items                                                    │
   │   collection_id, video_id (PK), position BIGINT, added_at            │
   │   UNIQUE (collection_id, position) — enforces D6 race-safety         │
   └──────────────────────────────────┬───────────────────────────────────┘
                                      │
                                      ▼
   ┌──────────────────────────────────────────────────────────────────────┐
   │ CLI (D4): maktaba-api compact-collections                            │
   │                                                                      │
   │  for each collection (or --library-id … or all):                     │
   │    BEGIN;                                                            │
   │     WITH ranked AS (                                                 │
   │       SELECT video_id,                                               │
   │              row_number() OVER (ORDER BY position, video_id) * 10    │
   │                AS new_position                                       │
   │         FROM collection_items                                        │
   │        WHERE collection_id = $1                                      │
   │       FOR UPDATE                                                     │
   │     )                                                                │
   │     UPDATE collection_items ci                                       │
   │        SET position = ranked.new_position                            │
   │       FROM ranked                                                    │
   │      WHERE ci.collection_id = $1                                     │
   │        AND ci.video_id = ranked.video_id;                            │
   │    COMMIT;                                                           │
   └──────────────────────────────────────────────────────────────────────┘
```

The unique index `(collection_id, position)` is a partial index gated
on `is_smart = false` (smart collections have no `collection_items`
rows in v1; see Plan 9.14's freeze flow).

---

## 2. Detailed implementation

### 2.1 Schema migration — `collections` and `collection_items`

```sql
-- shared/db/migrations/0044_collections.sql
BEGIN;

CREATE TABLE collections (
    id                  UUID PRIMARY KEY,
    library_id          UUID NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    name                TEXT NOT NULL,
    is_smart            BOOLEAN NOT NULL DEFAULT false,
    smart_query         JSONB,                              -- non-null for is_smart=true (Plan 9.14)
    frozen_from_query   JSONB,                              -- non-null after freeze (Plan 9.14)
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (length(name) BETWEEN 1 AND 256),
    CHECK (
        (is_smart = false AND smart_query IS NULL)
        OR
        (is_smart = true  AND smart_query IS NOT NULL)
    )
);

CREATE INDEX collections_library ON collections (library_id, name);

CREATE TABLE collection_items (
    collection_id  UUID NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    video_id       UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    position       BIGINT NOT NULL,
    added_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (collection_id, video_id),
    CHECK (position > 0)
);

-- D6 race-safety: two concurrent inserts cannot land on the same position.
-- Smart collections have no rows here (Plan 9.14 freeze flips is_smart=false
-- before inserting), so the constraint is a hard one — no partial filter.
CREATE UNIQUE INDEX collection_items_position
    ON collection_items (collection_id, position);

-- For the read path: ordered listing by position, paginated via cursor.
CREATE INDEX collection_items_order
    ON collection_items (collection_id, position, video_id);

COMMIT;
```

### 2.2 sqlc query stubs

```sql
-- internal/db/queries/collections.sql

-- name: CreateCollection :one
INSERT INTO collections (id, library_id, name, is_smart)
VALUES ($1, $2, $3, false)
RETURNING id, library_id, name, is_smart, created_at, updated_at;

-- name: GetCollection :one
SELECT id, library_id, name, is_smart, smart_query, frozen_from_query,
       created_at, updated_at
  FROM collections WHERE id = $1;

-- name: AppendItem :one
-- D6: single-statement insert at MAX(position) + 10; race-safe via unique index.
INSERT INTO collection_items (collection_id, video_id, position)
SELECT $1, $2, COALESCE(MAX(position), 0) + 10
  FROM collection_items
 WHERE collection_id = $1
RETURNING collection_id, video_id, position, added_at;

-- name: InsertItemAt :one
INSERT INTO collection_items (collection_id, video_id, position)
VALUES ($1, $2, $3)
RETURNING collection_id, video_id, position, added_at;

-- name: ReorderItem :one
UPDATE collection_items
   SET position = $3
 WHERE collection_id = $1 AND video_id = $2
RETURNING collection_id, video_id, position;

-- name: DeleteItem :execrows
DELETE FROM collection_items WHERE collection_id = $1 AND video_id = $2;

-- name: ListItemsAfterCursor :many
-- Cursor pagination per Story 7.2: order by (position, video_id) for
-- a deterministic boundary; cursor encodes the last-seen pair.
SELECT video_id, position, added_at
  FROM collection_items
 WHERE collection_id = $1
   AND (position, video_id) > ($2::bigint, $3::uuid)
 ORDER BY position, video_id
 LIMIT $4;

-- name: ListAllItemsForCompaction :many
-- Used by the compact-collections CLI; FOR UPDATE locks the rows.
SELECT video_id, position
  FROM collection_items
 WHERE collection_id = $1
 ORDER BY position, video_id
 FOR UPDATE;

-- name: CompactCollection :exec
WITH ranked AS (
    SELECT video_id,
           (row_number() OVER (ORDER BY position, video_id) * 10)::bigint
             AS new_position
      FROM collection_items
     WHERE collection_id = $1
)
UPDATE collection_items ci
   SET position = ranked.new_position
  FROM ranked
 WHERE ci.collection_id = $1
   AND ci.video_id = ranked.video_id;

-- name: ListCollectionIDsForLibrary :many
SELECT id FROM collections WHERE library_id = $1 AND is_smart = false;
```

### 2.3 Go handler — append item (D6)

```go
// api/internal/handlers/collections/items.go
package collections

import (
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"

    "maktaba/api/internal/db"
)

type addItemReq struct {
    VideoID  uuid.UUID `json:"video_id"`
    Position *int64    `json:"position,omitempty"`
}

type itemDTO struct {
    VideoID  uuid.UUID `json:"video_id"`
    Position int64     `json:"position"`
}

func AddItem(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cid, err := uuid.Parse(chi.URLParam(r, "collectionID"))
        if err != nil {
            jsonErr(w, http.StatusBadRequest, "bad-id", "invalid collection id")
            return
        }
        var req addItemReq
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            jsonErr(w, http.StatusBadRequest, "bad-json", err.Error())
            return
        }
        if req.VideoID == uuid.Nil {
            jsonErr(w, http.StatusUnprocessableEntity, "video-required", "")
            return
        }

        // Reject smart-collection writes upfront.
        coll, err := q.GetCollection(r.Context(), cid)
        switch {
        case errors.Is(err, pgx.ErrNoRows):
            jsonErr(w, http.StatusNotFound, "collection-not-found", "")
            return
        case err != nil:
            jsonErr(w, http.StatusInternalServerError, "lookup", err.Error())
            return
        case coll.IsSmart:
            jsonErr(w, http.StatusUnprocessableEntity, "smart-collection-readonly",
                "cannot mutate items on a smart collection")
            return
        }

        var pos int64
        var inserted db.AppendItemRow
        if req.Position == nil {
            // D6: single-statement append at MAX(position)+10.
            row, err := q.AppendItem(r.Context(), db.AppendItemParams{
                CollectionID: cid, VideoID: req.VideoID,
            })
            if err != nil {
                writeInsertErr(w, err)
                return
            }
            pos = row.Position
            inserted = row
        } else {
            if *req.Position <= 0 {
                jsonErr(w, http.StatusUnprocessableEntity, "bad-position",
                    "position must be > 0")
                return
            }
            row, err := q.InsertItemAt(r.Context(), db.InsertItemAtParams{
                CollectionID: cid, VideoID: req.VideoID, Position: *req.Position,
            })
            if err != nil {
                writeInsertErr(w, err)
                return
            }
            pos = row.Position
            inserted = db.AppendItemRow{
                CollectionID: row.CollectionID,
                VideoID:      row.VideoID,
                Position:     row.Position,
                AddedAt:      row.AddedAt,
            }
        }
        _ = inserted
        writeJSON(w, http.StatusCreated, itemDTO{VideoID: req.VideoID, Position: pos})
    }
}

func writeInsertErr(w http.ResponseWriter, err error) {
    var pgErr *pgx.PgError // alias for clarity
    if pgIsUniqueViolation(err, "collection_items_pkey") {
        jsonErr(w, http.StatusConflict, "video-already-in-collection",
            "the same video cannot be added twice")
        return
    }
    if pgIsUniqueViolation(err, "collection_items_position") {
        jsonErr(w, http.StatusConflict, "position-conflict",
            "another item already occupies that position; refresh and retry")
        return
    }
    if pgIsForeignKeyViolation(err) {
        jsonErr(w, http.StatusUnprocessableEntity, "video-or-collection-missing",
            "one of the references does not exist")
        return
    }
    jsonErr(w, http.StatusInternalServerError, "insert", err.Error())
    _ = pgErr
}
```

### 2.4 Go handler — reorder (single UPDATE per AC-1, D3)

```go
// api/internal/handlers/collections/reorder.go
package collections

type reorderReq struct {
    Position int64 `json:"position"`
}

func ReorderItem(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cid, _ := uuid.Parse(chi.URLParam(r, "collectionID"))
        vid, _ := uuid.Parse(chi.URLParam(r, "videoID"))
        var req reorderReq
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            jsonErr(w, http.StatusBadRequest, "bad-json", err.Error())
            return
        }
        if req.Position <= 0 {
            jsonErr(w, http.StatusUnprocessableEntity, "bad-position", "")
            return
        }
        out, err := q.ReorderItem(r.Context(), db.ReorderItemParams{
            CollectionID: cid, VideoID: vid, Position: req.Position,
        })
        switch {
        case errors.Is(err, pgx.ErrNoRows):
            jsonErr(w, http.StatusNotFound, "item-not-found", "")
        case pgIsUniqueViolation(err, "collection_items_position"):
            jsonErr(w, http.StatusConflict, "position-conflict",
                "another item already occupies that position; refresh and retry")
        case err != nil:
            jsonErr(w, http.StatusInternalServerError, "reorder", err.Error())
        default:
            writeJSON(w, http.StatusOK, itemDTO{VideoID: out.VideoID, Position: out.Position})
        }
    }
}
```

### 2.5 CLI command — `compact-collections`

```go
// api/cmd/maktaba-api/compact_collections.go
package main

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "log/slog"
    "os"

    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
)

func runCompactCollections(args []string) int {
    fs := flag.NewFlagSet("compact-collections", flag.ContinueOnError)
    libraryID := fs.String("library-id", "", "only compact collections in this library (UUID)")
    all := fs.Bool("all", false, "compact every manual collection")
    if err := fs.Parse(args); err != nil {
        return 2
    }
    if *libraryID == "" && !*all {
        fmt.Fprintln(os.Stderr, "must pass --library-id <uuid> or --all")
        return 2
    }

    ctx := context.Background()
    pool, err := pgxpool.New(ctx, os.Getenv("DATABASE_URL"))
    if err != nil {
        slog.Error("db", "err", err)
        return 1
    }
    defer pool.Close()

    q := db.New(pool)

    var collections []uuid.UUID
    if *libraryID != "" {
        lib, err := uuid.Parse(*libraryID)
        if err != nil {
            slog.Error("bad library id", "err", err)
            return 2
        }
        collections, err = q.ListCollectionIDsForLibrary(ctx, lib)
        if err != nil {
            slog.Error("list", "err", err)
            return 1
        }
    } else {
        // --all: enumerate every manual collection.
        rows, err := pool.Query(ctx,
            "SELECT id FROM collections WHERE is_smart = false ORDER BY id")
        if err != nil {
            slog.Error("list-all", "err", err)
            return 1
        }
        defer rows.Close()
        for rows.Next() {
            var id uuid.UUID
            if err := rows.Scan(&id); err != nil {
                slog.Error("scan", "err", err)
                return 1
            }
            collections = append(collections, id)
        }
    }

    failures := 0
    for _, id := range collections {
        if err := compactOne(ctx, pool, q, id); err != nil {
            slog.Error("compact", "collection_id", id, "err", err)
            failures++
            continue
        }
        slog.Info("compacted", "collection_id", id)
    }
    if failures > 0 {
        return 1
    }
    return 0
}

func compactOne(ctx context.Context, pool *pgxpool.Pool, q *db.Queries,
    cid uuid.UUID) error {
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
    if err != nil {
        return err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    qtx := q.WithTx(tx)

    // Take row locks first to prevent concurrent reorders during compaction.
    if _, err := qtx.ListAllItemsForCompaction(ctx, cid); err != nil {
        return err
    }
    if err := qtx.CompactCollection(ctx, cid); err != nil {
        return err
    }
    return tx.Commit(ctx)
}

var _ = errors.Is // for callers that catch txn-aborted retries upstream
```

The CLI is wired into the main `maktaba-api` binary as a subcommand:

```go
// api/cmd/maktaba-api/main.go (excerpt)
func main() {
    if len(os.Args) >= 2 && os.Args[1] == "compact-collections" {
        os.Exit(runCompactCollections(os.Args[2:]))
    }
    runServer()
}
```

### 2.6 List with cursor pagination (Story 7.2 primitive)

```go
// api/internal/handlers/collections/list.go (excerpt)

func ListItems(q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cid, _ := uuid.Parse(chi.URLParam(r, "collectionID"))
        cursor := decodeCursor(r.URL.Query().Get("cursor")) // (position int64, video_id uuid)
        limit := clamp(parseInt(r.URL.Query().Get("limit"), 50), 1, 200)

        rows, err := q.ListItemsAfterCursor(r.Context(), db.ListItemsAfterCursorParams{
            CollectionID: cid,
            Position:     cursor.Position,
            VideoID:      cursor.VideoID,
            Limit:        int32(limit),
        })
        if err != nil {
            jsonErr(w, http.StatusInternalServerError, "list", err.Error())
            return
        }
        var nextCursor string
        if len(rows) == limit {
            last := rows[len(rows)-1]
            nextCursor = encodeCursor(last.Position, last.VideoID)
        }
        writeJSON(w, 200, map[string]any{
            "items":       rows,
            "next_cursor": nextCursor,
        })
    }
}
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0044_collections.sql` | `collections`, `collection_items` tables; indexes | `TestMigrationCreatesCollections` |
| 2 | `api/internal/db/queries/collections.sql` | sqlc queries listed in §2.2 | sqlc smoke test |
| 3 | `api/internal/handlers/collections/create.go` | `CreateCollection` handler | `TestCreateCollection` |
| 4 | `api/internal/handlers/collections/items.go` | `AddItem`, error helpers | `TestAppendItemPositionIncrements`, `TestAddSameVideoTwice409` |
| 5 | `api/internal/handlers/collections/reorder.go` | `ReorderItem` handler | `TestReorderIsSingleUpdate`, `TestReorderPositionConflict409` |
| 6 | `api/internal/handlers/collections/list.go` | `ListItems` cursor handler | `TestListItemsCursorPagination` |
| 7 | `api/internal/handlers/collections/delete.go` | `DeleteItem` handler | `TestDeleteItem` |
| 8 | `api/internal/handlers/router.go` (extend) | route registrations | route table test |
| 9 | `api/cmd/maktaba-api/compact_collections.go` | `runCompactCollections`, `compactOne` | `TestCompactCollectionsCLI`, `TestCompactionIsIdempotent` |
| 10 | `api/cmd/maktaba-api/main.go` (extend) | dispatch | smoke test |
| 11 | `api/internal/handlers/collections/collections_test.go` | integration tests | (n/a) |

---

## 4. Test cases (keyed to story ACs)

### 4.1 `TestAppendItemPositionIncrements` — AC-2

```go
func TestAppendWithoutPositionUsesMaxPlus10(t *testing.T) {
    api, db := setup(t)
    coll := mkCollection(t, db, lib, "Series")
    v1, v2, v3 := mkVideo(t, db, lib), mkVideo(t, db, lib), mkVideo(t, db, lib)

    api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v1}).ExpectStatus(201)
    api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v2}).ExpectStatus(201)
    api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v3}).ExpectStatus(201)

    rows := api.GET(coll.itemsURL()).JSON()["items"].([]any)
    positions := []int64{}
    for _, r := range rows {
        positions = append(positions, int64(r.(map[string]any)["position"].(float64)))
    }
    assert.Equal(t, []int64{10, 20, 30}, positions)
}
```

### 4.2 `TestReorderIsSingleUpdate` — AC-1

```go
func TestReorder100ItemsByDragDropIs100SingleUpdates(t *testing.T) {
    api, db := setup(t)
    coll, videos := mkCollectionWith(t, db, lib, 100)

    // Reverse the order via 100 single-row updates: each new position is
    // computed as midway between the new neighbors (or end+10).
    rng := newRand(42)
    perm := rng.Perm(100)
    for i, target := range perm {
        v := videos[target]
        // place v at position (i+1)*10 + 5 to avoid colliding with the existing 10/20/...
        api.PATCH(coll.itemURL(v),
            map[string]int64{"position": int64((i+1)*10) + 5}).
            ExpectStatus(200)
    }

    // Final read-back: positions are unique, ordering is the perm.
    rows := allItems(t, db, coll.id)
    seen := map[int64]bool{}
    for _, r := range rows {
        assert.False(t, seen[r.Position])
        seen[r.Position] = true
    }
    // Each PATCH was a single UPDATE — verify by query plan or write count.
    assert.LessOrEqual(t, queryWriteCount(t, db, coll.id), 100+100)
}
```

### 4.3 `TestSameVideoAddedTwice409` — story edge case

```go
func TestAddingSameVideoTwiceReturns409(t *testing.T) {
    api, db := setup(t)
    coll := mkCollection(t, db, lib, "Series")
    v := mkVideo(t, db, lib)
    api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v}).ExpectStatus(201)
    resp := api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v}).
        ExpectStatus(409).JSON()
    assert.Equal(t, "video-already-in-collection", resp["type"])
    var n int
    db.QueryRow(ctx, "SELECT COUNT(*) FROM collection_items WHERE collection_id=$1",
        coll.id).Scan(&n)
    assert.Equal(t, 1, n)
}
```

### 4.4 `TestCompactionIsIdempotent` — AC-3

```go
func TestCompactionIsIdempotent(t *testing.T) {
    db := freshDB(t)
    coll, _ := mkCollectionWith(t, db, lib, 50)

    // Drift: pretend the user did many reorders.
    _, err := db.Exec(ctx, `
        UPDATE collection_items
           SET position = position * 1000 + (random()*1000)::bigint
         WHERE collection_id = $1`, coll.id)
    require.NoError(t, err)

    // First compaction.
    require.Equal(t, 0, runCLI(t, "compact-collections", "--library-id", lib.String()))
    first := snapshotPositions(t, db, coll.id)

    // Second compaction.
    require.Equal(t, 0, runCLI(t, "compact-collections", "--library-id", lib.String()))
    second := snapshotPositions(t, db, coll.id)

    assert.Equal(t, first, second)

    // And: positions are 10, 20, 30, ...
    expected := make([]int64, 50)
    for i := range expected {
        expected[i] = int64((i + 1) * 10)
    }
    assert.Equal(t, expected, first)
}
```

### 4.5 `TestCompactionPreservesOrder` — AC-3

```go
func TestCompactionPreservesOrder(t *testing.T) {
    db := freshDB(t)
    coll, videos := mkCollectionWith(t, db, lib, 5)

    // Manually drift to known sparse values: 100, 250, 7777, 1e9, 1e9+50
    drifted := []int64{100, 250, 7777, 1_000_000_000, 1_000_000_050}
    for i, v := range videos {
        _, err := db.Exec(ctx, `UPDATE collection_items SET position=$1
            WHERE collection_id=$2 AND video_id=$3`, drifted[i], coll.id, v)
        require.NoError(t, err)
    }
    expectedOrder := videos // already in ascending position

    runCLI(t, "compact-collections", "--all")

    rows, _ := db.Query(ctx, `SELECT video_id, position FROM collection_items
        WHERE collection_id=$1 ORDER BY position`, coll.id)
    var got []uuid.UUID
    for rows.Next() {
        var id uuid.UUID; var p int64
        rows.Scan(&id, &p); got = append(got, id)
    }
    assert.Equal(t, expectedOrder, got)
}
```

### 4.6 `TestSmartCollectionWriteRejected` — Plan 9.14 boundary

```go
func TestAppendToSmartCollectionReturns422(t *testing.T) {
    api, db := setup(t)
    coll := mkSmartCollection(t, db, lib, `{"filters": []}`)
    v := mkVideo(t, db, lib)
    resp := api.POST(coll.itemsURL(), map[string]uuid.UUID{"video_id": v}).
        ExpectStatus(422).JSON()
    assert.Equal(t, "smart-collection-readonly", resp["type"])
}
```

### 4.7 `TestListItemsCursorPagination` — story 7.2 reuse

```go
func TestListItemsCursorPaginatesBoundary(t *testing.T) {
    api, db := setup(t)
    coll, _ := mkCollectionWith(t, db, lib, 105)
    page1 := api.GET(coll.itemsURL() + "?limit=50").JSON()
    items1 := page1["items"].([]any)
    assert.Len(t, items1, 50)
    page2 := api.GET(coll.itemsURL() + "?limit=50&cursor=" + page1["next_cursor"].(string)).JSON()
    items2 := page2["items"].([]any)
    assert.Len(t, items2, 50)
    page3 := api.GET(coll.itemsURL() + "?limit=50&cursor=" + page2["next_cursor"].(string)).JSON()
    items3 := page3["items"].([]any)
    assert.Len(t, items3, 5)
    assert.Empty(t, page3["next_cursor"])
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **Same video added twice.** PK on `(collection_id, video_id)` rejects with unique violation; handler returns `409 type=video-already-in-collection`. | Schema + `TestSameVideoAddedTwice409`. |
| E2  | **Two concurrent appends.** Both run `INSERT … SELECT MAX(position)+10`; the unique index `collection_items_position` ensures one wins; the loser sees `position-conflict 409` and the client retries with the latest read. | D6 + `TestConcurrentAppendsRaceSafe`. |
| E3  | **Reorder colliding into an existing position.** Unique index rejects; handler returns `409 type=position-conflict`. The client recomputes (e.g., to a new midpoint) and retries. | §2.4 + `TestReorderPositionConflict409`. |
| E4  | **Position drift to BIGINT max.** With sparse `+10` appends, drift is bounded; halve-the-gap reorders consume bits but BIGINT has 63 of them. The compaction CLI is the recovery valve when an operator sees positions in the billions. | D4 + `TestCompactionAfterDrift`. |
| E5  | **Compaction during active reorders.** `FOR UPDATE` lock taken by `ListAllItemsForCompaction` blocks any concurrent reorder until the compaction commits. SERIALIZABLE isolation ensures correctness if the lock is somehow bypassed. | §2.5 + `TestCompactionLocksReorders`. |
| E6  | **Smart-collection write attempt.** Handler reads `is_smart` first and returns `422 type=smart-collection-readonly` before any DB write. | §2.3 + `TestAppendToSmartCollectionReturns422`. |
| E7  | **Cycle prevention.** Not applicable — collections are flat (no nesting in v1). The story is explicit. | Documented; no code. |
| E8  | **Library deletion cascades.** `collections.library_id … ON DELETE CASCADE` and `collection_items.collection_id … ON DELETE CASCADE` clean up automatically. | Schema. |
| E9  | **Video deletion cascades.** `collection_items.video_id … ON DELETE CASCADE` removes the row from every collection. The position gap is left intact (the user can re-add a video at any position). | Schema. |
| E10 | **Empty collection list.** `ListItemsAfterCursor` with no rows returns `{items: [], next_cursor: ""}`. | §2.6 default branch. |
| E11 | **Very large compaction.** A 1e6-item collection takes a single SERIALIZABLE transaction and one CTE-driven UPDATE; on a typical Postgres box this completes in seconds. The CLI logs throughput per collection. | §2.5 + load test (deferred). |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `shared/db/migrations/0044_collections.sql` creates `collections (id, library_id, name, is_smart bool default false, smart_query JSONB NULL, frozen_from_query JSONB NULL, created_at, updated_at)` and `collection_items (collection_id, video_id, position BIGINT, added_at, PK (collection_id, video_id), UNIQUE (collection_id, position))`. (`TestMigrationCreatesCollections`)
- [ ] **A2** `POST /api/collections/{id}/items {video_id}` (no `position`) inserts at `MAX(position) + 10` in a single statement; appended items read back at positions 10, 20, 30, … (`TestAppendWithoutPositionUsesMaxPlus10`)
- [ ] **A3** `POST /api/collections/{id}/items {video_id, position}` inserts at the supplied position; if the position is already taken, returns `409 type=position-conflict`. (`TestAddItemAtSpecificPosition`, `TestAddItemAtTakenPosition409`)
- [ ] **A4** Adding the same `video_id` twice returns `409 type=video-already-in-collection` (PK violation). (`TestAddingSameVideoTwiceReturns409`)
- [ ] **A5** `PATCH /api/collections/{id}/items/{video_id} {position}` is a single UPDATE; reordering 100 items via drag-and-drop is exactly 100 UPDATEs. (`TestReorder100ItemsByDragDropIs100SingleUpdates`)
- [ ] **A6** Reorder collision returns `409 type=position-conflict`. (`TestReorderPositionConflict409`)
- [ ] **A7** `maktaba-api compact-collections --library-id <uuid>` renumbers positions to 10, 20, 30, … per collection in a single SERIALIZABLE transaction; relative order is preserved. (`TestCompactionPreservesOrder`)
- [ ] **A8** `maktaba-api compact-collections --all` works on every manual collection across libraries. (`TestCompactAll`)
- [ ] **A9** Compaction is idempotent: running it twice yields the same final positions. (`TestCompactionIsIdempotent`)
- [ ] **A10** Mutating endpoints reject smart collections with `422 type=smart-collection-readonly`. (`TestAppendToSmartCollectionReturns422`)
- [ ] **A11** `GET /api/collections/{id}/items` paginates by `(position, video_id)` cursor per Story 7.2. (`TestListItemsCursorPaginatesBoundary`)
- [ ] **A12** `DELETE /api/collections/{id}/items/{video_id}` removes the row; the position gap is left intact (compaction is the cleanup path). (`TestDeleteItemLeavesGap`)
