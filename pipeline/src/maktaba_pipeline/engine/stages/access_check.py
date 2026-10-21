"""``access_check``: can this worker host read the video's file? (Epic 40, story 40.3)

An admin asks for it on the failures page ("Check access"): Maktaba cannot change the
share's permissions, so it tests them. The file is opened through this host's path map and
64 KiB are read (bounded by ``rules.queue_health.probe_timeout_seconds``). The result goes
to ``item_checks`` (ok, the error, the host, when). When the file reads, every stage whose
latest job for the video failed is queued again (forced, new attempts), and a missing file
that is back loses its ``missing_confirmed_at``.

The check itself never fails the job: what it found is its result.
"""

from __future__ import annotations

import asyncio
import socket
from typing import Any
from uuid import UUID

from ...db.jobs import Job
from ..context import StageContext
from ..enqueue import enqueue_job
from ..failure_policy import read_config
from ..graph import StageResult
from ..hashing import canonical_hash
from ..sql import is_pg, now, to_ts, uid
from ._common import require_video
from .registry import StageImpl

__all__ = ["IMPL", "read_head", "requeue_failed"]

READ_BYTES = 64 * 1024


def read_head(path: str) -> None:
    """Open ``path`` and read its first 64 KiB; raises what the file system says."""
    with open(path, "rb") as f:
        f.read(READ_BYTES)


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


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    video = await require_video(db, job.subject.id)
    cfg = await read_config(db)
    local = ctx.local_path(video.path)
    error: str | None = None
    try:
        await asyncio.wait_for(asyncio.to_thread(read_head, local), cfg.probe_timeout_seconds)
    except TimeoutError:
        error = f"no answer within {cfg.probe_timeout_seconds:g} s reading {local}"
    except OSError as e:
        error = f"{type(e).__name__}: {e}"
    ok = error is None
    host = socket.gethostname()
    await db.execute(
        "INSERT INTO item_checks (video_id, ok, error, host, checked_at)"
        " VALUES ($1, $2, $3, $4, $5) ON CONFLICT (video_id) DO UPDATE SET ok = EXCLUDED.ok,"
        " error = EXCLUDED.error, host = EXCLUDED.host, checked_at = EXCLUDED.checked_at",
        uid(video.id),
        ok if is_pg(db) else int(ok),
        None if error is None else error[:500],
        host,
        to_ts(db, now()),
    )
    requeued: list[str] = []
    if ok:
        await db.execute(
            "UPDATE videos SET missing_confirmed_at = NULL WHERE id = $1", uid(video.id)
        )
        requeued = await requeue_failed(db, video.id, "the access check read the file")
    return StageResult(
        output_hash=canonical_hash({"ok": ok, "error": error}),
        outcome="unchanged",
        summary={"ok": ok, "error": error, "host": host, "path": local, "requeued": requeued},
    )


IMPL = StageImpl(run=run)
