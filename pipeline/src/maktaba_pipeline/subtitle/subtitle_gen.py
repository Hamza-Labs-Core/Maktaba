"""Track R4 — SUBTITLE_GEN-stage glue (the EXTRACT/TRANSCRIBE analogue).

The SUBTITLE_GEN stage consumes exactly what TRANSCRIBE persisted: the
``transcript_id`` (carried in the job ``payload`` alongside
``audio_track_id``) and the ``transcript_segments`` rows
:func:`maktaba_pipeline.stt.segment_commit.commit_segment` wrote for it.
This module is to SUBTITLE_GEN what
:mod:`maktaba_pipeline.audio.extract` is to EXTRACT and
:mod:`maktaba_pipeline.stt.transcribe` is to TRANSCRIBE — the heavy
logic the thin :func:`maktaba_pipeline.handlers.subtitle_handler`
adapter delegates to:

- :func:`load_transcript_cues` reads back the TRANSCRIBE-produced
  transcript (its ``language``) plus every ordered segment and projects
  them into the :class:`~maktaba_pipeline.subtitle.generator.SubtitleCue`
  shape the existing renderers consume — via the existing
  :func:`maktaba_pipeline.subtitle.generator.segments_to_cues`. It does
  NOT reimplement segment loading or cue projection.
- :func:`commit_subtitles` is the SUBTITLE_GEN analogue of
  :func:`maktaba_pipeline.audio.extract.commit_extract`: it renders SRT
  + VTT via the existing pure
  :func:`~maktaba_pipeline.subtitle.generator.generate_srt` /
  :func:`~maktaba_pipeline.subtitle.generator.generate_vtt`, persists
  both artifacts via the *pre-existing* subtitle persistence helpers
  (:func:`~maktaba_pipeline.subtitle.manager.write_atomic` +
  :func:`~maktaba_pipeline.subtitle.manager.register_subtitle` into the
  ``subtitle_files`` registry — exactly the way EXTRACT reused the
  pre-existing ``audio_cache``), then advances the FSM
  ``TRANSCRIBED -> INDEXED`` via :func:`advance_after_stage`
  (replay-guarded exactly like ``commit_extract`` / ``commit_transcribe``).

Scope (Wave 0): exactly one transcript -> two artifacts (SRT + VTT),
straight-through, written to the canonical content-addressed sidecar
cache the manager owns.

NOTE(wave-0): the richer story-04-01 layout — a copy alias next to the
source media (``<source_dir>/<basename>.<lang>.srt``) and the
``<library_root>/.maktaba/subs/`` path convention — is NOT implemented
in the pre-existing :mod:`maktaba_pipeline.subtitle.manager`
(``cache_path_for`` owns a single content-addressed
``~/.maktaba/cache/subtitles/{video_id}/{source}.{lang}.{fmt}`` path).
Wiring the alias-copy + library-root path is a manager change, not a
SUBTITLE_GEN-stage change; deliberately deferred, not silent. Multi-
language / styling / library-settings-driven cue wrapping is likewise
out of Wave 0 (Story 4.2).

No successor enqueue: TRANSCRIBE already fans out BOTH SUBTITLE_GEN and
INDEX in parallel (see ``commit_transcribe``). SUBTITLE_GEN is a leaf —
it never enqueues a follow-on. The FSM edge ``TRANSCRIBED -> INDEXED``
is shared by the SUBTITLE_GEN/OK and INDEX/OK triggers; whichever of
the two parallel stages commits first advances the video, and the
other finds the row already at ``INDEXED`` and no-ops the advance
(the same replay guard ``commit_extract`` uses).
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Protocol
from uuid import UUID

if TYPE_CHECKING:
    from .generator import SubtitleCue

__all__ = [
    "MissingTranscript",
    "TranscriptCues",
    "commit_subtitles",
    "load_transcript_cues",
]


class MissingTranscript(LookupError):
    """The job's ``transcript_id`` has no transcript / no usable segments.

    A data inconsistency: TRANSCRIBE only enqueues SUBTITLE_GEN *after*
    activating a complete transcript. The handler classifies this
    non-retryable — a re-run cannot resurrect missing rows.
    """


class _SubtitleDB(Protocol):
    """The connection shape this module needs.

    A strict superset of ``commit_extract``'s ``_ExtractDB``; the
    runtime ``Database`` facade satisfies it. Tests pass the canonical
    fake.
    """

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


@dataclass(slots=True, frozen=True)
class TranscriptCues:
    """The TRANSCRIBE-produced transcript projected for the renderers."""

    transcript_id: UUID
    video_id: UUID
    language: str
    cues: list[SubtitleCue]


_SELECT_TRANSCRIPT = """
SELECT id, video_id, language
  FROM transcripts
 WHERE id = $1
"""

_SELECT_SEGMENTS = """
SELECT seq, start_sec, end_sec, text, speaker
  FROM transcript_segments
 WHERE transcript_id = $1
 ORDER BY seq
