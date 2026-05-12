"""Story 3.1 — :class:`STTBackend` Protocol shape + ``Segment`` schema."""

from __future__ import annotations

from collections.abc import AsyncIterator
from datetime import UTC, datetime

from maktaba_pipeline.stt.protocol import (
    BackendHealth,
    Segment,
    STTBackend,
    TranscriptionHints,
    Word,
)


class _FakeBackend:
    name = "fake"
    supports_streaming = True
    requires_file = False
    cost_per_minute = 0.0
    supports_word_timestamps = True

    async def transcribe(self, audio, language, hints):  # type: ignore[no-untyped-def]
        async def _gen() -> AsyncIterator[Segment]:
            yield Segment(seq=0, start_sec=0.0, end_sec=1.0, text="hello")

        return _gen()

    async def detect_language(self, audio):  # type: ignore[no-untyped-def]
        return "ar"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=True,
            model_loaded=True,
            version="0.0.0",
            device="cpu",
            last_check_at=datetime.now(tz=UTC),
        )

    async def warmup(self) -> None:
        return


def test_fake_backend_satisfies_protocol_at_runtime() -> None:
    backend = _FakeBackend()
    # ``STTBackend`` is ``runtime_checkable`` — the assertion fails the
    # moment the Protocol drifts from what we ask backends to expose.
    assert isinstance(backend, STTBackend)


def test_segment_dataclass_default_values() -> None:
    seg = Segment(seq=3, start_sec=0.0, end_sec=2.5, text="foo")
    assert seg.speaker is None
    assert seg.confidence is None
    assert seg.words == ()
    assert seg.metadata == {}


def test_segment_with_words_round_trips() -> None:
    word = Word(text="hello", start_sec=0.0, end_sec=0.5, confidence=0.97)
    seg = Segment(seq=0, start_sec=0.0, end_sec=0.5, text="hello", words=(word,))
    assert seg.words[0].text == "hello"


def test_transcription_hints_defaults_match_protocol_doc() -> None:
    h = TranscriptionHints()
    assert h.language is None
    assert h.initial_prompt is None
    assert h.word_timestamps is False
    assert h.start_offset_sec == 0.0
    assert h.reorder_window_sec == 30.0
