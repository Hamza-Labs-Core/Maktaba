"""Stale-claim sweep SQL for the reaper (Story 6.6).

The reaper finds rows in the live-claim states (``claimed``,
``running``, ``resuming``) whose ``last_heartbeat_at`` is older than
``stale_claim_sec`` and flips them to ``paused`` with
``paused_reason = 'crash'``. This module owns only the SQL + payload
shape; the periodic loop and advisory-lock orchestration live in
:mod:`maktaba_pipeline.pipeline.reaper`.

The CTE form captures the prior state for the notify payload; the
``FOR UPDATE SKIP LOCKED`` inside the CTE prevents two reaper
instances from double-reaping the same row should the advisory lock
ever fail.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import datetime
from typing import Any
from uuid import UUID

from .jobs import DBConn
from .pubsub import JOBS_REAPED, get_bus

__all__ = [
    "REAPER_ADVISORY_LOCK_KEY",
    "ReapedRow",
    "reap_once",
]


# 32-bit advisory-lock key. Picked once and pinned by this comment so
# future refactors don't reuse the integer for another lock.
# 0x6A6F6273 = ASCII "jobs".
REAPER_ADVISORY_LOCK_KEY: int = 0x6A6F6273


@dataclass(slots=True, frozen=True)
class ReapedRow:
    """One row swept by the reaper. Used for logging + notify payloads."""

    id: int
    video_id: UUID | None
    stage: str
    prev_state: str
    paused_at_sec: float
    paused_at: datetime
    last_heartbeat_at: datetime


_REAP_SQL_PG = """
WITH stale AS (
    SELECT id, state AS prev_state
      FROM processing_jobs
     WHERE state IN ('claimed', 'running', 'resuming')
       AND last_heartbeat_at < now() - ($1 || ' seconds')::interval
     FOR UPDATE SKIP LOCKED
)
UPDATE processing_jobs pj
   SET state            = 'paused',
       paused_at        = now(),
       paused_at_sec    = pj.last_segment_end_sec,
       paused_reason    = 'crash',
       claimed_by       = NULL,
       pause_requested  = false
  FROM stale
 WHERE pj.id = stale.id
RETURNING pj.id, pj.video_id, pj.stage,
          stale.prev_state,
          pj.paused_at_sec, pj.paused_at, pj.last_heartbeat_at
"""

_REAP_SELECT_SQLITE = """
SELECT id, state AS prev_state, last_segment_end_sec, last_heartbeat_at,
       video_id, stage
  FROM processing_jobs
 WHERE state IN ('claimed', 'running', 'resuming')
   AND last_heartbeat_at IS NOT NULL
   AND datetime(last_heartbeat_at) <
       datetime('now', ? || ' seconds')
