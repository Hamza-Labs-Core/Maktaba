# Implementation Plan — Story 6.4 Pause, Resume, Cancel via Request Flags

> Companion to [story-06-04-pause-resume-cancel.md](story-06-04-pause-resume-cancel.md).
> The story states *what* and *why*; this plan states *how*.
> The pause/resume protocol is owned by [architecture.md §7.7](../../architecture.md);
> channel naming follows the [README](README.md): `jobs.flag_set`,
> `jobs.force_pause`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language split | Go (API) for the HTTP endpoints + the `jobs.flag_set` / `jobs.force_pause` notify emit. Python (Pipeline) for the worker-side flag observation and force-pause subprocess abort handling. |
| Files (Go) | `api/internal/jobs/control.go` (handlers), `api/internal/jobs/control_test.go`, `shared/db/queries/jobs_control.sql` (sqlc input). |
| Files (Python) | `pipeline/src/maktaba_pipeline/pipeline/control.py` (flag poll, abort listener), `pipeline/tests/pipeline/test_control.py`. |
| Dependency | `processing_jobs` (Story 6.1), claim loop (6.2), heartbeat/progress (6.3). |
| Out of scope | Per-segment cooperative checkpointing inside the transcribe loop (Epic 3 Story 3.6 / 3.7); the actual subprocess kill chain for force-pause's ffmpeg/STT children (Epic 3 Story 3.7). |

## 1. Architecture diagram

```
                        ┌────────────────────────┐
   POST /api/jobs/{id}/ │   API handler          │
   pause | resume |     │   (Go, jobs/control.go)│
   pause?force=true |   └───────────┬────────────┘
   cancel                          │
                                   │ ┌─────────────────────────┐
                                   ├─►│  set request flag UPDATE │
                                   │ │  + NOTIFY jobs.flag_set  │
                                   │ │   payload {id, flag}     │
                                   │ └─────────────────────────┘
                                   │
                                   │ if pause?force=true:
                                   │ ┌─────────────────────────┐
                                   └─►│  force-pause UPDATE      │
                                     │  state→paused;           │
                                     │  paused_at=now;          │
                                     │  paused_at_sec=          │
                                     │   last_segment_end_sec;  │
                                     │  pause_requested=false;  │
                                     │  claimed_by=NULL         │
                                     │ + NOTIFY jobs.force_pause│
                                     │   payload {id}           │
                                     └─────────────────────────┘
                                                │
                                                ▼
       ┌─────────────────────────────────────────────────────────┐
       │ Pipeline worker (Python, pipeline/control.py)           │
       │                                                         │
       │  Two observation paths:                                 │
       │   1. Per-segment poll (cheap, indexed PK SELECT):       │
       │      after each tick, run                               │
       │        SELECT pause_requested, cancel_requested         │
       │          FROM processing_jobs WHERE id = $1             │
       │      → if pause: walk into mark_paused                  │
       │      → if cancel: walk into mark_cancelled              │
       │                                                         │
       │   2. LISTEN jobs.force_pause (Postgres) /               │
       │      PubsubBus subscribe (SQLite):                      │
       │      payload {id} → look up the worker's job; cancel    │
       │      the underlying subprocess; do NOT mutate state     │
       │      (the API has already done so).                     │
       └─────────────────────────────────────────────────────────┘
```

The control plane owns no state transitions other than:

- `pause_requested = true / false`
- `cancel_requested = true`
- *Force-pause only:* the `state → paused` transition.

Every other state transition is performed by the worker, in cooperation
with the request flags.

## 2. Implementation steps

### 2.1 New files (Go)

| Path | Purpose |
|---|---|
| `api/internal/jobs/control.go` | HTTP handlers: `Pause`, `Resume`, `Cancel`. |
| `api/internal/jobs/control_test.go` | Handler-level tests (table-driven; uses test DB). |
| `shared/db/queries/jobs_control.sql` | sqlc input for `SetPauseRequested`, `SetResumeRequested`, `SetCancelRequested`, `ForcePause`. |
| `api/internal/jobs/notify.go` | Thin wrapper around `pg_notify` for the four control channels (used by Story 6.4 and 6.6). |

