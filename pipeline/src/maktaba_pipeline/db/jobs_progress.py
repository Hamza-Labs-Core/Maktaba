"""Progress and heartbeat UPDATEs on ``processing_jobs`` (Story 6.3).

A worker proves it's alive by writing to the same row it claimed. Two
flavours of write live here:

- :func:`tick_progress` — bumps the segment counters AND
  ``last_heartbeat_at`` in a single UPDATE. Fires
  ``pg_notify('jobs.progress', payload)`` from the trigger landed by
  ``shared/db/migrations/0008_jobs_progress_notify.sql``. Called by the
  per-segment commit on the transcribe stage (Epic 3 Story 3.6) and any
  other stage that has natural progress cadence.
- :func:`tick_heartbeat` — UPDATE that only touches
  ``last_heartbeat_at``. Fires ``pg_notify('jobs.heartbeat', payload)``.
  Called by :class:`maktaba_pipeline.pipeline.heartbeat.HeartbeatTask`
  for stages without per-segment cadence (probe, index, thumbnail) and
  alongside ``tick_progress`` for the transcribe stage so a 60 s ffmpeg
  decode in the middle of one segment doesn't trip the reaper.

The split rule, in plain English (per plan-06-03 §1):

- **Progress tick** — any UPDATE that moves the segment counter,
  ``processed_seconds``, or ``last_segment_end_sec``. Fires
  ``jobs.progress`` *and* doubles as a heartbeat.
- **Heartbeat-only tick** — UPDATE that touches only
  ``last_heartbeat_at``. Fires ``jobs.heartbeat``. Never fires
  ``jobs.progress``.

Both UPDATEs gate on ``state IN ('claimed', 'running', 'resuming')`` so
a stale tick from a worker that hasn't observed a force-pause yet does
not bump ``last_heartbeat_at`` on a paused/terminal row (which would
defeat the reaper's staleness check).
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

from .jobs import DBConn
from .pubsub import JOBS_HEARTBEAT, JOBS_PROGRESS, get_bus

__all__ = [
    "ProgressTick",
    "tick_heartbeat",
    "tick_progress",
]


@dataclass(slots=True, frozen=True)
class ProgressTick:
    """Inputs to a single progress UPDATE.

    Per architecture §7.6, ``processed_seconds`` is the absolute count
    (``segment.end - seek_from``), not a delta. The caller computes the
    value, so a resume after pause restarts the counter from zero
    relative to the new ``seek_from``. ``segments_completed_delta``
    remains additive because it counts across the full lifetime of the
    job.

    Fields default to "no change" so a caller can bump a single column
    without specifying the rest. ``last_segment_end_sec``,
    ``realtime_factor``, and ``estimated_remaining_sec`` are
    ``None`` → "leave existing value" via ``COALESCE`` in the SQL.
    """

    job_id: int
    processed_seconds: float = 0.0
    segments_completed_delta: int = 0
    last_segment_end_sec: float | None = None
    realtime_factor: float | None = None
    estimated_remaining_sec: float | None = None


# Postgres path. The trigger from migration 0008 fires `jobs.progress`
# when `progress_updated_at` advances. The state predicate stops a
# stale tick from bumping a paused/terminal row.
_PROGRESS_SQL_PG = """
UPDATE processing_jobs
   SET processed_seconds        = $2,
       segments_completed       = segments_completed + $3,
       last_segment_end_sec     = COALESCE($4, last_segment_end_sec),
       realtime_factor          = COALESCE($5, realtime_factor),
       estimated_remaining_sec  = COALESCE($6, estimated_remaining_sec),
       progress_updated_at      = now(),
       last_heartbeat_at        = now()
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id, video_id, stage, state,
          last_segment_end_sec, processed_seconds,
          total_duration_seconds, segments_completed,
          realtime_factor, estimated_remaining_sec,
          progress_updated_at
"""

# SQLite parity. Same shape with `?` placeholders and `datetime('now')`
# instead of `now()`. SQLite has no triggers that publish to a process
# bus, so the helper publishes manually after the UPDATE commits.
_PROGRESS_SQL_SQLITE = """
UPDATE processing_jobs
   SET processed_seconds        = ?,
       segments_completed       = segments_completed + ?,
       last_segment_end_sec     = COALESCE(?, last_segment_end_sec),
       realtime_factor          = COALESCE(?, realtime_factor),
       estimated_remaining_sec  = COALESCE(?, estimated_remaining_sec),
       progress_updated_at      = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       last_heartbeat_at        = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id, video_id, stage, state,
          last_segment_end_sec, processed_seconds,
          total_duration_seconds, segments_completed,
          realtime_factor, estimated_remaining_sec,
          progress_updated_at
