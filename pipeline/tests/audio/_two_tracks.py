"""A real file with two audio tracks, made with ``ffmpeg -f lavfi`` in a test's tmp_path.

Stream ``0:a:0`` is English and the file's default, a 440 Hz tone; ``0:a:1`` is
Arabic and not the default, a 1000 Hz tone. A decoder left to pick its own stream
reads the English one; :func:`dominant_hz` says which tone a decoder was handed.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path
from typing import Any

import pytest

FFMPEG = shutil.which("ffmpeg")

needs_ffmpeg = pytest.mark.skipif(FFMPEG is None, reason="ffmpeg is not on PATH")

ENGLISH_HZ = 440.0
ARABIC_HZ = 1000.0


def two_track_file(path: Path, seconds: float = 6.0) -> Path:
    """Write the English (default, 0:a:0) + Arabic (0:a:1) file to ``path``."""
    assert FFMPEG is not None
    tone = "sine=frequency={hz:.0f}:sample_rate=48000:duration={s}"
    subprocess.run(
        [
            FFMPEG, "-hide_banner", "-v", "error", "-y",
            "-f", "lavfi", "-i", tone.format(hz=ENGLISH_HZ, s=seconds),
            "-f", "lavfi", "-i", tone.format(hz=ARABIC_HZ, s=seconds),
            "-map", "0:a", "-map", "1:a", "-c:a", "aac", "-b:a", "64k",
            "-metadata:s:a:0", "language=eng", "-metadata:s:a:1", "language=ara",
            "-disposition:a:0", "default", "-disposition:a:1", "0",
            str(path),
        ],
        check=True,
        capture_output=True,
        timeout=120,
    )  # fmt: skip
    return path


def dominant_hz(samples: Any, sample_rate: int = 16_000) -> float:
    """The loudest frequency in mono float samples."""
    import numpy as np  # the stt extra's: imported by the tests that decode

    x = np.asarray(samples, dtype=np.float32)
    spectrum = np.abs(np.fft.rfft(x))
    freqs = np.fft.rfftfreq(x.size, 1.0 / sample_rate)
    return float(freqs[int(np.argmax(spectrum[1:])) + 1])