### 2.2 New files (Python)

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/control.py` | `should_pause`, `should_cancel`, `ForcePauseListener`. |
| `pipeline/tests/pipeline/test_control.py` | Tests for the worker-side observation. |

### 2.3 Modified files

| Path | Change |
|---|---|
| `api/internal/server/router.go` | Wire `POST /api/jobs/{id}/pause`, `/resume`, `/cancel`. |
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | Spawn `ForcePauseListener` per worker. |
| `pipeline/src/maktaba_pipeline/db/jobs_state.py` | Add `mark_paused(job_id, *, at_sec, reason)`, `mark_cancelled(job_id, *, at_sec)`. |

### 2.4 HTTP contract

| Method + path | Body | Response | Notes |
|---|---|---|---|
| `POST /api/jobs/{id}/pause` | empty | `200 {id, state}` | Sets `pause_requested = true`. Idempotent. |
| `POST /api/jobs/{id}/pause?force=true` | empty | `200 {id, state: 'paused', paused_at_sec}` | Force path. Also notifies `jobs.force_pause`. |
| `POST /api/jobs/{id}/resume` | empty | `200 {id, state}` | Sets `pause_requested = false`. No state change. |
| `POST /api/jobs/{id}/cancel` | empty | `200 {id, state}` | Sets `cancel_requested = true`. Worker flips to `cancelled` on next per-segment check. |

All four endpoints return `404` if the id doesn't exist, `409` if the
job is already in a terminal state and the requested action would be
nonsensical (e.g., cancel on a `done` job).

## 3. SQL — control queries

`shared/db/queries/jobs_control.sql`:

```sql
-- name: SetPauseRequested :one
UPDATE processing_jobs
   SET pause_requested = true
 WHERE id = $1
   AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
RETURNING id, state, pause_requested, cancel_requested,
          last_segment_end_sec, paused_at, paused_at_sec;

-- name: SetResumeRequested :one
UPDATE processing_jobs
   SET pause_requested = false
 WHERE id = $1
   AND state IN ('pending', 'paused')
RETURNING id, state;

-- name: SetCancelRequested :one
UPDATE processing_jobs
   SET cancel_requested = true
 WHERE id = $1
   AND state IN ('pending', 'claimed', 'running', 'resuming', 'paused')
RETURNING id, state;

-- name: ForcePause :one
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = last_segment_end_sec,
       paused_reason    = 'user',
       pause_requested  = false,
       claimed_by       = NULL
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id, state, paused_at_sec;

-- name: GetJobState :one
SELECT id, state, pause_requested, cancel_requested,
       paused_at, paused_at_sec, last_segment_end_sec
  FROM processing_jobs WHERE id = $1;
```

The `state IN (...)` predicates make the UPDATEs no-ops when the request
doesn't apply (e.g., resume on a `running` job is fine and returns
`{state: 'running'}`; cancel on a `done` job returns `null` and the
handler maps it to `409`).

## 4. Go code scaffolding

`api/internal/jobs/control.go`:

```go
package jobs

import (
    "context"
    "encoding/json"
    "errors"
    "log/slog"
    "net/http"

    "github.com/go-chi/chi/v5"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"

    "maktaba/api/internal/db"
)

type ControlHandlers struct {
    Q    *db.Queries
    Pool *pgxpool.Pool
    Log  *slog.Logger
}

const (
    chanFlagSet    = "jobs.flag_set"
    chanForcePause = "jobs.force_pause"
)

