# Implementation Plan — Story 7.5 Video Processing Control

> Companion to [story-07-05-video-processing-control.md](story-07-05-video-processing-control.md).
> Two endpoints that bridge the API to Pipeline's job queue (Epic 6).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `POST /api/videos/{id}/process`, `POST /api/videos/{id}/reprocess`. |
| Source of truth | The `processing_jobs` table (Epic 6 Story 6.1). The API never invents jobs; it calls `Pipeline.Enqueue` over gRPC (Story 7.18). |
| Stage FSM | Owned by Epic 1 Story 1.6. The reprocess predecessor table is mirrored verbatim here so the API can validate without a round-trip. |
| Out of scope | The actual transitions in the worker (Pipeline). Mark-superseded SQL belongs to Epic 9 Story 9.6 — we just call the helper. |

## 1. Architecture diagram

```
   POST /api/videos/{id}/process
   { "stage": "transcribe", "priority": 50 }
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ 1. Validate id, stage ∈ {probe,extract,transcribe,         │
   │                          subtitle_gen,index,thumbnail}     │
   │    (scan is library-level → reject)                        │
   │                                                            │
   │ 2. SELECT id, state, attempts FROM processing_jobs         │
   │    WHERE video_id=$1 AND stage=$2                          │
   │    ORDER BY created_at DESC LIMIT 1                        │
   │                                                            │
   │ 3a. If row exists in {pending, paused}:                    │
   │       UPDATE processing_jobs                               │
   │          SET priority = LEAST(priority, $new),             │
   │              not_before = now(),                           │
   │              attempts = CASE state WHEN 'failed' THEN 0    │
   │                             ELSE attempts END              │
   │       WHERE id = $row.id                                   │
   │       RETURNING *;                                         │
   │     → 200 {job_id, state, note?}                           │
   │                                                            │
   │ 3b. If row exists in {running}:                            │
   │     → 200 {job_id, state, note: "job already running"}     │
   │                                                            │
   │ 3c. If row exists in {failed}:                             │
   │       UPDATE state='pending', attempts=0, error=NULL,      │
   │              not_before=now(), priority=$new               │
   │       → 200 {job_id, state}                                │
   │                                                            │
   │ 3d. Else (no row, or only terminal {done, cancelled}):     │
   │       Pipeline.Enqueue(video_id, stage, priority=$new)     │
   │       → 200 {job_id, state: "pending"}                     │
   └────────────────────────────────────────────────────────────┘

   POST /api/videos/{id}/reprocess
   { "from_stage": "transcribe" }
        │
        ▼
   ┌────────────────────────────────────────────────────────────┐
   │ 1. Validate from_stage ≠ "scan"                            │
   │ 2. Tx:                                                     │
   │    a. UPDATE videos SET state = predecessor[from_stage],   │
   │           updated_at = now()                               │
   │       WHERE id = $1;                                       │
   │    b. UPDATE transcripts SET superseded_at = now()         │
   │       WHERE video_id = $1 AND superseded_at IS NULL;       │
   │       (only when from_stage in {transcribe, subtitle_gen,  │
   │        index})                                             │
   │    c. Pipeline.EnqueueChain(video_id, from_stage,          │
   │                              priority=200)                 │
   │ 3. NOTIFY 'videos.state_changed' { video_id, new_state }   │
   │ 4. 202 + { job_ids: [...], from_stage, predecessor_state } │
   └────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/videos/process.go` | Both handler functions + the predecessor map. |
| `api/internal/videos/process_test.go` | Unit + integration. |
| `shared/db/queries/videos_process.sql` | sqlc inputs (find existing job, repriorise, mark superseded, set state). |
| `api/internal/videos/errors_process.go` | `stage-not-per-video`, `stage-not-allowed`, `unknown-stage`. |

## 3. Stage predecessor map

The FSM lives in Epic 1 Story 1.6; the predecessor map is the inverse of
the canonical "next stage" arrow set. We hard-code it here and pin it
with a unit test that imports the canonical arrow constants when the
1.6 helper exists, falling back to a literal table.

