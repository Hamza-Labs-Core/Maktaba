# Implementation Plan — Story 6.2 Claim Loop

> Companion to [story-06-02-claim-loop.md](story-06-02-claim-loop.md).
> The story states *what* and *why*; this plan states *how*.
> The atomic SQL is owned by [architecture.md §7.3](../../architecture.md);
> the channel name `jobs.new` is fixed in
> [Story 6.1](plan-06-01-schema-indexes.md) and the README.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline Service). The claim loop runs inside the worker process and dispatches into the stage handlers. Go-side code only consumes claims via gRPC for Story 2.5 (live progress); the API never claims directly. |
| File | `pipeline/src/maktaba_pipeline/pipeline/runner.py` (the loop), `pipeline/src/maktaba_pipeline/db/jobs_claim.py` (the SQL primitive). The split keeps the SQL testable without spinning a runner. |
| Schema dependency | `processing_jobs` (Story 6.1). The claim index `(state, priority, not_before)` must be present. |
| Out of scope | Heartbeat (6.3), pause/resume (6.4), retries (6.5), reaper (6.6), per-stage concurrency caps (6.7), shutdown (6.8). The claim loop only delivers a `Job` to a worker callback; everything downstream is owned by other stories. |

## 1. Architecture diagram

```
                    ┌──────────────────────────────────────┐
                    │  Worker process (one per host or     │
                    │  one per --stages bundle)            │
                    └─────────────────┬────────────────────┘
                                      │
                                      ▼
        ┌───────────────────────────────────────────────────────────┐
        │  ClaimLoop.run()                                          │
        │                                                           │
        │   ┌────────────────────────┐    ┌──────────────────────┐  │
        │   │ Wakeup source          │    │ Per-stage semaphore  │  │
        │   │  - LISTEN jobs.new     │    │ (Story 6.7)          │  │
        │   │  - asyncio.Event       │    │ acquire(timeout=0)   │  │
        │   │  - poll every          │    │ — bail if cap hit    │  │
        │   │    claim_poll_sec      │    └──────────────────────┘  │
        │   └────────────────────────┘                              │
        │              │                                            │
        │              ▼                                            │
        │   claim(supported_stages=...)                             │
        │     -- atomic UPDATE in §7.3 SQL --                       │
        │              │                                            │
        │       ┌──────┴──────┐                                     │
        │       │ Job? │ None │                                     │
        │       └─┬────┴──┬───┘                                     │
        │         │       │                                         │
        │         │       └──> sleep until next wakeup              │
        │         ▼                                                 │
        │   dispatch(job) → stage handler (own coroutine)           │
        │     - on done/failed/paused: release semaphore            │
        │     - on exception: backoff / fail (Story 6.5)            │
        └───────────────────────────────────────────────────────────┘
```

The wakeup source is multi-armed:

```
                              ┌──── jobs.new (Postgres LISTEN) ────┐
                              │                                     │
   asyncio.wait FIRST_COMPLETED┼──── shutdown_event.wait() ────────►│ → loop iterates
                              │                                     │
                              └──── timer (claim_poll_sec) ────────┘
```

`claim_poll_sec` (default 1 s) is a safety net: if the LISTEN connection
ever silently drops or a notification is lost, we still claim within a
second. The polling cost is one indexed UPDATE that returns 0 rows
when the queue is empty — sub-millisecond.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/db/jobs_claim.py` | The atomic claim SQL + a thin async wrapper. |
| `pipeline/src/maktaba_pipeline/pipeline/runner.py` | `ClaimLoop`, `Worker`, `WorkerConfig`. |
| `pipeline/src/maktaba_pipeline/pipeline/wakeup.py` | `WakeupSource` abstraction (Postgres LISTEN / SQLite Pubsub). |
| `pipeline/tests/db/test_jobs_claim.py` | SQL-level claim tests (atomicity, priority, not_before, paused-resume). |
| `pipeline/tests/pipeline/test_runner.py` | Loop-level tests (wakeup integration, semaphore gating, shutdown wiring stub). |
| `pipeline/tests/pipeline/test_runner_contention.py` | The N=10 workers / 100 jobs property test from the story. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/db/__init__.py` | Re-export `claim_one`. |
| `pipeline/src/maktaba_pipeline/cli.py` | New `pipeline run-worker --stages …` subcommand that boots a `ClaimLoop`. |
| `pipeline/src/maktaba_pipeline/config.py` | Add `WorkerConfig` (claim_poll_sec, supported_stages, worker_id). |

