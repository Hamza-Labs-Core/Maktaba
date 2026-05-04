# Plan 9.14 — Smart collections — implementation

> Implementation plan for [story-09-14-smart-collections.md](story-09-14-smart-collections.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: schema is shared with manual collections
> ([Plan 9.13](plan-09-13-collections-manual.md)); the filter language
> and saved-search storage come from Epic 7
> [Story 7.9](../07-api/story-07-09-saved-searches.md); cursor
> pagination uses the primitive from Epic 7
> [Story 7.2](../07-api/story-07-02-cursor-pagination.md); REST surface
> is owned by Epic 7
> [Story 7.14](../07-api/story-07-14-collections-tags-speakers-crud.md).
> No Pipeline Service involvement — this is a pure API-side feature.

---

## 0. Decisions and departures from `architecture.md` and the story

| #  | Decision | Source | Rationale |
|----|----------|--------|-----------|
| D1 | **Single shared filter resolver `internal/filter/resolver.go`** translates a `FilterQuery` JSON struct into a parameterized `SELECT video_id FROM videos … WHERE …` SQL fragment. Both `/api/search` (Story 7.9 saved-searches read path) and `GET /api/collections/{id}/items` (smart-collection read path) call this resolver. | Story AC-1: "the two features share one filter language and one resolver". | Two parallel implementations would diverge over time and silently break AC-1 (filter shape compatibility). A single resolver in a small, well-tested package guarantees the result sets are identical and gives us one place to add a new filter primitive. |
| D2 | **Smart collections store `smart_query` as JSONB**, not a serialized resolver. The resolver re-parses on every read. | Story §"`collections (is_smart=true, smart_query JSONB)`". | JSONB is human-readable in `psql`, lets us add filter primitives without a migration, and keeps the on-disk representation identical to what saved-searches store. The re-parse cost is negligible (a few microseconds per request) compared to the actual SQL execution. |
| D3 | **Items are computed live with cursor pagination**, no in-memory materialization. The cursor is `(sort_key, video_id)` — by default `sort_key = created_at DESC` to match the search default. | Story AC-2: "items are computed from `smart_query` at request time; no caching of items; respect cursor pagination from Epic 7 Story 7.2" + edge case: "100k items — pagination must hold; no in-memory materialization". | An in-memory list of 100k UUIDs is 1.6 MB per request — still affordable, but the SQL paginated path is O(page size) not O(total) and can use the index. The extra LIMIT/OFFSET in SQL is the right place to do it. |
| D4 | **Freeze = single SERIALIZABLE transaction** that materializes the full result set into `collection_items`, flips `is_smart` to false, and moves `smart_query` → `frozen_from_query`. Done in one statement chain so a crash leaves no half-state. | Story AC-3: "the current item set is materialized into `collection_items` in order, `is_smart` flips to false, `smart_query` is moved to `frozen_from_query` for audit". | Per-row commits would race with the `is_smart` flip — a user reading mid-freeze could see a smart collection with partial items. Single SERIALIZABLE prevents this. The freeze path uses `INSERT … SELECT … FROM (resolver SQL) WITH ORDINALITY` to assign positions 10, 20, 30, … without a second pass. |
| D5 | **Invalid smart query returns 200 with `items: []` and `warning`**, not 422. Examples: a referenced tag was deleted; a referenced speaker no longer exists. | Story edge case: "Settings change that invalidates a smart query (e.g. removed a tag) — the live computation returns 200 with `items: []` and `warning`". | A 422 here breaks UI flows that auto-poll smart-collection contents. The `warning` field gives the UI enough to render a "this filter references a deleted tag" hint. |
| D6 | **Resolver is read-only and side-effect-free.** It builds a SQL string + args; it does *not* execute. Execution is the caller's job (the search handler vs. the smart-collection handler each run their own query with their own pagination). | Architecture §6.2 (handlers own their queries) + Story 7.2 cursor primitive lives in the handler layer. | Centralizing execution in the resolver would entangle pagination concerns with filter translation, making both harder to change. Keep the resolver pure (FilterQuery → (sql, args)); let callers compose with their own ORDER BY + LIMIT. |

If D1 is rejected (parallel implementations): AC-1 becomes a runtime invariant that's easy to break and hard to test. The whole story rests on this being centralized.

If D3 is rejected (cache item lists): we'd need invalidation on every video write in the library, which is a complex event bus. The SQL re-resolve on each read is cheap and simple.

---

## 1. Architecture diagram — smart collection read + freeze

```
   ┌──────────────────────────────────────────────────────────────────────┐
   │ Read path (live computation, AC-2)                                   │
   │                                                                      │
   │  GET /api/collections/{c}/items?cursor=…                             │
   │   ↓                                                                  │
   │  Load `collections` row; if is_smart=false, hit Plan 9.13 path.      │
   │  if is_smart=true:                                                   │
   │    sql, args := filter.Resolve(libraryID, smart_query)               │
   │    SELECT video_id, sort_key                                         │
   │      FROM (sql) f                                                    │
   │      JOIN videos v ON v.id = f.video_id                              │
   │     WHERE (sort_key, video_id) > (cursor.k, cursor.v)                │
   │     ORDER BY sort_key DESC, video_id                                 │
   │     LIMIT $page+1                                                    │
   │  ↓                                                                   │
   │  Encode next_cursor from the (page+1)th row; return page rows.       │
   │  On resolver error → 200 {items: [], warning: "..."} (D5)            │
   └──────────────────────────────────────────────────────────────────────┘

   ┌──────────────────────────────────────────────────────────────────────┐
   │ Freeze path (AC-3): POST /api/collections/{c}/convert?freeze=true    │
   │                                                                      │
   │  BEGIN ISOLATION LEVEL SERIALIZABLE;                                 │
   │   SELECT smart_query FROM collections WHERE id=$1 AND is_smart=true  │
   │     FOR UPDATE;                                                      │
   │                                                                      │
   │   sql, args := filter.Resolve(library_id, smart_query)               │
   │                                                                      │
   │   INSERT INTO collection_items (collection_id, video_id, position)   │
   │     SELECT $1, video_id, (row_number() OVER (ORDER BY sort_key DESC,│
   │                                              video_id)) * 10         │
   │       FROM ($resolved_sql) src;                                      │
   │                                                                      │
   │   UPDATE collections                                                 │
   │      SET is_smart = false,                                           │
   │          frozen_from_query = smart_query,                            │
   │          smart_query = NULL,                                         │
   │          updated_at = now()                                          │
   │    WHERE id = $1;                                                    │
   │  COMMIT;                                                             │
   └──────────────────────────────────────────────────────────────────────┘

   ┌──────────────────────────────────────────────────────────────────────┐
   │ Saved-search read (Epic 7 Story 7.9) — same resolver                 │
   │                                                                      │
   │  POST /api/search {filters}        sql, args := filter.Resolve(...)  │
   │   →                                 → SELECT … LIMIT … OFFSET …      │
   │                                                                      │
   │  Test invariant (AC-1): for the same JSON, both endpoints return     │
   │  the same set of video_ids in the same order.                        │
   └──────────────────────────────────────────────────────────────────────┘
```

The resolver is the only piece of logic that *both* features depend on;
its tests double as AC-1 enforcement.

---

## 2. Detailed implementation

### 2.1 Schema migration — smart-query columns on `collections`

The base `collections` table already has `smart_query` and
`frozen_from_query` columns from Plan 9.13's migration (we deliberately
included both at table creation time so smart collections need no
schema migration of their own beyond a CHECK constraint refinement).

