"""Real media made with ``ffmpeg -f lavfi`` into a test's tmp_path (Epic 47 tests).

No library file is ever used. Every maker returns the path it wrote.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest

FFMPEG = shutil.which("ffmpeg")
FFPROBE = shutil.which("ffprobe")

needs_ffmpeg = pytest.mark.skipif(
    FFMPEG is None or FFPROBE is None, reason="ffmpeg and ffprobe are not on PATH"
)

_SRC = [
    "-f",
    "lavfi",
    "-i",
    "testsrc2=size=320x240:rate=25",
    "-f",
    "lavfi",
    "-i",
    "sine=frequency=440:sample_rate=48000",
]


def _ff(*args: str) -> None:
    assert FFMPEG is not None
    subprocess.run(
        [FFMPEG, "-hide_banner", "-v", "error", "-y", *args],
        check=True,
        capture_output=True,
        timeout=120,
    )


def mkv_h264_aac(path: Path, seconds: float = 4.0) -> Path:
    """An H.264 + AAC MKV (the remux case)."""
    _ff(
        *_SRC,
        "-t",
        str(seconds),
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-g",
        "25",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        "-b:a",
        "96k",
        str(path),
    )
    return path


def mp4_faststart(path: Path, seconds: float = 4.0) -> Path:
    """A good H.264 + AAC MP4 with the moov atom first."""
    _ff(
        *_SRC,
        "-t",
        str(seconds),
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-g",
        "25",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        "-b:a",
        "96k",
        "-movflags",
        "+faststart",
        str(path),
    )
    return path


def mp4_moov_at_end(path: Path, seconds: float = 4.0) -> Path:
    """An MP4 whose moov atom is after mdat (ffmpeg's default)."""
    _ff(
        *_SRC,
        "-t",
        str(seconds),
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        str(path),
    )
    return path


def remux_to_mp4(src: Path, dest: Path) -> Path:
    """``src`` remuxed (``-c copy``) to an MP4 with faststart."""
    _ff("-i", str(src), "-map", "0", "-c", "copy", "-movflags", "+faststart", str(dest))
    return dest


def reencode_to_mp4(src: Path, dest: Path) -> Path:
    """``src`` re-encoded to H.264 + AAC MP4 (the lossy path)."""
    _ff(
        "-i",
        str(src),
        "-c:v",
        "libx264",
        "-preset",
        "ultrafast",
        "-crf",
        "28",
        "-pix_fmt",
        "yuv420p",
        "-c:a",
        "aac",
        "-movflags",
        "+faststart",
        str(dest),
    )
    return dest


def truncated(src: Path, dest: Path, keep: float = 0.6) -> Path:
    """The first ``keep`` of ``src``'s bytes (an unfinished download)."""
    data = src.read_bytes()
    dest.write_bytes(data[: int(len(data) * keep)])
    return dest


def corrupt_middle(src: Path, dest: Path) -> Path:
    """``src`` with a run of bytes in the middle of its media data overwritten."""
    data = bytearray(src.read_bytes())
    start = len(data) // 2
    for i in range(start, min(len(data), start + 4096)):
        data[i] = (data[i] * 7 + 0x5A) & 0xFF
    dest.write_bytes(bytes(data))
    return dest
