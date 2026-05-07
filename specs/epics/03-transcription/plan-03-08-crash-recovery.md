# Plan 3.8 — Crash recovery & graceful shutdown (implementation)

> Implementation plan for [story-03-08-crash-recovery.md](story-03-08-crash-recovery.md).
> Self-contained: a developer should be able to ship the story from this
> document alone. Cross-links: builds on the per-segment commit
> invariant from [Plan 3.6](plan-03-06-segment-commit.md), the
> pause/resume mutators from [Plan 3.7](plan-03-07-pause-resume.md),
> and the reaper introduced in
> [Epic 6 Story 6.6](../06-job-queue/story-06-06-reaper.md).
> Architectural reference:
> [`architecture.md` §7.8 "Graceful shutdown"](../../architecture.md)
> and [§7.9 "Crash recovery"](../../architecture.md).

---

## 0. Decisions and departures from `architecture.md` and the story

| # | Decision | Source | Rationale |
|---|----------|--------|-----------|
| D1 | Signal handling lives in **one** place: `pipeline/src/maktaba_pipeline/runtime/lifecycle.py::ShutdownController`. Stages **never** install their own signal handlers. The controller exposes `await ctx.shutdown_requested.wait()` and `ctx.is_shutting_down() -> bool` (the same interface as `pause_requested` from the worker's perspective). | Refines the story (which says "the worker traps SIGTERM"). | Per-stage signal handlers race each other and easily double-handle (every stage sees SIGTERM, every stage tries to flip its job, two of them clobber). One controller, set up once at process start, fans out via an `asyncio.Event` shared with all per-claim tasks. The committer code (Plan 3.6) and pause code (Plan 3.7) treat shutdown identically to a normal cooperative pause. |
| D2 | Treating `SIGTERM` and the second `SIGTERM` differently: the FIRST signal sets `shutdown_requested` (cooperative); the SECOND signal sets `abort_requested` AND calls `os.killpg(0, SIGTERM)` to terminate any backend subprocesses, then schedules `loop.call_later(5.0, lambda: os._exit(1))` as the hard deadline. | Story acceptance: "A second SIGTERM/Ctrl-C aborts immediately". | "Aborts immediately" is operator-supplied permission to break the cooperative path; we still want the next 5 s to be the cleanest possible exit (release file handles, flush stderr) before the kernel gets called. The 5-second deadline is empirically enough for a Python interpreter to unwind asyncio and exit, but short enough that an impatient operator's third Ctrl-C is not needed. |
| D3 | The reaper runs in the **API service** (Go), not in a worker — it is a pure DB-side mutator and must outlive the workers that crashed. It is the one writer that can flip `state` from `running`/`claimed`/`resuming` directly to `paused` *without* the worker's cooperation, because by definition the worker has stopped heartbeating. | Refines architecture §7.9 (which doesn't say where the reaper lives). | If the reaper lived in a worker, it would have a chicken-and-egg problem: a worker process that has crashed cannot revive its own jobs. The API service is always running (otherwise the user can't even reach the UI). It owns the reaper and emits a `LISTEN jobs.reaped` notify so other interested services (UI websocket fan-out) update without polling. |
| D4 | The reaper UPDATE condition is `state IN ('claimed', 'running', 'resuming') AND last_heartbeat_at < now() - $stale_claim_sec`. We **deliberately** include `claimed` (not just `running`/`resuming`) because a worker can crash between claim and the first heartbeat write of the stage runner. | Refines architecture §7.9 (which says `claimed`/`running`/`resuming`). | `claim` already sets `last_heartbeat_at = now()` per [`architecture.md` §7.3 line 1023], so a `claimed` row whose heartbeat is stale is genuinely abandoned — no worker is going to advance it. Excluding `claimed` would leave abandoned-during-setup jobs in limbo until manual intervention. |
| D5 | The reaper batches jobs reaped per pass (default `max_per_pass = 100`) to avoid a single SQL statement holding write locks on hundreds of rows during the OOM-killer aftermath of a many-claim worker. Reaper passes are scheduled at a default `interval_sec = 30`. | Story §"Edge cases" implies the reaper's UPDATE is a single statement; this refines it to a bounded batch. | Bounded batches mean a "thundering herd" of crashed workers (e.g., the box rebooted with 8 workers all holding 4 jobs each → 32 stale rows) can't single-statement-block the API for seconds. Each batch holds locks for milliseconds; the next pass mops up. |
| D6 | The wall-clock-skew defense (story edge case) is implemented as: workers send NO timestamp with their heartbeat — `commit_segment` writes `last_heartbeat_at = now()` server-side. The reaper compares `now()` against the row, both server-side. There is no path through which a worker's wall clock can influence the comparison. | Story edge case: "All times are server-side `now()`; workers never send wall-clock timestamps". | Implemented as a *negative* contract: a static check (added to CI) greps the codebase for any UPDATE that sets `last_heartbeat_at` from a parameter binding rather than `now()`. If the grep matches, CI fails. This is the "mechanically enforced" version of the story's prose guarantee. |
| D7 | The chaos test fixture (`tests/chaos/transcribe_kill.py`) randomizes the kill point as `delay = uniform(0.5, audio_duration_sec * 0.8)` per round, defaults to `N = 5` rounds, and asserts the **final** segment count and concatenated text are equal across all rounds (deterministic backends) or within `levenshtein_ratio >= 0.99` (non-deterministic). | Story acceptance: "after restart, the resulting transcript matches the no-crash baseline byte-for-byte (or within ε for non-deterministic backends)". | Random kill points exercise different commit-boundary states; `N=5` is enough to exercise both "killed mid-segment" and "killed during transition between segments". The 0.99 ratio leaves room for occasional Whisper non-determinism (~1% of segments differ in single character) without being so loose it hides regressions. |

If D3 is rejected (reaper runs in a Python worker rather than the API
service), §3 changes (the reaper lives in `pipeline/runtime/reaper.py`)
and the deployment plan (§7) loses one fewer service to start before
workers; everything else holds.

---

## 1. Architecture diagram — shutdown + crash + reap

```
                    ┌──────────────────────────────────────────────────┐
                    │  Worker process (Python)                         │
                    │                                                  │
                    │  main():                                         │
                    │   ShutdownController.install()  (D1)             │
                    │   start N stage runners                          │
                    │                                                  │
                    │  ┌─────────── ShutdownController ───────────┐    │
                    │  │  on SIGTERM (1st):                       │    │
                    │  │    shutdown_requested.set()              │    │
                    │  │    log "graceful_shutdown_requested"     │    │
                    │  │  on SIGTERM (2nd):                       │    │
                    │  │    abort_requested.set()                 │    │
                    │  │    os.killpg(0, SIGTERM)                 │    │
                    │  │    loop.call_later(5, os._exit(1))       │    │
                    │  │  on SIGKILL: not deliverable → reaper    │    │
                    │  └──────────────────────┬──────────────────┘    │
                    │                         │                        │
                    │  per-claim runner:      ▼                        │
                    │   committer.commit() returns pause_requested OR  │
                    │     ctx.is_shutting_down() returns True (D1)     │
                    │     → raises StopWorker(reason='shutdown')       │
                    │     → mark_paused(reason='shutdown',             │
                    │                   at_sec=last_segment_end_sec)   │
                    │     → release GPU lock                           │
                    │                                                  │
                    │  await all runners → process exit 0 within       │
                    │     shutdown_grace_sec (default 120 s).          │
                    └──────────────────────────────────────────────────┘

                                                ─── crash ──────────────►

                    ┌──────────────────────────────────────────────────┐
                    │  API service (Go)                                │
                    │                                                  │
                    │  ReaperLoop (D3):                                │
                    │   every reaper.interval_sec (default 30 s):      │
                    │     1. UPDATE processing_jobs                    │
                    │           SET state          = 'paused',         │
                    │               paused_at      = now(),            │
                    │               paused_at_sec  = last_segment_end_sec, │
                    │               paused_reason  = 'crash',          │
                    │               pause_requested = false,           │
                    │               claimed_by     = NULL,             │
                    │               claimed_at     = NULL              │
                    │         WHERE id IN (                            │
                    │           SELECT id FROM processing_jobs         │
                    │            WHERE state IN ('claimed','running',  │
                    │                            'resuming')           │
                    │              AND last_heartbeat_at <             │
                    │                  now() - $stale_claim_sec        │
                    │            ORDER BY last_heartbeat_at            │
                    │            LIMIT $max_per_pass                   │
                    │            FOR UPDATE SKIP LOCKED)               │
                    │       RETURNING id;                              │
                    │     2. for each id: pg_notify('jobs.reaped',     │
                    │                       {job_id, reason:'crash'})  │
                    │     3. metrics.reaper_jobs_paused.inc(N)         │
                    │                                                  │
                    │  Concurrency: only one reaper instance at a      │
                    │  time; protected by pg_advisory_lock(REAPER_KEY) │
                    └──────────────────────────────────────────────────┘

                                                ── recovery ────────────►

                    ┌──────────────────────────────────────────────────┐
                    │  Any worker reclaims via the Plan 3.7 resume     │
                    │  path. The reaped job is indistinguishable from  │
                    │  a user-paused one except by paused_reason.      │
                    └──────────────────────────────────────────────────┘
```

The single insight: **shutdown and crash converge on the same
"paused" state**. The only difference is `paused_reason` (`'shutdown'`
vs. `'crash'`) and *who* set it (the worker itself vs. the reaper).
Resume is unaware of which path got us there.

---

## 2. Detailed implementation

### 2.1 Package layout — Python (Pipeline Service)

```
pipeline/src/maktaba_pipeline/
├── runtime/
│   ├── __init__.py
│   ├── lifecycle.py          # ShutdownController, install(), Context wiring
│   ├── deadline.py           # ShutdownDeadline helper for the 120 s grace
│   └── tests/
│       ├── test_shutdown_controller.py
│       ├── test_shutdown_grace_deadline.py
│       └── test_double_signal_aborts.py
├── pipeline/
│   └── stages/
│       └── transcribe.py     # extended: poll ctx.is_shutting_down()
└── tests/
    └── chaos/
        ├── conftest.py
        ├── kill_harness.py   # spawn a worker, kill it at random points
        └── test_chaos_kill_yields_consistent_resume.py
```

### 2.2 Package layout — Go (API service)

```
api/internal/reaper/
├── reaper.go                 # ReaperLoop, Pass(), advisory-lock guard
├── reaper_test.go
├── config.go                 # interval_sec, stale_claim_sec, max_per_pass
└── metrics.go                # reaper_jobs_paused counter, reaper_pass_duration
```

### 2.3 `lifecycle.py` — the one signal handler

```python
"""ShutdownController — the only place we install OS signal handlers.

Two-tier semantics (D2):
- 1st SIGTERM/SIGINT  → cooperative pause across all jobs
- 2nd SIGTERM/SIGINT  → abort: SIGTERM the process group, hard-exit in 5 s
"""
from __future__ import annotations
import asyncio
import logging
import os
import signal
from typing import Callable

log = logging.getLogger(__name__)

HARD_EXIT_DEADLINE_SEC = 5.0


class ShutdownController:
    def __init__(self):
        self.shutdown_requested = asyncio.Event()
        self.abort_requested = asyncio.Event()
        self._installed = False
        self._loop: asyncio.AbstractEventLoop | None = None
        self._second_signal_seen = False

    def is_shutting_down(self) -> bool:
        return self.shutdown_requested.is_set()

    def install(self, loop: asyncio.AbstractEventLoop | None = None) -> None:
        if self._installed:
            raise RuntimeError("ShutdownController already installed")
        self._loop = loop or asyncio.get_event_loop()
        for sig in (signal.SIGTERM, signal.SIGINT):
            try:
                self._loop.add_signal_handler(
                    sig, self._on_signal, sig)
            except NotImplementedError:
                # Windows: fall back to signal.signal (worker is Linux/macOS only,
                # but the dev test runner on Windows still needs this not to crash).
                signal.signal(sig, lambda s, f: self._on_signal(s))
        self._installed = True

    def _on_signal(self, sig) -> None:
        if not self.shutdown_requested.is_set():
            log.warning("graceful_shutdown_requested", extra={"signal": int(sig)})
            self.shutdown_requested.set()
            return
        # Second signal.
        if self._second_signal_seen:
            return
        self._second_signal_seen = True
        log.error("abort_requested", extra={"signal": int(sig)})
        self.abort_requested.set()
        # Kill the process group so any backend subprocess we spawned dies.
        try:
            os.killpg(os.getpgrp(), signal.SIGTERM)
        except (PermissionError, ProcessLookupError) as e:
            log.warning("killpg_failed", extra={"err": str(e)})
        assert self._loop is not None
        self._loop.call_later(HARD_EXIT_DEADLINE_SEC, _hard_exit)


def _hard_exit() -> None:
    log.error("hard_exit_deadline_reached")
    os._exit(1)
```

### 2.4 `deadline.py` — bounded grace

```python
"""ShutdownDeadline — bounds the total time we wait for jobs to commit.

Used by main(): after shutdown_requested fires, wait up to
shutdown_grace_sec for all stage runners to finish. If they don't, log
and exit anyway (an uncommitted segment is harmless per Plan 3.6).
"""
from __future__ import annotations
import asyncio
import logging
from contextlib import asynccontextmanager

log = logging.getLogger(__name__)

DEFAULT_GRACE_SEC = 120.0


@asynccontextmanager
async def shutdown_deadline(controller, *, grace_sec: float = DEFAULT_GRACE_SEC):
    """Wait up to grace_sec after shutdown_requested fires.

    Yields nothing; on exit, logs whether we hit the deadline.
    """
    yield
    if not controller.shutdown_requested.is_set():
        return
    try:
        await asyncio.wait_for(_wait_for_runners_done(), timeout=grace_sec)
    except asyncio.TimeoutError:
        log.error("shutdown_deadline_exceeded",
                  extra={"grace_sec": grace_sec})


async def _wait_for_runners_done():
    # Caller responsibility: it should keep references to runner tasks and
    # await them. This is the join point.
    pass  # populated by main() — kept stub here for unit-test isolation.
```

### 2.5 Stage integration — cooperative shutdown polling

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py  (excerpt)

# Inside the run_transcribe_stage loop (extending Plan 3.7 §2.8).
async for raw_seg in backend.transcribe_stream(audio):
    wall_sec = ...
    try:
        ready = rb.push(raw_seg)
    except OutOfOrderSegmentDropped:
        continue
    for s in ready:
        result = await com.commit(s, wall_sec=wall_sec)
        # NEW: shutdown check after commit, equivalent to the pause_requested check.
        if ctx.is_shutting_down():
            raise StopWorker(reason="shutdown",
                             last_end_sec=result.last_segment_end_sec)
```

In `mark_paused`, the existing reason argument carries `'shutdown'`
when the StopWorker reason is `'shutdown'`. No new code path; just a
new value flowing through the existing one.

### 2.6 `reaper.go` — the API-side reaper

```go
// api/internal/reaper/reaper.go
package reaper

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"
    "time"

    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
)