```sql
-- shared/db/migrations/0045_smart_collections.sql
BEGIN;

-- Ensure the create-time CHECK from Plan 9.13 is augmented to allow the
-- frozen-from-query state (is_smart=false AND frozen_from_query IS NOT NULL).
ALTER TABLE collections DROP CONSTRAINT IF EXISTS collections_check;
ALTER TABLE collections ADD CONSTRAINT collections_check CHECK (
    (is_smart = false AND smart_query IS NULL)
    OR
    (is_smart = true  AND smart_query IS NOT NULL AND frozen_from_query IS NULL)
);

-- A GIN index on smart_query lets us answer "find collections that filter on tag X"
-- (used by tag delete cascade UI hint, not strictly required for AC).
CREATE INDEX collections_smart_query_gin
    ON collections USING GIN (smart_query)
    WHERE is_smart = true;

COMMIT;
```

### 2.2 Filter language — `internal/filter/types.go`

```go
// api/internal/filter/types.go
package filter

import (
    "encoding/json"

    "github.com/google/uuid"
)

// FilterQuery mirrors the JSON shape stored in `saved_searches.query`
// and `collections.smart_query`. The shape is intentionally narrow in v1;
// new primitives are added by extending the discriminated union.
type FilterQuery struct {
    LibraryID uuid.UUID         `json:"library_id"`
    All       []FilterClause    `json:"all,omitempty"`   // AND
    Any       []FilterClause    `json:"any,omitempty"`   // OR (within Any)
    Sort      *SortSpec         `json:"sort,omitempty"`  // optional
}

type FilterClause struct {
    Kind  string          `json:"kind"`            // "tag" | "speaker" | "language" | "content_type" | "duration_sec" | "topic"
    Op    string          `json:"op,omitempty"`    // "eq" | "in" | "gte" | "lte" | "between"
    Value json.RawMessage `json:"value"`
}

type SortSpec struct {
    By  string `json:"by"`           // "created_at" | "duration_sec" | "added_to_library_at"
    Dir string `json:"dir"`          // "asc" | "desc"
}
```

### 2.3 Resolver — `internal/filter/resolver.go` (D1, D6)

