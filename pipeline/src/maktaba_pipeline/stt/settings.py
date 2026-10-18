"""What a transcription is asked for and what it returns (plan 03-10).

Kept apart from :mod:`maktaba_pipeline.stt.batched` so that workers without the ``stt``
extra (no NumPy, no MLX) can still import the transcribe stage, compute its input
fingerprint and enqueue it; only a worker that runs it needs the GPU code.
"""

from __future__ import annotations

import threading
from dataclasses import dataclass, field
from typing import Any

__all__ = ["SAMPLE_RATE", "Checkpoint", "FileProgress", "Sentence", "Transcript", "WhisperSettings"]

SAMPLE_RATE = 16_000


@dataclass(frozen=True, slots=True)
class WhisperSettings:
    """Whisper's knobs, as ``mlx_whisper.transcribe`` names them."""

    model: str = "mlx-community/whisper-large-v3-turbo"
    language: str = "ar"
    max_batch: int = 32
    word_timestamps: bool = True
    #: MLX's GPU memory: a limit for the arrays, and a cap on its cache of freed
    #: buffers (unbounded by default: every pass allocates differently sized buffers,
    #: and the cache grew to 70+ GB in an hour)
    gpu_limit_gb: float = 24.0
    gpu_cache_gb: float = 2.0
    temperatures: tuple[float, ...] = (0.0, 0.2, 0.4, 0.6, 0.8, 1.0)
    compression_ratio_threshold: float = 2.4
    logprob_threshold: float = -1.0
    no_speech_threshold: float = 0.6


@dataclass(slots=True)
class Sentence:
    start: float
    end: float
    text: str
    avg_logprob: float
    no_speech_prob: float
    words: list[tuple[float, float, str]] | None = None


@dataclass(slots=True)
class Transcript:
    sentences: list[Sentence]
    audio_sec: float
    language: str  # the language it was decoded as (forced: WhisperSettings.language)
    model: str
    gpu_sec: float  # wall time from admission to the last window
    #: the language Whisper hears in the opening 30 s, when it is sure enough (None:
    #: not detected, or not confident); recorded as such, never the forced one
    detected_language: str | None = None


@dataclass(slots=True)
class Checkpoint:
    """How far a file's transcription got: its seek (mel frames, 100 a second), the end
    of its last speech, its detected language and its sentences so far (Whisper segment
    dicts: start, end, text, avg_logprob, no_speech_prob, words). Resuming from it gives
    the same transcript as an uninterrupted run (windows are decoded independently)."""

    seek: int
    last_speech: float = 0.0
    language: str | None = None
    segments: list[dict[str, Any]] = field(default_factory=list)


class FileProgress:
    """A file's progress as the GPU thread publishes it after each pass, read from the
    event loop (the transcribe stage saves it as a :class:`Checkpoint`)."""

    def __init__(self, saved_seek: int = 0) -> None:
        self._lock = threading.Lock()
        self._seek = 0
        self._last_speech = 0.0
        self._language: str | None = None
        self._segments: list[dict[str, Any]] = []
        self._committed = 0
        #: the seek the caller has made durable: not lost if the worker stops now
        self.saved_seek = saved_seek

    def publish(
        self, seek: int, last_speech: float, language: str | None, segments: list[dict[str, Any]]
    ) -> None:
        with self._lock:
            self._seek, self._last_speech, self._language = seek, last_speech, language
            self._segments, self._committed = segments, len(segments)

    @property
    def seek(self) -> int:
        with self._lock:
            return self._seek

    def snapshot(self) -> Checkpoint:
        with self._lock:
            done = self._segments[: self._committed]  # later segments may be appended
            return Checkpoint(
                seek=self._seek,
                last_speech=self._last_speech,
                language=self._language,
                segments=[_plain(s) for s in done],
            )


def _plain(seg: dict[str, Any]) -> dict[str, Any]:
    """The fields a sentence is built from, as JSON (no NumPy numbers, no tokens)."""
    return {
        "start": float(seg["start"]),
        "end": float(seg["end"]),
        "text": str(seg["text"]),
        "avg_logprob": float(seg["avg_logprob"]),
        "no_speech_prob": float(seg["no_speech_prob"]),
        "words": [
            {"start": float(w["start"]), "end": float(w["end"]), "word": str(w["word"])}
            for w in seg.get("words") or []
        ],
    }
