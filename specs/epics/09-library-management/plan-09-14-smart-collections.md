# Implementation Plan — Story 9.14 Smart Collections

> Companion to [story-09-14-smart-collections.md](story-09-14-smart-collections.md).
> The story states *what* and *why*; this plan states *how*.
> Builds on Story 9.13 (manual collections schema) and on Epic 7
> Story 7.9 (the saved-search filter language).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Schema additions | `collections.smart_query JSONB` (already in architecture §8.2); add `frozen_from_query JSONB` for the AC-3 freeze. Index on `(is_smart) WHERE is_smart = true` is unnecessary because smart queries are infrequent. |
| Filter resolver reuse | The same `internal/search/filter` package that serves `GET /api/videos?…` (Epic 7 Story 7.9) backs `GET /api/collections/{id}/items` for smart collections. One filter language, one resolver, two endpoints. |
| Live computation | At request time, the handler reads `smart_query`, calls the resolver, returns paginated `video_id`s. No materialization. |
| Pagination | Reuse Epic 7 Story 7.2's cursor primitive — a SQL key-set cursor over `(score DESC, video_id DESC)` for ranked queries or `(created_at DESC, video_id DESC)` for ordered ones. The resolver chooses based on the query. |
| Freeze | `POST /api/collections/{id}/convert?freeze=true` materializes the current items into `collection_items`, sets `is_smart=false`, and copies `smart_query` to `frozen_from_query` for audit. |
| Out of scope | The HTTP routes (Epic 7 Story 7.14); the saved-search filter spec itself (Epic 7 Story 7.9 owns); the score/order DSL changes (out of scope). |

## 1. Architecture diagram

```
   GET /api/collections/{id}/items?cursor=...
        ↓
   collections.GetItems(id, cursor)
      ├─ row = SELECT id, is_smart, smart_query FROM collections WHERE id=$1
      ├─ if not row.is_smart:
      │      delegate to manual ListItems (Story 9.13)
      ├─ else:
      │      filter = filter.Parse(row.smart_query)         (Epic 7 Story 7.9)
      │      page = filter.Resolve(cursor, page_size)        (returns video_ids
      │                                                       in the agreed order)
      │      return page                                    (with next cursor)

   POST /api/collections/{id}/convert?freeze=true
      BEGIN TX
        SELECT smart_query FROM collections WHERE id=$1 FOR UPDATE
        IF NOT is_smart: 422 collection-not-smart
        # Materialize *all* matches; cap at FREEZE_MAX (configurable).
        ids = filter.ResolveAll(smart_query, limit=FREEZE_MAX)
        FOR i, v in enumerate(ids):
            INSERT INTO collection_items (collection_id, video_id,
                                          position)
            VALUES ($1, v, (i + 1) * 10)
        UPDATE collections
           SET is_smart = false,
               frozen_from_query = smart_query,
               smart_query = NULL,
               updated_at = now()
         WHERE id = $1
      COMMIT
      audit("library", "collection-frozen", {collection_id, n_items, query})
```

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `api/internal/collections/smart.go` | `GetSmartItems`, `Freeze`, `validateSmartQuery`. |
| `api/internal/collections/smart_test.go` | Unit + integration tests per §6. |
| `api/internal/handlers/collections/items_smart.go` | The dispatcher between manual and smart listing. |
| `shared/db/migrations/0042_collections_smart.sql` | Adds `smart_query` (if not present), `frozen_from_query`, and the validation CHECK. |
| `shared/db/queries/collections_smart.sql` | sqlc input. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `api/internal/handlers/collections/items.go` | Switch over `is_smart` to call manual or smart paths. |
| `api/internal/search/filter/filter.go` | Expose `ResolvePage(cursor, limit)` and `ResolveAll(limit)` if not already. |
| `specs/epics/09-library-management/README.md` | Tick story 9.14. |

### 2.3 Type definitions

