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
from typing import TYPE_CHECKING, Any, cast
from uuid import UUID

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
    from .stt.transcribe import _TranscribeDB
    from .subtitle.subtitle_gen import _SubtitleDB

__all__ = [
    "BackendSelector",
    "ExtractRunner",
    "ProbeRunner",
    "build_real_dispatch",
    "extract_handler",
    "probe_handler",
    "subtitle_handler",
    "transcribe_handler",
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

# DI seam for TRANSCRIBE: ``(*, video_id) -> (backend, name, version)``.
# The default builds a registry from the *configured* STT backend and
# walks the fallback chain (``stt.transcribe.default_select_backend``);
# tests inject a fake returning a canned backend so the suite never
# loads a model / spawns a subprocess (mirrors PROBE/EXTRACT seams).
BackendSelector = Callable[..., Awaitable[tuple[Any, str, "str | None"]]]

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
    except LookupError as exc:
        # The video row vanished mid-flight (TOCTOU: present at the
        # source SELECT, gone by ``commit_extract``'s state read). A
        # re-run cannot resurrect it, so this is terminal — classifying
        # it retryable would burn one attempt before the retry hit the
        # non-retryable missing-source guard anyway.
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
                kind="extract_video_vanished",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=False,
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


async def transcribe_handler(
    db: DBConn,
    job: Job,
    *,
    select_backend: BackendSelector | None = None,
) -> None:
    """Real TRANSCRIBE stage: pick a backend then transcribe + commit.

    Consumes exactly what EXTRACT persisted:

    1. the job ``payload`` ``{audio_track_id, content_hash}`` EXTRACT
       enqueued — the ``content_hash`` is the ``audio_cache`` key,
    2. :func:`stt.transcribe.load_audio_artifact` reads back the
       ``audio_cache`` row (the decoded WAV path + ``audio_track_id``),
    3. select an STT backend via the registry seam (DI: ``select_backend``
       — default builds the registry from the *configured* backend and
       walks the fallback chain; tests inject a fake so no model loads),
    4. :func:`stt.transcribe.commit_transcribe` inserts the transcript
       inactive, persists every backend segment via the existing
       :func:`stt.segment_commit.commit_segment`, activates it only
       after the full stream succeeds, advances the FSM
       ``AUDIO_EXTRACTED -> TRANSCRIBED``, and enqueues the follow-on
       ``SUBTITLE_GEN`` + ``INDEX`` jobs (same ``enqueue`` mechanism
       ``commit_extract`` uses),
    5. flip the job ``done``.

    Failure classification: a missing payload / missing ``audio_cache``
    row is *non-retryable* (a data inconsistency — EXTRACT only
    enqueues TRANSCRIBE after persisting the artifact); a transient
    backend / IO error mid-stream is *retryable*; a video that vanished
    mid-flight is terminal (mirrors EXTRACT's ``LookupError`` guard).
    """
    from .stt.transcribe import (  # noqa: PLC0415 — avoid import cycle at module load
        SelectedBackend,
        commit_transcribe,
        default_select_backend,
        load_audio_artifact,
    )

    pick = select_backend or default_select_backend
    try:
        payload = job.payload or {}
        content_hash = payload.get("content_hash")
        if not content_hash:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="transcribe_missing_payload",
                    message=(
                        f"TRANSCRIBE job {job.id} has no payload.content_hash"
                        " — EXTRACT should have enqueued it with one"
                    ),
                    retryable=False,
                ),
            )
            return

        artifact = await load_audio_artifact(
            cast("_TranscribeDB", db), content_hash=str(content_hash)
        )
        if artifact is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="transcribe_missing_audio_cache",
                    message=(
                        f"no audio_cache row for content_hash={content_hash}"
                        " — EXTRACT should not have enqueued TRANSCRIBE"
                    ),
                    retryable=False,
                ),
            )
            return

        backend, name, version = await pick(video_id=job.video_id)

        await commit_transcribe(
            cast("_TranscribeDB", db),
            video_id=job.video_id,
            artifact=artifact,
            selected=SelectedBackend(backend=backend, name=name, version=version),
            job_id=job.id,
            total_duration_sec=job.total_duration_seconds or 0.0,
            language=None,
        )
        await mark_done(db, job_id=job.id)
    except LookupError as exc:
        # TOCTOU: the video row vanished between the artifact read and
        # commit_transcribe's state read. Terminal — a re-run cannot
        # resurrect it (mirrors extract_handler's LookupError guard).
        _log.warning(
            "stage_handler_failed",
            stage=Stage.TRANSCRIBE.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="transcribe_video_vanished",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=False,
            ),
        )
    except Exception as exc:  # noqa: BLE001 — funnel every failure to the retry helper
        # Backend / IO failures (NoBackendReady, a broken model pipe, a
        # transient decode error) are transient by nature → retryable.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.TRANSCRIBE.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="transcribe_failed",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )


async def subtitle_handler(
    db: DBConn,
    job: Job,
) -> None:
    """Real SUBTITLE_GEN stage: load segments then render + commit.

    Consumes exactly what TRANSCRIBE persisted:

    1. the job ``payload`` ``{transcript_id, audio_track_id}`` TRANSCRIBE
       enqueued — the ``transcript_id`` names the *exact* transcript
       this stage renders (no "which transcript is active" re-query
       race),
    2. :func:`subtitle.subtitle_gen.load_transcript_cues` reads the
       transcript (its ``language``) + every ordered
       ``transcript_segments`` row and projects them into the
       renderers' cue shape via the existing ``segments_to_cues``,
    3. :func:`subtitle.subtitle_gen.commit_subtitles` renders SRT + VTT
       via the existing pure ``generate_srt`` / ``generate_vtt``,
       persists both via the pre-existing ``write_atomic`` +
       ``register_subtitle`` (the ``subtitle_files`` registry — the
       EXTRACT-``audio_cache`` analogue), and advances the FSM
       ``TRANSCRIBED -> INDEXED`` (replay-guarded),
    4. flip the job ``done``.

    No follow-on enqueue: TRANSCRIBE already fanned out SUBTITLE_GEN +
    INDEX in parallel — SUBTITLE_GEN is a leaf.

    Failure classification: a missing payload / missing transcript /
    no renderable segments is *non-retryable* (a data inconsistency —
    TRANSCRIBE only enqueues SUBTITLE_GEN after activating a complete
    transcript); a transient write / IO error is *retryable*; a video
    that vanished mid-flight is terminal (mirrors EXTRACT/TRANSCRIBE's
    ``LookupError`` guard).
    """
    from .subtitle.subtitle_gen import (  # noqa: PLC0415 — avoid import cycle at module load
        MissingTranscript,
        commit_subtitles,
        load_transcript_cues,
    )

    try:
        payload = job.payload or {}
        raw_transcript_id = payload.get("transcript_id")
        if not raw_transcript_id:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="subtitle_missing_payload",
                    message=(
                        f"SUBTITLE_GEN job {job.id} has no payload.transcript_id"
                        " — TRANSCRIBE should have enqueued it with one"
                    ),
                    retryable=False,
                ),
            )
            return

        try:
            transcript_id = UUID(str(raw_transcript_id))
        except (ValueError, AttributeError, TypeError):
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="subtitle_bad_payload",
                    message=(
                        f"SUBTITLE_GEN job {job.id} payload.transcript_id"
                        f" is not a UUID: {raw_transcript_id!r}"
                    ),
                    retryable=False,
                ),
            )
            return

        loaded = await load_transcript_cues(
            cast("_SubtitleDB", db), transcript_id=transcript_id
        )
        if loaded is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="subtitle_missing_transcript",
                    message=(
                        f"no transcript / renderable segments for"
                        f" transcript_id={transcript_id}"
                        " — TRANSCRIBE should not have enqueued SUBTITLE_GEN"
                    ),
                    retryable=False,
                ),
            )
            return

        await commit_subtitles(
            cast("_SubtitleDB", db),
            video_id=job.video_id,
            loaded=loaded,
        )
        await mark_done(db, job_id=job.id)
    except MissingTranscript as exc:
        # Data inconsistency surfaced from deeper in the load — terminal.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.SUBTITLE_GEN.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="subtitle_missing_transcript",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=False,
            ),
        )
    except LookupError as exc:
        # TOCTOU: the video row vanished between the load and
        # commit_subtitles' state read. Terminal — a re-run cannot
        # resurrect it (mirrors extract/transcribe's LookupError guard).
        _log.warning(
            "stage_handler_failed",
            stage=Stage.SUBTITLE_GEN.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="subtitle_video_vanished",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=False,
            ),
        )
    except Exception as exc:  # noqa: BLE001 — funnel every failure to the retry helper
        # A write / disk / transient IO failure is transient by nature
        # → retryable.
        _log.warning(
            "stage_handler_failed",
            stage=Stage.SUBTITLE_GEN.value,
            job_id=job.id,
            video_id=str(job.video_id),
            error=str(exc),
        )
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="subtitle_failed",
                message=str(exc),
                traceback=traceback.format_exc(),
                retryable=True,
            ),
        )


