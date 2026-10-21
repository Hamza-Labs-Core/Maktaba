"""Pool-aware claiming (plan 06-11 section 5; fixes F10).

Pools are resource classes stored in ``worker_pools``. ``global`` pools (NAS
reads, Plex, dizqueTV, YouTube) are enforced *in the claim SQL* across every
process and host, under a per-pool advisory lock. ``per_worker`` pools (cpu,
gpu) are enforced by in-process semaphores sized from the same table.

A job is only offered to a worker whose ``stages`` list contains the job's
stage (multi-host workers, notes N2): the list is a parameter of the claim.
"""

from __future__ import annotations

import json
import os
from collections.abc import Sequence
from dataclasses import dataclass
from datetime import datetime
from typing import Any
from uuid import UUID

from ..db.jobs import DBConn, Job, JobState, Stage
from .sql import is_pg, loads, parse_ts

__all__ = [
    "PoolConfig",
    "claim_for_pool",
    "effective_size",
    "load_pools",
    "row_to_job",
]


@dataclass(frozen=True, slots=True)
class PoolConfig:
    pool: str
    scope: str
    size: int | None
    default_size: int | None

    @property
    def effective_size(self) -> int:
        return effective_size(self)


def effective_size(pool: PoolConfig) -> int:
    """Configured size; for an unset per-worker pool ``cores - 1`` (minimum 1)."""
    if pool.size is not None:
        return pool.size
    if pool.default_size is not None:
        return pool.default_size
    if pool.scope == "per_worker":
        return max(1, (os.cpu_count() or 2) - 1)
    return 1


async def load_pools(db: Any) -> dict[str, PoolConfig]:
    rows = await db.fetch("SELECT pool, scope, size, default_size FROM worker_pools")
    return {
        r["pool"]: PoolConfig(
            r["pool"],
            r["scope"],
            None if r["size"] is None else int(r["size"]),
            None if r["default_size"] is None else int(r["default_size"]),
        )
        for r in rows
    }


_CLAIM_PG = """
UPDATE processing_jobs pj
   SET state             = 'claimed',
       claimed_by        = $1,
       claimed_at        = now(),
       last_heartbeat_at = now(),
       -- a parked job (Epic 40) taken by the backstop or after its release waits no more
       parked_on         = NULL,
       -- resuming a paused job (a worker stopped mid-run) is not a new attempt: counted,
       -- every restart pushed it past max_attempts and the claim broke the check
       attempts          = attempts + CASE WHEN state = 'paused' THEN 0 ELSE 1 END
 WHERE pj.id = (
   SELECT j.id
     FROM processing_jobs j
     JOIN job_stages s ON s.stage = j.stage
    WHERE s.pool = $2
      AND j.stage = ANY($3::text[])
      AND s.enabled
      AND NOT s.paused
      AND NOT COALESCE((SELECT paused FROM queue_control WHERE id = 1), false)
      AND j.state IN ('pending', 'paused')
      AND j.pause_requested = false
      AND j.cancel_requested = false
      AND (j.not_before IS NULL OR j.not_before <= now())
      AND (
            (SELECT scope FROM worker_pools WHERE pool = $2) = 'per_worker'
         OR (SELECT count(*)
               FROM processing_jobs b
               JOIN job_stages bs ON bs.stage = b.stage
              WHERE bs.pool = $2 AND b.state IN ('claimed', 'running', 'resuming'))
            < (SELECT COALESCE(size, default_size, 1) FROM worker_pools WHERE pool = $2)
      )
    ORDER BY j.priority, j.id
    FOR UPDATE OF j SKIP LOCKED
    LIMIT 1
 )
RETURNING pj.*
"""

_CLAIM_SQLITE_SELECT = """
SELECT j.id AS id
  FROM processing_jobs j
  JOIN job_stages s ON s.stage = j.stage
 WHERE s.pool = $1
   AND j.stage IN ({stages})
   AND s.enabled = 1
   AND s.paused = 0
   AND COALESCE((SELECT paused FROM queue_control WHERE id = 1), 0) = 0
   AND j.state IN ('pending', 'paused')
   AND j.pause_requested = 0
   AND j.cancel_requested = 0
   AND (j.not_before IS NULL OR datetime(j.not_before) <= datetime('now'))
   AND (
         (SELECT scope FROM worker_pools WHERE pool = $1) = 'per_worker'
      OR (SELECT count(*)
            FROM processing_jobs b
            JOIN job_stages bs ON bs.stage = b.stage
           WHERE bs.pool = $1 AND b.state IN ('claimed', 'running', 'resuming'))
         < (SELECT COALESCE(size, default_size, 1) FROM worker_pools WHERE pool = $1)
   )
 ORDER BY j.priority, j.id
 LIMIT 1
"""

_CLAIM_SQLITE_UPDATE = """
UPDATE processing_jobs
   SET state             = 'claimed',
       claimed_by        = $1,
       claimed_at        = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       last_heartbeat_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
       parked_on         = NULL,
       -- resuming a paused job (a worker stopped mid-run) is not a new attempt: counted,
       -- every restart pushed it past max_attempts and the claim broke the check
       attempts          = attempts + CASE WHEN state = 'paused' THEN 0 ELSE 1 END
 WHERE id = $2
   AND state IN ('pending', 'paused')
RETURNING *
"""


