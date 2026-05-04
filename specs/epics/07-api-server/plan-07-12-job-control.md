# Implementation Plan — Story 7.12 Job Control Endpoints

> Companion to [story-07-12-job-control.md](story-07-12-job-control.md).
> Idempotent flag-setters; never block on the worker.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/jobs?state&stage&video&cursor` (queue listing per architecture §9.5), `POST /api/jobs/{id}/{pause,resume,cancel,retry}`, `POST /api/jobs/{id}/priority {priority}`, bulk endpoints `POST /api/jobs:bulk-pause`, `POST /api/jobs:bulk-resume`, `POST /api/jobs:bulk-cancel`, `POST /api/jobs:bulk-retry`, plus per-video aggregates `POST /api/videos/{id}/{pause,resume,cancel}`. |
| Storage | The single source of truth is the `processing_jobs` table (Epic 6). All operations are simple UPDATEs. The seven canonical stages are `scan, probe, extract, transcribe, subtitle_gen, index, thumbnail` (architecture §3). |
| Bulk shape | `POST /api/jobs:bulk-X` accepts either `{ids:[...]}` (explicit list) or `{filter:{stage?, state?, library_id?}}` (predicate). Both bodies translate to a single UPDATE; `affected` is returned. |
| NOTIFY | All flag-setting UPDATEs fire `jobs.flag_set` (Story 7.16 listens). |
| Force pause | Goes deeper: it bypasses the worker by directly setting `state='paused'`. Used when the job is stuck. |
| Out of scope | The worker's reaction to flags (Epic 6 Stories 6.4, 6.5). The list endpoint shape (queue-stats Story 7.13 owns the SQL composition; this story wires the route). |

## 1. Architecture diagram

```
   POST /api/jobs/{id}/pause
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ UPDATE processing_jobs                                       │
   │    SET pause_requested = true                                │
   │  WHERE id = $1                                               │
   │    AND state IN ('pending','claimed','running','resuming')   │
   │ RETURNING *                                                  │
   │                                                              │
   │ rows = 0 →                                                   │
   │   - row exists but terminal → 409 job-terminal               │
   │   - row missing             → 404                            │
   │ rows = 1 → 200 with row                                      │
   │                                                              │
   │ Trigger fires NOTIFY 'jobs.flag_set' with {id, flag:"pause"} │
   └──────────────────────────────────────────────────────────────┘

   POST /api/jobs/{id}/pause?force=true
        │
        ▼
   ┌──────────────────────────────────────────────────────────────┐
   │ UPDATE processing_jobs                                       │
   │    SET state               = 'paused',                       │
   │        paused_reason       = 'user-force',                   │
   │        paused_at_sec       = last_segment_end_sec,           │
   │        claimed_by          = NULL,                           │
   │        pause_requested     = false                           │
   │  WHERE id = $1                                               │
   │    AND state IN ('claimed','running','resuming')             │
   │ RETURNING *                                                  │
   └──────────────────────────────────────────────────────────────┘

   POST /api/jobs/{id}/resume
   POST /api/jobs/{id}/cancel
   POST /api/jobs/{id}/retry
        │
        ▼ similar UPDATE statements with state-transition guards

   POST /api/videos/{id}/{pause,resume,cancel}
        │
        ▼ UPDATE multiple rows in one statement; return {affected: N}

   POST /api/jobs/{id}/priority { priority }
        │
        ▼ UPDATE processing_jobs SET priority=$2 WHERE id=$1
          AND state IN ('pending','claimed','running','resuming','paused')

   POST /api/jobs:bulk-{pause,resume,cancel,retry}
   { ids:[...]              }   -- explicit
   { filter:{ stage?, state?, library_id? } }  -- predicate
        │
        ▼ One UPDATE per call; return {affected: N}
          The predicate form joins to videos when library_id is supplied:
          UPDATE processing_jobs j SET ...
            FROM videos v
           WHERE v.id = j.video_id
             AND (   $stage::text IS NULL OR j.stage   = $stage)
             AND (   $state::text IS NULL OR j.state   = $state)
             AND (   $library_id::uuid IS NULL OR v.library_id = $library_id)

   GET /api/jobs?state&stage&video&cursor
        │
        ▼ See plan-07-13-queue-stats.md for the listing SQL — this story
          mounts the route and reuses the cursor primitive (07-02).
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/jobs/handler.go` | Per-job route handlers + per-video aggregates. |
| `api/internal/jobs/control.go` | Service-layer: state-machine guards + UPDATE shapes. |
| `api/internal/jobs/types.go` | DTOs. |
| `api/internal/jobs/handler_test.go` | Integration. |
| `api/internal/jobs/control_test.go` | Unit (state-machine guard table). |
| `shared/db/queries/jobs_control.sql` | sqlc inputs. |

## 3. Type definitions

```go
// api/internal/jobs/types.go
package jobs