```go
// api/internal/collections/smart.go
const (
    DefaultPageSize = 50
    FreezeMax       = 100_000
)

type SmartListPage struct {
    Items      []ItemView    `json:"items"`
    NextCursor *string       `json:"next_cursor,omitempty"`
    Warning    *string       `json:"warning,omitempty"`  // e.g., "removed_tag"
}

type ItemView struct {
    VideoID   uuid.UUID `json:"video_id"`
    Score     *float64  `json:"score,omitempty"`        // populated for ranked queries
    AddedAt   *time.Time `json:"added_at,omitempty"`     // null for smart
}
```

## 3. Database migration

`shared/db/migrations/0042_collections_smart.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

ALTER TABLE collections
    ADD COLUMN IF NOT EXISTS smart_query        JSONB,
    ADD COLUMN IF NOT EXISTS frozen_from_query  JSONB;

-- A smart collection MUST have a non-null smart_query.
-- A non-smart collection MUST have a null smart_query.
ALTER TABLE collections
    ADD CONSTRAINT collections_smart_query_consistency_chk CHECK (
        (is_smart = true  AND smart_query IS NOT NULL) OR
        (is_smart = false AND smart_query IS NULL)
    );

-- The frozen_from_query is set only when a smart collection is converted.
-- It's an audit field; allow null on non-frozen rows.

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_smart_query_consistency_chk;
ALTER TABLE collections DROP COLUMN IF EXISTS frozen_from_query;
-- smart_query intentionally not dropped on Down (data preservation).
-- +goose StatementEnd
```

`shared/db/queries/collections_smart.sql`:

```sql
-- name: GetCollection :one
SELECT id, name, is_smart, smart_query, frozen_from_query, library_id
  FROM collections WHERE id = $1;

-- name: FreezeSmartCollection :exec
UPDATE collections
   SET is_smart          = false,
       frozen_from_query = smart_query,
       smart_query       = NULL,
       updated_at        = now()
 WHERE id = $1 AND is_smart = true;
```

## 4. Code scaffolding

### 4.1 Dispatch in the items handler

```go
// api/internal/handlers/collections/items.go (revised)
func ItemsHandler(d *handlers.Deps) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        ctx := r.Context()
        id, _ := uuid.Parse(chi.URLParam(r, "id"))
        col, err := d.Queries.GetCollection(ctx, id)
        if err != nil { handlers.WriteError(w, 404, "collection-not-found", ""); return }

        cursor := r.URL.Query().Get("cursor")
        pageSize := parsePageSize(r, collections.DefaultPageSize)

        if !col.IsSmart {
            page, err := collections.ListManualItems(ctx, d.Pool, id, cursor, pageSize)
            if err != nil { handlers.WriteError(w, 500, "list-failed", err.Error()); return }
            handlers.WriteJSON(w, 200, page); return
        }

        page, err := collections.GetSmartItems(ctx, d, col, cursor, pageSize)
        if err != nil { handlers.WriteError(w, 500, "list-failed", err.Error()); return }
        handlers.WriteJSON(w, 200, page)
    }
}
```

### 4.2 `GetSmartItems`

```go
// api/internal/collections/smart.go
func GetSmartItems(ctx context.Context, d *handlers.Deps, col db.Collection,
                    cursor string, pageSize int) (SmartListPage, error) {
    f, parseWarn, err := filter.Parse(col.SmartQuery)
    if err != nil {
        // A query that no longer parses (e.g., references a removed tag)
        // returns an empty page with a warning per the story's edge case.
        return SmartListPage{Items: []ItemView{},
            Warning: ptr(parseWarn.String())}, nil
    }
    f = f.WithLibraryScope(col.LibraryID)        // smart scope is per-library
    page, err := f.ResolvePage(ctx, d.Pool, cursor, pageSize)
    if err != nil { return SmartListPage{}, err }

    items := make([]ItemView, 0, len(page.Rows))
    for _, r := range page.Rows {
        items = append(items, ItemView{VideoID: r.VideoID, Score: r.Score})
    }
    return SmartListPage{Items: items, NextCursor: page.NextCursor}, nil
}
```

### 4.3 `Freeze`