### 2.3 Type definitions

```python
# pipeline/src/maktaba_pipeline/db/jobs_claim.py
from __future__ import annotations
from collections.abc import Sequence
from .jobs import Job, JobState, Stage


async def claim_one(
    db,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    """Atomic single-job claim. Returns None when nothing eligible.

    See architecture §7.3 for the SQL contract. The claim:
      - matches `pending` rows OR `paused` rows whose pause_requested=false,
      - filters by `not_before <= now()` and `cancel_requested = false`,
      - filters by `stage = ANY(supported_stages)`,
      - orders by (priority ASC, id ASC),
      - holds the SELECT under FOR UPDATE SKIP LOCKED so N workers don't
        contend on the same row,
      - sets state='claimed', claimed_by, claimed_at, last_heartbeat_at,
        and increments attempts in the same statement.
    """
    ...
```

### 2.4 Function signatures — runner

```python
# pipeline/src/maktaba_pipeline/pipeline/runner.py
from dataclasses import dataclass, field

@dataclass(slots=True)
class WorkerConfig:
    worker_id: str                      # "{hostname}/{pid}/{uuid}"
    supported_stages: tuple[Stage, ...] # from --stages flag or pipeline.toml
    claim_poll_sec: float = 1.0         # safety-net poll cadence
    shutdown_grace_sec: float = 120.0   # see Story 6.8

@dataclass(slots=True)
class ClaimLoop:
    db: DBConn
    cfg: WorkerConfig
    dispatch: Callable[[Job], Awaitable[None]]
    semaphores: dict[Stage, asyncio.Semaphore]   # Story 6.7
    shutdown_event: asyncio.Event = field(default_factory=asyncio.Event)
    _wakeup: WakeupSource = field(init=False)

    async def run(self) -> None: ...
```

`dispatch` is provided by `Worker`. The split of `ClaimLoop` (claim
plumbing) from `Worker` (stage routing + lifecycle) makes the loop unit
testable with a fake dispatch that records `Job.id` and never blocks.

## 3. SQL — the atomic claim

`pipeline/src/maktaba_pipeline/db/jobs_claim.py`:

```python
import json
from collections.abc import Sequence

from .jobs import Job, Stage, _row_to_job  # _row_to_job: helper from Story 6.1


_CLAIM_SQL_PG = """
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = $1,
       claimed_at        = now(),
       last_heartbeat_at = now(),
       attempts          = attempts + 1
 WHERE id = (
   SELECT id FROM processing_jobs
    WHERE state IN ('pending', 'paused')
      AND (state = 'pending'
           OR (state = 'paused' AND pause_requested = false))
      AND (not_before IS NULL OR not_before <= now())
      AND cancel_requested = false
      AND stage = ANY($2::text[])
    ORDER BY priority ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
 )
RETURNING *
"""


async def claim_one_pg(
    db,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    stages = [s.value for s in supported_stages]
    row = await db.fetchrow(_CLAIM_SQL_PG, worker_id, stages)
    return _row_to_job(row) if row else None
```

### 3.1 SQLite variant — `BEGIN IMMEDIATE` + asyncio lock

SQLite has no `FOR UPDATE SKIP LOCKED`. We emulate the SKIP-LOCKED
semantic by:

1. Acquiring a process-wide `asyncio.Lock` so only one coroutine
   inside a single worker process is in the claim critical section
   at a time. (One worker process × N stages: N coroutines may try
   to claim concurrently for different stages.)
