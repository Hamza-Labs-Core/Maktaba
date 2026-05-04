# Implementation Plan — Story 21.7 Job & Pipeline Visibility

> Companion to [story-21-07-job-pipeline-visibility.md](story-21-07-job-pipeline-visibility.md).
> Extend Epic-7 endpoints; add `/api/queue/stats` fields, segment-by-segment progress,
> single `WS /ws/jobs?job_id=` filtered surface; admin panel charts; route-namespace lint.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Endpoints | `GET /api/queue/stats` (extended), `GET /api/jobs/{id}` (extended), `WS /ws/jobs` (filtered). |
| Disallowed | `/api/processing/*`. CI lint asserts no chi route registers under that prefix. |
| Stuck classification | Heartbeat > 3× `heartbeat_interval` ⇒ `state=stuck` on the wire while DB still says `running`. |
| Privileged paths | Mask to `<library_root>/<rel>` for non-admins. |

## 1. Project layout

```
api/internal/handlers/
├── queue_stats.go               # Extended payload
├── job_detail.go                # Full state-machine history + last 50 errors
├── ws_jobs.go                   # Single endpoint, query-filtered
└── handlers_test.go

api/internal/jobs/
├── stuck.go                     # heartbeat→stuck classifier
└── path_mask.go                 # privileged-data masking

shared/db/queries/
└── queue_stats.sql              # named queries used by stats

api/internal/middleware/
└── route_lint.go                # CI: forbid /api/processing/*

web/src/admin/
├── QueueDashboard.tsx
├── JobList.tsx
└── JobDetail.tsx
```

## 2. Queue stats payload

```go
// api/internal/handlers/queue_stats.go
type QueueStats struct {
    ByStageState map[string]map[string]int64        `json:"by_stage_state"`            // stage→state→n
    ByLibrary    map[string]LibraryQueueStats       `json:"by_library"`
    GlobalDepth  int64                              `json:"global_depth"`
    InProgress   int64                              `json:"in_progress"`
    OldestPendingAgeS int64                         `json:"oldest_pending_age_s"`
    StageRolling1hAvg map[string]float64            `json:"stage_rolling_1h_avg_s"`
    LastErrors []ErrorEntry                         `json:"last_errors"`
}

type ErrorEntry struct {
    ErrorID   uuid.UUID `json:"error_id"`
    Category  string    `json:"category"`           // always "job" for this surface
    CreatedAt time.Time `json:"created_at"`         // matches audit_log.created_at
    Stage     string    `json:"stage"`
    JobID     string    `json:"job_id"`
}
```

## 3. Stats SQL

```sql
-- shared/db/queries/queue_stats.sql

-- name: QueueByStageState :many
SELECT stage, state, COUNT(*) AS n
  FROM processing_jobs
 GROUP BY stage, state;

-- name: QueueOldestPendingAge :one
SELECT EXTRACT(EPOCH FROM (now() - MIN(created_at)))::bigint AS age_s
  FROM processing_jobs WHERE state='pending';

-- name: QueueGlobalDepth :one
SELECT COUNT(*) AS depth FROM processing_jobs WHERE state IN ('pending','running');

-- name: QueueStageRolling1hAvg :many
SELECT stage, AVG(EXTRACT(EPOCH FROM (finished_at - started_at)))::float AS avg_s
  FROM processing_jobs
 WHERE finished_at >= now() - interval '1 hour'
   AND state='done'
 GROUP BY stage;

-- name: QueueLast50Errors :many
-- Audit-log linkage uses category='job' and event='job.error'; this
-- pair is the canonical audit row emitted by the failure path in
-- plan-21-05. We do NOT cast target_id::uuid because audit_log.target_id
-- is TEXT (per architecture §8.6.1 / plan-21-06): it carries multiple
-- id shapes across categories. Matching by target_kind='job' first
-- ensures every selected row has a UUID-shaped target_id, and casting
-- to UUID is safe within the qualified subset. We compare by string
-- equality against j.id::text so the index on (target_kind,target_id)
-- in plan-21-06 is used directly without a per-row cast.
SELECT j.id AS job_id, j.stage, j.last_error_id AS error_id,
       a.category, a.created_at
  FROM processing_jobs j
  JOIN audit_log a
    ON a.target_kind = 'job'
   AND a.target_id   = j.id::text
   AND a.category    = 'job'
   AND a.event       = 'job.error'
 WHERE j.state = 'failed'
 ORDER BY a.created_at DESC
 LIMIT 50;
```

