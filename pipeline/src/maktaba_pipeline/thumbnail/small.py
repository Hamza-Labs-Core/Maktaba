"""A video's small copy: where it lives and the ffmpeg line that makes it.

The ``thumb_small`` stage (``tv.maktaba.thumbnails``, Epic 54 wave 2) writes it; the convert,
the review pages and the mediaintel import read these paths too, so they live here.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Any

from ..engine.media import ffmpeg
from .generator import thumbnail_dir_for

__all__ = ["ROOT", "SMALL_NAME", "WIDTH", "small_args", "small_path", "staged_path"]

WIDTH = 320
SMALL_NAME = "small.jpg"

#: the cache root (``None``: ``~/.maktaba/cache/thumbnails``); tests point it elsewhere
ROOT: str | None = os.environ.get("MAKTABA_THUMBNAIL_ROOT") or None


def small_path(content_hash: str) -> Path:
    """Where a video's small copy lives (exists or not)."""
    return thumbnail_dir_for(content_hash, root=ROOT) / SMALL_NAME


def staged_path(video_id: Any) -> Path:
    """Where the mediaintel import leaves a video's small copy while its real hash is
    still pending (the final folder is named after that hash); the stage moves it in."""
    return thumbnail_dir_for("imported", root=ROOT) / f"{video_id}.jpg"


def small_args(src: str, *, binary: str | None = None) -> list[str]:
    """One frame of ``src`` scaled to ``WIDTH`` px, JPEG on stdout (``binary``: the ffmpeg;
    default the current one)."""
    return [
        binary or ffmpeg(), "-v", "error", "-nostdin", "-i", src,
        "-vf", f"scale={WIDTH}:-2", "-frames:v", "1", "-q:v", "5",
        "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
    ]  # fmt: skip