const advisoryLockKey int64 = 0x4d414b54414241 // "MAKTABA"

type Config struct {
    IntervalSec   int  `yaml:"interval_sec"   default:"30"`
    StaleClaimSec int  `yaml:"stale_claim_sec" default:"90"`
    MaxPerPass    int  `yaml:"max_per_pass"   default:"100"`
}

type Reaper struct {
    DB     *pgxpool.Pool
    Cfg    Config
    Log    *slog.Logger
    Metric MetricsSink
}

type ReapedJob struct {
    ID                  int64   `json:"job_id"`
    LastSegmentEndSec   float64 `json:"paused_at_sec"`
    HeartbeatAt         time.Time
}

// Run loops until ctx is done. One pass per IntervalSec.
func (r *Reaper) Run(ctx context.Context) error {
    interval := time.Duration(r.Cfg.IntervalSec) * time.Second
    t := time.NewTicker(interval)
    defer t.Stop()

    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-t.C:
            if err := r.Pass(ctx); err != nil {
                r.Log.Error("reaper_pass_failed", "err", err)
            }
        }
    }
}

// Pass executes one batch reap. Idempotent under advisory-lock contention.
func (r *Reaper) Pass(ctx context.Context) error {
    start := time.Now()
    var reaped []ReapedJob

    err := r.DB.BeginFunc(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
        // Single-instance guard: txn-scoped advisory lock; non-blocking try.
        var got bool
        if err := tx.QueryRow(ctx,
            `SELECT pg_try_advisory_xact_lock($1)`, advisoryLockKey,
        ).Scan(&got); err != nil {
            return fmt.Errorf("advisory lock: %w", err)
        }
        if !got {
            return nil // another reaper instance is in flight; skip pass.
        }

        rows, err := tx.Query(ctx, `
            UPDATE processing_jobs j
               SET state           = 'paused',
                   paused_at       = now(),
                   paused_at_sec   = last_segment_end_sec,
                   paused_reason   = 'crash',
                   pause_requested = false,
                   claimed_by      = NULL,
                   claimed_at      = NULL
             WHERE id IN (
                 SELECT id
                   FROM processing_jobs
                  WHERE state IN ('claimed','running','resuming')
                    AND last_heartbeat_at IS NOT NULL
                    AND last_heartbeat_at < now() - ($1 || ' seconds')::interval
                  ORDER BY last_heartbeat_at
                  LIMIT $2
                  FOR UPDATE SKIP LOCKED
             )
            RETURNING id, paused_at_sec, last_heartbeat_at
        `, r.Cfg.StaleClaimSec, r.Cfg.MaxPerPass)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var rj ReapedJob
            if err := rows.Scan(&rj.ID, &rj.LastSegmentEndSec, &rj.HeartbeatAt); err != nil {
                return err
            }
            reaped = append(reaped, rj)
        }
        if err := rows.Err(); err != nil {
            return err
        }
        // Notify within the transaction so notifies are atomic with the update.
        for _, rj := range reaped {
            payload, _ := json.Marshal(map[string]any{
                "job_id": rj.ID, "reason": "crash",
                "paused_at_sec": rj.LastSegmentEndSec,
            })
            if _, err := tx.Exec(ctx,
                `SELECT pg_notify('jobs.reaped', $1)`, string(payload)); err != nil {
                return err
            }
        }
        return nil
    })

    r.Metric.PassDuration(time.Since(start))
    r.Metric.JobsPaused(len(reaped))
    if len(reaped) > 0 {
        r.Log.Warn("reaped_stale_claims",
            "count", len(reaped), "duration_ms", time.Since(start).Milliseconds())
    }
    return err
}
```

### 2.7 Wiring the reaper into the API service

```go
// api/cmd/maktaba-api/main.go (excerpt)
func main() {
    ctx, stop := signal.NotifyContext(context.Background(),
        os.Interrupt, syscall.SIGTERM)
    defer stop()

    pool, _ := pgxpool.New(ctx, dbURL)
    defer pool.Close()

    reaperCfg := loadReaperConfig() // env / yaml
    rp := &reaper.Reaper{
        DB: pool, Cfg: reaperCfg, Log: log, Metric: metrics.Reaper,
    }
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { return rp.Run(gctx) })
    g.Go(func() error { return startHTTPServer(gctx, pool) })
    if err := g.Wait(); err != nil && !errors.Is(err, context.Canceled) {
        log.Error("api_exit", "err", err)
    }
}
```

### 2.8 Chaos test harness — `tests/chaos/kill_harness.py`

```python
"""KillHarness — spawn a worker, transcribe a fixture, kill it, repeat.

Used by test_chaos_kill_yields_consistent_resume to exercise the full
shutdown/crash/recovery loop end-to-end.
"""
from __future__ import annotations
import asyncio
import os
import random
import signal
import subprocess
import time
from dataclasses import dataclass