// POST /api/jobs/{id}/pause(?force=true)
func (h *ControlHandlers) Pause(w http.ResponseWriter, r *http.Request) {
    id, ok := parseJobID(w, r)
    if !ok {
        return
    }
    force := r.URL.Query().Get("force") == "true"

    ctx := r.Context()
    tx, err := h.Pool.Begin(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    defer tx.Rollback(ctx)
    qtx := h.Q.WithTx(tx)

    row, err := qtx.SetPauseRequested(ctx, id)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            httpStatus(w, http.StatusNotFound, "job not found or already terminal")
            return
        }
        httpInternal(w, h.Log, err)
        return
    }

    if err := pgNotify(ctx, tx, chanFlagSet, map[string]any{
        "id": id, "flag": "pause",
    }); err != nil {
        httpInternal(w, h.Log, err)
        return
    }

    var pausedAtSec *float64
    state := row.State
    if force {
        forced, err := qtx.ForcePause(ctx, id)
        if err == nil {
            state = forced.State
            pausedAtSec = &forced.PausedAtSec.Float64
            if err := pgNotify(ctx, tx, chanForcePause, map[string]any{
                "id": id,
            }); err != nil {
                httpInternal(w, h.Log, err)
                return
            }
        } else if !errors.Is(err, pgx.ErrNoRows) {
            httpInternal(w, h.Log, err)
            return
        }
        // ErrNoRows here means the job wasn't in claimed/running/resuming —
        // either already paused or pending. The pause_requested flag is set;
        // when a worker eventually claims, it will see the flag.
    }

    if err := tx.Commit(ctx); err != nil {
        httpInternal(w, h.Log, err)
        return
    }

    body := map[string]any{"id": id, "state": state}
    if pausedAtSec != nil {
        body["paused_at_sec"] = *pausedAtSec
    }
    httpJSON(w, http.StatusOK, body)
}

// POST /api/jobs/{id}/resume
func (h *ControlHandlers) Resume(w http.ResponseWriter, r *http.Request) {
    id, ok := parseJobID(w, r)
    if !ok {
        return
    }
    ctx := r.Context()
    tx, err := h.Pool.Begin(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    defer tx.Rollback(ctx)
    qtx := h.Q.WithTx(tx)

    row, err := qtx.SetResumeRequested(ctx, id)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            httpStatus(w, http.StatusNotFound,
                "job not found or not in a resumable state")
            return
        }
        httpInternal(w, h.Log, err)
        return
    }
    if err := pgNotify(ctx, tx, chanFlagSet, map[string]any{
        "id": id, "flag": "resume",
    }); err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    if err := tx.Commit(ctx); err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    httpJSON(w, http.StatusOK, map[string]any{
        "id": id, "state": row.State,
    })
}

// POST /api/jobs/{id}/cancel
func (h *ControlHandlers) Cancel(w http.ResponseWriter, r *http.Request) {
    id, ok := parseJobID(w, r)
    if !ok {
        return
    }
    ctx := r.Context()
    tx, err := h.Pool.Begin(ctx)
    if err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    defer tx.Rollback(ctx)
    qtx := h.Q.WithTx(tx)

    row, err := qtx.SetCancelRequested(ctx, id)
    if err != nil {
        if errors.Is(err, pgx.ErrNoRows) {
            httpStatus(w, http.StatusNotFound,
                "job not found or already terminal")
            return
        }
        httpInternal(w, h.Log, err)
        return
    }
    if err := pgNotify(ctx, tx, chanFlagSet, map[string]any{
        "id": id, "flag": "cancel",
    }); err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    if err := tx.Commit(ctx); err != nil {
        httpInternal(w, h.Log, err)
        return
    }
    httpJSON(w, http.StatusOK, map[string]any{
        "id": id, "state": row.State,
    })
}

