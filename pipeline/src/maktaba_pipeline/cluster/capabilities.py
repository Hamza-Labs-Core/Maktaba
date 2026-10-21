"""What this machine can do (Epic 31 design section 7), reported at join and each start.

Plain JSON: OS and version, architecture, CPU count, memory, Apple Silicon, MLX-Whisper
importable, NVIDIA GPUs (when ``pynvml`` is installed), ffmpeg path and version
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

__all__ = ["EXTRAS", "capabilities", "has_module", "is_apple_silicon", "memory_bytes"]

#: extra -> the module whose presence means it is installed
EXTRAS: dict[str, str] = {
    "stt": "mlx_whisper",
    "search": "sentence_transformers",
    "mediaintel": "yt_dlp",
}


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


def _nvidia() -> list[dict[str, Any]]:
    if not has_module("pynvml"):
        return []
    try:
        import pynvml  # type: ignore[import-not-found,unused-ignore]  # optional

        pynvml.nvmlInit()
        try:
            out: list[dict[str, Any]] = []
            for i in range(pynvml.nvmlDeviceGetCount()):
                name = pynvml.nvmlDeviceGetName(pynvml.nvmlDeviceGetHandleByIndex(i))
                out.append({"name": name.decode() if isinstance(name, bytes) else str(name)})
            return out
        finally:
            pynvml.nvmlShutdown()
    except Exception:  # noqa: BLE001 - no driver, no GPU: nothing to report
        return []


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
    return {
        "os": platform.system().lower(),
        "os_version": platform.release(),
        "arch": platform.machine().lower(),
        "cpu_count": os.cpu_count() or 1,
        "memory_gb": None if mem is None else round(mem / 1024**3, 1),
        "apple_silicon": is_apple_silicon(),
        "mlx": has_module("mlx_whisper"),
        "gpus": _nvidia(),
        "ffmpeg": _ffmpeg(),
        "extras": sorted(extra for extra, mod in EXTRAS.items() if has_module(mod)),
        "python": platform.python_version(),
        "pipeline_version": __version__,
    }