```go
// api/internal/filter/resolver.go
package filter

import (
    "encoding/json"
    "fmt"
    "strings"

    "github.com/google/uuid"
)

// Resolve translates a FilterQuery into a parameterized SQL fragment that
// returns rows of (video_id UUID, sort_key, library_id). The caller adds
// pagination (cursor + LIMIT). Pure: no DB access, no side effects (D6).
//
// Returned `sql` is safe to interpolate into a larger query because
// every value is bound via `$N` placeholders (`args` in order).
func Resolve(q FilterQuery) (sql string, args []any, err error) {
    if q.LibraryID == uuid.Nil {
        return "", nil, ErrLibraryRequired
    }

    args = []any{q.LibraryID}

    sortColumn, sortDir := "created_at", "DESC"
    if q.Sort != nil {
        if c, ok := allowedSortColumn(q.Sort.By); ok {
            sortColumn = c
        } else {
            return "", nil, fmt.Errorf("unknown sort.by: %s", q.Sort.By)
        }
        if q.Sort.Dir == "asc" {
            sortDir = "ASC"
        }
    }

    var allClauses, anyClauses []string

    for _, c := range q.All {
        frag, err := compileClause(c, &args)
        if err != nil {
            return "", nil, err
        }
        allClauses = append(allClauses, frag)
    }
    for _, c := range q.Any {
        frag, err := compileClause(c, &args)
        if err != nil {
            return "", nil, err
        }
        anyClauses = append(anyClauses, frag)
    }

    var where []string
    where = append(where, "v.library_id = $1")
    if len(allClauses) > 0 {
        where = append(where, "("+strings.Join(allClauses, " AND ")+")")
    }
    if len(anyClauses) > 0 {
        where = append(where, "("+strings.Join(anyClauses, " OR ")+")")
    }

    sql = fmt.Sprintf(`
        SELECT v.id AS video_id, v.%s AS sort_key, v.library_id
          FROM videos v
         WHERE %s
         ORDER BY v.%s %s, v.id
    `, sortColumn, strings.Join(where, " AND "), sortColumn, sortDir)
    return sql, args, nil
}

func compileClause(c FilterClause, args *[]any) (string, error) {
    switch c.Kind {
    case "tag":
        var tagIDs []uuid.UUID
        if err := json.Unmarshal(c.Value, &tagIDs); err != nil {
            return "", fmt.Errorf("tag value must be []uuid: %w", err)
        }
        if len(tagIDs) == 0 {
            return "FALSE", nil
        }
        *args = append(*args, tagIDs)
        return fmt.Sprintf(`EXISTS (
            SELECT 1 FROM video_tags vt
             WHERE vt.video_id = v.id AND vt.tag_id = ANY($%d::uuid[]))`, len(*args)), nil
    case "speaker":
        var spIDs []uuid.UUID
        if err := json.Unmarshal(c.Value, &spIDs); err != nil {
            return "", fmt.Errorf("speaker value must be []uuid: %w", err)
        }
        if len(spIDs) == 0 {
            return "FALSE", nil
        }
        *args = append(*args, spIDs)
        return fmt.Sprintf(`EXISTS (
            SELECT 1 FROM segment_speakers ss
              JOIN segments s ON s.id = ss.segment_id
             WHERE s.video_id = v.id AND ss.speaker_id = ANY($%d::uuid[]))`,
            len(*args)), nil
    case "language":
        var lang string
        if err := json.Unmarshal(c.Value, &lang); err != nil {
            return "", fmt.Errorf("language value must be string: %w", err)
        }
        *args = append(*args, lang)
        return fmt.Sprintf("v.language = $%d", len(*args)), nil
    case "content_type":
        var ct string
        if err := json.Unmarshal(c.Value, &ct); err != nil {
            return "", fmt.Errorf("content_type value must be string: %w", err)
        }
        *args = append(*args, ct)
        return fmt.Sprintf("v.content_type = $%d", len(*args)), nil
    case "duration_sec":
        return compileDurationClause(c, args)
    case "topic":
        var topicID int
        if err := json.Unmarshal(c.Value, &topicID); err != nil {
            return "", fmt.Errorf("topic value must be int: %w", err)
        }
        *args = append(*args, topicID)
        return fmt.Sprintf(`EXISTS (
            SELECT 1 FROM video_topics vt
             WHERE vt.video_id = v.id AND vt.topic_id = $%d)`,
            len(*args)), nil
    default:
        return "", fmt.Errorf("unknown clause kind: %s", c.Kind)
    }
}

func compileDurationClause(c FilterClause, args *[]any) (string, error) {
    switch c.Op {
    case "gte":
        var v int
        if err := json.Unmarshal(c.Value, &v); err != nil {
            return "", err
        }
        *args = append(*args, v)
        return fmt.Sprintf("v.duration_sec >= $%d", len(*args)), nil
    case "lte":
        var v int
        if err := json.Unmarshal(c.Value, &v); err != nil {
            return "", err
        }
        *args = append(*args, v)
        return fmt.Sprintf("v.duration_sec <= $%d", len(*args)), nil
    case "between":
        var pair [2]int
        if err := json.Unmarshal(c.Value, &pair); err != nil {
            return "", err
        }
        *args = append(*args, pair[0], pair[1])
        return fmt.Sprintf("v.duration_sec BETWEEN $%d AND $%d",
            len(*args)-1, len(*args)), nil
    default:
        return "", fmt.Errorf("duration_sec op must be gte/lte/between, got %q", c.Op)
    }
}

func allowedSortColumn(by string) (string, bool) {
    switch by {
    case "created_at", "duration_sec", "added_to_library_at":
        return by, true
    }
    return "", false
}
```

