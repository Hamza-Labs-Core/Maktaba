# Implementation Plan — Story 6.3 Heartbeat & Progress

> Companion to [story-06-03-heartbeat-progress.md](story-06-03-heartbeat-progress.md).
> The story states *what* and *why*; this plan states *how*.
> Channel naming is fixed in the [README](README.md): `jobs.progress` and
> `jobs.heartbeat` are plural and singular legacy names are retired.

## 0. Scope and placement

| Concern | Decision |
|---|---|
| Language | Python (Pipeline Service). The progress UPDATE and notify path live in `pipeline/db/jobs_progress.py`. The heartbeat task runs inside the worker coroutine alongside the stage handler. |
| Schema dependency | Story 6.1's `processing_jobs`. No new columns; we use `last_heartbeat_at`, `progress_updated_at`, `processed_seconds`, `segments_completed`, `last_segment_end_sec`, `realtime_factor`, `estimated_remaining_sec`. |
| Notify trigger | Postgres trigger `processing_jobs_notify_progress_trg` fires `pg_notify('jobs.progress', payload)` when `progress_updated_at` is bumped, and `pg_notify('jobs.heartbeat', payload)` when only `last_heartbeat_at` is bumped. We split the trigger so consumers can subscribe to one or the other. |
| Out of scope | Per-segment commit transactions for the transcribe stage (Epic 3 Story 3.6); reaper math (Story 6.6); pause flag observation (Story 6.4). This story owns the heartbeat cadence, the progress UPDATE, and the two notifications. |

## 1. Architecture diagram

```
                ┌─────────────────────────────────────────────┐
                │ Worker stage handler (e.g., transcribe)     │
                │ Spawns two cooperative tasks:               │
                │  ┌────────────────────┐  ┌───────────────┐  │
                │  │ stage main coro    │  │ heartbeat task │  │
                │  │ (per-segment loop) │  │ (5s tick)      │  │
                │  └─────────┬──────────┘  └───────┬───────┘  │
                └────────────┼────────────────────┼──────────┘
                             │                    │
        on each segment      │                    │ every heartbeat_sec
        commit               ▼                    ▼
              ┌──────────────────────┐  ┌────────────────────────┐
              │ tick_progress(...)   │  │ tick_heartbeat(...)    │
              │ — single UPDATE      │  │ — single UPDATE        │
              │   bumps progress +   │  │   only last_heartbeat  │
              │   last_heartbeat_at  │  │   _at = now()          │
              └──────────┬───────────┘  └────────────┬───────────┘
                         │                           │
                         ▼                           ▼
              ┌─────────────────────────────────────────────────┐
              │ Postgres trigger:                               │
              │   AFTER UPDATE WHEN OLD.progress_updated_at IS  │
              │     DISTINCT FROM NEW.progress_updated_at       │
              │     → pg_notify('jobs.progress', payload)       │
              │                                                 │
              │   AFTER UPDATE WHEN ONLY OLD.last_heartbeat_at  │
              │     IS DISTINCT FROM NEW.last_heartbeat_at      │
              │     → pg_notify('jobs.heartbeat', { id, stage,  │
              │                                     ts })       │
              └────────────────────┬────────────────────────────┘
                                   │
                ┌──────────────────┴──────────────────┐
                ▼                                     ▼
          jobs.progress                          jobs.heartbeat
          (consumed by API → WS                  (consumed by reaper
           Story 2.5 for live UI)                 Story 6.6 only)
```

The split rule, in plain English:

- **Progress tick** — any UPDATE that moves the segment counter,
  `processed_seconds`, or `last_segment_end_sec`. Fires `jobs.progress`
  *and* doubles as a heartbeat (no separate `jobs.heartbeat` notify).
- **Heartbeat-only tick** — UPDATE that touches only `last_heartbeat_at`.
  Fires `jobs.heartbeat`. Never fires `jobs.progress`.

The UI never subscribes to `jobs.heartbeat`; rendering on every 5 s tick
would be wasted bandwidth. The reaper never subscribes to `jobs.progress`
because progress already implies liveness.

## 2. Implementation steps

### 2.1 New files

