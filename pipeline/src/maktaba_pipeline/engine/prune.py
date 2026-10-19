"""Retention (plan 06-11 section 6.4); run by the reaper loop every hour.

Deletes run in batches of 5000 rows per transaction. ``health_samples`` is not a worker's
any more: the home's hub writes it as a cache and folds and prunes it (design-agent.md
section 7.9).
"""

from __future__ import annotations

import asyncio
import contextlib
from dataclasses import asdict, dataclass
from datetime import datetime, timedelta
from typing import Any

from ..log import get_logger
from .sql import now, to_ts

__all__ = ["PruneReport", "prune_once", "run_pruner"]

_log = get_logger()

BATCH = 5000


@dataclass(frozen=True, slots=True)
class PruneReport:
    job_events: int = 0
    jobs: int = 0
    channel_plans: int = 0
    workers: int = 0


async def _delete_batches(db: Any, sql: str, *args: Any) -> int:
    """Run a ``DELETE ... WHERE id IN (SELECT ... LIMIT n)`` until a batch comes back short."""
    total = 0
    while True:
        async with db.transaction():
            n = await _count_deleted(db, sql, *args)
        total += n
        if n < BATCH:
            return total


async def _count_deleted(db: Any, sql: str, *args: Any) -> int:
    rows = await db.fetch(sql + " RETURNING 1", *args)
    return len(rows)


async def prune_once(db: Any, *, at: datetime | None = None) -> PruneReport:
    t = at or now()
    events = 0
    for level, days in (("debug", 7), ("info", 30)):
        events += await _delete_batches(
            db,
            "DELETE FROM job_events WHERE id IN (SELECT id FROM job_events"
            f" WHERE level = '{level}' AND at < $1 LIMIT {BATCH})",
            to_ts(db, t - timedelta(days=days)),
        )
    events += await _delete_batches(
        db,
        "DELETE FROM job_events WHERE id IN (SELECT id FROM job_events"
        f" WHERE level IN ('warn','error') AND at < $1 LIMIT {BATCH})",
        to_ts(db, t - timedelta(days=90)),
    )
    # terminal rows older than 90 days, but a ``done`` row only when a newer ``done`` row
    # exists for the same subject and stage (skip checks need the latest one, forever)
    jobs = await _delete_batches(
        db,
        "DELETE FROM processing_jobs WHERE id IN (SELECT j.id FROM processing_jobs j"
        " WHERE j.state IN ('done','failed','cancelled') AND j.finished_at < $1"
        " AND (j.state <> 'done' OR EXISTS (SELECT 1 FROM processing_jobs k"
        "   WHERE k.subject_kind = j.subject_kind AND k.subject_id = j.subject_id"
        "   AND k.stage = j.stage AND k.state = 'done' AND k.id > j.id))"
        f" LIMIT {BATCH})",
        to_ts(db, t - timedelta(days=90)),
    )
    plans = await _count_deleted(
        db,
        "DELETE FROM channel_plans WHERE id NOT IN"
        " (SELECT id FROM channel_plans ORDER BY id DESC LIMIT 20)",
    )
    workers = await _count_deleted(
        db, "DELETE FROM workers WHERE last_seen_at < $1", to_ts(db, t - timedelta(days=1))
    )
    # a transcription's saved progress outlives its job only if the job was dropped
    # (failed for good, cancelled, its file changed): a week on, it is not resumed
    await db.execute(
        "DELETE FROM transcribe_checkpoints WHERE updated_at < $1",
        to_ts(db, t - timedelta(days=7)),
    )
    return PruneReport(events, jobs, plans, workers)


async def run_pruner(db: Any, stop: asyncio.Event, *, interval_sec: float = 3600.0) -> None:
    while not stop.is_set():
        try:
            report = await prune_once(db)
            _log.info("pruned", **asdict(report))
        except Exception:  # noqa: BLE001 - retention must never take the worker down
            _log.exception("prune_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval_sec)