async def claim_for_pool(
    db: DBConn,
    *,
    worker_id: str,
    pool: str,
    stages: Sequence[str],
    scope: str | None = None,
) -> Job | None:
    """Atomically claim the best eligible job of ``pool`` among ``stages``.

    Eligibility: stage enabled and not paused, global pause off, not
    pause/cancel requested, ``not_before`` passed, and (for a global pool)
    fewer than ``size`` jobs of the pool already claimed or running anywhere.
    Order is ``priority`` then ``id``.
    """
    if not stages:
        raise ValueError("claim_for_pool: stages must not be empty")
    names = [str(s) for s in stages]
    if is_pg(db):
        if scope is None:
            row = await db.fetchrow("SELECT scope FROM worker_pools WHERE pool = $1", pool)
            scope = None if row is None else str(row["scope"])
        if scope == "global":
            # Serialise claimers of one global pool so the busy count cannot race.
            async with db.transaction():
                await db.execute(
                    "SELECT pg_advisory_xact_lock(hashtext($1))", f"maktaba.pool:{pool}"
                )
                claimed = await db.fetchrow(_CLAIM_PG, worker_id, pool, names)
        else:
            claimed = await db.fetchrow(_CLAIM_PG, worker_id, pool, names)
        return row_to_job(claimed) if claimed is not None else None

    placeholders = ",".join(f"${i + 2}" for i in range(len(names)))
    select_sql = _CLAIM_SQLITE_SELECT.replace("{stages}", placeholders)
    async with db.transaction():
        cand = await db.fetchrow(select_sql, pool, *names)
        if cand is None:
            return None
        claimed = await db.fetchrow(_CLAIM_SQLITE_UPDATE, worker_id, cand["id"])
    return row_to_job(claimed) if claimed is not None else None


# --------------------------------------------------------------------------- rows


def row_to_job(row: Any) -> Job:
    """Build a :class:`Job` from a row mapping, tolerating both drivers and old fakes."""
    g = _getter(row)
    return Job(
        id=int(row["id"]),
        video_id=_uuid(g("video_id")),
        library_id=_uuid(g("library_id")),
        stage=_stage(row["stage"]),
        state=JobState(row["state"]),
        priority=int(row["priority"]),
        attempts=int(row["attempts"]),
        max_attempts=int(row["max_attempts"]),
        claimed_by=_str(row["claimed_by"]),
        claimed_at=_dt(row["claimed_at"]),
        last_heartbeat_at=_dt(row["last_heartbeat_at"]),
        not_before=_dt(row["not_before"]),
        error=_str(row["error"]),
        total_duration_seconds=_float(row["total_duration_seconds"]),
        processed_seconds=float(row["processed_seconds"]),
        segments_completed=int(row["segments_completed"]),
        last_segment_end_sec=float(row["last_segment_end_sec"]),
        estimated_remaining_sec=_float(row["estimated_remaining_sec"]),
        realtime_factor=_float(row["realtime_factor"]),
        progress_updated_at=_dt(row["progress_updated_at"]),
        pause_requested=bool(row["pause_requested"]),
        cancel_requested=bool(row["cancel_requested"]),
        paused_at=_dt(row["paused_at"]),
        paused_at_sec=_float(row["paused_at_sec"]),
        paused_reason=_str(row["paused_reason"]),
        resumed_at=_dt(row["resumed_at"]),
        resume_count=int(row["resume_count"]),
        metrics=_json_dict(row["metrics"]),
        payload=_json_dict(row["payload"]),
        created_at=_dt(row["created_at"]) or datetime.min,
        finished_at=_dt(row["finished_at"]),
        subject_kind=str(g("subject_kind") or "video"),
        subject_id=_uuid(g("subject_id")),
        series_id=_uuid(g("series_id")),
        season_id=_uuid(g("season_id")),
        stage_version=_int(g("stage_version")),
        input_hash=_str(g("input_hash")),
        output_hash=_str(g("output_hash")),
        outcome=_str(g("outcome")),
        output_summary=_json_dict(g("output_summary")),
        force=bool(g("force") or False),
        force_cascade=bool(g("force_cascade") or False),
        reasons=list(loads(g("reasons")) or []),
        enqueue_count=int(g("enqueue_count") or 1),
        triggered_by=_int(g("triggered_by")),
        triggered_by_stage=_str(g("triggered_by_stage")),
        rerun_requested=bool(g("rerun_requested") or False),
        rerun_force=bool(g("rerun_force") or False),
        rerun_reasons=list(loads(g("rerun_reasons")) or []),
        propagated_at=_dt(g("propagated_at")),
        error_cause=_str(g("error_cause")),
        progress=_json_dict(g("progress")),
        started_at=_dt(g("started_at")),
    )


def _stage(value: Any) -> str:
    """Legacy stage names come back as :class:`Stage` members (callers compare by
    identity); any other stage is a plain string."""
    name = str(value)
    try:
        return Stage(name)
    except ValueError:
        return name


def _getter(row: Any) -> Any:
    def get(key: str) -> Any:
        try:
            return row[key]
        except (KeyError, IndexError):
            return None

    return get


def _uuid(v: Any) -> UUID | None:
    if v is None:
        return None
    return v if isinstance(v, UUID) else UUID(str(v))


def _dt(v: Any) -> datetime | None:
    return parse_ts(v)


def _str(v: Any) -> str | None:
    return None if v is None else str(v)


def _float(v: Any) -> float | None:
    return None if v is None else float(v)


def _int(v: Any) -> int | None:
    return None if v is None else int(v)


def _json_dict(v: Any) -> dict[str, Any] | None:
    if v is None:
        return None
    if isinstance(v, dict):
        return v
    parsed = json.loads(v)
    if not isinstance(parsed, dict):
        raise TypeError(f"expected JSON object, got {type(parsed).__name__}")
    return parsed