| Path | Purpose |
|---|---|
| `pipeline/src/maktaba_pipeline/db/jobs_progress.py` | `tick_progress`, `tick_heartbeat`, the two SQL statements, payload shape. |
| `pipeline/src/maktaba_pipeline/pipeline/heartbeat.py` | `HeartbeatTask` — async coroutine that fires `tick_heartbeat` every `heartbeat_sec`. |
| `shared/db/migrations/0028_jobs_progress_notify.sql` | The progress + heartbeat triggers. |
| `pipeline/tests/db/test_jobs_progress.py` | Progress UPDATE + notify tests. |
| `pipeline/tests/pipeline/test_heartbeat.py` | Heartbeat task lifecycle tests. |
| `pipeline/tests/lint/test_no_singular_channel_names.py` | Greps `pipeline/` and `api/` for retired singular names. |

### 2.2 Modified files

| Path | Change |
|---|---|
| `pipeline/src/maktaba_pipeline/db/__init__.py` | Re-export `tick_progress`, `tick_heartbeat`. |
| `pipeline/src/maktaba_pipeline/config.py` | Add `[workers].heartbeat_sec = 5` (default); pin in `WorkerConfig`. |

### 2.3 Function signatures

```python
# pipeline/src/maktaba_pipeline/db/jobs_progress.py
from dataclasses import dataclass


@dataclass(slots=True, frozen=True)
class ProgressTick:
    """Inputs to a progress UPDATE. Defaults are 'no change'."""
    job_id: int
    # processed_seconds is the absolute value (segment.end - seek_from)
    # per architecture §7.6, NOT a delta. The caller computes it.
    processed_seconds: float = 0.0
    segments_completed_delta: int = 0
    last_segment_end_sec: float | None = None    # absolute, not delta
    realtime_factor: float | None = None         # EWMA-smoothed, not raw
    estimated_remaining_sec: float | None = None


async def tick_progress(db, t: ProgressTick) -> None:
    """One UPDATE that bumps progress counters AND last_heartbeat_at.

    The trigger fires `jobs.progress` exactly once. Caller is the
    transcribe stage's per-segment commit (Epic 3 Story 3.6) — that
    commit lives in the same transaction as the segment INSERT, so
    this function MUST be called inside an open transaction.
    """
    ...


async def tick_heartbeat(db, *, job_id: int) -> None:
    """One UPDATE that only sets last_heartbeat_at = now().

    The trigger fires `jobs.heartbeat`; the reaper observes it; the
    UI does not.
    """
    ...
```

```python
# pipeline/src/maktaba_pipeline/pipeline/heartbeat.py
import asyncio
from contextlib import asynccontextmanager


class HeartbeatTask:
    """Periodic last_heartbeat_at update for stages without per-segment cadence.

    Lifecycle: started at the top of the stage handler, cancelled at the
    end (success, failure, or pause). The progress tick path is always
    the better signal — start the heartbeat task only for stages that
    do NOT call tick_progress on a frequent enough cadence.
    """

    def __init__(self, db, *, job_id: int, interval_sec: float):
        self.db = db
        self.job_id = job_id
        self.interval_sec = interval_sec
        self._task: asyncio.Task | None = None
        self._stop = asyncio.Event()

    async def _run(self) -> None:
        while not self._stop.is_set():
            try:
                await asyncio.wait_for(
                    self._stop.wait(), timeout=self.interval_sec,
                )
                return  # _stop set → exit cleanly
            except asyncio.TimeoutError:
                await tick_heartbeat(self.db, job_id=self.job_id)

    def start(self) -> None:
        assert self._task is None
        self._task = asyncio.create_task(
            self._run(), name=f"heartbeat-{self.job_id}",
        )

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task


@asynccontextmanager
async def heartbeat_for(db, *, job_id: int, interval_sec: float):
    hb = HeartbeatTask(db, job_id=job_id, interval_sec=interval_sec)
    hb.start()
    try:
        yield hb
    finally:
        await hb.stop()
```

Stage handlers wrap their main loop in `async with heartbeat_for(...)`.
For transcribe specifically, the heartbeat task is *also* started (not
just `tick_progress`) so a 60-second-long single-segment ffmpeg decode
doesn't trigger the reaper. The double-tick (progress UPDATE + heartbeat
UPDATE in the same window) is harmless: both update the same column to
the same value.

## 3. SQL — progress and heartbeat UPDATEs

`pipeline/src/maktaba_pipeline/db/jobs_progress.py`:

```python
_PROGRESS_SQL_PG = """
-- Architecture §7.6 semantics: processed_seconds is the absolute count
-- (segment.end - seek_from). The caller computes the value, not a delta,
-- so a resume after pause restarts the counter from zero relative to the
-- new seek_from. segments_completed remains additive because it counts
-- across the full lifetime of the job.
UPDATE processing_jobs
   SET processed_seconds        = $2,
       segments_completed       = segments_completed + $3,
       last_segment_end_sec     = COALESCE($4, last_segment_end_sec),
       realtime_factor          = COALESCE($5, realtime_factor),
       estimated_remaining_sec  = COALESCE($6, estimated_remaining_sec),
       progress_updated_at      = now(),
       last_heartbeat_at        = now()
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""


async def tick_progress(db, t: ProgressTick) -> None:
    row = await db.fetchrow(
        _PROGRESS_SQL_PG, t.job_id,
        t.processed_seconds, t.segments_completed_delta,
        t.last_segment_end_sec, t.realtime_factor,
        t.estimated_remaining_sec,
    )
    if row is None:
        # The row moved to paused/cancelled/done while we were computing
        # the delta — the worker's per-segment cancel check (Story 6.4)
        # will see this on the next iteration. We don't raise.
        return


_HEARTBEAT_SQL_PG = """
UPDATE processing_jobs
   SET last_heartbeat_at = now()
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id
"""


async def tick_heartbeat(db, *, job_id: int) -> None:
    await db.fetchrow(_HEARTBEAT_SQL_PG, job_id)
```

The `state IN (...)` predicate stops a stale tick from a worker that
hasn't observed a force-pause yet from accidentally bumping
`last_heartbeat_at` on a `paused` row (which would defeat Story 6.6's
reaper math). It also avoids an UPDATE on terminal rows — silent no-op,
no error.

### 3.1 Notify trigger — Postgres

`shared/db/migrations/0028_jobs_progress_notify.sql`:

```sql
-- +goose Up
-- +goose StatementBegin

-- Progress payload mirrors architecture §7.10 byte-for-byte.
CREATE OR REPLACE FUNCTION processing_jobs_notify_progress() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.progress_updated_at IS DISTINCT FROM OLD.progress_updated_at THEN
        PERFORM pg_notify(
            'jobs.progress',
            json_build_object(
                'id',                       NEW.id,
                'video_id',                 NEW.video_id,
                'stage',                    NEW.stage,
                'state',                    NEW.state,
                'last_segment_end_sec',     NEW.last_segment_end_sec,
                'processed_seconds',        NEW.processed_seconds,
                'total_duration_seconds',   NEW.total_duration_seconds,
                'segments_completed',       NEW.segments_completed,
                'realtime_factor',          NEW.realtime_factor,
                'estimated_remaining_sec',  NEW.estimated_remaining_sec,
                'updated_at',               NEW.progress_updated_at
            )::text
        );
    ELSIF NEW.last_heartbeat_at IS DISTINCT FROM OLD.last_heartbeat_at THEN
        PERFORM pg_notify(
            'jobs.heartbeat',
            json_build_object(
                'id',                NEW.id,
                'stage',             NEW.stage,
                'last_heartbeat_at', NEW.last_heartbeat_at
            )::text
        );
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER processing_jobs_notify_progress_trg
    AFTER UPDATE OF progress_updated_at, last_heartbeat_at
        ON processing_jobs
    FOR EACH ROW
    EXECUTE FUNCTION processing_jobs_notify_progress();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS processing_jobs_notify_progress_trg ON processing_jobs;
DROP FUNCTION IF EXISTS processing_jobs_notify_progress();
-- +goose StatementEnd
```

The `ELSIF` ensures a single UPDATE that bumps both columns fires
exactly one notify on `jobs.progress` (not also `jobs.heartbeat`).

### 3.2 SQLite path — application-level publish

SQLite has no triggers that can call `pg_notify`. The Python helpers
publish on the in-process `PubsubBus` after the UPDATE commits:

```python
from .pubsub import JOBS_PROGRESS, JOBS_HEARTBEAT, get_bus

async def tick_progress(db, t: ProgressTick) -> None:
    row = await db.fetchrow(_PROGRESS_SQL, ...)
    if row is None:
        return
    if db.dialect == "sqlite":
        # Re-fetch the canonical payload columns after commit.
        full = await db.fetchrow(_PAYLOAD_SQL, t.job_id)
        get_bus().publish(JOBS_PROGRESS, _build_progress_payload(full))


async def tick_heartbeat(db, *, job_id: int) -> None:
    row = await db.fetchrow(_HEARTBEAT_SQL, job_id)
    if row is None:
        return
    if db.dialect == "sqlite":
        get_bus().publish(JOBS_HEARTBEAT, {
            "id":                job_id,
            "stage":             None,    # filled by re-query if needed
            "last_heartbeat_at": _now_iso(),
        })
```

The SQLite payload-builder mirrors the Postgres trigger output exactly
(same key order, same casts) so the API consumer never needs to branch
on dialect.

## 4. Stage integration — illustrative

For non-transcribe stages, the heartbeat task is the only liveness
mechanism. Probe is the canonical short-job example:

```python
# pipeline/src/maktaba_pipeline/pipeline/stages/probe.py (illustrative)
async def run_probe(ctx, job, video):
    async with heartbeat_for(ctx.db, job_id=job.id,
                             interval_sec=ctx.cfg.heartbeat_sec):
        # The actual probe call (~2-10 s on a typical lecture):
        result = await ffprobe.run(video.path)
        await ctx.db.execute(
            "UPDATE videos SET probe_result = $1 WHERE id = $2",
            json.dumps(result), video.id,
        )
        # Mark the job done in a single UPDATE that also fires nothing
        # (the trigger only fires on progress_updated_at / last_heartbeat_at):
        await mark_done(ctx.db, job.id)
```

For transcribe (per architecture §7.6):

```python
async def run_transcribe(ctx, job, video):
    async with heartbeat_for(ctx.db, job_id=job.id,
                             interval_sec=ctx.cfg.heartbeat_sec):
        # ... per architecture §7.6 ...
        async for segment in stt.transcribe_stream(audio):
            async with ctx.db.begin() as tx:
                await tx.execute(insert_segment_stmt, ...)
                await tick_progress(tx, ProgressTick(
                    job_id=job.id,
                    # Architecture §7.6: processed_seconds = segment.end - seek_from.
                    processed_seconds=segment.end - seek_from,
                    segments_completed_delta=1,
                    last_segment_end_sec=segment.end,
                    realtime_factor=ewma(...),
                    estimated_remaining_sec=...,
                ))
            ...
```

The heartbeat task runs concurrently with the per-segment loop. When a
single segment takes 60 s of wall-time (slow ffmpeg decode, `whisper-cpu`
on a long passage), the 5 s heartbeat ticks keep the row alive and the
reaper away. If a segment commits inside that window, the progress tick
collides with a heartbeat tick → both update `last_heartbeat_at` to
nearly-equal `now()` values; the trigger picks the latest and fires
`jobs.progress`, not a duplicate `jobs.heartbeat`.

## 5. Test plan

### 5.1 Progress UPDATE tests (`pipeline/tests/db/test_jobs_progress.py`)

| Test | What it pins |
|---|---|
| `test_tick_progress_advances_counters` | Insert pending row; mark running; call `tick_progress(delta=10s, segments=1, end=10s)` → row's `processed_seconds=10`, `segments_completed=1`, `last_segment_end_sec=10`. |
| `test_tick_progress_bumps_heartbeat` | After tick_progress, `last_heartbeat_at == progress_updated_at == now()` (within 50 ms). |
| `test_tick_progress_emits_jobs_progress_notify` | LISTEN `jobs.progress`; one tick → one notify whose payload matches the §7.10 schema byte-for-byte (compare via `json.loads` and `assert ==` against the expected dict). |
| `test_tick_progress_does_not_emit_jobs_heartbeat` | Same as above but also LISTEN `jobs.heartbeat` → zero notifies. |
| `test_tick_progress_no_op_on_terminal_row` | Insert a `done` row; tick_progress → no UPDATE rows, no notify. |
| `test_tick_progress_no_op_on_paused_row` | Insert a `paused` row; tick_progress → no UPDATE, no notify. |
| `test_tick_heartbeat_only_bumps_heartbeat` | UPDATE row to `running`; call `tick_heartbeat` → `last_heartbeat_at` advanced; `progress_updated_at` unchanged. |
| `test_tick_heartbeat_emits_jobs_heartbeat_notify` | LISTEN `jobs.heartbeat`; one tick → one notify with `{id, stage, last_heartbeat_at}` payload. |
| `test_tick_heartbeat_does_not_emit_jobs_progress` | LISTEN `jobs.progress`; one tick → zero notifies. |
| `test_progress_payload_shape_matches_arch_7_10` | Capture the JSON; deep-equal against an expected fixture dict; lock the order and key set. Treats this as a contract test for the WS consumer. |