// pgNotify is a one-line helper that runs `SELECT pg_notify($1, $2)`
// inside the open transaction, so the notify only fires on commit.
func pgNotify(ctx context.Context, tx pgx.Tx, channel string, payload any) error {
    body, err := json.Marshal(payload)
    if err != nil {
        return err
    }
    _, err = tx.Exec(ctx, "SELECT pg_notify($1, $2)", channel, string(body))
    return err
}
```

The notify is inside the same transaction as the flag UPDATE → it fires
exactly when the row is durably visible. If the commit fails, no notify
fires; the API returns 5xx and the worker never sees a phantom request.

### 4.1 Router wiring

`api/internal/server/router.go` (excerpt):

```go
r.Route("/api/jobs/{id}", func(r chi.Router) {
    r.Post("/pause",  ctrl.Pause)
    r.Post("/resume", ctrl.Resume)
    r.Post("/cancel", ctrl.Cancel)
})
```

## 5. Python code scaffolding — worker observation

`pipeline/src/maktaba_pipeline/pipeline/control.py`:

```python
"""Worker-side observation of the request flags and force-pause notifies.

Two cheap queries + one LISTEN. The per-segment poll is the primary
mechanism (architecture §7.7); the LISTEN is a fast path for force-pause
that lets the worker abort the in-flight subprocess without waiting for
the next segment commit.
"""
from __future__ import annotations

import asyncio
import logging
import signal
from dataclasses import dataclass

from ..db.pubsub import JOBS_FORCE_PAUSE, get_bus


log = logging.getLogger(__name__)


_FLAG_POLL_SQL = """
SELECT pause_requested, cancel_requested
  FROM processing_jobs
 WHERE id = $1
"""


@dataclass(frozen=True, slots=True)
class FlagState:
    pause: bool
    cancel: bool


async def read_flags(db, *, job_id: int) -> FlagState:
    row = await db.fetchrow(_FLAG_POLL_SQL, job_id)
    if row is None:
        # Row deleted — interpret as cancel (the FK CASCADE removed it).
        return FlagState(pause=False, cancel=True)
    return FlagState(pause=bool(row["pause_requested"]),
                     cancel=bool(row["cancel_requested"]))


async def should_pause(db, *, job_id: int) -> bool:
    return (await read_flags(db, job_id=job_id)).pause


async def should_cancel(db, *, job_id: int) -> bool:
    return (await read_flags(db, job_id=job_id)).cancel


