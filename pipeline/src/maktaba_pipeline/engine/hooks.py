"""Engine bookkeeping hooked into the legacy state helpers (``db/jobs_state.py``).

``mark_failed_or_retry`` / ``mark_cancelled`` / ``mark_paused`` stay the single
place where a job's state changes; when the connection is a real engine
connection (``db.engine_hooks``) they call into here, inside the same
transaction, to record the normalised ``error_cause``, the ``job_events`` row,
the live ``events`` row and, when a trigger arrived mid-run, the follow-up job.
"""

from __future__ import annotations

from typing import Any

from .causes import classify
from .events import job_ref, log_event, publish
from .propagate import enqueue_follow_up

__all__ = ["on_cancelled", "on_failure", "on_paused"]


async def _job_row(db: Any, job_id: int) -> Any | None:
    return await db.fetchrow(
        "SELECT * FROM processing_jobs WHERE id = $1",
        job_id,
    )


def _common(row: Any) -> dict[str, Any]:
    return {
        "job_id": int(row["id"]),
        "subject_kind": row["subject_kind"],
        "subject_id": row["subject_id"],
        "library_id": row["library_id"],
        "stage": row["stage"],
    }


async def on_failure(
    db: Any,
    job_id: int,
    *,
    kind: str,
    message: str,
    new_state: str,
    not_before: Any,
) -> None:
    """Record a failure that ``mark_failed_or_retry`` just wrote.

    ``new_state`` is ``pending`` (retry scheduled) or ``failed`` (terminal).
    A terminal failure with ``rerun_requested`` still gets its follow-up (new
    input deserves a fresh attempt); a retry clears the flag because the retry
    is the follow-up.
    """
    row = await _job_row(db, job_id)
    if row is None:
        return
    cause = classify(kind, message)
    will_retry = new_state == "pending"
    await db.execute(
        "UPDATE processing_jobs SET error_cause = $2, rerun_requested = $3 WHERE id = $1",
        job_id,
        cause,
        bool(row["rerun_requested"]) and not will_retry,
    )
    await log_event(
        db,
        **_common(row),
        level="warn" if will_retry else "error",
        kind="retry_scheduled" if will_retry else "failed",
        message=f"{cause}: {message}"[:500],
        data={"cause": cause, "kind": kind, "attempts": int(row["attempts"])},
    )
    ref = await job_ref(db, job_id)
    await publish(
        db,
        "job.failed",
        {
            "job": ref,
            "message": message[:500],
            "will_retry": will_retry,
            "not_before": None if not_before is None else str(not_before),
        },
    )
    if not will_retry and row["rerun_requested"]:
        from .claim import row_to_job

        await enqueue_follow_up(db, row_to_job(row), row)


async def on_cancelled(db: Any, job_id: int) -> None:
    row = await _job_row(db, job_id)
    if row is None:
        return
    await log_event(db, **_common(row), level="info", kind="cancelled", message="cancelled")
    if row["rerun_requested"]:
        from .claim import row_to_job

        await enqueue_follow_up(db, row_to_job(row), row)


async def on_paused(db: Any, job_id: int, reason: str) -> None:
    row = await _job_row(db, job_id)
    if row is None:
        return
    await log_event(db, **_common(row), level="info", kind="paused", message=f"paused ({reason})")