2. Opening a `BEGIN IMMEDIATE` transaction so SQLite serializes us
   against any concurrent writer at the database level. Other workers
   in other processes (rare but supported) wait on the SQLite mutex,
   which mirrors `FOR UPDATE` for "the next one to commit wins."
3. The SELECT-UPDATE pair uses the same row id; if another worker
   committed first, our SELECT picks the next eligible row.

```python
import asyncio

_SQLITE_CLAIM_SELECT = """
SELECT id FROM processing_jobs
 WHERE state IN ('pending', 'paused')
   AND (state = 'pending'
        OR (state = 'paused' AND pause_requested = 0))
   AND (not_before IS NULL OR datetime(not_before) <= datetime('now'))
   AND cancel_requested = 0
   AND stage IN (%s)                -- placeholders generated by len(stages)
 ORDER BY priority ASC, id ASC
 LIMIT 1
"""

_SQLITE_CLAIM_UPDATE = """
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = ?,
       claimed_at        = datetime('now'),
       last_heartbeat_at = datetime('now'),
       attempts          = attempts + 1
 WHERE id = ?
   AND state IN ('pending', 'paused')   -- defensive: another writer could have raced
RETURNING *
"""


_sqlite_claim_lock = asyncio.Lock()


async def claim_one_sqlite(
    db,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    placeholders = ",".join("?" * len(supported_stages))
    select_sql = _SQLITE_CLAIM_SELECT % placeholders

    async with _sqlite_claim_lock:
        async with db.transaction(mode="immediate"):
            row = await db.fetchrow(
                select_sql, [s.value for s in supported_stages],
            )
            if row is None:
                return None
            updated = await db.fetchrow(
                _SQLITE_CLAIM_UPDATE, worker_id, row["id"],
            )
            return _row_to_job(updated) if updated else None
```

### 3.2 Dialect dispatch

```python
async def claim_one(db, *, worker_id, supported_stages) -> Job | None:
    if db.dialect == "postgres":
        return await claim_one_pg(db, worker_id=worker_id,
                                  supported_stages=supported_stages)
    return await claim_one_sqlite(db, worker_id=worker_id,
                                  supported_stages=supported_stages)
```

## 4. Wakeup source abstraction

`pipeline/src/maktaba_pipeline/pipeline/wakeup.py`:

```python
from __future__ import annotations

import asyncio
from typing import AsyncIterator, Protocol

from ..db.pubsub import JOBS_NEW, get_bus


class WakeupSource(Protocol):
    """Yields signals that mean "try to claim now". Channel-agnostic."""

    async def signals(self) -> AsyncIterator[None]: ...
    async def aclose(self) -> None: ...


class PgListenWakeup:
    """LISTEN jobs.new + a poll-tick fallback."""

    def __init__(self, db, *, poll_sec: float):
        self._db = db
        self._poll_sec = poll_sec
        self._listener = None
        self._stop = asyncio.Event()

    async def signals(self):
        # Set up the listener once.
        self._listener = await self._db.acquire_listener()
        events: asyncio.Queue[None] = asyncio.Queue()

        def on_notify(*_args):
            events.put_nowait(None)

        await self._listener.add_listener(JOBS_NEW, on_notify)

        try:
            while not self._stop.is_set():
                # Wait for either a notify, the poll timeout, or shutdown.
                done, pending = await asyncio.wait(
                    [
                        asyncio.create_task(events.get()),
                        asyncio.create_task(asyncio.sleep(self._poll_sec)),
                        asyncio.create_task(self._stop.wait()),
                    ],
                    return_when=asyncio.FIRST_COMPLETED,
                )
                for t in pending:
                    t.cancel()
                yield None
        finally:
            await self._listener.remove_listener(JOBS_NEW, on_notify)

    async def aclose(self):
        self._stop.set()


class PubsubWakeup:
    """Bus subscribe + poll. Used in SQLite mode and tests."""

    def __init__(self, *, poll_sec: float):
        self._bus = get_bus()
        self._poll_sec = poll_sec
        self._stop = asyncio.Event()

    async def signals(self):
        queue = await self._bus.subscribe(JOBS_NEW)
        while not self._stop.is_set():
            done, pending = await asyncio.wait(
                [
                    asyncio.create_task(queue.get()),
                    asyncio.create_task(asyncio.sleep(self._poll_sec)),
                    asyncio.create_task(self._stop.wait()),
                ],
                return_when=asyncio.FIRST_COMPLETED,
            )
            for t in pending:
                t.cancel()
            yield None

    async def aclose(self):
        self._stop.set()
```