import "time"

type JobDTO struct {
    ID                int64    `json:"id"`
    VideoID           string   `json:"video_id"`
    Stage             string   `json:"stage"`
    State             string   `json:"state"`
    Priority          int      `json:"priority"`
    Attempts          int      `json:"attempts"`
    PauseRequested    bool     `json:"pause_requested"`
    CancelRequested   bool     `json:"cancel_requested"`
    Error             *string  `json:"error,omitempty"`
    Note              string   `json:"note,omitempty"`
    LastSegmentEndSec float64  `json:"last_segment_end_sec"`
    CreatedAt         time.Time`json:"created_at"`
    FinishedAt        *time.Time`json:"finished_at,omitempty"`
}

type AffectedResponse struct {
    Affected int `json:"affected"`
}
```

## 4. Service layer

```go
// api/internal/jobs/control.go
package jobs

import (
    "context"
    "database/sql"
    "errors"

    "maktaba/api/internal/httperror"
)

var (
    livePauseable = []string{"pending", "claimed", "running", "resuming"}
    forcePauseable = []string{"claimed", "running", "resuming"}
    cancelable    = []string{"pending", "claimed", "running", "resuming", "paused"}
)

func (s *service) pause(ctx context.Context, id int64, force bool) (*JobDTO, *httperror.Error) {
    if force {
        row, err := s.db.ForcePauseJob(ctx, id)
        return s.mapResult(row, err)
    }
    row, err := s.db.SetPauseRequested(ctx, id)
    return s.mapResult(row, err)
}

func (s *service) resume(ctx context.Context, id int64) (*JobDTO, *httperror.Error) {
    row, err := s.db.ResumeJob(ctx, id)
    return s.mapResult(row, err)
}

func (s *service) cancel(ctx context.Context, id int64) (*JobDTO, *httperror.Error) {
    row, err := s.db.SetCancelRequested(ctx, id)
    return s.mapResult(row, err)
}

func (s *service) retry(ctx context.Context, id int64) (*JobDTO, *httperror.Error) {
    row, err := s.db.RetryJob(ctx, id)
    if errors.Is(err, sql.ErrNoRows) {
        // Distinguish "not found" from "exists but not failed."
        cur, err2 := s.db.GetJob(ctx, id)
        if errors.Is(err2, sql.ErrNoRows) { return nil, httperror.NotFound("job") }
        if err2 != nil { return nil, httperror.Internal("db") }
        return nil, &httperror.Error{
            Type: TypeJobNotFailed, Title: "job-not-failed",
            Status: 409, Detail: "current state: "+cur.State,
        }
    }
    return s.mapResult(row, err)
}

func (s *service) mapResult(row Job, err error) (*JobDTO, *httperror.Error) {
    if errors.Is(err, sql.ErrNoRows) {
        return nil, &httperror.Error{Type: TypeJobTerminalOrMissing, Title: "no-op",
            Status: 409, Detail: "job is terminal or missing"}
    }
    if err != nil { return nil, httperror.Internal("db") }
    return toDTO(row), nil
}

// pauseVideo flips `pause_requested = true` on every non-terminal job
// for `video_id`. Returns the count.
func (s *service) pauseVideo(ctx context.Context, videoID uuid.UUID) (int, *httperror.Error) {
    n, err := s.db.PauseAllForVideo(ctx, videoID)
    if err != nil { return 0, httperror.Internal("db") }
    return int(n), nil
}
```

## 5. SQL — sqlc inputs

`shared/db/queries/jobs_control.sql`:

```sql
-- name: SetPauseRequested :one
UPDATE processing_jobs
   SET pause_requested = true
 WHERE id = $1
   AND state IN ('pending','claimed','running','resuming')
RETURNING *;

-- name: ForcePauseJob :one
UPDATE processing_jobs
   SET state               = 'paused',
       paused_reason       = 'user-force',
       paused_at_sec       = last_segment_end_sec,
       claimed_by          = NULL,
       pause_requested     = false
 WHERE id = $1
   AND state IN ('claimed','running','resuming')
