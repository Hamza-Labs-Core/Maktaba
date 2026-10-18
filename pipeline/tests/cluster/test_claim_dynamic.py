"""The claim loop reads the served set on every poll (design section 5)."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.pipeline.runner import ClaimLoop, WorkerConfig
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video


@pytest.mark.asyncio
async def test_the_served_set_changes_between_polls(db: Database) -> None:
    lib = await make_library(db)
    for _ in range(2):
        v = await make_video(db, lib)
        await enqueue_job(db, subject_kind="video", subject_id=v, stage="probe")
    served: tuple[str, ...] = ()
    claimed: list[int] = []

    async def dispatch(job: Job) -> None:
        claimed.append(job.id)

    cfg = WorkerConfig(
        supported_stages=(),  # empty at start (for example draining): allowed when dynamic
        pool_mode=True,
        stage_pools={"probe": "nas_read", "tag": "cpu"},
        dynamic_stages=lambda: served,
    )
    loop = ClaimLoop(db, cfg, dispatch)
    in_flight: set[asyncio.Task[None]] = set()
    assert await loop._drain(in_flight) == 0
    served = ("tag",)
    assert await loop._drain(in_flight) == 0
    served = ("probe", "tag")
    assert await loop._drain(in_flight) == 2
    await asyncio.gather(*in_flight)
    assert len(claimed) == 2


def test_an_empty_static_set_is_still_refused() -> None:
    with pytest.raises(ValueError):
        WorkerConfig(supported_stages=())
