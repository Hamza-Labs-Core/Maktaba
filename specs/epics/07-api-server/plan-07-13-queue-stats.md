# Implementation Plan — Story 7.13 Queue Stats Endpoint

> Companion to [story-07-13-queue-stats.md](story-07-13-queue-stats.md).
> Single canonical surface for queue health.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Routes | `GET /api/queue/stats` (aggregate snapshot) and `GET /api/jobs?state&stage&video&cursor` (per-row listing per architecture §9.5). The list endpoint is mounted by plan-07-12 — its SQL composition lives here, alongside the same partial indexes. |
| Storage | Reads `processing_jobs` + a synthetic `workers` view (claimed-by + last_heartbeat). The seven canonical stages are `scan, probe, extract, transcribe, subtitle_gen, index, thumbnail` (architecture §3). |
| Indexes | This story owns three partial indexes (AC-2) plus the cursor-supporting `(updated_at DESC, id DESC)` index for the list endpoint. Pipeline Story 6.1 may already declare some; we add only the missing ones in a fresh migration. |
| Out of scope | The workers table itself (workers are encoded as `claimed_by` strings; no separate table). The bulk-update SQL (plan-07-12 owns it). |

## 1. Architecture diagram

```
   GET /api/queue/stats
        │
        ▼
   ┌─────────────────────────────────────────────────────────────┐
   │ One round trip composing the response with several CTEs:    │
   │                                                             │
   │ WITH                                                        │
   │  by_stage_state AS (                                        │
   │    SELECT stage, state, count(*) AS n                       │
   │      FROM processing_jobs                                   │
   │     WHERE state IN ('pending','running','paused','failed')  │
   │     GROUP BY stage, state),                                 │
   │  done24 AS (                                                │
   │    SELECT stage, count(*) AS n                              │
   │      FROM processing_jobs                                   │
   │     WHERE state IN ('done','failed')                         │
   │       AND finished_at >= now() - interval '24 hours'        │
   │     GROUP BY stage),                                        │
   │  workers AS (                                               │
   │    SELECT claimed_by AS id,                                 │
   │           id AS current_job_id,                             │
   │           max(last_heartbeat_at) AS last_heartbeat          │
   │      FROM processing_jobs                                   │
   │     WHERE state = 'running'                                 │
   │     GROUP BY claimed_by, id),                               │
   │  oldest AS (                                                │
   │    SELECT extract(epoch FROM (now()-min(created_at)))       │
   │      FROM processing_jobs WHERE state = 'pending'),         │
   │  eta AS (                                                   │
   │    SELECT stage, sum(estimated_remaining_sec) AS s,         │
   │                  count(*) AS n                              │
   │      FROM processing_jobs WHERE state = 'running'           │
   │     GROUP BY stage)                                         │
   │ SELECT json_build_object(                                   │
   │    'by_stage', ...,                                         │
   │    'eta_sec', ...,                                          │
   │    'total_in_flight', ...,                                  │
   │    'oldest_pending_age_sec', ...,                           │
   │    'workers', ...                                           │
   │ ) FROM ...                                                  │
   └─────────────────────────────────────────────────────────────┘
```

## 2. New files

| Path | Purpose |
|---|---|
| `api/internal/queue/handler.go` | Route. |
| `api/internal/queue/stats.go` | Service. |
| `api/internal/queue/types.go` | DTO. |
| `api/internal/queue/handler_test.go` | Integration. |
| `shared/db/queries/queue_stats.sql` | sqlc inputs. |
| `shared/db/migrations/0018_processing_jobs_stats_indexes.sql` | The three indexes from AC-2. |

## 3. SQL — indexes

`shared/db/migrations/0018_processing_jobs_stats_indexes.sql`:

```sql
-- +goose Up
-- +goose StatementBegin
CREATE INDEX IF NOT EXISTS pj_done24_idx
    ON processing_jobs (stage, finished_at)
    WHERE state IN ('done','failed');

CREATE INDEX IF NOT EXISTS pj_oldest_pending_idx
    ON processing_jobs (state, created_at)
    WHERE state = 'pending';

CREATE INDEX IF NOT EXISTS pj_workers_idx
    ON processing_jobs (state, claimed_by, last_heartbeat_at)
    WHERE state = 'running';

-- Supports the GET /api/jobs?state&stage&video listing endpoint
-- (architecture §9.5). The cursor primitive uses (updated_at DESC, id DESC).
CREATE INDEX IF NOT EXISTS pj_list_cursor_idx
    ON processing_jobs (updated_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS pj_list_cursor_idx;
DROP INDEX IF EXISTS pj_workers_idx;
DROP INDEX IF EXISTS pj_oldest_pending_idx;
DROP INDEX IF EXISTS pj_done24_idx;
-- +goose StatementEnd
```