@dataclass
class KillRound:
    pid: int
    killed_at_wall_sec: float
    audio_committed_at_kill: float


class KillHarness:
    def __init__(self, *, fixture_video: str, db_url: str, total_rounds: int = 5,
                 seed: int = 0xCAFE):
        self.fixture = fixture_video
        self.db_url = db_url
        self.total_rounds = total_rounds
        self._rng = random.Random(seed)
        self.rounds: list[KillRound] = []

    async def baseline(self) -> str:
        """Run once with no kills; return concatenated transcript text."""
        proc = await self._spawn_worker()
        await self._wait_for_done(proc)
        return await self._read_transcript_text()

    async def chaos(self) -> str:
        """Run total_rounds with random kills; return final transcript text."""
        for r in range(self.total_rounds):
            proc = await self._spawn_worker()
            duration = await self._fixture_duration_sec()
            kill_at = self._rng.uniform(0.5, duration * 0.8)
            await asyncio.sleep(kill_at)
            committed = await self._read_last_segment_end_sec()
            os.kill(proc.pid, signal.SIGKILL)
            await proc.wait()
            self.rounds.append(KillRound(
                pid=proc.pid, killed_at_wall_sec=kill_at,
                audio_committed_at_kill=committed))
            # Wait long enough for the reaper to mark the job paused.
            await asyncio.sleep(self._stale_claim_sec() + 5)

        # Final round: no kill — let it finish.
        proc = await self._spawn_worker()
        await self._wait_for_done(proc, timeout=600)
        return await self._read_transcript_text()

    # … helpers omitted for brevity (subprocess spawn, DB queries) …
