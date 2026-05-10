"""Behaviour tests for :class:`maktaba_pipeline.pipeline.runner.ClaimLoop`.

The loop's contract is:

- Wakeup → claim every eligible row up to per-stage capacity → dispatch.
- Notify wakes the loop within one tick.
- Successful claims reset the back-off; consecutive empty drains
  exponentially extend the wakeup poll cadence.
- :attr:`shutdown_event` set → loop exits after draining in-flight.
- A dispatch exception releases the semaphore and the loop keeps going.

The tests use :class:`PollOnlyWakeup` for deterministic timing and
the :class:`PubsubWakeup` only for the notify-wakes-loop test. They
re-use the ``FakeDB`` from ``tests/db/test_jobs_claim.py`` indirectly
by re-implementing a thin variant here so the two test files stay
independent.

Note: not marked ``unit`` — see the same caveat in
``tests/db/test_jobs_claim.py`` (asyncio's socketpair vs. the netguard).
"""

from __future__ import annotations

import asyncio
import contextlib
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.db.jobs import Job, Stage
from maktaba_pipeline.db.jobs_claim import (
    _CLAIM_SQL_PG,
    _reset_sqlite_claim_lock,
)
from maktaba_pipeline.db.pubsub import JOBS_NEW, get_bus, reset_bus
from maktaba_pipeline.pipeline.runner import (
    ClaimLoop,
    WorkerConfig,
    default_worker_id,
    install_signal_handlers,
)
from maktaba_pipeline.pipeline.wakeup import (
    PollOnlyWakeup,
    PubsubWakeup,
    WakeupSource,
)


@dataclass
class _Row:
    id: int
    video_id: UUID
    stage: str
    state: str = "pending"
    priority: int = 100
    attempts: int = 0

    def as_full_row(self) -> dict[str, Any]:
        now = datetime.now(UTC)
        return {
            "id": self.id,
            "video_id": self.video_id,
            "stage": self.stage,
            "state": self.state,
            "priority": self.priority,
            "attempts": self.attempts,
            "max_attempts": 3,
            "claimed_by": "w",
            "claimed_at": now,
            "last_heartbeat_at": now,
            "not_before": None,
            "error": None,
            "total_duration_seconds": None,
            "processed_seconds": 0.0,
            "segments_completed": 0,
            "last_segment_end_sec": 0.0,
            "estimated_remaining_sec": None,
            "realtime_factor": None,
            "progress_updated_at": None,
            "pause_requested": False,
            "cancel_requested": False,
            "paused_at": None,
            "paused_at_sec": None,
            "paused_reason": None,
            "resumed_at": None,
            "resume_count": 0,
            "metrics": None,
            "payload": None,
            "created_at": now,
            "finished_at": None,
        }


@dataclass
class FakeDB:
    """Minimal claim-aware fake. Only :func:`claim_one` is exercised."""

    dialect: str = "postgres"
    rows: list[_Row] = field(default_factory=list)
    _next_id: int = 1
    _lock: asyncio.Lock | None = None

    def add(self, stage: Stage, *, priority: int = 100) -> _Row:
        row = _Row(id=self._next_id, video_id=uuid4(), stage=stage.value, priority=priority)
        self.rows.append(row)
        self._next_id += 1
        return row

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    def _get_lock(self) -> asyncio.Lock:
        if self._lock is None:
            self._lock = asyncio.Lock()
        return self._lock

    async def fetchrow(self, sql: str, *args: Any) -> dict[str, Any] | None:
        if sql.strip() != _CLAIM_SQL_PG.strip():
            raise AssertionError(f"unexpected SQL: {sql!r}")
        worker_id, stages = args
        async with self._get_lock():
            eligible = [r for r in self.rows if r.state == "pending" and r.stage in list(stages)]
            if not eligible:
                return None
            eligible.sort(key=lambda r: (r.priority, r.id))
            row = eligible[0]
            row.state = "claimed"
            row.attempts += 1
            full = row.as_full_row()
            full["claimed_by"] = worker_id
            return full


