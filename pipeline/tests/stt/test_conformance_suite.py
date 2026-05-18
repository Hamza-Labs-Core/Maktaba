"""Story 3.1-4 / HLB-310 — the cross-backend STT conformance suite.

``protocol.py``'s module docstring promises *"a shared conformance
suite (in ``tests/stt/test_conformance_suite.py``) exercises the same
fixtures against every backend so adding a new one is 'write a class
and the suite passes — nothing else'"*. That file did not exist; this
is it.

The suite is a single set of contract assertions parametrised across
**every** concrete backend (``whisper-mlx``, ``whisper-cpu``,
``openai-api``). Each backend is constructed with its injected
``transcribe_fn`` DI seam returning a canonical fixture, so the suite
never loads mlx_whisper / faster-whisper / the OpenAI SDK — exactly
the "fakes for the heavy models" the gap analysis calls for. Adding a
fourth backend means adding one ``_make_*`` entry to ``BACKENDS`` and
the whole contract is enforced for it for free.

Contract enforced (the spec's named conformance cases, AC 3.1-4):

- ``test_transcribe_yields_segments``      — non-empty Segment stream
- ``test_segments_monotonic``              — ``seg[i].end <= seg[i+1].start + ε``
- ``test_segments_cover_audio``            — union of spans covers the clip
- ``test_segment_shape``                   — Protocol field types
- ``test_word_timestamps``                 — words present + bounded iff
                                             ``supports_word_timestamps``
- ``test_language_detection``              — ``detect_language`` returns a code
- ``test_pause_between_segments``          — a silence gap is preserved
- ``test_satisfies_protocol``              — ``isinstance(b, STTBackend)``

These are real behavioural assertions on the live ``backend.transcribe``
path (the same code the TRANSCRIBE stage drives), not shape-only checks.
Uses ``asyncio.run`` like the rest of ``tests/stt`` (the netguard reason
— intentionally NOT ``unit``-marked).
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any, cast

import pytest

from maktaba_pipeline.stt.faster_whisper import FasterWhisperBackend
from maktaba_pipeline.stt.mlx import WhisperMLXBackend
from maktaba_pipeline.stt.openai_api import OpenAIWhisperBackend
from maktaba_pipeline.stt.protocol import STTBackend, TranscriptionHints

# A canonical 6 s clip: three segments, the 2nd→3rd boundary carries a
# 0.5 s silence gap (the "pause between segments" case). Per-word timing
# is supplied for the word-capable backends; the words tile each
# segment's span exactly. This is the single fixture every backend sees.
_RAW_SEGMENTS: list[dict[str, Any]] = [
    {
        "start": 0.0,
        "end": 2.0,
        "text": " bismillah ",
        "confidence": 0.97,
        "words": [
            {"word": "bismi", "start": 0.0, "end": 1.0, "probability": 0.98},
            {"word": "llah", "start": 1.0, "end": 2.0, "probability": 0.96},
        ],
    },
    {
        "start": 2.0,
        "end": 4.0,
        "text": "al-hamdu",
        "confidence": 0.95,
        "words": [
            {"word": "al", "start": 2.0, "end": 2.7, "probability": 0.9},
            {"word": "hamdu", "start": 2.7, "end": 4.0, "probability": 0.93},
        ],
    },
    # 0.5 s silence (4.0 → 4.5) before the final segment.
    {
        "start": 4.5,
        "end": 6.0,
        "text": "lillah",
        "confidence": 0.91,
        "words": [
            {"word": "li", "start": 4.5, "end": 5.0, "probability": 0.88},
            {"word": "llah", "start": 5.0, "end": 6.0, "probability": 0.9},
        ],
    },
]

_AUDIO_DURATION = 6.0
_EPS = 0.05  # the monotonicity ε the Segment docstring specifies


def _fake_transcribe(_path: str, _kwargs: dict[str, Any]) -> list[dict[str, Any]]:
    # Return fresh copies so a backend mutating the dict can't bleed
    # into the next parametrised backend.
    return [dict(s) for s in _RAW_SEGMENTS]


def _make_mlx() -> STTBackend:
    return WhisperMLXBackend(force_ready=True, transcribe_fn=_fake_transcribe)


def _make_faster_whisper() -> STTBackend:
    return FasterWhisperBackend(device="cpu", force_ready=True, transcribe_fn=_fake_transcribe)


def _make_openai() -> STTBackend:
    # OpenAIWhisperBackend narrows ``cost_per_minute`` to ``float`` (the
    # Protocol declares ``float | None``); the cast bridges that
    # invariant-attribute mismatch exactly as production
    # ``transcribe._build_registry`` does — it is structurally a backend.
    return cast("STTBackend", OpenAIWhisperBackend(transcribe_fn=_fake_transcribe))


# Every concrete backend the registry can pick. Add a row here and the
# entire contract below is enforced for the new backend automatically.
BACKENDS = {
    "whisper-mlx": _make_mlx,
    "whisper-cpu": _make_faster_whisper,
    "openai-api": _make_openai,
}

_PARAMS = list(BACKENDS.items())
_IDS = list(BACKENDS)


@pytest.fixture
def clip(tmp_path: Path) -> str:
    """A real (tiny) on-disk clip.

    Backends with ``requires_file=True`` (OpenAI) plan chunks off the
    file's size via ``compute_chunk_offsets``, which returns ``[]`` for
    a nonexistent path — so the fixture must actually exist on disk.
    The bytes are irrelevant: every backend's transcribe is driven
    through the injected fake ``transcribe_fn``.
    """
    p = tmp_path / "clip.wav"
    p.write_bytes(b"\x00" * 4096)
    return str(p)


def _run(backend: STTBackend, clip_path: str, *, word_timestamps: bool = True) -> list[Any]:
    hints = TranscriptionHints(word_timestamps=word_timestamps)

    async def _drive() -> list[Any]:
        out: list[Any] = []
        async for seg in backend.transcribe(clip_path, "ar", hints):
            out.append(seg)
        return out

    return asyncio.run(_drive())


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_satisfies_protocol(name: str, factory: Any) -> None:
    backend = factory()
    assert isinstance(backend, STTBackend), f"{name} is not an STTBackend"
    assert isinstance(backend.name, str) and backend.name
    assert isinstance(backend.model, str) and backend.model


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_transcribe_yields_segments(name: str, factory: Any, clip: str) -> None:
    segs = _run(factory(), clip)
    assert len(segs) == 3, f"{name} dropped segments"


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_segments_monotonic(name: str, factory: Any, clip: str) -> None:
    segs = _run(factory(), clip)
    assert [s.seq for s in segs] == [0, 1, 2], f"{name} seq not 0..n"
    for a, b in zip(segs, segs[1:], strict=False):
        assert a.end_sec <= b.start_sec + _EPS, (
            f"{name} non-monotonic: {a.end_sec} !<= {b.start_sec}+{_EPS}"
        )


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_segments_cover_audio(name: str, factory: Any, clip: str) -> None:
    segs = _run(factory(), clip)
    covered = sum(s.end_sec - s.start_sec for s in segs)
    # The clip is 6 s with a 0.5 s real silence; covered speech must be
    # the remaining ~5.5 s (allow ε slack), and never exceed the clip.
    assert covered == pytest.approx(5.5, abs=_EPS), f"{name} coverage={covered}"
    assert segs[-1].end_sec <= _AUDIO_DURATION + _EPS


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_segment_shape(name: str, factory: Any, clip: str) -> None:
    for s in _run(factory(), clip):
        assert isinstance(s.seq, int)
        assert isinstance(s.start_sec, float)
        assert isinstance(s.end_sec, float)
        # Text is non-empty and has no leading/trailing newlines. (Only
        # the MLX backend's AC mandates full NFC + whitespace trim —
        # 3.2-5; the contract common to ALL backends is just "a usable
        # string", so the suite asserts the universal floor, not MLX's
        # stricter normalisation.)
        assert isinstance(s.text, str) and s.text.strip()
        assert s.start_sec <= s.end_sec
        assert s.confidence is None or isinstance(s.confidence, float)


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_word_timestamps(name: str, factory: Any, clip: str) -> None:
    backend = factory()
    segs = _run(backend, clip)
    if backend.supports_word_timestamps:
        # Word timestamps requested + supported → every segment carries
        # words whose spans stay inside the segment and are monotonic
        # (this is the chain that feeds transcript_words, Story 3.6-1.3).
        for s in segs:
            assert s.words, f"{name} supports words but emitted none"
            assert s.words[0].start_sec >= s.start_sec - _EPS
            assert s.words[-1].end_sec <= s.end_sec + _EPS
            for w in s.words:
                assert isinstance(w.text, str) and w.text
                assert w.start_sec <= w.end_sec
    else:
        # A backend that doesn't support words must yield none rather
        # than fabricate them (OpenAI verbose-JSON has no word layer).
        assert all(s.words == () for s in segs), f"{name} fabricated words"


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_word_timestamps_off_when_not_requested(name: str, factory: Any, clip: str) -> None:
    # word_timestamps=False must suppress the words even on a
    # word-capable backend (RAM-saving default, TranscriptionHints).
    segs = _run(factory(), clip, word_timestamps=False)
    assert all(s.words == () for s in segs), f"{name} emitted words when not requested"


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_language_detection(name: str, factory: Any, clip: str) -> None:
    backend = factory()
    code = asyncio.run(backend.detect_language(clip))
    assert isinstance(code, str) and 2 <= len(code) <= 8


@pytest.mark.parametrize(("name", "factory"), _PARAMS, ids=_IDS)
def test_pause_between_segments(name: str, factory: Any, clip: str) -> None:
    segs = _run(factory(), clip)
    # The fixture's 2nd→3rd boundary is a real 0.5 s silence; a
    # conformant backend must preserve the gap (not stretch a segment
    # across it), so seg[2].start - seg[1].end ≈ 0.5.
    gap = segs[2].start_sec - segs[1].end_sec
    assert gap == pytest.approx(0.5, abs=_EPS), f"{name} swallowed the pause (gap={gap})"
