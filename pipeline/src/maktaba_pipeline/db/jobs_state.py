"""State-transition helpers for ``processing_jobs`` (Stories 6.4 + 6.5).

This module owns the worker-side mutations that are *not* the claim
itself (which lives in :mod:`.jobs_claim`):

- :func:`read_flags` — cheap PK SELECT for the per-segment cooperative
  pause/cancel check (Story 6.4).
- :func:`mark_paused`, :func:`mark_cancelled`, :func:`mark_done` —
  cooperative state-flip UPDATEs that the stage handler runs after a
  per-segment commit observes a request flag.
- :func:`mark_failed_or_retry` — exception-path transition that decides
  retry vs terminal failure based on ``attempts`` / ``max_attempts``
  and the ``StageError.retryable`` flag (Story 6.5).
- :func:`retry_failed` — operator-driven reset of a ``failed`` row
  (the worker analogue of the Go API's ``POST /api/jobs/{id}/retry``
  handler — exposed here so the Python pipeline can reset rows in
  tests and admin scripts without an HTTP round-trip).

Every UPDATE in this module gates by both ``id`` and an expected
``state`` predicate so a concurrent transition (force-pause from the
API, reaper sweep) never silently clobbers a row in an unexpected
state; the ones a worker runs on its own job also by its claim
(:mod:`.claim_owner`), so a machine that lost the home past the claim
lease does not end a job the home has since handed to another.
Returning ``None`` from these helpers means "the row was not in
the expected state" — the caller decides whether that's an error or
a benign no-op.
"""

from __future__ import annotations

import json
import random
from dataclasses import dataclass
from datetime import datetime
from typing import Any

from . import claim_owner
from .jobs import DBConn

__all__ = [
    "FailureOutcome",
    "FlagState",
    "StageError",
    "mark_cancelled",
    "mark_done",
    "mark_failed_or_retry",
    "mark_paused",
    "read_flags",
    "retry_failed",
]


@dataclass(slots=True, frozen=True)
class FlagState:
    """Snapshot of the two cooperative request flags."""

    pause: bool
    cancel: bool


@dataclass(slots=True, frozen=True)
class StageError:
    """Structured failure descriptor produced by a stage handler.

    ``retryable`` is the stage's judgement: a network blip is True, an
    OOM is False. The retry decision in :func:`mark_failed_or_retry`
    short-circuits to ``failed`` when ``retryable=False`` regardless of
    attempt count.
    """

    kind: str
    message: str
    traceback: str | None = None
    retryable: bool = True


@dataclass(slots=True, frozen=True)
class FailureOutcome:
    """Result of :func:`mark_failed_or_retry`.

    ``state`` is one of:
    - ``'pending'`` — retry queued, ``not_before`` set to the backoff deadline.
    - ``'failed'`` — terminal; preserve the error JSON for postmortems.
    - ``'noop'`` — the row had already moved to a non-live state (force-
      paused, cancelled, reaped). The caller should log at INFO and
      exit the stage cleanly without raising.
    """

    state: str
    not_before: datetime | None = None
    #: Epic 40, on an engine connection: the failure's class, and what it is parked on
    failure_class: str | None = None
    parked_on: str | None = None


# ---------------------------------------------------------------------------
# Flag observation (Story 6.4)
# ---------------------------------------------------------------------------

_READ_FLAGS_PG = """
SELECT pause_requested, cancel_requested
  FROM processing_jobs
 WHERE id = $1
"""

_READ_FLAGS_SQLITE = """
SELECT pause_requested, cancel_requested
  FROM processing_jobs
 WHERE id = ?
"""


async def read_flags(db: DBConn, *, job_id: int) -> FlagState:
    """Read the pause/cancel request flags for one job by primary key.

    Hits the PK index, < 1 ms warm. The worker calls this between
    per-segment commits; an absent row is interpreted as a cancel
    (the FK CASCADE removed it via a video deletion) so the worker
    exits cleanly rather than looping forever.
    """
    sql = _READ_FLAGS_PG if db.dialect == "postgres" else _READ_FLAGS_SQLITE
    row = await db.fetchrow(sql, job_id)
    if row is None:
        return FlagState(pause=False, cancel=True)
    return FlagState(
        pause=bool(row["pause_requested"]),
        cancel=bool(row["cancel_requested"]),
    )