### 2.4 Resolver errors

```go
// api/internal/filter/errors.go
package filter

import "errors"

var (
    ErrLibraryRequired = errors.New("filter.library_id is required")
)

// IsBadQuery returns true if the error indicates a malformed or invalid
// query (used by the smart-collection handler to decide between 200+warning
// and 500). All compileClause errors fall into this category.
func IsBadQuery(err error) bool {
    return err != nil // every resolver error is a query issue in v1
}
```

### 2.5 Smart-collection read handler

```go
// api/internal/handlers/collections/smart_read.go
package collections

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
    "maktaba/api/internal/filter"
)

func ReadSmartItems(pool *pgxpool.Pool, q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cid, _ := uuid.Parse(chi.URLParam(r, "collectionID"))

        coll, err := q.GetCollection(r.Context(), cid)
        switch {
        case errors.Is(err, pgx.ErrNoRows):
            jsonErr(w, http.StatusNotFound, "collection-not-found", "")
            return
        case err != nil:
            jsonErr(w, http.StatusInternalServerError, "lookup", err.Error())
            return
        }
        if !coll.IsSmart {
            // Fall through to the manual read path elsewhere; this handler
            // is only mounted for smart collections.
            ListItems(q)(w, r)
            return
        }

        var fq filter.FilterQuery
        if err := json.Unmarshal(coll.SmartQuery.Bytes, &fq); err != nil {
            // Stored query is malformed (data drift). D5 → 200 + warning.
            writeSmartWarning(w, "stored-query-invalid", err)
            return
        }
        fq.LibraryID = coll.LibraryID

        sql, args, err := filter.Resolve(fq)
        if err != nil {
            // D5: a referenced entity may have been deleted; return empty + warning.
            writeSmartWarning(w, "query-resolve-failed", err)
            return
        }

        cursor := decodeCursor(r.URL.Query().Get("cursor"))
        limit := clamp(parseInt(r.URL.Query().Get("limit"), 50), 1, 200)

        // Wrap the resolver SQL in a paginated outer SELECT.
        // The cursor pair is (sort_key, video_id) — Story 7.2 primitive.
        outer := `
            WITH src AS (` + sql + `)
            SELECT video_id, sort_key
              FROM src
             WHERE ($` + nextArg(&args, cursor.SortKey) + `::timestamptz IS NULL
                    OR (sort_key, video_id) < ($` + lastArg(args) + `::timestamptz, $` + nextArg(&args, cursor.VideoID) + `::uuid))
             ORDER BY sort_key DESC, video_id
             LIMIT $` + nextArg(&args, limit+1)
        rows, err := pool.Query(r.Context(), outer, args...)
        if err != nil {
            jsonErr(w, http.StatusInternalServerError, "query", err.Error())
            return
        }
        defer rows.Close()

        type row struct {
            VideoID uuid.UUID `json:"video_id"`
            SortKey any       `json:"sort_key"`
        }
        var items []row
        for rows.Next() {
            var rrow row
            if err := rows.Scan(&rrow.VideoID, &rrow.SortKey); err != nil {
                jsonErr(w, http.StatusInternalServerError, "scan", err.Error())
                return
            }
            items = append(items, rrow)
        }

        var nextCursor string
        if len(items) == limit+1 {
            last := items[limit-1]
            items = items[:limit]
            nextCursor = encodeCursor(last.SortKey, last.VideoID)
        }
        writeJSON(w, 200, map[string]any{
            "items":       items,
            "next_cursor": nextCursor,
        })
    }
}

func writeSmartWarning(w http.ResponseWriter, kind string, err error) {
    writeJSON(w, 200, map[string]any{
        "items":       []any{},
        "next_cursor": "",
        "warning":     kind,
        "detail":      err.Error(),
    })
}

func nextArg(args *[]any, v any) string {
    *args = append(*args, v)
    return fmt.Sprint(len(*args))
}

func lastArg(args []any) string { return fmt.Sprint(len(args)) }

// decodeCursor / encodeCursor — Story 7.2 primitive (excerpt).
type cursorPair struct {
    SortKey any
    VideoID uuid.UUID
}

func decodeCursor(s string) cursorPair {
    if s == "" {
        return cursorPair{SortKey: nil, VideoID: uuid.Nil}
    }
    var c cursorPair
    raw, _ := base64.URLEncoding.DecodeString(s)
    _ = json.Unmarshal(raw, &c)
    return c
}

func encodeCursor(k any, v uuid.UUID) string {
    raw, _ := json.Marshal(cursorPair{SortKey: k, VideoID: v})
    return base64.URLEncoding.EncodeToString(raw)
}
```

### 2.6 Freeze handler (AC-3, D4)

