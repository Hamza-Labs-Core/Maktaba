"""Real per-stage adapter handlers (Track R1).

The runtime's :func:`maktaba_pipeline.runtime.build_default_dispatch`
binds every :class:`~maktaba_pipeline.db.jobs.Stage` to a no-op
placeholder that just logs and marks the job ``done``. The real
per-stage business logic already exists as pure, well-unit-tested
library functions in the Epic 1-6 modules — but with zero runtime
callers.

This module supplies the missing glue: thin ``(db, job) -> None``
adapters that

1. validate the job carries the inputs the stage needs,
2. load the prerequisites the prior stage produced,
3. call the existing library function (the heavy logic stays there),
4. let that function persist + enqueue follow-on work the way its own
   unit tests demonstrate, and
5. flip the job ``done`` (or ``failed`` / retry on error) via the
   :mod:`maktaba_pipeline.db.jobs_state` helpers.

:func:`build_real_dispatch` returns the override map the daemon entry
point feeds to :func:`maktaba_pipeline.runtime.run`. Only stages with a
genuine thin-wrapper mapping are registered; every other stage is left
on the placeholder until its real wiring lands (tracked in the
gap-closure plan). In particular ``THUMBNAIL`` has no implementing
module at all and stays on the placeholder.
"""

from __future__ import annotations

import traceback
from collections.abc import Awaitable, Callable
from pathlib import Path
from typing import TYPE_CHECKING, cast

from .audio.extract import ExtractError
from .audio.extract import extract_to_file as _ffmpeg_extract
from .audio.probe import ProbeResult
from .audio.probe import probe as _ffprobe
from .db.jobs import DBConn, Job, Stage
from .db.jobs_state import StageError, mark_done, mark_failed_or_retry
from .log import get_logger

if TYPE_CHECKING:
    from .audio.extract import _ExtractDB
    from .audio.probe import _ProbeDB

__all__ = [
    "ExtractRunner",
    "ProbeRunner",
    "build_real_dispatch",
    "extract_handler",
    "probe_handler",
]

_log = get_logger()

# DI seam: the default shells out to ffprobe; tests inject a fake that
# returns a curated ``ProbeResult`` so the unit suite never spawns a
# subprocess (mirrors the ``now`` / ``rng`` seams in jobs_state).
ProbeRunner = Callable[[str], Awaitable[ProbeResult]]

# DI seam for EXTRACT: the default decodes the chosen track to a cached
# WAV via ffmpeg; tests inject a fake returning a path so the unit
# suite never spawns ffmpeg (mirrors PROBE's ``run_probe`` seam).
ExtractRunner = Callable[..., Awaitable[Path]]

_SELECT_VIDEO_PATH = "SELECT path FROM videos WHERE id = $1"
_SELECT_VIDEO_SRC = "SELECT path, content_hash FROM videos WHERE id = $1"


async def probe_handler(
    db: DBConn,
    job: Job,
    *,
    run_probe: ProbeRunner | None = None,
) -> None:
    """Real PROBE stage: ffprobe the source file then ``commit_probe``.

    The heavy lifting (parsing ffprobe JSON, the media_info /
    audio_tracks UPSERTs, the FSM advance, and the follow-on EXTRACT
    enqueue) all lives in :func:`maktaba_pipeline.audio.probe.commit_probe`
    and is exercised by ``tests/audio/test_probe.py``. This adapter only
    resolves the on-disk path and drives that function.
    """
    probe = run_probe or _ffprobe
    try:
        row = await db.fetchrow(_SELECT_VIDEO_PATH, job.video_id)
        if row is None or row["path"] is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="probe_missing_source",
                    message=f"no videos.path for video_id={job.video_id}",
                    retryable=False,
                ),
            )
            return

        path = str(row["path"])
        result = await probe(path)

        # commit_probe persists media_info + audio_tracks, advances the
        # video FSM, and enqueues the EXTRACT job when audio is present
        # — exactly the follow-on behaviour the placeholder elided.
        from .audio.probe import commit_probe  # noqa: PLC0415 — avoid import cycle at module load

        # ``commit_probe`` needs the ``execute`` method too; the runtime
        # always dispatches the concrete ``runtime.Database`` facade,
        # which is a strict superset of ``DBConn`` and satisfies the
        # probe module's ``_ProbeDB`` protocol — the same cast the probe
        # module itself uses internally for the enqueue() call.
        await commit_probe(cast("_ProbeDB", db), video_id=job.video_id, result=result)
        await mark_done(db, job_id=job.id)
    except Exception as exc:  # noqa: BLE001 — funnel every failure to the retry helper
        _log.warning(
            "stage_handler_failed",
            stage=Stage.PROBE.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="probe_failed",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )


async def extract_handler(
    db: DBConn,
    job: Job,
    *,
    run_extract: ExtractRunner | None = None,
) -> None:
    """Real EXTRACT stage: pick the audio track then decode + commit.

    Consumes exactly what PROBE persisted:

    1. ``videos.path`` (source media) + ``videos.content_hash`` (the
       content-addressed audio-cache key) in one SELECT,
    2. the ``audio_tracks`` rows ``commit_probe`` wrote — read back via
       :func:`audio.extract.load_selected_track`, which reconstructs the
       pure :class:`AudioTrack` view and runs the Story 2.2
       track-selection policy,
    3. ffmpeg-decode the chosen track to the cache path (DI seam:
       ``run_extract`` — tests inject a fake so no subprocess spawns),
    4. :func:`audio.extract.commit_extract` persists the
       ``audio_cache`` artifact, stamps the track, advances the FSM
       ``PROBED -> AUDIO_EXTRACTED``, and enqueues the follow-on
       TRANSCRIBE job (same ``enqueue`` mechanism ``commit_probe`` uses
       for the EXTRACT enqueue),
    5. flip the job ``done``.

    Failure classification: a missing source row / content_hash and a
    missing audio track are *non-retryable* (a re-run cannot fix a
    data inconsistency); an ffmpeg decode failure is *retryable*
    (transient I/O, partially-written file).
    """
    extract = run_extract or _ffmpeg_extract
    try:
        row = await db.fetchrow(_SELECT_VIDEO_SRC, job.video_id)
        if row is None or row["path"] is None or row["content_hash"] is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="extract_missing_source",
                    message=(
                        f"no videos.path/content_hash for video_id={job.video_id}"
                    ),
                    retryable=False,
                ),
            )
            return

        path = str(row["path"])
        content_hash = str(row["content_hash"])

        from .audio.extract import commit_extract, load_selected_track  # noqa: PLC0415

        selected = await load_selected_track(
            cast("_ExtractDB", db), video_id=job.video_id
        )
        if selected is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="extract_no_audio_track",
                    message=(
                        f"no selectable audio_tracks row for video_id={job.video_id}"
                        " — PROBE should not have enqueued EXTRACT"
                    ),
                    retryable=False,
                ),
            )
            return

        dest = await extract(
            path,
            selected.track.index,
            content_hash=content_hash,
        )
        dest_path = Path(dest)
        try:
            bytes_written: int | None = dest_path.stat().st_size
        except OSError:
            bytes_written = None

        await commit_extract(
            cast("_ExtractDB", db),
            video_id=job.video_id,
            audio_track_id=selected.db_id,
            content_hash=content_hash,
            cache_path=str(dest_path),
            bytes_written=bytes_written,
        )
        await mark_done(db, job_id=job.id)
    except ExtractError as exc:
        # ffmpeg ran but failed — transient by nature (bad partial
        # read, disk pressure). Retryable.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.EXTRACT.value,
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
    except Exception as exc:  # noqa: BLE001 — funnel every failure to the retry helper
        _log.warning(
            "stage_handler_failed",
            stage=Stage.EXTRACT.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="extract_failed",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )


def build_real_dispatch() -> dict[Stage, Callable[[DBConn, Job], Awaitable[None]]]:
    """Return the dispatch-override map for the daemon entry point.

    Only stages with a genuine thin-wrapper adapter are registered.
    Every unregistered stage (TRANSCRIBE, SUBTITLE_GEN, INDEX,
    THUMBNAIL) keeps the runtime's placeholder handler until its real
    orchestration lands — see the module docstring and the gap-closure
    plan's Track R1/R2 concerns.

    Track R2: EXTRACT now has a real adapter and joins the map. Because
    it is in the map, ``_DEFAULT_STAGES`` listing EXTRACT is safe — a
    default worker claiming an EXTRACT job runs the real decode +
    commit + TRANSCRIBE enqueue rather than the silent no-op drain.
    """

    return {
        Stage.PROBE: probe_handler,
        Stage.EXTRACT: extract_handler,
    }
