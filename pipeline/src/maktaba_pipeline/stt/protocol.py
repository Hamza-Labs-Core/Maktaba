"""Story 3.1 — :class:`STTBackend` Protocol and the canonical ``Segment``.

Every concrete backend implements this single Protocol; a shared
conformance suite (in ``tests/stt/test_conformance_suite.py``) exercises
the same fixtures against every backend so adding a new one is "write
a class and the suite passes — nothing else".

The ``Segment`` schema mirrors ``architecture.md §3.4``: ``seq``,
``start_sec``, ``end_sec``, ``text``, optional ``speaker``,
``confidence``, optional ``words``. Backends that don't support a
field set it to ``None`` and downstream callers must not assume the
field is populated.

``AudioSource`` is the union the orchestrator picks between based on
``backend.requires_file``. The streaming path passes a byte iterator;
the file path passes a path. Backends are free to accept either or
raise ``NotImplementedError`` if asked for the wrong shape.
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Protocol, Union, runtime_checkable

__all__ = [
    "AudioSource",
    "BackendHealth",
    "Segment",
    "STTBackend",
    "TranscriptionHints",
    "Word",
    "parse_words",
]


@dataclass(slots=True, frozen=True)
class Word:
    """Optional word-level timing — emitted when the backend supports it."""

    text: str
    start_sec: float
    end_sec: float
    confidence: float | None = None


def parse_words(raw: Any) -> tuple[Word, ...]:
    """Map a backend's raw per-word list onto :class:`Word` tuples.

    Story 3.6-1.3 — every backend that sets
    ``supports_word_timestamps`` and is asked for word timestamps
    (``hints.word_timestamps``) returns a ``words`` list on each raw
    segment dict (``mlx_whisper`` / ``faster-whisper`` / the OpenAI
    verbose-JSON shape all use ``{word|text, start, end,
    probability|confidence}``). This single parser is the one place
    that normalises those vendor key variants so the three backends'
    Segment-mapping stays DRY and the ``transcript_words`` rows
    :func:`maktaba_pipeline.stt.segment_commit.commit_segment` persists
    have a uniform shape regardless of which backend produced them.

    A missing / empty / non-list ``words`` value yields an empty tuple
    (the Segment default) — word timestamps are strictly optional and a
    backend that did not produce them must not raise here.
    """
    if not raw or not isinstance(raw, (list, tuple)):
        return ()
    out: list[Word] = []
    for w in raw:
        if not isinstance(w, dict):
            continue
        text = w.get("word", w.get("text"))
        start = w.get("start", w.get("start_sec"))
        end = w.get("end", w.get("end_sec"))
        if text is None or start is None or end is None:
            continue
        conf = w.get("probability", w.get("confidence"))
        out.append(
            Word(
                text=str(text),
                start_sec=float(start),
                end_sec=float(end),
                confidence=float(conf) if conf is not None else None,
            )
        )
    return tuple(out)


@dataclass(slots=True, frozen=True)
class Segment:
    """Canonical transcript segment.

    Backends MUST emit segments in monotonic order (``seg[i].end_sec
    <= seg[i+1].start_sec + ε``); the conformance suite enforces this
    with ε = 0.05. The orchestrator's reorder buffer (Story 3.6) catches
    out-of-order emissions from non-streaming backends.

    ``audio_sec`` and ``wall_sec`` carry the per-segment timing the
    EWMA realtime-factor needs (Story 3.6). When the backend can't
    measure wall time per segment (e.g. OpenAI API), the orchestrator
    fills these from its own clock.
    """

    seq: int
    start_sec: float
    end_sec: float
    text: str
    speaker: str | None = None
    confidence: float | None = None
    words: tuple[Word, ...] = field(default_factory=tuple)
    audio_sec: float | None = None
    wall_sec: float | None = None
    metadata: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True, frozen=True)
class TranscriptionHints:
    """Per-job knobs the orchestrator passes to ``backend.transcribe``.

    - ``language`` — ISO 639-1 forcing flag. ``None`` means autodetect.
    - ``initial_prompt`` — biases the decoder. Default for Arabic
      libraries is the bismillah; per architecture §3.4.
    - ``word_timestamps`` — enables per-word output for backends that
      support it. False saves a non-trivial amount of RAM on Whisper.
    - ``start_offset_sec`` — resume seek (Story 3.7); the backend should
      apply it to its own decoder. The extract stage already seeks
      ffmpeg via ``-ss``, so most backends ignore this.
    - ``reorder_window_sec`` — the orchestrator's buffer length for
      out-of-order segments. The backend doesn't honour it; it's here
      so the orchestrator can pass it through to the conformance suite.
    """

    language: str | None = None
    initial_prompt: str | None = None
    word_timestamps: bool = False
    start_offset_sec: float = 0.0
    reorder_window_sec: float = 30.0


@dataclass(slots=True, frozen=True)
class BackendHealth:
    """``backend.health()`` payload — reported on ``/api/system/health``."""

    ready: bool
    model_loaded: bool
    version: str
    device: str
    last_check_at: datetime
    details: dict[str, Any] = field(default_factory=dict)


# Streaming backends consume an async iterator of PCM chunks; file
# backends consume a path. The orchestrator chooses based on
# ``backend.requires_file``.
AudioSource = Union[AsyncIterator[bytes], "str"]


@runtime_checkable
class STTBackend(Protocol):
    """The Protocol every backend implements (Story 3.1 AC-1)."""

    name: str
    #: The concrete model identity stamped onto ``transcripts.model``
    #: (e.g. ``"large-v3"`` for Whisper, ``"whisper-1"`` for the OpenAI
    #: API). This is the *model* — distinct from ``name`` (the backend
    #: vendor key) and the runtime ``version``. Every concrete backend
    #: is constructed with ``model=`` and exposes it here.
    model: str
    supports_streaming: bool
    requires_file: bool
    cost_per_minute: float | None
    supports_word_timestamps: bool

    def transcribe(  # noqa: D401 — Protocol method
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        """Yield :class:`Segment` objects in monotonic time order."""
        ...

    async def detect_language(self, audio: AudioSource) -> str:
        """Return the detected ISO 639-1 language code."""
        ...

    async def health(self) -> BackendHealth:
        """Return current readiness without touching the model."""
        ...

    async def warmup(self) -> None:
        """Eager-load the model so the first ``transcribe`` is hot.

        Default implementation is a no-op (``await asyncio.sleep(0)``)
        so backends without a measurable cold start can skip it.
        """
        ...