class _ManualWakeup:
    """A WakeupSource we drive by hand from the test."""

    poll_sec = 60.0  # never trips on its own

    def __init__(self) -> None:
        self._queue: asyncio.Queue[None] = asyncio.Queue()
        self._stop = asyncio.Event()

    def fire(self) -> None:
        self._queue.put_nowait(None)

    async def signals(self) -> Any:
        while not self._stop.is_set():
            getter = asyncio.create_task(self._queue.get())
            stopper = asyncio.create_task(self._stop.wait())
            done, pending = await asyncio.wait(
                {getter, stopper},
                return_when=asyncio.FIRST_COMPLETED,
            )
            for t in pending:
                t.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await t
            if self._stop.is_set():
                return
            yield None

    async def aclose(self) -> None:
        self._stop.set()


@pytest.fixture(autouse=True)
def _reset_state() -> None:
    _reset_sqlite_claim_lock()
    reset_bus()


def _make_cfg(stages: tuple[Stage, ...] = (Stage.PROBE,), **kwargs: Any) -> WorkerConfig:
    defaults: dict[str, Any] = {
        "supported_stages": stages,
        "worker_id": "w-test",
        "claim_poll_sec": 0.05,
        "claim_poll_max_sec": 0.2,
    }
    defaults.update(kwargs)
    return WorkerConfig(**defaults)


@pytest.mark.asyncio
async def test_loop_dispatches_one_job() -> None:
    db = FakeDB()
    db.add(Stage.PROBE)
    cfg = _make_cfg()
    seen: list[int] = []

    async def dispatch(job: Job) -> None:
        seen.append(job.id)

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    await asyncio.sleep(0.05)
    loop.shutdown_event.set()
    wakeup.fire()  # nudge so signals() returns
    await asyncio.wait_for(task, timeout=2.0)

    assert seen == [1]


@pytest.mark.asyncio
async def test_loop_drains_batch_on_one_wakeup() -> None:
    """One wakeup should claim every eligible row, not just the first.

    With capacity == batch size, a single notify carries the whole
    batch — the production property that lets the scanner enqueue 100
    rows in one transaction and have the loop drain them in one tick.
    """
    db = FakeDB()
    for _ in range(5):
        db.add(Stage.PROBE)
    cfg = _make_cfg()
    semaphores = {Stage.PROBE: asyncio.Semaphore(5)}
    seen: list[int] = []
    seen_lock = asyncio.Lock()

    async def dispatch(job: Job) -> None:
        async with seen_lock:
            seen.append(job.id)

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, semaphores=semaphores, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    await asyncio.sleep(0.1)

    loop.shutdown_event.set()
    wakeup.fire()
    await asyncio.wait_for(task, timeout=2.0)

    assert sorted(seen) == [1, 2, 3, 4, 5]


@pytest.mark.asyncio
async def test_loop_respects_per_stage_capacity() -> None:
    """A wakeup must not claim more rows than the semaphore allows.

    With ``Semaphore(2)`` and a dispatch that blocks until released,
    the loop claims exactly 2 jobs per wakeup tick. Subsequent ticks
    (driven by the production poll fallback or, here, repeated
    manual fires) drain the rest two-at-a-time.
    """
    db = FakeDB()
    for _ in range(5):
        db.add(Stage.PROBE)
    cfg = _make_cfg()
    semaphores = {Stage.PROBE: asyncio.Semaphore(2)}
    release = asyncio.Event()

    seen: list[int] = []
    seen_lock = asyncio.Lock()

    async def dispatch(job: Job) -> None:
        async with seen_lock:
            seen.append(job.id)
        await release.wait()

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, semaphores=semaphores, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    # Two jobs claimed; further claims block on the semaphore.
    await asyncio.sleep(0.1)
    async with seen_lock:
        assert sorted(seen) == [1, 2]
    # Capacity exhausted — sem._value should be 0.
    assert semaphores[Stage.PROBE]._value == 0  # noqa: SLF001

    # Release dispatches so the semaphore opens back up. Then drive
    # the loop with repeated wakeups (the production analogue is the
    # poll-timer arm of the wakeup source) until everything drains.
    release.set()
    for _ in range(5):
        wakeup.fire()
        await asyncio.sleep(0.05)

    loop.shutdown_event.set()
    wakeup.fire()
    await asyncio.wait_for(task, timeout=2.0)
    assert sorted(seen) == [1, 2, 3, 4, 5]


