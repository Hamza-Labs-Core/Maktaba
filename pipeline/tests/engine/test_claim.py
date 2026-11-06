"""Pool-aware claiming and the pool-mode runner (plan 06-11 section 5)."""

from __future__ import annotations

import asyncio
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool, load_pools
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.pipeline.runner import ClaimLoop, WorkerConfig
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, rows, setup


async def _videos(db: Database, n: int, stage: str = "probe", priority: int = 100) -> list[UUID]:
    lib = await make_library(db)
    ids: list[UUID] = []
    for _ in range(n):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage, priority=priority)
        ids.append(v)
    return ids


@pytest.mark.asyncio
async def test_global_pool_never_exceeds_its_size_across_claimers(db: Database) -> None:
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
async def test_pool_size_is_read_live(db: Database) -> None:
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
async def test_per_worker_pool_is_not_limited_in_sql(db: Database) -> None:
    await _videos(db, 5, stage="categorize")  # cpu pool is per_worker
    got = [
        await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) for _ in range(5)
    ]
    assert all(j is not None for j in got)


@pytest.mark.asyncio
async def test_only_a_worker_that_serves_the_stage_is_offered_the_job(db: Database) -> None:
    await _videos(db, 1, stage="transcribe")  # gpu pool
    assert await claim_for_pool(db, worker_id="windows", pool="gpu", stages=["index"]) is None
    job = await claim_for_pool(db, worker_id="mac", pool="gpu", stages=["transcribe", "index"])
    assert job is not None and job.stage == "transcribe" and job.claimed_by == "mac"


@pytest.mark.asyncio
async def test_paused_stage_and_global_pause_are_skipped(db: Database) -> None:
    await _videos(db, 2, stage="categorize")
    await db.execute(
        "UPDATE job_stages SET paused = $1, paused_at = CURRENT_TIMESTAMP, paused_by = 'test'"
        " WHERE stage = 'categorize'",
        True,
    )
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is None
    await db.execute(
        "UPDATE job_stages SET paused = $1, paused_at = NULL, paused_by = NULL"
        " WHERE stage = 'categorize'",
        False,
    )
    await db.execute("UPDATE queue_control SET paused = $1 WHERE id = 1", True)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is None
    await db.execute("UPDATE queue_control SET paused = $1 WHERE id = 1", False)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is not None


@pytest.mark.asyncio
async def test_disabled_stage_and_future_not_before_are_skipped(db: Database) -> None:
    ids = await _videos(db, 1, stage="categorize")
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'categorize'", False)
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is None
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'categorize'", True)
    # a pending debounce / backoff window in the future
    from datetime import timedelta

    from maktaba_pipeline.engine.sql import now, to_ts

    await db.execute(
        "UPDATE processing_jobs SET not_before = $1", to_ts(db, now() + timedelta(hours=1))
    )
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is None
    await db.execute(
        "UPDATE processing_jobs SET not_before = $1", to_ts(db, now() - timedelta(seconds=1))
    )
    assert await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"]) is not None
    assert ids


@pytest.mark.asyncio
async def test_priority_then_id_order(db: Database) -> None:
    lib = await make_library(db)
    a = await make_video(db, lib)
    b = await make_video(db, lib)
    c = await make_video(db, lib)
    for v, p in ((a, 100), (b, 50), (c, 50)):
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="categorize", priority=p)
    order: list[str] = []
    for _ in range(3):
        j = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"])
        assert j is not None
        order.append(str(j.video_id))
    assert order == [str(b), str(c), str(a)]