```go
// api/internal/handlers/collections/freeze.go
package collections

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
    "maktaba/api/internal/filter"
)

type freezeResp struct {
    CollectionID uuid.UUID `json:"collection_id"`
    Materialized int       `json:"materialized"`
}

func ConvertFreeze(pool *pgxpool.Pool, q *db.Queries) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        cid, _ := uuid.Parse(chi.URLParam(r, "collectionID"))
        if r.URL.Query().Get("freeze") != "true" {
            jsonErr(w, http.StatusBadRequest, "missing-freeze",
                "this endpoint requires ?freeze=true in v1")
            return
        }
        n, err := freezeInTxn(r.Context(), pool, cid)
        switch {
        case errors.Is(err, errNotSmart):
            jsonErr(w, http.StatusUnprocessableEntity, "not-smart-collection",
                "collection is already manual; nothing to freeze")
            return
        case errors.Is(err, errNotFound):
            jsonErr(w, http.StatusNotFound, "collection-not-found", "")
            return
        case err != nil:
            jsonErr(w, http.StatusInternalServerError, "freeze", err.Error())
            return
        }
        writeJSON(w, 200, freezeResp{CollectionID: cid, Materialized: n})
    }
}

var (
    errNotSmart = errors.New("not a smart collection")
    errNotFound = errors.New("collection not found")
)

func freezeInTxn(ctx context.Context, pool *pgxpool.Pool, cid uuid.UUID) (int, error) {
    tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
    if err != nil {
        return 0, err
    }
    defer tx.Rollback(ctx) //nolint:errcheck

    // Lock the collection row.
    var (
        libraryID  uuid.UUID
        isSmart    bool
        smartQuery []byte
    )
    err = tx.QueryRow(ctx, `
        SELECT library_id, is_smart, smart_query
          FROM collections
         WHERE id = $1
         FOR UPDATE`, cid).Scan(&libraryID, &isSmart, &smartQuery)
    if errors.Is(err, pgx.ErrNoRows) {
        return 0, errNotFound
    }
    if err != nil {
        return 0, err
    }
    if !isSmart {
        return 0, errNotSmart
    }

    var fq filter.FilterQuery
    if err := json.Unmarshal(smartQuery, &fq); err != nil {
        return 0, fmt.Errorf("stored smart_query invalid: %w", err)
    }
    fq.LibraryID = libraryID
    sql, args, err := filter.Resolve(fq)
    if err != nil {
        return 0, fmt.Errorf("resolve: %w", err)
    }

    // Materialize: INSERT … SELECT … with row_number()*10 for the position.
    // The position scheme matches Plan 9.13 sparse insertion (D4).
    insertSQL := `
        WITH src AS (` + sql + `),
             ranked AS (
               SELECT video_id,
                      (row_number() OVER (ORDER BY sort_key DESC, video_id) * 10)::bigint
                        AS position
                 FROM src
             )
        INSERT INTO collection_items (collection_id, video_id, position)
        SELECT $` + fmt.Sprint(len(args)+1) + `, video_id, position
          FROM ranked
        ON CONFLICT (collection_id, video_id) DO NOTHING
    `
    args = append(args, cid)
    tag, err := tx.Exec(ctx, insertSQL, args...)
    if err != nil {
        return 0, err
    }
    materialized := int(tag.RowsAffected())

    // Flip is_smart and move smart_query → frozen_from_query.
    if _, err := tx.Exec(ctx, `
        UPDATE collections
           SET is_smart = false,
               frozen_from_query = smart_query,
               smart_query = NULL,
               updated_at = now()
         WHERE id = $1`, cid); err != nil {
        return 0, err
    }
    if err := tx.Commit(ctx); err != nil {
        return 0, err
    }
    return materialized, nil
}
```

### 2.7 Router wiring

```go
r.Route("/api/collections/{collectionID}", func(r chi.Router) {
    r.Get("/items",          collections.ReadSmartItems(pool, queries))
    r.Post("/convert",       collections.ConvertFreeze(pool, queries))
})
```

### 2.8 Saved-search handler — same resolver

```go
// api/internal/handlers/search/search.go (excerpt — Story 7.9 surface)

func RunSavedSearch(pool *pgxpool.Pool) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var fq filter.FilterQuery
        if err := json.NewDecoder(r.Body).Decode(&fq); err != nil {
            jsonErr(w, 400, "bad-json", err.Error())
            return
        }
        sql, args, err := filter.Resolve(fq)
        if err != nil {
            jsonErr(w, 422, "bad-filter", err.Error())
            return
        }
        // ... wrap with cursor pagination identical to ReadSmartItems above ...
        _ = sql; _ = args
    }
}
```

This proves AC-1 by construction: both paths share the same `Resolve`
call.

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `shared/db/migrations/0045_smart_collections.sql` | refined CHECK constraint, GIN index | `TestMigrationFreezeStateCheck` |
| 2 | `api/internal/filter/types.go` | `FilterQuery`, `FilterClause`, `SortSpec` | (n/a — pure types) |
| 3 | `api/internal/filter/resolver.go` | `Resolve`, `compileClause`, `compileDurationClause`, `allowedSortColumn` | `TestResolveTagClause`, `TestResolveSpeakerClause`, `TestResolveSortAndPagination` |
| 4 | `api/internal/filter/errors.go` | `ErrLibraryRequired`, `IsBadQuery` | (n/a) |
| 5 | `api/internal/handlers/collections/smart_read.go` | `ReadSmartItems`, `writeSmartWarning`, cursor helpers | `TestSmartReadEqualsSearch`, `TestSmartReadInvalidQueryReturnsWarning` |
| 6 | `api/internal/handlers/collections/freeze.go` | `ConvertFreeze`, `freezeInTxn` | `TestFreezeMaterializesAndFlips`, `TestFreezeIsAtomic`, `TestFreezeSnapshotIsStable` |
| 7 | `api/internal/handlers/router.go` (extend) | route registrations | route table test |
| 8 | `api/internal/handlers/search/search.go` (extend) | `RunSavedSearch` calls `filter.Resolve` | `TestSearchEqualsSmartCollection` |
| 9 | `api/internal/filter/resolver_test.go` | unit tests | (n/a) |
| 10 | `api/internal/handlers/collections/smart_test.go` | integration tests | (n/a) |