RETURNING *;

-- name: ResumeJob :one
UPDATE processing_jobs
   SET pause_requested = false,
       state           = CASE WHEN state = 'paused' THEN 'pending' ELSE state END,
       paused_reason   = NULL,
       resumed_at      = CASE WHEN state = 'paused' THEN now() ELSE resumed_at END,
       resume_count    = resume_count + CASE WHEN state = 'paused' THEN 1 ELSE 0 END
 WHERE id = $1
   AND state NOT IN ('done','failed','cancelled')
RETURNING *;

-- name: SetCancelRequested :one
UPDATE processing_jobs
   SET cancel_requested = true,
       state            = CASE WHEN state = 'pending'
                               THEN 'cancelled'
                               ELSE state END,
       finished_at      = CASE WHEN state = 'pending'
                               THEN now() ELSE finished_at END
 WHERE id = $1
   AND state NOT IN ('done','failed','cancelled')
RETURNING *;

-- name: RetryJob :one
UPDATE processing_jobs
   SET state      = 'pending',
       attempts   = 0,
       error      = NULL,
       not_before = now(),
       cancel_requested = false,
       pause_requested  = false
 WHERE id = $1
   AND state = 'failed'
RETURNING *;

-- name: GetJob :one
SELECT * FROM processing_jobs WHERE id = $1;

-- name: PauseAllForVideo :execrows
UPDATE processing_jobs
   SET pause_requested = true
 WHERE video_id = $1
   AND state IN ('pending','claimed','running','resuming');

-- name: ResumeAllForVideo :execrows
UPDATE processing_jobs
   SET pause_requested = false,
       state           = CASE WHEN state = 'paused' THEN 'pending' ELSE state END,
       paused_reason   = NULL,
       resumed_at      = CASE WHEN state = 'paused' THEN now() ELSE resumed_at END,
       resume_count    = resume_count + CASE WHEN state = 'paused' THEN 1 ELSE 0 END
 WHERE video_id = $1
   AND state NOT IN ('done','failed','cancelled');

-- name: CancelAllForVideo :execrows
UPDATE processing_jobs
   SET cancel_requested = true,
       state            = CASE WHEN state = 'pending'
                               THEN 'cancelled'
                               ELSE state END,
       finished_at      = CASE WHEN state = 'pending'
                               THEN now() ELSE finished_at END
 WHERE video_id = $1
   AND state NOT IN ('done','failed','cancelled');

-- name: SetJobPriority :one
UPDATE processing_jobs
   SET priority = $2
 WHERE id = $1
   AND state IN ('pending','claimed','running','resuming','paused')
RETURNING *;

-- name: BulkPauseByIDs :execrows
UPDATE processing_jobs
   SET pause_requested = true
 WHERE id = ANY($1::bigint[])
   AND state IN ('pending','claimed','running','resuming');

-- name: BulkResumeByIDs :execrows
UPDATE processing_jobs
   SET pause_requested = false,
       state           = CASE WHEN state = 'paused' THEN 'pending' ELSE state END
 WHERE id = ANY($1::bigint[])
   AND state NOT IN ('done','failed','cancelled');

-- name: BulkCancelByIDs :execrows
UPDATE processing_jobs
   SET cancel_requested = true,
       state            = CASE WHEN state = 'pending' THEN 'cancelled' ELSE state END,
       finished_at      = CASE WHEN state = 'pending' THEN now() ELSE finished_at END
 WHERE id = ANY($1::bigint[])
   AND state NOT IN ('done','failed','cancelled');

-- name: BulkRetryByIDs :execrows
UPDATE processing_jobs
   SET state      = 'pending',
       attempts   = 0,
       error      = NULL,
       not_before = now(),
       cancel_requested = false,
       pause_requested  = false
 WHERE id = ANY($1::bigint[])
   AND state = 'failed';

-- The predicate-form bulk endpoints use a single UPDATE with NULL-skipping
-- filters. processing_jobs.id is BIGSERIAL, so the array params are
-- bigint[].
-- name: BulkPauseByFilter :execrows
UPDATE processing_jobs j
   SET pause_requested = true
  FROM videos v
 WHERE v.id = j.video_id
   AND j.state IN ('pending','claimed','running','resuming')
   AND ($1::text IS NULL OR j.stage    = $1)
   AND ($2::text IS NULL OR j.state    = $2)
   AND ($3::uuid IS NULL OR v.library_id = $3);
