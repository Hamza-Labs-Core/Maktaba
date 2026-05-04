# Implementation Plan — Story 7.14 Collections, Tags, Speakers

> Companion to [story-07-14-collections-tags-speakers.md](story-07-14-collections-tags-speakers.md).
> Three small CRUD surfaces sharing a curation theme.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | Collections: `GET/POST /api/collections`, `GET/PATCH/DELETE /api/collections/{id}`, `GET/POST /api/collections/{id}/items`, `PATCH /api/collections/{id}/items/{video_id}`, `DELETE /api/collections/{id}/items/{video_id}`. Tags: `PATCH /api/videos/{id}/tags`, `GET /api/tags`. Speakers: `POST /api/speakers/merge`, `PATCH /api/speakers/{id}`, `GET /api/speakers`. |
| Storage | Schema lives in Epic 9 (Stories 9.12–9.14); we read/write through it. |
| Smart collections | `is_smart` + `smart_query` JSON. The runtime translation reuses Story 7.8's filter pipeline. |
| Out of scope | The actual smart-query language (Epic 9 Story 9.14 owns), tag normalization SQL constraints (Epic 9 Story 9.12). |

## 1. Architecture diagram

```
   GET /api/collections/{id}
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ if is_smart=false:                                          │
   │   SELECT v.* FROM collection_items ci                       │
   │     JOIN videos v ON v.id = ci.video_id                     │
   │    WHERE ci.collection_id = $1                              │
   │    ORDER BY ci.position, ci.video_id                        │
   │   (paginate.Where applied; cursor encodes position+video_id)│
   │                                                             │
   │ if is_smart=true:                                           │
   │   parse smart_query JSON →                                  │
   │   call shared.search.QueryBuilder(filters)                  │
   │   query videos with the same WHERE shape                    │
   │   if parse fails → return [] + warning                      │
   └─────────────────────────────────────────────────────────────┘

   POST /api/speakers/merge { keep, drop }
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ Tx:                                                         │
   │   UPDATE segment_speakers SET speaker_id = $keep            │
   │     WHERE speaker_id = $drop;                               │
   │   DELETE FROM speakers WHERE id = $drop;                    │
   │ Affected rows from the UPDATE returned.                     │
   └─────────────────────────────────────────────────────────────┘

   PATCH /api/videos/{id}/tags { add: [...], remove: [...] }
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ Tx:                                                         │
   │   for tag in add:  ensure tag row, INSERT INTO video_tags   │
   │                    (video_id, tag_id) ON CONFLICT DO NOTHING│
   │   for tag in remove: DELETE FROM video_tags                 │
   │                       WHERE video_id=$1 AND tag_id IN(...); │
   └─────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/collections/handler.go` | Routes. |
| `api/internal/collections/items.go` | Item add/remove/reorder. |
| `api/internal/collections/smart.go` | Smart-query evaluation. |
| `api/internal/tags/handler.go` | Routes (delta semantics). |
| `api/internal/tags/normalize.go` | NFC + trim + case-fold for unique-checks. |
| `api/internal/speakers/handler.go` | Merge + rename. |
| `api/internal/{collections,tags,speakers}/types.go` | DTOs. |
| `api/internal/{collections,tags,speakers}/*_test.go` | Tests. |
| `shared/db/queries/{collections,tags,speakers}.sql` | sqlc inputs. |

## 3. SQL — sqlc inputs

`shared/db/queries/collections.sql`:

```sql
-- name: CreateCollection :one
INSERT INTO collections (id, name, is_smart, smart_query, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now())
RETURNING *;

-- name: AddCollectionItem :exec
INSERT INTO collection_items (collection_id, video_id, position, added_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (collection_id, video_id) DO UPDATE
   SET position = EXCLUDED.position;

-- name: ReorderCollectionItem :exec
UPDATE collection_items SET position = $3
 WHERE collection_id = $1 AND video_id = $2;

-- name: RemoveCollectionItem :exec
DELETE FROM collection_items
 WHERE collection_id = $1 AND video_id = $2;

-- name: ListCollectionItems :many
SELECT v.*, ci.position
  FROM collection_items ci
  JOIN videos v ON v.id = ci.video_id
 WHERE ci.collection_id = $1
   AND (ci.position, ci.video_id) > ($2, $3)
 ORDER BY ci.position, ci.video_id
 LIMIT $4;
```