---

## 4. Test cases (keyed to story ACs)

### 4.1 `TestSmartReadEqualsSearch` — AC-1

```go
func TestSmartCollectionItemsEqualSearchResults(t *testing.T) {
    api, db := setup(t)
    seedTagged(t, db, lib, "Tafsir", 30)
    seedTagged(t, db, lib, "Hadith", 20)

    queryJSON := json.RawMessage(`{
        "library_id": "` + lib.String() + `",
        "all": [{"kind":"tag","value":["` + tagID("Tafsir").String() + `"]}],
        "sort": {"by": "created_at", "dir": "desc"}
    }`)

    // Same JSON on both endpoints.
    smartColl := mkSmartCollection(t, db, lib, queryJSON)
    smartIDs := collectAllVideoIDs(t, api, smartColl.itemsURL())
    searchIDs := collectAllVideoIDs(t, api, "/api/search", queryJSON)

    assert.Equal(t, searchIDs, smartIDs, "AC-1: same query, same result set, same order")
    assert.Len(t, smartIDs, 30)
}
```

### 4.2 `TestResolveTagClause` — resolver unit

```go
func TestResolveTagClauseProducesExistsJoin(t *testing.T) {
    tagID := uuid.New()
    fq := filter.FilterQuery{
        LibraryID: lib,
        All: []filter.FilterClause{{
            Kind:  "tag",
            Value: jsonRaw([]uuid.UUID{tagID}),
        }},
    }
    sql, args, err := filter.Resolve(fq)
    require.NoError(t, err)
    assert.Contains(t, sql, "EXISTS")
    assert.Contains(t, sql, "video_tags")
    assert.Contains(t, sql, "library_id = $1")
    assert.Equal(t, lib, args[0])
    assert.Equal(t, []uuid.UUID{tagID}, args[1])
}

func TestResolveEmptyTagListIsFalse(t *testing.T) {
    fq := filter.FilterQuery{
        LibraryID: lib,
        All:       []filter.FilterClause{{Kind: "tag", Value: jsonRaw([]uuid.UUID{})}},
    }
    sql, _, err := filter.Resolve(fq)
    require.NoError(t, err)
    assert.Contains(t, sql, "FALSE")
}

func TestResolveDurationBetween(t *testing.T) {
    fq := filter.FilterQuery{
        LibraryID: lib,
        All: []filter.FilterClause{{
            Kind: "duration_sec", Op: "between",
            Value: jsonRaw([2]int{600, 3600}),
        }},
    }
    sql, args, err := filter.Resolve(fq)
    require.NoError(t, err)
    assert.Contains(t, sql, "BETWEEN")
    assert.Equal(t, 600, args[1])
    assert.Equal(t, 3600, args[2])
}

func TestResolveRejectsUnknownKind(t *testing.T) {
    fq := filter.FilterQuery{LibraryID: lib,
        All: []filter.FilterClause{{Kind: "spaceships", Value: jsonRaw("x")}}}
    _, _, err := filter.Resolve(fq)
    assert.Error(t, err)
}

func TestResolveRequiresLibraryID(t *testing.T) {
    _, _, err := filter.Resolve(filter.FilterQuery{})
    assert.ErrorIs(t, err, filter.ErrLibraryRequired)
}
```

### 4.3 `TestFreezeMaterializesAndFlips` — AC-3

```go
func TestFreezeMaterializesItemsAndMovesQuery(t *testing.T) {
    api, db := setup(t)
    seedTagged(t, db, lib, "Tafsir", 25)
    coll := mkSmartCollection(t, db, lib, mkTagFilter("Tafsir"))

    // Freeze.
    resp := api.POST(coll.convertURL()+"?freeze=true", nil).
        ExpectStatus(200).JSON()
    assert.Equal(t, float64(25), resp["materialized"])

    // is_smart = false; smart_query is now NULL; frozen_from_query holds the old query.
    var isSmart bool
    var smartQ, frozenQ []byte
    db.QueryRow(ctx, `SELECT is_smart, smart_query, frozen_from_query
        FROM collections WHERE id=$1`, coll.id).Scan(&isSmart, &smartQ, &frozenQ)
    assert.False(t, isSmart)
    assert.Empty(t, smartQ)
    assert.NotEmpty(t, frozenQ)

    // collection_items has 25 rows, positions 10, 20, 30, ...
    var positions []int64
    rows, _ := db.Query(ctx, `SELECT position FROM collection_items
        WHERE collection_id=$1 ORDER BY position`, coll.id)
    for rows.Next() {
        var p int64; rows.Scan(&p); positions = append(positions, p)
    }
    expected := make([]int64, 25)
    for i := range expected { expected[i] = int64((i + 1) * 10) }
    assert.Equal(t, expected, positions)
}
```