```go
func Freeze(ctx context.Context, d *handlers.Deps,
            collectionID uuid.UUID, actorID uuid.UUID) (n int, err error) {
    tx, err := d.Pool.Begin(ctx)
    if err != nil { return 0, err }
    defer tx.Rollback(ctx)
    q := d.Queries.WithTx(tx)

    col, err := q.GetCollection(ctx, collectionID)
    if err != nil { return 0, err }
    if !col.IsSmart {
        return 0, &ValidationError{Code: "collection-not-smart"}
    }

    f, _, err := filter.Parse(col.SmartQuery)
    if err != nil { return 0, err }
    f = f.WithLibraryScope(col.LibraryID)

    ids, err := f.ResolveAll(ctx, tx, FreezeMax)
    if err != nil { return 0, err }
    if len(ids) >= FreezeMax {
        return 0, &ValidationError{Code: "collection-too-large",
            Message: "smart query produced more than FREEZE_MAX rows; refine first"}
    }
    for i, vid := range ids {
        if _, err := q.InsertItemAtPosition(ctx, db.InsertItemAtPositionParams{
            CollectionID: collectionID, VideoID: vid,
            Position: int32((i + 1) * collections.PositionStep),
        }); err != nil { return 0, err }
    }
    if err := q.FreezeSmartCollection(ctx, collectionID); err != nil { return 0, err }
    if err := tx.Commit(ctx); err != nil { return 0, err }

    // Audit (Story 9.17).
    d.Audit.Write(ctx, audit.LibraryEvent{
        Event: "collection-frozen", LibraryID: col.LibraryID,
        ActorUserID: actorID,
        Payload: map[string]any{
            "collection_id": collectionID, "n_items": len(ids),
            "frozen_from_query": col.SmartQuery,
        },
    })
    return len(ids), nil
}
```

### 4.4 Smart-collection write guard

The trigger from Story 9.13 already blocks `INSERT/UPDATE` on
`collection_items` when the parent is `is_smart=true`. This story
inherits that protection — no new code.

## 5. Test plan

### 5.1 `smart_test.go`

| Test | What it pins |
|---|---|
| `TestGetSmartItems_MatchesSearchEndpoint` | Stand up a fixture library with 100 videos and varied tags/languages; create a smart collection with `smart_query = {"language":"ar"}`; `GET /api/collections/{id}/items` and `GET /api/videos?language=ar` return the same set in the same order. AC-1. |
| `TestGetSmartItems_RespectsLibraryScope` | Smart query without an explicit library filter still scopes to the collection's library. (No cross-library leakage.) |
| `TestGetSmartItems_NoCachingOfItems` | Insert a new video matching the query; subsequent GET returns it without any explicit invalidation. AC-2. |
| `TestGetSmartItems_CursorPagination` | 100 matching videos, page_size 25 → 4 pages with monotonically advancing cursors; last page returns `next_cursor=null`. |
| `TestGetSmartItems_QueryWithRemovedTagReturnsWarning` | Smart query references a tag that was deleted → 200 with `items: []` and `warning: "removed_tag:..."`. AC edge case. |
| `TestFreeze_HappyPath` | 50 matching videos → freeze → `is_smart=false`, `frozen_from_query` set, 50 rows in `collection_items` at positions 10..500. AC-3. |
| `TestFreeze_RejectsNonSmart` | Already-frozen collection → 422 `collection-not-smart`. |
| `TestFreeze_FrozenCollectionDoesNotChangeOnNewMatches` | Freeze → insert a new video that matches the query → frozen collection unchanged. AC-3 verbatim. |
| `TestFreeze_TooManyRows` | Smart query with > FREEZE_MAX rows → 422 `collection-too-large`. |
| `TestFreeze_AuditRowWritten` | After freeze, an `audit_log` row with `event='collection-frozen'`, payload includes `n_items` and `frozen_from_query`. |
| `TestSmartGuard_TriggerBlocksInsertOnSmart` | Direct `INSERT INTO collection_items` against a smart collection → CHECK violation (`collection_is_smart`). Carries over from Story 9.13's trigger. |
| `TestPagination_100kRowsHoldsUp` | Smart query that returns 100k matches; page through; each page < 100 ms. AC edge case. |

