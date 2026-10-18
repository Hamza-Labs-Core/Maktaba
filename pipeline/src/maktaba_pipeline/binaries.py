"""Which ffmpeg / ffprobe this worker runs (Epic 31 design section 7).

``MAKTABA_FFMPEG`` / ``MAKTABA_FFPROBE`` name the binaries (a full path on a machine
whose ffmpeg is not on PATH, as is common on Windows); otherwise the names on PATH. Read
at call time, so an environment set after import (a node's, a test's) still applies.
"""

from __future__ import annotations

import os

__all__ = ["ffmpeg_binary", "ffprobe_binary"]


def ffmpeg_binary(explicit: str | None = None) -> str:
    """``explicit`` when given, else ``$MAKTABA_FFMPEG``, else ``ffmpeg``."""
    return explicit or os.environ.get("MAKTABA_FFMPEG") or "ffmpeg"


def ffprobe_binary(explicit: str | None = None) -> str:
    """``explicit`` when given, else ``$MAKTABA_FFPROBE``, else ``ffprobe``."""
    return explicit or os.environ.get("MAKTABA_FFPROBE") or "ffprobe"
