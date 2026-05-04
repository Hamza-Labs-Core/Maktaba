# Implementation Plan — Story 7.3 Library CRUD

> Companion to [story-07-03-library-crud.md](story-07-03-library-crud.md).
> First real REST surface; relies on the skeleton (7.1) and pagination (7.2).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET/POST /api/libraries`, `GET/PATCH/DELETE /api/libraries/{id}`, `POST /api/libraries/{id}/scan`, `GET /api/libraries/{id}/stats`. |
| Storage | `libraries` table already in `architecture.md §7`; this story does not add new columns. |
| Settings merge | RFC 7396 JSON Merge Patch — but with our own helper because the Go ecosystem's options either over-merge (replace arrays) or pull in heavyweight deps. |
| Scan trigger | Calls Pipeline gRPC's `Enqueue` (Story 7.18) which in turn writes to `processing_jobs`; the API does not insert directly so the Pipeline keeps the source-of-truth. |
| Out of scope | The actual scanner (Epic 1), the stats query body (Epic 9 Story 9.7 owns the SQL composition; we just call it). |

## 1. Architecture diagram

```
   POST /api/libraries
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │ libraryHandler.Create                              │
   │  1. Decode + validate (Story 7.19 binds)           │
   │  2. validateRoots()                                │
   │       - absolute?  - exists?  - readable?  - dedup │
   │       - overlap with another library's roots       │
   │  3. INSERT INTO libraries (...) RETURNING *        │
   │  4. 201 + Location: /api/libraries/{id}            │
   └────────────────────────────────────────────────────┘

   PATCH /api/libraries/{id}
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │  Decode body → PatchInput                          │
   │  Read current settings (jsonb_extract)             │
   │  merged := DeepMerge(current, patch.Settings)      │
   │  UPDATE libraries SET settings=$1, name=$2, ...,    │
   │         updated_at=now() WHERE id=$id              │
   │  Return 200 + full row                             │
   └────────────────────────────────────────────────────┘

   DELETE /api/libraries/{id}?purge=true&confirm=<name>
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │  Validate confirm == row.name                      │
   │  Tx:  DELETE FROM libraries WHERE id=$1            │
   │       (cascade removes videos, jobs, …)            │
   │  After commit → walk roots, unlink files           │
   │       collect failed paths                         │
   │  204 if all unlinked, 207 + body if partial        │
   └────────────────────────────────────────────────────┘

   POST /api/libraries/{id}/scan
        │
        ▼
   ┌────────────────────────────────────────────────────┐
   │  pipelineClient.Enqueue(library_id, stage="scan",  │
   │                         priority=50)                │
   │  202 + {job_id}                                    │
   └────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/libraries/handler.go` | `chi` route registration + handlers. |
| `api/internal/libraries/service.go` | Business logic (validation, deep-merge, purge orchestration). |
| `api/internal/libraries/sql.go` | sqlc-style generated query wrappers (or hand-rolled if sqlc-generated lands later). |
| `api/internal/libraries/types.go` | Request/response DTOs and Validators. |
| `api/internal/libraries/errors.go` | Local problem-type constants (`library-roots-invalid`, `library-roots-overlap`, `library-name-exists`, `confirmation-required`, etc.). |
| `api/internal/libraries/handler_test.go` | Integration tests per §6. |
| `api/internal/libraries/service_test.go` | Unit tests for `validateRoots`, `DeepMerge`. |
| `shared/db/queries/libraries.sql` | sqlc inputs (`CreateLibrary`, `GetLibraryByID`, `UpdateLibrary`, `DeleteLibrary`, `ListLibraries`, `GetLibraryStats`). |

## 3. Type definitions

```go
// api/internal/libraries/types.go
package libraries

import (
    "time"
    "github.com/google/uuid"
)

type Library struct {
    ID        uuid.UUID       `json:"id"`
    Name      string          `json:"name"`
    Roots     []string        `json:"roots"`
    Settings  map[string]any  `json:"settings"`
    CreatedAt time.Time       `json:"created_at"`
    UpdatedAt time.Time       `json:"updated_at"`
}

type CreateInput struct {
    Name     string         `json:"name"     validate:"required,min=1,max=128"`
    Roots    []string       `json:"roots"    validate:"required,min=1,dive,required"`
    Settings map[string]any `json:"settings"`
}

type PatchInput struct {
    Name     *string        `json:"name,omitempty"`
    Roots    *[]string      `json:"roots,omitempty"`
    Settings map[string]any `json:"settings,omitempty"`
}

type DeleteOpts struct {
    Purge   bool   `query:"purge"`
    Confirm string `query:"confirm"`
}