class ForcePauseListener:
    """Subscribes to jobs.force_pause and runs a callback per id.

    Stage handlers register a `(job_id, abort_callback)` while running.
    The callback typically sends SIGTERM to an ffmpeg/STT subprocess
    group; the per-segment commit then sees state='paused' and exits.
    """

    def __init__(self, db):
        self.db = db
        self._registry: dict[int, asyncio.Future] = {}
        self._stop = asyncio.Event()
        self._task: asyncio.Task | None = None

    def register(self, job_id: int) -> asyncio.Future:
        """Returns a Future that resolves when force-pause arrives for job_id."""
        fut: asyncio.Future = asyncio.get_event_loop().create_future()
        self._registry[job_id] = fut
        return fut

    def unregister(self, job_id: int) -> None:
        self._registry.pop(job_id, None)

    async def _run(self) -> None:
        if self.db.dialect == "postgres":
            listener = await self.db.acquire_listener()

            def on_notify(*args):
                payload = args[-1]
                try:
                    job_id = int(__import__("json").loads(payload)["id"])
                except Exception:
                    log.exception("malformed_force_pause_payload",
                                  extra={"payload": payload})
                    return
                fut = self._registry.get(job_id)
                if fut and not fut.done():
                    fut.set_result(None)

            await listener.add_listener(JOBS_FORCE_PAUSE, on_notify)
            try:
                await self._stop.wait()
            finally:
                await listener.remove_listener(JOBS_FORCE_PAUSE, on_notify)
        else:
            queue = await get_bus().subscribe(JOBS_FORCE_PAUSE)
            while not self._stop.is_set():
                done, pending = await asyncio.wait(
                    [
                        asyncio.create_task(queue.get()),
                        asyncio.create_task(self._stop.wait()),
                    ],
                    return_when=asyncio.FIRST_COMPLETED,
                )
                for t in pending:
                    t.cancel()
                if self._stop.is_set():
                    return
                for t in done:
                    if t.result() is None:
                        continue
                    payload = t.result()
                    job_id = int(__import__("json").loads(payload)["id"])
                    fut = self._registry.get(job_id)
                    if fut and not fut.done():
                        fut.set_result(None)

    def start(self) -> None:
        assert self._task is None
        self._task = asyncio.create_task(
            self._run(), name="force-pause-listener",
        )

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
```

### 5.1 Wiring into a stage handler

```python
async def run_transcribe(ctx, job, video):
    abort_fut = ctx.force_pause_listener.register(job.id)
    try:
        async with heartbeat_for(ctx.db, job_id=job.id, interval_sec=5):
            async for segment in stt.transcribe_stream(audio):
                async with ctx.db.begin() as tx:
                    await commit_segment(tx, segment)
                    await tick_progress(tx, ProgressTick(...))

                # Per-segment cooperative checks.
                flags = await read_flags(ctx.db, job_id=job.id)
                if flags.cancel:
                    await mark_cancelled(ctx.db, job.id,
                                         at_sec=segment.end)
                    return CancelResult(at_sec=segment.end)
                if flags.pause:
                    # Architecture §7.8: a pause that arrives because
                    # the worker is shutting down must record reason
                    # 'shutdown' so observers can tell why the run
                    # stopped. The shutdown orchestrator (plan-06-08)
                    # sets ctx.shutdown_event when SIGTERM/SIGINT
                    # arrives.
                    reason = (
                        "shutdown"
                        if (ctx.shutdown_event is not None
                            and ctx.shutdown_event.is_set())
                        else "user"
                    )
                    await mark_paused(ctx.db, job.id,
                                      at_sec=segment.end, reason=reason)
                    return PauseResult(at_sec=segment.end)

                # Force-pause arrived? Abort subprocess; commit happens via
                # API path; we just bail out.
                if abort_fut.done():
                    audio.terminate()  # ffmpeg subprocess group SIGTERM
                    return PauseResult(at_sec=job.last_segment_end_sec)

        await mark_done(ctx.db, job.id)
        return DoneResult()
    finally:
        ctx.force_pause_listener.unregister(job.id)