# ---------------------------------------------------------------------------
# Cooperative state transitions (Story 6.4)
# ---------------------------------------------------------------------------

_MARK_PAUSED_PG = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = $2,
       paused_reason    = $3,
       pause_requested  = false,
       claimed_by       = NULL
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
   AND ($4::text IS NULL OR claimed_by = $4::text)
RETURNING id, state, paused_at_sec
"""

_MARK_PAUSED_SQLITE = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       paused_at_sec    = ?,
       paused_reason    = ?,
       pause_requested  = 0,
       claimed_by       = NULL
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
   AND (? IS NULL OR claimed_by = ?)
RETURNING id, state, paused_at_sec
"""


async def mark_paused(
    db: DBConn,
    *,
    job_id: int,
    at_sec: float,
    reason: str = "user",
) -> Any | None:
    """Cooperative pause from inside a running stage handler.

    ``reason`` is one of ``'user'`` (the API set ``pause_requested``),
    ``'shutdown'`` (the worker observed its own SIGTERM via
    :class:`maktaba_pipeline.pipeline.shutdown.ShutdownOrchestrator`),
    or ``'crash'`` (the reaper). The schema does not constrain the
    set; callers should stick to the canonical four (``user``,
    ``shutdown``, ``crash``, ``maintenance``) so observers can switch
    on it without surprise values.
    """
    owner = claim_owner.current()
    if db.dialect == "postgres":
        out = await db.fetchrow(_MARK_PAUSED_PG, job_id, at_sec, reason, owner)
    else:
        out = await db.fetchrow(_MARK_PAUSED_SQLITE, at_sec, reason, job_id, owner, owner)
    if out is not None and _hooked(db):
        from ..engine.hooks import on_paused

        await on_paused(db, job_id, reason)
    return out


_MARK_CANCELLED_PG = """
UPDATE processing_jobs
   SET state            = 'cancelled',
       finished_at      = now(),
       claimed_by       = NULL,
       cancel_requested = false
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming', 'paused', 'pending')
   AND ($2::text IS NULL OR claimed_by IS NULL OR claimed_by = $2::text)
RETURNING id, state
"""

_MARK_CANCELLED_SQLITE = """
UPDATE processing_jobs
   SET state            = 'cancelled',
       finished_at      = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       claimed_by       = NULL,
       cancel_requested = 0
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming', 'paused', 'pending')
   AND (? IS NULL OR claimed_by IS NULL OR claimed_by = ?)
RETURNING id, state
"""


async def mark_cancelled(db: DBConn, *, job_id: int) -> Any | None:
    """Terminal cancel transition triggered by the worker (a job another worker now
    holds is left to it)."""
    sql = _MARK_CANCELLED_PG if db.dialect == "postgres" else _MARK_CANCELLED_SQLITE
    owner = claim_owner.current()
    args = (job_id, owner) if db.dialect == "postgres" else (job_id, owner, owner)
    if not _hooked(db):
        return await db.fetchrow(sql, *args)
    from ..engine.hooks import on_cancelled

    async with db.transaction():
        out = await db.fetchrow(sql, *args)
        if out is not None:
            await on_cancelled(db, job_id)
    return out


_MARK_DONE_PG = """
UPDATE processing_jobs
   SET state            = 'done',
       finished_at      = now(),
       claimed_by       = NULL
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
   AND ($2::text IS NULL OR claimed_by = $2::text)
RETURNING id, state
"""

_MARK_DONE_SQLITE = """
UPDATE processing_jobs
   SET state            = 'done',
       finished_at      = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       claimed_by       = NULL
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
   AND (? IS NULL OR claimed_by = ?)
RETURNING id, state
"""


async def mark_done(db: DBConn, *, job_id: int) -> Any | None:
    """Terminal success transition."""
    owner = claim_owner.current()
    if db.dialect == "postgres":
        return await db.fetchrow(_MARK_DONE_PG, job_id, owner)
    return await db.fetchrow(_MARK_DONE_SQLITE, job_id, owner, owner)


# ---------------------------------------------------------------------------
# Failure / retry transition (Story 6.5)
# ---------------------------------------------------------------------------

_FAIL_OR_RETRY_PG = """
UPDATE processing_jobs
   SET state         = $2,
       not_before    = $3,
       claimed_by    = NULL,
       error         = $4,
       finished_at   = $5
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
   AND ($6::text IS NULL OR claimed_by = $6::text)
RETURNING id, state, not_before
"""