type Stats struct {
    TotalVideos       int            `json:"total_videos"`
    TotalDurationSec  float64        `json:"total_duration_sec"`
    ByState           map[string]int `json:"by_state"`
    ProcessedPct      float64        `json:"processed_pct"`
    ByLanguage        map[string]int `json:"by_language"`
}

type ScanResponse struct {
    JobID int64 `json:"job_id"`
}
```

## 4. Handler scaffolding

```go
// api/internal/libraries/handler.go
package libraries

import (
    "encoding/json"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
    "maktaba/api/internal/paginate"
)

type Deps struct {
    DB         DB              // sqlc-generated *Queries
    Pipeline   PipelineClient  // Story 7.18 wrapper
    FS         FileSystem      // injected so tests stub disk
    Logger     *slog.Logger
}

func Mount(r chi.Router, d Deps) {
    h := &handler{d}
    r.Route("/api/libraries", func(r chi.Router) {
        r.Get("/", h.list)
        r.Post("/", h.create)
        r.Get("/{id}", h.get)
        r.Patch("/{id}", h.patch)
        r.Delete("/{id}", h.delete)
        r.Post("/{id}/scan", h.scan)
        r.Get("/{id}/stats", h.stats)
    })
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
    var in CreateInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json"))
        return
    }
    if err := validate(in); err != nil {
        httperror.Write(w, r, err)
        return
    }
    in.Roots = dedup(in.Roots)
    if err := h.svc.validateRoots(r.Context(), in.Roots, uuid.Nil); err != nil {
        httperror.Write(w, r, err)
        return
    }
    lib, err := h.svc.create(r.Context(), in)
    if err != nil {
        httperror.Write(w, r, err)
        return
    }
    w.Header().Set("Location", "/api/libraries/"+lib.ID.String())
    w.WriteHeader(http.StatusCreated)
    _ = json.NewEncoder(w).Encode(lib)
}
```

The other handlers follow the same pattern. The `list` handler wires
`paginate.Decode` + `paginate.ParseLimit` + `paginate.Bound` from
Story 7.2.

## 5. Service-layer scaffolding

```go
// api/internal/libraries/service.go
package libraries

import (
    "context"
    "io/fs"
    "os"
    "path/filepath"
    "strings"
)

func (s *service) validateRoots(ctx context.Context, roots []string, excluding uuid.UUID) *httperror.Error {
    var bad []FieldError

    for i, root := range roots {
        if !filepath.IsAbs(root) {
            bad = append(bad, FieldError{Field: pathField(i), Message: "not-absolute"})
            continue
        }
        info, err := s.fs.Stat(root)
        switch {
        case errors.Is(err, fs.ErrNotExist):
            bad = append(bad, FieldError{Field: pathField(i), Message: "not-found"})
            continue
        case err != nil:
            bad = append(bad, FieldError{Field: pathField(i), Message: "stat-failed"})
            continue
        case !info.IsDir():
            bad = append(bad, FieldError{Field: pathField(i), Message: "not-directory"})
            continue
        }
        if !s.fs.Readable(root) {
            bad = append(bad, FieldError{Field: pathField(i), Message: "not-readable"})
        }
    }
    if len(bad) > 0 {
        return &httperror.Error{
            Type: TypeRootsInvalid, Title: "library roots invalid",
            Status: 422, Errors: bad,
        }
    }

    overlap, err := s.checkOverlap(ctx, roots, excluding)
    if err != nil {
        return httperror.Internal("overlap check failed")
    }
    if overlap != "" {
        return &httperror.Error{
            Type: TypeRootsOverlap, Title: "library roots overlap",
            Status: 422, Detail: "root '" + overlap + "' overlaps an existing library",
        }
    }
    return nil
}

// DeepMerge merges b into a, recursing into nested maps. Arrays are replaced
// (not concatenated) so a PATCH that sends `tags: []` clears the field.
func DeepMerge(a, b map[string]any) map[string]any {
    out := cloneMap(a)
    for k, v := range b {
        if subB, ok := v.(map[string]any); ok {
            if subA, ok := out[k].(map[string]any); ok {
                out[k] = DeepMerge(subA, subB)
                continue
            }
        }
        out[k] = v
    }
    return out
}

// dedup preserves first-occurrence order, normalizing trailing slashes.
func dedup(in []string) []string {
    seen := map[string]struct{}{}
    out := make([]string, 0, len(in))
    for _, p := range in {
        clean := strings.TrimRight(filepath.Clean(p), string(filepath.Separator))
        if _, ok := seen[clean]; ok {
            continue
        }
        seen[clean] = struct{}{}
        out = append(out, clean)
    }
    return out
}

