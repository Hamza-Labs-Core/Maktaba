"""The ``enrich_jobs`` queue (slot 0079) — claim/complete/defer/retry.

Modeled on the ``processing_jobs`` claim semantics
(:mod:`maktaba_pipeline.db.jobs_claim`) but on its own table so enrich
work runs decoupled from ``videos.state``: a slow or rate-limited enrich
never holds a video out of ``READY`` (Story 26.7 D2).

Two failure modes are distinguished, because they have different fault
attribution:

- **defer** — provider paused (breaker open) or rate-limited / over the
  daily cap. *Not the job's fault.* The job is rescheduled to a later
  ``not_before`` window **without consuming an attempt**, so a flapping
  provider can't exhaust retries (Story 26.7 ``test_daily_cap_defers``).
- **retry-or-fail** — an unexpected error. The attempt is consumed and
  the job backs off (:func:`compute_backoff`); past ``max_attempts`` it
  goes ``failed`` — and crucially this never touches ``videos.state``.

All SQL is ``$N``-parameterised; the connection wrapper rewrites to ``?``
on SQLite. Writes use ``fetchrow`` (… ``RETURNING``) to match the
project's dialect-agnostic call convention.
"""

from __future__ import annotations

from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from enum import StrEnum
from typing import Any, Protocol
from uuid import UUID, uuid4

from ..pipeline.backoff import compute_backoff

__all__ = [
    "DEFAULT_MAX_ATTEMPTS",
    "Conn",
    "EnrichJob",
    "EnrichJobStatus",
    "claim_enrich_job",
    "complete_enrich_job",
    "defer_enrich_job",
    "enqueue_enrich",
    "retry_or_fail_enrich_job",
]

DEFAULT_MAX_ATTEMPTS = 5


class EnrichJobStatus(StrEnum):
    PENDING = "pending"
    RUNNING = "running"
    DONE = "done"
    DEFERRED = "deferred"
    FAILED = "failed"


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class Conn(Protocol):
    """Minimal connection shape the queue needs (mirrors db.jobs.DBConn)."""

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...


@dataclass(slots=True)
class EnrichJob:
    """One claimed enrich job."""

    id: UUID
    video_id: UUID
    status: EnrichJobStatus
    force: bool
    attempts: int


def _now() -> datetime:
    return datetime.now(UTC)


_ENQUEUE_SQL = """
INSERT INTO enrich_jobs (id, video_id, status, force, attempts, not_before, created_at, updated_at)
VALUES ($1, $2, 'pending', $3, 0, $4, $4, $4)
ON CONFLICT DO NOTHING
RETURNING id
"""


async def enqueue_enrich(
    conn: Conn,
    video_id: UUID,
    *,
    force: bool = False,
    now: datetime | None = None,
) -> UUID | None:
    """Enqueue a pending enrich job for ``video_id``.

    The slot-0079 partial unique index keeps at most one open
    (pending/running/deferred) job per video, so a duplicate enqueue is a
    no-op (``ON CONFLICT DO NOTHING`` → ``None``) — idempotent by
    construction, which is what the ordering/resume guarantees rely on.
    """
    ts = now or _now()
    row = await conn.fetchrow(_ENQUEUE_SQL, str(uuid4()), str(video_id), force, ts)
    if row is None:
        return None
    return UUID(str(row["id"]))


_CLAIM_SQL = """
UPDATE enrich_jobs
SET status = 'running', updated_at = $1
WHERE id = (
    SELECT id FROM enrich_jobs
    WHERE status IN ('pending', 'deferred') AND not_before <= $1
    ORDER BY not_before ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
RETURNING id, video_id, status, force, attempts
"""

