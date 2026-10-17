"""The reconciler (plan 06-11 section 6.5).

Runs every 60 s on whichever worker holds the leader lock (Postgres advisory
lock; on SQLite there is one process, so it is always the leader). It repairs
and schedules:

1. ``done`` rows whose propagation never ran (a crash between a legacy
   handler's ``mark_done`` and the engine bookkeeping);
2. a scheduled ``youtube_sync`` refresh for libraries that have YouTube channels;
3. the version sweep: after a stage ``version`` bump, every subject whose last
   success ran at an older version is queued once (bulk priority 500).
"""

from __future__ import annotations

import asyncio
import contextlib
import os
from collections.abc import Mapping
from dataclasses import dataclass
from datetime import datetime, timedelta
from typing import Any
from uuid import UUID

from ..log import get_logger
from .enqueue import enqueue_job
from .graph import StageDef
from .propagate import after_legacy_done
from .sql import is_pg, now, to_ts

__all__ = ["ReconcileReport", "reconcile_once", "run_reconciler"]

_log = get_logger()

SUCCESS = "('changed','unchanged','skipped_unchanged','imported')"
SWEEP_BATCH = 1000
SWEEP_PRIORITY = 500
UNPROPAGATED_AFTER_SEC = 60
DEFAULT_YT_REFRESH_HOURS = 168


@dataclass(frozen=True, slots=True)
class ReconcileReport:
    leader: bool
    propagated: int = 0
    yt_refresh: int = 0
    version_sweep: int = 0


async def reconcile_once(
    db: Any,
    graph: Mapping[str, StageDef],
    *,
    yt_refresh_hours: float | None = None,
    at: datetime | None = None,
) -> ReconcileReport:
    """One reconciliation pass; returns what it did (``leader=False`` => nothing)."""
    t = at or now()
    async with db.transaction():
        if is_pg(db):
            got = await db.fetchval(
                "SELECT pg_try_advisory_xact_lock(hashtext('maktaba.reconciler'))"
            )
            if not got:
                return ReconcileReport(leader=False)
        propagated = await _repair_unpropagated(db, graph, t)
        refresh = await _youtube_refresh(db, graph, t, yt_refresh_hours)
        sweep = await _version_sweep(db, graph)
    return ReconcileReport(True, propagated, refresh, sweep)


async def _repair_unpropagated(db: Any, graph: Mapping[str, StageDef], t: datetime) -> int:
    cutoff = to_ts(db, t - timedelta(seconds=UNPROPAGATED_AFTER_SEC))
    rows = await db.fetch(
        "SELECT id, stage FROM processing_jobs WHERE state = 'done' AND propagated_at IS NULL"
        " AND (finished_at IS NULL OR finished_at < $1) ORDER BY id LIMIT 500",
        cutoff,
    )
    n = 0
    for r in rows:
        stage = graph.get(r["stage"])
        if stage is not None and stage.legacy:
            res = await after_legacy_done(db, int(r["id"]), graph)
            n += 1 if res is not None else 0
        else:
            # no propagation can be reconstructed (engine rows finish atomically):
            # mark it so it is not retried forever
            await db.execute(
                "UPDATE processing_jobs SET propagated_at = $2 WHERE id = $1",
                int(r["id"]),
                to_ts(db, t),
            )
    return n


async def _youtube_refresh(
    db: Any, graph: Mapping[str, StageDef], t: datetime, hours: float | None
) -> int:
    if "youtube_sync" not in graph:
        return 0
    if hours is None:
        hours = float(os.environ.get("MAKTABA_YT_REFRESH_HOURS", DEFAULT_YT_REFRESH_HOURS))
    cutoff = to_ts(db, t - timedelta(hours=hours))
    rows = await db.fetch(
        "SELECT l.id AS id FROM libraries l"
        " WHERE EXISTS (SELECT 1 FROM youtube_channels c WHERE c.library_id = l.id)"
        " AND NOT EXISTS (SELECT 1 FROM processing_jobs j WHERE j.subject_kind = 'library'"
        "   AND j.subject_id = l.id AND j.stage = 'youtube_sync'"
        "   AND (j.state IN ('pending','claimed','running','resuming','paused')"
        "        OR (j.state = 'done' AND j.finished_at >= $1)))",
        cutoff,
    )
    n = 0
    for r in rows:
        res = await enqueue_job(
            db,
            subject_kind="library",
            subject_id=UUID(str(r["id"])),
            stage="youtube_sync",
            priority=SWEEP_PRIORITY,
            reason={"why": "scheduled refresh", "by": "system"},
        )
        n += 1 if res.job_id is not None else 0
    return n


async def _version_sweep(db: Any, graph: Mapping[str, StageDef]) -> int:
    total = 0
    for stage in graph.values():
        if stage.legacy:
            continue
        rows = await db.fetch(
            "SELECT j.subject_kind AS kind, j.subject_id AS sid FROM processing_jobs j"
            " WHERE j.stage = $1 AND j.state = 'done' AND j.stage_version < $2"
            f" AND j.outcome IN {SUCCESS}"
            " AND j.id = (SELECT max(k.id) FROM processing_jobs k"
            "  WHERE k.subject_kind = j.subject_kind AND k.subject_id = j.subject_id"
            f"  AND k.stage = j.stage AND k.state = 'done' AND k.outcome IN {SUCCESS})"
            " AND NOT EXISTS (SELECT 1 FROM processing_jobs l WHERE l.subject_kind = j.subject_kind"
            "  AND l.subject_id = j.subject_id AND l.stage = j.stage"
            "  AND l.state IN ('pending','claimed','running','resuming','paused'))"
            " ORDER BY j.id LIMIT $3",
            stage.name,
            stage.version,
            SWEEP_BATCH,
        )
        for r in rows:
            res = await enqueue_job(
                db,
                subject_kind=r["kind"],
                subject_id=UUID(str(r["sid"])),
                stage=stage.name,
                priority=SWEEP_PRIORITY,
                reason={"why": f"{stage.name} version bumped", "by": "system"},
            )
            total += 1 if res.job_id is not None else 0
    return total


async def run_reconciler(
    db: Any,
    graph: Mapping[str, StageDef],
    stop: asyncio.Event,
    *,
    interval_sec: float = 60.0,
) -> None:
    while not stop.is_set():
        try:
            report = await reconcile_once(db, graph)
            if report.leader and (report.propagated or report.yt_refresh or report.version_sweep):
                _log.info(
                    "reconciled",
                    propagated=report.propagated,
                    yt_refresh=report.yt_refresh,
                    version_sweep=report.version_sweep,
                )
        except Exception:  # noqa: BLE001 - the reconciler must never take the worker down
            _log.exception("reconcile_failed")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval_sec)