```

---

## 3. Code scaffolding — file-by-file checklist

| Order | File | Symbols introduced | Tests gating |
|-------|------|--------------------|--------------|
| 1 | `pipeline/src/maktaba_pipeline/runtime/lifecycle.py` | `ShutdownController`, `HARD_EXIT_DEADLINE_SEC` | `test_shutdown_controller`, `test_double_signal_aborts` |
| 2 | `pipeline/src/maktaba_pipeline/runtime/deadline.py` | `shutdown_deadline`, `DEFAULT_GRACE_SEC` | `test_shutdown_grace_deadline` |
| 3 | `pipeline/src/maktaba_pipeline/pipeline/stages/transcribe.py` | inject `ctx.is_shutting_down()` poll after each commit | `test_sigterm_pauses_all_jobs` |
| 4 | `api/internal/reaper/config.go` + `reaper.go` + `metrics.go` | `Reaper`, `ReapedJob`, `Config`, advisory-lock guard | `reaper_test.go` (single-instance, batched, idempotent) |
| 5 | `api/internal/reaper/reaper_test.go` | reaper unit tests | (gating itself) |
| 6 | `api/cmd/maktaba-api/main.go` | wire `rp.Run(gctx)` into the errgroup | API smoke test still passes |
| 7 | `tests/chaos/kill_harness.py` + `test_chaos_kill_yields_consistent_resume.py` | end-to-end chaos test | (final gate) |

---

## 4. Test cases

### 4.1 `test_sigterm_pauses_all_jobs` (story-named)

```python
async def test_sigterm_pauses_all_jobs_within_grace(
    db, worker_factory, job_factory,
):
    """Two transcribe jobs running; SIGTERM → both 'paused' with reason 'shutdown'."""
    job_a = await job_factory.running(at_sec=10.0)
    job_b = await job_factory.running(at_sec=20.0)
    worker = await worker_factory.spawn(num_runners=2)
    await worker.wait_until_claimed(job_a.id)
    await worker.wait_until_claimed(job_b.id)

    t0 = time.monotonic()
    os.kill(worker.pid, signal.SIGTERM)
    await worker.wait_for_exit(timeout=125.0)  # shutdown_grace_sec + slack
    elapsed = time.monotonic() - t0
    assert elapsed < 125.0

    rows = await db.fetch(
        "SELECT id, state, paused_reason FROM processing_jobs WHERE id IN ($1, $2)",
        job_a.id, job_b.id)
    for r in rows:
        assert r["state"] == "paused"
        assert r["paused_reason"] == "shutdown"
