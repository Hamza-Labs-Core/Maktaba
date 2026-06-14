"""Story 26.7 — the enrich_jobs queue: enqueue/claim/complete/defer/retry."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta
from uuid import uuid4

import pytest

from maktaba_pipeline.enrich.jobs import (
    EnrichJobStatus,
    claim_enrich_job,
    complete_enrich_job,
    defer_enrich_job,
    enqueue_enrich,
    retry_or_fail_enrich_job,
)

from ._fake_enrich_db import FakeEnrichDB

NOW = datetime(2026, 6, 14, 12, 0, 0, tzinfo=UTC)


@pytest.mark.asyncio
async def test_enqueue_then_claim_then_complete() -> None:
    db = FakeEnrichDB()
    vid = uuid4()
    jid = await enqueue_enrich(db, vid, now=NOW)
    assert jid is not None

    job = await claim_enrich_job(db, now=NOW)
    assert job is not None and job.video_id == vid
    assert db.jobs[str(job.id)]["status"] == "running"

    await complete_enrich_job(db, job, now=NOW)
    assert db.jobs[str(job.id)]["status"] == "done"


@pytest.mark.asyncio
async def test_enqueue_idempotent_one_open_per_video() -> None:
    db = FakeEnrichDB()
    vid = uuid4()
    first = await enqueue_enrich(db, vid, now=NOW)
    second = await enqueue_enrich(db, vid, now=NOW)
    assert first is not None
    assert second is None  # open job already exists → no duplicate


@pytest.mark.asyncio
async def test_defer_does_not_consume_attempt() -> None:
    # test_daily_cap_defers: a rate-limited/paused job is rescheduled to a
    # later window without consuming an attempt.
    db = FakeEnrichDB()
    vid = uuid4()
    await enqueue_enrich(db, vid, now=NOW)
    job = await claim_enrich_job(db, now=NOW)
    assert job is not None
    await defer_enrich_job(db, job, reason="rate_limited", delay=timedelta(minutes=10), now=NOW)
    row = db.jobs[str(job.id)]
    assert row["status"] == "deferred"
    assert row["attempts"] == 0  # not consumed
    assert row["not_before"] == NOW + timedelta(minutes=10)
    # Not claimable until the window passes.
    assert await claim_enrich_job(db, now=NOW) is None
    assert await claim_enrich_job(db, now=NOW + timedelta(minutes=11)) is not None


@pytest.mark.asyncio
async def test_retry_consumes_attempt_then_fails_at_cap() -> None:
    db = FakeEnrichDB()
    vid = uuid4()
    await enqueue_enrich(db, vid, now=NOW)
    job = await claim_enrich_job(db, now=NOW)
    assert job is not None
    # Walk attempts up to the cap (max_attempts=3 here).
    status = await retry_or_fail_enrich_job(db, job, error="boom", max_attempts=3, now=NOW)
    assert status is EnrichJobStatus.PENDING
    assert db.jobs[str(job.id)]["attempts"] == 1

    job.attempts = 2
    status = await retry_or_fail_enrich_job(db, job, error="boom", max_attempts=3, now=NOW)
    assert status is EnrichJobStatus.FAILED
    assert db.jobs[str(job.id)]["status"] == "failed"