```

The `audio.terminate()` call is provided by the audio-decoder context
manager in Epic 2 Story 2.3; it sends SIGTERM to the ffmpeg process
group with a 5 s grace before SIGKILL. The state is *already* `paused`
because the API set it; the worker's role is just to release resources
and exit the coroutine.

## 6. Test plan

### 6.1 Go handler tests (`api/internal/jobs/control_test.go`)

| Test | What it pins |
|---|---|
| `TestPause_SetsFlagAndNotifies` | Insert running job; POST /pause → `pause_requested=true`; LISTEN `jobs.flag_set` receives `{id, flag:"pause"}`. |
| `TestPause_Idempotent` | Two POSTs → same response (200), both notifies fire (one per call), single state transition (none here, both leave row in `running`). |
| `TestPause_Force_AlsoUpdatesState` | Insert running job; POST /pause?force=true → row in `paused`, `paused_at_sec = last_segment_end_sec`, `claimed_by=NULL`; one `jobs.flag_set` + one `jobs.force_pause` received. |
| `TestPause_Force_NoOpWhenAlreadyPaused` | Insert paused job; POST /pause?force=true → `pause_requested` cleared (already false), no force-pause notify, no double-pause; state stays `paused`. |
| `TestResume_DoesNotMutateState` | Insert paused job (pause_requested=true); POST /resume → row stays `paused`, `pause_requested=false`; LISTEN `jobs.flag_set` receives `{id, flag:"resume"}`. |
| `TestResume_OnRunningIsNoop` | Insert running job; POST /resume → 404 (state IN ('pending','paused') predicate excludes 'running'). Documents the contract: resume only makes sense for paused or pre-empted-pending. |
| `TestCancel_SetsFlag` | Insert running job; POST /cancel → `cancel_requested=true`, notify fires. |
| `TestCancel_OnTerminalReturns404` | Insert done job; POST /cancel → 404. |
| `TestPause_NotFound` | POST /pause on unknown id → 404. |
| `TestPauseForce_DoesNotFireForcePauseIfStateMismatch` | Insert paused job; POST /pause?force=true → exactly one `jobs.flag_set` and **zero** `jobs.force_pause` notifies. (The force UPDATE no-ops because of the state predicate.) |

### 6.2 Python worker-side tests (`pipeline/tests/pipeline/test_control.py`)

| Test | What it pins |
|---|---|
| `test_should_pause_reads_current_flag` | Insert row with `pause_requested=true`; `await should_pause(db, job_id=X)` returns True. |
| `test_should_pause_returns_false_when_row_missing` | (FK CASCADE deleted the row.) `should_pause` returns False; `should_cancel` returns True. |
| `test_force_pause_listener_resolves_future` | Start listener; register job 42; from another connection `pg_notify('jobs.force_pause', '{"id":42}')` → future resolves within 0.5 s. |
| `test_force_pause_listener_ignores_other_ids` | Register job 42; notify {"id": 99} → future for 42 stays pending. |
| `test_force_pause_listener_clean_shutdown` | Start, then `await stop()`; underlying listener removed; no leaked tasks. |
| `test_pause_observed_within_one_segment_window` | Use the full transcribe loop with synthetic 1 s segments; set pause; assert ≤ 2 segments commit before mark_paused. |
| `test_cancel_after_pause_is_consistent` | Set pause_requested=true → worker pauses → set cancel_requested=true on the paused row → re-claim → worker observes cancel on first per-segment check → state becomes `cancelled`; no orphaned `claimed_by`. |

### 6.3 End-to-end smoke

`pipeline/tests/integration/test_pause_resume_cancel_e2e.py` boots an
in-process worker against a SQLite DB, enqueues a synthetic transcribe
job whose backend yields 1 s segments forever, and exercises the full
HTTP+worker round trip via the in-process API handler. Asserts the
state-machine traversal `running → paused → running (after resume) →
cancelled (after cancel)` happens within bounded time.

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Pause requested before claim | `pending` row with `pause_requested=true` is excluded from the claim WHERE (Story 6.2's `(state='paused' AND pause_requested=false)` branch never matches a `pending` row, but the `state='pending'` branch is unconditional). **Adjustment:** the claim's `state='pending'` branch must also gate on `pause_requested=false` — added in Story 6.2's plan; this story's tests rely on it. | `test_claim_skips_pending_with_pause_requested` (Story 6.2) |
| Cancel requested mid-resume context-rebuild | The worker's `resuming` setup phase polls `should_cancel` between expensive sub-steps (model load, decoder seek). On observe, it `mark_cancelled` from `resuming` (legal terminal transition). | `test_cancel_during_resuming` (in `test_control.py`) |
| Force-pause on a paused row | API's force UPDATE no-ops (state predicate); `pause_requested` is cleared. The endpoint returns 200 with the paused row's body. No `jobs.force_pause` notify fires. | `TestPauseForce_DoesNotFireForcePauseIfStateMismatch` |
| Force-pause when worker is unresponsive | API has already flipped state to `paused`. The reaper (Story 6.6) does not see this row in the live-claim states, so it leaves it alone. The next claim (resume) reads the canonical `last_segment_end_sec` and continues from there — the in-flight segment that was being processed when force-pause arrived was not committed and is re-transcribed. | Verified by Epic 3 Story 3.7's plan; this story owns only the API contract. |
| Worker observes pause flag, then API force-pauses concurrently | Worker's per-segment check sees `pause=true`, runs `mark_paused(at_sec=segment.end)`. API's force-pause UPDATE sees `state IN (claimed, running, resuming)` predicate fail (state already `paused`) → no-op. Net result: one paused state with `paused_at_sec = segment.end`. | Race-test in `test_pause_observed_within_one_segment_window` |
| API's notify and worker's read race | The API commit is what makes the flag visible; the worker's next `read_flags` after the commit reads `pause_requested=true`. There's no window where the notify arrives but the row is unchanged. | Postgres MVCC guarantee |
| Notify lost (listener disconnected) | `should_pause` is a poll, not a notify. The worker observes the flag on the next per-segment check, which is at most one segment-duration away (~1-30 s for transcribe). Force-pause is the only path that depends on the notify — and it only matters when the worker is otherwise stuck for > segment-duration. | Documented in the docstring of `ForcePauseListener`. |
| Repeated cancel | Idempotent: `cancel_requested = true` is true after one or fifty calls. Notifies fire once per call. | `TestCancel_Idempotent` (variant of `TestCancel_SetsFlag`). |
| Resume on a `pending` row | Endpoint allows `state IN ('pending', 'paused')`. On a pending row that had been pause-requested before claim, this clears the flag and the row becomes claimable. | `TestResume_OnPendingClearsFlag` (variant of `TestResume_DoesNotMutateState`). |

## 8. Performance analysis

### 8.1 Per-segment poll cost

`SELECT pause_requested, cancel_requested FROM processing_jobs WHERE id=$1`
hits the PK index. Postgres warm: ~0.05 ms; SQLite warm: ~0.1 ms. The
transcribe stage commits ~3 segments/s at 0.3× realtime → 6 polls/s →
< 0.1% CPU. Acceptable.

### 8.2 Notify latency

`pg_notify` is sub-millisecond local. The worker's listener wakes on
the next event-loop tick → end-to-end force-pause latency ~2-5 ms from
`tx.Commit()` to `audio.terminate()`. The 5 s SIGTERM grace dominates.

## 9. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `github.com/jackc/pgx/v5` | already pinned | DB driver + transaction-scoped notify. |
| `github.com/go-chi/chi/v5` | already pinned | HTTP router. |
| `asyncpg`, `aiosqlite` | already pinned | Python listener / DB. |

## 10. Acceptance checklist

**Endpoints**
- [ ] `POST /api/jobs/{id}/pause` sets `pause_requested=true`, idempotent, fires `jobs.flag_set`.
- [ ] `POST /api/jobs/{id}/pause?force=true` additionally runs the force UPDATE (state→paused, paused_at_sec=last_segment_end_sec, pause_requested=false, claimed_by=NULL) and fires `jobs.force_pause`.
- [ ] `POST /api/jobs/{id}/resume` sets `pause_requested=false`, no state change, fires `jobs.flag_set`.
- [ ] `POST /api/jobs/{id}/cancel` sets `cancel_requested=true`, fires `jobs.flag_set`.

**Worker observation**
- [ ] `should_pause` and `should_cancel` use a single PK SELECT (no joins).
- [ ] `ForcePauseListener` resolves a per-job future on `jobs.force_pause` arrival.
- [ ] Stage handlers integrate flag observation after every per-segment commit.

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_pause_request_is_idempotent` passes.
- [ ] AC: `test_force_pause_emits_jobs_force_pause` passes.
- [ ] AC: `test_resume_does_not_mutate_state_directly` passes.
- [ ] AC: `test_cancel_after_pause_is_consistent` passes.
- [ ] AC: `test_pause_observed_within_one_segment_window` passes.
- [ ] AC: per-segment flag check is one indexed PK SELECT (`EXPLAIN` output checked into a fixture).

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.4.
- [ ] OpenAPI spec for the four endpoints lands in `api/openapi.yaml`.
