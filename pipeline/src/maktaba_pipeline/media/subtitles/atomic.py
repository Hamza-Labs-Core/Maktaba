"""Atomic write of the SRT/VTT pair.

Both files for a given (video, language) move into place together:
either both new artefacts replace any previous version, or neither
does. We achieve that by writing to ``.maktaba/.tmp/`` first and then
``os.replace``-ing each in turn — replace is atomic on POSIX and on
modern Windows (NTFS).

If the *second* replace fails after the first succeeded the directory
is left in a partial state. We log and re-raise; the next pipeline
run will regenerate both files into a consistent state.
"""

from __future__ import annotations

import contextlib
import os
import uuid
from pathlib import Path

__all__ = ["write_atomic_pair"]


def write_atomic_pair(
    srt_path: Path,
    srt_bytes: bytes,
    vtt_path: Path,
    vtt_bytes: bytes,
    *,
    tmp_dir: Path,
) -> None:
    """Write SRT then VTT atomically into the destination directory.

    Stages both files under ``tmp_dir`` with random names, then
    promotes them in order. The tmp_dir is expected to be on the
    same filesystem as the destination — this is what makes
    ``os.replace`` atomic. If the caller hands us a tmp_dir on a
    different FS, ``os.replace`` will raise ``EXDEV`` and we'll
    abort cleanly.
    """
    token = uuid.uuid4().hex
    tmp_srt = tmp_dir / f"{token}.srt"
    tmp_vtt = tmp_dir / f"{token}.vtt"

    # Stage both files. If either write fails, unlink any partial
    # tempfile and propagate.
    try:
        tmp_srt.write_bytes(srt_bytes)
        tmp_vtt.write_bytes(vtt_bytes)
    except OSError:
        for p in (tmp_srt, tmp_vtt):
            with contextlib.suppress(FileNotFoundError, OSError):
                p.unlink()
        raise

    # Promote. The first replace is the commit point — once that
    # succeeds we MUST finish or surface the partial state.
    try:
        os.replace(tmp_srt, srt_path)
    except OSError:
        with contextlib.suppress(FileNotFoundError, OSError):
            tmp_srt.unlink()
        with contextlib.suppress(FileNotFoundError, OSError):
            tmp_vtt.unlink()
        raise

    try:
        os.replace(tmp_vtt, vtt_path)
    except OSError:
        # SRT already landed; VTT did not. Leave a partial state and
        # propagate — the caller logs and the next run will heal.
        with contextlib.suppress(FileNotFoundError, OSError):
            tmp_vtt.unlink()
        raise
