"""Inputs for ``production_style.compute_metrics``'s regression test and benchmark.

Two sets, both what real callers pass:

* the parity worlds: the windows ``style_sample`` reads from the fake ffmpeg of
  ``tests/stages/world.py`` for every video of the first-party parity catalogs
  (classify's 28 videos, core-media's files, livetv's 44-video show), and
* a synthetic season at real scale: 30 episodes, four 30 s windows each (300
  analysis frames, 60 thumbnails a window), with a shot structure: a few camera angles
  that keep coming back, and inserts (b-roll, graphics) that never do.
"""

from __future__ import annotations

import functools
import random

import numpy as np

from maktaba_pipeline.classify.production_style import (
    ANALYSIS_FPS,
    THUMB_FPS,
    THUMB_H,
    THUMB_W,
    FrameStat,
    StreamInfo,
    WindowSample,
    parse_frame_stats,
    parse_stream_info,
    sample_windows,
    split_thumbs,
)
from tests.stages.world import _fake_output

Case = tuple[str, list[WindowSample], StreamInfo]

# the stage's own constants (core-media's style_sample: WINDOWS, WINDOW_S)
WINDOWS = 4
WINDOW_S = 30.0


def _classify_videos() -> list[tuple[str, float]]:
    """(filename, duration) of make_catalog's 28 videos (tests/stages/test_leftover_stages)."""
    out = [(f"v{n:03d}.mp4", 1500.0 + (n - 1) % 8) for n in range(1, 17)]
    out += [(f"v{n:03d}.mp4", 70.0) for n in range(17, 23)]
    out += [(f"v{n:03d}.mp4", 120.0) for n in range(23, 26)]
    out += [("v026.mp4", 45.0)]
    out += [(f"v{n:03d}.mp4", 3000.0) for n in range(27, 29)]
    return out


def _parity_videos() -> list[tuple[str, float]]:
    vids = _classify_videos()
    vids += [("talk_a.mp4", 1500.0), ("talk_b.mp4", 1500.0), ("silent_c.mp4", 1500.0)]
    vids += [(f"show_{i + 1:02d}.mp4", 1500.0 + i) for i in range(40)]
    vids += [(f"other_{i + 1:02d}.mp4", 2400.0) for i in range(4)]
    return vids


def _from_fake(filename: str, duration: float) -> Case:
    """What style_sample builds for one file: a WindowSample per sampled window."""
    family = filename.split("_")[0]
    out, err = _fake_output(family, "style")
    log = err.decode()
    info = parse_stream_info(log)
    frames = parse_frame_stats(log)
    windows = [
        WindowSample(length, frames, split_thumbs(out))
        for _start, length in sample_windows(duration, count=WINDOWS, length_s=WINDOW_S)
    ]
    return filename, windows, info


@functools.cache
def parity_cases() -> tuple[Case, ...]:
    return tuple(_from_fake(f, d) for f, d in _parity_videos())


# Randomness comes from random.Random (its streams are reproducible by contract, numpy's
# Generator streams are not promised across versions); numpy only does the arithmetic.


def _base(rng: random.Random) -> np.ndarray:
    """One camera set-up or insert: a smooth gradient with a few bright and dark patches."""
    yy, xx = np.mgrid[0:THUMB_H, 0:THUMB_W]
    img = rng.uniform(-2, 2) * xx + rng.uniform(-3, 3) * yy + rng.uniform(40, 200)
    for _ in range(rng.randrange(2, 8)):
        x0, y0 = rng.randrange(THUMB_W - 8), rng.randrange(THUMB_H - 6)
        w, h = rng.randrange(4, 24), rng.randrange(3, 14)
        img[y0 : y0 + h, x0 : x0 + w] += rng.uniform(-110, 110)
    out: np.ndarray = np.clip(img, 0, 255).astype(np.int16)
    return out


def _jitter(rng: random.Random, base: np.ndarray) -> bytes:
    """The set-up again, a frame later: sensor noise (-3..3) and a little movement."""
    noise = np.frombuffer(rng.randbytes(base.size), dtype=np.uint8).reshape(base.shape) % 7
    noisy = base + (rng.randrange(-4, 5) - 3) + noise
    return bytes(np.clip(noisy, 0, 255).astype(np.uint8).tobytes())


def _episode(rng: random.Random, angles: list[np.ndarray], inserts: float) -> list[WindowSample]:
    """Four windows of shots: a recurring angle, or (with odds ``inserts``) a new picture."""
    windows = []
    per_thumb = ANALYSIS_FPS // THUMB_FPS
    n_thumbs, n_frames = int(WINDOW_S * THUMB_FPS), int(WINDOW_S * ANALYSIS_FPS)
    for _ in range(WINDOWS):
        thumbs: list[bytes] = []
        cuts: set[int] = set()
        while len(thumbs) < n_thumbs:
            cuts.add(len(thumbs) * per_thumb)
            if rng.random() >= inserts:
                base, length = rng.choice(angles), rng.randrange(4, 24)
            else:
                base, length = _base(rng), rng.randrange(1, 6)
            thumbs += [_jitter(rng, base) for _ in range(length)]
        frames = []
        for j in range(n_frames):
            cut = j in cuts and j > 0
            r = [rng.random() for _ in range(6)]
            frames.append(
                FrameStat(
                    t=j / ANALYSIS_FPS,
                    scene=15 + 45 * r[0] if cut else 4 * r[0],
                    ydif=8 + 32 * r[1] if cut else 0.1 + 2.9 * r[1],
                    yavg=60 + 100 * r[2],
                    ylow=16.0,
                    yhigh=235.0,
                    satavg=10 + 50 * r[3],
                    uavg=110 + 30 * r[4],
                    vavg=115 + 30 * r[5],
                )
            )
        windows.append(WindowSample(WINDOW_S, frames, thumbs[:n_thumbs]))
    return windows


@functools.cache
def season_cases(episodes: int = 30) -> tuple[Case, ...]:
    """A season of ``episodes``: the first half a studio talk show (few inserts), the
    second half produced (mostly inserts): both ends of the look search's load."""
    rng = random.Random(54)
    angles = [_base(rng) for _ in range(4)]
    info = StreamInfo(1920, 1080, 25.0, 3018.0, 1500.0)
    return tuple(
        (f"season_e{i + 1:02d}", _episode(rng, angles, 0.3 if i < episodes // 2 else 0.8), info)
        for i in range(episodes)
    )