"""

_REAP_UPDATE_SQLITE = """
UPDATE processing_jobs
   SET state            = 'paused',
       paused_at        = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       paused_at_sec    = last_segment_end_sec,
       paused_reason    = 'crash',
       claimed_by       = NULL,
       pause_requested  = 0
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
RETURNING paused_at, paused_at_sec
"""


async def reap_once(db: DBConn, *, stale_claim_sec: float) -> list[ReapedRow]:
    """One reaper tick. Returns the rows that were reaped this call.

    Postgres: single CTE UPDATE, ``RETURNING`` the previous state and
    the new ``paused_at_sec`` for each row. Fires
    ``pg_notify('jobs.reaped', payload)`` once per row.

    SQLite: SELECT then per-row UPDATE; publishes on the in-process
    bus once per row. The two-statement form is acceptable for SQLite
    because the reaper holds the process-local lock from
    :class:`maktaba_pipeline.pipeline.reaper.Reaper` while it runs.
    """
    if db.dialect == "postgres":
        return await _reap_postgres(db, stale_claim_sec)
    return await _reap_sqlite(db, stale_claim_sec)


async def _reap_postgres(db: DBConn, stale_claim_sec: float) -> list[ReapedRow]:
    fetch = getattr(db, "fetch", None)
    if fetch is None:
        # Fall back to the fetchrow-only Protocol by issuing one
        # statement and parsing whatever the helper returns.
        rows_iter = await db.fetchrow(_REAP_SQL_PG, str(stale_claim_sec))
        rows = [rows_iter] if rows_iter is not None else []
    else:
        rows = list(await fetch(_REAP_SQL_PG, str(stale_claim_sec)))

    out: list[ReapedRow] = []
    for r in rows:
        if r is None:
            continue
        reaped = ReapedRow(
            id=int(r["id"]),
            video_id=_optional_uuid(r["video_id"]),
            stage=str(r["stage"]),
            prev_state=str(r["prev_state"]),
            paused_at_sec=float(r["paused_at_sec"] or 0.0),
            paused_at=_to_datetime(r["paused_at"]),
            last_heartbeat_at=_to_datetime(r["last_heartbeat_at"]),
        )
        out.append(reaped)
        await _emit_notify_pg(db, reaped)
    return out


async def _reap_sqlite(db: DBConn, stale_claim_sec: float) -> list[ReapedRow]:
    # `datetime('now', '-90 seconds')` — SQLite expects a signed offset.
    offset = f"-{stale_claim_sec} seconds"
    fetch = getattr(db, "fetch", None)
    if fetch is not None:
        candidates = list(await fetch(_REAP_SELECT_SQLITE, offset))
    else:
        # The minimal Protocol only has fetchrow; iterate row-by-row.
        # This branch is exercised by the unit-test fake.
        candidates = []
        seen_ids: set[int] = set()
        while True:
            row = await db.fetchrow(_REAP_SELECT_SQLITE, offset)
            if row is None:
                break
            row_id = int(row["id"])
            if row_id in seen_ids:
                break
            seen_ids.add(row_id)
            candidates.append(row)

    out: list[ReapedRow] = []
    for cand in candidates:
        updated = await db.fetchrow(_REAP_UPDATE_SQLITE, int(cand["id"]))
        if updated is None:
            # Row state changed between the SELECT and the UPDATE.
            continue
        reaped = ReapedRow(
            id=int(cand["id"]),
            video_id=_optional_uuid(cand["video_id"]),
            stage=str(cand["stage"]),
            prev_state=str(cand["prev_state"]),
            paused_at_sec=float(updated["paused_at_sec"] or 0.0),
            paused_at=_to_datetime(updated["paused_at"]),
            last_heartbeat_at=_to_datetime(cand["last_heartbeat_at"]),
        )
        out.append(reaped)
        get_bus().publish(
            JOBS_REAPED,
            {
                "id": reaped.id,
                "prev_state": reaped.prev_state,
                "paused_at_sec": reaped.paused_at_sec,
            },
        )
    return out


async def _emit_notify_pg(db: DBConn, reaped: ReapedRow) -> None:
    payload = json.dumps(
        {
            "id": reaped.id,
            "prev_state": reaped.prev_state,
            "paused_at_sec": reaped.paused_at_sec,
        }
    )
    execute = getattr(db, "execute", None)
    if execute is None:
        # Best-effort fall-back via fetchrow — the test fake exposes
        # only fetchrow, so we issue the notify via SELECT pg_notify.
        await db.fetchrow("SELECT pg_notify($1, $2)", "jobs.reaped", payload)
    else:
        await execute("SELECT pg_notify($1, $2)", "jobs.reaped", payload)


def _optional_uuid(value: Any) -> UUID | None:
    """Decode a nullable UUID column (slot 0058 made both scope columns
    nullable: ``video_id`` is null on scan rows, ``library_id`` is null
    on per-video rows). The reap SELECTs do not stage-filter, so a
    crashed library-scoped scan row (``video_id`` null) is a reap
    candidate and must decode cleanly."""
    if value is None:
        return None
    if isinstance(value, UUID):
        return value
    return UUID(str(value))


def _to_datetime(value: Any) -> datetime:
    if isinstance(value, datetime):
        return value
    if value is None:
        from datetime import UTC

        return datetime.now(UTC)
    return datetime.fromisoformat(str(value))
