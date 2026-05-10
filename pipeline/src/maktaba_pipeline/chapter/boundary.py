"""Cosine-distance boundary detection on smoothed unit embeddings.

The detector slides a smoothing window over the input embeddings and
emits a boundary at every position where the cosine distance between
adjacent smoothed centroids exceeds ``threshold``. Numpy is used
when available for the hot loops; a pure-Python fallback keeps the
module importable in environments without numpy installed (search
extras are optional in ``pyproject.toml``).
"""

from __future__ import annotations

import math
from collections.abc import Sequence
from typing import Any

try:  # numpy is in the optional ``search`` extra; degrade gracefully.
    import numpy as _np  # type: ignore[import-not-found,unused-ignore]

    _HAS_NUMPY = True
except ImportError:  # pragma: no cover - exercised via env without numpy
    _np = None  # type: ignore[assignment,unused-ignore]
    _HAS_NUMPY = False

__all__ = ["cosine_distance", "detect_boundaries"]


def cosine_distance(a: Sequence[float], b: Sequence[float]) -> float:
    """Return ``1 - cosine_similarity(a, b)``.

    Robust to zero-magnitude inputs (returns 1.0 rather than NaN);
    this matches our convention of "totally dissimilar" for an
    embedding that collapsed to the origin.
    """
    if len(a) != len(b):
        raise ValueError("cosine_distance: vectors must be same length")
    dot = 0.0
    na = 0.0
    nb = 0.0
    for ai, bi in zip(a, b, strict=True):
        dot += ai * bi
        na += ai * ai
        nb += bi * bi
    if na == 0.0 or nb == 0.0:
        return 1.0
    sim = dot / (math.sqrt(na) * math.sqrt(nb))
    # Clamp because float rounding can push us past +/-1.
    if sim > 1.0:
        sim = 1.0
    elif sim < -1.0:
        sim = -1.0
    return 1.0 - sim


def _smooth_window(
    embeddings: Sequence[Sequence[float]], i: int, window: int
) -> list[float]:
    """Mean-pool embeddings in ``[i, i+window)`` (clipped to bounds)."""
    n = len(embeddings)
    lo = max(0, i)
    hi = min(n, i + window)
    if lo >= hi:
        return list(embeddings[i])
    dim = len(embeddings[lo])
    acc = [0.0] * dim
    count = 0
    for k in range(lo, hi):
        vec = embeddings[k]
        if len(vec) != dim:
            # Defensive: skip mis-shaped rows rather than crash.
            continue
        for d, val in enumerate(vec):
            acc[d] += float(val)
        count += 1
    if count == 0:
        return list(embeddings[i])
    return [v / count for v in acc]


def _smooth_numpy(
    embeddings: Sequence[Sequence[float]], window: int
) -> Any:
    """Numpy fast-path: rolling-window mean across rows.

    Return type is :class:`Any` so the rest of the module stays
    importable when numpy is missing — the compute path is gated on
    :data:`_HAS_NUMPY` so an empty return is unreachable in that
    case.
    """
    assert _np is not None
    arr = _np.asarray(embeddings, dtype=_np.float64)
    n = arr.shape[0]
    out = _np.empty_like(arr)
    for i in range(n):
        lo = i
        hi = min(n, i + window)
        out[i] = arr[lo:hi].mean(axis=0)
    return out


def detect_boundaries(
    embeddings: Sequence[Sequence[float]],
    *,
    threshold: float = 0.35,
    smoothing_window: int = 3,
) -> list[tuple[int, float]]:
    """Return ``(index, distance)`` pairs where ``distance > threshold``.

    ``index`` is the unit index *at* which the boundary occurs — i.e.
    a returned ``(7, 0.42)`` means unit 7 starts a new chapter and
    the distance between the smoothed centroid ending at unit 6 and
    the smoothed centroid starting at unit 7 was 0.42.

    With fewer than two embeddings the function returns an empty
    list — there is nothing to compare against.
    """
    n = len(embeddings)
    if n < 2:
        return []
    if smoothing_window < 1:
        raise ValueError("smoothing_window must be >= 1")

    if _HAS_NUMPY:
        smoothed = _smooth_numpy(embeddings, smoothing_window)
        out: list[tuple[int, float]] = []
        for i in range(1, n):
            dist = cosine_distance(smoothed[i - 1].tolist(), smoothed[i].tolist())
            if dist > threshold:
                out.append((i, dist))
        return out

    out_py: list[tuple[int, float]] = []
    prev = _smooth_window(embeddings, 0, smoothing_window)
    for i in range(1, n):
        curr = _smooth_window(embeddings, i, smoothing_window)
        dist = cosine_distance(prev, curr)
        if dist > threshold:
            out_py.append((i, dist))
        prev = curr
    return out_py
