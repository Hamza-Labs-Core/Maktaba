"""``extract`` on the engine (Epic 54 story 54.36 §2): the WAV a speech backend that reads a
file needs.

Before, ``extract`` was a legacy handler (``handlers.extract_handler``): it marked its own job
done and queued ``transcribe`` by hand, and the engine then recorded a hash of the time. Now
the stage asks for ``transcribe`` as a declared request (``may_request``): a request, not an
edge, because it carries the payload transcribe reads (``content_hash``: extract ran) and,
for a streaming backend, is left out when the video's transcription is already handled, as
before. Its output is the WAV it wrote (or that it wrote none). The track choice, the decode,
the saved artifact, the video's state and the failure kinds are the handler's, unchanged."""

from __future__ import annotations

from pathlib import Path
from typing import Any, cast

from maktaba_pipeline.audio.extract import (
    ExtractError,
    _ExtractDB,
    commit_extract,
    commit_extract_skipped,
    extract_to_file,
    load_selected_track,
    transcribe_payload,
    transcription_handled,
)
from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.context import StageContext, StageFailure
from maktaba_pipeline.engine.graph import Request, StageResult
from maktaba_pipeline.engine.hashing import canonical_hash
from maktaba_pipeline.engine.paths import to_local
from maktaba_pipeline.engine.stages.registry import StageImpl
from maktaba_pipeline.log import get_logger

__all__ = ["IMPL"]

_log = get_logger()

# the decoder, a module attribute so a test can swap it for a fake ffmpeg
decode = extract_to_file


def requires_file() -> bool:
    """Whether the configured speech backend reads a file (else no WAV is written)."""
    from maktaba_pipeline.stt.transcribe import configured_backend_requires_file

    return configured_backend_requires_file()


async def run(db: Any, job: Job, ctx: StageContext) -> StageResult:
    if job.video_id is None:
        # impossible under the per-video scope check; a re-run cannot invent the video
        raise StageFailure(
            "extract_missing_source", f"extract job {job.id} has no video_id", retryable=False
        )
    row = await db.fetchrow("SELECT path, content_hash FROM videos WHERE id = $1", job.video_id)
    if row is None or row["path"] is None or row["content_hash"] is None:
        raise StageFailure(
            "extract_missing_source",
            f"no videos.path/content_hash for video_id={job.video_id}",
            retryable=False,
        )
    content_hash = str(row["content_hash"])
    xdb = cast("_ExtractDB", db)
    try:
        selected = await load_selected_track(xdb, video_id=job.video_id)
        if selected is None:
            raise StageFailure(
                "extract_no_audio_track",
                f"no selectable audio_tracks row for video_id={job.video_id}"
                " — PROBE should not have enqueued EXTRACT",
                retryable=False,
            )
        ask = True
        if not requires_file():
            state = await commit_extract_skipped(
                xdb,
                video_id=job.video_id,
                audio_track_id=selected.db_id,
                content_hash=content_hash,
                queue_transcribe=False,
            )
            ask = not await transcription_handled(xdb, job.video_id)
            wav: str | None = None
        else:
            dest = Path(
                await decode(
                    to_local(str(row["path"])), selected.track.index, content_hash=content_hash
                )
            )
            try:
                size: int | None = dest.stat().st_size
            except OSError:
                size = None
            wav = dest.as_posix()  # stored with "/" on every OS
            state = await commit_extract(
                xdb,
                video_id=job.video_id,
                audio_track_id=selected.db_id,
                content_hash=content_hash,
                cache_path=wav,
                bytes_written=size,
                queue_transcribe=False,
            )
    except StageFailure:
        raise
    except ExtractError as exc:  # ffmpeg ran and failed: transient by nature
        raise StageFailure(exc.kind, str(exc), retryable=True) from exc
    except LookupError as exc:  # the video vanished mid-flight: a re-run cannot bring it back
        raise StageFailure("extract_video_vanished", str(exc), retryable=False) from exc
    except Exception as exc:  # any other failure: another run may succeed
        raise StageFailure("extract_failed", str(exc), retryable=True) from exc
    requests: tuple[Request, ...] = ()
    if ask:
        requests = (
            Request(
                stage="transcribe",
                subject_kind="video",
                subject_id=job.video_id,
                reason="extract wrote the audio" if wav else "extract ran (a streaming backend)",
                payload=transcribe_payload(selected.db_id, content_hash),
                priority=100,
            ),
        )
    return StageResult(
        output_hash=canonical_hash({"track": selected.db_id, "wav": wav}),
        requests=requests,
        summary={"track": selected.db_id, "wav": wav is not None, "state": state},
    )


# no input fingerprint: a queued extract always runs, as the legacy stage did
IMPL = StageImpl(run=run)