`PollOnlyWakeup` (no notification source — pure poll) exists for tests
that want determinism without the bus.

## 5. The runner

`pipeline/src/maktaba_pipeline/pipeline/runner.py`:

```python
import asyncio
import contextlib
import logging
from collections.abc import Awaitable, Callable

from ..db.jobs import Job, Stage
from ..db.jobs_claim import claim_one
from .wakeup import PgListenWakeup, PubsubWakeup, WakeupSource


log = logging.getLogger(__name__)


class ClaimLoop:
    def __init__(
        self,
        db,
        cfg: "WorkerConfig",
        dispatch: Callable[[Job], Awaitable[None]],
        semaphores: dict[Stage, asyncio.Semaphore],
        shutdown_event: asyncio.Event,
    ):
        self.db = db
        self.cfg = cfg
        self.dispatch = dispatch
        self.semaphores = semaphores
        self.shutdown_event = shutdown_event
        self._wakeup: WakeupSource = (
            PgListenWakeup(db, poll_sec=cfg.claim_poll_sec)
            if db.dialect == "postgres"
            else PubsubWakeup(poll_sec=cfg.claim_poll_sec)
        )

    async def run(self) -> None:
        log.info("claim_loop_started", extra={"worker_id": self.cfg.worker_id})
        in_flight: set[asyncio.Task] = set()

        try:
            async for _ in self._wakeup.signals():
                if self.shutdown_event.is_set():
                    break

                # Drain *all* eligible jobs the wakeup permits us to claim,
                # bounded by per-stage semaphores. Looping until claim returns
                # None lets one notify carry batched enqueues from a scanner.
                while not self.shutdown_event.is_set():
                    eligible = [
                        s for s in self.cfg.supported_stages
                        if self.semaphores[s].locked() is False
                        and self.semaphores[s]._value > 0  # noqa: SLF001
                    ]
                    if not eligible:
                        break

                    job = await claim_one(
                        self.db,
                        worker_id=self.cfg.worker_id,
                        supported_stages=tuple(eligible),
                    )
                    if job is None:
                        break

                    sem = self.semaphores[Stage(job.stage)]
                    if not sem.locked():
                        await sem.acquire()
                    task = asyncio.create_task(
                        self._run_job(job, sem),
                        name=f"job-{job.id}-{job.stage}",
                    )
                    in_flight.add(task)
                    task.add_done_callback(in_flight.discard)
        finally:
            await self._wakeup.aclose()
            # Drain in-flight tasks; Story 6.8 owns the grace-period semantics.
            if in_flight:
                await asyncio.gather(*in_flight, return_exceptions=True)
            log.info("claim_loop_stopped", extra={"worker_id": self.cfg.worker_id})

    async def _run_job(self, job: Job, sem: asyncio.Semaphore) -> None:
        try:
            await self.dispatch(job)
        except Exception:
            # Story 6.5 owns the failure → backoff transition; here we only
            # log and ensure the semaphore is released.
            log.exception("job_dispatch_raised",
                          extra={"job_id": job.id, "stage": job.stage})
            raise
        finally:
            sem.release()
```