@pytest.mark.asyncio
async def test_loop_releases_semaphore_on_dispatch_exception() -> None:
    db = FakeDB()
    db.add(Stage.PROBE)
    db.add(Stage.PROBE)
    cfg = _make_cfg()
    semaphores = {Stage.PROBE: asyncio.Semaphore(1)}

    seen: list[int] = []
    seen_lock = asyncio.Lock()

    async def dispatch(job: Job) -> None:
        async with seen_lock:
            seen.append(job.id)
        if job.id == 1:
            raise RuntimeError("simulated stage failure")

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, semaphores=semaphores, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    await asyncio.sleep(0.1)
    wakeup.fire()
    await asyncio.sleep(0.1)

    loop.shutdown_event.set()
    wakeup.fire()
    await asyncio.wait_for(task, timeout=2.0)

    # The exception didn't wedge the semaphore — both jobs ran.
    assert sorted(seen) == [1, 2]
    assert semaphores[Stage.PROBE]._value == 1  # noqa: SLF001


@pytest.mark.asyncio
async def test_shutdown_event_breaks_loop() -> None:
    db = FakeDB()
    cfg = _make_cfg(claim_poll_sec=0.05, claim_poll_max_sec=0.05)
    wakeup = PollOnlyWakeup(poll_sec=0.05)

    async def dispatch(_: Job) -> None:  # pragma: no cover — empty queue
        pass

    loop = ClaimLoop(db, cfg, dispatch, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    await asyncio.sleep(0.1)
    loop.shutdown_event.set()
    await asyncio.wait_for(task, timeout=1.0)


@pytest.mark.asyncio
async def test_pubsub_wakeup_wakes_loop_within_poll_window() -> None:
    """Publishing on JOBS_NEW should wake the loop sooner than the poll fallback."""
    db = FakeDB()
    cfg = _make_cfg(claim_poll_sec=2.0, claim_poll_max_sec=2.0)

    seen = asyncio.Event()

    async def dispatch(_: Job) -> None:
        seen.set()

    wakeup = PubsubWakeup(poll_sec=2.0)  # safety net well above the test deadline
    loop = ClaimLoop(db, cfg, dispatch, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    # Let the subscriber attach before publishing.
    await asyncio.sleep(0.05)

    db.add(Stage.PROBE)
    get_bus().publish(JOBS_NEW, {"id": 1, "stage": "probe"})

    # If the wakeup works, the dispatch fires well within 1s; if it
    # doesn't, the safety-net poll wouldn't fire until 2s and we'd
    # time out.
    await asyncio.wait_for(seen.wait(), timeout=1.0)

    loop.shutdown_event.set()
    await wakeup.aclose()
    await asyncio.wait_for(task, timeout=1.0)


@pytest.mark.asyncio
async def test_loop_backs_off_when_queue_empty_then_resets_on_claim() -> None:
    db = FakeDB()
    cfg = _make_cfg(claim_poll_sec=0.05, claim_poll_max_sec=0.4)
    wakeup = PollOnlyWakeup(poll_sec=0.05)

    poll_at_dispatch: list[float] = []
    seen = asyncio.Event()

    async def dispatch(_: Job) -> None:
        # Sample inside dispatch — by this point the loop has already
        # finished its drain and reset poll_sec for the next iteration.
        poll_at_dispatch.append(wakeup.poll_sec)
        seen.set()

    loop = ClaimLoop(db, cfg, dispatch, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    # Several empty drains drive poll_sec up exponentially.
    await asyncio.sleep(0.35)
    backed_off = wakeup.poll_sec
    assert backed_off > cfg.claim_poll_sec, (
        f"expected backoff > {cfg.claim_poll_sec}, got {backed_off}"
    )

    # Enqueue one row; the next wakeup claims it and resets backoff.
    db.add(Stage.PROBE)
    await asyncio.wait_for(seen.wait(), timeout=2.0)

    loop.shutdown_event.set()
    await wakeup.aclose()
    await asyncio.wait_for(task, timeout=1.0)

    assert poll_at_dispatch == [cfg.claim_poll_sec]


@pytest.mark.asyncio
async def test_loop_drains_in_flight_on_shutdown() -> None:
    db = FakeDB()
    db.add(Stage.PROBE)
    cfg = _make_cfg()
    semaphores = {Stage.PROBE: asyncio.Semaphore(1)}

    finished = asyncio.Event()

    async def dispatch(_: Job) -> None:
        await asyncio.sleep(0.1)
        finished.set()

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, semaphores=semaphores, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    await asyncio.sleep(0.05)  # dispatch is in flight, sleeping

    loop.shutdown_event.set()
    wakeup.fire()
    await asyncio.wait_for(task, timeout=2.0)

    # The in-flight dispatch finished before the loop returned.
    assert finished.is_set()


@pytest.mark.asyncio
async def test_loop_filters_stages_per_worker() -> None:
    db = FakeDB()
    db.add(Stage.INDEX, priority=1)  # would win on priority alone
    db.add(Stage.TRANSCRIBE, priority=100)
    cfg = _make_cfg(stages=(Stage.TRANSCRIBE,))
    semaphores = {Stage.TRANSCRIBE: asyncio.Semaphore(1)}

    seen: list[Stage] = []

    async def dispatch(job: Job) -> None:
        seen.append(job.stage)

    wakeup = _ManualWakeup()
    loop = ClaimLoop(db, cfg, dispatch, semaphores=semaphores, wakeup=wakeup)
    task = asyncio.create_task(loop.run())

    wakeup.fire()
    await asyncio.sleep(0.1)

    loop.shutdown_event.set()
    wakeup.fire()
    await asyncio.wait_for(task, timeout=2.0)

    assert seen == [Stage.TRANSCRIBE]
    # The INDEX row was left alone for an INDEX-capable worker.
    assert any(r.stage == Stage.INDEX.value and r.state == "pending" for r in db.rows)


@pytest.mark.asyncio
async def test_install_signal_handlers_sets_event() -> None:
    import signal as _signal

    event = asyncio.Event()
    install_signal_handlers(event, loop=asyncio.get_running_loop())

    # Sending SIGTERM to ourselves should set the event.
    asyncio.get_running_loop().call_soon(_signal.raise_signal, _signal.SIGTERM)
    await asyncio.wait_for(event.wait(), timeout=1.0)
    assert event.is_set()


def test_worker_config_validates_stages() -> None:
    with pytest.raises(ValueError, match="supported_stages"):
        WorkerConfig(supported_stages=())


def test_worker_config_validates_poll_sec() -> None:
    with pytest.raises(ValueError, match="claim_poll_sec"):
        WorkerConfig(supported_stages=(Stage.PROBE,), claim_poll_sec=0)


def test_worker_config_validates_max_geq_min() -> None:
    with pytest.raises(ValueError, match="claim_poll_max_sec"):
        WorkerConfig(
            supported_stages=(Stage.PROBE,),
            claim_poll_sec=2.0,
            claim_poll_max_sec=1.0,
        )


def test_default_worker_id_format() -> None:
    wid = default_worker_id()
    parts = wid.split("/")
    assert len(parts) == 3
    assert parts[1].isdigit()
    assert len(parts[2]) == 8


def test_wakeup_source_protocol_satisfied_by_implementations() -> None:
    assert isinstance(PollOnlyWakeup(poll_sec=1.0), WakeupSource)
    assert isinstance(PubsubWakeup(poll_sec=1.0), WakeupSource)
