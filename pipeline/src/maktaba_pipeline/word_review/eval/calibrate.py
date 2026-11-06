"""Whisper's word probability, calibrated (Epic 52, story 52.2).

Whisper is over-confident on fluent hallucinations and under-confident on short function
words, so its probability is mapped to P(wrong) per language and model by isotonic
regression (pool-adjacent-violators: the map never goes up as the probability rises),
fitted on the gold set's labels. The curve is stored next to the pack as
``language_packs/<code>/calibration/<model>.json`` and read by ``scoring.curve_for``.
"""

from __future__ import annotations

import json
from collections.abc import Sequence
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

__all__ = ["ece", "fit", "save"]


def fit(probs: Sequence[float], wrong: Sequence[bool]) -> tuple[list[float], list[float]]:
    """The isotonic (non-increasing in ``probs``) map to P(wrong): knots ``(x, y)``."""
    pts = sorted(zip(probs, wrong, strict=True))
    blocks: list[list[float]] = []  # [sum_y, count, x_lo, x_hi]
    for p, w in pts:
        blocks.append([float(w), 1.0, p, p])
        # P(wrong) must not rise with the probability: merge while it does
        while len(blocks) > 1 and blocks[-2][0] / blocks[-2][1] < blocks[-1][0] / blocks[-1][1]:
            s, c, lo, _hi = blocks.pop(-2)
            blocks[-1][0] += s
            blocks[-1][1] += c
            blocks[-1][2] = lo
    xs, ys = [], []
    for s, c, lo, hi in blocks:
        xs.append((lo + hi) / 2)
        ys.append(s / c)
    return xs, ys


def ece(probs: Sequence[float], wrong: Sequence[bool], predict: Any, bins: int = 10) -> float:
    """Expected calibration error of ``predict(prob) -> P(wrong)``, in ``bins`` bins."""
    if not probs:
        return 0.0
    total = 0.0
    for b in range(bins):
        lo, hi = b / bins, (b + 1) / bins
        idx = [i for i, p in enumerate(probs) if lo <= p < hi or (b == bins - 1 and p == 1.0)]
        if not idx:
            continue
        conf = sum(predict(probs[i]) for i in idx) / len(idx)
        acc = sum(wrong[i] for i in idx) / len(idx)
        total += len(idx) / len(probs) * abs(conf - acc)
    return total


def save(path: Path, probs: Sequence[float], wrong: Sequence[bool]) -> dict[str, Any]:
    from ..scoring import Curve

    xs, ys = fit(probs, wrong)
    curve = Curve(tuple(xs), tuple(ys))
    out = {
        "x": [round(x, 4) for x in xs],
        "y": [round(y, 4) for y in ys],
        "n": len(probs),
        "ece_before": round(ece(probs, wrong, lambda p: 1 - p), 4),
        "ece_after": round(ece(probs, wrong, curve), 4),
        "fitted_at": datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ"),
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(out, indent=1) + "\n", encoding="utf-8")
    return out
