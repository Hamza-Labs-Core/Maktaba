"""The reconciler leaves the connection to the other loops (SQLite: one shared connection).

On SQLite every loop of a worker (reconciler, pruner, channel top-up, queue health,
heartbeat) shares one connection behind an asyncio lock. A reconciler pass that ran in
one transaction held it for the whole pass: with 100 ms per statement, ~10 s over a
1000-row sweep, longer than the worker's 8 s heartbeat. These tests count statements, not
seconds: a hook on the database records each statement as it completes and which task
ran it, so the bound holds however slow the machine is. On Postgres (a pool) the same
tests check the heartbeat never waits for the pass and a cancelled pass rolls back whole.
"""

from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs_state import mark_done
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.reconcile import THUMBS_SWEEP_WHY, reconcile_once
from maktaba_pipeline.engine.sql import is_pg, now, to_ts
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.engine.workers import heartbeat_worker, register_worker
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video

#: the most reconciler statements that may complete while another loop waits for the
#: connection: one unit of work (an enqueue: its transaction and its event) and a spare
BOUND = 12
#: a guard against a hang (the failure this catches is a deadlock), not a timing check
HANG_SEC = 10.0

Hook = Callable[[asyncio.Task[Any] | None, str], None]


def hook_statements(db: Database, monkeypatch: pytest.MonkeyPatch, on_done: Hook) -> None:
    """Call ``on_done(task, sql)`` after each statement of ``db`` completes."""

    def wrap(name: str) -> None:
        orig: Callable[..., Awaitable[Any]] = getattr(db, name)

        async def run(sql: str, *args: Any) -> Any:
            out = await orig(sql, *args)
            on_done(asyncio.current_task(), sql)
            return out

        monkeypatch.setattr(db, name, run)

    for name in ("fetchrow", "fetch", "execute", "executemany"):
        wrap(name)


async def thumbs_sweep_videos(db: Database, n: int) -> list[UUID]:
    """``n`` videos whose latest ``thumb_hash`` found no thumbnail of its own (the
    poster): each gets one sweep job from the reconciler."""
    lib = await make_library(db)
    vids = [await make_video(db, lib) for _ in range(n)]
    for v in vids:
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', outcome = 'changed', finished_at = $1,"
        " output_summary = $2 WHERE stage = 'thumb_hash'",
        to_ts(db, now() - timedelta(minutes=5)),
        '{"source": "poster.jpg"}',
    )
    return vids


async def unpropagated_probes(db: Database, n: int) -> list[int]:
    """``n`` legacy ``probe`` jobs done but never propagated (the process died between)."""
    lib = await make_library(db)
    ids = []
    for _ in range(n):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
        job = await claim_for_pool(db, worker_id="w", pool="nas_read", stages=["probe"])
        assert job is not None
        await mark_done(db, job_id=job.id)
        ids.append(job.id)
    await db.execute(
        "UPDATE processing_jobs SET finished_at = $1 WHERE stage = 'probe'",
        to_ts(db, now() - timedelta(minutes=5)),
    )
    return ids


