"""Which ffmpeg / ffprobe this worker runs (Epic 31 design section 7).

``MAKTABA_FFMPEG`` / ``MAKTABA_FFPROBE`` name the binaries (a full path on a machine
whose ffmpeg is not on PATH, as is common on Windows); otherwise the installed
``tool.ffmpeg`` pack's (Epic 51: ``$MAKTABA_COMPONENTS_DIR/tool.ffmpeg/<current>/ffmpeg/``,
the folder the supervisor passes); otherwise the names on PATH. Read at call time, so an
environment set after import (a node's, a test's), or a pack installed while the worker
runs, still applies.
"""

from __future__ import annotations

import os
import sys
from pathlib import Path

__all__ = ["ffmpeg_binary", "ffprobe_binary"]


def _pack_program(name: str) -> str | None:
    """``name`` in the installed tool.ffmpeg pack, or None (no components folder, no
    current pointer, a pointer naming no folder, or the program not there)."""
    root = (os.environ.get("MAKTABA_COMPONENTS_DIR") or "").strip()
    if not root:
        return None
    pack = Path(root) / "tool.ffmpeg"
    try:
        cur = (pack / "current").read_text(encoding="utf-8").strip()
    except OSError:
        return None
    if not cur or "/" in cur or "\\" in cur or cur.startswith("."):
        return None
    exe = pack / cur / "ffmpeg" / (name + ".exe" if sys.platform == "win32" else name)
    return str(exe) if exe.is_file() and os.access(exe, os.X_OK) else None


def ffmpeg_binary(explicit: str | None = None) -> str:
    """``explicit`` when given, else ``$MAKTABA_FFMPEG``, else the pack's, else ``ffmpeg``."""
    return explicit or os.environ.get("MAKTABA_FFMPEG") or _pack_program("ffmpeg") or "ffmpeg"


def ffprobe_binary(explicit: str | None = None) -> str:
    """``explicit`` when given, else ``$MAKTABA_FFPROBE``, else the pack's, else ``ffprobe``."""
    return explicit or os.environ.get("MAKTABA_FFPROBE") or _pack_program("ffprobe") or "ffprobe"