@pytest.mark.asyncio
async def test_claimed_job_has_new_fields_and_string_stage(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash", force=True)
    job = await claim_for_pool(db, worker_id="w", pool="nas_read", stages=["thumb_hash"])
    assert job is not None
    assert job.stage == "thumb_hash" and job.subject_kind == "video"
    assert job.subject.id == v and job.library_id == lib
    assert job.force is True and job.attempts == 1 and job.state == "claimed"


@pytest.mark.asyncio
async def test_load_pools(db: Database) -> None:
    pools = await load_pools(db)
    assert pools["nas_read"].scope == "global" and pools["nas_read"].effective_size == 2
    assert pools["gpu"].scope == "per_worker" and pools["gpu"].effective_size == 32
    assert pools["cpu"].effective_size >= 1


@pytest.mark.asyncio
async def test_runner_pool_mode_resizes_per_worker_pool_on_next_drain(db: Database) -> None:
    rec = Recorder()
    rt = await setup(db, rec)  # toy stages run in the cpu pool
    lib = await make_library(db)
    for _ in range(6):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    release = asyncio.Event()
    started: list[int] = []

    async def dispatch(job: Job) -> None:
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
async def test_runner_global_pool_is_limited_by_the_database(db: Database) -> None:
    lib = await make_library(db)
    for _ in range(5):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    release = asyncio.Event()

    async def dispatch(job: Job) -> None:
        await release.wait()

    def mk() -> ClaimLoop:
        return ClaimLoop(
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


@pytest.mark.asyncio
async def test_resuming_a_paused_job_is_not_a_new_attempt(db: Database) -> None:
    ids = await _videos(db, 1, stage="categorize")
    await db.execute(
        "UPDATE processing_jobs SET state = 'paused', attempts = max_attempts + 1"
        " WHERE subject_id = $1",
        str(ids[0]),
    )
    job = await claim_for_pool(db, worker_id="w", pool="cpu", stages=["categorize"])
    assert job is not None and job.attempts == job.max_attempts + 1  # unchanged, no error


@pytest.mark.unit
def test_a_stage_jobs_lines_carry_its_component() -> None:
    """Epic 53 story 53.01 T07: a stage's job logs with its ByStage component."""
    from types import SimpleNamespace

    import structlog

    from maktaba_pipeline.components.catalog import default
    from maktaba_pipeline.pipeline.runner import _job_log_context

    job = SimpleNamespace(
        id=7, stage="transcribe", video_id=None, subject_id=None, subject_kind=None
    )
    with _job_log_context(job):  # type: ignore[arg-type]
        got = structlog.contextvars.get_contextvars().get("component")
    comp = default().by_stage("transcribe")
    assert comp is not None and got == comp.id
    job.stage = "no-such-stage"
    with _job_log_context(job):  # type: ignore[arg-type]
        assert "component" not in structlog.contextvars.get_contextvars()


@pytest.mark.asyncio
async def test_a_plugin_runs_at_most_its_concurrency(db: Database) -> None:
    """Epic 54 story 54.06 T05: a plugin with concurrency 1 never runs two jobs at once,
    whatever the plugins pool's size; another plugin is not held back by it."""
    import json

    await db.execute(
        "INSERT INTO worker_pools (pool, label, scope, size, default_size)"
        " VALUES ('plugins', 'Plugins', 'global', 8, 8)"
        " ON CONFLICT (pool) DO UPDATE SET size = 8"
    )
    for pid, stage, conc in (
        ("dev.example.one", "p_00000001_work", 1),
        ("dev.example.two", "p_00000002_work", 2),
    ):
        await db.execute(
            "INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, extension)"
            " VALUES ($1, $1, '{video}', 'plugins', 'media', $2)",
            stage,
            f"plugin:{pid}",
        )
        await db.execute(
            "INSERT INTO plugin_installs (plugin_id, version, digest, path, manifest)"
            " VALUES ($1, '1.0.0', 'd', '/x', $2::jsonb)",
            pid,
            json.dumps({"resources": {"concurrency": conc}}),
        )
    await _videos(db, 5, stage="p_00000001_work")
    await _videos(db, 5, stage="p_00000002_work")
    got = await asyncio.gather(
        *[
            claim_for_pool(
                db, worker_id=f"w{i}", pool="plugins", stages=["p_00000001_work"], scope="global"
            )
            for i in range(4)
        ]
    )
    assert len([g for g in got if g is not None]) == 1
    two = [
        await claim_for_pool(
            db, worker_id=f"x{i}", pool="plugins", stages=["p_00000001_work", "p_00000002_work"]
        )
        for i in range(4)
    ]
    assert sorted(j.stage for j in two if j is not None) == ["p_00000002_work", "p_00000002_work"]