```go
// api/internal/videos/process.go
package videos

import (
    "context"
    "encoding/json"
    "errors"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "maktaba/api/internal/httperror"
)

type Stage string

const (
    StageScan        Stage = "scan"
    StageProbe       Stage = "probe"
    StageExtract     Stage = "extract"
    StageTranscribe  Stage = "transcribe"
    StageSubtitleGen Stage = "subtitle_gen"
    StageIndex       Stage = "index"
    StageThumbnail   Stage = "thumbnail"
)

// predecessorState maps the stage you want to re-run *from* to the video
// state to roll back to. From the Story 1.6 FSM:
//   discovered → probed → audio_extracted → transcribed → indexed → ready
var predecessorState = map[Stage]string{
    StageProbe:       "discovered",
    StageExtract:     "probed",
    StageTranscribe:  "audio_extracted",
    StageSubtitleGen: "transcribed",
    StageIndex:       "transcribed",
    StageThumbnail:   "probed",
}

// liveStates: the API's view of "in-flight."
var liveStates = []string{"pending", "claimed", "running", "resuming", "paused"}

// supersedingStages: re-running these invalidates the current transcript.
var supersedingStages = map[Stage]bool{
    StageTranscribe:  true,
    StageSubtitleGen: true,
    StageIndex:       true,
}
```

## 4. Handler scaffolding

```go
type ProcessRequest struct {
    Stage    *Stage `json:"stage,omitempty"     validate:"omitempty,oneof=probe extract transcribe subtitle_gen index thumbnail"`
    Priority *int32 `json:"priority,omitempty"  validate:"omitempty,gte=1,lte=999"`
}

type ProcessResponse struct {
    JobID  int64  `json:"job_id"`
    Stage  Stage  `json:"stage"`
    State  string `json:"state"`
    Note   string `json:"note,omitempty"`
}

func (h *handler) process(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    var in ProcessRequest
    if r.ContentLength > 0 {
        if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
            httperror.Write(w, r, httperror.BadRequest("invalid json")); return
        }
    }
    stage := StageTranscribe
    if in.Stage != nil { stage = *in.Stage }
    priority := int32(50)
    if in.Priority != nil { priority = *in.Priority }
    if stage == StageScan {
        httperror.Write(w, r, &httperror.Error{
            Type: TypeStageNotPerVideo, Title: "stage not per-video",
            Status: 400, Detail: "scan is a library-level operation; use /api/libraries/{id}/scan",
        })
        return
    }

    out, perr := h.svc.processNow(r.Context(), id, stage, priority)
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}

type ReprocessRequest struct {
    FromStage Stage `json:"from_stage" validate:"required,oneof=probe extract transcribe subtitle_gen index thumbnail"`
}

type ReprocessResponse struct {
    JobIDs            []int64 `json:"job_ids"`
    FromStage         Stage   `json:"from_stage"`
    PredecessorState  string  `json:"predecessor_state"`
}

func (h *handler) reprocess(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    if err != nil { httperror.Write(w, r, httperror.BadRequest("invalid id")); return }

    var in ReprocessRequest
    if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
        httperror.Write(w, r, httperror.BadRequest("invalid json")); return
    }
    if in.FromStage == StageScan {
        httperror.Write(w, r, &httperror.Error{
            Type: TypeStageNotPerVideo, Title: "stage not per-video",
            Status: 400, Detail: "scan is library-wide",
        })
        return
    }

    out, perr := h.svc.reprocess(r.Context(), id, in.FromStage)
    if perr != nil { httperror.Write(w, r, perr); return }
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(out)
}
```

## 5. Service layer

