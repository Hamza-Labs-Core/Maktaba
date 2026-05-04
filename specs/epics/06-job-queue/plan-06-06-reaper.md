# Implementation Plan — Story 6.6 Reaper for Crashed Claims

> Companion to [story-06-06-reaper.md](story-06-06-reaper.md).
> The story states *what* and *why*; this plan states *how*.
> The 90 s `stale_claim_sec` math is fixed in the README:
> `90 s = 18 × 5 s heartbeat`.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline). The reaper is a periodic asyncio task that runs inside every Pipeline worker process. Mutual exclusion across processes is enforced with `pg_advisory_lock`. |
| Files | `pipeline/src/maktaba_pipeline/pipeline/reaper.py` (loop), `pipeline/src/maktaba_pipeline/db/jobs_reaper.py` (SQL), `pipeline/tests/pipeline/test_reaper.py`, `pipeline/tests/db/test_jobs_reaper_sql.py`. |
| Schema dependency | The partial reaper index from Story 6.1: `(state, last_heartbeat_at) WHERE state IN ('claimed','running','resuming')`. |
| Out of scope | The crash-recovery experience from the worker side (Epic 3 Story 3.8); the API's surfacing of `jobs.reaped` events to the UI (Epic 2 Story 2.5). |

## 1. Architecture diagram

```
                       ┌──────────────────────────────────────┐
                       │ Each Pipeline worker process spawns: │
                       │   Reaper(db, interval=30s).start()   │
                       └──────────────────┬───────────────────┘
                                          │
                                          ▼
                ┌──────────────────────────────────────────────┐
                │  every reaper_interval_sec (default 30 s)    │
                │                                              │
                │   1. SELECT pg_try_advisory_lock(LOCK_KEY)   │
                │      ↳ false → skip this tick                │
                │      ↳ true → continue                       │
                │                                              │
                │   2. UPDATE processing_jobs                  │
                │        SET state='paused',                   │
                │            paused_at=now(),                  │
                │            paused_at_sec=last_segment_end_sec│
                │            paused_reason='crash',            │
                │            claimed_by=NULL,                  │
                │            pause_requested=false             │
                │      WHERE state IN ('claimed','running',    │
                │                     'resuming')              │
                │        AND last_heartbeat_at <               │
                │              now() - $stale_claim_sec        │
                │      RETURNING id, state_before, paused_at_sec│
                │                                              │
                │   3. For each reaped row:                    │
                │        pg_notify('jobs.reaped', payload)     │
                │        log INFO "reaped_stale_claim"         │
                │        counter += 1                          │
                │                                              │
                │   4. SELECT pg_advisory_unlock(LOCK_KEY)     │
                └──────────────────────────────────────────────┘
                                          │
                                          ▼
              ┌────────────────────────────────────────────────┐
              │ Other workers wake on jobs.reaped →            │
              │   their claim loops re-claim the paused row     │
              │   per Story 6.2 (paused + pause_requested=false)│
              └────────────────────────────────────────────────┘
```

`stale_claim_sec` is enforced in two places: the runtime config (default
90 s) and a property test (`stale_claim_sec == 18 × heartbeat_sec`)
that breaks the build if the two drift apart.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/pipeline/reaper.py` | `Reaper` class, `start()`/`stop()`, periodic loop. |
| `pipeline/src/maktaba_pipeline/db/jobs_reaper.py` | The reaper SQL + `reap_once(db) -> list[ReapedRow]`. |
| `pipeline/src/maktaba_pipeline/config.py` (modified) | Adds `[reaper]` section: `interval_sec=30`, `stale_claim_sec=90`. |
| `pipeline/tests/pipeline/test_reaper.py` | Loop-level tests. |
| `pipeline/tests/db/test_jobs_reaper_sql.py` | SQL-level tests including advisory-lock contention. |

### 2.2 Type definitions

```python
# pipeline/src/maktaba_pipeline/db/jobs_reaper.py
from dataclasses import dataclass
from datetime import datetime
from uuid import UUID

