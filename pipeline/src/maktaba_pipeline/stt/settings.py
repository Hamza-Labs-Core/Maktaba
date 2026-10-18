"""What a transcription is asked for and what it returns (plan 03-10).

Kept apart from :mod:`maktaba_pipeline.stt.batched` so that workers without the ``stt``
extra (no NumPy, no MLX) can still import the transcribe stage, compute its input
fingerprint and enqueue it; only a worker that runs it needs the GPU code.
"""

from __future__ import annotations

from dataclasses import dataclass

__all__ = ["SAMPLE_RATE", "Sentence", "Transcript", "WhisperSettings"]

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