```go
func (s *service) processNow(ctx context.Context, id uuid.UUID, stage Stage, prio int32) (*ProcessResponse, *httperror.Error) {
    row, err := s.db.FindLatestJob(ctx, FindLatestJobParams{VideoID: id, Stage: string(stage)})
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return nil, httperror.Internal("db read failed")
    }
    switch {
    case errors.Is(err, sql.ErrNoRows) || isTerminal(row.State):
        // No live or paused job → enqueue fresh.
        res, gerr := s.pipeline.Enqueue(ctx, EnqueueRequest{
            VideoID: id, Stage: string(stage), Priority: prio,
        })
        if gerr != nil { return nil, mapGRPC(gerr) }
        return &ProcessResponse{JobID: res.ID, Stage: stage, State: "pending"}, nil

    case row.State == "running":
        return &ProcessResponse{JobID: row.ID, Stage: stage, State: row.State,
            Note: "job already running"}, nil

    case row.State == "failed":
        upd, err := s.db.RestartFailed(ctx, RestartFailedParams{ID: row.ID, Priority: prio})
        if err != nil { return nil, httperror.Internal("restart failed") }
        return &ProcessResponse{JobID: upd.ID, Stage: stage, State: "pending"}, nil

    default: // pending | paused | claimed | resuming
        upd, err := s.db.RepriorityJob(ctx, RepriorityJobParams{ID: row.ID, Priority: prio})
        if err != nil { return nil, httperror.Internal("repriority failed") }
        return &ProcessResponse{JobID: upd.ID, Stage: stage, State: upd.State}, nil
    }
}

func (s *service) reprocess(ctx context.Context, id uuid.UUID, from Stage) (*ReprocessResponse, *httperror.Error) {
    pred, ok := predecessorState[from]
    if !ok {
        return nil, &httperror.Error{Type: TypeUnknownStage, Title: "unknown stage", Status: 400}
    }

    var jobIDs []int64
    err := s.db.Tx(ctx, func(tx Tx) error {
        if _, err := tx.SetVideoState(ctx, SetVideoStateParams{ID: id, State: pred}); err != nil {
            return err
        }
        if supersedingStages[from] {
            if err := tx.SupersedeTranscripts(ctx, id); err != nil { return err }
        }
        ids, err := s.pipeline.EnqueueChain(ctx, id, string(from), 200)
        if err != nil { return err }
        jobIDs = ids
        return nil
    })
    if err != nil { return nil, mapGRPC(err) }

    s.notify.Publish(ctx, "videos.state_changed", map[string]any{
        "video_id":  id,
        "new_state": pred,
    })

    return &ReprocessResponse{
        JobIDs: jobIDs, FromStage: from, PredecessorState: pred,
    }, nil
}
```

## 6. SQL — sqlc inputs

`shared/db/queries/videos_process.sql`:

```sql
-- name: FindLatestJob :one
SELECT * FROM processing_jobs
 WHERE video_id = $1 AND stage = $2
 ORDER BY created_at DESC
 LIMIT 1;

-- name: RepriorityJob :one
UPDATE processing_jobs
   SET priority   = LEAST(priority, $2),
       not_before = now()
 WHERE id = $1
RETURNING *;

-- name: RestartFailed :one
UPDATE processing_jobs
   SET state      = 'pending',
       attempts   = 0,
       error      = NULL,
       priority   = $2,
       not_before = now()
 WHERE id = $1 AND state = 'failed'
RETURNING *;

-- name: SetVideoState :one
UPDATE videos SET state = $2, updated_at = now()
 WHERE id = $1
RETURNING id, state;

-- name: SupersedeTranscripts :exec
UPDATE transcripts
   SET superseded_at = now()
 WHERE video_id = $1 AND superseded_at IS NULL;
```

## 7. Test plan

### 7.1 Unit (`process_test.go`)

| Test | What it pins |
|---|---|
| `TestPredecessorMap` | Each non-scan stage has a predecessor; scan is absent. |
| `TestProcessRejectsScan` | POST `{stage: "scan"}` → 400 `stage-not-per-video`. |
| `TestProcessUnknownStage` | POST `{stage: "banana"}` → 400 (validator). |
| `TestReprocessRejectsScan` | POST `{from_stage: "scan"}` → 400 `stage-not-per-video`. |
| `TestSupersedingStages` | `transcribe`, `subtitle_gen`, `index` set the map true; `extract`, `thumbnail` false. |

