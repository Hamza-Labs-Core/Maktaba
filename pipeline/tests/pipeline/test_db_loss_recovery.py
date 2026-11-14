"""A worker whose database connections are dropped under it goes on by itself.

2026-11-13: the supervisor restarted the home's Postgres under the main worker; every
connection got "terminating connection due to administrator command". The worker stayed
up for hours, dispatched nothing and logged nothing. What held it: a statement its task
was cancelled in (a timeout) on a connection the server then dropped. asyncpg sends
ROLLBACK at the end of a transaction block, and gives a connection back, only once the
server acknowledged the cancelled statement, with no limit, and a dropped connection
never acknowledges: the task never left the block.

Here, with stand-ins for asyncpg (the same against a real server:
``test_db_loss_pg.py``): the transaction and the pool hand-back never wait on a
connection that cannot answer, the claim loop dispatches again once the database is
back, the worker's health says "stuck" when the loop stops passing, and a worker whose
core loop ended exits 75.
"""

from __future__ import annotations

import asyncio
import contextlib
import json
from collections.abc import AsyncIterator
from typing import Any

import pytest

from maktaba_pipeline import runtime
from maktaba_pipeline.cluster import exitcodes
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.db.jobs_state import mark_done
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.log import liveness
from maktaba_pipeline.log.http import build_health_response
from maktaba_pipeline.log.liveness import Progress
from maktaba_pipeline.pipeline import watchdog as watchdog_mod
from maktaba_pipeline.pipeline.runner import ClaimLoop, WorkerConfig
from maktaba_pipeline.pipeline.wakeup import PollOnlyWakeup
from maktaba_pipeline.runtime import Database, RuntimeConfig, _pg_transaction
from tests.engine.factory import make_library, make_video
from tests.engine.fixtures_db import db, pg_template, sqlite_template  # noqa: F401


class ConnectionDoesNotExistError(Exception):
    """asyncpg's, by name: the server ended the connection in the middle of a statement."""


# -- the transaction block -------------------------------------------------------------


class _Tx:
    def __init__(self, conn: _Conn) -> None:
        self.conn = conn

    async def start(self) -> None:
        self.conn.log.append("begin")

    async def commit(self) -> None:
        self.conn.log.append("commit")

    async def rollback(self) -> None:
        self.conn.log.append("rollback")
        if self.conn.never_answers:
            # as asyncpg after a cancel on a dropped connection: the statement waits for
            # an acknowledgement that never comes
            await asyncio.Event().wait()


class _Conn:
    def __init__(self) -> None:
        self.log: list[str] = []
        self.closed = False
        self.never_answers = False

    def transaction(self) -> _Tx:
        return _Tx(self)

    def is_closed(self) -> bool:
        return self.closed

    def terminate(self) -> None:
        self.closed = True
        self.log.append("terminate")


@pytest.mark.asyncio
async def test_a_block_that_ends_well_commits() -> None:
    conn = _Conn()
    async with _pg_transaction(conn):
        pass
    assert conn.log == ["begin", "commit"]


@pytest.mark.asyncio
async def test_a_block_that_fails_rolls_back_and_raises_its_own_error() -> None:
    conn = _Conn()
    with pytest.raises(ValueError, match="mine"):
        async with _pg_transaction(conn):
            raise ValueError("mine")
    assert conn.log == ["begin", "rollback"]


@pytest.mark.asyncio
async def test_a_cancelled_block_never_waits_for_a_rollback() -> None:
    """The hang of 2026-11-13: the ROLLBACK after a cancel never returned."""
    conn = _Conn()
    conn.never_answers = True

    async def work() -> None:
        async with _pg_transaction(conn):
            await asyncio.sleep(3600)  # a statement the database never answers

    with pytest.raises(TimeoutError):
        # the outer bound fails the test if the block hangs; the inner one is the
        # caller's own timeout (the worker heartbeat's, a stage's)
        await asyncio.wait_for(_timed(work(), 0.05), timeout=5)
    assert conn.log == ["begin", "terminate"], "the server rolls back what the session left"


async def _timed(coro: Any, timeout: float) -> None:
    await asyncio.wait_for(coro, timeout=timeout)


