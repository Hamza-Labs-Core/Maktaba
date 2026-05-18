"""Track R3 — TRANSCRIBE-stage glue (the EXTRACT analogue for STT).

The TRANSCRIBE stage consumes exactly what EXTRACT persisted: the
``audio_cache`` artifact (a decoded 16 kHz mono WAV keyed by the
video's ``content_hash``) plus the ``audio_track_id`` it was extracted
from. This module is to TRANSCRIBE what
:mod:`maktaba_pipeline.audio.extract` is to EXTRACT — the heavy logic
the thin :func:`maktaba_pipeline.handlers.transcribe_handler` adapter
delegates to:

- :func:`load_audio_artifact` reconstructs the cached-WAV reference the
  EXTRACT stage wrote (``audio_cache`` PK lookup).
- :func:`default_select_backend` is the production DI default for the
  handler's backend seam: it builds a :class:`BackendRegistry` from the
  *configured* STT backend (never a hardcoded vendor) and walks the
  fallback chain via :func:`pick_backend`. Tests inject a fake selector
  yielding a canned backend so no model is ever loaded.
- :func:`commit_transcribe` is the TRANSCRIBE analogue of
  :func:`maktaba_pipeline.audio.extract.commit_extract`: it creates +
  activates the transcript row, streams every backend segment through
  the existing :func:`maktaba_pipeline.stt.segment_commit.commit_segment`
  (the hot path stays there), advances the FSM
  ``AUDIO_EXTRACTED -> TRANSCRIBED`` via :func:`advance_after_stage`
  (replay-guarded exactly like ``commit_extract``), and enqueues the
  follow-on ``SUBTITLE_GEN`` + ``INDEX`` jobs via the *same* idempotent
  per-video :func:`enqueue` mechanism.

Scope (Wave 0): exactly one job -> one track -> one transcript,
straight-through. Pause/resume & crash-recovery mid-transcription is a
SEPARATE later story — see the resume marker in :func:`commit_transcribe`.
"""

from __future__ import annotations

from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Any, Protocol
from uuid import UUID

if TYPE_CHECKING:
    from .protocol import STTBackend

__all__ = [
    "AudioArtifact",
    "BackendSelector",
    "SelectedBackend",
    "commit_transcribe",
    "default_select_backend",
    "load_audio_artifact",
]


class _TranscribeDB(Protocol):
    """The connection shape this module needs.

    A strict superset of ``commit_extract``'s ``_ExtractDB`` (it also
    drives ``flip_active_transcript`` / ``commit_segment``); the runtime
    ``Database`` facade satisfies it. Tests pass the canonical fake.
    """

    dialect: str

    def transaction(self) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> Any: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


@dataclass(slots=True, frozen=True)
class AudioArtifact:
    """The EXTRACT-produced cached-WAV reference TRANSCRIBE consumes."""

    content_hash: str
    video_id: UUID
    audio_track_id: int
    path: str


@dataclass(slots=True, frozen=True)
class SelectedBackend:
    """A picked backend plus the identity stamped on the transcript row."""

    backend: STTBackend
    name: str
    version: str | None


# DI seam for the handler: ``(* , video_id) -> (backend, name, version)``.
# The default builds a registry from the *configured* backend and walks
# the fallback chain (:func:`default_select_backend`); tests inject a
# fake returning a canned backend so the suite never loads a model.
BackendSelector = Callable[..., Awaitable[tuple["STTBackend", str, str | None]]]


_SELECT_AUDIO_CACHE = """
SELECT content_hash, video_id, audio_track_id, path, bytes
  FROM audio_cache
 WHERE content_hash = $1
"""


async def load_audio_artifact(
    db: _TranscribeDB,
    *,
    content_hash: str,
) -> AudioArtifact | None:
    """Read back the ``audio_cache`` row EXTRACT's ``commit_extract`` wrote.

    Returns ``None`` when no row exists for ``content_hash`` — the
    caller treats that as an unrecoverable data inconsistency (EXTRACT
    only enqueues TRANSCRIBE *after* persisting the artifact).
    """
    row = await db.fetchrow(_SELECT_AUDIO_CACHE, content_hash)
    if row is None or row["path"] is None:
        return None
    return AudioArtifact(
        content_hash=str(row["content_hash"]),
        video_id=row["video_id"],
        audio_track_id=int(row["audio_track_id"]),
        path=str(row["path"]),
    )