_FAIL_OR_RETRY_SQLITE = """
UPDATE processing_jobs
   SET state         = ?,
       not_before    = ?,
       claimed_by    = NULL,
       error         = ?,
       finished_at   = ?
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
   AND (? IS NULL OR claimed_by = ?)
RETURNING id, state, not_before
"""

_READ_ATTEMPTS_PG = """
SELECT attempts, max_attempts FROM processing_jobs WHERE id = $1
"""

_READ_ATTEMPTS_SQLITE = """
SELECT attempts, max_attempts FROM processing_jobs WHERE id = ?
"""


async def mark_failed_or_retry(
    db: DBConn,
    *,
    job_id: int,
    error: StageError,
    rng: random.Random | None = None,
    now: datetime | None = None,
) -> FailureOutcome:
    """Decide retry-vs-terminal-fail and write the row.

    Reads ``attempts`` / ``max_attempts`` first (one cheap SELECT) so
    the backoff can be computed in Python; the UPDATE then writes the
    decided values in a single round-trip. If the row has already
    moved to a non-live state (force-paused, cancelled, reaped),
    returns ``FailureOutcome(state='noop')`` so the caller can log
    INFO and exit cleanly.

    ``now`` and ``rng`` are dependency-injection seams for tests.

    On an engine connection (``db.engine_hooks``) the whole transition runs in
    one transaction together with the engine bookkeeping: the normalised
    ``error_cause``, the ``job_events`` / ``events`` rows and, when a trigger
    arrived mid-run, the follow-up job (``engine/hooks.py``).
    """
    if not _hooked(db):
        return await _fail_or_retry(db, job_id, error, rng, now)
    from ..engine.hooks import on_failure

    async with db.transaction():
        outcome = await _fail_or_retry_engine(db, job_id, error, rng, now)
        if outcome.state in ("pending", "failed"):
            await on_failure(
                db,
                job_id,
                kind=error.kind,
                message=error.message,
                new_state=outcome.state,
                not_before=outcome.not_before,
                failure_class=outcome.failure_class,
                parked_on=outcome.parked_on,
            )
    return outcome


_FAIL_ENGINE = """
UPDATE processing_jobs
   SET state         = $2,
       not_before    = $3,
       claimed_by    = NULL,
       error         = $4,
       finished_at   = $5,
       failure_class = $6,
       parked_on     = $7,
       attempts      = CASE WHEN $8 THEN {refund} ELSE attempts END,
       max_attempts  = COALESCE($9, max_attempts)
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
   AND {owner}
RETURNING id, state, not_before
"""


async def _fail_or_retry_engine(
    db: DBConn,
    job_id: int,
    error: StageError,
    rng: random.Random | None,
    when: datetime | None,
) -> FailureOutcome:
    """Epic 40: the failure's class decides (``engine/failure_policy.py``): park without
    spending the attempt, retry within the class budget, or fail."""
    from ..engine.failure_policy import decide, not_before
    from ..engine.sql import is_pg, to_ts

    row = await db.fetchrow(
        "SELECT attempts, max_attempts, stage, video_id FROM processing_jobs WHERE id = $1",
        job_id,
    )
    if row is None:
        return FailureOutcome(state="noop")
    decision = await decide(
        db,
        stage=str(row["stage"]),
        video_id=row["video_id"],
        kind=error.kind,
        message=error.message,
        retryable=error.retryable,
        attempts=int(row["attempts"]),
        max_attempts=int(row["max_attempts"]),
        rng=rng,
    )
    at = _now(when)
    nb = not_before(decision, at)
    pending = decision.action in ("park", "retry")
    pg = is_pg(db)
    err_json = json.dumps(
        {
            "kind": error.kind,
            "message": error.message,
            "traceback": error.traceback,
            "retryable": error.retryable,
        }
    )
    sql = _FAIL_ENGINE.replace(
        "{refund}", "GREATEST(attempts - 1, 0)" if pg else "MAX(attempts - 1, 0)"
    ).replace("{owner}", claim_owner.guard(db, 10))
    out = await db.fetchrow(
        sql,
        job_id,
        "pending" if pending else "failed",
        to_ts(db, nb),
        err_json,
        None if pending else to_ts(db, at),
        decision.failure_class,
        decision.parked_on,
        (decision.action == "park") if pg else int(decision.action == "park"),
        decision.max_attempts,
        claim_owner.current(),
    )
    if out is None:
        return FailureOutcome(state="noop")
    nb_raw = out["not_before"]
    got: datetime | None
    if nb_raw is None or isinstance(nb_raw, datetime):
        got = nb_raw
    else:
        got = datetime.fromisoformat(str(nb_raw).replace("Z", "+00:00"))
    return FailureOutcome(
        state=str(out["state"]),
        not_before=got,
        failure_class=decision.failure_class,
        parked_on=decision.parked_on,
    )