`shared/db/queries/tags.sql`:

```sql
-- name: UpsertTag :one
INSERT INTO tags (id, name, name_fold, created_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (name_fold) DO UPDATE SET name = tags.name
RETURNING *;

-- name: AddVideoTag :exec
INSERT INTO video_tags (video_id, tag_id)
VALUES ($1, $2)
ON CONFLICT (video_id, tag_id) DO NOTHING;

-- name: RemoveVideoTag :exec
DELETE FROM video_tags
 WHERE video_id = $1 AND tag_id = ANY($2::uuid[]);

-- name: ListTagsForVideo :many
SELECT t.id, t.name
  FROM video_tags vt JOIN tags t ON t.id = vt.tag_id
 WHERE vt.video_id = $1
 ORDER BY t.name;

-- name: ListAllTags :many
SELECT t.*, count(vt.video_id) AS use_count
  FROM tags t LEFT JOIN video_tags vt ON vt.tag_id = t.id
 GROUP BY t.id
 ORDER BY t.name;
```

`shared/db/queries/speakers.sql`:

```sql
-- name: MergeSpeakers :one
WITH moved AS (
  UPDATE segment_speakers SET speaker_id = $1
   WHERE speaker_id = $2
   RETURNING segment_id
)
DELETE FROM speakers WHERE id = $2
RETURNING (SELECT count(*)::int FROM moved) AS affected_segments;

-- name: RenameSpeaker :one
UPDATE speakers SET name = $2, updated_at = now()
 WHERE id = $1
RETURNING *;

-- name: SpeakerByName :one
SELECT * FROM speakers WHERE name = $1;
```

## 4. Type definitions

```go
// api/internal/collections/types.go
package collections

import "encoding/json"
import "github.com/google/uuid"

type Collection struct {
    ID         uuid.UUID       `json:"id"`
    Name       string          `json:"name"`
    IsSmart    bool            `json:"is_smart"`
    SmartQuery json.RawMessage `json:"smart_query,omitempty"`
}

type ItemPatch struct {
    Position int `json:"position" validate:"gte=0"`
}

// api/internal/tags/types.go
type TagDelta struct {
    Add    []string `json:"add,omitempty"`
    Remove []string `json:"remove,omitempty"`
}

// api/internal/speakers/types.go
type MergeRequest struct {
    Keep uuid.UUID `json:"keep" validate:"required"`
    Drop uuid.UUID `json:"drop" validate:"required"`
}

type MergeResponse struct {
    AffectedSegments int `json:"affected_segments"`
}
```

## 5. Tag normalization

```go
// api/internal/tags/normalize.go
package tags

import (
    "strings"
    "golang.org/x/text/cases"
    "golang.org/x/text/language"
    "golang.org/x/text/unicode/norm"
)

// Normalize returns the form used as the unique key. Display value keeps
// original casing.
func Normalize(s string) string {
    s = strings.TrimSpace(s)
    s = norm.NFC.String(s)
    return cases.Fold().String(s)
}
```

## 6. Smart collection evaluation

```go
// api/internal/collections/smart.go
package collections

import (
    "context"
    "encoding/json"

    "maktaba/api/internal/search"
)

// evaluateSmart parses smart_query into a search.Filters value (only
// filter-style; not full-text). Returns the matched video page or an
// empty slice + a warning string on parse failure.
func (s *service) evaluateSmart(ctx context.Context, q json.RawMessage, cur paginate.Cursor, limit int) ([]Video, string, error) {
    var f search.Filters
    if err := json.Unmarshal(q, &f); err != nil {
        return nil, "smart-query-parse-failed", nil
    }
    rows, err := s.db.SmartCollectionVideos(ctx, f, cur, limit)
    if err != nil { return nil, "smart-query-runtime-error", nil }
    return rows, "", nil
}
```

The handler swallows query errors (per AC: never 500 on a parse problem)
and exposes them as `warning`:

```json
{ "items": [], "warning": "smart-query-parse-failed", "next": null }
```

## 7. Handler scaffolding

