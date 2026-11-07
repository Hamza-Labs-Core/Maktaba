"""``tv_gc`` (Epic 33, story 33.3 AC8): the daily sweep of old schedule generations,
retired channels past the rollback retention and the staged channels of failed or
cancelled changes. The API's change controller enqueues it once a day (a visible run)."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    from maktaba_pipeline.tvrelease.gc import collect

    out = await collect(db)
    return StageResult(output_hash=canonical_hash(["tv_gc", out]), summary=out)


IMPL = StageImpl(run=run)