def build_real_dispatch() -> dict[Stage, Callable[[DBConn, Job], Awaitable[None]]]:
    """Return the dispatch-override map for the daemon entry point.

    Only stages with a genuine thin-wrapper adapter are registered.
    Every unregistered stage (INDEX, THUMBNAIL) keeps the runtime's
    placeholder handler until its real orchestration lands — see the
    module docstring and the gap-closure plan's Track R1/R2/R3/R4
    concerns. THUMBNAIL has no implementing module at all.

    Track R2: EXTRACT now has a real adapter and joins the map. Because
    it is in the map, ``_DEFAULT_STAGES`` listing EXTRACT is safe — a
    default worker claiming an EXTRACT job runs the real decode +
    commit + TRANSCRIBE enqueue rather than the silent no-op drain.

    Track R3: TRANSCRIBE now has a real thin-wrapper adapter
    (``commit_transcribe`` inserts the transcript inactive, persists
    every backend segment via ``commit_segment``, activates it only
    after the full stream succeeds, advances the FSM
    ``AUDIO_EXTRACTED -> TRANSCRIBED``, and enqueues SUBTITLE_GEN +
    INDEX), so it joins the override map. ``_DEFAULT_STAGES`` listing
    TRANSCRIBE is now safe — a default worker claiming a TRANSCRIBE job
    runs the real STT path rather than the silent no-op drain.

    Track R4: SUBTITLE_GEN now has a real thin-wrapper adapter
    (``commit_subtitles`` loads the transcript's ordered segments,
    renders SRT + VTT via the existing pure generator, persists both
    via the pre-existing ``subtitle_files`` registry +
    content-addressed sidecar — the EXTRACT-``audio_cache`` analogue —
    and advances the FSM ``TRANSCRIBED -> INDEXED``), so it joins the
    override map. ``_DEFAULT_STAGES`` listing SUBTITLE_GEN is now safe
    — a default worker claiming a SUBTITLE_GEN job runs the real render
    + persist rather than the silent no-op drain. SUBTITLE_GEN is a
    leaf (no follow-on enqueue): TRANSCRIBE already fanned it out in
    parallel with INDEX, and the shared ``TRANSCRIBED -> INDEXED`` FSM
    edge is replay-guarded so the second of the two parallel stages
    no-ops the advance.
    """

    return {
        Stage.PROBE: probe_handler,
        Stage.EXTRACT: extract_handler,
        Stage.TRANSCRIBE: transcribe_handler,
        Stage.SUBTITLE_GEN: subtitle_handler,
    }