### 7.2 Integration (`process_integration_test.go`)

| Test | What it pins |
|---|---|
| `TestProcessNoExistingJob` | Empty `processing_jobs` → Pipeline `Enqueue` called once with priority 50; 200 with `state=pending`. |
| `TestProcessExistingPending` | Pending job at priority 200 → priority lowered to 50; same `job_id` returned. |
| `TestProcessExistingRunning` | Running job → 200 with `note: "job already running"`; no UPDATE; no Enqueue. |
| `TestProcessExistingFailed` | Failed job → state flipped to `pending`, `attempts` reset to 0, error cleared. |
| `TestProcessConcurrent` | Two concurrent `/process` calls → both return same `job_id`; only one row. |
| `TestProcessAfterDone` | Existing `done` row → fresh `Enqueue` (because terminal); two rows in DB. |
| `TestReprocessRollbacksState` | Video in `ready`, reprocess from `transcribe` → state `audio_extracted`; transcripts `superseded_at` set. |
| `TestReprocessFiresNotify` | Postgres `LISTEN videos.state_changed` → one event with `video_id`, `new_state`. |
| `TestReprocessKeepsOldTranscript` | After reprocess, the old `transcripts` row is still queryable with `?include_superseded=true`. |
| `TestReprocessInFlightTranscribe` | A `transcribe` job is `running`; reprocess enqueues a new chain after it; old job marked `superseded` on completion (covered by Pipeline tests; here we just assert the chain enqueue happened). |
| `TestReprocessFromExtract` | Reprocess from `extract` does **not** mark transcripts superseded (extract is upstream of transcribe). |
| `TestReprocessMissingFile` | Source file missing on disk → 200 (the worker is the source of truth for execution). |

## 8. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Process on a video with both pending and done jobs for the same stage | The latest by `created_at` wins; if it's pending, repriorise; if done, fresh enqueue. | `TestProcessAfterDone` |
| Reprocess from `subtitle_gen` on a video whose subtitle stage has never run | Predecessor is `transcribed`; the chain starts from `subtitle_gen`. The `SupersedeTranscripts` UPDATE matches no rows → no-op. | Integration |
| Process called with an explicit priority of 999 | Repriorise still uses `LEAST(priority, $2)` → if existing is 50, no change. The test enforces the floor logic, not "always lower." | Integration |
| Two reprocess calls in flight | Each opens its own transaction; the second's `SetVideoState` may step on the first's value (last writer wins). This is acceptable: both intend the same predecessor state. | Documented |
| Pipeline gRPC down | 503 `streaming-unavailable` (canonical). Same as Library scan. | Failure injection |
| Reprocess where the existing transcript is already superseded | UPDATE matches zero rows; chain still enqueues. No 409. | Integration |
| Process on a non-existent video | `FindLatestJob` returns `sql.ErrNoRows`; the API still calls Enqueue, which the Pipeline rejects with `INVALID_ARGUMENT`; we map to 404. | `TestProcessUnknownVideo` |
| `priority < 1` | Validator rejects with 422. | Unit |
| Reprocess fires the NOTIFY before transaction commits | The NOTIFY is published *after* commit (we capture `jobIDs` first, then `notify.Publish` outside the closure). | `TestReprocessFiresNotify` (asserts ordering) |

## 9. Acceptance checklist

- [ ] `POST /process` repriorises live jobs and enqueues fresh ones.
- [ ] `POST /process` is idempotent under concurrency.
- [ ] `POST /reprocess` rolls back state, supersedes transcripts (when applicable), enqueues chain, fires NOTIFY.
- [ ] Predecessor map matches Story 1.6 FSM (a unit test imports the canonical constants when available).
- [ ] All `Test*` cases pass on Postgres; SQLite parity for the SQL parts (NOTIFY uses the SQLite pubsub bus).
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.5.
