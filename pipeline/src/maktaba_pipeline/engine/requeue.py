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

from .enqueue import enqueue_job
from .sql import uid

__all__ = ["requeue_failed"]

_LATEST_FAILED = """
SELECT j.stage AS stage FROM processing_jobs j
 WHERE j.subject_kind = 'video' AND j.subject_id = $1 AND j.state = 'failed'
   AND j.stage <> 'access_check'
   AND NOT EXISTS (SELECT 1 FROM processing_jobs n
                    WHERE n.subject_kind = j.subject_kind AND n.subject_id = j.subject_id
                      AND n.stage = j.stage AND n.id > j.id)
 ORDER BY j.stage
"""


async def requeue_failed(db: Any, video_id: UUID, why: str) -> list[str]:
    """Queue again (forced) every stage whose latest job for the video failed."""
    relay = getattr(db, "engine_relay", None)
    if relay is not None:
        got = await relay.ask("requeue_failed", video_id=str(video_id), why=why)
        return [str(s) for s in got or []]
    stages = [str(r["stage"]) for r in await db.fetch(_LATEST_FAILED, uid(video_id))]
    for stage in stages:
        await enqueue_job(
            db,
            subject_kind="video",
            subject_id=video_id,
            stage=stage,
            force=True,
            reason={"why": why, "by": "system"},
        )
    return stages