"""


_HEARTBEAT_SQL_PG = """
UPDATE processing_jobs
   SET last_heartbeat_at = now()
 WHERE id = $1
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id, stage, last_heartbeat_at
"""

_HEARTBEAT_SQL_SQLITE = """
UPDATE processing_jobs
   SET last_heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
 WHERE id = ?
   AND state IN ('claimed', 'running', 'resuming')
RETURNING id, stage, last_heartbeat_at
"""


async def tick_progress(db: DBConn, t: ProgressTick) -> None:
    """One UPDATE that bumps progress counters AND ``last_heartbeat_at``.

    The Postgres trigger fires ``jobs.progress`` exactly once. The
    SQLite path publishes the equivalent payload on the in-process bus
    after the UPDATE commits. Caller is the per-stage commit (e.g.,
    transcribe's per-segment commit, Epic 3 Story 3.6).

    No-ops silently when the row has moved to a non-live state — the
    worker's per-segment cancel/pause check (Story 6.4) will see the
    transition on the next iteration.
    """
    if db.dialect == "postgres":
        sql = _PROGRESS_SQL_PG
        args: tuple[Any, ...] = (
            t.job_id,
            t.processed_seconds,
            t.segments_completed_delta,
            t.last_segment_end_sec,
            t.realtime_factor,
            t.estimated_remaining_sec,
        )
    else:
        sql = _PROGRESS_SQL_SQLITE
        args = (
            t.processed_seconds,
            t.segments_completed_delta,
            t.last_segment_end_sec,
            t.realtime_factor,
            t.estimated_remaining_sec,
            t.job_id,
        )

    row = await db.fetchrow(sql, *args)
    if row is None:
        return
    if db.dialect == "sqlite":
        get_bus().publish(JOBS_PROGRESS, _build_progress_payload(row))


async def tick_heartbeat(db: DBConn, *, job_id: int) -> None:
    """One UPDATE that only sets ``last_heartbeat_at = now()``.

    Fires ``jobs.heartbeat`` (consumed by the reaper, never the UI) and
    is a no-op on rows outside the live-claim states.
    """
    sql = _HEARTBEAT_SQL_PG if db.dialect == "postgres" else _HEARTBEAT_SQL_SQLITE
    arg = job_id if db.dialect == "postgres" else job_id
    row = await db.fetchrow(sql, arg)
    if row is None:
        return
    if db.dialect == "sqlite":
        get_bus().publish(
            JOBS_HEARTBEAT,
            {
                "id": int(row["id"]),
                "stage": str(row["stage"]),
                "last_heartbeat_at": _coerce_iso(row["last_heartbeat_at"]),
            },
        )


def _build_progress_payload(row: Any) -> dict[str, Any]:
    """Match the Postgres trigger's ``jobs.progress`` payload shape exactly.

    Architecture §7.10 pins this contract; the WS consumer parses it
    without branching on dialect. Only the SQLite helper builds it
    here — the Postgres trigger emits the same JSON via
    ``json_build_object``.
    """
    return {
        "id": int(row["id"]),
        "video_id": str(row["video_id"]),
        "stage": str(row["stage"]),
        "state": str(row["state"]),
        "last_segment_end_sec": _opt_float(row["last_segment_end_sec"]),
        "processed_seconds": float(row["processed_seconds"]),
        "total_duration_seconds": _opt_float(row["total_duration_seconds"]),
        "segments_completed": int(row["segments_completed"]),
        "realtime_factor": _opt_float(row["realtime_factor"]),
        "estimated_remaining_sec": _opt_float(row["estimated_remaining_sec"]),
        "updated_at": _coerce_iso(row["progress_updated_at"]),
    }


def _opt_float(value: Any) -> float | None:
    return None if value is None else float(value)


def _coerce_iso(value: Any) -> str:
    if value is None:
        return datetime.now(UTC).isoformat()
    if isinstance(value, datetime):
        return value.isoformat()
    return str(value)


# Re-export json so the SQLite payload stays serializable without an
# extra import in the heartbeat task. (Imported lazily inside callers
# to keep this module's import cost minimal.)
_ = json