### 5.2 Heartbeat task tests (`pipeline/tests/pipeline/test_heartbeat.py`)

| Test | What it pins |
|---|---|
| `test_heartbeat_ticks_at_interval` | Start `HeartbeatTask(interval=0.1s)`; sleep 0.35 s; stop → `last_heartbeat_at` advanced ≥ 3 times. |
| `test_heartbeat_stop_is_prompt` | After `stop()`, no further ticks fire. |
| `test_heartbeat_for_context_cleans_up_on_exception` | Stage raises inside the `async with`; heartbeat task cancelled cleanly. |
| `test_heartbeat_no_op_when_row_terminal` | Mark row done; tick fires; UPDATE returns no rows; no notify; no exception. |
| `test_heartbeat_skipped_during_paused_state` | Mark row paused mid-loop; the next tick UPDATE is a no-op (state predicate filters); the worker's pause-observation path (Story 6.4) handles the exit. |

### 5.3 Lint test (`pipeline/tests/lint/test_no_singular_channel_names.py`)

```python
import pathlib
import re

import pytest

ROOTS = [
    pathlib.Path(__file__).resolve().parents[3] / "src",
    pathlib.Path(__file__).resolve().parents[3] / "tests",
]

FORBIDDEN = [
    "job.progress", "job.heartbeat", "job.pending",
    "job.reaped", "job.force_pause",
]


@pytest.mark.parametrize("name", FORBIDDEN)
def test_no_legacy_singular_channel_names(name: str):
    pat = re.compile(rf'["\']{re.escape(name)}["\']')
    hits: list[str] = []
    for root in ROOTS:
        for path in root.rglob("*.py"):
            for i, line in enumerate(path.read_text().splitlines(), 1):
                # Skip lines flagged with `# legacy-name-ok` for the few
                # places (e.g. README parsing) that genuinely need the string.
                if "# legacy-name-ok" in line:
                    continue
                if pat.search(line):
                    hits.append(f"{path}:{i}: {line.strip()}")
    assert not hits, f"Retired channel name {name!r} found:\n" + "\n".join(hits)
