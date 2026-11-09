"""``thumbnail`` on the engine (Epic 54 story 54.36 §2): a video's poster, sprite and chapter
frames.

Before, ``thumbnail`` was a legacy handler (``thumbnail.handler.thumbnail_handler``): it
marked its own job done, and the engine then recorded a hash of the time (every run counted
as a change). Now the stage hashes what it made: the poster's and the sprite's paths, which
the thumbnail cache names by the video's content, so a run that makes the same images is
``unchanged``. The frames, the saved paths and the video's state (``commit_thumbnails``) and
the failure kinds are the handler's, unchanged. It still runs on the home only (the share is
written)."""

from __future__ import annotations

from typing import Any, cast

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.log import get_logger
from maktaba_pipeline.thumbnail.generator import ThumbnailError
from maktaba_pipeline.thumbnail.generator import generate_thumbnails as generate
from maktaba_pipeline.thumbnail.handler import (
    _ChapterReadDB,
    _load_chapter_starts,
    commit_thumbnails,
)

__all__ = ["IMPL"]

_log = get_logger()


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.video_id is None:
        # impossible under the per-video scope check; a re-run cannot invent the video
        raise StageFailure(
            "thumbnail_missing_source", f"thumbnail job {job.id} has no video_id", retryable=False
        )
    row = await db.fetchrow(
        "SELECT path, content_hash, duration_sec FROM videos WHERE id = $1", job.video_id
    )
    if row is None or row["path"] is None or row["content_hash"] is None:
        raise StageFailure(
            "thumbnail_missing_source",
            f"no videos.path/content_hash for video_id={job.video_id}",
            retryable=False,
        )
    duration = float(row["duration_sec"]) if row["duration_sec"] is not None else 0.0
    try:
        chapters = await _load_chapter_starts(cast("_ChapterReadDB", db), job.video_id)
        made = await generate(
            ctx.local_path(str(row["path"])),  # the job's path map
            duration_sec=duration,
            content_hash=str(row["content_hash"]),
            chapters=chapters,
        )
        state = await commit_thumbnails(
            db, video_id=job.video_id, poster_path=str(made.poster), sprite_path=str(made.sprite)
        )
    except ThumbnailError as exc:  # ffmpeg ran and failed: transient by nature
        raise StageFailure(exc.kind, str(exc), retryable=True) from exc
    except LookupError as exc:  # the video vanished mid-flight: a re-run cannot bring it back
        raise StageFailure("thumbnail_video_vanished", str(exc), retryable=False) from exc
    except Exception as exc:  # any other failure: another run may succeed
        raise StageFailure("thumbnail_failed", str(exc), retryable=True) from exc
    return StageResult(
        output_hash=canonical_hash({"poster": str(made.poster), "sprite": str(made.sprite)}),
        summary={"chapters": len(made.chapter_thumbs), "state": state},
    )


# no input fingerprint: a queued thumbnail always makes its frames, as the legacy stage did
IMPL = StageImpl(run=run)
