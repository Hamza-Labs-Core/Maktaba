"""Pool-aware claiming and the pool-mode runner (plan 06-11 section 5)."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool, load_pools
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.pipeline.runner import ClaimLoop, WorkerConfig
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, rows, setup


async def _videos(db, n: int, stage: str = "probe", priority: int = 100):
    lib = await make_library(db)
    ids = []
    for _ in range(n):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage, priority=priority)
        ids.append(v)
    return ids


@pytest.mark.asyncio
async def test_global_pool_never_exceeds_its_size_across_claimers(db) -> None:
    await _videos(db, 6)  # probe uses the global nas_read pool (size 2)
    results = await asyncio.gather(
        *[
            claim_for_pool(db, worker_id=f"w{i}", pool="nas_read", stages=["probe"])
            for i in range(6)
        ]
    )
    got = [r for r in results if r is not None]
    assert len(got) == 2
    busy = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE state = 'claimed'")
    assert busy == 2
    # another claimer (another host) sees the pool full
    assert await claim_for_pool(db, worker_id="other", pool="nas_read", stages=["probe"]) is None
    # one finishes, one more slot opens
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE id = $1", got[0].id)
    nxt = await claim_for_pool(db, worker_id="other", pool="nas_read", stages=["probe"])
    assert nxt is not None
    assert await claim_for_pool(db, worker_id="other", pool="nas_read", stages=["probe"]) is None


@pytest.mark.asyncio
async def test_pool_size_is_read_live(db) -> None:
    await _videos(db, 5)
    await db.execute("UPDATE worker_pools SET size = 4 WHERE pool = 'nas_read'")
    got = []
    for i in range(6):
        j = await claim_for_pool(db, worker_id=f"w{i}", pool="nas_read", stages=["probe"])
        if j is not None:
            got.append(j)
    assert len(got) == 4
    await db.execute("UPDATE worker_pools SET size = 0 WHERE pool = 'nas_read'")
    assert await claim_for_pool(db, worker_id="x", pool="nas_read", stages=["probe"]) is None


@pytest.mark.asyncio
async def test_per_worker_pool_is_not_limited_in_sql(db) -> None:
    await _videos(db, 5, stage="subtitle_gen")  # cpu pool is per_worker
    got = [
        await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"])
        for _ in range(5)
    ]
    assert all(j is not None for j in got)


@pytest.mark.asyncio
async def test_only_a_worker_that_serves_the_stage_is_offered_the_job(db) -> None:
    await _videos(db, 1, stage="transcribe")  # gpu pool
    assert await claim_for_pool(db, worker_id="windows", pool="gpu", stages=["index"]) is None
    job = await claim_for_pool(db, worker_id="mac", pool="gpu", stages=["transcribe", "index"])
    assert job is not None and job.stage == "transcribe" and job.claimed_by == "mac"


@pytest.mark.asyncio
async def test_paused_stage_and_global_pause_are_skipped(db) -> None:
    await _videos(db, 2, stage="subtitle_gen")
    await db.execute("UPDATE job_stages SET paused = $1 WHERE stage = 'subtitle_gen'", True)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is None
    await db.execute("UPDATE job_stages SET paused = $1 WHERE stage = 'subtitle_gen'", False)
    await db.execute("UPDATE queue_control SET paused = $1 WHERE id = 1", True)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is None
    await db.execute("UPDATE queue_control SET paused = $1 WHERE id = 1", False)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is not None


@pytest.mark.asyncio
async def test_disabled_stage_and_future_not_before_are_skipped(db) -> None:
    ids = await _videos(db, 1, stage="subtitle_gen")
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'subtitle_gen'", False)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is None
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'subtitle_gen'", True)
    # a pending debounce / backoff window in the future
    from datetime import timedelta

    from maktaba_pipeline.engine.sql import now, to_ts

    await db.execute(
        "UPDATE processing_jobs SET not_before = $1", to_ts(db, now() + timedelta(hours=1))
    )
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is None
    await db.execute(
        "UPDATE processing_jobs SET not_before = $1", to_ts(db, now() - timedelta(seconds=1))
    )
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"]) is not None
    assert ids


@pytest.mark.asyncio
async def test_priority_then_id_order(db) -> None:
    lib = await make_library(db)
    a = await make_video(db, lib)
    b = await make_video(db, lib)
    c = await make_video(db, lib)
    for v, p in ((a, 100), (b, 50), (c, 50)):
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="subtitle_gen", priority=p)
    order = []
    for _ in range(3):
        j = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["subtitle_gen"])
        order.append(str(j.video_id))
    assert order == [str(b), str(c), str(a)]


@pytest.mark.asyncio
async def test_claimed_job_has_new_fields_and_string_stage(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash", force=True)
    job = await claim_for_pool(db, worker_id="w", pool="nas_read", stages=["thumb_hash"])
    assert job is not None
    assert job.stage == "thumb_hash" and job.subject_kind == "video"
    assert job.subject.id == v and job.library_id == lib
    assert job.force is True and job.attempts == 1 and job.state == "claimed"


@pytest.mark.asyncio
async def test_load_pools(db) -> None:
    pools = await load_pools(db)
    assert pools["nas_read"].scope == "global" and pools["nas_read"].effective_size == 2
    assert pools["gpu"].scope == "per_worker" and pools["gpu"].effective_size == 1
    assert pools["cpu"].effective_size >= 1


@pytest.mark.asyncio
async def test_runner_pool_mode_resizes_per_worker_pool_on_next_drain(db) -> None:
    rec = Recorder()
    rt = await setup(db, rec)  # toy stages run in the cpu pool
    lib = await make_library(db)
    for _ in range(6):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    release = asyncio.Event()
    started: list[int] = []

    async def dispatch(job) -> None:
        started.append(job.id)
        await release.wait()

    cfg = WorkerConfig(
        supported_stages=tuple(rt.graph),
        pool_mode=True,
        stage_pools={s: "cpu" for s in rt.graph},
    )
    loop = ClaimLoop(db, cfg, dispatch)
    in_flight: set[asyncio.Task[None]] = set()

    await db.execute("UPDATE worker_pools SET size = 2 WHERE pool = 'cpu'")
    assert await loop._drain(in_flight) == 2
    await asyncio.sleep(0)
    assert len(started) == 2
    # raising the size takes effect on the next drain, counting what is in flight
    await db.execute("UPDATE worker_pools SET size = 4 WHERE pool = 'cpu'")
    assert await loop._drain(in_flight) == 2
    await asyncio.sleep(0)
    assert len(started) == 4
    assert await loop._drain(in_flight) == 0
    release.set()
    await asyncio.gather(*in_flight)
    # capacity is back: the rest drain
    assert await loop._drain(in_flight) == 2
    await asyncio.gather(*in_flight)
    assert len(await rows(db, "t_a")) == 6


@pytest.mark.asyncio
async def test_runner_global_pool_is_limited_by_the_database(db) -> None:
    lib = await make_library(db)
    for _ in range(5):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    release = asyncio.Event()

    async def dispatch(job) -> None:
        await release.wait()

    mk = lambda: ClaimLoop(  # noqa: E731
        db,
        WorkerConfig(
            supported_stages=("probe",), pool_mode=True, stage_pools={"probe": "nas_read"}
        ),
        dispatch,
    )
    a, b = mk(), mk()
    ta: set[asyncio.Task[None]] = set()
    tb: set[asyncio.Task[None]] = set()
    claimed = await asyncio.gather(a._drain(ta), b._drain(tb))
    assert sum(claimed) == 2  # nas_read size is 2 across both "workers"
    release.set()
    await asyncio.gather(*ta, *tb)
