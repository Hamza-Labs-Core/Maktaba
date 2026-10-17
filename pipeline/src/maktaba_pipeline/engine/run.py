"""Running one job through the engine (plan 06-11 section 6.1)."""

from __future__ import annotations

import traceback
from collections.abc import Mapping
from typing import Any

from ..db.jobs import Job
from ..db.jobs_state import StageError, mark_cancelled, mark_failed_or_retry
from ..log import get_logger
from .context import JobCancelled, StageContext, StageFailure
from .events import job_ref, log_event, publish
from .graph import GraphViolation, StageDef, StageResult
from .propagate import finalize_success, last_success
from .sql import now, to_ts
from .stages.registry import get_impl

__all__ = ["execute"]

_log = get_logger()


async def _mark_running(db: Any, job: Job) -> bool:
    row = await db.fetchrow(
        "UPDATE processing_jobs SET state = 'running', started_at = $2"
        " WHERE id = $1 AND state IN ('claimed', 'resuming') RETURNING id",
        job.id,
        to_ts(db, now()),
    )
    return row is not None


async def execute(
    db: Any,
    job: Job,
    *,
    stage: StageDef,
    graph: Mapping[str, StageDef],
    ctx: StageContext,
) -> None:
    """Skip check, handler, then propagation. Failures go through ``mark_failed_or_retry``."""
    impl = get_impl(stage.name)
    if impl is None or impl.run is None:
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="dispatch_unknown_stage",
                message=f"no handler registered for stage {stage.name}",
                retryable=False,
            ),
        )
        return
    if not await _mark_running(db, job):
        return  # cancelled or reaped between claim and start

    try:
        input_hash: str | None = None
        if impl.input_fingerprint is not None:
            input_hash = await impl.input_fingerprint(db, job.subject)
            if input_hash is not None and not (job.force or job.force_cascade):
                prev = await last_success(db, job)
                if (
                    prev is not None
                    and prev["stage_version"] == stage.version
                    and prev["input_hash"] == input_hash
                ):
                    await finalize_success(
                        db,
                        job,
                        stage,
                        graph,
                        output_hash=str(prev["output_hash"]),
                        outcome=None,
                        input_hash=input_hash,
                        skipped_from=prev,
                    )
                    return

        ref = await job_ref(db, job.id)
        await publish(db, "job.started", {"job": ref})
        await log_event(
            db,
            job_id=job.id,
            subject_kind=job.subject_kind,
            subject_id=job.subject.id,
            library_id=job.library_id,
            stage=stage.name,
            level="debug",
            kind="claimed",
            message=f"started by {job.claimed_by or 'worker'}",
        )
        result: StageResult = await impl.run(db, job, ctx)
        for level, message, data in result.log:
            await ctx.log(level, message, data)
        await finalize_success(
            db,
            job,
            stage,
            graph,
            output_hash=result.output_hash,
            outcome=result.outcome,
            input_hash=input_hash,
            changed_subjects=result.changed_subjects,
            requests=result.requests,
            summary=(
                {**(result.summary or {}), "review_digests": ctx.review_digests}
                if ctx.review_digests
                else result.summary
            ),
        )
    except JobCancelled:
        await mark_cancelled(db, job_id=job.id)
    except StageFailure as exc:
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind=exc.kind,
                message=exc.message,
                traceback=traceback.format_exc(),
                retryable=exc.retryable,
            ),
        )
    except GraphViolation as exc:
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(kind="graph_violation", message=str(exc), retryable=False),
        )
    except Exception as exc:  # noqa: BLE001 - every handler failure becomes a classified job failure
        _log.exception("stage_handler_failed", job_id=job.id, stage=stage.name)
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind=type(exc).__name__,
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )
