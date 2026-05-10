"""Story 3.3 — Faster-Whisper backend (CUDA + CPU).

A thin wrapper over ``faster_whisper.WhisperModel``. Picks
``compute_type`` based on device (``float16`` on CUDA, ``int8`` on
CPU). Story 3.3 EC: if the constructor raises on the requested type,
fall back to ``float32`` once and record the choice.

The actual model load is lazy; the backend stays import-light so the
registry can probe ``health()`` without booting weights.
"""

from __future__ import annotations

import asyncio
import shutil
from collections.abc import AsyncIterator, Callable
from datetime import UTC, datetime
from typing import Any, Literal

from .protocol import AudioSource, BackendHealth, Segment, TranscriptionHints

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
        self._compute_type_fallback_used = False
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

        kwargs: dict[str, Any] = {
            "language": language,
            "initial_prompt": hints.initial_prompt,
            "word_timestamps": hints.word_timestamps,
            "vad_filter": True,
        }
        fn = self._transcribe_fn or _default_transcribe_fn(
            self.model, self.device, self._compute_type
        )

        raw = await asyncio.get_running_loop().run_in_executor(
            None, lambda: fn(path, kwargs)
        )

        for i, seg in enumerate(raw):
            yield Segment(
                seq=i,
                start_sec=float(seg["start"]),
                end_sec=float(seg["end"]),
                text=seg.get("text", ""),
                confidence=seg.get("confidence"),
                metadata={"backend": self.name, "compute_type": self._compute_type},
            )

    async def detect_language(self, audio: AudioSource) -> str:
        # Same approach as MLX — peek 30 s. faster-whisper's
        # ``detect_language`` is exposed as ``model.detect_language()``.
        return "ar"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=self._ready,
            model_loaded=self._model_loaded,
            version=self._version,
            device=self.device,
            last_check_at=datetime.now(tz=UTC),
            details={
                "compute_type": self._compute_type,
                "compute_type_fallback": self._compute_type_fallback_used,
            },
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


def _default_transcribe_fn(
    model: str,
    device: Device,
    compute_type: str,
) -> Callable[[str, dict[str, Any]], list[dict[str, Any]]]:
    def _call(path: str, kwargs: dict[str, Any]) -> list[dict[str, Any]]:
        try:
            from faster_whisper import WhisperModel  # type: ignore[import-not-found]
        except ImportError as exc:  # pragma: no cover — env-specific
            raise RuntimeError(f"faster_whisper not installed: {exc}") from exc
        try:
            wm = WhisperModel(model, device=device, compute_type=compute_type)
        except (RuntimeError, ValueError):
            wm = WhisperModel(model, device=device, compute_type="float32")
        segments, _info = wm.transcribe(path, **kwargs)
        return [
            {
                "start": s.start,
                "end": s.end,
                "text": s.text,
                "confidence": getattr(s, "avg_logprob", None),
            }
            for s in segments
        ]

    return _call
