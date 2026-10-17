"""Live events and the job log (plan 06-11 section 7).

Lifecycle events go into the existing ``events`` table (channel ``jobs``)
inside the caller's transaction; the slot 0061 trigger notifies every replica.
On SQLite there is no trigger, so the in-process bus is also published to.
"""

from __future__ import annotations

from typing import Any

from ..db.pubsub import get_bus
from .sql import dumps, is_pg, loads, uid

__all__ = ["JOBS_CHANNEL", "job_ref", "log_event", "publish"]

JOBS_CHANNEL = "jobs"

#: ``job_events`` kinds that are never mirrored as ``job.log`` (volume control).
_QUIET_KINDS = frozenset({"enqueued", "coalesced", "skipped"})


async def publish(db: Any, type_: str, payload: dict[str, Any]) -> None:
    """Insert one ``events`` row on channel ``jobs``."""
    await db.execute(
        "INSERT INTO events (channel, type, payload) VALUES ($1, $2, $3)",
        JOBS_CHANNEL,
        type_,
        dumps(payload),
    )
    if not is_pg(db):
        get_bus().publish("ws.events", {"channel": JOBS_CHANNEL, "type": type_, "payload": payload})


async def job_ref(db: Any, job_id: int) -> dict[str, Any] | None:
    """The ``JobRef`` payload of section 7.2 for one job id."""
    from .subjects import label_of

    row = await db.fetchrow(
        "SELECT id, stage, state, subject_kind, subject_id, library_id, priority, attempts,"
        " max_attempts, outcome, error_cause, created_at, started_at, finished_at, progress"
        " FROM processing_jobs WHERE id = $1",
        job_id,
    )
    if row is None:
        return None
    label = await label_of(db, row["subject_kind"], row["subject_id"])
    return {
        "id": int(row["id"]),
        "stage": row["stage"],
        "state": row["state"],
        "subject_kind": row["subject_kind"],
        "subject_id": str(row["subject_id"]),
        "subject_label": label,
        "library_id": None if row["library_id"] is None else str(row["library_id"]),
        "priority": int(row["priority"]),
        "attempts": int(row["attempts"]),
        "max_attempts": int(row["max_attempts"]),
        "outcome": row["outcome"],
        "error_cause": row["error_cause"],
        "created_at": _iso(row["created_at"]),
        "started_at": _iso(row["started_at"]),
        "finished_at": _iso(row["finished_at"]),
        "progress": loads(row["progress"]),
    }


def _iso(value: Any) -> str | None:
    if value is None:
        return None
    return str(value.isoformat() if hasattr(value, "isoformat") else value)


async def log_event(
    db: Any,
    *,
    job_id: int,
    subject_kind: str,
    subject_id: Any,
    library_id: Any,
    stage: str,
    level: str,
    kind: str,
    message: str,
    data: dict[str, Any] | None = None,
    subject_label: str | None = None,
) -> None:
    """Append a ``job_events`` row; mirror info-and-above rows as ``job.log``."""
    message = message[:500]
    await db.execute(
        "INSERT INTO job_events (job_id, subject_kind, subject_id, library_id, stage, level, kind,"
        " message, data) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        job_id,
        subject_kind,
        uid(subject_id),
        uid(library_id),
        stage,
        level,
        kind,
        message,
        None if data is None else dumps(data),
    )
    if level != "debug" and kind not in _QUIET_KINDS:
        from .subjects import label_of

        payload: dict[str, Any] = {
            "job_id": job_id,
            "stage": stage,
            "subject_label": subject_label or await label_of(db, subject_kind, subject_id),
            "level": level,
            "kind": kind,
            "message": message,
        }
        if data is not None:
            payload["data"] = data
        await publish(db, "job.log", payload)
