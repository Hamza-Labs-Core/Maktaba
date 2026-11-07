"""What this machine can do (Epic 31 design section 7), reported at join and each start.

Plain JSON: OS and version, architecture, CPU count, memory, Apple Silicon, MLX-Whisper
importable, NVIDIA GPUs (name, memory, compute capability, driver: NVML, else
``nvidia-smi``, :mod:`..gpu`; with none, why, in ``gpu_problem`` and the log), the Whisper
engine this machine transcribes with (``stt_backend``), ffmpeg path and version
(``MAKTABA_FFMPEG`` first), installed extras, Python and pipeline versions. Every probe
is best effort: a missing piece is reported as absent, never raised.
"""

from __future__ import annotations

import contextlib
import importlib.util
import os
import platform
import shutil
import subprocess
import sys
from typing import Any

from .. import __version__
from ..binaries import ffmpeg_binary
from ..log import get_logger

__all__ = [
    "EXTRAS",
    "capabilities",
    "has_module",
    "is_apple_silicon",
    "memory_bytes",
    "second_listen_sharing",
    "stt_backend",
]

_log = get_logger()

#: extra -> the module whose presence means it is installed (``stt``: :func:`stt_backend`,
#: whose engine differs by machine)
EXTRAS: dict[str, str] = {
    "search": "sentence_transformers",
    "mediaintel": "yt_dlp",
}

#: the Whisper engines (``stt_backend``): MLX on Apple Silicon; CTranslate2 elsewhere, on
#: an NVIDIA GPU when the machine has one and the CUDA wheels, else on the CPU
STT_MLX = "mlx"
STT_CT2_CUDA = "ct2-cuda"
STT_CT2_CPU = "ct2-cpu"


def has_module(name: str) -> bool:
    try:
        return importlib.util.find_spec(name) is not None
    except (ImportError, ValueError):
        return False


def is_apple_silicon() -> bool:
    return platform.system() == "Darwin" and platform.machine() in {"arm64", "aarch64"}


def memory_bytes() -> int | None:
    """Physical memory, or None when it cannot be read."""
    if sys.platform == "win32":
        from ..engine.health import windows_memory

        mem = windows_memory()
        return None if mem is None else mem[0]
    with contextlib.suppress(ValueError, OSError, AttributeError):
        return int(os.sysconf("SC_PAGE_SIZE")) * int(os.sysconf("SC_PHYS_PAGES"))
    return None


def _nvidia() -> tuple[list[dict[str, Any]], str | None]:
    """``(this machine's NVIDIA GPUs, why none)``: NVML, else nvidia-smi (:mod:`..gpu`).
    A machine with none logs why: an empty list alone hid a GPU nvidia-smi could see."""
    from ..gpu import GpuProbe, probe

    found: GpuProbe | None = None
    problem: str | None
    try:
        found = probe()
        problem = found.problem
    except Exception as exc:  # noqa: BLE001 - a probe that breaks reports no GPU, and why
        problem = f"the GPU probe failed: {type(exc).__name__}: {exc}"
    if found is None or not found.gpus:
        _log.warning("capabilities_no_nvidia_gpu", reason=problem)
        return [], problem
    out: list[dict[str, Any]] = []
    for g in found.gpus:
        out.append(
            {
                "name": g.name,
                "memory_gb": round(g.memory_gib, 1),
                "compute_capability": None
                if g.compute is None
                else f"{g.compute[0]}.{g.compute[1]}",
                "driver": g.driver,
                "source": found.source,
            }
        )
    return out, None


def _cuda_wheels() -> bool:
    """The stt extra's CUDA library for CTranslate2 (cuBLAS 12, ``nvidia-cublas-cu12``) is
    installed, in site-packages (``stt/ct2.py`` loads it from there), without loading it."""
    spec = importlib.util.find_spec("nvidia") if has_module("nvidia") else None
    roots = list(spec.submodule_search_locations or []) if spec is not None else []
    return any(os.path.isdir(os.path.join(root, "cublas")) for root in roots)


def stt_backend(gpus: list[dict[str, Any]]) -> str | None:
    """The Whisper engine this machine transcribes with, or None without one installed:
    ``mlx`` on Apple Silicon with MLX-Whisper; elsewhere ``ct2-cuda`` with faster-whisper,
    CTranslate2 and their CUDA wheels on a machine with an NVIDIA GPU, else ``ct2-cpu``.
    Nothing is imported (the probe runs at every start)."""
    if is_apple_silicon():
        return STT_MLX if has_module("mlx_whisper") else None
    if not (has_module("faster_whisper") and has_module("ctranslate2")):
        return None
    return STT_CT2_CUDA if gpus and _cuda_wheels() else STT_CT2_CPU


def second_listen_sharing(backend: str | None) -> str | None:
    """How the second listen shares the GPU with Whisper here (``second_listen_gpu``):
    ``together`` (both loaded; Apple Silicon's unified memory always), ``sequential``
    (they take turns: :mod:`..stt.gpu_turns`, from the GPU's measured free memory), or
    None where it does not run on the GPU. Nothing is loaded to answer."""
    if backend == STT_MLX:
        return "together"
    if backend != STT_CT2_CUDA:
        return None
    try:
        from ..stt import cohere_cuda
        from ..stt.cohere import DEFAULT_MODEL
        from ..stt.env import refine_model, settings_from_env

        settings = settings_from_env()
        if cohere_cuda.problem(settings.device) is not None:
            return None
        return cohere_cuda.mode(refine_model() or DEFAULT_MODEL, settings)
    except Exception:  # noqa: BLE001 - the report goes out without it
        _log.warning("capabilities_second_listen_unknown", exc_info=True)
        return None


def _ffmpeg() -> dict[str, str] | None:
    binary = ffmpeg_binary()
    path = binary if os.path.isabs(binary) and os.path.exists(binary) else shutil.which(binary)
    if path is None:
        return None
    version = ""
    try:
        first = subprocess.run(
            [path, "-hide_banner", "-version"], capture_output=True, text=True, timeout=10
        ).stdout.splitlines()
        if first and first[0].startswith("ffmpeg version "):
            version = first[0].split()[2]
    except (OSError, subprocess.SubprocessError):
        pass
    return {"path": path, "version": version}


def capabilities() -> dict[str, Any]:
    """The capability report; key names are the ones the Machines page reads."""
    mem = memory_bytes()
    gpus, gpu_problem = _nvidia()
    backend = stt_backend(gpus)
    extras = {extra for extra, mod in EXTRAS.items() if has_module(mod)}
    if backend is not None:
        extras.add("stt")
    return {
        "os": platform.system().lower(),
        "os_version": platform.release(),
        "arch": platform.machine().lower(),
        "cpu_count": os.cpu_count() or 1,
        "memory_gb": None if mem is None else round(mem / 1024**3, 1),
        "apple_silicon": is_apple_silicon(),
        "mlx": has_module("mlx_whisper"),
        "gpus": gpus,
        "gpu_problem": gpu_problem,
        "stt_backend": backend,
        "second_listen_gpu": second_listen_sharing(backend),
        "ffmpeg": _ffmpeg(),
        "extras": sorted(extras),
        "python": platform.python_version(),
        "pipeline_version": __version__,
    }