The `eligible` list filters by which stages still have capacity *before*
we hit the DB — saves a wasted UPDATE when every slot is full. The
`asyncio.Semaphore`'s private `_value` access is intentional and pinned
in a comment-justified workaround; the alternative is wrapping the
semaphore (Story 6.7's plan does so).

## 6. Test plan

### 6.1 SQL-level (`pipeline/tests/db/test_jobs_claim.py`)

| Test | What it pins |
|---|---|
| `test_claim_returns_pending_row` | Insert one pending row → `claim_one` returns a Job with `state='claimed'`, `claimed_by=worker`, `claimed_at` set, `attempts=1`. |
| `test_claim_returns_none_when_empty` | Empty table → returns `None` without raising. |
| `test_claim_returns_none_when_only_terminal` | Insert done/failed/cancelled rows → returns `None`. |
| `test_claim_respects_priority` | Insert 3 rows: priorities 100, 50, 200 → first claim returns the 50, second the 100, third the 200. |
| `test_claim_respects_id_tiebreak` | Two rows at priority 100, ids 5 and 7 → 5 claimed first. |
| `test_claim_skips_not_before_in_future` | `not_before = now() + 60s` → not claimable until clock advances. Use `pg_advance_clock()` shim or set `not_before` to the past. |
| `test_claim_picks_paused_when_pause_requested_false` | Insert `state='paused', pause_requested=false` → claim returns the row; state transitions to `claimed`; `attempts++`. |
| `test_claim_skips_paused_when_pause_requested_true` | Insert `state='paused', pause_requested=true` → claim returns `None`. |
| `test_claim_skips_cancel_requested` | Insert `state='pending', cancel_requested=true` → claim returns `None`. (Cancel is a Story 6.4 transition, not a claim filter consequence.) |
| `test_claim_filters_by_stage` | Insert `transcribe` row; claim with `supported_stages=('extract',)` → `None`. Switch to `('transcribe',)` → returns the row. |
| `test_claim_atomic_under_contention_postgres` | 10 asyncio tasks, all calling `claim_one_pg` against 100 enqueued rows; assert each row claimed exactly once (count `claimed_by` per row, must be 1). |
| `test_claim_atomic_under_contention_sqlite` | Same as above but SQLite; relies on `_sqlite_claim_lock` + `BEGIN IMMEDIATE`. |
| `test_claim_increments_attempts` | Two consecutive claims of the same row (after a manual reset to pending) → `attempts` reads 1 then 2. |
| `test_claim_respects_paused_pause_requested_combinations` | Truth-table: state ∈ {pending, paused, claimed, running, done, failed, cancelled} × pause_requested ∈ {true, false} → only (pending, *) and (paused, false) are claimable. |

### 6.2 Wakeup integration (`pipeline/tests/pipeline/test_runner.py`)

| Test | What it pins |
|---|---|
| `test_listen_jobs_new_wakes_claim_loop` | Start a loop with a slow `claim_poll_sec=10s`; enqueue from a separate connection → loop claims within 1 s. |
| `test_pubsub_wakes_loop_sqlite` | SQLite variant of the above; `PubsubBus` publish wakes the loop. |
| `test_poll_fallback_when_listener_missing` | Boot the loop with `poll_sec=0.1` and a fake wakeup that never yields notify → loop still claims via the poll arm. |
| `test_loop_drains_batch_on_one_notify` | Enqueue 5 jobs in a single transaction (one notify fires); loop claims all 5 within one wakeup iteration when caps permit. |
| `test_shutdown_event_breaks_loop` | Set `shutdown_event` while loop is sleeping → loop exits within 0.5 s. |
| `test_loop_releases_semaphore_on_dispatch_exception` | Dispatch raises; semaphore back at full capacity; loop continues. |

### 6.3 Property test — N=10 workers / 100 jobs

`pipeline/tests/pipeline/test_runner_contention.py`:

```python
import asyncio
import pytest
from uuid import uuid4

from maktaba_pipeline.db.jobs import enqueue, Stage
from maktaba_pipeline.db.jobs_claim import claim_one


@pytest.mark.parametrize("dialect", ["postgres", "sqlite"])
@pytest.mark.asyncio
async def test_claim_atomic_under_contention(db_for_dialect, video_factory, dialect):
    db = db_for_dialect
    videos = [await video_factory() for _ in range(100)]
    for v in videos:
        await enqueue(db, video_id=v.id, stage=Stage.PROBE)

    claimed_ids: list[int] = []
    lock = asyncio.Lock()

    async def worker(name: str):
        local: list[int] = []
        while True:
            job = await claim_one(db, worker_id=name,
                                  supported_stages=(Stage.PROBE,))
            if job is None:
                # Drain may finish before all rows visible to this connection;
                # do one more round-trip after a tiny sleep.
                await asyncio.sleep(0.01)
                job = await claim_one(db, worker_id=name,
                                      supported_stages=(Stage.PROBE,))
                if job is None:
                    break
            local.append(job.id)
        async with lock:
            claimed_ids.extend(local)

    await asyncio.gather(*(worker(f"w{i}") for i in range(10)))

    assert sorted(claimed_ids) == sorted([
        j["id"] for j in await db.fetch(
            "SELECT id FROM processing_jobs WHERE state='claimed'")
    ])
    assert len(claimed_ids) == 100
    assert len(set(claimed_ids)) == 100   # no double-claim
```

The double-loop guard (`if None: sleep and try once more`) handles the
brief window where another worker has just released the SELECT row's
lock but our connection's snapshot doesn't yet see the new committed
state. In production the LISTEN signal handles this naturally; in the
test we don't wire up the bus.

### 6.4 Cross-stage worker

`test_disjoint_stage_workers_scale` (also referenced by Story 6.7):
launch one worker with `--stages transcribe` and another with
`--stages index`; enqueue 5 transcribe + 5 index jobs; assert both
workers run concurrently with no contention. Owned here for the
loop-level wiring; Story 6.7 owns the semaphore math.

## 7. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Worker dies between SELECT and UPDATE | Cannot happen: the claim is one statement; the SELECT lives in the sub-query and shares the UPDATE's transaction. The Postgres engine guarantees atomicity. | Architecture §7.3; verified by `test_claim_atomic_under_contention_postgres` running the loop under chaos kill (Story 6.6's plan) |
| Two workers race on the same row id | `FOR UPDATE SKIP LOCKED` makes the loser's sub-query return a different row id (or none); each worker claims a distinct row or none. | `test_claim_atomic_under_contention_*` |
| `cancel_requested=true` arrives at the front of the queue | Skipped by the WHERE; cancellation is enacted by Story 6.4's flag-set responder, not by the claim loop. | `test_claim_skips_cancel_requested` |
| Worker supports only `transcribe`; an `index` job is enqueued at priority 50 | Worker never claims it. Another worker (or the same one with broader `--stages`) claims it. | `test_claim_filters_by_stage` |
| `paused` row has `pause_requested=true` | Excluded by the predicate; it stays "shelved" until `pause_requested` is cleared (Story 6.4). | `test_claim_skips_paused_when_pause_requested_true` |
| `paused` row with `pause_requested=false` | Returned by the claim; the worker sees `state` was `paused` and walks into the `resuming` setup phase before flipping to `running` (Story 6.6 / 7.7). The state at the end of the atomic claim is `claimed`; the next UPDATE inside the worker flips to `resuming`. | `test_claim_picks_paused_when_pause_requested_false` |
| `not_before` set to the past | Treated as eligible. Indexed by the claim index. | `test_claim_skips_not_before_in_future` (negative variant covers the past case) |
| Empty `supported_stages` argument | Defensive raise: a worker that supports no stages should not be calling claim. The CLI rejects `--stages` empty at startup. | Unit test in `test_runner.py`: `test_empty_stages_raises` |
| LISTEN connection dies mid-loop | The asyncpg listener fires `connection_lost`; `PgListenWakeup` reconnects and re-subscribes on the next iteration. The poll-tick fallback bridges the outage. | `test_listener_reconnect_after_disconnect` (in §6.2 fixture, not enumerated) |
| SQLite single-writer contention with 10 workers | Realistic upper bound is 1–2 worker processes for SQLite; the asyncio lock serializes claim within a process and the WAL serializes across processes. The story's atomicity test still passes because correctness, not throughput, is what's asserted. | `test_claim_atomic_under_contention_sqlite` |
| Stage filter changes mid-loop (operator updates pipeline.toml) | Out of scope here. The runner reads `cfg.supported_stages` once at startup. SIGHUP reload is not wired; restart the worker to change stages. Documented in `pipeline.toml` comments. | N/A |

## 8. Performance analysis

### 8.1 Per-claim cost

Postgres claim, fully indexed:

| Step | Cost |
|---|---|
| `LISTEN jobs.new` notify delivery | < 1 ms (in-process asyncpg) |
| Claim UPDATE with index lookup + `FOR UPDATE SKIP LOCKED` | ~0.3 ms cold, ~0.05 ms warm on a 10K-row table |
| Network round-trip (Unix domain socket) | ~0.05 ms |

→ Single-claim wall-clock ~0.5 ms warm. The loop can claim ~2K jobs/s
per worker against a quiet DB; we never need that throughput because
real jobs run for seconds to hours.

### 8.2 Notify storm

If a scanner enqueues 10 000 rows in a tight loop, 10 000 notify
messages fan out. Each subscriber sees them all (asyncpg buffers,
no drops within reason). The claim loop drains greedily on each
wakeup, so the notify storm collapses into one contiguous claim
batch; CPU per notify is ~50 µs of bookkeeping in the asyncpg layer.

If notify backlog ever becomes a problem, the fix is per-channel
deduplication in the `PgListenWakeup` (collapse N pending notifies
into one wakeup signal); the public interface doesn't change.

## 9. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `asyncpg` | ≥ 0.29 | LISTEN/NOTIFY support; same dep as Story 6.1. |
| `aiosqlite` | ≥ 0.20 | SQLite async driver; same dep as Story 6.1. |
| `pytest-asyncio` | dev | All claim/loop tests are coroutine-based. |
| `freezegun` | dev | Used in `test_claim_skips_not_before_in_future` to advance the clock; alternatively the test inserts `not_before = now() - interval '1s'` for the eligible variant and `now() + interval '60s'` for the skip variant — `freezegun` is used only when the test wants to assert eligibility flip without a sleep. |

## 10. Acceptance checklist

**Code**
- [ ] `pipeline/src/maktaba_pipeline/db/jobs_claim.py` exposes `claim_one(db, *, worker_id, supported_stages)` working on both Postgres and SQLite.
- [ ] The Postgres SQL is byte-identical to architecture §7.3 (verified by a string-compare test against the file).
- [ ] `pipeline/src/maktaba_pipeline/pipeline/runner.py` exposes `ClaimLoop`, `WorkerConfig`.
- [ ] `pipeline/src/maktaba_pipeline/pipeline/wakeup.py` exposes `PgListenWakeup`, `PubsubWakeup`, `WakeupSource` Protocol.
- [ ] `pipeline/cli.py` boots a worker with `--stages transcribe,probe` (parses to a tuple of `Stage`).

**Behaviour (story acceptance criteria)**
- [ ] AC: claim is atomic under N=10 worker contention (`test_claim_atomic_under_contention_*`).
- [ ] AC: claim respects priority order.
- [ ] AC: claim respects `not_before` and `cancel_requested`.
- [ ] AC: claim returns `None` cleanly when empty.
- [ ] AC: claim picks up `paused, pause_requested=false` rows for resume.
- [ ] AC: `LISTEN jobs.new` wakes the loop within `claim_poll_sec` of the enqueue.
- [ ] AC: a worker with `--stages transcribe` does not claim `index` jobs even when they're at the front of the queue.

**Performance**
- [ ] Single-claim p95 < 5 ms warm against a 10K-row table on the CI Postgres.
- [ ] Claim loop overhead under no-work conditions < 1% CPU (one indexed UPDATE returning 0 rows per `claim_poll_sec`).

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.2.
- [ ] The `runner.py` module docstring documents the loop's wakeup-and-drain pattern with a link back to architecture §7.3.