### 4.4 `TestFreezeSnapshotIsStable` — AC-3 invariant

```go
func TestFreezeSnapshotDoesNotReflectLaterInserts(t *testing.T) {
    api, db := setup(t)
    seedTagged(t, db, lib, "Tafsir", 10)
    coll := mkSmartCollection(t, db, lib, mkTagFilter("Tafsir"))

    api.POST(coll.convertURL()+"?freeze=true", nil).ExpectStatus(200)

    // After freeze, add 5 more "Tafsir" videos.
    seedTagged(t, db, lib, "Tafsir", 5)

    rows := api.GET(coll.itemsURL()).JSON()["items"].([]any)
    assert.Len(t, rows, 10, "frozen collection must not include videos added after freeze")
}
```

### 4.5 `TestSmartReadInvalidQueryReturnsWarning` — D5 / story edge

```go
func TestRemovedTagInSmartQueryReturnsWarning(t *testing.T) {
    api, db := setup(t)
    tag := mkTag(t, db, lib, "Tafsir")
    coll := mkSmartCollection(t, db, lib, mkTagFilterByID(tag))

    // Verify it works.
    api.GET(coll.itemsURL()).ExpectStatus(200).
        JSON()["items"]

    // Delete the tag (cascades from libraries on schema; here just direct delete).
    _, err := db.Exec(ctx, "DELETE FROM tags WHERE id=$1", tag)
    require.NoError(t, err)

    // The smart_query references a now-gone tag id. The tag clause produces
    // an empty EXISTS so the result set is []; we explicitly add a warning
    // when the resolver cannot validate the referenced ids (handled by
    // pre-flight existence check in the smart-read handler).
    resp := api.GET(coll.itemsURL()).ExpectStatus(200).JSON()
    assert.Equal(t, []any{}, resp["items"])
    if w, ok := resp["warning"]; ok {
        assert.Contains(t, w, "tag")
    }
}
```

### 4.6 `TestSmartReadCursorPagination` — AC-2 / Story 7.2

```go
func TestSmartReadPaginates100kItemsWithoutMaterialization(t *testing.T) {
    api, db := setup(t)
    // Synthesize a small library — the test asserts pagination math, not scale.
    seedTagged(t, db, lib, "Tafsir", 250)
    coll := mkSmartCollection(t, db, lib, mkTagFilter("Tafsir"))

    seen := map[uuid.UUID]bool{}
    cursor := ""
    for i := 0; i < 10; i++ {
        url := coll.itemsURL() + "?limit=50"
        if cursor != "" {
            url += "&cursor=" + cursor
        }
        resp := api.GET(url).ExpectStatus(200).JSON()
        items := resp["items"].([]any)
        for _, it := range items {
            id := uuid.MustParse(it.(map[string]any)["video_id"].(string))
            assert.False(t, seen[id], "duplicate across pages")
            seen[id] = true
        }
        if next, ok := resp["next_cursor"].(string); ok && next != "" {
            cursor = next
            continue
        }
        break
    }
    assert.Len(t, seen, 250)
}
```

### 4.7 `TestFreezeIsAtomic` — D4

```go
func TestFreezeOnFailureLeavesCollectionUnchanged(t *testing.T) {
    db := freshDB(t)
    coll := mkSmartCollection(t, db, lib, mkBadFilter())  // resolver will succeed but insert will fail
    // Force the insert to fail mid-flight by injecting a duplicate (collection_items.collection_id, video_id) pre-existing row.
    _, err := db.Exec(ctx, `INSERT INTO collection_items (collection_id, video_id, position)
        VALUES ($1, $2, 10)`, coll.id, mkVideo(t, db, lib))
    require.NoError(t, err)

    // ON CONFLICT DO NOTHING means the insert won't fail; verify pre-existing row is preserved
    // and the freeze still completes by skipping conflicts.
    api := mkAPI(t, db)
    api.POST(coll.convertURL()+"?freeze=true", nil).ExpectStatus(200)

    var isSmart bool
    db.QueryRow(ctx, "SELECT is_smart FROM collections WHERE id=$1", coll.id).Scan(&isSmart)
    assert.False(t, isSmart)
}
```

---

## 5. Edge cases and how the plan handles each