def _hooked(db: DBConn) -> bool:
    return bool(getattr(db, "engine_hooks", False))


async def _fail_or_retry(
    db: DBConn,
    job_id: int,
    error: StageError,
    rng: random.Random | None,
    now: datetime | None,
) -> FailureOutcome:
    sql_read = _READ_ATTEMPTS_PG if db.dialect == "postgres" else _READ_ATTEMPTS_SQLITE
    row = await db.fetchrow(sql_read, job_id)
    if row is None:
        return FailureOutcome(state="noop")

    attempts = int(row["attempts"])
    max_attempts = int(row["max_attempts"])
    err_json = json.dumps(
        {
            "kind": error.kind,
            "message": error.message,
            "traceback": error.traceback,
            "retryable": error.retryable,
        }
    )

    will_retry = error.retryable and attempts < max_attempts

    if will_retry:
        # Lazy import to avoid the db ↔ pipeline package import cycle.
        from ..pipeline.backoff import compute_backoff

        seconds = compute_backoff(attempts, rng=rng)
        not_before = _now(now)
        not_before_value = _add_seconds(not_before, seconds)
        new_state = "pending"
        finished_at: datetime | None = None
    else:
        not_before_value = None
        new_state = "failed"
        finished_at = _now(now)

    sql = _FAIL_OR_RETRY_PG if db.dialect == "postgres" else _FAIL_OR_RETRY_SQLITE
    owner = claim_owner.current()
    if db.dialect == "postgres":
        out = await db.fetchrow(
            sql,
            job_id,
            new_state,
            not_before_value,
            err_json,
            finished_at,
            owner,
        )
    else:
        out = await db.fetchrow(
            sql,
            new_state,
            _to_iso(not_before_value),
            err_json,
            _to_iso(finished_at),
            job_id,
            owner,
            owner,
        )

    if out is None:
        return FailureOutcome(state="noop")
    state = str(out["state"])
    nb_raw = out["not_before"]
    nb: datetime | None
    if nb_raw is None:
        nb = None
    elif isinstance(nb_raw, datetime):
        nb = nb_raw
    else:
        nb = datetime.fromisoformat(str(nb_raw))
    return FailureOutcome(state=state, not_before=nb)


# ---------------------------------------------------------------------------
# Operator-driven retry (Story 6.5)
# ---------------------------------------------------------------------------

_RETRY_FAILED_PG = """
UPDATE processing_jobs
   SET state         = 'pending',
       attempts      = 0,
       not_before    = NULL,
       error         = NULL,
       finished_at   = NULL,
       claimed_by    = NULL,
       claimed_at    = NULL
 WHERE id = $1
   AND state = 'failed'
RETURNING id, state
"""

_RETRY_FAILED_SQLITE = """
UPDATE processing_jobs
   SET state         = 'pending',
       attempts      = 0,
       not_before    = NULL,
       error         = NULL,
       finished_at   = NULL,
       claimed_by    = NULL,
       claimed_at    = NULL
 WHERE id = ?
   AND state = 'failed'
RETURNING id, state
"""


async def retry_failed(db: DBConn, *, job_id: int) -> Any | None:
    """Reset a ``failed`` row back to ``pending``.

    Returns ``None`` if the row is missing or not in ``failed`` —
    callers should map that to a 409 from the HTTP layer.
    """
    sql = _RETRY_FAILED_PG if db.dialect == "postgres" else _RETRY_FAILED_SQLITE
    return await db.fetchrow(sql, job_id)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _now(when: datetime | None) -> datetime:
    if when is not None:
        return when
    from datetime import UTC

    return datetime.now(UTC)


def _add_seconds(when: datetime, seconds: float) -> datetime:
    from datetime import timedelta

    return when + timedelta(seconds=seconds)


def _to_iso(value: datetime | None) -> str | None:
    if value is None:
        return None
    return value.isoformat()