Indexes from Story 18.7 cover these queries.

## 4. Job detail payload

```go
// api/internal/handlers/job_detail.go
type JobDetail struct {
    ID            uuid.UUID
    Stage         string
    State         string             // includes synthetic "stuck"
    Library       string
    Video         string
    Attempt       int
    Worker        string
    HeartbeatAt   time.Time
    StateHistory  []StateTransition  // from job_state_log table
    SegmentProgress []SegmentMark    // for transcribe
    LastErrorID   *uuid.UUID
    Path          string             // masked for non-admin
}

type SegmentMark struct {
    Index    int
    StartSec float64
    EndSec   float64
    AtTs     time.Time
}
```

`SegmentProgress` reads from `processing_jobs.last_segment_end_sec` plus a `job_segment_progress(job_id, idx, end_sec, at)` table appended on every transcribe segment commit.

### 4.1 `job.segment_progress` WS event envelope

The WS event uses the canonical envelope defined in **architecture §7.10**
(WebSocket job-progress events). The field names below align with that
section so any client implementing §7.10 can decode the event without
local additions:

```json
{
  "type":       "job.segment_progress",
  "schema":     "v1",
  "ts":         "2026-05-04T12:34:56Z",
  "job_id":     "01HXXX...",
  "stage":      "transcribe",
  "data": {
    "idx":          7,
    "start_sec":    140.0,
    "end_sec":      160.0,
    "duration_sec": 1832.0,
    "progress":     0.087
  }
}
```

`type`, `schema`, `ts`, `job_id`, and `stage` are envelope fields owned
by §7.10. `data` is the event-specific payload. The trace_id (when
present) is carried in the `data` object as `trace_id` to align with
the LISTEN/NOTIFY trace-continuity contract (plan-21-03 §8.1).

## 5. WS endpoint with filter

```go
// api/internal/handlers/ws_jobs.go
func (h *Handler) WSJobs(w http.ResponseWriter, r *http.Request) {
    jobID := r.URL.Query().Get("job_id")              // empty = all
    user  := userFrom(r)
    conn, err := upgrader.Upgrade(w, r, nil); if err != nil { return }
    sub := h.bus.Subscribe(r.Context(), user, busFilter{JobID: jobID})
    defer sub.Close()

    pump := time.NewTicker(time.Second)             // EC2 batching cadence
    defer pump.Stop()

    var batch []Event
    for {
        select {
        case <-r.Context().Done(): return
        case ev := <-sub.Ch:
            if !mayObserve(user, ev) { continue }
            batch = append(batch, ev)
        case <-pump.C:
            if len(batch) > 0 {
                _ = conn.WriteJSON(batch)
                batch = batch[:0]
            }
        }
    }
}
```

EC2: per-second batching caps server-side fan-out cost when the same job
has 100+ subscribers.

> **Independence from UI throttle.** The 1 Hz server-side batching is
> orthogonal to any UI-side throttle. The web admin panel may render at
> a slower cadence (e.g., 2 s) by coalescing batches client-side; the
> server makes no assumption about render rate. Conversely, a low-power
> client may apply an additional throttle without affecting server cost.
> Tests must therefore measure event delivery counts, not rendered frames.

## 6. Stuck classifier

```go
// api/internal/jobs/stuck.go
const stuckMultiple = 3

func ClassifyState(j ProcessingJob, hbInterval time.Duration) string {
    if j.State != "running" { return j.State }
    if time.Since(j.HeartbeatAt) > stuckMultiple*hbInterval {
        return "stuck"
    }
    return j.State
}
```

Applied at JSON serialization; DB row remains `running`. **Both surfaces
must call `ClassifyState`:**
- REST `GET /api/jobs/{id}` — invoked in `job_detail.go` before encoding
  the response struct.
- WS `/ws/jobs?job_id=` — invoked in `ws_jobs.go`'s event-builder before
  every `conn.WriteJSON(batch)` call so a stuck job surfaces in the WS
  stream as well as in REST polls.

A shared helper (`jobs.ApplyClassify(j, hbInterval)`) is used by both
sites so the rule cannot drift.

## 7. Path masking