### 5.2 Filter parity test

`api/internal/search/filter/parity_test.go`:

| Test | What it pins |
|---|---|
| `TestFilter_VideosEndpoint_AndSmartCollectionResolveSameSet` | Same `filter` JSON used in both `/api/videos` and `/api/collections/{id}/items`; the underlying SQL emits the same WHERE clause and the same ORDER BY; result sets are byte-equal. |
| `TestFilter_RejectsUnknownKeyButSmartCollectionStillReturns200` | A smart_query with an unrecognized field returns a parse warning (not error) so AC-2 holds; the videos endpoint returns 422 (different error mode for live UI). The two paths agree on parsing but differ on error mapping; tests pin both. |

### 5.3 Migration test

| Test | What it pins |
|---|---|
| `test_consistency_chk_blocks_smart_with_null_query` | INSERT `is_smart=true, smart_query=NULL` → CHECK violation. |
| `test_consistency_chk_blocks_nonsmart_with_query` | INSERT `is_smart=false, smart_query='{}'` → CHECK violation. |
| `test_freeze_round_trip` | A frozen row has `is_smart=false`, `smart_query=NULL`, `frozen_from_query` set. |

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Smart query with 100k matches | Pagination holds; the resolver uses key-set cursors and never materializes in-memory. | `TestPagination_100kRowsHoldsUp` |
| Settings change invalidates a smart query (removed tag) | Live computation returns 200 with `items: []` and `warning`; no error. AC edge case. | `TestGetSmartItems_QueryWithRemovedTagReturnsWarning` |
| Freeze produces a snapshot; underlying catalog changes after | The frozen collection is a manual collection; it does not auto-update. AC-3. | `TestFreeze_FrozenCollectionDoesNotChangeOnNewMatches` |
| Smart query with `score` ordering (semantic search) | The cursor uses `(score, video_id)`; pagination is stable across reads (snapshot isolation). For very-frequent index updates, page boundaries can shift; documented. | Documented |
| User refreshes page during freeze | The freeze is a single tx; readers see either pre-freeze (smart) or post-freeze (manual). No partial states. | Implicit (tx isolation) |
| Direct DB write to `collection_items` for a smart collection | Trigger raises `collection_is_smart`. | `TestSmartGuard_TriggerBlocksInsertOnSmart` (Story 9.13) |

## 7. Configuration

| Key | Default | Effect |
|---|---|---|
| `collections.freeze_max` | 100,000 | Freeze refuses larger queries. |
| `collections.default_page_size` | 50 | Per-page items. |

## 8. Dependencies

| Dep | Source | Why |
|---|---|---|
| `internal/search/filter` | Epic 7 Story 7.9 | Single filter language. |
| `internal/cursor` | Epic 7 Story 7.2 | Cursor primitive. |
| `audit_log` | Story 9.17 | `collection-frozen` event. |
| Story 9.13 smart-guard trigger | already added | Blocks direct writes. |

## 9. Acceptance checklist

**Migration**
- [ ] `0042_collections_smart.sql` adds `smart_query`, `frozen_from_query`, and the consistency CHECK.

**Code**
- [ ] `api/internal/collections/smart.go` exposes `GetSmartItems`, `Freeze`.
- [ ] `ItemsHandler` switches between manual and smart based on `is_smart`.

**Behaviour (story acceptance criteria)**
- [ ] AC-1: smart-collection items match the saved-search response for the same query.
- [ ] AC-2: items are computed live; no caching; respects cursor pagination.
- [ ] AC-3: freeze materializes, flips `is_smart`, copies `smart_query` → `frozen_from_query`.

**Observability**
- [ ] Counter `smart_collection_freezes_total{outcome=ok|too_large|not_smart}`.
- [ ] Histogram `smart_collection_resolve_duration_seconds`.

**Docs**
- [ ] `specs/epics/09-library-management/README.md` ticks story 9.14.
- [ ] API docs explain the warning shape on a removed-tag query.
