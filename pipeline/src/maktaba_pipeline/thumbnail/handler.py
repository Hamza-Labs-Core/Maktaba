"""THUMBNAIL stage adapter (Story R-thumbnail / Story 7.7).

The runtime placeholder for THUMBNAIL just logged + marked the job
``done`` without producing any images. This adapter is the real glue,
shaped exactly like the PROBE / EXTRACT adapters in
:mod:`maktaba_pipeline.handlers`:

1. resolve ``videos.path`` / ``content_hash`` / ``duration_sec`` for the
   job's ``video_id``,
2. load the video's chapter starts (so each chapter gets a thumbnail),
3. drive :func:`maktaba_pipeline.thumbnail.generator.generate_thumbnails`
   (DI seam: ``run_generate`` — tests inject a fake so no ffmpeg spawns),
4. :func:`commit_thumbnails` persists ``videos.poster_path`` /
   ``sprite_path`` and advances the FSM ``INDEXED -> THUMBNAILED``,
5. flip the job ``done`` (or ``failed`` / retry on error).

Failure classification mirrors EXTRACT: a missing source row is
*non-retryable* (a re-run cannot invent the file); an ffmpeg failure is
*retryable* (transient I/O / partial write).

Chapter thumbnails are written to disk under the content-addressed
thumbnail cache; wiring their paths into ``chapters.metadata`` is a
tracked follow-up (it needs a dialect-safe JSONB merge the other commit
helpers don't yet have a pattern for), so the commit here persists the
two first-class ``videos`` columns only.
"""

from __future__ import annotations

import traceback
from typing import TYPE_CHECKING, Any, Protocol, cast

from ..db.jobs import DBConn, Job, Stage
from ..db.jobs_state import StageError, mark_done, mark_failed_or_retry
from ..log import get_logger
from .generator import ThumbnailError, ThumbnailSet, generate_thumbnails

if TYPE_CHECKING:
    from collections.abc import Awaitable, Callable

    # ``(src, *, duration_sec, content_hash, chapters) -> ThumbnailSet``
    ThumbnailRunner = Callable[..., Awaitable[ThumbnailSet]]

_log = get_logger()

_SELECT_THUMB_SOURCE = "SELECT path, content_hash, duration_sec FROM videos WHERE id = $1"
_SELECT_CHAPTERS = "SELECT seq, start_sec FROM chapters WHERE video_id = $1 ORDER BY seq"
_UPDATE_VIDEO_THUMBS = "UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1"


async def thumbnail_handler(
    db: DBConn,
    job: Job,
    *,
    run_generate: ThumbnailRunner | None = None,
) -> None:
    """Real THUMBNAIL stage: generate poster + sprite + chapter thumbs."""
    generate = run_generate or generate_thumbnails
    try:
        if job.video_id is None:
            # Per-video stage with no video_id — impossible under the
            # slot 0058 scope CHECK; terminal defence.
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="thumbnail_missing_source",
                    message=f"thumbnail job {job.id} has no video_id",
                    retryable=False,
                ),
            )
            return

        row = await db.fetchrow(_SELECT_THUMB_SOURCE, job.video_id)
        if row is None or row["path"] is None or row["content_hash"] is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="thumbnail_missing_source",
                    message=f"no videos.path/content_hash for video_id={job.video_id}",
                    retryable=False,
                ),
            )
            return

        path = str(row["path"])
        content_hash = str(row["content_hash"])
        duration_sec = float(row["duration_sec"]) if row["duration_sec"] is not None else 0.0

        chapters = await _load_chapter_starts(cast("_ChapterReadDB", db), job.video_id)

        result = await generate(
            path,
            duration_sec=duration_sec,
            content_hash=content_hash,
            chapters=chapters,
        )

        await commit_thumbnails(
            cast("_ThumbnailDB", db),
            video_id=job.video_id,
            poster_path=str(result.poster),
            sprite_path=str(result.sprite),
        )
        await mark_done(db, job_id=job.id)
    except ThumbnailError as exc:
        # ffmpeg ran but failed — transient by nature. Retryable.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.THUMBNAIL.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind=exc.kind,
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )
    except LookupError as exc:
        # The video row vanished mid-flight (TOCTOU). A re-run cannot
        # resurrect it, so this is terminal.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.THUMBNAIL.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="thumbnail_video_vanished",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=False,
            ),
        )
    except Exception as exc:  # noqa: BLE001 — funnel every other failure to retry
        _log.warning(
            "stage_handler_failed",
            stage=Stage.THUMBNAIL.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="thumbnail_failed",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )


class _ChapterReadDB(Protocol):
    """The multi-row read surface the chapter lookup needs.

    ``DBConn`` only declares ``fetchrow``; the runtime ``Database``
    facade (and the test fake) also expose ``fetch``, so the handler
    casts to this narrower protocol for the one query that returns many
    rows.
    """

    async def fetch(self, sql: str, *args: Any) -> Any: ...


async def _load_chapter_starts(db: _ChapterReadDB, video_id: Any) -> list[tuple[int, float]]:
    """Read ``(seq, start_sec)`` for the video's chapters in seq order.

    Returns an empty list when the video has no chapters — the generator
    simply skips the chapter-thumbnail pass in that case.
    """
    rows = await db.fetch(_SELECT_CHAPTERS, video_id)
    return [(int(r["seq"]), float(r["start_sec"])) for r in rows]


# _ThumbnailDB is the slice commit_thumbnails needs: the transactional
# UPDATE plus the FSM advance's state read/write. The runtime always
# dispatches the concrete Database facade, a strict superset of DBConn,
# so the cast in thumbnail_handler is the same shape PROBE/EXTRACT use.
if TYPE_CHECKING:
    from ..orchestrator.advance import DBConn as _ThumbnailDB
else:  # pragma: no cover - runtime alias
    _ThumbnailDB = Any


async def commit_thumbnails(
    db: _ThumbnailDB,
    *,
    video_id: Any,
    poster_path: str,
    sprite_path: str,
) -> str:
    """Persist the poster/sprite paths and advance the video state.

    Returns the new ``videos.state``. The THUMBNAIL analogue of
    :func:`maktaba_pipeline.audio.extract.commit_extract`:

    1. UPDATE ``videos.poster_path`` / ``sprite_path`` in a transaction,
    2. advance the FSM ``INDEXED -> THUMBNAILED`` via
       :func:`advance_after_stage` (its terminal-drop guard makes a
       replay a no-op).

    THUMBNAIL is a leaf — no follow-on stage is enqueued; the final
    ``THUMBNAILED -> READY`` gate is driven elsewhere (the readiness
    sweep), so this helper does not enqueue.
    """
    from ..domain.states import Outcome, State, Trigger  # noqa: PLC0415
    from ..orchestrator.advance import advance_after_stage  # noqa: PLC0415

    async with db.transaction():
        await db.execute(_UPDATE_VIDEO_THUMBS, video_id, poster_path, sprite_path)

    state_row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", video_id)
    if state_row is None:
        raise LookupError(f"video {video_id} not found")
    current_state = State(state_row["state"])

    if current_state == State.INDEXED:
        new_state = await advance_after_stage(db, video_id, Trigger.THUMBNAIL, Outcome.OK, log=_log)
    else:
        # Replay / out-of-order: leave the row where it is. The FSM has
        # no THUMBNAILED --THUMBNAIL--> edge, so re-advancing would raise.
        new_state = current_state

    _log.info(
        "thumbnail_committed",
        video_id=str(video_id),
        new_state=str(new_state),
        poster_path=poster_path,
        sprite_path=sprite_path,
    )
    return str(new_state)
