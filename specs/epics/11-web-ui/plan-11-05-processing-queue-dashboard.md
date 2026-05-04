# Implementation Plan — Story 11.5 Processing Queue Dashboard

> Companion to [story-11-05-processing-queue-dashboard.md](story-11-05-processing-queue-dashboard.md).
> Backed by canonical Epic 7 endpoints `/api/queue/stats`, `/api/jobs/{id}`,
> `/ws/jobs` (REVIEW §1.2.a — the parallel `/api/processing/*` namespace
> is **not** used).

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Route | `/queue` (admin gate is soft — non-admins see read-only). |
| Placement | `web/src/routes/queue/`, `web/src/features/queue/`. |
| Data | `useJobs(filters)` (`GET /api/jobs`), `useQueueStats()` (`GET /api/queue/stats`), WS `/ws/jobs` for delta updates. |
| Render throttle | UI updates per visible job throttled to ≤ 1 Hz (architecture §7.10). |
| Stage names | Canonical: `scan, probe, extract, transcribe, subtitle_gen, index, thumbnail` (REVIEW §1.3.b/c). |
| Out of scope | Job/stage internals (Epic 6); video detail (Story 11.2). |

## 1. Component layout

```
<QueuePage>
 ├─ <StageCards>         per-stage: pending / running / done / failed + oldest_pending_age_sec
 ├─ <Toolbar>            scope (24h | all), bulk-action affordances
 ├─ <JobsTable>          virtualized; one row per processing_jobs row
 │    <JobRow>
 │      ├─ <VideoCell/>   poster + title (link to /watch/{id})
 │      ├─ <StageBadge stage/>
 │      ├─ <StateBadge state heartbeatAgeSec/>   ("stale — being reaped" if ≥ 90s)
 │      ├─ <ProgressBar processedSec totalSec etaSec/>
 │      └─ <RowActions>  pause, resume, cancel, retry, prio-bump, force-pause
 └─ <BulkActionsBar>     pause-all / resume-all / cancel-all (server-side filter)
```

## 2. File layout

| Path | Purpose |
|---|---|
| `web/src/routes/queue/index.tsx` | Lazy route. |
| `web/src/features/queue/QueuePage.tsx` | Page composition. |
| `web/src/features/queue/useJobs.ts` | Infinite query + WS-merged state. |
| `web/src/features/queue/useQueueStats.ts` | Stats query (poll every 5 s; WS pushes invalidate). |
| `web/src/features/queue/wsJobs.ts` | Subscribes to `/ws/jobs`; throttled fan-out per job. |
| `web/src/features/queue/components/StageCards.tsx` | Per-stage summary card. |
| `web/src/features/queue/components/JobRow.tsx` | One row + action menu. |
| `web/src/features/queue/components/ProgressBar.tsx` | Token-driven; clamps to never animate backward. |
| `web/src/features/queue/components/BulkActionsBar.tsx` | Submits server-side filter expression. |
| `web/src/features/queue/components/PartialResultDialog.tsx` | "12 of 50 paused — retry the rest?" flow. |
| `web/src/lib/api/jobs.ts` | Typed wrapper for job CRUD + actions. |

## 3. Data model

```ts
type StageName = 'scan'|'probe'|'extract'|'transcribe'|'subtitle_gen'|'index'|'thumbnail';
type JobState  = 'pending'|'running'|'paused'|'done'|'failed'|'cancelled'|'pause_requested';

type Job = {
  id: string;
  videoId: string;
  videoTitle: string;
  posterUrl: string;
  stage: StageName;
  state: JobState;
  attempts: number;
  priority: number;
  processedSec: number;
  totalSec: number;
  etaSec?: number;
  realtimeFactor?: number;
  lastSegmentEndSec: number;
  lastHeartbeatAt?: string;
  pauseRequested: boolean;
  pauseRequestedAt?: string;
  pauseGraceSec: number;     // computed for force-pause UI
  reapedReason?: string;
  error?: string;
  createdAt: string;
  finishedAt?: string;
};

type QueueStats = {
  perStage: Record<StageName, {
    pending: number; running: number; done: number; failed: number;
    oldest_pending_age_sec: number | null;
  }>;
  workersActive: number;
};
```

## 4. Implementation steps

### 4.1 Initial fetch + WS merge