async def default_select_backend(
    *,
    video_id: UUID,
    settings: Any | None = None,
) -> tuple[STTBackend, str, str | None]:
    """Production default for the handler's backend DI seam.

    Builds a :class:`BackendRegistry` from the *configured* STT backend
    (``library.settings.stt.backend`` → ``pipeline.toml`` →
    :data:`maktaba_pipeline.library_mgmt.config.EFFECTIVE_DEFAULTS`) and
    walks the fallback chain via :func:`pick_backend`. The vendor is
    **never hardcoded here** — it comes entirely from config. The heavy
    backend classes (``mlx_whisper`` / ``faster_whisper`` / ``openai``)
    are imported lazily inside the factory so importing this module
    costs nothing.

    NOTE (Wave-0 selection policy): the library-scoped settings plumbing
    (per-library ``stt.backend``/``stt.fallback``) is not yet wired into
    the worker — see Story 3.5 / 9.1. Until it is, this resolves the
    process-wide configured default. Substituting the library override
    is a settings-plumbing change, *not* a TRANSCRIBE-stage change; the
    seam keeps the handler agnostic.
    """
    from ..library_mgmt.config import EFFECTIVE_DEFAULTS  # noqa: PLC0415
    from .registry import pick_backend  # noqa: PLC0415

    stt_cfg = dict(EFFECTIVE_DEFAULTS.get("stt", {}))
    if settings is not None:
        stt_cfg.update(getattr(settings, "stt", None) or {})
    primary = str(stt_cfg.get("backend", "whisper-mlx"))
    fallback = list(stt_cfg.get("fallback", []) or [])
    model = str(stt_cfg.get("model", "large-v3"))

    registry = _build_registry(model=model)
    backend, trace = await pick_backend(
        registry, primary=primary, fallback=fallback
    )
    return backend, trace.chosen, _backend_version(backend)


def _build_registry(*, model: str) -> Any:
    """Register the whitelisted backends. Lazy heavy imports only."""
    from .registry import BackendRegistry  # noqa: PLC0415

    registry = BackendRegistry()
    # Each register is best-effort: a backend whose optional heavy dep
    # is absent simply isn't registered, and ``pick_backend`` walks past
    # it. The configured primary still drives the choice.
    try:  # pragma: no cover - depends on optional deps at runtime
        from .mlx import WhisperMLXBackend  # noqa: PLC0415

        registry.register(WhisperMLXBackend(model=model))
    except Exception:  # noqa: BLE001
        pass
    try:  # pragma: no cover
        from .faster_whisper import FasterWhisperBackend  # noqa: PLC0415

        registry.register(FasterWhisperBackend(model=model))
    except Exception:  # noqa: BLE001
        pass
    try:  # pragma: no cover
        from typing import cast as _cast  # noqa: PLC0415

        from .openai_api import OpenAIWhisperBackend  # noqa: PLC0415
        from .protocol import STTBackend as _STTBackend  # noqa: PLC0415

        # OpenAIWhisperBackend narrows ``cost_per_minute`` to ``float``
        # (the Protocol declares ``float | None``); the cast bridges the
        # invariant-attribute mismatch — it is structurally a backend.
        registry.register(_cast("_STTBackend", OpenAIWhisperBackend()))
    except Exception:  # noqa: BLE001
        pass
    return registry


def _backend_version(backend: STTBackend) -> str | None:
    # Concrete backends expose the model/runtime version as ``version``
    # (public) or ``_version`` (the mlx/openai/faster-whisper private
    # convention). Either is fine for the transcript-history stamp.
    v = getattr(backend, "version", None)
    if v is None:
        v = getattr(backend, "_version", None)
    return str(v) if v is not None else None


