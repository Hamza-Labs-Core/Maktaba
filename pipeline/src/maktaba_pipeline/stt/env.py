"""This machine's transcription settings and the decode of a file's audio.

Read by the transcription stages (``tv.maktaba.transcription``, Epic 54 wave 3) and by the
pipeline's own code (the cluster's capabilities, the model fetch, the STT backends), so
they live in the pipeline rather than in a stage.
"""

from __future__ import annotations

import asyncio
import os
from typing import TYPE_CHECKING

from ..binaries import ffmpeg_binary
from ..engine.context import StageFailure
from . import engine
from .cohere import DEFAULT_MODEL as REFINE_MODEL
from .settings import SAMPLE_RATE, WhisperSettings

if TYPE_CHECKING:
    import numpy as np

__all__ = ["DECODE_TIMEOUT_S", "decode_audio", "refine_model", "settings_from_env"]

#: how long ffmpeg may take to decode one file's audio
DECODE_TIMEOUT_S = 900.0


def settings_from_env(max_batch: int | None = None) -> WhisperSettings:
    """This machine's settings, each engine reading its own knobs."""
    env = os.environ
    name = engine.current()
    model = engine.model_for(name, env.get("MAKTABA_STT_MODEL"))
    language = env.get("MAKTABA_STT_LANGUAGE", "ar")
    words = env.get("MAKTABA_STT_WORDS", "1") != "0"
    batch = max_batch or int(env.get("MAKTABA_STT_BATCH") or engine.DEFAULT_BATCH[name])
    if name == engine.CT2:
        return WhisperSettings(
            model=model,
            language=language,
            word_timestamps=words,
            max_batch=batch,
            device=env.get("MAKTABA_STT_DEVICE") or "auto",
            compute_type=env.get("MAKTABA_STT_COMPUTE_TYPE") or "auto",
            cpu_threads=int(env.get("MAKTABA_STT_CPU_THREADS") or 0),
        )
    return WhisperSettings(
        model=model,
        language=language,
        word_timestamps=words,
        max_batch=batch,
        gpu_limit_gb=float(env.get("MAKTABA_STT_GPU_GB", "24")),
        gpu_cache_gb=float(env.get("MAKTABA_STT_GPU_CACHE_GB", "2")),
    )


def refine_model() -> str | None:
    """The model of the second listen, or None when it is off (``MAKTABA_STT_REFINE=0``)."""
    if os.environ.get("MAKTABA_STT_REFINE", "1") == "0":
        return None
    return os.environ.get("MAKTABA_STT_REFINE_MODEL", REFINE_MODEL)


async def decode_audio(
    path: str, timeout: float = DECODE_TIMEOUT_S, *, stream: int = 0
) -> np.ndarray:
    """The file's audio stream ``stream`` (``-map 0:a:N``; the first by default) as
    mono float32 at 16 kHz, piped out of ffmpeg into memory (no file written)."""
    import numpy as np

    proc = await asyncio.create_subprocess_exec(
        ffmpeg_binary(), "-nostdin", "-hide_banner", "-loglevel", "error",
        "-i", path, "-map", f"0:a:{stream}", "-vn", "-ac", "1", "-ar", str(SAMPLE_RATE),
        "-f", "s16le", "-",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )  # fmt: skip
    try:
        out, err = await asyncio.wait_for(proc.communicate(), timeout)
    except TimeoutError:
        proc.kill()
        await proc.wait()
        raise
    if proc.returncode != 0:
        msg = err.decode("utf-8", "replace").strip().splitlines()
        tail = msg[-1] if msg else f"ffmpeg exit {proc.returncode}"
        if "matches no streams" in tail or "does not contain any stream" in tail:
            raise StageFailure("no_audio", f"{path}: no audio track", retryable=False)
        raise StageFailure("decode_failed", f"{path}: {tail}", retryable=False)
    return np.frombuffer(out, np.int16).astype(np.float32) / 32768.0