"""


async def load_transcript_cues(
    db: _SubtitleDB,
    *,
    transcript_id: UUID,
) -> TranscriptCues | None:
    """Read the transcript + its ordered segments, projected to cues.

    Returns ``None`` when the transcript row is missing or has zero
    renderable segments — the caller treats that as an unrecoverable
    data inconsistency (TRANSCRIBE only enqueues SUBTITLE_GEN after a
    complete transcript was activated).

    Segment loading + the cue projection are NOT reimplemented: the
    rows are fed straight through the existing
    :func:`maktaba_pipeline.subtitle.generator.segments_to_cues`
    (empty-text rows are dropped there).
    """
    from .generator import segments_to_cues  # noqa: PLC0415 — avoid import cycle

    tr = await db.fetchrow(_SELECT_TRANSCRIPT, transcript_id)
    if tr is None:
        return None

    seg_rows = await db.fetch(_SELECT_SEGMENTS, transcript_id)
    cues = segments_to_cues(
        {
            "start_sec": r["start_sec"],
            "end_sec": r["end_sec"],
            "text": r["text"],
            "speaker": r["speaker"],
        }
        for r in seg_rows
    )
    if not cues:
        return None

    return TranscriptCues(
        transcript_id=UUID(str(tr["id"])),
        video_id=UUID(str(tr["video_id"])),
        language=str(tr["language"]),
        cues=cues,
    )


async def commit_subtitles(
    db: _SubtitleDB,
    *,
    video_id: UUID,
    loaded: TranscriptCues,
    cache_root: str | None = None,
) -> str:
    """Render SRT + VTT, persist both, and advance the video state.

    Returns the new ``videos.state``. The SUBTITLE_GEN analogue of
    :func:`maktaba_pipeline.audio.extract.commit_extract`:

    1. render both formats via the existing pure
       :func:`~maktaba_pipeline.subtitle.generator.generate_srt` /
       :func:`~maktaba_pipeline.subtitle.generator.generate_vtt` (the
       rendering logic is NOT reimplemented here),
    2. write each to its deterministic content-addressed sidecar via
       the pre-existing
       :func:`~maktaba_pipeline.subtitle.manager.write_atomic` and
       register the row via
       :func:`~maktaba_pipeline.subtitle.manager.register_subtitle`
       (idempotent UPSERT on ``(video_id, language, format, source)`` —
       a re-run overwrites in place, exactly like EXTRACT's
       content-addressed ``audio_cache`` UPSERT — so no duplicate
       artifacts on replay),
    3. advance the FSM ``TRANSCRIBED -> INDEXED`` via
       :func:`advance_after_stage` (its terminal-drop guard + the
       explicit state check make a replay a no-op — exactly the
       ``commit_extract`` / ``commit_transcribe`` shape).

    No follow-on enqueue: SUBTITLE_GEN is a parallel leaf TRANSCRIBE
    already fanned out alongside INDEX.

    Idempotent on replay: ``register_subtitle``'s ON CONFLICT upsert and
    the FSM ``late_stage_finish`` / state-check guard both tolerate a
    repeat; ``write_atomic`` overwrites the same deterministic path.
    """
    from ..domain.states import Outcome, State, Trigger  # noqa: PLC0415
    from ..log import get_logger  # noqa: PLC0415
    from ..orchestrator.advance import advance_after_stage  # noqa: PLC0415
    from .formats import SubtitleFormat  # noqa: PLC0415
    from .generator import generate_srt, generate_vtt  # noqa: PLC0415
    from .manager import (  # noqa: PLC0415
        SubtitleRecord,
        SubtitleSource,
        cache_path_for,
        register_subtitle,
        write_atomic,
    )

    log = get_logger()

    renderers = (
        (SubtitleFormat.SRT, generate_srt(loaded.cues)),
        (SubtitleFormat.VTT, generate_vtt(loaded.cues)),
    )
    for fmt, content in renderers:
        dest = cache_path_for(
            video_id,
            loaded.language,
            fmt,
            SubtitleSource.GENERATED,
            root=cache_root,
        )
        byte_size, sha256 = write_atomic(dest, content)
        await register_subtitle(
            _as_manager_db(db),
            SubtitleRecord(
                video_id=video_id,
                language=loaded.language,
                format=fmt,
                source=SubtitleSource.GENERATED,
                path=dest,
                byte_size=byte_size,
                sha256=sha256,
                transcript_id=loaded.transcript_id,
                metadata={"transcript_id": str(loaded.transcript_id)},
            ),
        )

    state_row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", video_id)
    if state_row is None:
        raise LookupError(f"video {video_id} not found")
    current_state = State(state_row["state"])

    if current_state == State.TRANSCRIBED:
        new_state = await advance_after_stage(
            db, video_id, Trigger.SUBTITLE_GEN, Outcome.OK, log=log
        )
    else:
        # Replay / parallel-stage race: INDEX (the other stage TRANSCRIBE
        # fanned out) already advanced TRANSCRIBED -> INDEXED, or this
        # job is a retry. Leave the row where it is — the FSM has no
        # TRANSCRIBED <- edge from INDEXED, mirroring the commit_extract
        # / commit_transcribe replay guard.
        new_state = current_state

    log.info(
        "subtitles_committed",
        video_id=str(video_id),
        transcript_id=str(loaded.transcript_id),
        language=loaded.language,
        cues=len(loaded.cues),
        new_state=str(new_state),
    )
    return str(new_state)


def _as_manager_db(db: _SubtitleDB) -> Any:
    # ``register_subtitle`` expects its own (narrower) Protocol; the
    # subtitle DB shape is a strict superset, so the cast is type-safe
    # at runtime (mirrors ``extract._as_job_db`` / ``transcribe._as_job_db``).
    return db