```go
// api/internal/jobs/path_mask.go
func MaskPath(role string, libID, libRoot, full string) string {
    if role == "admin" { return full }
    rel, err := filepath.Rel(libRoot, full)
    if err != nil { return "<library>/?" }
    return fmt.Sprintf("<library>/%s", rel)
}
```

EC3 mapping.

## 8. Route-namespace lint

```go
// api/internal/middleware/route_lint.go
//go:build routelint

func main() {
    r := api.NewRouter()
    forbidden := []string{"/api/processing/"}
    err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
        for _, p := range forbidden {
            if strings.HasPrefix(route, p) {
                return fmt.Errorf("forbidden route %q", route)
            }
        }
        return nil
    })
    if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
}
```

`make lint:routes` runs in PR CI.

## 9. Admin panel

```tsx
// web/src/admin/QueueDashboard.tsx
export function QueueDashboard() {
    const { data: stats } = useQueueStats({ pollMs: 2000 });
    return (
        <Stack gap="lg">
            <KpiRow stats={stats} />
            <QueueDepthChart series={stats.depthSeries} />
            <StageThroughputChart avg={stats.stage_rolling_1h_avg_s} />
            <JobList lastErrors={stats.last_errors} />
        </Stack>
    );
}
```

`JobList.tsx` chip-filters on stage, state, library; clicking a row opens `JobDetail` which subscribes to `WS /ws/jobs?job_id=…`.

## 10. Test cases

### TC1 — Snapshot
Seed 100 jobs across stages. Hit `GET /api/queue/stats`. Assert payload counts match a hand-counted SQL run. Run `EXPLAIN ANALYZE` for each named query and store snapshot under `tests/explain/postgres/QueueByStageState.txt`; assert `using_index:processing_jobs_*`.

### TC2 — Live progress
Drop a 60 s clip. Connect `WS /ws/jobs?job_id=<id>`. Wait until job done. Assert: ≥ 6 `job.segment_progress` events received in order, each with `idx=0..N`. End-to-end latency from server commit to client paint ≤ 1 s (measured by client send-time vs. event-time).

### TC3 — Errored job
Drop a synthetic file with corrupt moov. Job fails. `GET /api/queue/stats`'s `last_errors[0]` has the `error_id`, `category=ffmpeg`, `job_id`. UI shows row with link to job detail.

### TC4 — No parallel surface
`GET /api/processing/status` returns 404. `make lint:routes` passes. Add `r.Get("/api/processing/foo", h)` in a fixture; lint fails naming the route.

### EC1 — Stuck (REST + WS parity)
Force a worker to stop heartbeating without exiting. After
`3 × hbInterval`:
- `GET /api/jobs/{id}` returns `state=stuck`.
- A connected `WS /ws/jobs?job_id=<id>` client receives a state-update
  event (or the next batched event) with `state=stuck`.
- DB row still `running`.
- Reaper (Story 19.4) eventually requeues.

The test asserts identical `state` strings from both surfaces in the
same observation window so any future change that lands on only one
side regresses the test.

### EC2 — 100 subscribers
100 WS clients on same job. Fire 1,000 progress events server-side over 60 s. Each client receives all events. Per-second batching keeps server CPU bounded; metric `ws_send_batches_per_second` ~60.

### EC3 — Path mask
As non-admin: `GET /api/jobs/{id}` shows `path=<library>/folder/foo.mp4`. As admin, full absolute path.

## 11. Edge cases summary

| Case | Source | Handling |
|---|---|---|
| EC1 stuck | story | Synthetic state at JSON time. |
| EC2 fan-out | story | Per-second batching. |
| EC3 path masking | story | Role-aware helper. |
| WS filter bypass | impl | Server-side filter; client `job_id` is hint only. |
| Parallel namespace creep | impl | Lint at PR time. |

## 12. Configuration

```yaml
queue_visibility:
  ws_batch_interval: 1s
  stuck_multiple: 3
  last_errors_limit: 50
  rolling_avg_window: 1h
```

## 13. Dependencies

- Story 7.13/7.16 Epic 7 (endpoints exist; we extend payloads).
- Story 18.7 (indexes that make stats queries fast).
- Story 19.4 (heartbeat semantics).
- Story 21.5 (`error_id`, `category` fields).
- Story 21.6 (audit `job_error` rows back the error list).