@pytest.mark.asyncio
async def test_a_block_whose_connection_was_dropped_raises_what_dropped_it() -> None:
    conn = _Conn()
    with pytest.raises(ConnectionDoesNotExistError):
        async with _pg_transaction(conn):
            conn.closed = True  # terminating connection due to administrator command
            raise ConnectionDoesNotExistError("connection was closed in the middle of operation")
    assert conn.log == ["begin"], "no statement on a closed connection"


@pytest.mark.asyncio
async def test_a_rollback_that_gets_no_answer_ends_the_connection(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(runtime, "PG_ROLLBACK_TIMEOUT_SEC", 0.05)
    conn = _Conn()
    conn.never_answers = True
    with pytest.raises(ValueError, match="mine"):
        await asyncio.wait_for(_fails_in_block(conn), timeout=5)
    assert conn.log == ["begin", "rollback", "terminate"]


async def _fails_in_block(conn: _Conn) -> None:
    async with _pg_transaction(conn):
        raise ValueError("mine")


# -- the pool --------------------------------------------------------------------------


class _Pool:
    def __init__(self) -> None:
        self.conn = _Conn()
        self.acquire_timeouts: list[float | None] = []
        self.release_timeouts: list[float | None] = []
        self.release_error: BaseException | None = None

    async def acquire(self, *, timeout: float | None = None) -> _Conn:
        self.acquire_timeouts.append(timeout)
        return self.conn

    async def release(self, conn: _Conn, *, timeout: float | None = None) -> None:
        self.release_timeouts.append(timeout)
        if self.release_error is not None:
            raise self.release_error


@pytest.mark.asyncio
async def test_a_pool_connection_is_taken_and_given_back_within_a_bound() -> None:
    pool = _Pool()
    database = Database(dialect="postgres", pool=pool)
    async with database.connection() as conn:
        assert conn is pool.conn
    # asyncpg waits without limit for a slot, and at the release for a cancelled
    # statement's acknowledgement, unless told otherwise
    assert pool.acquire_timeouts == [runtime.PG_ACQUIRE_TIMEOUT_SEC]
    assert pool.release_timeouts == [runtime.PG_RELEASE_TIMEOUT_SEC]


@pytest.mark.asyncio
async def test_a_release_that_cannot_finish_drops_the_connection_and_keeps_the_outcome() -> None:
    pool = _Pool()
    pool.release_error = TimeoutError()  # no acknowledgement within the bound
    database = Database(dialect="postgres", pool=pool)
    async with database.connection():
        result = "done"
    assert result == "done", "the block's work stands"
    assert pool.conn.log == ["terminate"], "the slot is free: the connection is ended"

    with pytest.raises(ValueError, match="mine"):
        async with database.connection():
            raise ValueError("mine")


@pytest.mark.asyncio
async def test_a_transaction_uses_the_bounded_connection() -> None:
    pool = _Pool()
    database = Database(dialect="postgres", pool=pool)
    async with database.transaction(), database.transaction():  # the inner: a savepoint
        pass
    assert pool.conn.log == ["begin", "begin", "commit", "commit"]
    assert pool.acquire_timeouts == [runtime.PG_ACQUIRE_TIMEOUT_SEC]
    assert pool.release_timeouts == [runtime.PG_RELEASE_TIMEOUT_SEC]


# -- the claim loop over a database that goes away and comes back ----------------------


class _Flaky:
    """The database, refusing every call while ``down`` (connections terminated, the
    server starting up) and never answering while ``silent``."""

    def __init__(self, real: Database) -> None:
        self._real = real
        self.dialect = real.dialect
        self.engine_hooks = real.engine_hooks
        self.down = False
        self.silent = False
        self.refused = 0

    async def _gate(self) -> None:
        if self.silent:
            await asyncio.Event().wait()
        if self.down:
            self.refused += 1
            raise ConnectionDoesNotExistError("connection was closed in the middle of operation")

    async def fetch(self, sql: str, *args: Any) -> Any:
        await self._gate()
        return await self._real.fetch(sql, *args)

    async def fetchrow(self, sql: str, *args: Any) -> Any:
        await self._gate()
        return await self._real.fetchrow(sql, *args)

    async def fetchval(self, sql: str, *args: Any) -> Any:
        await self._gate()
        return await self._real.fetchval(sql, *args)

    async def execute(self, sql: str, *args: Any) -> Any:
        await self._gate()
        return await self._real.execute(sql, *args)

    @contextlib.asynccontextmanager
    async def transaction(self) -> AsyncIterator[Any]:
        await self._gate()
        async with self._real.transaction() as conn:
            yield conn


def _loop_cfg() -> WorkerConfig:
    return WorkerConfig(
        supported_stages=("probe",),
        claim_poll_sec=0.01,
        claim_poll_max_sec=0.02,
        worker_id="w1",
        pool_mode=True,
        stage_pools={s.name: s.pool for s in GRAPH},
        dynamic_stages=lambda: ("probe",),
    )


async def _queue_probe(database: Database, lib: Any) -> None:
    video = await make_video(database, lib)
    await enqueue_job(database, subject_kind="video", subject_id=video, stage="probe")


async def _until(cond: Any, what: str) -> None:
    for _ in range(1000):
        if cond():
            return
        await asyncio.sleep(0.01)
    raise AssertionError(what)


@pytest.mark.asyncio
async def test_dispatch_resumes_once_the_database_is_back(db: Database) -> None:  # noqa: F811
    await sync_graph(db, GRAPH)
    lib = await make_library(db)
    flaky = _Flaky(db)
    done: list[int] = []
    passes = 0

    async def dispatch(job: Job) -> None:
        await mark_done(flaky, job_id=job.id)
        done.append(job.id)

    def passed() -> None:
        nonlocal passes
        passes += 1

    stop = asyncio.Event()
    loop = ClaimLoop(
        flaky,
        _loop_cfg(),
        dispatch,
        shutdown_event=stop,
        wakeup=PollOnlyWakeup(poll_sec=0.01),
        on_pass=passed,
    )
    task = asyncio.ensure_future(loop.run())
    try:
        await _queue_probe(db, lib)
        await _until(lambda: len(done) == 1, "a job is dispatched while the database answers")

        flaky.down = True  # the database is restarted under the worker
        await _queue_probe(db, lib)
        await _queue_probe(db, lib)
        before = passes
        await _until(lambda: flaky.refused >= 5, "each pass asks again")
        assert len(done) == 1 and not task.done(), "nothing runs, and the loop lives on"
        assert passes > before, "a failed pass is still a pass: the loop is not stuck"

        flaky.down = False  # and is back
        await _until(lambda: len(done) == 3, "the jobs queued meanwhile are dispatched")
    finally:
        stop.set()
        await asyncio.wait_for(task, timeout=10)


# -- health: "ok" means jobs can be dispatched -----------------------------------------


class _Clock:
    def __init__(self) -> None:
        self.t = 1000.0

    def __call__(self) -> float:
        return self.t


@pytest.mark.asyncio
async def test_health_turns_not_ok_when_the_claim_loop_stops_passing(db: Database) -> None:  # noqa: F811
    await sync_graph(db, GRAPH)
    flaky = _Flaky(db)
    clock = _Clock()
    beat = liveness.LoopHeartbeat(stale_sec=300, clock=clock)
    passes = Progress("dispatch", "no pass of the claim loop", 120, clock=clock)

    async def dispatch(job: Job) -> None:
        raise AssertionError("nothing is queued")

    stop = asyncio.Event()
    wake = PollOnlyWakeup(poll_sec=0.01)
    loop = ClaimLoop(
        flaky, _loop_cfg(), dispatch, shutdown_event=stop, wakeup=wake, on_pass=passes.mark
    )
    task = asyncio.ensure_future(loop.run())
    try:
        await asyncio.sleep(0.1)
        clock.t += 119
        assert build_health_response(beat, [passes])[0] == 200

        # the loop passes: time alone does not make it stale
        clock.t += 100
        await asyncio.sleep(0.1)
        assert build_health_response(beat, [passes])[0] == 200

        flaky.silent = True  # a claim the database never answers: the loop is stuck in it
        await asyncio.sleep(0.1)
        clock.t += 121
        beat.beat()  # the event loop itself is fine, as it was on 2026-11-13
        status, _ctype, body = build_health_response(beat, [passes])
        assert status == 503, "the supervisor restarts on anything outside 2xx"
        doc = json.loads(body)
        assert doc["status"] == "fail" and "event_loop" not in doc["checks"]
        assert doc["checks"]["dispatch"] == {
            "status": "fail",
            "reason": "no pass of the claim loop for 121s (stale after 120s)",
        }
    finally:
        task.cancel()
        with contextlib.suppress(asyncio.CancelledError):
            await task


def test_health_says_which_check_failed_and_recovers() -> None:
    clock = _Clock()
    beat = liveness.LoopHeartbeat(stale_sec=300, clock=clock)
    answers = Progress("database", "no answer from the database", 60, clock=clock)
    passes = Progress("dispatch", "no pass of the claim loop", 120, clock=clock)
    assert build_health_response(beat, [answers, passes])[0] == 200
    clock.t += 61
    status, _ctype, body = build_health_response(beat, [answers, passes])
    assert status == 503
    assert list(json.loads(body)["checks"]) == ["database"]
    answers.mark()
    assert build_health_response(beat, [answers, passes])[0] == 200


def test_the_registered_checks_are_the_ones_health_answers_for() -> None:
    clock = _Clock()
    liveness.clear_progress()
    try:
        p = liveness.watch_progress(Progress("dispatch", "no pass", 10, clock=clock))
        assert liveness.progress_checks() == [p]
        clock.t += 11
        assert build_health_response(None, liveness.progress_checks())[0] == 503
    finally:
        liveness.clear_progress()
    assert liveness.progress_checks() == []


# -- the worker exits when a core loop ended -------------------------------------------


def _cfg() -> RuntimeConfig:
    return RuntimeConfig(
        database_url="sqlite:///:memory:",
        stages=("probe",),
        engine=False,
        worker_id="w1",
        claim_poll_sec=0.01,
        claim_poll_max_sec=0.02,
        shutdown_drain_sec=0.1,
    )


@pytest.fixture
def worker(monkeypatch: pytest.MonkeyPatch) -> list[int]:
    """``runtime.run`` in a test: no signal handlers on the test's loop, and a forced
    exit is recorded, not taken."""
    forced: list[int] = []
    monkeypatch.setattr(runtime, "install_signal_handlers", lambda _event: None)
    monkeypatch.setattr(watchdog_mod, "_exit_now", forced.append)
    exitcodes.reset()
    liveness.clear_progress()
    return forced


@pytest.mark.asyncio
async def test_the_worker_exits_75_when_the_claim_loop_dies(
    db: Database,  # noqa: F811
    worker: list[int],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    async def dies(self: ClaimLoop) -> None:
        raise RuntimeError("the wakeup source broke")

    monkeypatch.setattr(ClaimLoop, "run", dies)
    code = await asyncio.wait_for(runtime.run(_cfg(), db=db), timeout=20)
    assert code == exitcodes.EX_TEMPFAIL == 75
    assert liveness.progress_checks() == []


@pytest.mark.asyncio
async def test_the_worker_exits_75_when_the_claim_loop_returns_by_itself(
    db: Database,  # noqa: F811
    worker: list[int],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    async def returns(self: ClaimLoop) -> None:
        return None

    monkeypatch.setattr(ClaimLoop, "run", returns)
    assert await asyncio.wait_for(runtime.run(_cfg(), db=db), timeout=20) == 75


@pytest.mark.asyncio
async def test_the_worker_exits_75_when_a_core_loop_ends(
    db: Database,  # noqa: F811
    worker: list[int],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    async def ends(*_a: Any, **_k: Any) -> None:
        raise RuntimeError("the watch died")

    monkeypatch.setattr(runtime, "database_watch", ends)
    assert await asyncio.wait_for(runtime.run(_cfg(), db=db), timeout=20) == 75


@pytest.mark.asyncio
async def test_a_worker_asked_to_stop_exits_0(
    db: Database,  # noqa: F811
    worker: list[int],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    events: list[asyncio.Event] = []
    monkeypatch.setattr(runtime, "install_signal_handlers", events.append)
    task = asyncio.ensure_future(runtime.run(_cfg(), db=db))
    await _until(lambda: len(liveness.progress_checks()) == 2, "the worker is up")
    assert {p.name for p in liveness.progress_checks()} == {"dispatch", "database"}
    events[0].set()  # SIGTERM
    assert await asyncio.wait_for(task, timeout=20) == 0
    assert worker == []
