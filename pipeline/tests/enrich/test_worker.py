"""Story 26.7 §3 — the enrich worker's outcome routing."""

from __future__ import annotations

from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.enrich.jobs import enqueue_enrich
from maktaba_pipeline.enrich.worker import (
    ProviderPaused,
    RateLimited,
    process_one_enrich_job,
)

from ._fake_enrich_db import FakeEnrichDB


class _Service:
    def __init__(self, exc: Exception | None = None) -> None:
        self.exc = exc
        self.calls = 0

    async def enrich_video(self, conn: object, video_id: UUID, *, force: bool) -> object:
        self.calls += 1
        if self.exc is not None:
            raise self.exc
        return {"ok": True}


@pytest.mark.asyncio
async def test_success_marks_done() -> None:
    db = FakeEnrichDB()
    await enqueue_enrich(db, uuid4())
    job = await process_one_enrich_job(db, _Service())
    assert job is not None
    assert db.jobs[str(job.id)]["status"] == "done"


@pytest.mark.asyncio
async def test_idle_returns_none() -> None:
    db = FakeEnrichDB()
    assert await process_one_enrich_job(db, _Service()) is None


@pytest.mark.asyncio
async def test_provider_paused_defers_no_attempt() -> None:
    db = FakeEnrichDB()
    await enqueue_enrich(db, uuid4())
    job = await process_one_enrich_job(db, _Service(ProviderPaused()))
    assert job is not None
    row = db.jobs[str(job.id)]
    assert row["status"] == "deferred"
    assert row["attempts"] == 0


@pytest.mark.asyncio
async def test_rate_limited_defers() -> None:
    db = FakeEnrichDB()
    await enqueue_enrich(db, uuid4())
    job = await process_one_enrich_job(db, _Service(RateLimited()))
    assert job is not None
    assert db.jobs[str(job.id)]["status"] == "deferred"


@pytest.mark.asyncio
async def test_unexpected_error_retries() -> None:
    db = FakeEnrichDB()
    await enqueue_enrich(db, uuid4())
    job = await process_one_enrich_job(db, _Service(RuntimeError("network")))
    assert job is not None
    row = db.jobs[str(job.id)]
    # Consumed an attempt and re-queued (below the cap).
    assert row["attempts"] == 1
    assert row["status"] == "pending"