// purgeFiles unlinks every file under each root after the DB delete commits.
// Returns the list of paths that failed to delete; empty slice → 204; non-empty → 207.
func (s *service) purgeFiles(ctx context.Context, roots []string) []string {
    var failed []string
    for _, root := range roots {
        _ = filepath.Walk(root, func(p string, info fs.FileInfo, err error) error {
            if err != nil { failed = append(failed, p); return nil }
            if info.IsDir() { return nil }
            if rmErr := s.fs.Remove(p); rmErr != nil {
                failed = append(failed, p)
            }
            return nil
        })
    }
    return failed
}
```

`checkOverlap` runs a SQL query of the form:

```sql
-- name: FindOverlappingRoots :one
SELECT roots
  FROM libraries
 WHERE id <> $1
   AND EXISTS (
     SELECT 1 FROM unnest(roots) r
      WHERE EXISTS (
        SELECT 1 FROM unnest($2::text[]) candidate
         WHERE candidate LIKE r || '/%'
            OR r LIKE candidate || '/%'
            OR candidate = r
      )
   )
 LIMIT 1;
```

## 6. SQL — sqlc inputs

`shared/db/queries/libraries.sql`:

```sql
-- name: CreateLibrary :one
INSERT INTO libraries (id, name, roots, settings, created_at, updated_at)
VALUES ($1, $2, $3, $4, now(), now())
RETURNING *;

-- name: GetLibraryByID :one
SELECT * FROM libraries WHERE id = $1;

-- name: ListLibraries :many
SELECT *
  FROM libraries
 WHERE ($1::timestamptz IS NULL OR (updated_at, id) < ($1, $2))
 ORDER BY updated_at DESC, id DESC
 LIMIT $3;

-- name: UpdateLibrary :one
UPDATE libraries
   SET name = COALESCE($2, name),
       roots = COALESCE($3, roots),
       settings = COALESCE($4, settings),
       updated_at = now()
 WHERE id = $1
RETURNING *;

-- name: DeleteLibrary :exec
DELETE FROM libraries WHERE id = $1;

-- name: GetLibraryStats :one
SELECT
  count(v.id)                                        AS total_videos,
  COALESCE(sum(mi.duration_sec), 0)                  AS total_duration_sec,
  jsonb_object_agg(v.state, count_by_state.n)        AS by_state,
  jsonb_object_agg(v.detected_language,
                   count_by_language.n)              AS by_language
FROM libraries l
LEFT JOIN videos v          ON v.library_id = l.id
LEFT JOIN media_info mi     ON mi.video_id  = v.id
LEFT JOIN LATERAL (
    SELECT v2.state AS state, count(*) AS n
      FROM videos v2 WHERE v2.library_id = l.id GROUP BY v2.state
) count_by_state ON count_by_state.state = v.state
LEFT JOIN LATERAL (
    SELECT v2.detected_language AS detected_language, count(*) AS n
      FROM videos v2 WHERE v2.library_id = l.id GROUP BY v2.detected_language
) count_by_language ON count_by_language.detected_language = v.detected_language
WHERE l.id = $1
GROUP BY l.id;
```

The stats query is the one Epic 9 Story 9.7 owns; this story embeds it
verbatim so the API can serve `/stats` without waiting for that epic.

## 7. Test plan

### 7.1 Unit (`service_test.go`)

| Test | What it pins |
|---|---|
| `TestDeepMergePreservesUnmentionedKeys` | `{stt:{backend:x, model:y}}` + `{stt:{model:z}}` → `{stt:{backend:x, model:z}}`. |
| `TestDeepMergeReplacesArrays` | `{tags:[a,b]}` + `{tags:[c]}` → `{tags:[c]}`. |
| `TestDedupPreservesOrder` | `["/a","/b","/a"]` → `["/a","/b"]`. |
| `TestDedupNormalizesSlashes` | `["/a/","/a"]` → `["/a"]`. |
| `TestValidateRootsRelative` | `["./media"]` → 422 with `not-absolute`. |
| `TestValidateRootsMissing` | `["/no-such-dir"]` → 422 `not-found`. |
| `TestValidateRootsUnreadable` | `chmod 000`'d dir → 422 `not-readable`. |
| `TestValidateRootsAcceptsAbsolute` | `["/tmp"]` → no error. |

### 7.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestCreateLibrary` | POST with two valid roots → 201, `Location` header, body has `id`/`created_at`. |
| `TestCreateRejectsRelative` | One absolute, one relative → 422 listing the relative one. |
| `TestCreateUniqueName` | POST same name twice → 409 `library-name-exists`. |
| `TestCreateRejectsOverlap` | A `/mnt/media` exists; POST `/mnt/media/lectures` → 422 `library-roots-overlap`. |
| `TestPatchDeepMerge` | PATCH `{settings:{stt:{model:large-v3}}}` over `{stt:{backend:whisper}}` → merged result returned. |
| `TestDeleteSoft` | `DELETE /api/libraries/{id}` → 204; rows for the library are gone; on-disk files untouched. |
| `TestDeletePurgeRequiresConfirm` | `DELETE ?purge=true` without `confirm` → 412 `confirmation-required`. |
| `TestDeletePurgeWithConfirm` | `DELETE ?purge=true&confirm=<name>` → 204; FS-level files unlinked. |
| `TestDeletePurgePartial` | Read-only mount → 207 with `failed_paths` listed; DB delete still committed. |
| `TestScanEnqueues` | POST `/scan` → 202 with `{job_id}`; gRPC fake records exactly one Enqueue call with `priority=50`. |
| `TestScanIdempotent` | Two concurrent `/scan` POSTs → second returns 200 with the same `job_id`; only one row in `processing_jobs`. |
| `TestStatsShape` | Fixture with mixed states → response keys match `total_videos, total_duration_sec, by_state, processed_pct, by_language`. |
| `TestStatsPerformanceCI` | 50 000-video fixture (CI-nightly only) → query <50 ms. |
| `TestListPagination` | Seed 30 libraries; list with `?limit=10` → 3 pages, no duplicates, last page `next: null`. |

