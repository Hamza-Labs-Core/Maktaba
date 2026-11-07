"""``tv_stage`` (Epic 33, plan 33.10 D2): build the release and schedule generation of a
TV change (``payload.change_id``), moving it ``draft -> staged``. Enqueued by the API's
change controller when an admin presses Stage (or a producer drafts in auto mode), so the
work is a visible run in the control centre. A change queued behind the one in flight
stays a draft (``waiting_inputs``); the controller enqueues it again later."""

from __future__ import annotations

from typing import Any

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages.registry import StageImpl

__all__ = ["IMPL"]


def change_id_of(job: Job) -> str:
    cid = (job.payload or {}).get("change_id")
    if not cid:
        raise StageFailure("bad_payload", "no change_id in the job's payload", retryable=False)
    return str(cid)


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    from maktaba_pipeline.tvrelease.stage import ChangeInFlight, stage_change

    cid = change_id_of(job)
    try:
        state = await stage_change(db, cid, actor_source=f"job:{job.id}")
    except ChangeInFlight as e:
        return StageResult(
            output_hash=canonical_hash(["queued", cid, e.change_id]),
            outcome="waiting_inputs",
            summary={"change_id": cid, "state": "draft", "queued_behind": e.change_id},
        )
    except LookupError as e:
        raise StageFailure("no_change", str(e), retryable=False) from e
    return StageResult(
        output_hash=canonical_hash(["staged", cid, state]),
        summary={"change_id": cid, "state": state},
    )


IMPL = StageImpl(run=run)
