"""The worker's watchdog (pipeline/watchdog.py): a worker that cannot work ends with 75,
so that whatever runs it starts it again (2026-11-13: one stayed up for hours with the
database restarted under it, dispatching nothing)."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.cluster.exitcodes import EX_TEMPFAIL
from maktaba_pipeline.log.liveness import Progress
from maktaba_pipeline.pipeline.watchdog import Watchdog, database_watch


class _Clock:
    def __init__(self) -> None:
        self.t = 1000.0

    def __call__(self) -> float:
        return self.t


def _dog(shutdown: asyncio.Event, exits: list[int], **kw: float) -> Watchdog:
    return Watchdog(shutdown, exit_now=exits.append, **kw)


@pytest.mark.asyncio
async def test_a_core_loop_that_dies_ends_the_worker_with_75() -> None:
    shutdown = asyncio.Event()
    exits: list[int] = []
    dog = _dog(shutdown, exits)

    async def dies() -> None:
        raise RuntimeError("boom")

    task = dog.guard(asyncio.ensure_future(dies()), "worker-hb")
    with pytest.raises(RuntimeError):
        await task
    await asyncio.sleep(0)
    assert shutdown.is_set(), "the worker stops (its jobs get their drain)"
    assert dog.exit_code == EX_TEMPFAIL == 75
    assert dog.reason == "the worker-hb loop died: RuntimeError: boom"
    assert exits == [], "the graceful stop comes first"


@pytest.mark.asyncio
async def test_a_core_loop_that_returns_or_is_cancelled_ends_the_worker() -> None:
    for end in ("returns", "cancelled"):
        shutdown = asyncio.Event()
        dog = _dog(shutdown, [])

        async def loop(end: str = end) -> None:
            if end == "cancelled":
                await asyncio.sleep(3600)

        task = dog.guard(asyncio.ensure_future(loop()), "reaper")
        await asyncio.sleep(0)
        if end == "cancelled":
            task.cancel()
        with pytest.raises(asyncio.CancelledError) if end == "cancelled" else _nothing():
            await task
        await asyncio.sleep(0)
        assert shutdown.is_set() and dog.exit_code == 75, end
        assert dog.reason is not None and "reaper" in dog.reason


class _nothing:
    def __enter__(self) -> None:
        return None

    def __exit__(self, *_exc: object) -> None:
        return None


@pytest.mark.asyncio
async def test_a_loop_that_ends_at_shutdown_is_a_clean_stop() -> None:
    shutdown = asyncio.Event()
    dog = _dog(shutdown, [])

    async def loop() -> None:
        await shutdown.wait()

    task = dog.guard(asyncio.ensure_future(loop()), "components")
    shutdown.set()
    await task
    await asyncio.sleep(0)
    assert dog.reason is None and dog.exit_code == 0


@pytest.mark.asyncio
async def test_progress_that_stays_still_past_its_window_and_the_grace_ends_the_worker() -> None:
    clock = _Clock()
    shutdown = asyncio.Event()
    dog = _dog(shutdown, [], grace_sec=30)
    passes = dog.watch(Progress("dispatch", "no pass of the claim loop", 300, clock=clock))
    runner = asyncio.ensure_future(dog.run(interval=0.01))

    clock.t += 329  # stale (health says so), still inside the grace
    await asyncio.sleep(0.05)
    assert not shutdown.is_set() and not passes.healthy()

    passes.mark()  # it moved again: nothing happens
    clock.t += 200
    await asyncio.sleep(0.05)
    assert not shutdown.is_set()

    clock.t += 131
    await asyncio.wait_for(runner, timeout=5)
    assert shutdown.is_set() and dog.exit_code == 75
    assert dog.reason == "no pass of the claim loop for 331s (stale after 300s)"


@pytest.mark.asyncio
async def test_a_worker_that_cannot_stop_is_ended_outright() -> None:
    shutdown = asyncio.Event()
    exits: list[int] = []
    dog = _dog(shutdown, exits, hard_exit_sec=0.05)
    dog.fail("the claim loop died: RuntimeError: x")
    dog.fail("a second reason changes nothing")
    assert dog.reason == "the claim loop died: RuntimeError: x"
    await asyncio.sleep(0.2)  # the shutdown never completes here
    assert exits == [75]


@pytest.mark.asyncio
async def test_a_worker_that_stopped_in_time_is_not_ended_outright() -> None:
    exits: list[int] = []
    dog = _dog(asyncio.Event(), exits, hard_exit_sec=0.05)
    dog.fail("the reaper loop returned")
    dog.disarm()
    await asyncio.sleep(0.2)
    assert exits == [] and dog.exit_code == 75


@pytest.mark.asyncio
async def test_a_stop_that_was_asked_for_is_not_a_failure() -> None:
    shutdown = asyncio.Event()
    shutdown.set()
    exits: list[int] = []
    dog = _dog(shutdown, exits, hard_exit_sec=0.01)
    dog.fail("too late to matter")
    await asyncio.sleep(0.05)
    assert dog.reason is None and dog.exit_code == 0 and exits == []


class _DB:
    def __init__(self) -> None:
        self.mode = "ok"
        self.asked = 0

    async def fetchval(self, _sql: str) -> int:
        self.asked += 1
        if self.mode == "refused":
            raise ConnectionRefusedError("refused")
        if self.mode == "silent":
            await asyncio.sleep(3600)
        return 1


@pytest.mark.asyncio
async def test_the_database_watch_marks_each_answer_and_none_while_it_is_gone() -> None:
    clock = _Clock()
    db = _DB()
    answers = Progress("database", "no answer from the database", 60, clock=clock)
    stop = asyncio.Event()
    task = asyncio.ensure_future(database_watch(db, answers, stop, interval=0.01, timeout=0.02))

    async def asked(n: int) -> None:
        target = db.asked + n
        for _ in range(500):
            if db.asked >= target:
                return
            await asyncio.sleep(0.01)
        raise AssertionError("the watch stopped asking")

    await asked(2)
    for mode in ("refused", "silent"):  # an error, and no answer at all
        db.mode = mode
        await asked(2)
        clock.t += 61
        await asked(2)
        assert not answers.healthy(), mode
        db.mode = "ok"
        await asked(2)
        assert answers.healthy(), "the first answer after the outage"
    assert not task.done()
    stop.set()
    await asyncio.wait_for(task, timeout=5)