async def commit_transcribe(
    db: _TranscribeDB,
    *,
    video_id: UUID,
    artifact: AudioArtifact,
    selected: SelectedBackend,
    job_id: int,
    total_duration_sec: float,
    language: str | None,
    detected_language: str | None = None,
) -> str:
    """Create the transcript, persist every segment, advance + enqueue.

    Returns the new ``videos.state``. The TRANSCRIBE analogue of
    :func:`maktaba_pipeline.audio.extract.commit_extract`:

    1. :func:`flip_active_transcript` atomically retires any prior
       active transcript for ``(video_id, audio_track_id)`` and inserts
       the new active row,
    2. iterate the backend's segment stream and persist each via the
       existing :func:`commit_segment` (the hot path / progress
       accounting stays there — not reimplemented),
    3. advance the FSM ``AUDIO_EXTRACTED -> TRANSCRIBED`` via
       :func:`advance_after_stage` (its terminal-drop guard + the
       explicit state check make a replay a no-op — exactly the
       ``commit_extract`` shape),
    4. enqueue the follow-on ``SUBTITLE_GEN`` + ``INDEX`` jobs via
       :func:`enqueue` — the *same* idempotent per-video mechanism
       ``commit_extract`` uses for the TRANSCRIBE enqueue.

    Idempotent on replay: ``flip_active_transcript`` creates a fresh
    transcript (history rows are unconstrained; exactly-one-active is
    preserved), ``commit_segment``'s ON CONFLICT swallows duplicate
    seqs, the FSM guard tolerates a repeat, and the ``enqueue``
    unique-live index dedupes the downstream rows.
    """
    from ..db.jobs import Stage as _JobStage  # noqa: PLC0415
    from ..db.jobs import enqueue  # noqa: PLC0415
    from ..domain.states import Outcome, State, Trigger  # noqa: PLC0415
    from ..log import get_logger  # noqa: PLC0415
    from ..orchestrator.advance import advance_after_stage  # noqa: PLC0415
    from .protocol import TranscriptionHints  # noqa: PLC0415
    from .registry import flip_active_transcript  # noqa: PLC0415
    from .segment_commit import commit_segment  # noqa: PLC0415

    log = get_logger()

    lang = language or detected_language or "auto"
    transcript_id = await flip_active_transcript(
        db,
        video_id=video_id,
        audio_track_id=artifact.audio_track_id,
        language=lang,
        detected_language=detected_language,
        language_confidence=None,
        backend=selected.name,
        model=getattr(selected.backend, "model", selected.name),
        backend_version=selected.version,
        word_level=False,
        diarized=False,
        metadata={"content_hash": artifact.content_hash},
    )

    # Stream the backend's segments straight through commit_segment.
    # TODO(resume): mid-transcription pause/resume is Story 3.6-3/3.7 —
    # out of Wave 0. A resumed job would seek the decoder + skip the
    # already-committed seq prefix here (and the reorder buffer would
    # plug in front of commit_segment); the straight-through path emits
    # the full stream from seq 0. Deliberately deferred, not silent.
    hints = TranscriptionHints(language=language)
    segment_stream = selected.backend.transcribe(artifact.path, language, hints)
    async for segment in segment_stream:
        await commit_segment(
            db,
            transcript_id=transcript_id,
            job_id=job_id,
            segment=segment,
            total_duration_sec=total_duration_sec,
        )

    state_row = await db.fetchrow("SELECT state FROM videos WHERE id = $1", video_id)
    if state_row is None:
        raise LookupError(f"video {video_id} not found")
    current_state = State(state_row["state"])

    if current_state == State.AUDIO_EXTRACTED:
        new_state = await advance_after_stage(
            db, video_id, Trigger.TRANSCRIBE, Outcome.OK, log=log
        )
    else:
        # Replay / out-of-order: leave the row where it is. The FSM has
        # no AUDIO_EXTRACTED <- edge from TRANSCRIBED, mirroring the
        # commit_extract replay guard.
        new_state = current_state

    # The downstream payload contract: SUBTITLE_GEN + INDEX both need to
    # locate the *exact* transcript this stage produced without racing a
    # "which transcript is active" re-query (a re-run could flip
    # is_active mid-flight). Carrying the transcript id + the track is
    # the minimal-but-sufficient set — mirrors how EXTRACT chose
    # {audio_track_id, content_hash}.
    downstream_payload = {
        "transcript_id": str(transcript_id),
        "audio_track_id": artifact.audio_track_id,
    }
    for stage in (_JobStage.SUBTITLE_GEN, _JobStage.INDEX):
        await enqueue(
            _as_job_db(db),
            video_id=video_id,
            stage=stage,
            priority=100,
            payload=downstream_payload,
        )

    log.info(
        "transcribe_committed",
        video_id=str(video_id),
        audio_track_id=artifact.audio_track_id,
        transcript_id=str(transcript_id),
        backend=selected.name,
        new_state=str(new_state),
    )
    return str(new_state)


def _as_job_db(db: _TranscribeDB) -> Any:
    # The job-queue helpers expect their own Protocol; the transcribe DB
    # shape is a strict superset, so the cast is type-safe at runtime
    # (mirrors ``extract._as_job_db`` / ``probe._as_job_db``).
    return db