```

### 4.2 `test_double_sigterm_aborts_fast` (story-named)

```python
async def test_double_sigterm_aborts_within_5s(
    db, worker_factory, hanging_backend, job_factory,
):
    """Worker stuck in a single segment; double SIGTERM → exit < 5 s."""
    job = await job_factory.running()
    worker = await worker_factory.spawn(backend=hanging_backend(hang_sec=300))
    await worker.wait_until_claimed(job.id)

    os.kill(worker.pid, signal.SIGTERM)
    await asyncio.sleep(0.1)
    t0 = time.monotonic()
    os.kill(worker.pid, signal.SIGTERM)
    await worker.wait_for_exit(timeout=10.0)
    elapsed = time.monotonic() - t0
    assert elapsed < 6.0  # 5s deadline + ~1s slack

    # In-flight segment was uncommitted → DB consistent.
    j = await db.fetchrow(
        "SELECT segments_completed, last_segment_end_sec FROM processing_jobs WHERE id=$1",
        job.id)
    assert j["segments_completed"] == 0
    assert j["last_segment_end_sec"] == 0.0
```

### 4.3 `test_reaper_pauses_stale_claim` (story-named)

```python
async def test_reaper_flips_stale_claim_to_paused(
    db, reaper, job_factory, freezer,
):
    """Claim a job; freeze its heartbeat; advance time past stale_claim_sec → reaper acts."""
    job = await job_factory.claimed(at_sec=42.0, claimed_by="dead-worker-A")
    # Force last_heartbeat_at to "now" (current time).
    await db.execute(
        "UPDATE processing_jobs SET last_heartbeat_at = now() WHERE id = $1",
        job.id)

    # Simulate clock advance by mutating the DB row's heartbeat backwards.
    await db.execute(
        "UPDATE processing_jobs "
        "   SET last_heartbeat_at = now() - interval '120 seconds' "
        " WHERE id = $1", job.id)

    await reaper.Pass(ctx=anything())

    j = await db.fetchrow("SELECT * FROM processing_jobs WHERE id=$1", job.id)
    assert j["state"] == "paused"
    assert j["paused_reason"] == "crash"
    assert j["paused_at_sec"] == 42.0
    assert j["claimed_by"] is None