```

The Go side gets a parallel test in `api/internal/jobs/lint_test.go`
that walks `*.go` files under `api/` and the streaming service. Both
tests run in CI on every PR.

## 6. Edge cases — handling table

| Case | Behaviour | Where it's pinned |
|---|---|---|
| Stage finishes faster than `heartbeat_sec` | The completion UPDATE flips state to `done`; the heartbeat task's next tick is a no-op (state predicate filters); the heartbeat task is then cancelled by the `heartbeat_for` context exit. | `test_heartbeat_no_op_when_row_terminal` |
| Single segment longer than `heartbeat_sec` (e.g., 60 s ffmpeg decode) | The heartbeat task ticks `last_heartbeat_at` independently of the segment loop; reaper does not pick up the row. | `test_heartbeat_ticks_at_interval` (synthetic; the realistic 60 s scenario is covered by Epic 3 Story 3.6's plan) |
| Progress UPDATE collides with heartbeat UPDATE in the same millisecond | Both touch `last_heartbeat_at`; one wins per row-level lock. The trigger sees `progress_updated_at IS DISTINCT FROM OLD` for the progress UPDATE (fires `jobs.progress`) and `last_heartbeat_at IS DISTINCT FROM OLD` for the heartbeat UPDATE (fires `jobs.heartbeat`). The reaper sees one `jobs.heartbeat`; the UI sees one `jobs.progress`. | Story 6.6 reaper test asserts no double-reap; this story's `test_tick_progress_does_not_emit_jobs_heartbeat` asserts the trigger's branching. |
| LISTEN consumer (the API) drops mid-stream | Postgres queues notifies for delivery on reconnect (within a bounded buffer); on overflow, the consumer reconciles via `SELECT * FROM processing_jobs WHERE state IN (running, resuming) AND progress_updated_at > $last_seen`. The reconciliation lives in the API (Epic 2 Story 2.5), not here. | API-side; this story owns the notify fire only. |
| Worker pauses mid-segment | The progress tick wraps the pause check (architecture §7.6); after the segment commits, `mark_paused` runs in a separate transaction that flips state to `paused`, sets `paused_at_sec = last_segment_end_sec`. The next heartbeat tick is a no-op (state filter). | `test_heartbeat_skipped_during_paused_state` |
| `processed_seconds` overflow | REAL field; saturates at FP-precision around 16M seconds (≈ 6 months of audio in one job). Not a real risk; a single video maxes at hours. | Not separately tested. |
| Negative `processed_seconds` (clock skew or backend bug) | Allowed by the SQL; the column type accepts it. The CHECK only constrains `last_segment_end_sec`. We do not defend against negative deltas; flag in code review. | Documented in `ProgressTick` docstring. |
| `realtime_factor` smoothing across a paused/resumed seam | The EWMA value carries forward in the row; resume continues with the prior smoothing. | Not a test; emergent property of the schema. |

## 7. Performance analysis

### 7.1 UPDATE cost

A bare progress UPDATE touches one row by primary key, modifies five
fields, fires the trigger:

| Cost | Postgres warm | SQLite warm |
|---|---|---|
| Row UPDATE by PK | ~0.05 ms | ~0.1 ms |
| Trigger fire + `pg_notify` | ~0.2 ms | n/a |
| Async fanout to N listeners | O(N) bytes ~ free for small N | n/a |

A transcribe stage producing one segment per second of audio at 0.3×
realtime → ~3 progress ticks/s/job. Two parallel transcribe jobs →
~6 ticks/s. The per-tick cost (~0.3 ms) means progress UPDATEs consume
< 0.2% of one CPU core. Heartbeat-only ticks at 0.2 Hz are negligible.

### 7.2 Notify storm

Worst case: a fast STT backend on a short clip emits 100 segments in
1 s; 100 progress UPDATEs → 100 notifies. The API debounces to 1 Hz
per visible job (architecture §7.10), so the WS fanout collapses to
one outbound message per visible job per second. The Postgres notify
buffer (8 KB per backend, default) trivially absorbs 100 small notifies.

## 8. Dependencies

| Dep | Version | Why this one |
|---|---|---|
| `asyncpg` | already pinned | LISTEN/NOTIFY support. |
| `aiosqlite` | already pinned | SQLite tests. |
| No new deps | — | Triggers and SQL only. |

## 9. Acceptance checklist

**Migration**
- [ ] `0028_jobs_progress_notify.sql` applies cleanly; trigger exists; `goose down` reverts.
- [ ] `progress_updated_at IS DISTINCT FROM OLD.progress_updated_at` branch fires `jobs.progress`; the heartbeat-only branch fires `jobs.heartbeat`.

**Code**
- [ ] `tick_progress(db, ProgressTick)` is one UPDATE that bumps progress + heartbeat in the same statement.
- [ ] `tick_heartbeat(db, *, job_id)` is one UPDATE that bumps only `last_heartbeat_at`.
- [ ] `HeartbeatTask` and the `heartbeat_for` async-context wrapper are usable from any stage handler with a one-liner.
- [ ] `WorkerConfig.heartbeat_sec` defaults to **5**; the README's heartbeat-cadence row links here.

**Behaviour (story acceptance criteria)**
- [ ] AC: progress UPDATE doubles as a heartbeat (same `now()` on both columns).
- [ ] AC: pure heartbeat UPDATEs fire `jobs.heartbeat`, not `jobs.progress`.
- [ ] AC: progress payload matches architecture §7.10 byte-for-byte.
- [ ] AC: the lint test asserts zero hits on the retired singular channel names across `pipeline/` and `api/`.

**Performance**
- [ ] Progress UPDATE p95 < 1 ms warm against a 10K-row table.
- [ ] Heartbeat-only UPDATE p95 < 0.5 ms warm.

**Docs**
- [ ] `specs/epics/06-job-queue/README.md` ticks story 6.3.
- [ ] The notify trigger function has a SQL comment block referencing this plan and architecture §7.10.
