"""Queue again every stage whose latest job for a video failed (Epic 40, story 40.3).

The access check that reads the file (``access_check``) and the scan that finds a missing
file again (:mod:`.missing_files`) both do it. Only the queue engine writes the queue
(README §15.3): in a first-party plugin's runner (Epic 54 story 54.36, ``tv.maktaba.core-
media``) the worker does it, for the job's own video (``access_check``) or a video of the
job's library (``scan``), as the relayed ``requeue_failed``.
"""

from __future__ import annotations

from typing import Any
from uuid import UUID

from .enqueue import EnqueueResult, enqueue_job
from .sql import loads, uid

__all__ = ["HEAL_PRIORITY", "requeue_failed", "requeue_stage"]

#: what the healer (:mod:`.healer`) queues, and what follows it: behind every other work
#: (normal is 100, the sweeps 500, the backfills 900)
HEAL_PRIORITY = 950

_LATEST_FAILED = """
SELECT j.stage AS stage, j.payload AS payload, j.force_cascade AS force_cascade
  FROM processing_jobs j
 WHERE j.subject_kind = 'video' AND j.subject_id = $1 AND j.state = 'failed'
   AND j.stage <> 'access_check'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
 ORDER BY j.stage
"""


async def requeue_failed(db: Any, video_id: UUID, why: str) -> list[str]:
    """Queue again (forced) every stage whose latest job for the video failed, each asked
    what the failed job was asked."""
    relay = getattr(db, "engine_relay", None)
    if relay is not None:
        got = await relay.ask("requeue_failed", video_id=str(video_id), why=why)
        return [str(s) for s in got or []]
    rows = await db.fetch(_LATEST_FAILED, uid(video_id))
    for r in rows:
        await requeue_stage(db, video_id, str(r["stage"]), why, failed=r)
    return [str(r["stage"]) for r in rows]


def _asked(payload: Any) -> dict[str, Any] | None:
    """A failed job's payload for the job that takes its place. ``run_now`` was an admin's
    word about that job (it passes the quiet hours), not about this one."""
    p = loads(payload)
    if not isinstance(p, dict):
        return None
    return {k: v for k, v in p.items() if k != "run_now"} or None


async def requeue_stage(
    db: Any,
    video_id: UUID,
    stage: str,
    why: str,
    *,
    failed: Any,
    force: bool = True,
    priority: int | None = None,
) -> EnqueueResult:
    """Queue one stage of a video again, with the ``payload`` and ``force_cascade`` of the
    job that ``failed`` (the API's Retry of a job carries its payload too)."""
    return await enqueue_job(
        db,
        subject_kind="video",
        subject_id=video_id,
        stage=stage,
        priority=priority,
        force=force,
        force_cascade=bool(failed["force_cascade"]),
        payload=_asked(failed["payload"]),
        reason={"why": why, "by": "system"},
    )