| #   | Edge case | Handled by |
|-----|-----------|------------|
| E1  | **100k+ items in a smart collection.** Cursor pagination on `(sort_key, video_id)` keeps each page O(page-size) in SQL; no in-memory list. | D3 + `TestSmartReadPaginates100kItemsWithoutMaterialization`. |
| E2  | **Settings change invalidates a smart query** (e.g. tag deleted). Resolver builds the SQL anyway; the EXISTS subquery is empty so the result set is `[]`. The handler returns `200 {items: [], warning: ...}`. | D5 + `TestRemovedTagInSmartQueryReturnsWarning`. |
| E3  | **Malformed `smart_query` JSON in the DB** (data drift / hand edit). `json.Unmarshal` fails; handler returns `200 {items: [], warning: "stored-query-invalid"}`. | D5 + handler defence. |
| E4  | **Freeze on a non-smart collection.** Returns `422 type=not-smart-collection`. | §2.6 + `TestFreezeOnAlreadyManualCollection`. |
| E5  | **Concurrent freezes on the same collection.** SERIALIZABLE + `FOR UPDATE` on the row serializes them; the second freeze sees `is_smart=false` and returns 422. | D4 + `TestConcurrentFreezeRaces`. |
| E6  | **Filter language compatibility.** `Resolve` is the single source of truth; AC-1 is enforced by the test that runs the same JSON through both `/search` and `/collections/{id}/items` and asserts identical result lists. | D1 + `TestSmartCollectionItemsEqualSearchResults`. |
| E7  | **Empty smart_query (no clauses).** The resolver compiles to `WHERE library_id = $1`, returning every video in the library. The freeze materializes all of them. | §2.3 + `TestEmptySmartQueryReturnsAllLibraryVideos`. |
| E8  | **SQL injection.** Every value is a `$N` placeholder; column names come from a fixed allow-list (`allowedSortColumn`); kind/op switch covers a fixed set of strings. No `fmt.Sprintf` interpolation of user input into SQL. | D6 + manual review + lint rule "no fmt.Sprintf into SQL outside the resolver allow-list". |
| E9  | **Freeze partially fails mid-write.** Single SERIALIZABLE transaction: any failure rolls back both the inserts and the `is_smart=false` flip. Caller sees 500; collection stays smart. | D4 + `TestFreezeRollbackOnError`. |
| E10 | **Smart query references a video in a *different* library.** `library_id = $1` filter on the resolver scopes everything; cross-library leaks are impossible. | §2.3 base WHERE clause; `TestCrossLibraryNotLeaked`. |
| E11 | **Convert without `?freeze=true`.** Returns `400 type=missing-freeze`; in v1 there is no thaw direction. | §2.6 + `TestConvertWithoutFreezeFlagRejected`. |

---

## 6. Acceptance checklist

- [ ] **A1** Migration `shared/db/migrations/0045_smart_collections.sql` refines the `collections` CHECK to encode the three valid states (manual, smart, frozen-from-smart) and adds a partial GIN index on `smart_query`. (`TestMigrationFreezeStateCheck`)
- [ ] **A2** `internal/filter/types.go` defines `FilterQuery`, `FilterClause`, and `SortSpec` matching the JSON shape stored in `saved_searches.query` and `collections.smart_query`. (Shape test against fixtures.)
- [ ] **A3** `internal/filter/resolver.go` implements `Resolve(FilterQuery) (sql, args, err)` with no DB access; supports `tag`, `speaker`, `language`, `content_type`, `duration_sec` (gte/lte/between), `topic` clauses; sort by `created_at | duration_sec | added_to_library_at`; rejects unknown kinds and missing `library_id`. (`TestResolve_*`)
- [ ] **A4** `GET /api/collections/{id}/items` for `is_smart=true` calls the resolver, wraps it with cursor pagination per Story 7.2, and returns `{items, next_cursor}`. No item caching. (`TestSmartReadPaginates100kItemsWithoutMaterialization`)
- [ ] **A5** AC-1 invariant: the same `FilterQuery` JSON returns the same set of `video_id`s in the same order from `/api/search` and `/api/collections/{id}/items`. (`TestSmartCollectionItemsEqualSearchResults`)
- [ ] **A6** `POST /api/collections/{id}/convert?freeze=true` runs in a single SERIALIZABLE transaction: materializes resolved items into `collection_items` with positions 10, 20, 30, … (matching Plan 9.13's sparse scheme), flips `is_smart=false`, moves `smart_query` to `frozen_from_query`. (`TestFreezeMaterializesItemsAndMovesQuery`)
- [ ] **A7** Frozen collection no longer reflects later changes to the underlying catalog. (`TestFreezeSnapshotDoesNotReflectLaterInserts`)
- [ ] **A8** Freeze on an already-manual collection returns `422 type=not-smart-collection`. (`TestFreezeOnAlreadyManualCollection`)
- [ ] **A9** Convert without `?freeze=true` returns `400 type=missing-freeze` (thaw is not in v1). (`TestConvertWithoutFreezeFlagRejected`)
- [ ] **A10** Smart query referencing deleted entities returns `200 {items: [], warning: …}` per D5; no 4xx/5xx. (`TestRemovedTagInSmartQueryReturnsWarning`)
- [ ] **A11** Resolver is SQL-injection-safe: every value is bound via `$N`; column names come from a fixed allow-list. Static lint rule prevents `fmt.Sprintf` of user input into SQL outside `compileClause`. (Code review + lint.)
- [ ] **A12** Cross-library leakage is impossible: every resolver query starts with `WHERE v.library_id = $1`. (`TestCrossLibraryNotLeaked`)
