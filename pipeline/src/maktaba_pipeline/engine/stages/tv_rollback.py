"""``tv_rollback`` (Epic 33, story 33.9): put the previous TV release back on the air for
a live change (``payload.change_id``) - the reverse commit of
:mod:`...tvrelease.rollback`. The API enqueues it for an admin's rollback; the change
controller for the front door's automatic rollback (a crash of the new live slot inside
the grace) and for a swap whose front-door operation failed (``payload.reason``,
``payload.op_id``)."""

from __future__ import annotations

from typing import Any

from ...db.jobs import Job
from ..context import StageContext, StageFailure
from ..graph import StageResult
from ..hashing import canonical_hash
from .registry import StageImpl
from .tv_stage import change_id_of

__all__ = ["IMPL"]


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    from ...tvrelease.rollback import rollback_change

    cid = change_id_of(job)
    payload = job.payload or {}
    raw = payload.get("op_id")
    op = int(raw) if isinstance(raw, int | str) and str(raw) != "" else None
    try:
        report = await rollback_change(
            db, cid, op_id=op,
            reason=str(payload.get("reason") or "manual"), actor_source=f"job:{job.id}",
            actor_user_id=payload.get("actor") or None,
        )  # fmt: skip
    except LookupError as e:
        raise StageFailure("no_rollback_point", str(e), retryable=False) from e
    return StageResult(
        output_hash=canonical_hash(["rolled_back", cid, report.get("to_gen")]),
        summary={"change_id": cid, **report},
    )


IMPL = StageImpl(run=run)
