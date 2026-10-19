"""``tv_swap`` (Epic 33): put an approved TV change (``payload.change_id``, in
``swapping``) on the air - the swap commit of :mod:`...tvrelease.commit`. With TV slots it
is the database half of the swap (lineup, settings, generation flip and the front door's
swap operation, one transaction); the front door then switches each channel at its
programme boundary and the controller makes the change live. Without slots the commit is
the whole swap. The controller enqueues it when an approved change's ``swap_at`` is
reached or an admin presses Swap now."""

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
    from ...tvrelease.commit import commit_change

    cid = change_id_of(job)
    try:
        report = await commit_change(db, cid, actor_source=f"job:{job.id}")
    except LookupError as e:
        raise StageFailure("not_swapping", str(e), retryable=False) from e
    except Exception as e:  # the change went back to ready: an admin retries the swap
        raise StageFailure("swap_failed", str(e)[:300], retryable=False) from e
    return StageResult(
        output_hash=canonical_hash(["swapped", cid, report.get("to_gen")]),
        summary={"change_id": cid, **report},
    )


IMPL = StageImpl(run=run)
