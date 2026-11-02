"""No stage of a pool starves (2026-11-02): the claim shares a pool among its stages.

On the owner's home the thumbnail stage (70k jobs at priority 900) had not run since
2026-10-22: nas_read has two slots, and thumb_small and chapter_thumbs (42k jobs at 100)
always had the next job under a strict ``ORDER BY priority, id``. The claim now gives each
priority class claims in inverse proportion to its priority (stride scheduling over
``claim_shares``), heads of one priority stay in id order, and a job at ``URGENT_PRIORITY``
or better (Process now) still goes first. Both dialects run every test (``db`` fixture).
"""

from __future__ import annotations

import asyncio
from collections import Counter
from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import URGENT_PRIORITY, claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video

POOL = "nas_read"  # global, size 2 (as on the home)
BUSY = ("thumb_small", "chapter_thumbs")  # kept full of priority-100 work
LOW = "thumbnail"  # one job at priority 900
STAGES = [*BUSY, LOW]


async def _enqueue(db: Database, lib: UUID, stage: str, priority: int) -> UUID:
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage, priority=priority)
    return v


async def _claim(db: Database) -> Job | None:
    return await claim_for_pool(db, worker_id="w", pool=POOL, stages=STAGES)


async def _done(db: Database, job: Job) -> None:
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE id = $1", job.id)


@pytest.mark.asyncio
async def test_a_priority_900_stage_runs_while_the_pool_stays_full_of_priority_100(
    db: Database,
) -> None:
    lib = await make_library(db)
    for _ in range(6):
        for stage in BUSY:
            await _enqueue(db, lib, stage, 100)
    low = await _enqueue(db, lib, LOW, 900)

    in_flight: list[Job] = []
    order: list[str] = []
    while len(order) < 60:
        job = await _claim(db)
        if job is None:
            # the pool is full (2 slots): the oldest finishes, and the busy stages get one
            # more job each, so they never run out while the low one waits
            assert len(in_flight) == 2
            await _done(db, in_flight.pop(0))
            for stage in BUSY:
                await _enqueue(db, lib, stage, 100)
            continue
        in_flight.append(job)
        order.append(str(job.stage))
        if job.video_id == low:
            break
    # stride bound: nine claims at priority 100 (100 * 9 = 900), then the 900
    assert order[-1] == LOW, order
    assert len(order) <= 9 + 1, order
    # and priority still decides the split: the busy stages, in id order, got the nine
    assert len(order) == 10 and set(order[:-1]) == set(BUSY), order


@pytest.mark.asyncio
async def test_the_share_keeps_going_one_low_claim_for_every_nine_at_priority_100(
    db: Database,
) -> None:
    lib = await make_library(db)
    for _ in range(40):
        for stage in BUSY:
            await _enqueue(db, lib, stage, 100)
    for _ in range(5):
        await _enqueue(db, lib, LOW, 900)
    seen: Counter[str] = Counter()
    for _ in range(50):  # five rounds of nine at 100, one at 900
        job = await _claim(db)
        assert job is not None
        seen[str(job.stage)] += 1
        await _done(db, job)
    # the two busy stages take the nine at 100 between them, in id order
    assert seen == {"thumb_small": 23, "chapter_thumbs": 22, LOW: 5}, seen


@pytest.mark.asyncio
async def test_process_now_beats_a_starved_stage(db: Database) -> None:
    lib = await make_library(db)
    for stage in BUSY:
        await _enqueue(db, lib, stage, 100)
    await _enqueue(db, lib, LOW, 900)
    # the busy class is far ahead in its share: the low stage is next by fair share
    await db.execute(
        "INSERT INTO claim_shares (owner, pass) VALUES ($1, $2)", f"prio:{POOL}:100", 1_000_000
    )
    now = await _enqueue(db, lib, "thumb_small", URGENT_PRIORITY)
    job = await _claim(db)
    assert job is not None and job.video_id == now and job.priority == URGENT_PRIORITY
    # with Process now done, fair share again: the starved stage before the busy ones
    await _done(db, job)
    job = await _claim(db)
    assert job is not None and job.stage == LOW


@pytest.mark.asyncio
async def test_urgent_jobs_go_strictly_by_priority_then_age(db: Database) -> None:
    lib = await make_library(db)
    await _enqueue(db, lib, LOW, 900)
    a = await _enqueue(db, lib, "chapter_thumbs", 50)
    b = await _enqueue(db, lib, "thumb_small", 10)
    c = await _enqueue(db, lib, "thumbnail", 50)
    got: list[UUID | None] = []
    for _ in range(3):
        job = await _claim(db)
        assert job is not None
        got.append(job.video_id)
        await _done(db, job)
    assert got == [b, a, c]


@pytest.mark.asyncio
async def test_a_class_back_from_idle_does_not_cash_in_saved_credit(db: Database) -> None:
    lib = await make_library(db)
    for _ in range(40):
        await _enqueue(db, lib, "thumb_small", 100)
    # priority 100 runs alone for a while: the pool clock climbs to 2000
    for _ in range(20):
        job = await _claim(db)
        assert job is not None and job.stage == "thumb_small"
        await _done(db, job)
    # the 900 class had nothing to do; now it has work. It joins at the pool clock (2000),
    # not at 0: one claim, then nine at 100, not two (900, 1800) back to back
    for _ in range(5):
        await _enqueue(db, lib, LOW, 900)
    seen: list[str] = []
    for _ in range(11):
        job = await _claim(db)
        assert job is not None
        seen.append(str(job.stage))
        await _done(db, job)
    assert seen == [LOW, *["thumb_small"] * 9, LOW], seen


@pytest.mark.asyncio
async def test_equal_priorities_stay_in_id_order_across_stages(db: Database) -> None:
    # a cascade's batch (three fp_gate, then the detect they trigger) must run in the order
    # it was made, as under ORDER BY priority, id: a share per stage round-robined equal
    # stages and split the batch, so the stage downstream ran once per half
    lib = await make_library(db)
    made = [
        await _enqueue(db, lib, stage, 100)
        for stage in ("thumb_small", "thumb_small", "thumb_small", "chapter_thumbs", "thumb_small")
    ]
    got: list[UUID | None] = []
    for _ in made:
        job = await _claim(db)
        assert job is not None
        got.append(job.video_id)
        await _done(db, job)
    assert got == made


@pytest.mark.asyncio
async def test_concurrent_claimers_of_a_per_worker_pool_get_distinct_jobs(db: Database) -> None:
    lib = await make_library(db)
    for _ in range(4):
        await _enqueue(db, lib, "categorize", 100)
    results = await asyncio.gather(
        *[
            claim_for_pool(db, worker_id=f"w{i}", pool="cpu", stages=["categorize"])
            for i in range(4)
        ]
    )
    got = [r.id for r in results if r is not None]
    assert len(got) == len(set(got)) and got
    claimed = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE state = 'claimed'")
    assert claimed == len(got)
