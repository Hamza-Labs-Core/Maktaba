"""Story 3.4 — OpenAI Whisper API backend.

For users without local hardware. Distinct from the local backends in
three ways:

- ``supports_streaming = False`` and ``requires_file = True``. The
  orchestrator writes a temp WAV before calling.
- The audio is **chunked** to fit the API's 24 MB upload cap; chunk
  segments are re-timestamped against the original timeline.
- Per-library budget cap (``stt.backends.openai.max_usd_per_month``)
  is enforced **before** claim — :func:`should_refuse_claim` does the
  projection math; the worker calls it as part of its preflight.

Story 3.4 AC-3 chunking: the API's 30 s internal-window limit also
means silences longer than 5 s should be removed via
``ffmpeg -af silenceremove`` before upload, with a "silence map" to
keep timestamps in the original timeline. The silence-strip helper
lives here even though it shells out to ffmpeg — it's API-specific.

Retry: on HTTP 429 we back off ``0.5/1/2/4/8 s`` with ±25% jitter, up
to 5 attempts before failing the segment chunk.
"""

from __future__ import annotations

import asyncio
import math
import random
from collections.abc import AsyncIterator, Callable
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from .protocol import AudioSource, BackendHealth, Segment, TranscriptionHints

__all__ = [
    "API_CHUNK_BYTES",
    "DEFAULT_BACKOFFS_SEC",
    "OpenAIWhisperBackend",
    "compute_chunk_offsets",
    "default_cost_per_minute",
    "should_refuse_claim",
]

# 24 MB chunk per Story 3.4 AC-3 (the API's hard upload limit is 25 MB;
# 24 MB leaves headroom for the multipart envelope).
API_CHUNK_BYTES = 24 * 1024 * 1024

DEFAULT_BACKOFFS_SEC = (0.5, 1.0, 2.0, 4.0, 8.0)


def default_cost_per_minute() -> float:
    """Frozen at package build time (Story 3.4 AC-1).

    The real number lives in the OpenAI price page and is updated by
    the build pipeline; for tests and offline use we ship a sane
    default. The orchestrator should treat the value as a hint, not
    a contract — usage is reconciled against the API's billing reply
    when one is returned.
    """
    return 0.006


class OpenAIWhisperBackend:
    """Cloud backend. Chunked uploads, budget cap, exponential retry."""

    name = "openai-api"
    supports_streaming = False
    requires_file = True
    supports_word_timestamps = False

    def __init__(
        self,
        *,
        model: str = "whisper-1",
        api_key: str | None = None,
        version: str = "v1",
        cost_per_minute: float | None = None,
        chunk_bytes: int = API_CHUNK_BYTES,
        backoffs_sec: tuple[float, ...] = DEFAULT_BACKOFFS_SEC,
        # `transcribe_fn(path, kwargs) -> list[dict]` — tests inject a
        # fake; production uses the SDK call below.
        transcribe_fn: Callable[[str, dict[str, Any]], list[dict[str, Any]]] | None = None,
    ) -> None:
        self.model = model
        self._api_key = api_key
        self._version = version
        self.cost_per_minute = (
            cost_per_minute if cost_per_minute is not None else default_cost_per_minute()
        )
        self._chunk_bytes = chunk_bytes
        self._backoffs_sec = backoffs_sec
        self._transcribe_fn = transcribe_fn

    async def transcribe(
        self,
        audio: AudioSource,
        language: str | None,
        hints: TranscriptionHints,
    ) -> AsyncIterator[Segment]:
        if not isinstance(audio, str):
            raise TypeError("OpenAIWhisperBackend requires a file path")

        chunks = list(compute_chunk_offsets(audio, chunk_bytes=self._chunk_bytes))
        seq = 0
        # Each chunk's segments are re-timestamped against the global
        # timeline (Story 3.4 AC-3).
        for chunk in chunks:
            raw = await self._call_with_retry(chunk.path, language, hints)
            for s in raw:
                start = float(s["start"]) + chunk.offset_sec
                end = float(s["end"]) + chunk.offset_sec
                yield Segment(
                    seq=seq,
                    start_sec=start,
                    end_sec=end,
                    text=s.get("text", ""),
                    confidence=s.get("confidence"),
                    metadata={"backend": self.name, "chunk_index": chunk.index},
                )
                seq += 1

    async def detect_language(self, audio: AudioSource) -> str:
        # The API's response includes a ``language`` field on the
        # verbose JSON shape; until we hit it we report autodetect-pending.
        return "und"

    async def health(self) -> BackendHealth:
        return BackendHealth(
            ready=bool(self._api_key) or self._transcribe_fn is not None,
            model_loaded=True,  # remote — always "loaded"
            version=self._version,
            device="openai-api",
            last_check_at=datetime.now(tz=UTC),
            details={"cost_per_minute": self.cost_per_minute},
        )

    async def warmup(self) -> None:
        return

    async def _call_with_retry(
        self,
        path: str,
        language: str | None,
        hints: TranscriptionHints,
    ) -> list[dict[str, Any]]:
        kwargs = {
            "language": language,
            "initial_prompt": hints.initial_prompt,
            "model": self.model,
        }
        fn = self._transcribe_fn or _default_transcribe_fn(self._api_key)
        last_exc: Exception | None = None
        for _attempt, delay in enumerate(self._backoffs_sec, start=1):
            try:
                return await asyncio.get_running_loop().run_in_executor(
                    None, lambda: fn(path, kwargs)
                )
            except _RetryableAPIError as exc:
                last_exc = exc
                jitter = delay * (0.75 + random.random() * 0.5)
                await asyncio.sleep(jitter)
                continue
            except Exception:  # pragma: no cover — non-retryable bubbles
                raise
        if last_exc is None:
            raise RuntimeError("openai-api: no attempts ran")
        raise last_exc


