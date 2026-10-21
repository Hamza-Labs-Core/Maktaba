"""Running one job through the engine (plan 06-11 section 6.1)."""

from __future__ import annotations

import asyncio
import traceback
from collections.abc import Mapping
from typing import Any

from ..cluster.agent import jobs as job_events
from ..db import claim_owner
from ..db.jobs import Job
from ..db.jobs_state import StageError, mark_cancelled, mark_failed_or_retry
from ..log import get_logger
from .context import JobCancelled, JobDeferred, StageContext, StageFailure
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
    """Skip check, handler, then propagation. Failures go through ``mark_failed_or_retry``.

    Every step of the job's life is also reported to the cluster agent (``job`` events,
    design-agent.md section 3); a job that ends without a result (the worker stopped,
    the database went away) is reported as such."""
    try:
        await _execute(db, job, stage=stage, graph=graph, ctx=ctx)
    except asyncio.CancelledError:
        job_events.finished(
            job.id,
            "paused",
            outcome=job_events.failure("interrupted", "worker stopping", retryable=True),
        )
        raise
    finally:
        job_events.finished(
            job.id, "failed", outcome=job_events.failure("interrupted", "no result", retryable=True)
        )


async def _execute(
    db: Any,
    job: Job,
    *,
    stage: StageDef,
    graph: Mapping[str, StageDef],
    ctx: StageContext,
) -> None:
    impl = get_impl(stage.name)
    if impl is None or impl.run is None:
        job_events.claimed(job)
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="dispatch_unknown_stage",
                message=f"no handler registered for stage {stage.name}",
                retryable=False,
            ),
        )
        job_events.finished(
            job.id,
            "failed",
            outcome=job_events.failure("dispatch_unknown_stage", stage.name, retryable=False),
        )
        return
    if not await _mark_running(db, job):
        return  # cancelled or reaped between claim and start
    job_events.claimed(job)

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
                    job_events.finished(job.id, "skipped")
                    return

        ref = await job_ref(db, job.id)
        if ref is not None:
            job_events.tracker().set_title(job.id, ref.get("subject_label"))
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
        job_events.finished(job.id, "done", output=result.summary)
    except JobCancelled:
        await mark_cancelled(db, job_id=job.id)
        job_events.finished(
            job.id, "failed", outcome=job_events.failure("cancelled", None, retryable=False)
        )
    except JobDeferred as exc:
        await _defer(db, job, stage, exc)
        job_events.finished(
            job.id, "deferred", outcome=job_events.failure("deferred", exc.reason, retryable=True)
        )
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
        job_events.finished(
            job.id,
            "failed",
            outcome=job_events.failure(exc.kind, exc.message, retryable=exc.retryable),
        )
    except GraphViolation as exc:
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(kind="graph_violation", message=str(exc), retryable=False),
        )
        job_events.finished(
            job.id,
            "failed",
            outcome=job_events.failure("graph_violation", str(exc), retryable=False),
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
        job_events.finished(
            job.id,
            "failed",
            outcome=job_events.failure(type(exc).__name__, str(exc), retryable=True),
        )


async def _defer(db: Any, job: Job, stage: StageDef, exc: JobDeferred) -> None:
    """Back to ``paused`` (claiming it again is no new attempt), not before the retry time."""
    from datetime import timedelta

    at = now()
    await db.execute(
        "UPDATE processing_jobs SET state = 'paused', paused_at = $2, paused_reason = $3,"
        " claimed_by = NULL, not_before = $4"
        " WHERE id = $1 AND state IN ('claimed', 'running', 'resuming')"
        f" AND {claim_owner.guard(db, 5)}",
        job.id,
        to_ts(db, at),
        exc.reason,
        to_ts(db, at + timedelta(seconds=exc.retry_after_sec)),
        claim_owner.current(),
    )
    await log_event(
        db,
        job_id=job.id,
        subject_kind=job.subject_kind,
        subject_id=job.subject.id,
        library_id=job.library_id,
        stage=stage.name,
        level="warn",
        kind="paused",
        message=f"given back for {round(exc.retry_after_sec)} s: {exc.reason}",
    )
