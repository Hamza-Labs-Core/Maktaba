"""Engine bookkeeping hooked into the legacy state helpers (``db/jobs_state.py``).

``mark_failed_or_retry`` / ``mark_cancelled`` / ``mark_paused`` stay the single
place where a job's state changes; when the connection is a real engine
connection (``db.engine_hooks``) they call into here, inside the same
transaction, to record the normalised ``error_cause``, the ``job_events`` row,
the live ``events`` row and, when a trigger arrived mid-run, the follow-up job.
"""

from __future__ import annotations

import time
from typing import Any

from ..cluster.agent import jobs as job_events
from .causes import classify
from .events import job_ref, log_event, publish
from .propagate import enqueue_follow_up
from .sql import dumps

__all__ = ["on_cancelled", "on_failure", "on_paused", "on_progress"]


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
    failure_class: str | None = None,
    parked_on: str | None = None,
) -> None:
    """Record a failure that ``mark_failed_or_retry`` just wrote.

    ``new_state`` is ``pending`` (retry scheduled, or parked on ``parked_on``: Epic 40)
    or ``failed`` (terminal).
    A terminal failure with ``rerun_requested`` still gets its follow-up (new
    input deserves a fresh attempt); a retry clears the flag because the retry
    is the follow-up.
    """
    job_events.finished(
        job_id,
        "failed",
        outcome=job_events.failure(kind, message, retryable=new_state == "pending"),
    )
    row = await _job_row(db, job_id)
    if row is None:
        return
    cause = classify(kind, message)
    will_retry = new_state == "pending"
    if will_retry:
        # the retry is the follow-up: it takes over a force (or force cascade) that
        # arrived mid-run, or it could finish skipped_unchanged without the forced work
        await db.execute(
            "UPDATE processing_jobs SET error_cause = $2, rerun_requested = $3,"
            " force = $4, force_cascade = $5, rerun_force = $6, rerun_force_cascade = $6"
            " WHERE id = $1",
            job_id,
            cause,
            False,
            bool(row["force"]) or bool(row["rerun_force"]),
            bool(row["force_cascade"]) or bool(row["rerun_force_cascade"]),
            False,
        )
    else:
        await db.execute(
            "UPDATE processing_jobs SET error_cause = $2, rerun_requested = $3 WHERE id = $1",
            job_id,
            cause,
            bool(row["rerun_requested"]),
        )
    data: dict[str, Any] = {"cause": cause, "kind": kind, "attempts": int(row["attempts"])}
    if failure_class is not None:
        data["class"] = failure_class
    if parked_on is not None:
        data["parked_on"] = parked_on
    await log_event(
        db,
        **_common(row),
        level="warn" if will_retry else "error",
        # a parked job is logged as ``paused`` (job_events.kind is a closed list) with
        # ``data.parked_on`` saying on what
        kind="paused" if parked_on else "retry_scheduled" if will_retry else "failed",
        message=(f"waiting on {parked_on}: " if parked_on else f"{cause}: ") + message[:450],
        data=data,
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
            "failure_class": failure_class,
            "parked_on": parked_on,
        },
    )
    if not will_retry and row["rerun_requested"]:
        from .claim import row_to_job

        await enqueue_follow_up(db, row_to_job(row), row)


async def on_cancelled(db: Any, job_id: int) -> None:
    job_events.finished(
        job_id, "failed", outcome=job_events.failure("cancelled", None, retryable=False)
    )
    row = await _job_row(db, job_id)
    if row is None:
        return
    await log_event(db, **_common(row), level="info", kind="cancelled", message="cancelled")
    if row["rerun_requested"]:
        from .claim import row_to_job

        await enqueue_follow_up(db, row_to_job(row), row)


async def on_paused(db: Any, job_id: int, reason: str) -> None:
    job_events.finished(
        job_id, "paused", outcome=job_events.failure("paused", reason, retryable=True)
    )
    row = await _job_row(db, job_id)
    if row is None:
        return
    await log_event(db, **_common(row), level="info", kind="paused", message=f"paused ({reason})")


_PROGRESS_EVERY_SEC = 2.0
_last_progress: dict[int, float] = {}


async def on_progress(
    db: Any, job_id: int, *, step: str, done: float, total: float | None, message: str | None = None
) -> None:
    """Mirror a legacy progress tick into ``processing_jobs.progress`` and a ``job.progress`` event.

    Throttled to one write per job every 2 s (the transcribe stage ticks once per segment).
    """
    job_events.progress(job_id, step=step, done=done, total=total)
    t = time.monotonic()
    if t - _last_progress.get(job_id, float("-inf")) < _PROGRESS_EVERY_SEC:
        return
    if len(_last_progress) > 1024:
        _last_progress.clear()
    _last_progress[job_id] = t
    doc: dict[str, Any] = {
        "step": step,
        "steps": [step],
        "done_units": done,
        "total_units": total or 0,
    }
    if message:
        doc["message"] = message
    await db.execute("UPDATE processing_jobs SET progress = $2 WHERE id = $1", job_id, dumps(doc))
    ref = await job_ref(db, job_id)
    if ref is not None:
        await publish(db, "job.progress", {"job": ref})
