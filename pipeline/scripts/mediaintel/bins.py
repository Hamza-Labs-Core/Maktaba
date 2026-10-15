"""Where ffmpeg / ffprobe are: ``MEDIAINTEL_FFMPEG`` / ``MEDIAINTEL_FFPROBE`` when set,
else whatever ``PATH`` finds (``ffmpeg.exe`` on Windows, ``ffmpeg`` elsewhere)."""

from __future__ import annotations

import os
import shutil

FFMPEG: str = os.environ.get("MEDIAINTEL_FFMPEG") or shutil.which("ffmpeg") or "ffmpeg"
FFPROBE: str = os.environ.get("MEDIAINTEL_FFPROBE") or shutil.which("ffprobe") or "ffprobe"