-- (BulkResumeByFilter / BulkCancelByFilter / BulkRetryByFilter follow the
-- same shape, with the per-state guards from the by-id variants.)
```

The trigger emitting `jobs.flag_set` lives in Pipeline Story 6.1's
migration; this story does not introduce a new trigger, only relies on it.

## 6. Handler scaffolding

```go
// api/internal/jobs/handler.go
package jobs

import (
    "encoding/json"
    "net/http"
    "strconv"

    "github.com/go-chi/chi/v5"

    "maktaba/api/internal/httperror"
)

func Mount(r chi.Router, d Deps) {
    h := &handler{d}
    r.Get("/api/jobs", h.list)             // architecture §9.5; SQL in plan-07-13.
    r.Route("/api/jobs/{id}", func(r chi.Router) {
        r.Post("/pause",    h.pause)
        r.Post("/resume",   h.resume)
        r.Post("/cancel",   h.cancel)
        r.Post("/retry",    h.retry)
        r.Post("/priority", h.priority)
    })
    // chi treats colons in static path segments as literal; bulk routes
    // mount under /api/jobs:<verb> per architecture §9.5.
    r.Post("/api/jobs:bulk-pause",  h.bulkPause)
    r.Post("/api/jobs:bulk-resume", h.bulkResume)
    r.Post("/api/jobs:bulk-cancel", h.bulkCancel)
    r.Post("/api/jobs:bulk-retry",  h.bulkRetry)
    r.Route("/api/videos/{id}", func(r chi.Router) {
        r.Post("/pause",  h.pauseVideo)
        r.Post("/resume", h.resumeVideo)
        r.Post("/cancel", h.cancelVideo)
    })
}

// --- new handlers ---

type PriorityInput struct {
    Priority int `json:"priority" validate:"required,gte=1,lte=999"`
}