# 32-bit advisory lock key. Picked once and pinned by a comment so future
# refactors don't reuse the integer for another lock.
REAPER_ADVISORY_LOCK_KEY: int = 0x6A6F6273   # ASCII "jobs"


@dataclass(frozen=True, slots=True)
class ReapedRow:
    id: int
    video_id: UUID
    stage: str
    prev_state: str           # 'claimed' | 'running' | 'resuming'
    paused_at_sec: float
    paused_at: datetime
    last_heartbeat_at: datetime
```

### 2.3 The reaper SQL

`pipeline/src/maktaba_pipeline/db/jobs_reaper.py`:

```python
import json
from collections.abc import Sequence

from .pubsub import JOBS_REAPED, get_bus


_REAP_SQL_PG = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = last_segment_end_sec,
       paused_reason    = 'crash',
       claimed_by       = NULL,
       pause_requested  = false
 WHERE state IN ('claimed', 'running', 'resuming')
   AND last_heartbeat_at < now() - ($1 || ' seconds')::interval
RETURNING id, video_id, stage,
          (SELECT state FROM processing_jobs prev
            WHERE prev.id = processing_jobs.id) AS state_now,
          paused_at_sec, paused_at, last_heartbeat_at
"""

# RETURNING gives us the row AFTER the UPDATE — `state` is always 'paused'.
# We lose the prior state via this approach. To capture the prior state
# without a sub-query oddity, use a CTE:

_REAP_SQL_PG_CTE = """
WITH stale AS (
    SELECT id, state AS prev_state
      FROM processing_jobs
     WHERE state IN ('claimed', 'running', 'resuming')
       AND last_heartbeat_at < now() - ($1 || ' seconds')::interval
     FOR UPDATE SKIP LOCKED
)
UPDATE processing_jobs pj
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = pj.last_segment_end_sec,
       paused_reason    = 'crash',
       claimed_by       = NULL,
       pause_requested  = false
  FROM stale
 WHERE pj.id = stale.id
RETURNING pj.id, pj.video_id, pj.stage,
          stale.prev_state,
          pj.paused_at_sec, pj.paused_at, pj.last_heartbeat_at
