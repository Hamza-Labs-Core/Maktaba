"""The home's enrich sweep: queued ``enrich_jobs`` become engine ``enrich`` jobs.

Run by the home's worker at every 30 s refresh (:mod:`maktaba_pipeline.cluster.node`), so
no ``enrich_jobs`` row is left waiting (stub audit #4: nothing ever ran them):

* a due row (``pending``, or ``deferred`` whose window passed) with no live engine job
  for its video gets one (stage ``enrich``), which shows in the control centre;
* a row left ``running`` by a worker that died (no live engine job, untouched for
  :data:`STUCK_AFTER`) goes back to ``pending``;
* with ``rules.enrich.auto`` on, a few videos that never had a suggestion get a queued
  row (``origin = 'auto'``), within the same daily caps.
"""

from __future__ import annotations

from datetime import timedelta
from typing import Any
from uuid import UUID

from ..engine.enqueue import enqueue_job
from ..engine.sql import now, to_ts
from ..log import get_logger
from .gating import EnrichSettings, should_enqueue_enrich
from .jobs import enqueue_enrich

__all__ = ["STAGE", "STUCK_AFTER", "sweep"]

STAGE = "enrich"
STUCK_AFTER = timedelta(minutes=30)
#: engine jobs enqueued per sweep (the lookup pool runs one at a time anyway)
BATCH = 25

_log = get_logger(component="enrich_sweep")

_LIVE = "('pending', 'claimed', 'running', 'resuming', 'paused')"


async def sweep(db: Any, *, batch: int = BATCH) -> dict[str, int]:
    from ..rules import read as read_rules
    from .providers import configured_providers

    at = now()
    out = {"reset": 0, "auto": 0, "enqueued": 0}
    stuck = await db.fetch(
        "SELECT j.id FROM enrich_jobs j WHERE j.status = 'running' AND j.updated_at < $1"
        " AND NOT EXISTS (SELECT 1 FROM processing_jobs p WHERE p.stage = $2"
        f" AND p.subject_kind = 'video' AND p.subject_id = j.video_id AND p.state IN {_LIVE})",
        to_ts(db, at - STUCK_AFTER),
        STAGE,
    )
    for r in stuck:
        await db.execute(
            "UPDATE enrich_jobs SET status = 'pending', updated_at = $2 WHERE id = $1"
            " AND status = 'running'",
            str(r["id"]),
            to_ts(db, at),
        )
        out["reset"] += 1

    cfg, _, _ = await read_rules(db, "enrich")
    if should_enqueue_enrich(EnrichSettings(cfg.auto), await configured_providers(db, cfg)):
        fresh = await db.fetch(
            "SELECT v.id FROM videos v WHERE v.deleted_at IS NULL"
            " AND NOT EXISTS (SELECT 1 FROM enrich_jobs j WHERE j.video_id = v.id)"
            " AND NOT EXISTS (SELECT 1 FROM media_metadata_enrichment e WHERE e.video_id = v.id"
            " AND e.provider IN ('internal', 'web', 'llm'))"
            " ORDER BY v.created_at DESC LIMIT $1",
            max(0, cfg.auto_batch),
        )
        for r in fresh:
            if await enqueue_enrich(db, UUID(str(r["id"])), origin="auto") is not None:
                out["auto"] += 1

    due = await db.fetch(
        "SELECT j.video_id, j.origin FROM enrich_jobs j"
        " WHERE j.status IN ('pending', 'deferred') AND j.not_before <= $1"
        " AND NOT EXISTS (SELECT 1 FROM processing_jobs p WHERE p.stage = $2"
        f" AND p.subject_kind = 'video' AND p.subject_id = j.video_id AND p.state IN {_LIVE})"
        " ORDER BY j.not_before LIMIT $3",
        to_ts(db, at),
        STAGE,
        batch,
    )
    for r in due:
        res = await enqueue_job(
            db,
            subject_kind="video",
            subject_id=str(r["video_id"]),
            stage=STAGE,
            reason={"why": f"enrich_{r['origin'] or 'auto'}", "by": "system"},
        )
        if res.enqueued:
            out["enqueued"] += 1
    if any(out.values()):
        _log.info("enrich_sweep", **out)
    return out
