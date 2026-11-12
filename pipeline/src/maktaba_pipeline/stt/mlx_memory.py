"""MLX's GPU memory in a transcription process: its limits, and models let go when idle.

Whisper (:class:`~.batched.BatchedTranscriber`) and the second listen
(:class:`~.cohere.CohereRecogniser`) share one MLX allocator per process (an ml runner holds
both). MLX keeps freed buffers in a cache for reuse, and by default that cache may grow to
the memory limit (about 1.5x the GPU's recommended working set: more than 100 GB on a 96 GB
Mac), so the limits are set by whichever model loads first, not only by Whisper's.

An ml runner keeps its models between jobs. Once it has had no transcription work for
:attr:`~.settings.WhisperSettings.unload_idle_s` (``MAKTABA_STT_UNLOAD_IDLE_S``, default 600;
0 keeps them), each model is let go and its memory given back; the next job loads it again
(a few seconds, from the disk cache).
"""

from __future__ import annotations

import gc
import os
from typing import Any

from ..log import get_logger
from .settings import WhisperSettings

__all__ = ["apply_limits", "release", "trim", "unload_idle_s"]

_log = get_logger()


def unload_idle_s(settings: WhisperSettings | None) -> float:
    """Seconds without work after which a model is let go (0: never)."""
    if settings is not None:
        return max(0.0, settings.unload_idle_s)
    try:
        return max(0.0, float(os.environ.get("MAKTABA_STT_UNLOAD_IDLE_S", "600") or 0))
    except ValueError:
        return 600.0


def _cache_bytes(settings: WhisperSettings | None) -> int:
    if settings is not None:
        return int(settings.gpu_cache_gb * 1e9)
    try:
        return int(float(os.environ.get("MAKTABA_STT_GPU_CACHE_GB", "2")) * 1e9)
    except ValueError:
        return int(WhisperSettings().gpu_cache_gb * 1e9)


def apply_limits(mx: Any, settings: WhisperSettings | None) -> None:
    """MLX's array limit and the cap on its cache of freed buffers, for the whole process
    (from Whisper's settings, else this machine's ``MAKTABA_STT_GPU_*`` knobs)."""
    if settings is not None:
        limit = int(settings.gpu_limit_gb * 1e9)
    else:
        try:
            limit = int(float(os.environ.get("MAKTABA_STT_GPU_GB", "24")) * 1e9)
        except ValueError:
            limit = int(WhisperSettings().gpu_limit_gb * 1e9)
    mx.set_memory_limit(limit)
    mx.set_cache_limit(_cache_bytes(settings))


def trim(mx: Any, settings: WhisperSettings | None) -> None:
    """Empty MLX's cache of freed buffers once it is past its cap."""
    if mx is not None and mx.get_cache_memory() > _cache_bytes(settings):
        mx.clear_cache()


def release(mx: Any, what: str, idle_s: float, **fields: Any) -> None:
    """After a model's references are dropped: collect them and give MLX's freed buffers
    back to the system, and say so (``stt.model_unloaded``)."""
    gc.collect()
    if mx is not None:
        mx.clear_cache()
    _log.info(
        "stt.model_unloaded",
        model=what,
        idle_s=round(idle_s),
        **({"gpu_active_gb": round(mx.get_active_memory() / 1e9, 2)} if mx is not None else {}),
        **fields,
    )