"""


async def reap_once(db, *, stale_claim_sec: float) -> list[ReapedRow]:
    """One reaper tick. Returns the rows that were reaped.

    The CTE captures the prior state for the notify payload; the FOR
    UPDATE SKIP LOCKED prevents two reaper instances from double-reaping
    the same row should the advisory lock ever fail.
    """
    rows = await db.fetch(_REAP_SQL_PG_CTE, str(stale_claim_sec))
    out: list[ReapedRow] = []
    for r in rows:
        reaped = ReapedRow(
            id=r["id"], video_id=r["video_id"], stage=r["stage"],
            prev_state=r["prev_state"],
            paused_at_sec=r["paused_at_sec"], paused_at=r["paused_at"],
            last_heartbeat_at=r["last_heartbeat_at"],
        )
        out.append(reaped)

        payload = {
            "id":            reaped.id,
            "prev_state":    reaped.prev_state,
            "paused_at_sec": reaped.paused_at_sec,
        }
        if db.dialect == "postgres":
            await db.execute(
                "SELECT pg_notify('jobs.reaped', $1)", json.dumps(payload),
            )
        else:
            get_bus().publish(JOBS_REAPED, payload)

    return out
```

The `RETURNING pj.id, pj.video_id, ...` columns drive the notify payload
exactly per the story's `{id, prev_state, paused_at_sec}` schema.

### 2.4 The reaper loop

`pipeline/src/maktaba_pipeline/pipeline/reaper.py`:

```python
from __future__ import annotations

import asyncio
import logging

from ..db.jobs_reaper import REAPER_ADVISORY_LOCK_KEY, reap_once


log = logging.getLogger(__name__)


class Reaper:
    def __init__(
        self,
        db,
        *,
        interval_sec: float = 30.0,
        stale_claim_sec: float = 90.0,
        heartbeat_sec: float = 5.0,   # for the parity assertion
    ):
        # Property: stale_claim_sec must be >> heartbeat_sec; we pin
        # 18× as the canonical ratio (Story 6.6 README note).
        if heartbeat_sec > 0:
            ratio = stale_claim_sec / heartbeat_sec
            if abs(ratio - 18) > 1e-6:
                raise ValueError(
                    f"stale_claim_sec ({stale_claim_sec}) must equal "
                    f"18 × heartbeat_sec ({heartbeat_sec}) = "
                    f"{18 * heartbeat_sec}"
                )

        self.db = db
        self.interval_sec = interval_sec
        self.stale_claim_sec = stale_claim_sec
        self._stop = asyncio.Event()
        self._task: asyncio.Task | None = None

    async def _try_lock(self) -> bool:
        """Postgres: pg_try_advisory_lock; SQLite: process-local mutex."""
        if self.db.dialect == "postgres":
            row = await self.db.fetchrow(
                "SELECT pg_try_advisory_lock($1) AS got",
                REAPER_ADVISORY_LOCK_KEY,
            )
            return bool(row["got"])
        # SQLite: there's only one process holding the connection in our
        # deployment shape; the asyncio.Lock below serializes ticks.
        if not hasattr(self, "_local_lock"):
            self._local_lock = asyncio.Lock()
        return await self._local_lock.acquire() is None or True

    async def _release_lock(self) -> None:
        if self.db.dialect == "postgres":
            await self.db.execute(
                "SELECT pg_advisory_unlock($1)", REAPER_ADVISORY_LOCK_KEY,
            )
        else:
            self._local_lock.release()

    async def _tick(self) -> int:
        if not await self._try_lock():
            log.debug("reaper_lock_busy")
            return 0
        try:
            reaped = await reap_once(
                self.db, stale_claim_sec=self.stale_claim_sec,
            )
            if reaped:
                log.info(
                    "reaped_stale_claims", extra={
                        "count": len(reaped),
                        "ids":   [r.id for r in reaped],
                    },
                )
            return len(reaped)
        finally:
            await self._release_lock()

    async def _run(self) -> None:
        log.info("reaper_started",
                 extra={"interval_sec": self.interval_sec,
                        "stale_claim_sec": self.stale_claim_sec})
        while not self._stop.is_set():
            try:
                await self._tick()
            except Exception:
                log.exception("reaper_tick_failed")
            try:
                await asyncio.wait_for(self._stop.wait(),
                                       timeout=self.interval_sec)
                return  # _stop set
            except asyncio.TimeoutError:
                continue

    def start(self) -> None:
        assert self._task is None
        self._task = asyncio.create_task(self._run(), name="reaper")

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
```

The `_try_lock` is non-blocking: a second worker tick that arrives
while another reaper is still running simply returns 0 and waits for
the next interval.

## 3. Test plan

### 3.1 SQL tests (`pipeline/tests/db/test_jobs_reaper_sql.py`)

| Test | What it pins |
|---|---|
| `test_reaper_pauses_stale_claim` | Insert row claimed=now-100s, last_heartbeat_at=now-100s; `reap_once(stale_claim_sec=90)` → row reaped; state='paused', paused_reason='crash', paused_at_sec=last_segment_end_sec, claimed_by=NULL. |
| `test_reaper_skips_fresh_heartbeats` | Insert row claimed=now-200s but last_heartbeat_at=now-1s; `reap_once(stale_claim_sec=90)` → no rows reaped. |
| `test_reaper_skips_terminal_states` | Insert done/failed/cancelled rows with last_heartbeat_at=now-1000s; reap → no rows reaped. |
| `test_reaper_skips_paused` | Insert paused row with last_heartbeat_at=now-1000s; reap → no rows. |
| `test_reaper_returns_prev_state` | Reap a `running` row → ReapedRow.prev_state == 'running'. Reap a `resuming` row → 'resuming'. |
| `test_reaper_emits_jobs_reaped_notify` | LISTEN jobs.reaped; reap one row → exactly one notify with `{id, prev_state, paused_at_sec}`. |
| `test_reaper_advisory_lock_blocks_concurrent` | Open two connections; conn A acquires `pg_advisory_lock(REAPER_ADVISORY_LOCK_KEY)`; conn B's `pg_try_advisory_lock` returns false; B's reap path is a no-op. |
| `test_reaper_skips_for_update_skip_locked` | Two reaper coroutines on conn A and B (same advisory key, simulate via different keys for the test) call `reap_once` simultaneously against 100 stale rows; total reaps == 100; no row reaped twice. |
| `test_reaper_payload_byte_for_byte` | Pin the JSON payload key order and types. |
| `test_reaper_uses_partial_index` | `EXPLAIN` the reap UPDATE; assert the planner picks `processing_jobs_reaper_idx`. |

### 3.2 Loop tests (`pipeline/tests/pipeline/test_reaper.py`)

| Test | What it pins |
|---|---|
| `test_reaper_runs_periodically` | Start reaper(interval=0.05s); insert two stale rows; sleep 0.2s; both rows reaped; stop. |
| `test_reaper_advisory_lock_two_instances` | Start two `Reaper` instances against the same DB; insert one stale row; only one reap fires; the other's tick is a no-op. |
| `test_reaper_stop_is_prompt` | After `stop()`, no further ticks; underlying advisory lock released. |
| `test_reaper_logs_count` | Insert 5 stale rows; capture INFO logs; one log entry with `count=5, ids=[...]`. |
| `test_stale_claim_sec_default_matches_heartbeat_ratio` | Constructor with `stale_claim_sec=90, heartbeat_sec=5` succeeds; `stale_claim_sec=60, heartbeat_sec=5` raises. |
| `test_reaper_swallows_db_error_and_keeps_running` | Wrap `reap_once` to raise once; reaper logs exception; next tick completes normally. |
| `test_reaper_emits_per_row_notify` | Insert 3 stale rows; subscribe to bus/listener; receive exactly 3 notifies. |

### 3.3 Property test — `stale_claim_sec` parity

```python
def test_stale_claim_sec_default_matches_heartbeat_ratio():
    from maktaba_pipeline.pipeline.reaper import Reaper

    # The canonical ratio per Story 6.6 README note.
    Reaper(db=_NullDb(), interval_sec=30.0,
           stale_claim_sec=90.0, heartbeat_sec=5.0)  # OK

    with pytest.raises(ValueError, match="18"):
        Reaper(db=_NullDb(), stale_claim_sec=60.0, heartbeat_sec=5.0)

    with pytest.raises(ValueError):
        Reaper(db=_NullDb(), stale_claim_sec=90.0, heartbeat_sec=10.0)
```

A config change to one without the other fails at construction time —
the bug never reaches a deployed environment.

## 4. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Clock skew between client and server | The reaper's `now() - $sec::interval` math is server-side; client clocks are irrelevant. The `last_heartbeat_at` was also written with `now()` from the same server. | Architecture §7.9; `test_reaper_pauses_stale_claim` |
| Worker revives just as the reaper runs | The UPDATE's WHERE filters by `last_heartbeat_at < now() - stale_claim_sec`. If a heartbeat lands right before the reaper's UPDATE, the predicate's now() snapshot may still see the old value → row reaped. The worker's NEXT progress UPDATE finds `state='paused'` (state predicate excludes it from the UPDATE) → its UPDATE is a no-op → it sees zero affected rows → it exits the stage. | `test_reaper_race_with_late_heartbeat` (in `test_reaper.py`) |
| Two reaper instances start within the same millisecond | `pg_try_advisory_lock` is mutex-strong; only one wins. | `test_reaper_advisory_lock_blocks_concurrent` |
| Reaper crashes mid-tick (e.g., DB connection drops between SELECT and the UPDATE in the CTE) | The transaction rolls back; the `pg_advisory_lock` is released automatically when the connection dies; next tick from any instance acquires the lock. | Inherited from Postgres; not separately tested. |
| Row whose `paused_at_sec` would be NULL (no segments committed yet) | `last_segment_end_sec` defaults to `0` → `paused_at_sec=0`. Resume restarts from offset 0, equivalent to a fresh claim. | `test_reaper_zero_offset_for_fresh_job` |
| `paused_reason` already set to 'user' (a force-pause that didn't transition cleanly) | Cannot happen: force-pause excludes the row from `state IN ('claimed','running','resuming')`. The reaper would not target a row already in `paused`. | Implicit by SQL predicate. |
| Worker's heartbeat task didn't shut down on graceful exit (Story 6.8 path) but the worker also paused all its rows | The `state=paused` predicate excludes them; reaper leaves them alone. | `test_reaper_skips_paused` |
| Operator changes `stale_claim_sec` mid-run via SIGHUP | Out of scope: config reload is not wired (per Story 6.2 plan). Restart workers to change the value. | Documented in `pipeline.toml`. |
| Tens of thousands of stale rows (catastrophic outage recovery) | Single UPDATE returns all rows in one statement; the partial index makes this proportional to the stale count, not the full table. The notify fanout is per-row; the API consumer sees one event per id. The advisory lock ensures only one worker drives the recovery. | Stress test `test_reaper_handles_10k_stale` |

## 5. Performance analysis

### 5.1 Per-tick cost

Quiet queue (no stale rows): one `pg_try_advisory_lock`, one CTE
`UPDATE … RETURNING` returning zero rows, one `pg_advisory_unlock`.
Total: ~0.5 ms warm. At 30 s interval, < 0.002% of a CPU core.

### 5.2 Catastrophic recovery

If 10K rows go stale at once (entire pipeline restarted), the single
UPDATE walks the partial reaper index (~10K rows), takes row-level
locks, writes new tuple versions, and fires 10K notifies. On a
commodity DB this completes in ~200 ms. Acceptable for a recovery
event.

The notify fanout (10K events) is throttled by the API/WS layer (1 Hz
per visible job); the bus itself absorbs the burst easily.

## 6. Dependencies

No new deps. `pg_advisory_lock` is core Postgres; SQLite path uses
`asyncio.Lock`.

## 7. Acceptance checklist

**Code**
- [ ] `Reaper` class runs at default 30 s interval.
- [ ] `stale_claim_sec` defaults to 90.
- [ ] Constructor raises if `stale_claim_sec / heartbeat_sec != 18`.
- [ ] `pg_try_advisory_lock(REAPER_ADVISORY_LOCK_KEY)` ensures only one reaper runs at a time across all worker processes against the same DB.
- [ ] The reap UPDATE never reaps `done`, `failed`, `paused`, `cancelled`, or `pending` rows (verified by predicate AND by tests).

**Behaviour (story acceptance criteria)**
- [ ] AC: `test_reaper_skips_fresh_heartbeats` — reaper's UPDATE matches zero rows when heartbeats are fresh.
- [ ] AC: `test_reaper_advisory_lock` — second reaper returns immediately.
- [ ] AC: `test_reaper_emits_jobs_reaped_notify` — one notify per reaped row with `{id, prev_state, paused_at_sec}`.
- [ ] AC: `test_stale_claim_sec_default_matches_heartbeat_ratio` — config drift fails the build.

**Performance**
- [ ] No-op tick < 1 ms warm.
- [ ] Stress test: 10K stale rows reaped in < 1 s on the standard CI Postgres.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.6.
- [ ] `Reaper` module docstring documents the `90 s = 18 × 5 s` ratio with a link back to the README's resolved REVIEW §1.4.c entry.
