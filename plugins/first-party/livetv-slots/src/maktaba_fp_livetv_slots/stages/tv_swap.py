"""``tv_swap`` (Epic 33): put an approved TV change (``payload.change_id``, in
``swapping``) on the air - the swap commit of :mod:`...tvrelease.commit`. With TV slots it
is the database half of the swap (lineup, settings, generation flip and the front door's
swap operation, one transaction); the front door then switches each channel at its
programme boundary and the controller makes the change live. Without slots the commit is
the whole swap. The controller enqueues it when an approved change's ``swap_at`` is
reached or an admin presses Swap now."""

from __future__ import annotations

from typing import Any

from maktaba_fp_livetv_slots.stages.tv_stage import change_id_of
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    from maktaba_pipeline.tvrelease.for_job import commit_for_job

    cid = change_id_of(job)
    # the commit writes the stored TV settings, the engine's: in a runner the worker does it
    # for this job's change (tvrelease.for_job)
    got = await commit_for_job(db, job)
    if "lookup" in got:
        raise StageFailure("not_swapping", str(got["lookup"]), retryable=False)
    if "failed" in got:
        raise StageFailure("swap_failed", str(got["failed"]), retryable=False)
    report = got["report"]
    return StageResult(
        output_hash=canonical_hash(["swapped", cid, report.get("to_gen")]),
        summary={"change_id": cid, **report},
    )


IMPL = StageImpl(run=run)
