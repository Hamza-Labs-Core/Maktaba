"""Engine bookkeeping hooked into the legacy state helpers: progress, pause, cancel."""

from __future__ import annotations

from uuid import UUID

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.db.jobs_progress import ProgressTick, tick_progress
from maktaba_pipeline.db.jobs_state import mark_cancelled, mark_paused
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.hooks import _last_progress
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video


async def _claimed(db: Database, stage: str = "transcribe", pool: str = "gpu") -> tuple[Job, UUID]:
    lib = await make_library(db)
    v = await make_video(db, lib, title="Talk")
    await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage)
    job = await claim_for_pool(db, worker_id="mac", pool=pool, stages=[stage])
    assert job is not None
    return job, v


@pytest.mark.asyncio
async def test_legacy_progress_tick_feeds_the_queue_ui_throttled(db: Database) -> None:
    job, _ = await _claimed(db)
    _last_progress.clear()
    await tick_progress(
        db, ProgressTick(job_id=job.id, processed_seconds=30.0, segments_completed_delta=1)
    )
    await tick_progress(
        db, ProgressTick(job_id=job.id, processed_seconds=60.0, segments_completed_delta=1)
    )
    row = await db.fetchrow("SELECT progress FROM processing_jobs WHERE id = $1", job.id)
    doc = loads(row["progress"])
    assert (
        doc["step"] == "transcribe" and doc["done_units"] == 30.0
    )  # the second tick was throttled
    evs = await db.fetch("SELECT payload FROM events WHERE type = 'job.progress'")
    assert len(evs) == 1
    assert loads(evs[0]["payload"])["job"]["subject_label"] == "Talk"


@pytest.mark.asyncio
async def test_pause_and_cancel_are_logged_and_a_midrun_trigger_survives_cancel(
    db: Database,
) -> None:
    job, v = await _claimed(db)
    await mark_paused(db, job_id=job.id, at_sec=12.0, reason="shutdown")
    kinds = [r["kind"] for r in await db.fetch("SELECT kind FROM job_events ORDER BY id")]
    assert "paused" in kinds
    # resume (claim again), a trigger arrives, then the operator cancels: one follow-up row remains
    again = await claim_for_pool(db, worker_id="mac", pool="gpu", stages=["transcribe"])
    assert again is not None and again.id == job.id
    res = await enqueue_job(db, subject_kind="video", subject_id=v, stage="transcribe", force=True)
    assert res.outcome == "rerun_requested"
    await mark_cancelled(db, job_id=job.id)
    rows = await db.fetch(
        "SELECT state, force FROM processing_jobs WHERE stage = 'transcribe' ORDER BY id"
    )
    assert [r["state"] for r in rows] == ["cancelled", "pending"]
    assert bool(rows[1]["force"]) is True
    assert "cancelled" in [r["kind"] for r in await db.fetch("SELECT kind FROM job_events")]