```go
// api/internal/tags/handler.go (delta path)
func (h *handler) patchVideoTags(w http.ResponseWriter, r *http.Request) {
    vid, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    var d TagDelta
    if err := json.NewDecoder(r.Body).Decode(&d); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if perr := h.svc.applyDelta(r.Context(), vid, d); perr != nil {
        httperror.Write(w, r, perr); return
    }
    rows, _ := h.db.ListTagsForVideo(r.Context(), vid)
    json.NewEncoder(w).Encode(rows)
}

// api/internal/speakers/handler.go (merge)
func (h *handler) merge(w http.ResponseWriter, r *http.Request) {
    var in MergeRequest
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if in.Keep == in.Drop {
        httperror.Write(w, r, httperror.BadRequest("keep and drop must differ"))
        return
    }
    affected, perr := h.svc.merge(r.Context(), in.Keep, in.Drop)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(MergeResponse{AffectedSegments: affected})
}
```

## 8. Test plan

### 8.1 Unit

| Test | What it pins |
|---|---|
| `TestNormalizeTag` | `"  Tafsīr  "` → `"tafsīr"` (trim + NFC + fold). |
| `TestNormalizeCaseFold` | `"Tafsir"` and `"tafsir"` → same key. |
| `TestEvaluateSmartParseFail` | Invalid JSON → returns empty + `smart-query-parse-failed` warning. |

### 8.2 Integration

| Test | What it pins |
|---|---|
| `TestCollectionItemOrdering` | Add 5 items at positions [10,20,30,40,50], reorder one to 25 → list returns in correct order. |
| `TestCollectionGapsAllowed` | Positions [10,30,50] → list iterates without complaint. |
| `TestSmartCollectionLive` | Smart query `{filters:{language:["ar"]}}` → list returns Arabic videos; not stored in `collection_items`. |
| `TestSmartCollectionPagination` | Same smart collection paginated → cursors work; new videos arriving don't appear in resumed pagination (consistent with Story 7.2). |
| `TestSmartCollectionInvalidQuery` | Insert a row with malformed `smart_query` → GET returns 200 with `items: []`, `warning: smart-query-parse-failed`. |
| `TestTagDeltaAdd` | Video has `[a,b,c]`; PATCH `{add:[d]}` → tags become `[a,b,c,d]`. |
| `TestTagDeltaRemove` | Video has `[a,b,c]`; PATCH `{remove:[b]}` → tags become `[a,c]`. |
| `TestTagAddDuplicate` | Adding existing tag is no-op (not 409). | 
| `TestTagsCaseFolded` | Add `"Tafsir"` then `"tafsir"` → one row, display preserves first-write casing. |
| `TestSpeakerMerge` | Two speakers, one with 100 segments → merge keeps 1, drops 0; `affected_segments=100`. |
| `TestSpeakerMergeAtomic` | Kill the API mid-merge (simulate via panic in test handler) → DB rolls back; both speakers still exist. |
| `TestSpeakerRenameCollision` | Rename to a name another speaker uses → 409 `speaker-name-exists` with hint to merge. |

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Two items at same position | Tie broken by `video_id`; `(position, video_id)` cursor handles it. | `TestCollectionItemOrdering` |
| Smart query with deeply nested or invalid filter | Returns 200 + warning, never 500. | `TestSmartCollectionInvalidQuery` |
| Tag delta with both add and remove of the same name | Final state is "added" (delta is order-deterministic: removes happen first, then adds). | Unit |
| Tag normalization difference (`"Tafsir"` vs `"tafsir"`) | Both fold to one row; display text comes from the first writer. | `TestTagsCaseFolded` |
| Speaker merge to itself (`keep == drop`) | 400 `bad-request`. | Unit |
| Merge under concurrent writes | Tx serializes; no half-state. | `TestSpeakerMergeAtomic` |
| Removing a tag that was never on the video | No-op; not 404. | Integration |
| Collection deletion cascades | `collection_items` removed via FK. The collection's smart query is just metadata; nothing to clean up. | Documented |
| Smart collection refers to a tag/library the user can't read | The SQL filter intersects with the user's authorised set (auth middleware injects this). | Auth-integration |

## 10. Acceptance checklist

- [ ] Collection items ordered by `(position, video_id)`; reorders are one UPDATE.
- [ ] Smart collections evaluate live and pass through pagination.
- [ ] Tag delta semantics match AC-3 (replace via flat array also supported via the videos PATCH from Story 7.4).
- [ ] Speaker merge is atomic and returns `affected_segments`.
- [ ] Tag normalization produces one row across casing/whitespace differences.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.14.