func (h *handler) priority(w http.ResponseWriter, r *http.Request) {
    id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    var in PriorityInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if err := validate(in); err != nil { httperror.Write(w, r, err); return }
    out, perr := h.svc.setPriority(r.Context(), id, in.Priority)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

// BulkInput accepts either {ids:[...]} or {filter:{...}} but not both.
type BulkInput struct {
    IDs    []int64      `json:"ids,omitempty"`
    Filter *BulkFilter  `json:"filter,omitempty"`
}

type BulkFilter struct {
    Stage     *string    `json:"stage,omitempty"     validate:"omitempty,oneof=scan probe extract transcribe subtitle_gen index thumbnail"`
    State     *string    `json:"state,omitempty"`
    LibraryID *uuid.UUID `json:"library_id,omitempty"`
}

func (h *handler) bulkPause(w http.ResponseWriter, r *http.Request) {
    h.bulk(w, r, h.svc.bulkPause)
}
// bulkResume / bulkCancel / bulkRetry follow the identical shape.

func (h *handler) bulk(w http.ResponseWriter, r *http.Request, op func(context.Context, BulkInput) (int, *httperror.Error)) {
    var in BulkInput
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if (len(in.IDs) > 0) == (in.Filter != nil) {
        httperror.Write(w, r, httperror.BadRequest("provide exactly one of ids/filter")); return
    }
    n, perr := op(r.Context(), in)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(AffectedResponse{Affected: n})
}

func (h *handler) pause(w http.ResponseWriter, r *http.Request) {
    id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    force := r.URL.Query().Get("force") == "true"
    out, perr := h.svc.pause(r.Context(), id, force)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

// resume, cancel, retry follow the same pattern.

func (h *handler) pauseVideo(w http.ResponseWriter, r *http.Request) {
    vid, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }
    n, perr := h.svc.pauseVideo(r.Context(), vid)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(AffectedResponse{Affected: n})
}
```

## 7. Test plan

### 7.1 Unit (`control_test.go`)

| Test | What it pins |
|---|---|
| `TestPauseRunningSetsFlag` | Job in `running` → `pause_requested=true`; state unchanged. |
| `TestForcePauseFromRunning` | Job in `running` → state flips to `paused`; `paused_at_sec=last_segment_end_sec`; `claimed_by=NULL`. |
| `TestForcePauseRejectsTerminal` | Job in `done` → 409 `job-terminal`. |
| `TestResumePaused` | Job in `paused` → state flips to `pending`; `paused_reason=NULL`. |
| `TestResumeRunningNoOp` | Job in `running` → 200, state unchanged. |
| `TestCancelPending` | Job in `pending` → state immediately `cancelled` with `finished_at` set. |
| `TestCancelRunning` | Job in `running` → `cancel_requested=true`; state stays `running` (worker handles transition). |
| `TestCancelTerminal` | Job in `done` → 409 `job-terminal`. |
| `TestRetryFailed` | Job in `failed` → state `pending`, attempts 0, error cleared. |
| `TestRetryNonFailed` | Job in `running` → 409 `job-not-failed`. |
| `TestRetryMissing` | Unknown id → 404. |

### 7.2 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestPauseFiresNotify` | Postgres `LISTEN jobs.flag_set`; pause → exactly one event. |
| `TestPauseResumeCycle` | Fast pause-then-resume → state cycles back to pending; worker (out-of-process fake) re-claims it. |
| `TestForcePauseRace` | Worker simulator commits a segment while API issues force-pause → API uses one UPDATE with the read inside; final `paused_at_sec` matches one of the committed values. |
| `TestVideoPause5Jobs` | Video with 5 active jobs across stages → `affected=5`; only the non-terminal flipped. |
| `TestVideoPauseMixedStates` | Video with 3 pending, 1 done, 1 cancelled → `affected=3`. |
| `TestControlLatencyP99` | 100 control ops/s for 30 s → p99 < 20 ms (DB-only path). |
| `TestIdempotency` | Two pause calls → both 200 with same body; only one `pause_requested=true` UPDATE side effect (idempotent at flag level). |
| `TestSetPriority` | POST `/api/jobs/{id}/priority {priority:10}` on a `pending` job → 200 with `priority=10`; on a terminal job → 409. |
| `TestBulkPauseByIDs` | `POST /api/jobs:bulk-pause {ids:[1,2,3]}` → `affected=3`; one UPDATE; one NOTIFY per row via row-level trigger. |
| `TestBulkPauseByFilter` | `POST /api/jobs:bulk-pause {filter:{state:"pending",library_id:<id>}}` → only matching rows flipped. |
| `TestBulkRejectsBoth` | `{ids:[1], filter:{...}}` in same body → 400 `bad-request`. |
| `TestJobsListByStateStage` | `GET /api/jobs?state=running&stage=transcribe` → only matching rows; cursor returned for pagination. |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Pause a `pending` job | Sets `pause_requested=true`; the claim loop skips paused-flagged rows; effectively a "freeze in queue." | Documented in API ref. |
| Resume a `running` job | No-op; 200 with current state. | `TestResumeRunningNoOp` |
| Cancel a `pending` job | Immediately transitions to `cancelled` (no worker round-trip needed). | `TestCancelPending` |
| Cancel a `running` job | Sets flag; the worker observes after the next segment commit. | `TestCancelRunning` |
| Retry a non-failed job | 409 `job-not-failed`. | `TestRetryNonFailed` |
| Force-pause race with worker segment commit | Single UPDATE reads `last_segment_end_sec` inside the SET; always consistent. | `TestForcePauseRace` |
| Mass per-video resume with 50 jobs | One UPDATE; the `affected` count is DB rows, not "actually started" — concurrency caps may delay restart. | Documented |
| `id` not numeric | 400 `bad-request`. | Unit |
| All ops issued by an unauthenticated caller | Auth middleware (Epic 10) short-circuits. | Out-of-scope |
| Resume on a `paused` job that the reaper has just freed (`claimed_by` null) | Resume sets state to `pending`; the next claim loop tick picks it up. | Integration |

## 9. Acceptance checklist

- [ ] Each per-job endpoint is one UPDATE statement.
- [ ] `force=true` directly sets `state='paused'` with the documented side-effects.
- [ ] Per-video aggregates return `{affected: N}`.
- [ ] `POST /api/jobs/{id}/priority` adjusts priority on non-terminal jobs (one UPDATE).
- [ ] `POST /api/jobs:bulk-pause|bulk-resume|bulk-cancel|bulk-retry` accept either `{ids:[...]}` or `{filter:{stage?, state?, library_id?}}` (mutually exclusive).
- [ ] `GET /api/jobs?state&stage&video&cursor` lists rows with the canonical cursor (architecture §9.5; SQL composed in plan-07-13).
- [ ] State-machine guards reject illegal transitions with 409 + a typed error.
- [ ] All endpoints are idempotent.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.12.
