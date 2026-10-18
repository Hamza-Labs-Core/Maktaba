"""Audio fingerprints of a video's opening and closing 45 s (theme song, intro, end nasheed).

Episodes of one show play the same intro and closing music even when the picture
changes, so the strongest "same show" evidence is the sound. ffmpeg (built with
``--enable-chromaprint``) fingerprints a window of audio; two fingerprints are
compared by bit error rate, sliding one against the other so music that starts a
few seconds earlier or later still lines up.

Measured on one channel: episodes of the same season score 0.81–0.91, different
series 0.53–0.54 (0.5 is chance). YouTube uploads sometimes cut the intro or the
end song, so a match is evidence and a non-match is *not*: callers must treat a
low score as "unknown", never as "different show".

:func:`ffmpeg_chromaprint_args` builds the command, :func:`parse_raw` /
:func:`encode` / :func:`decode` handle the bytes, :func:`similarity` compares.
"""

from __future__ import annotations

import base64
import struct
from collections.abc import Sequence

from ..binaries import ffmpeg_binary

__all__ = [
    "AUDIO_FP_VERSION",
    "HEAD_S",
    "MATCH",
    "TAIL_S",
    "decode",
    "encode",
    "ffmpeg_chromaprint_args",
    "matches",
    "parse_raw",
    "similarity",
    "windows",
]

AUDIO_FP_VERSION: int = 1
HEAD_S: float = 45.0  # opening window (cold open + intro); intros fall well inside it
TAIL_S: float = 45.0  # closing window (end song + credits)
#: at or above this a pair of windows is the same music (same-series pairs measured
#: 0.81–0.91, different series 0.53–0.54)
MATCH: float = 0.70
_ITEM_S = 0.1238  # one chromaprint item ≈ 4096 samples at 11025 Hz / 3


def windows(duration_s: float | None) -> list[tuple[str, float, float]]:
    """``(which, start, length)`` for the head and, when the video is long enough to
    have a separate ending, the tail."""
    out = [("head", 0.0, HEAD_S)]
    if duration_s and duration_s > HEAD_S + TAIL_S + 30:
        out.append(("tail", max(0.0, duration_s - TAIL_S), TAIL_S))
    return out


def ffmpeg_chromaprint_args(
    path: str, start: float, length: float, *, binary: str | None = None
) -> list[str]:
    """ffmpeg argv writing the raw (uint32 little-endian) fingerprint to stdout."""
    return [
        ffmpeg_binary(binary), "-v", "error", "-nostdin",
        "-ss", f"{start:.2f}", "-t", f"{length:.2f}",
        "-i", path, "-vn", "-ac", "1", "-f", "chromaprint", "-fp_format", "raw", "pipe:1",
    ]  # fmt: skip


def parse_raw(raw: bytes) -> list[int]:
    n = len(raw) // 4
    return list(struct.unpack(f"<{n}I", raw[: n * 4]))


def encode(fp: Sequence[int]) -> str:
    return base64.b64encode(struct.pack(f"<{len(fp)}I", *fp)).decode("ascii")


def decode(s: str | None) -> list[int]:
    return parse_raw(base64.b64decode(s)) if s else []


def similarity(
    a: Sequence[int], b: Sequence[int], *, max_shift_s: float = 12.0, step: int = 2
) -> float:
    """Best ``1 − bit error rate`` over alignments of ``a`` against ``b`` (shifted up to
    ``max_shift_s`` either way). ``step`` thins items and shifts for speed (music is
    smooth at ~8 items per second). Below 3 s of overlap the result is 0."""
    if not a or not b:
        return 0.0
    max_shift = int(max_shift_s / _ITEM_S)
    min_pairs = int(3.0 / _ITEM_S / step)
    best = 0.0
    for s in range(-max_shift, max_shift + 1, step):
        lo, hi = max(0, -s), min(len(a), len(b) - s)
        if hi - lo < min_pairs * step:
            continue
        bits = 0
        n = 0
        for i in range(lo, hi, step):
            bits += (a[i] ^ b[i + s]).bit_count()
            n += 1
        best = max(best, 1.0 - bits / (32 * n))
    return best


def matches(a: Sequence[int], b: Sequence[int], *, threshold: float = MATCH, step: int = 4) -> bool:
    """``similarity(a, b, step=step) >= threshold``, cheaper on the usual "no": a coarse
    pass reads every 4th item the full pass reads (same alignments; still ~650 bits per
    alignment, so its estimate is within ~0.04) and stops when even the best alignment
    is 0.1 short of ``threshold``. Different music sits near 0.5, far below 0.6."""
    if not a or not b:
        return False
    max_shift = int(12.0 / _ITEM_S)
    coarse = 0.0
    for s in range(-max_shift, max_shift + 1, step):
        lo, hi = max(0, -s), min(len(a), len(b) - s)
        if hi - lo < int(3.0 / _ITEM_S):
            continue
        idx = range(lo, hi, step * 4)
        bits = sum((a[i] ^ b[i + s]).bit_count() for i in idx)
        coarse = max(coarse, 1.0 - bits / (32 * len(idx)))
        if coarse >= threshold - 0.1:
            break
    if coarse < threshold - 0.1:
        return False
    return similarity(a, b, step=step) >= threshold
