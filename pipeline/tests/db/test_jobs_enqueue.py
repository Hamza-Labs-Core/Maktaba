"""Behaviour tests for :func:`maktaba_pipeline.db.jobs.enqueue` / ``enqueue_scan``.

These are now thin wrappers over :func:`maktaba_pipeline.engine.enqueue.enqueue_job`
(plan 06-11 section 3.5), so the tests run against the real migrated schema:
SQLite always, Postgres when ``MAKTABA_TEST_PG_URL`` is set. They pin the
legacy outcome vocabulary (``inserted`` / ``reused`` / ``skipped_done_unchanged``).

Not marked ``unit``: asyncio's event-loop bootstrap needs a socketpair, which
the unit-tier netguard rejects.
"""

from __future__ import annotations

import asyncio
import json
from datetime import UTC, datetime, timedelta

import pytest

from maktaba_pipeline.db import JOBS_NEW, Stage, enqueue, get_bus, reset_bus
from maktaba_pipeline.db.jobs import enqueue_scan
from maktaba_pipeline.engine.sql import to_ts
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video


async def _count(db: Database) -> int:
    return int(await db.fetchval("SELECT count(*) FROM processing_jobs"))


@pytest.mark.asyncio
async def test_enqueue_inserts_pending_row(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    res = await enqueue(db, video_id=video, stage=Stage.PROBE)
    assert res.outcome == "inserted"
    row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", res.id)
    assert row["state"] == "pending" and row["stage"] == "probe"
    assert row["priority"] == 100 and row["max_attempts"] == 3
    assert row["subject_kind"] == "video" and str(row["subject_id"]) == str(video)


@pytest.mark.asyncio
async def test_enqueue_idempotent_returns_same_id(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    a = await enqueue(db, video_id=video, stage=Stage.PROBE)
    b = await enqueue(db, video_id=video, stage=Stage.PROBE)
    assert (b.id, b.outcome) == (a.id, "reused")
    assert await _count(db) == 1


@pytest.mark.asyncio
async def test_enqueue_concurrent_idempotent(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    results = await asyncio.gather(
        *[enqueue(db, video_id=video, stage=Stage.PROBE) for _ in range(20)]
    )
    assert len({r.id for r in results}) == 1
    assert [r.outcome for r in results].count("inserted") == 1
    assert await _count(db) == 1


@pytest.mark.asyncio
async def test_enqueue_skips_when_done_and_source_unchanged(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    first = await enqueue(db, video_id=video, stage=Stage.PROBE)
    finished = datetime.now(UTC) + timedelta(seconds=5)
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', finished_at = $2 WHERE id = $1",
        first.id,
        to_ts(db, finished),
    )
    again = await enqueue(db, video_id=video, stage=Stage.PROBE)
    assert (again.id, again.outcome) == (first.id, "skipped_done_unchanged")
    assert await _count(db) == 1


@pytest.mark.asyncio
async def test_enqueue_creates_new_when_source_changed(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    first = await enqueue(db, video_id=video, stage=Stage.PROBE)
    finished = datetime.now(UTC) - timedelta(days=1)
    await db.execute(
        "UPDATE processing_jobs SET state = 'done', finished_at = $2 WHERE id = $1",
        first.id,
        to_ts(db, finished),
    )
    await db.execute(
        "UPDATE videos SET updated_at = $2 WHERE id = $1", str(video), to_ts(db, datetime.now(UTC))
    )
    again = await enqueue(db, video_id=video, stage=Stage.PROBE)
    assert again.outcome == "inserted" and again.id != first.id


@pytest.mark.asyncio
async def test_enqueue_payload_round_trips(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    res = await enqueue(db, video_id=video, stage=Stage.TRANSCRIBE, payload={"model": "x", "n": 3})
    raw = await db.fetchval("SELECT payload FROM processing_jobs WHERE id = $1", res.id)
    assert (json.loads(raw) if isinstance(raw, str) else raw) == {"model": "x", "n": 3}


@pytest.mark.asyncio
async def test_enqueue_payload_none_stores_null(db: Database) -> None:
    video = await make_video(db, await make_library(db))
    res = await enqueue(db, video_id=video, stage=Stage.PROBE)
    assert await db.fetchval("SELECT payload FROM processing_jobs WHERE id = $1", res.id) is None


@pytest.mark.asyncio
async def test_enqueue_priority_default_and_override(db: Database) -> None:
    lib = await make_library(db)
    a = await enqueue(db, video_id=await make_video(db, lib), stage=Stage.PROBE)
    b = await enqueue(db, video_id=await make_video(db, lib), stage=Stage.PROBE, priority=50)
    assert await db.fetchval("SELECT priority FROM processing_jobs WHERE id = $1", a.id) == 100
    assert await db.fetchval("SELECT priority FROM processing_jobs WHERE id = $1", b.id) == 50


@pytest.mark.asyncio
async def test_enqueue_accepts_stages_beyond_the_original_seven(db: Database) -> None:
    """Regression for F6: the epic-9 stages used to violate the stage CHECKs."""
    video = await make_video(db, await make_library(db))
    res = await enqueue(db, video_id=video, stage="categorize")
    assert res.outcome == "inserted"


@pytest.mark.asyncio
async def test_enqueue_emits_pubsub_event_on_sqlite(db: Database) -> None:
    if db.dialect != "sqlite":
        pytest.skip("Postgres publishes through the AFTER INSERT trigger")
    reset_bus()
    queue = await get_bus().subscribe(JOBS_NEW)
    video = await make_video(db, await make_library(db))
    res = await enqueue(db, video_id=video, stage=Stage.PROBE)
    msg = json.loads(queue.get_nowait())
    assert msg["id"] == res.id and msg["stage"] == "probe" and msg["subject_kind"] == "video"
    await enqueue(db, video_id=video, stage=Stage.PROBE)  # reused: no second publish
    assert queue.empty()
    reset_bus()


@pytest.mark.asyncio
async def test_enqueue_scan_is_library_scoped_and_idempotent(db: Database) -> None:
    lib = await make_library(db)
    a = await enqueue_scan(db, library_id=lib, priority=50)
    b = await enqueue_scan(db, library_id=lib)
    assert (a.outcome, b.outcome, b.id) == ("inserted", "reused", a.id)
    row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", a.id)
    assert row["video_id"] is None and str(row["library_id"]) == str(lib)
    assert row["subject_kind"] == "library" and row["priority"] == 50
