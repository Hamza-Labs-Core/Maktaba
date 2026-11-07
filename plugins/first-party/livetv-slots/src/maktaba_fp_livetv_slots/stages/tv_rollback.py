"""``tv_rollback`` (Epic 33, story 33.9): put the previous TV release back on the air for
a live change (``payload.change_id``) - the reverse commit of
:mod:`...tvrelease.rollback`. The API enqueues it for an admin's rollback; the change
controller for the front door's automatic rollback (a crash of the new live slot inside
the grace) and for a swap whose front-door operation failed (``payload.reason``,
``payload.op_id``)."""

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
    from maktaba_pipeline.tvrelease.for_job import rollback_for_job

    cid = change_id_of(job)
    # the rollback writes the stored TV settings, the engine's: in a runner the worker does
    # it for this job's change (tvrelease.for_job)
    got = await rollback_for_job(db, job)
    if "lookup" in got:
        raise StageFailure("no_rollback_point", str(got["lookup"]), retryable=False)
    report = got["report"]
    return StageResult(
        output_hash=canonical_hash(["rolled_back", cid, report.get("to_gen")]),
        summary={"change_id": cid, **report},
    )


IMPL = StageImpl(run=run)