# SQLite has no FOR UPDATE SKIP LOCKED; the connection wrapper / claim
# lock serialises claims, so the bare statement is correct there.
_CLAIM_SQL_SQLITE = """
UPDATE enrich_jobs
SET status = 'running', updated_at = $1
WHERE id = (
    SELECT id FROM enrich_jobs
    WHERE status IN ('pending', 'deferred') AND not_before <= $1
    ORDER BY not_before ASC
    LIMIT 1
)
RETURNING id, video_id, status, force, attempts
"""


async def claim_enrich_job(conn: Conn, *, now: datetime | None = None) -> EnrichJob | None:
    """Atomically claim the next due enrich job, or ``None`` if idle.

    "Due" means ``status ∈ {pending, deferred}`` and ``not_before <= now``
    so backoff and defer windows are honoured. The claim flips the row to
    ``running`` in the same statement (``RETURNING``) — a crash mid-run
    leaves a ``running`` row the reaper reclaims (Story 26.7 resume).
    """
    ts = now or _now()
    sql = _CLAIM_SQL_SQLITE if getattr(conn, "dialect", "postgres") == "sqlite" else _CLAIM_SQL
    row = await conn.fetchrow(sql, ts)
    if row is None:
        return None
    return EnrichJob(
        id=UUID(str(row["id"])),
        video_id=UUID(str(row["video_id"])),
        status=EnrichJobStatus(str(row["status"])),
        force=bool(row["force"]),
        attempts=int(row["attempts"]),
    )


_COMPLETE_SQL = """
UPDATE enrich_jobs SET status = 'done', updated_at = $2 WHERE id = $1 RETURNING id
"""


async def complete_enrich_job(conn: Conn, job: EnrichJob, *, now: datetime | None = None) -> None:
    """Mark a claimed job done."""
    await conn.fetchrow(_COMPLETE_SQL, str(job.id), now or _now())


_DEFER_SQL = """
UPDATE enrich_jobs
SET status = 'deferred', not_before = $2, last_error = $3, updated_at = $4
WHERE id = $1
RETURNING id
"""


async def defer_enrich_job(
    conn: Conn,
    job: EnrichJob,
    *,
    reason: str,
    delay: timedelta = timedelta(minutes=15),
    now: datetime | None = None,
) -> None:
    """Reschedule a job to a later window WITHOUT consuming an attempt.

    Used when the provider is paused (breaker open) or rate-limited / over
    the daily cap — failures that are not the job's fault. A deferred job
    drains when the window passes (Story 26.7 ``test_daily_cap_defers``).
    """
    ts = now or _now()
    await conn.fetchrow(_DEFER_SQL, str(job.id), ts + delay, reason, ts)


_RETRY_SQL = """
UPDATE enrich_jobs
SET status = 'pending', attempts = $2, not_before = $3, last_error = $4, updated_at = $5
WHERE id = $1
RETURNING id
"""

_FAIL_SQL = """
UPDATE enrich_jobs
SET status = 'failed', attempts = $2, last_error = $3, updated_at = $4
WHERE id = $1
RETURNING id
"""


async def retry_or_fail_enrich_job(
    conn: Conn,
    job: EnrichJob,
    *,
    error: str,
    max_attempts: int = DEFAULT_MAX_ATTEMPTS,
    now: datetime | None = None,
) -> EnrichJobStatus:
    """Consume an attempt; back off and re-queue, or fail past the cap.

    A genuine error (not a defer-class condition) consumes an attempt.
    Below ``max_attempts`` the job goes back to ``pending`` with an
    exponential backoff ``not_before``; at or past the cap it goes
    ``failed`` — and never touches ``videos.state`` (Story 26.7 D2).
    Returns the resulting status.
    """
    ts = now or _now()
    attempts = job.attempts + 1
    if attempts >= max_attempts:
        await conn.fetchrow(_FAIL_SQL, str(job.id), attempts, error, ts)
        return EnrichJobStatus.FAILED
    delay = compute_backoff(attempts)
    await conn.fetchrow(_RETRY_SQL, str(job.id), attempts, ts + timedelta(seconds=delay), error, ts)
    return EnrichJobStatus.PENDING