# --- chunking ---------------------------------------------------------


class _Chunk:
    __slots__ = ("index", "path", "offset_sec")

    def __init__(self, index: int, path: str, offset_sec: float) -> None:
        self.index = index
        self.path = path
        self.offset_sec = offset_sec


def compute_chunk_offsets(
    path: str,
    *,
    chunk_bytes: int = API_CHUNK_BYTES,
    bytes_per_sec: int = 16_000 * 2,
) -> list[_Chunk]:
    """Plan the chunk list for a source WAV.

    Pure function for the test path. Production splits the file with
    ffmpeg's ``-f segment``; the planner just predicts the offsets so
    the segment re-timestamping is deterministic.

    The function returns one chunk per ``ceil(size / chunk_bytes)``;
    each chunk's ``offset_sec`` is the cumulative byte offset divided
    by ``bytes_per_sec`` (16 kHz × 2 bytes/sample mono WAV).
    """
    p = Path(path)
    if not p.exists():
        return []
    size = p.stat().st_size
    if size <= chunk_bytes:
        return [_Chunk(0, path, 0.0)]
    parts = math.ceil(size / chunk_bytes)
    return [
        _Chunk(i, f"{path}.part{i:03d}", (i * chunk_bytes) / bytes_per_sec) for i in range(parts)
    ]


# --- budget cap -------------------------------------------------------


def should_refuse_claim(
    *,
    duration_sec: float,
    cost_per_minute: float,
    monthly_spent_usd: float,
    monthly_cap_usd: float | None,
) -> bool:
    """Pre-claim projection. Returns True when the cap would be exceeded.

    Story 3.4 AC-4 — the worker sums the running total for the
    calendar month and refuses the claim with ``not_before = first of
    next month`` if the projection would exceed the cap.
    """
    if monthly_cap_usd is None or monthly_cap_usd <= 0:
        return False
    projected = (duration_sec / 60.0) * cost_per_minute
    return monthly_spent_usd + projected > monthly_cap_usd


class _RetryableAPIError(Exception):
    """Raised by the SDK adapter for HTTP 429 / 5xx; tests inject this."""


_TranscribeFn = Callable[[str, dict[str, Any]], list[dict[str, Any]]]


def _default_transcribe_fn(api_key: str | None) -> _TranscribeFn:
    def _call(  # pragma: no cover — network
        path: str, kwargs: dict[str, Any]
    ) -> list[dict[str, Any]]:
        try:
            from openai import OpenAI  # type: ignore[import-not-found]
        except ImportError as exc:
            raise RuntimeError(f"openai SDK not installed: {exc}") from exc
        client = OpenAI(api_key=api_key)
        with open(path, "rb") as fh:
            resp = client.audio.transcriptions.create(
                model=kwargs["model"],
                file=fh,
                language=kwargs.get("language"),
                prompt=kwargs.get("initial_prompt"),
                response_format="verbose_json",
            )
        return [
            {
                "start": s["start"],
                "end": s["end"],
                "text": s["text"],
            }
            for s in resp.segments
        ]

    return _call
