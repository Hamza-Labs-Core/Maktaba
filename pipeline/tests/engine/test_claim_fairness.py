"""No stage of a pool starves (2026-11-02): the claim shares a pool among its stages.

On the owner's home the thumbnail stage (70k jobs at priority 900) had not run since
2026-10-22: nas_read has two slots, and thumb_small and chapter_thumbs (42k jobs at 100)
always had the next job under a strict ``ORDER BY priority, id``. The claim now gives each
stage claims in inverse proportion to its head's priority (stride scheduling over
``stage_claim_pass``), and a job at ``URGENT_PRIORITY`` or better (Process now) still goes
first. Both dialects run every test (``db`` fixture).
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
    # stride bound: nine claims of each priority-100 stage (100 * 9 = 900), then the 900
    assert order[-1] == LOW, order
    assert len(order) <= 2 * 9 + 1, order
    # and priority still decides the split: the busy stages got the claims before it
    assert Counter(order[:-1]) == {"thumb_small": 9, "chapter_thumbs": 9}


@pytest.mark.asyncio
async def test_the_share_keeps_going_one_low_claim_for_every_nine_of_each_busy_stage(
    db: Database,
) -> None:
    lib = await make_library(db)
    for _ in range(40):
        for stage in BUSY:
            await _enqueue(db, lib, stage, 100)
    for _ in range(5):
        await _enqueue(db, lib, LOW, 900)
    seen: Counter[str] = Counter()
    for _ in range(57):  # three rounds of 9 + 9 + 1
        job = await _claim(db)
        assert job is not None
        seen[str(job.stage)] += 1
        await _done(db, job)
    assert seen == {"thumb_small": 27, "chapter_thumbs": 27, LOW: 3}


@pytest.mark.asyncio
async def test_process_now_beats_a_starved_stage(db: Database) -> None:
    lib = await make_library(db)
    for stage in BUSY:
        await _enqueue(db, lib, stage, 100)
    await _enqueue(db, lib, LOW, 900)
    # the busy stages are far ahead in their share: the low stage is next by fair share
    for stage in BUSY:
        await db.execute(
            "INSERT INTO claim_shares (owner, pass) VALUES ($1, $2)", f"stage:{stage}", 1_000_000
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
async def test_a_stage_back_from_idle_does_not_cash_in_saved_credit(db: Database) -> None:
    lib = await make_library(db)
    for _ in range(30):
        await _enqueue(db, lib, "thumb_small", 100)
    # thumb_small runs alone for a while: its pass climbs to 2000
    for _ in range(20):
        job = await _claim(db)
        assert job is not None and job.stage == "thumb_small"
        await _done(db, job)
    # chapter_thumbs had nothing to do; now it has work. It joins at the lowest pass among
    # the stages with work (2000), not at 0: the two alternate instead of chapter_thumbs
    # taking 20 claims in a row
    for _ in range(10):
        await _enqueue(db, lib, "chapter_thumbs", 100)
    seen: list[str] = []
    for _ in range(6):
        job = await _claim(db)
        assert job is not None
        seen.append(str(job.stage))
        await _done(db, job)
    assert Counter(seen) == {"thumb_small": 3, "chapter_thumbs": 3}, seen


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
