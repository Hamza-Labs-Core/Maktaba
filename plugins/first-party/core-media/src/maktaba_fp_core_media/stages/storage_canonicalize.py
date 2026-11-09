"""``storage_canonicalize`` (story 31.3): the home converts its stored paths to
canonical share paths by itself (:mod:`maktaba_pipeline.sharepath.auto`). The home's
agent enqueues it (catalog subject); it runs on the home's own machine only, whose
mounts decide the mapping.

Epic 54 story 54.36: a stage of ``tv.maktaba.core-media``. The conversion pauses and resumes
stages and writes the share map, which only the queue engine's process may: in the plugin's
runner the worker runs it for this job (:func:`maktaba_pipeline.sharepath.auto.run_for_job`,
relayed) and the stage records its summary."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.sharepath.auto import run_for_job

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    out = await run_for_job(db)
    return StageResult(output_hash=canonical_hash(["storage_canonicalize", out]), summary=out)


IMPL = StageImpl(run=run)