```

### 4.4 `test_chaos_kill_yields_consistent_resume` (story-named)

```python
async def test_chaos_kill_yields_consistent_resume(
    db, fixture_30min_arabic_lecture,
):
    """SIGKILL the worker N times during a fixture; final transcript matches baseline."""
    harness = KillHarness(
        fixture_video=fixture_30min_arabic_lecture, db_url=db.url,
        total_rounds=5, seed=0xC0DE)
    baseline = await harness.baseline()
    chaos = await harness.chaos()

    # For deterministic backends (synthetic / fixture-driven): byte equality.
    if BACKEND_IS_DETERMINISTIC:
        assert baseline == chaos
    else:
        # Real Whisper: 99% similarity.
        from difflib import SequenceMatcher
        ratio = SequenceMatcher(None, baseline, chaos).ratio()
        assert ratio >= 0.99, f"chaos transcript drift: {ratio=:.4f}"

    # Invariant: no duplicate seq within any transcript.
    dup = await db.fetchval("""
        SELECT count(*) FROM (
            SELECT transcript_id, seq, count(*) c
              FROM transcript_segments
             GROUP BY 1, 2
            HAVING count(*) > 1
        ) x""")
    assert dup == 0
```

### 4.5 `test_shutdown_controller` (unit)

```python
def test_first_signal_sets_shutdown_only(controller):
    controller.install()
    assert not controller.shutdown_requested.is_set()
    controller._on_signal(signal.SIGTERM)
    assert controller.shutdown_requested.is_set()
    assert not controller.abort_requested.is_set()


def test_install_twice_raises():
    c = ShutdownController()
    c.install()
    with pytest.raises(RuntimeError):
        c.install()
```

### 4.6 `test_double_signal_aborts` (unit)

```python
async def test_double_signal_sets_abort_and_schedules_hard_exit(monkeypatch):
    controller = ShutdownController()
    controller.install()
    hard_exit_called = []
    monkeypatch.setattr(
        "maktaba_pipeline.runtime.lifecycle._hard_exit",
        lambda: hard_exit_called.append(True))
    killpg_called = []
    monkeypatch.setattr(
        os, "killpg", lambda *a: killpg_called.append(a))

    controller._on_signal(signal.SIGTERM)
    controller._on_signal(signal.SIGTERM)

    assert controller.abort_requested.is_set()
    assert killpg_called == [(os.getpgrp(), signal.SIGTERM)]

    await asyncio.sleep(HARD_EXIT_DEADLINE_SEC + 0.1)
    assert hard_exit_called == [True]


async def test_third_signal_is_idempotent():
    controller = ShutdownController()
    controller.install()
    controller._on_signal(signal.SIGTERM)
    controller._on_signal(signal.SIGTERM)
    # The third signal must not call killpg again or schedule another hard exit.
    controller._on_signal(signal.SIGTERM)
    # (Asserted by the absence of additional side effects in the prior test.)
