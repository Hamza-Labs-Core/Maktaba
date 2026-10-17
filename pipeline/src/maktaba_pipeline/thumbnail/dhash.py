"""64-bit difference hash (dHash) for poster/thumbnail similarity.

Story 26.3 D5: series detection compares posters with a cheap perceptual
hash. Episodes of one show usually share branding (frame, logo, colour
scheme), so their thumbnails land within a few bits of each other.

The pixels come from ffmpeg (already a hard dependency) scaling the image
to 9×8 grayscale — no imaging library needed. :func:`dhash_from_gray` is
pure; :func:`ffmpeg_gray_args` builds the command the caller runs.
"""

from __future__ import annotations

__all__ = [
    "DHASH_VERSION",
    "FRAMES_VERSION",
    "FRAME_POINTS",
    "dhash_from_gray",
    "dhashes_from_frames",
    "ffmpeg_frame_args",
    "ffmpeg_gray_args",
    "frame_times",
    "hamming",
    "similarity",
]

DHASH_VERSION: int = 1

_W, _H = 9, 8


def ffmpeg_gray_args(image_path: str, *, binary: str = "ffmpeg") -> list[str]:
    """ffmpeg argv that writes the image as raw 9×8 8-bit grayscale to stdout."""
    return [
        binary, "-v", "error", "-nostdin", "-i", image_path,
        "-vf", f"scale={_W}:{_H}:flags=area,format=gray",
        "-frames:v", "1", "-f", "rawvideo", "pipe:1",
    ]  # fmt: skip


def dhash_from_gray(pixels: bytes) -> int:
    """dHash over a 9×8 row-major grayscale buffer: bit = left pixel brighter than right."""
    if len(pixels) != _W * _H:
        raise ValueError(f"expected {_W * _H} gray bytes, got {len(pixels)}")
    value = 0
    for y in range(_H):
        row = pixels[y * _W : (y + 1) * _W]
        for x in range(_W - 1):
            value = (value << 1) | (1 if row[x] > row[x + 1] else 0)
    return value


def hamming(a: int, b: int) -> int:
    return (a ^ b).bit_count()


def similarity(a: int, b: int) -> float:
    """1.0 for identical hashes, 0.0 for all 64 bits different."""
    return 1.0 - hamming(a, b) / 64.0


# --------------------------------------------------------------------------- video frames

#: where frames are taken: values > 1 are seconds from the start (intro / title card),
#: values ≤ 1 are fractions of the duration (the set, the presenter, inserts)
FRAME_POINTS: tuple[float, ...] = (15.0, 45.0, 0.25, 0.5, 0.75)
FRAMES_VERSION: int = 1


def frame_times(duration_s: float, points: tuple[float, ...] = FRAME_POINTS) -> list[float]:
    """Timestamps for :func:`ffmpeg_frames_args`, kept inside the video."""
    out: list[float] = []
    for p in points:
        t = p if p > 1 else p * duration_s
        if 0 <= t < duration_s - 1:
            out.append(round(t, 2))
    return out


def ffmpeg_frame_args(video_path: str, t: float, *, binary: str = "ffmpeg") -> list[str]:
    """ffmpeg argv writing the frame at ``t`` seconds as raw 9×8 gray to stdout. ``-ss``
    before ``-i`` seeks in the container, so only a few packets are read."""
    return [
        binary, "-v", "error", "-nostdin", "-ss", f"{t}", "-i", video_path,
        "-vf", f"scale={_W}:{_H}:flags=area,format=gray",
        "-frames:v", "1", "-f", "rawvideo", "pipe:1",
    ]  # fmt: skip


def dhashes_from_frames(raw: bytes) -> list[int]:
    """dHashes of the back-to-back 9×8 frames ffmpeg wrote; flat frames (black, a fade,
    one colour) carry no design and are dropped."""
    size = _W * _H
    out: list[int] = []
    for i in range(0, len(raw) - size + 1, size):
        h = dhash_from_gray(raw[i : i + size])
        if 8 <= h.bit_count() <= 56:
            out.append(h)
    return out
