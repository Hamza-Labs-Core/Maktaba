# Implementation Plan — Story 7.4 Video List, Detail, Patch, Delete

> Companion to [story-07-04-video-crud.md](story-07-04-video-crud.md).
> Heaviest read surface in Epic 7. Performance hinges on covering indexes
> and a single-trip detail view.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/videos`, `GET /api/videos/{id}`, `PATCH /api/videos/{id}`, `DELETE /api/videos/{id}`. |
| Filtering | All filters live in URL query; SQL is parameterised; no filter is applied in-memory. |
| FTS | `q=` filter routes to `tsvector @@ plainto_tsquery` (Postgres) or `videos_fts MATCH` (SQLite). The full search story (7.8) lives elsewhere; this is just a list-side narrow. |
| Detail view | One round-trip via a CTE that joins `media_info`, `audio_tracks`, `chapters`, `tags`, `transcripts.id`, `playback_state`. |
| Out of scope | Search API proper (7.8), tag delta (7.14), processing control (7.5), the Pipeline-side cascade on delete (Epic 9). |

## 1. Architecture diagram

```
   GET /api/videos?library=…&language=ar&type=lecture&tag=tafsir&q=foo&sort=updated_at&limit=20
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ filterBuilder                                                │
   │  builds (where, args) incrementally:                         │
   │    library_id  → "library_id = ANY($n)"  (multi-allowed)     │
   │    language    → "detected_language = ANY($n)"               │
   │    type        → "content_type = ANY($n)"                    │
   │    tag         → "EXISTS (SELECT 1 FROM video_tags vt        │
   │                     JOIN tags t ON t.id=vt.tag_id            │
   │                    WHERE vt.video_id=videos.id AND t.name=ANY($n))"│
   │    q           → "fts MATCH $n"  (or  ts_vec @@ ...)         │
   │    sort        → ORDER BY <col> DESC, id DESC                │
   │    cursor      → paginate.Where(...)                         │
   └──────────────────────────────────────────────────────────────┘

   GET /api/videos/{id}
        │
        ▼ one query, one round trip
   ┌──────────────────────────────────────────────────────────────┐
   │ WITH                                                         │
   │   v   AS (SELECT * FROM videos WHERE id = $1),               │
   │   mi  AS (SELECT * FROM media_info WHERE video_id = $1),     │
   │   tr  AS (SELECT array_agg(...) FROM audio_tracks ...),       │
   │   ch  AS (SELECT array_agg(...) FROM chapters ...),           │
   │   tg  AS (SELECT array_agg(...) FROM video_tags JOIN tags ...│
   │   ts  AS (SELECT id FROM transcripts WHERE video_id=$1       │
   │              AND superseded_at IS NULL ORDER BY created_at   │
   │              DESC LIMIT 1),                                  │
   │   ps  AS (SELECT * FROM playback_state                       │
   │            WHERE user_id = $2 AND video_id = $1)             │
   │ SELECT * FROM v, mi, tr, ch, tg, ts, ps;                     │
   └──────────────────────────────────────────────────────────────┘

   DELETE /api/videos/{id}?purge=true&confirm=<id>
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ Tx: DELETE FROM videos WHERE id=$1   (cascade)               │
   │     INSERT INTO audit_log(category='library',action='video-purge',│
   │                            payload={path}, actor_user_id, ts)│
   │ After commit → unlink source file                            │
   └──────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/videos/handler.go` | Routes + handlers. |
| `api/internal/videos/list.go` | Filter/sort builder for the list endpoint. |
| `api/internal/videos/detail.go` | Single-trip CTE detail query. |
| `api/internal/videos/patch.go` | Field allow-list, deep clean. |
| `api/internal/videos/delete.go` | Soft vs purge, audit row. |
| `api/internal/videos/types.go` | DTOs. |
| `api/internal/videos/errors.go` | Local problem types. |
| `api/internal/videos/handler_test.go` | Integration. |
| `api/internal/videos/list_test.go` | Unit + filter combination tests. |
| `shared/db/queries/videos.sql` | sqlc inputs. |
| `shared/db/migrations/0012_videos_indexes.sql` | Covering indexes for list filters. |

## 3. SQL — covering indexes

`shared/db/migrations/0012_videos_indexes.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- AC-1 mentions covering indexes on these three filter columns. The
-- fourth and fifth combine with library_id for the most common query
-- shape in Epic 12 (mobile library browse).
CREATE INDEX IF NOT EXISTS videos_library_updated_idx
  ON videos (library_id, updated_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS videos_language_idx
  ON videos (detected_language)
  WHERE detected_language IS NOT NULL;

CREATE INDEX IF NOT EXISTS videos_content_type_idx
  ON videos (content_type)
  WHERE content_type IS NOT NULL;

CREATE INDEX IF NOT EXISTS video_tags_lookup_idx
  ON video_tags (tag_id, video_id);

-- Postgres FTS column (created here lazily so this story is
-- self-contained; Story 7.8 will refine weights and dictionaries).
ALTER TABLE videos
  ADD COLUMN IF NOT EXISTS search_tsv tsvector
  GENERATED ALWAYS AS (
    setweight(to_tsvector('simple', coalesce(title, '')), 'A') ||
    setweight(to_tsvector('simple', coalesce(description, '')), 'B')
  ) STORED;

CREATE INDEX IF NOT EXISTS videos_search_tsv_idx
  ON videos USING GIN (search_tsv);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS videos_search_tsv_idx;
ALTER TABLE videos DROP COLUMN IF EXISTS search_tsv;
DROP INDEX IF EXISTS video_tags_lookup_idx;
DROP INDEX IF EXISTS videos_content_type_idx;
DROP INDEX IF EXISTS videos_language_idx;
DROP INDEX IF EXISTS videos_library_updated_idx;
-- +goose StatementEnd
```

The SQLite mirror swaps the FTS column for an FTS5 virtual table
(`videos_fts USING fts5(title, description, content=videos)` plus
INSERT/UPDATE triggers); the partial indexes work on SQLite ≥ 3.8.

## 4. Type definitions

```go
// api/internal/videos/types.go
package videos

import (
    "encoding/json"
    "time"
    "github.com/google/uuid"
)

type Video struct {
    ID               uuid.UUID  `json:"id"`
    LibraryID        uuid.UUID  `json:"library_id"`
    Title            string     `json:"title"`
    Description      *string    `json:"description"`
    DetectedLanguage *string    `json:"detected_language"`
    ContentType      *string    `json:"content_type"`
    State            string     `json:"state"`
    Path             string     `json:"path"`
    Playable         bool       `json:"playable"`
    CreatedAt        time.Time  `json:"created_at"`
    UpdatedAt        time.Time  `json:"updated_at"`
}

type ListFilters struct {
    LibraryIDs   []uuid.UUID
    Languages    []string
    ContentTypes []string
    Tags         []string
    Q            string
    Sort         string // "updated_at" | "created_at" | "title" | "duration_sec"
}

type ListItem struct {
    Video
    DurationSec *float64   `json:"duration_sec"`
    PosterURL   *string    `json:"poster_url"`
}

type Detail struct {
    Video
    MediaInfo     *MediaInfo     `json:"media_info"`
    AudioTracks   []AudioTrack   `json:"audio_tracks"`
    Chapters      []Chapter      `json:"chapters"`
    Tags          []Tag          `json:"tags"`
    TranscriptID  *uuid.UUID     `json:"transcript_id"`
    PlaybackState *PlaybackState `json:"playback_state"`
}

type Patch struct {
    Title       *string  `json:"title,omitempty"        validate:"omitempty,max=512"`
    Description *string  `json:"description,omitempty"  validate:"omitempty,max=8192"`
    Tags        *[]string `json:"tags,omitempty"`
    // any other field on the body is silently ignored.
}
```

## 5. Filter builder

```go
// api/internal/videos/list.go
package videos

import (
    "fmt"
    "strings"

    "maktaba/api/internal/httperror"
    "maktaba/api/internal/paginate"
)

var allowedSorts = map[string]string{
    "updated_at":   "updated_at",
    "created_at":   "created_at",
    "title":        "title",
    "duration_sec": "duration_sec",
}

func buildListSQL(f ListFilters, cur paginate.Cursor, limit int) (string, []any, *httperror.Error) {
    args := []any{}
    where := []string{"1=1"}
    addAny := func(col string, vals []string) {
        if len(vals) == 0 { return }
        args = append(args, vals)
        where = append(where, fmt.Sprintf("%s = ANY($%d)", col, len(args)))
    }
    addUUIDAny := func(col string, vals []uuid.UUID) {
        if len(vals) == 0 { return }
        args = append(args, vals)
        where = append(where, fmt.Sprintf("%s = ANY($%d)", col, len(args)))
    }

    addUUIDAny("v.library_id", f.LibraryIDs)
    addAny("v.detected_language", f.Languages)
    addAny("v.content_type", f.ContentTypes)

    if len(f.Tags) > 0 {
        args = append(args, f.Tags)
        where = append(where, fmt.Sprintf(`EXISTS (
            SELECT 1 FROM video_tags vt JOIN tags t ON t.id = vt.tag_id
             WHERE vt.video_id = v.id AND t.name = ANY($%d))`, len(args)))
    }

    if q := strings.TrimSpace(f.Q); q != "" {
        args = append(args, q)
        where = append(where, fmt.Sprintf("v.search_tsv @@ plainto_tsquery('simple', $%d)", len(args)))
    }

    sortCol, ok := allowedSorts[f.Sort]
    if f.Sort != "" && !ok {
        return "", nil, &httperror.Error{
            Type: TypeInvalidSort, Title: "invalid sort",
            Status: 400, Detail: f.Sort,
        }
    }
    if sortCol == "" { sortCol = "updated_at" }

    spec := paginate.SortSpec{TimeCol: "v." + sortCol, IDCol: "v.id", Desc: true}
    curFrag, curArgs := paginate.Where(spec, cur, len(args)+1)
    if curFrag != "" {
        where = append(where, curFrag)
        args = append(args, curArgs...)
    }

    args = append(args, limit+1)
    sql := fmt.Sprintf(`
        SELECT v.id, v.library_id, v.title, v.description, v.detected_language,
               v.content_type, v.state, v.path, v.created_at, v.updated_at,
               mi.duration_sec, v.poster_url
          FROM videos v
          LEFT JOIN media_info mi ON mi.video_id = v.id
         WHERE %s
         ORDER BY %s DESC, v.id DESC
         LIMIT $%d`,
        strings.Join(where, " AND "), "v."+sortCol, len(args))
    return sql, args, nil
}
```

## 6. Single-trip detail query

`shared/db/queries/videos.sql`:

```sql
-- name: GetVideoDetail :one
SELECT
    v.*,
    to_jsonb(mi.*) AS media_info,
    COALESCE(
      (SELECT jsonb_agg(to_jsonb(at.*) ORDER BY at.index)
         FROM audio_tracks at WHERE at.video_id = v.id), '[]'::jsonb
    ) AS audio_tracks,
    COALESCE(
      (SELECT jsonb_agg(to_jsonb(c.*) ORDER BY c.seq)
         FROM chapters c WHERE c.video_id = v.id), '[]'::jsonb
    ) AS chapters,
    COALESCE(
      (SELECT jsonb_agg(jsonb_build_object('id', t.id, 'name', t.name))
         FROM video_tags vt JOIN tags t ON t.id = vt.tag_id
        WHERE vt.video_id = v.id), '[]'::jsonb
    ) AS tags,
    (SELECT id FROM transcripts
      WHERE video_id = v.id AND superseded_at IS NULL
      ORDER BY created_at DESC LIMIT 1) AS transcript_id,
    to_jsonb(ps.*) AS playback_state
  FROM videos v
  LEFT JOIN media_info mi ON mi.video_id = v.id
  LEFT JOIN playback_state ps ON ps.video_id = v.id AND ps.user_id = $2
 WHERE v.id = $1;
```

The `playable` field is derived in code: `v.path != "" && fs.Stat(v.path)
== nil` — but the spec says **don't stat per-request** for the list path,
only the detail path. Detail caches the stat result for `playable_ttl_sec`
(default 30 s) per-video to avoid hammering NFS.

## 7. Patch / delete handlers

```go
// api/internal/videos/patch.go
func (h *handler) patch(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    var raw map[string]json.RawMessage
    if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    // Allow-list: silently drop unknown keys; the spec says ignore, not error.
    var p Patch
    for _, key := range []string{"title", "description", "tags"} {
        if v, ok := raw[key]; ok {
            switch key {
            case "title":       _ = json.Unmarshal(v, &p.Title)
            case "description": _ = json.Unmarshal(v, &p.Description)
            case "tags":        _ = json.Unmarshal(v, &p.Tags)
            }
        }
    }
    if err := validate(p); err != nil { httperror.Write(w, r, err); return }

    out, perr := h.svc.applyPatch(r.Context(), id, p)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}
```

```go
// api/internal/videos/delete.go
func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    purge := r.URL.Query().Get("purge") == "true"
    confirm := r.URL.Query().Get("confirm")

    if purge && confirm != id.String() {
        httperror.Write(w, r, &httperror.Error{
            Type: httperror.TypeConfirmationReq, Title: "confirmation required",
            Status: 412, Detail: "?confirm=<id> required when ?purge=true",
        })
        return
    }

    path, perr := h.svc.deleteRow(r.Context(), id)
    if perr != nil { httperror.Write(w, r, perr); return }

    if !purge {
        w.WriteHeader(http.StatusNoContent); return
    }
    if perr := h.svc.unlinkPath(r.Context(), path); perr != nil {
        if errors.Is(perr.Cause(), fs.ErrNotExist) {
            w.Header().Set("Maktaba-Warning", "file-not-found")
            w.WriteHeader(http.StatusNoContent); return
        }
        httperror.Write(w, r, perr); return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

The audit row is inserted inside the same transaction as the DB delete:

```sql
-- name: PurgeVideo :one
WITH gone AS (DELETE FROM videos WHERE id = $1 RETURNING path)
INSERT INTO audit_log (category, action, payload, actor_user_id, ts)
SELECT 'library', 'video-purge', jsonb_build_object('path', path), $2, now()
  FROM gone
RETURNING (SELECT path FROM gone);
```

## 8. Test plan

### 8.1 Unit (`list_test.go`)

| Test | What it pins |
|---|---|
| `TestListBuilderLanguageFilter` | `?language=ar` → SQL contains `detected_language = ANY($1)` and arg is `[ar]`. |
| `TestListBuilderTagFilter` | `?tag=tafsir&tag=fiqh` → EXISTS subquery with `tags.name = ANY($n)`; both names in args. |
| `TestListBuilderUnknownSort` | `?sort=banana` → 400 `invalid-sort`. |
| `TestListBuilderQEmpty` | `?q=  ` (whitespace) → no FTS clause added. |
| `TestListBuilderCursorAppend` | Cursor + filter combo numbers placeholders correctly. |

### 8.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestListAllFilters` | Seed mixed corpus; filter `library + language + type + tag + q` → exactly the matching subset. |
| `TestListPagination` | 1000 videos, `limit=50` → 20 pages, no duplicates. |
| `TestDetailIsOneRoundTrip` | Wrap the DB connection with a query counter; GET `/api/videos/{id}` → exactly one `Query` call. |
| `TestDetailMissingFile` | Unmount drive (test substitutes a `Stat` returning `ErrNotExist`) → 200 with `playable: false`. |
| `TestPatchTitle` | PATCH `{title: "x"}` → 200 with `title=x` and `updated_at` advanced. |
| `TestPatchIgnoresState` | PATCH `{state: "ready"}` → 200; row's `state` unchanged. |
| `TestPatchDescriptionTooLong` | PATCH with 16 KB description → 413 `payload-too-large`. |
| `TestPatchTagsReplaces` | PATCH `{tags: [a, b]}` → tags become exactly `[a, b]` (replace semantic). |
| `TestDeleteSoft` | DELETE `?purge=false` → 204; row gone; file still on disk. |
| `TestDeletePurgeNoConfirm` | DELETE `?purge=true` → 412 `confirmation-required`. |
| `TestDeletePurgeWithConfirm` | DELETE `?purge=true&confirm=<id>` → 204; file unlinked; `audit_log` has the row. |
| `TestDeletePurgeMissingFile` | File pre-deleted out-of-band → 204 with `Maktaba-Warning: file-not-found`. |

### 8.3 Performance

| Test | What it pins |
|---|---|
| `TestListPerformance10K` | 10 000-video fixture, library + language filter → p95 < 100 ms. |
| `TestDetailPerformance10K` | Same fixture, GET detail → p95 < 30 ms (single round-trip + `playable` cache). |

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| File missing on disk, DB row exists | Detail returns `playable: false`. List does not stat (perf). | `TestDetailMissingFile` |
| Two videos sharing `content_hash` mid-merge | List returns both; detail returns the matching `id`. Dedup is the Pipeline's responsibility (Epic 9). | Documented; no test here. |
| PATCH body has unknown keys | Silently ignored; allow-list parsing. | `TestPatchIgnoresState` |
| 16 KB description payload | Story 7.19 caps body to 8 KB on this route → 413. | `TestPatchDescriptionTooLong` |
| `?q=` containing FTS metacharacters (`&`, `|`, `:`) | Use `plainto_tsquery` (Postgres) / `MATCH` with quoting (SQLite); user input never injected verbatim. | Unit `TestQAvoidsInjection` |
| `?library=<not-a-uuid>` | 400 `invalid-query-parameter`. | Unit |
| Many filters combined | Each adds an `AND` clause; the index planner picks the most selective via `videos_library_updated_idx` first. Verify with `EXPLAIN` in CI nightly. | Manual verification |
| Concurrent PATCH on the same video | Last writer wins; `updated_at` reflects the latest UPDATE. No optimistic-lock at this layer (could be added later). | Documented in API reference. |
| DELETE cascades into `processing_jobs` | FK `ON DELETE CASCADE` removes them; Pipeline workers tolerate "video gone" mid-flight. | FK constraint |
| Purge against a directory that's read-only | `unlink` fails → 500 `internal` (with audit row already committed). The audit row preserves the intent even if the unlink failed. | `TestPurgeReadOnly` (variant) |
| PATCH `tags: []` | Removes all tags. | Unit |

## 10. Acceptance checklist

- [ ] List endpoint supports the documented filter set; query is parameterised.
- [ ] Detail endpoint completes in one DB round-trip (verified by query counter).
- [ ] PATCH ignores fields outside `{title, description, tags}`.
- [ ] DELETE with `purge=true` requires matching `confirm=<id>`; audit row is written.
- [ ] Indexes from §3 land in `0012_videos_indexes.sql`.
- [ ] All `Test*` cases pass on Postgres and SQLite (where applicable).
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.4.
