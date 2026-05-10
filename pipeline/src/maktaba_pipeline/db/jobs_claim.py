"""Atomic single-job claim — the primitive every worker rides on.

Story 6.2 (the claim loop) and architecture §7.3 own the SQL contract.
A worker calls :func:`claim_one` to atomically promote one eligible
``processing_jobs`` row to ``state='claimed'`` and returns a fully
populated :class:`Job`. Two dialect-specific implementations live here:

- :func:`claim_one_pg` — single Postgres ``UPDATE`` whose sub-query
  pins one candidate under ``FOR UPDATE SKIP LOCKED``. N concurrent
  workers contend without serialising; each gets a distinct row, or
  ``None`` if nothing is eligible. The whole claim is one statement,
  so a worker that dies mid-claim cannot leave a half-claimed row.
- :func:`claim_one_sqlite` — emulates SKIP LOCKED via a process-wide
  ``asyncio.Lock`` plus ``BEGIN IMMEDIATE``. SQLite has no row-level
  locks, but the asyncio lock serialises in-process callers and the
  immediate-mode write transaction serialises across processes via
  the SQLite mutex. The observable property is the same: at most one
  worker holds any given job at any time.

Eligibility predicate (single source of truth):

- ``state IN ('pending', 'paused')``
- ``pause_requested = false`` — a pause requested **before** the claim
  must be honoured even on a fresh ``pending`` row. Otherwise the user
  pressing Pause between enqueue and claim would silently lose.
- ``cancel_requested = false`` — cancellation is enacted by Story 6.4's
  responder, not by transitioning a claimed row.
- ``not_before IS NULL OR not_before <= now()`` — backoff (Story 6.5)
  delays re-eligibility.
- ``stage = ANY(supported_stages)`` — per-stage workers don't steal
  jobs they cannot run.

Ordering: ``ORDER BY priority ASC, id ASC``. Lower-numbered priority
wins (50 = "user pressed Process now" beats 100 = "newly discovered");
ties broken by FIFO insertion order.
"""

from __future__ import annotations

import asyncio
import json
from collections.abc import Sequence
from datetime import datetime
from typing import Any
from uuid import UUID

from .jobs import DBConn, Job, JobState, Stage

__all__ = [
    "claim_one",
    "claim_one_pg",
    "claim_one_sqlite",
]


# Postgres claim — architecture §7.3, modulated to filter
# pause_requested=false on *all* states (not just paused). The
# architecture's original SQL only gated the paused branch on the flag;
# without the broader filter, a pending row whose user pressed Pause
# before any worker picked it up would still be claimed. Story 6.2's
# acceptance criteria and test_claim_skips_pending_with_pause_requested
# pin the broader interpretation.
_CLAIM_SQL_PG = """
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = $1,
       claimed_at        = now(),
       last_heartbeat_at = now(),
       attempts          = attempts + 1
 WHERE id = (
   SELECT id FROM processing_jobs
    WHERE state IN ('pending', 'paused')
      AND pause_requested = false
      AND (not_before IS NULL OR not_before <= now())
      AND cancel_requested = false
      AND stage = ANY($2::text[])
    ORDER BY priority ASC, id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
 )
RETURNING *
"""