```ts
const jobsQuery = useInfiniteQuery({
  queryKey: ['jobs', filters],
  queryFn: ({ pageParam }) => api.get('/jobs', { params: { ...filters, cursor: pageParam, limit: 100 } }),
  getNextPageParam: (last) => last.next_cursor ?? undefined,
});

useWsTopic('/ws/jobs', (msg) => {
  // throttle: bucket per jobId, flush at 1 Hz
  enqueueJobUpdate(msg);
});
```

`enqueueJobUpdate` keeps a per-jobId buffer; a single `requestAnimationFrame`-driven flusher applies the latest event per id at most once per second.

### 4.2 Heartbeat / stale detection

```ts
function isStale(job: Job, nowMs: number): boolean {
  if (!job.lastHeartbeatAt) return false;
  return nowMs - Date.parse(job.lastHeartbeatAt) >= 90_000;
}
```

The 90 s threshold matches the canonical 5 s heartbeat × 18 (REVIEW §1.4.c). Stale rows render the "stale — being reaped" badge.

### 4.3 ETA stability

`<ProgressBar>` does not display ETA until `segments_completed >= 3` (avoids unstable EWMA on fresh jobs). When it does, the ETA value is smoothed to ≤ 1 update / 5 s.

### 4.4 Backwards-bar guard

If a WS update lowers `last_segment_end_sec` from the previous render, log a warning and clamp the bar to its prior value.

```ts
const displayed = Math.max(prevDisplayed, job.processedSec);
if (job.processedSec < prevDisplayed) console.warn('job_progress_decreased', { jobId: job.id });
```

### 4.5 Force-pause UI

When `pauseRequested && now - pauseRequestedAt > pauseGraceSec`, the row reveals a "Force pause" button → `POST /api/jobs/{id}/force-pause`.

### 4.6 Bulk actions

Bulk selection emits a server-side filter expression (e.g., `{ stage: 'transcribe', state: 'running' }`) rather than an array of IDs. Endpoint: `POST /api/jobs:bulk-pause { filter }`. Response includes `{ affected, errors }`. If `errors.length > 0`, open `<PartialResultDialog>` offering retry on the failed subset.

### 4.7 Reconnect reconciliation

On `ws.onreconnect`, do one `qc.invalidateQueries(['jobs', filters])` to resync. `useQueueStats` invalidates similarly.

## 5. Edge cases

| Case | Handling |
|---|---|
| 50 jobs running | Render passes 60 fps; CPU < 10%. |
| 1000+ jobs | Virtualize; bulk actions use server-side filter, not ID list. |
| Pause then immediate resume | Idempotent: resume on a `running` job is a no-op (server confirms). |
| Retry of `failed` | `attempts → 0`; `state → pending`. |
| WS down 15 s | One re-fetch on reconnect reconciles. |

## 6. Test cases

### 6.1 Unit

| Test | Asserts |
|---|---|
| `progress bar never animates backwards` | Two updates: 50% then 30% → bar stays at 50%, console warning logged. |
| `eta hidden until 3 segments` | Render with `segments_completed = 2` → no ETA text. |
| `stale detection at 90s` | Job with heartbeat at now-91s → stale badge present. |
| `bulk pause uses filter, not IDs` | Mock POST receives `{ filter: { stage: 'transcribe' } }`, not `{ ids: [...] }`. |
| `force-pause button gated on grace window` | Visible only when `pauseRequested && elapsed > pauseGraceSec`. |
| `reconcile on WS reconnect` | `invalidateQueries` called once per reconnect. |

### 6.2 e2e

| Test | Asserts |
|---|---|
| `pause transcribe enters paused within 60 s` | Click Pause; row state transitions paused at next segment boundary. |
| `partial bulk failure dialog` | Mock returns `{ affected: 12, errors: [...] }` → dialog with retry CTA. |
| `WS disconnect then reconnect` | Disable WS for 15 s; on reconnect, jobs list reconciles. |

## 7. Performance

- `<JobRow>` is a `React.memo` keyed by `(jobId, state, processedSec, etaSec)`.
- WS updates throttled to 1 Hz/job; full table render budget ≤ 8 ms even at 50 visible rows.
- Stats poll falls back from WS-driven invalidation to 5 s setInterval if no WS event in 30 s.

## 8. Dependencies

- API: Epic 7 Stories 7.12 (job control), 7.13 (queue stats), 7.16 (WS).
- Queue internals: Epic 6 Stories 6.4–6.10.
- Indexes (REVIEW §6.1): owned by Epic 6 Story 6.1 plan; this story consumes the resulting `oldest_pending_age_sec` field.