@pytest.mark.asyncio
async def test_a_heartbeat_gets_the_connection_during_a_long_pass(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    await sync_graph(db, GRAPH)
    graph = {s.name: s for s in GRAPH}
    vids = await thumbs_sweep_videos(db, 150)
    await register_worker(db, worker_id="w-1", host="h", stages=["probe"], pools=["cpu"])

    ask = asyncio.Event()

    async def heartbeat() -> bool:
        # its own task from the start, like the worker's loop (a task made from inside
        # the reconciler would inherit its transaction)
        await ask.wait()
        return await heartbeat_worker(db, "w-1")

    reconciler: asyncio.Task[Any] | None = None
    beat = asyncio.create_task(heartbeat())
    done_by_reconciler = 0
    asked_at: int | None = None  # reconciler statements done when the heartbeat asked
    beat_at: int | None = None  # ... when the heartbeat's UPDATE completed

    def on_done(task: asyncio.Task[Any] | None, sql: str) -> None:
        nonlocal done_by_reconciler, asked_at, beat_at
        if task is reconciler:
            done_by_reconciler += 1
            if done_by_reconciler == 100:  # well inside the sweep
                asked_at = done_by_reconciler
                ask.set()
        elif task is beat and sql.startswith("UPDATE workers"):
            beat_at = done_by_reconciler

    hook_statements(db, monkeypatch, on_done)
    reconciler = asyncio.create_task(reconcile_once(db, graph))
    report = await asyncio.wait_for(reconciler, HANG_SEC)
    assert await beat is True
    assert asked_at is not None and beat_at is not None
    # the whole pass is 4-7 statements a video; the heartbeat waited for at most one unit
    assert done_by_reconciler > 3 * len(vids)
    if is_pg(db):
        # a pool: the heartbeat has a connection of its own and the pass's transaction
        # holds nothing it needs, so it never waits for the pass (how many statements the
        # pass gets in meanwhile is up to the network, so no tighter bound here)
        assert beat_at < done_by_reconciler, (asked_at, beat_at, done_by_reconciler)
    else:
        assert beat_at - asked_at <= BOUND, (asked_at, beat_at, done_by_reconciler)
    assert report.thumbs_dir == len(vids)


async def check_whole(db: Database, vids: list[UUID], probes: list[int]) -> int:
    """No half-done unit: the connection is not left in a transaction, another loop can
    write, each sweep job carries its reason (once per video), and a legacy repair is
    all or nothing (marked propagated exactly when its follow-ups exist). Returns how
    many videos have their sweep job."""
    if not is_pg(db):
        assert not db._conn.in_transaction  # noqa: SLF001 - the shared connection itself
    assert await asyncio.wait_for(heartbeat_worker(db, "w-1"), HANG_SEC) is True
    queued = 0
    for v in vids:
        live = await db.fetch(
            "SELECT reasons FROM processing_jobs WHERE subject_kind = 'video'"
            " AND subject_id = $1 AND stage = 'thumb_hash'"
            " AND state IN ('pending','claimed','running','resuming','paused')",
            str(v),
        )
        assert len(live) <= 1
        if live:
            assert THUMBS_SWEEP_WHY in str(live[0]["reasons"])
            queued += 1
    for job_id in probes:
        row = await db.fetchrow(
            "SELECT video_id, propagated_at, outcome FROM processing_jobs WHERE id = $1", job_id
        )
        follow = await db.fetchval(
            "SELECT count(*) FROM processing_jobs WHERE video_id = $1 AND stage = 'link'",
            row["video_id"],
        )
        if row["propagated_at"] is None:
            assert row["outcome"] is None and follow == 0
        else:
            assert row["outcome"] == "changed" and follow == 1
    return queued


@pytest.mark.asyncio
async def test_a_pass_stopped_anywhere_leaves_only_whole_units(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    await sync_graph(db, GRAPH)
    graph = {s.name: s for s in GRAPH}
    probes = await unpropagated_probes(db, 2)
    vids = await thumbs_sweep_videos(db, 4)
    await register_worker(db, worker_id="w-1", host="h", stages=["probe"], pools=["cpu"])

    reconciler: asyncio.Task[Any] | None = None
    stop_after = 0
    count = 0

    def on_done(task: asyncio.Task[Any] | None, sql: str) -> None:
        nonlocal count
        if task is reconciler and task is not None:
            count += 1
            if count == stop_after:
                task.cancel()  # lands at the reconciler's next await, wherever it is

    hook_statements(db, monkeypatch, on_done)
    stopped = 0
    queued = 0
    # each pass is cancelled one statement later than the last, on what the earlier ones
    # left, until a pass runs to its end
    while True:
        stop_after += 1
        count = 0
        task = reconciler = asyncio.create_task(reconcile_once(db, graph))
        await asyncio.wait({task}, timeout=HANG_SEC)
        reconciler = None
        assert task.done(), "the pass hung"
        if not task.cancelled():
            task.result()  # a cancel must stay a cancel, never turn into an error
            break
        stopped += 1
        got = await check_whole(db, vids, probes)
        assert got >= queued  # what a stopped pass committed stays
        queued = got
        assert stop_after < 500, "the pass never finished"
    assert stopped > 20  # the cancels landed inside the repairs and the sweep alike
    assert await check_whole(db, vids, probes) == len(vids)
    again = await reconcile_once(db, graph)
    assert again.propagated == 0 and again.thumbs_dir == 0
    for job_id in probes:
        assert (
            await db.fetchval("SELECT propagated_at FROM processing_jobs WHERE id = $1", job_id)
            is not None
        )


@pytest.mark.asyncio
@pytest.mark.parametrize("closed", [False, True], ids=["open", "closed"])
async def test_a_transaction_cancelled_in_its_begin_leaves_none_open(
    tmp_path: Path, closed: bool
) -> None:
    """SQLite: aiosqlite runs a queued BEGIN even when its caller is cancelled. The
    transaction is rolled back before the cancel goes on, and the cancel stays a cancel
    when the connection was closed meanwhile (a loop that catches errors would otherwise
    never stop)."""
    db = await Database.connect(f"sqlite:///{tmp_path / 'c.db'}")
    try:

        async def tx() -> None:
            async with db.transaction():
                raise AssertionError("never reached: cancelled in the BEGIN")

        task = asyncio.create_task(tx())
        await asyncio.sleep(0)  # the BEGIN is queued, the task waits for it
        task.cancel()
        if closed:  # the shutdown race: the connection closes behind the queued BEGIN
            await db._conn.close()  # noqa: SLF001
        await asyncio.wait({task}, timeout=HANG_SEC)
        assert task.cancelled()
        if not closed:
            assert not db._conn.in_transaction  # noqa: SLF001
            await db.execute("CREATE TABLE t (x INTEGER)")  # autocommit works again
            assert await db.fetchval("SELECT count(*) FROM t") == 0
    finally:
        await db.close()