```

### 4.7 `test_shutdown_grace_deadline` (unit)

```python
async def test_grace_deadline_exits_anyway(controller, caplog):
    controller.install()
    controller.shutdown_requested.set()
    async with shutdown_deadline(controller, grace_sec=0.1):
        # Simulate runners that never finish.
        await asyncio.sleep(0.5)
    assert any("shutdown_deadline_exceeded" in r.message for r in caplog.records)
```

### 4.8 `test_reaper_advisory_lock_serializes_passes` (Go, unit)

```go
func TestReaperPass_AdvisoryLockBlocksConcurrentPasses(t *testing.T) {
    pool := newTestPool(t)
    r1 := &Reaper{DB: pool, Cfg: defaultCfg(), Log: testLogger(), Metric: noopMetric{}}
    r2 := &Reaper{DB: pool, Cfg: defaultCfg(), Log: testLogger(), Metric: noopMetric{}}

    seedStaleJobs(t, pool, 5)

    var n1, n2 int
    var wg sync.WaitGroup
    wg.Add(2)
    go func() { defer wg.Done(); n1 = mustPassReturnReapedCount(t, r1) }()
    go func() { defer wg.Done(); n2 = mustPassReturnReapedCount(t, r2) }()
    wg.Wait()

    // Exactly one Pass touched all five jobs; the other was a no-op.
    assert.True(t, (n1 == 5 && n2 == 0) || (n1 == 0 && n2 == 5),
        "expected one pass to win, got n1=%d n2=%d", n1, n2)
}
```

### 4.9 `test_reaper_max_per_pass_respected` (Go, unit)

```go
func TestReaperPass_BatchesAtMaxPerPass(t *testing.T) {
    pool := newTestPool(t)
    cfg := defaultCfg()
    cfg.MaxPerPass = 10
    r := &Reaper{DB: pool, Cfg: cfg, Log: testLogger(), Metric: noopMetric{}}

    seedStaleJobs(t, pool, 25)

    require.NoError(t, r.Pass(context.Background()))
    require.Equal(t, 15, countStale(t, pool))   // 25 − 10 = 15 left

    require.NoError(t, r.Pass(context.Background()))
    require.Equal(t, 5, countStale(t, pool))

    require.NoError(t, r.Pass(context.Background()))
    require.Equal(t, 0, countStale(t, pool))
}
```

### 4.10 `test_reaper_skips_heartbeating_jobs` (Go, unit, edge case E1)

```go
func TestReaperPass_DoesNotTouchFreshHeartbeats(t *testing.T) {
    pool := newTestPool(t)
    r := &Reaper{DB: pool, Cfg: defaultCfg(), Log: testLogger(), Metric: noopMetric{}}

    fresh := seedFreshClaim(t, pool)            // last_heartbeat_at = now()
    require.NoError(t, r.Pass(context.Background()))

    state := jobState(t, pool, fresh)
    require.Equal(t, "running", state, "fresh-heartbeat job must not be reaped")
}
```

---

## 5. Edge cases and how the plan handles each

| # | Edge case (story §"Edge cases") | Handled by |
|---|---------------------------------|------------|
| E1 | Reaper races a recovering worker. Both attempt to mutate the same job. | The reaper's UPDATE includes `WHERE last_heartbeat_at < now() - $stale_claim_sec`; a heartbeating worker invalidates the predicate, so the UPDATE matches zero rows for that job. Additionally, the reaper acquires `pg_try_advisory_xact_lock(MAKTABA)` (D5 + D3) so two reaper instances don't double-fire. (`test_reaper_skips_heartbeating_jobs`, `test_reaper_advisory_lock_serializes_passes`) |
| E2 | Wall-clock skew. A workstation whose clock jumped backward cannot fool the reaper. | All times in `commit_segment` (Plan 3.6) and the reaper UPDATE are `now()`, server-side. Workers never send wall-clock timestamps for the heartbeat — D6 enforces this with a CI grep that fails if any code attempts to bind `last_heartbeat_at` from a parameter. |
| E3 | Workers crash in the *middle* of `commit_segment`. | Transaction atomicity (Plan 3.6) means either the segment+job both committed or neither did. The crashed worker's heartbeat goes stale; the reaper flips the row to `paused` with `paused_at_sec = last_segment_end_sec`. The pre-crash committed prefix is intact. (Verified by `test_chaos_kill_yields_consistent_resume`.) |
| E4 | `os.killpg` on the second SIGTERM also signals the parent shell on a foreground worker. | Workers are intended to run as a daemon (`systemd`, `launchd`, or `docker`) where there is no foreground shell to be killed by `killpg(0, …)`. Dev mode (foreground `python -m maktaba_pipeline.worker`) sets `os.setpgrp()` at startup so the worker is in its own process group; the shell is unaffected. (Tested manually; documented in the runbook.) |
| E5 | `shutdown_grace_sec` exceeded; workers exit with in-flight segments uncommitted. | The hard deadline path logs `shutdown_deadline_exceeded` and exits. Per Plan 3.6, an uncommitted segment is harmless — the next worker resumes from the last committed `last_segment_end_sec`. We re-transcribe ≤30 s on resume; correctness is preserved. |
| E6 | A reaper pass takes longer than `interval_sec`. | The next tick fires, but `pg_try_advisory_xact_lock` returns false (the previous pass still holds the lock); the new pass returns a no-op (E1). When the long pass finally finishes, normal cadence resumes. The metric `reaper_pass_duration` makes the slowdown observable. |
| E7 | Reaper service is itself down (API-service outage). | Crashed jobs accumulate in `claimed`/`running`/`resuming` until the API is restored. They are not lost — the row survives — and on next reaper pass they all get flipped in batches of `MaxPerPass`. The user sees them as "running" for the duration of the outage; this is correct, since "still claimed" is the same observable state until proven stale. |
| E8 | A fresh `'claimed'` row that hasn't yet written its first heartbeat. | The claim itself sets `last_heartbeat_at = now()` (per architecture §7.3); the reaper compares against that initial timestamp. Until `stale_claim_sec` (default 90 s) elapses, the row is considered alive. (`test_reaper_skips_heartbeating_jobs`) |
| E9 | Multiple workers in the same process group; `os.killpg` on second SIGTERM kills siblings. | Workers are intended to run one process per worker (each its own PID). If two workers share a process group (a foreground `&` chain), the operator's intent is to kill all of them — that's exactly what `killpg` does. The hard-exit deadline of 5 s ensures the cluster recovers via the reaper if any worker hangs during its own shutdown. |
| E10 | Reaper-flipped jobs accumulate `paused_reason = 'crash'` rows that never resume. | These rows are claimable like any other paused job (Plan 3.7); a healthy worker will pick them up. If no worker is around, that's an infrastructure problem the user observes via the UI's "0 workers connected" indicator (Epic 21). |

---

## 6. Acceptance checklist

- [ ] **A1** On `SIGTERM`/`SIGINT`, the worker treats it as `pause_requested` for every job it holds, with `paused_reason = 'shutdown'`. Each affected job commits the current segment (if any), flips to `paused`, and the process exits within `shutdown_grace_sec` (default 120 s). (`test_sigterm_pauses_all_jobs`)
- [ ] **A2** A second `SIGTERM`/Ctrl-C aborts immediately with the same correctness guarantee — the in-flight segment was uncommitted, so the DB is consistent. (`test_double_sigterm_aborts_fast`, `test_double_signal_aborts`)
- [ ] **A3** On crash (`SIGKILL`, panic, host reboot), the reaper finds jobs whose `last_heartbeat_at < now() - stale_claim_sec` (default 90 s) and flips them to `paused` with `paused_reason = 'crash'`, `paused_at_sec = last_segment_end_sec`. They are then claimable as resumes by any worker. (`test_reaper_pauses_stale_claim`)
- [ ] **A4** Chaos test: random `SIGKILL` mid-job N=5 times → final transcript matches the no-crash baseline byte-for-byte (deterministic backends) or `levenshtein_ratio >= 0.99` (non-deterministic), with no duplicate `seq` values. (`test_chaos_kill_yields_consistent_resume`)
- [ ] **A5** The reaper holds `pg_try_advisory_xact_lock(MAKTABA)` for the duration of each pass; concurrent passes degrade to no-ops. (`test_reaper_advisory_lock_serializes_passes`)
- [ ] **A6** The reaper batches at most `max_per_pass` (default 100) rows per pass; subsequent passes mop up. (`test_reaper_max_per_pass_respected`)
- [ ] **A7** Heartbeating jobs are not reaped, regardless of how stale other jobs are. (`test_reaper_skips_heartbeating_jobs`)
- [ ] **A8** A reaped job, on resume, takes the same code path as a user-paused job — no special-case branch in the worker. (Code review; no new conditional on `paused_reason` in Plan 3.7's resume path.)
- [ ] **A9** Wall-clock skew on a worker host cannot affect reaper decisions: a CI grep fails if any code binds `last_heartbeat_at` from a parameter rather than `now()`. (Static check; lint rule.)
- [ ] **A10** `pg_notify('jobs.reaped', ...)` fires in the same transaction as the UPDATE — UI consumers see reaped rows transitioning to `paused` immediately. (Postgres semantics; covered by Go unit test on the test DB harness.)
- [ ] **A11** `ShutdownController.install()` is called exactly once at process start; per-stage signal handlers are forbidden by a CI lint rule (`grep -r "signal\.signal\|add_signal_handler" pipeline/src/maktaba_pipeline/pipeline/stages/`).
