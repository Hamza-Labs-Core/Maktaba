"""``production_style.compute_metrics`` as it was before its speed-up (PR for the Epic 54
Phase 4 open item), kept in the tests only: the regression test checks that the shipped
version returns exactly what this one does. Do not change it; it is the reference.
"""
# ruff: noqa: E501

from __future__ import annotations

import json
import math
import statistics
from collections.abc import Sequence
from dataclasses import asdict, dataclass
from operator import add, sub
from pathlib import Path
from typing import Any

from maktaba_pipeline.classify.production_style import (
    _LOOK_H,
    _LOOK_W,
    ANALYSIS_FPS,
    CUT_SCORE,
    DARK_Y,
    EDGE_GRADIENT,
    HELD_S,
    LOOK_DISTANCE,
    STILL_YDIF,
    THUMB_FPS,
    THUMB_H,
    THUMB_W,
    FrameStat,
    StreamInfo,
    StyleMetrics,
    WindowSample,
    _mean,
    _pstd,
    _quantile,
)


def _look_vec(thumb: bytes) -> tuple[int, ...]:
    bw, bh = THUMB_W // _LOOK_W, THUMB_H // _LOOK_H
    out: list[int] = []
    for by in range(_LOOK_H):
        rows = [thumb[(by * bh + r) * THUMB_W : (by * bh + r + 1) * THUMB_W] for r in range(bh)]
        for bx in range(_LOOK_W):
            out.append(sum(map(sum, [row[bx * bw : (bx + 1) * bw] for row in rows])) // (bw * bh))
    return tuple(out)


def _dist(a: Sequence[int], b: Sequence[int]) -> float:
    if len(a) != len(b):
        raise ValueError("look vectors differ in length")
    total: int = sum(map(abs, map(sub, a, b)))
    return total / len(a)


def _edge_share(thumb: bytes) -> float:
    w, h = THUMB_W, THUMB_H
    edges = 0
    # per row in C (map/operator), not per pixel in Python: this runs for every
    # sampled thumbnail of every season
    for y in range(h - 1):
        row, nxt = y * w, (y + 1) * w
        px = thumb[row : row + w - 1]
        right = map(abs, map(sub, px, thumb[row + 1 : row + w]))
        down = map(abs, map(sub, px, thumb[nxt : nxt + w - 1]))
        edges += sum(map(EDGE_GRADIENT.__le__, map(add, right, down)))
    return edges / ((w - 1) * (h - 1))


@dataclass(frozen=True, slots=True)
class _LookStats:
    labels: list[int]  # look id per thumbnail (all windows, in order)
    looks: int
    top3_share: float
    revisit_share: float
    recurring: list[bool]  # per thumbnail: its look recurs or is held (not an insert)


def _looks(windows: Sequence[Sequence[bytes]]) -> _LookStats:
    """Cluster thumbnails into looks (greedy, first-come representatives).

    A *run* is a maximal stretch of consecutive thumbnails with the same look;
    every window starts a new run. A look is an *insert* (b-roll, graphic, AI
    shot) when it never comes back and is never held for ``HELD_S`` — studio
    angles keep coming back, and an on-location presenter holds the shot.
    """
    reps: list[tuple[int, ...]] = []
    counts: list[int] = []
    runs: list[int] = []
    longest: list[int] = []
    labels: list[int] = []
    changes = revisits = 0
    for thumbs in windows:
        prev = -1
        run = 0
        for th in thumbs:
            v = _look_vec(th)
            best, best_d = -1, LOOK_DISTANCE
            for i, r in enumerate(reps):
                d = _dist(v, r)
                if d < best_d:
                    best, best_d = i, d
            if best < 0:
                reps.append(v)
                counts.append(0)
                runs.append(0)
                longest.append(0)
                best = len(reps) - 1
            elif best != prev and prev >= 0:
                revisits += 1
            if best != prev:
                runs[best] += 1
                run = 0
                if prev >= 0:
                    changes += 1
            run += 1
            longest[best] = max(longest[best], run)
            counts[best] += 1
            labels.append(best)
            prev = best
    total = sum(counts)
    if not total:
        return _LookStats([], 0, 1.0, 0.0, [])
    top3 = sum(sorted(counts, reverse=True)[:3]) / total
    return _LookStats(
        labels,
        len(reps),
        top3,
        revisits / changes if changes else 0.0,
        [runs[i] >= 2 or longest[i] >= HELD_S * THUMB_FPS for i in labels],
    )


def compute_metrics(
    windows: Sequence[WindowSample], info: StreamInfo | None = None
) -> StyleMetrics:
    """Reduce one episode's sampled windows to :class:`StyleMetrics`."""
    frames = [f for w in windows for f in w.frames]
    # the first frame of each window has no predecessor: no score, no YDIF
    body = [f for w in windows for f in list(w.frames)[1:]]
    good = [[t for t in w.thumbs if len(t) == THUMB_W * THUMB_H] for w in windows]
    thumbs = [t for ths in good for t in ths]
    sampled = sum(min(w.length_s, len(w.frames) / ANALYSIS_FPS) for w in windows)
    minutes = max(sampled / 60.0, 1e-6)
    calm = [f.ydif for f in body if f.scene < CUT_SCORE]
    looks = _looks(good)
    # frames under an insert look (thumbnail j of a window ≈ analysis frame j·step)
    step = ANALYSIS_FPS // THUMB_FPS
    inserts: list[FrameStat] = []
    k = 0
    for w, ths in zip(windows, good, strict=True):
        fr = list(w.frames)
        inserts += [
            fr[j * step]
            for j in range(len(ths))
            if not looks.recurring[k + j] and j * step < len(fr)
        ]
        k += len(ths)
    info = info or StreamInfo()
    return StyleMetrics(
        sampled_s=sampled,
        cuts_per_min=sum(1 for f in body if f.scene >= CUT_SCORE) / minutes,
        motion=statistics.median(calm) if calm else 0.0,
        motion_p90=_quantile(calm, 0.9),
        still_share=(sum(1 for f in body if f.ydif < STILL_YDIF) / len(body)) if body else 1.0,
        luma_mean=_mean([f.yavg for f in frames]),
        luma_std=_pstd([f.yavg for f in frames]),
        dark_share=(sum(1 for f in frames if f.yavg < DARK_Y) / len(frames)) if frames else 0.0,
        sat_mean=_mean([f.satavg for f in frames]),
        sat_std=_pstd([f.satavg for f in frames]),
        chroma_spread=math.hypot(_pstd([f.uavg for f in frames]), _pstd([f.vavg for f in frames])),
        looks_per_min=looks.looks / minutes,
        top3_share=looks.top3_share,
        revisit_share=looks.revisit_share,
        insert_share=(looks.recurring.count(False) / len(thumbs)) if thumbs else 0.0,
        insert_luma=_mean([f.yavg for f in inserts]),
        insert_sat=_mean([f.satavg for f in inserts]),
        # every 2nd thumbnail is plenty for a density estimate
        edge_density=_mean([_edge_share(t) for t in thumbs[::2]]),
        width=info.width,
        height=info.height,
        fps=info.fps,
        bitrate_kbps=info.bitrate_kbps,
    )


# ---------------------------------------------------------------------------
# the fixture: this reference's answers over tests/classify/style_inputs, so the full sweep
# in test_production_style_cost runs only the shipped version. Rewrite it (only from this
# reference) when those inputs change:
#
#     uv run --extra dev python -m tests.classify.style_reference

FIXTURE = Path(__file__).with_name("style_reference.json")


def reference_outputs() -> dict[str, dict[str, Any]]:
    from tests.classify.style_inputs import parity_cases, season_cases

    return {
        name: asdict(compute_metrics(windows, info))
        for name, windows, info in (*parity_cases(), *season_cases())
    }


if __name__ == "__main__":
    # json writes floats with repr, which reads back as exactly the same float
    FIXTURE.write_text(json.dumps(reference_outputs(), indent=1, sort_keys=True) + "\n")