### 7.3 Failure-injection

| Test | What it pins |
|---|---|
| `TestScanWhenPipelineDown` | gRPC fake returns `UNAVAILABLE` → 503 `streaming-unavailable` (re-using the canonical type). |
| `TestPurgeWhenLibraryDeletedMidScan` | While a scan job is mid-flight, DELETE → cascade removes the job row; the worker (Pipeline) marks remnants `cancelled` (out-of-scope here, just covered by FK). |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Duplicate roots in the same POST | Silently collapsed via `dedup`; debug log emitted. | `TestDedupPreservesOrder` |
| One library nested inside another | 422 `library-roots-overlap` with the offending pair. | `TestCreateRejectsOverlap` |
| `?purge=true` without confirm | 412 `confirmation-required`. | `TestDeletePurgeRequiresConfirm` |
| `confirm=<wrong-name>` | 412 `confirmation-required`. | Same test, variant. |
| `purge=true` against an NFS mount where `unlink` succeeds but the dir cannot be removed | 207 with the directory paths in `failed_paths`. | `TestDeletePurgePartial` |
| Library deleted while a scan is mid-flight | FK `ON DELETE CASCADE` removes `processing_jobs` rows; the worker tolerates a "library gone" error (Pipeline owns that path). | FK + Pipeline contract |
| Concurrent `POST /scan` on the same library | The Pipeline-side `Enqueue` is idempotent (Story 6.1); the API just trusts that and returns whatever id comes back. | `TestScanIdempotent` |
| PATCH carrying `roots` with one bad path | 422 with the offending path; nothing persisted. | `TestPatchInvalidRoots` |
| `settings` PATCH that sets a key to `null` | DeepMerge replaces the value with `null`; readers must handle this. | Documented in `service.go`. |
| Stats request on a library with zero videos | All counts zero; `processed_pct = 0`; not 404. | `TestStatsEmpty` |
| `name` collision with case differences (`Quran` vs `quran`) | Names are stored verbatim; uniqueness is case-sensitive. The UI may want case-insensitive search later (out of scope). | Documented in API reference. |

## 9. Acceptance checklist

**AC-1 — create library**
- [ ] 201 + Location header.
- [ ] Body includes `id` and `created_at`.

**AC-2 — reject invalid roots**
- [ ] 422 problem+json `library-roots-invalid` with per-path detail.

**AC-3 — update + merge settings**
- [ ] DeepMerge produces the expected output (covered by unit + integration).
- [ ] `updated_at` advances.

**AC-4 — delete with purge**
- [ ] Default delete leaves files alone.
- [ ] `purge=true` requires `confirm=<name>`.
- [ ] DB delete commits before file unlink begins.
- [ ] 207 reports `failed_paths` on partial failures.

**AC-5 — trigger scan**
- [ ] 202 with `job_id`.
- [ ] `priority=50`; `jobs.new` NOTIFY fires.

**AC-6 — stats accuracy**
- [ ] Single SQL round-trip.
- [ ] Shape matches `{total_videos, total_duration_sec, by_state, processed_pct, by_language}`.

**Wiring**
- [ ] Routes mounted via `libraries.Mount(r, deps)` in the central router.
- [ ] All handlers go through `httperror.Write` for errors.

**Docs**
- [ ] API reference for `/api/libraries/*`.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.3.