## 4. Type definitions

```go
// api/internal/queue/types.go
package queue

import "time"

type StageStats struct {
    Pending int `json:"pending"`
    Running int `json:"running"`
    Paused  int `json:"paused"`
    Failed  int `json:"failed"`
    Done24h int `json:"done_24h"`
}

type Worker struct {
    ID            string     `json:"id"`
    Host          string     `json:"host"`            // derived from claimed_by ("worker-host-pid")
    LastHeartbeat *time.Time `json:"last_heartbeat"`
    CurrentJobID  *int64     `json:"current_job_id"`
}

type Stats struct {
    ByStage              map[string]StageStats `json:"by_stage"`
    EtaSec               float64               `json:"eta_sec"`
    TotalInFlight        int                   `json:"total_in_flight"`
    OldestPendingAgeSec  float64               `json:"oldest_pending_age_sec"`
    Workers              []Worker              `json:"workers"`
}
```

## 5. Service

```go
// api/internal/queue/stats.go
package queue

import (
    "context"

    "maktaba/api/internal/httperror"
)

// allStages reflects the canonical enum (Epic 6 Story 6.1).
var allStages = []string{"scan","probe","extract","transcribe","subtitle_gen","index","thumbnail"}

// perStageParallelism is read from app_settings; default below.
var defaultParallelism = map[string]int{
    "scan": 1, "probe": 4, "extract": 2, "transcribe": 1,
    "subtitle_gen": 2, "index": 4, "thumbnail": 4,
}

func (s *service) stats(ctx context.Context) (*Stats, *httperror.Error) {
    raw, err := s.db.QueueStats(ctx)
    if err != nil { return nil, httperror.Internal("queue stats") }

    out := Stats{ ByStage: map[string]StageStats{} }
    for _, stage := range allStages { out.ByStage[stage] = StageStats{} }

    for _, r := range raw.ByStageState {
        s := out.ByStage[r.Stage]
        switch r.State {
        case "pending": s.Pending = int(r.N)
        case "running": s.Running = int(r.N)
        case "paused":  s.Paused = int(r.N)
        case "failed":  s.Failed = int(r.N)
        }
        out.ByStage[r.Stage] = s
    }
    for _, r := range raw.Done24 {
        s := out.ByStage[r.Stage]
        s.Done24h = int(r.N)
        out.ByStage[r.Stage] = s
    }
    for stage, eta := range raw.EtaByStage {
        p := defaultParallelism[stage]
        if p < 1 { p = 1 }
        out.EtaSec += eta.SumSec / float64(p)
    }
    out.TotalInFlight = raw.TotalRunning
    out.OldestPendingAgeSec = raw.OldestPendingSec
    for _, w := range raw.Workers {
        out.Workers = append(out.Workers, Worker{
            ID: w.ID, Host: hostFromClaimedBy(w.ID),
            LastHeartbeat: w.LastHeartbeat, CurrentJobID: &w.JobID,
        })
    }
    return &out, nil
}

// hostFromClaimedBy parses "host-1234" → "host". Pipeline workers set
// claimed_by as "<hostname>-<pid>" by convention.
func hostFromClaimedBy(s string) string {
    i := strings.LastIndex(s, "-")
    if i < 0 { return s }
    return s[:i]
}
```

## 6. SQL — sqlc inputs

`shared/db/queries/queue_stats.sql`:

```sql
-- name: QueueByStageState :many
SELECT stage, state, count(*)::int AS n
  FROM processing_jobs
 WHERE state IN ('pending','running','paused','failed')
 GROUP BY stage, state;

-- name: QueueDone24 :many
SELECT stage, count(*)::int AS n
  FROM processing_jobs
 WHERE state IN ('done','failed')
   AND finished_at >= now() - interval '24 hours'
 GROUP BY stage;

-- name: QueueWorkers :many
SELECT claimed_by AS id,
       id AS job_id,
       last_heartbeat_at AS last_heartbeat
  FROM processing_jobs
 WHERE state = 'running';

-- name: QueueOldestPending :one
SELECT COALESCE(extract(epoch FROM (now() - min(created_at))), 0)::float
  FROM processing_jobs
 WHERE state = 'pending';

-- name: QueueEtaByStage :many
SELECT stage,
       COALESCE(sum(estimated_remaining_sec), 0)::float AS sum_sec,
       count(*)::int AS n
  FROM processing_jobs
 WHERE state = 'running'
 GROUP BY stage;

-- name: QueueTotalRunning :one
SELECT count(*)::int FROM processing_jobs WHERE state = 'running';

-- name: ListJobs :many
-- Drives GET /api/jobs?state&stage&video&cursor. NULL params skip the
-- predicate. processing_jobs.id is BIGSERIAL → cursor uses
-- paginate.IDKindBigint.
SELECT id, video_id, stage, state, priority, attempts, last_error,
       created_at, updated_at
  FROM processing_jobs
 WHERE ($1::text IS NULL OR state    = $1)
   AND ($2::text IS NULL OR stage    = $2)
   AND ($3::uuid IS NULL OR video_id = $3)
   /* paginate.Where fragment inserted at $4..$5 (updated_at, id) */
 ORDER BY updated_at DESC, id DESC
 LIMIT $6;
```

`stage` is validated against the seven canonical stages
(`scan, probe, extract, transcribe, subtitle_gen, index, thumbnail`)
before the query runs; unknown stages return 400.

The Go service issues these in one `BeginTx → run all → Commit` block to
keep the snapshot consistent. Six queries, but the planner uses tiny
indexed scans; total time stays well under 30 ms per AC.

## 7. Handler scaffolding

```go
// api/internal/queue/handler.go
package queue

import (
    "encoding/json"
    "net/http"

    "maktaba/api/internal/httperror"
)

func Mount(r chi.Router, d Deps) {
    h := &handler{d}
    r.Get("/api/queue/stats", h.stats)
}

func (h *handler) stats(w http.ResponseWriter, r *http.Request) {
    out, perr := h.svc.stats(r.Context())
    if perr != nil { httperror.Write(w, r, perr); return }
    json.NewEncoder(w).Encode(out)
}
```

## 8. Test plan

### 8.1 Integration (`handler_test.go`)

| Test | What it pins |
|---|---|
| `TestEmptyQueue` | No jobs → response has every stage with zeros; `total_in_flight=0`; `workers=[]`. |
| `TestMixedQueue` | Seeded with 3 pending, 2 running, 1 paused, 1 failed in `transcribe`; 5 done in last hour → expected counts. |
| `TestEtaSec` | 3 running jobs of total `estimated_remaining_sec=600` each, parallelism 1 → `eta_sec=600`. |
| `TestEtaSecWithParallelism` | 4 running probes, 600s each, parallelism 4 → `eta_sec=600`. |
| `TestOldestPending` | Pending row 90s old → `oldest_pending_age_sec ≈ 90`. |
| `TestWorkersWithStaleHeartbeat` | Running job with NULL `last_heartbeat_at` → worker entry has `last_heartbeat: null`. |
| `TestPerformance100k` | 100k-row fixture → `< 30 ms` (CI nightly). |
| `TestSingleSnapshot` | All sub-queries run inside one TX → no count drift across stages. |

## 9. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Stage with no jobs | Still appears with all zeros. | `TestEmptyQueue` |
| Worker stale (no heartbeat in N minutes) | Returned with `last_heartbeat: null` so the UI can flag it. | `TestWorkersWithStaleHeartbeat` |
| Multiple jobs claimed by the same worker | Only the latest by `last_heartbeat` is returned (workers can only run one job at a time per the queue invariant; if more rows appear, it's a bug to surface, not hide). | Documented |
| `parallelism` config out of range (≤0) | Defaults to 1. | Service unit |
| `done_24h` includes failed | Yes — both terminal states count. | Documented in API ref. |
| 100k-row table query plan changes | The three partial indexes from §3 keep query under 30 ms; CI nightly perf test catches regressions. | `TestPerformance100k` |

## 10. Acceptance checklist

- [ ] Response shape matches AC-1 verbatim.
- [ ] Three indexes from §3 land in `0018_processing_jobs_stats_indexes.sql`.
- [ ] Single transaction → consistent snapshot.
- [ ] Performance < 30 ms on 100k-row fixture (nightly).
- [ ] Stages with zero jobs still appear with zeros.
- [ ] All `Test*` cases pass.
- [ ] `specs/epics/07-api-server/README.md` ticks story 7.13.
