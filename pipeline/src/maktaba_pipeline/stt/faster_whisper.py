"""Story 3.3 — Faster-Whisper backend (CUDA + CPU), on the CTranslate2 engine.

The registry's faster-whisper backend (the handlers' TRANSCRIBE path, the gRPC
``STTTest``). It runs the engine the transcribe stage runs on Windows and Linux
(story 38.8, :mod:`.ct2`: batched Whisper on CTranslate2), one file per call: the same
model files, device choice and word timings, not a second way to load faster-whisper.
``compute_type`` follows the device (``float16`` on CUDA, ``int8`` on CPU) unless
given; one the device cannot run falls back to its default, with a line in the log
(:func:`.ct2.choose_compute_type`).

The model loads on the first file, on the engine's thread; the backend stays
import-light so the registry can probe ``health()`` without booting weights.
"""

from __future__ import annotations

import asyncio
import dataclasses
import math
import shutil
from collections.abc import AsyncIterator, Callable
from datetime import UTC, datetime
from typing import TYPE_CHECKING, Any, Literal

from .protocol import AudioSource, BackendHealth, Segment, TranscriptionHints, parse_words

if TYPE_CHECKING:
    from .settings import Transcript

__all__ = ["FasterWhisperBackend"]


Device = Literal["cuda", "cpu"]


class FasterWhisperBackend:
    """Both CUDA and CPU variants. Pick via constructor ``device``."""

    supports_streaming = True
    requires_file = False
    cost_per_minute: float | None = 0.0
    supports_word_timestamps = True

    def __init__(
        self,
        *,
        device: Device = "cpu",
        model: str = "large-v3",
        version: str = "0.0.0",
        compute_type: str | None = None,
        transcribe_fn: Callable[[str, dict[str, Any]], list[dict[str, Any]]] | None = None,
        force_ready: bool | None = None,
    ) -> None:
        self.device: Device = device
        self.name = "whisper-cuda" if device == "cuda" else "whisper-cpu"
        self.model = model
        self._version = version
        self._compute_type = compute_type or ("float16" if device == "cuda" else "int8")
        #: a stand-in for the engine (tests): (path, kwargs) -> segment dicts
        self._transcribe_fn = transcribe_fn
        if force_ready is not None:
            self._ready = force_ready
        else:
            self._ready = _device_available(device)
        self._model_loaded = False

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        if not self._ready:
            raise RuntimeError(f"{self.name} is not ready on this host")
        path = audio if isinstance(audio, str) else await _materialise_to_path(audio)
        await self.warmup()
        meta = {"backend": self.name, "compute_type": self._compute_type}

        if self._transcribe_fn is not None:
            fn = self._transcribe_fn
            kwargs: dict[str, Any] = {
                "language": language,
                "initial_prompt": hints.initial_prompt,
                "word_timestamps": hints.word_timestamps,
            }
            raw = await asyncio.get_running_loop().run_in_executor(None, lambda: fn(path, kwargs))
            for i, seg in enumerate(raw):
                yield Segment(
                    seq=i,
                    start_sec=float(seg["start"]),
                    end_sec=float(seg["end"]),
                    text=seg.get("text", ""),
                    confidence=seg.get("confidence"),
                    # Story 3.6-1.3 — per-word timings when asked for, so
                    # commit_segment persists transcript_words
                    words=parse_words(seg.get("words")) if hints.word_timestamps else (),
                    metadata=meta,
                )
            return

        result = await self._on_engine(path, language, hints)
        for i, s in enumerate(result.sentences):
            words = [{"word": w, "start": a, "end": b} for a, b, w in s.words or []]
            yield Segment(
                seq=i,
                start_sec=s.start,
                end_sec=s.end,
                text=s.text,
                confidence=round(math.exp(s.avg_logprob), 4),
                words=parse_words(words) if hints.word_timestamps else (),
                metadata={**meta, "engine": result.backend_version or result.backend},
            )

    async def _on_engine(
        self, path: str, language: str | None, hints: TranscriptionHints
    ) -> Transcript:
        """The file through the stage's engine (:mod:`.ct2`), with this backend's model,
        device and precision over the machine's other settings."""
        from ..engine.stages.transcribe import decode_audio, settings_from_env
        from . import engine
        from .ct2 import get_transcriber

        base = settings_from_env()
        settings = dataclasses.replace(
            base,
            model=engine.model_for(engine.CT2, self.model),
            language=language or base.language,
            word_timestamps=hints.word_timestamps,
            device=self.device,
            compute_type=self._compute_type,
        )
        samples = await decode_audio(path)
        return await get_transcriber(settings).transcribe(samples, prompt=hints.initial_prompt)

    async def detect_language(self, audio: AudioSource) -> str:
        # The engine records what Whisper hears in a file's first window
        # (Transcript.detected_language); this protocol's caller forces Arabic.
        return "ar"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=self._ready,
            model_loaded=self._model_loaded,
            version=self._version,
            device=self.device,
            last_check_at=datetime.now(tz=UTC),
            details={"compute_type": self._compute_type},
        )

    async def warmup(self) -> None:
        if self._model_loaded:
            return
        self._model_loaded = True


# --- helpers ----------------------------------------------------------


def _device_available(device: Device) -> bool:
    if device == "cuda":
        return shutil.which("nvidia-smi") is not None
    return True


async def _materialise_to_path(_iter: AsyncIterator[bytes]) -> str:
    raise NotImplementedError(
        "FasterWhisperBackend currently expects a file path; the orchestrator's "
        "streaming wrapper materialises one before calling."
    )