async def claim_one_pg(
    db: DBConn,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    """Postgres-side claim. See module docstring for semantics."""
    if not supported_stages:
        raise ValueError("claim_one: supported_stages must not be empty")
    stages = [s.value for s in supported_stages]
    row = await db.fetchrow(_CLAIM_SQL_PG, worker_id, stages)
    return _row_to_job(row) if row is not None else None


# SQLite claim. Two statements wrapped in BEGIN IMMEDIATE + a
# process-wide asyncio.Lock. The SELECT picks the next eligible row
# under the same predicate as Postgres; the UPDATE uses the row id and
# defensively re-checks state IN ('pending','paused') so a writer that
# committed between the SELECT and UPDATE doesn't get clobbered.
_SQLITE_CLAIM_SELECT = """
SELECT id FROM processing_jobs
 WHERE state IN ('pending', 'paused')
   AND pause_requested = 0
   AND (not_before IS NULL OR datetime(not_before) <= datetime('now'))
   AND cancel_requested = 0
   AND stage IN ({placeholders})
 ORDER BY priority ASC, id ASC
 LIMIT 1
"""

_SQLITE_CLAIM_UPDATE = """
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = ?,
       claimed_at        = datetime('now'),
       last_heartbeat_at = datetime('now'),
       attempts          = attempts + 1
 WHERE id = ?
   AND state IN ('pending', 'paused')
RETURNING *
"""


_sqlite_claim_lock: asyncio.Lock | None = None


def _get_sqlite_claim_lock() -> asyncio.Lock:
    """Return the process-wide SQLite claim lock, creating it lazily.

    The lock must live on the running event loop, so we create it on
    first use rather than at import time (asyncio.Lock binds to the
    loop active at construction; importing this module before the loop
    starts would otherwise pin it to the wrong one).
    """
    global _sqlite_claim_lock
    if _sqlite_claim_lock is None:
        _sqlite_claim_lock = asyncio.Lock()
    return _sqlite_claim_lock


def _reset_sqlite_claim_lock() -> None:
    """Test-only helper — drop the cached lock so a new event loop can rebind it."""
    global _sqlite_claim_lock
    _sqlite_claim_lock = None


async def claim_one_sqlite(
    db: DBConn,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    """SQLite-side claim. See module docstring for the asyncio-lock + BEGIN IMMEDIATE pattern."""
    if not supported_stages:
        raise ValueError("claim_one: supported_stages must not be empty")
    stages = [s.value for s in supported_stages]
    placeholders = ",".join("?" * len(stages))
    select_sql = _SQLITE_CLAIM_SELECT.format(placeholders=placeholders)

    lock = _get_sqlite_claim_lock()
    async with lock, db.transaction():
        candidate = await db.fetchrow(select_sql, *stages)
        if candidate is None:
            return None
        row = await db.fetchrow(_SQLITE_CLAIM_UPDATE, worker_id, candidate["id"])
        return _row_to_job(row) if row is not None else None


async def claim_one(
    db: DBConn,
    *,
    worker_id: str,
    supported_stages: Sequence[Stage],
) -> Job | None:
    """Atomic single-job claim. Dispatches to the dialect-specific impl.

    Returns ``None`` when nothing eligible exists — the caller is
    expected to wait for the next wakeup signal (LISTEN jobs.new on
    Postgres, the in-process bus on SQLite, or a poll-tick fallback)
    before trying again.
    """
    if db.dialect == "postgres":
        return await claim_one_pg(
            db,
            worker_id=worker_id,
            supported_stages=supported_stages,
        )
    return await claim_one_sqlite(
        db,
        worker_id=worker_id,
        supported_stages=supported_stages,
    )


def _row_to_job(row: Any) -> Job:
    """Build a :class:`Job` from a row mapping, tolerating both drivers.

    asyncpg returns parsed JSON, native ``datetime``, native ``UUID``;
    aiosqlite returns ``TEXT``/``str`` for the same shapes. We coerce
    in one place so the rest of the pipeline doesn't care which driver
    served the row.
    """
    return Job(
        id=int(row["id"]),
        video_id=_to_uuid(row["video_id"]),
        stage=Stage(row["stage"]),
        state=JobState(row["state"]),
        priority=int(row["priority"]),
        attempts=int(row["attempts"]),
        max_attempts=int(row["max_attempts"]),
        claimed_by=_optional_str(row["claimed_by"]),
        claimed_at=_to_datetime(row["claimed_at"]),
        last_heartbeat_at=_to_datetime(row["last_heartbeat_at"]),
        not_before=_to_datetime(row["not_before"]),
        error=_optional_str(row["error"]),
        total_duration_seconds=_optional_float(row["total_duration_seconds"]),
        processed_seconds=float(row["processed_seconds"]),
        segments_completed=int(row["segments_completed"]),
        last_segment_end_sec=float(row["last_segment_end_sec"]),
        estimated_remaining_sec=_optional_float(row["estimated_remaining_sec"]),
        realtime_factor=_optional_float(row["realtime_factor"]),
        progress_updated_at=_to_datetime(row["progress_updated_at"]),
        pause_requested=bool(row["pause_requested"]),
        cancel_requested=bool(row["cancel_requested"]),
        paused_at=_to_datetime(row["paused_at"]),
        paused_at_sec=_optional_float(row["paused_at_sec"]),
        paused_reason=_optional_str(row["paused_reason"]),
        resumed_at=_to_datetime(row["resumed_at"]),
        resume_count=int(row["resume_count"]),
        metrics=_to_json_dict(row["metrics"]),
        payload=_to_json_dict(row["payload"]),
        created_at=_required_datetime(row["created_at"]),
        finished_at=_to_datetime(row["finished_at"]),
    )


def _to_uuid(value: Any) -> UUID:
    if isinstance(value, UUID):
        return value
    return UUID(str(value))


def _to_datetime(value: Any) -> datetime | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        return value
    return datetime.fromisoformat(str(value))


def _required_datetime(value: Any) -> datetime:
    dt = _to_datetime(value)
    if dt is None:
        raise ValueError("created_at must not be null")
    return dt


def _to_json_dict(value: Any) -> dict[str, Any] | None:
    if value is None:
        return None
    if isinstance(value, dict):
        return value
    parsed = json.loads(value)
    if not isinstance(parsed, dict):
        raise TypeError(f"expected JSON object, got {type(parsed).__name__}")
    return parsed


def _optional_str(value: Any) -> str | None:
    return None if value is None else str(value)


def _optional_float(value: Any) -> float | None:
    return None if value is None else float(value)
