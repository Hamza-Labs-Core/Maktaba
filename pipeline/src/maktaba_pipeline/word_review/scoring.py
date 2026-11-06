"""How sure a word is wrong (Epic 52, stories 52.6 and 52.14): every signal is an error
probability, combined as ``1 − Π(1 − p)``, halved for a word that matches a name.

The table is pack-independent; thresholds move together with the library's sensitivity.
Whisper's word probability goes through the language's calibration curve when one is
fitted (story 52.2), else the identity map with a stricter rule: only a raw probability
below :data:`RAW_FLOOR` counts as ``low_confidence`` at all.
"""

from __future__ import annotations

import bisect
import json
from collections.abc import Iterable
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path

from ..log import get_logger

__all__ = [
    "BUDGET_FLOOR",
    "MAX_SPAN",
    "NAME_FACTOR",
    "P",
    "POOR_MIN_WORDS",
    "POOR_OVER_BUDGET",
    "POOR_SUSPECT_SHARE",
    "RAW_FLOOR",
    "TEXT_ONLY_RAISE",
    "THRESHOLDS",
    "Curve",
    "combine",
    "curve_for",
    "threshold",
]

_log = get_logger()

#: sensitivity → the priority a word needs to be flagged
THRESHOLDS: dict[str, float] = {"low": 0.65, "normal": 0.5, "high": 0.35}
#: a sentence without word evidence (an old transcript) is weaker evidence
TEXT_ONLY_RAISE = 0.1

#: each signal's error probability (fitted on the gold set when it grows: story 52.2)
P: dict[str, float] = {
    "listeners_disagree": 0.6,
    # above the text-only threshold (0.6): an old transcript has no other evidence, and a
    # word the dictionary does not know is the reason to look at it; a name halves it
    # below any threshold
    "not_in_dictionary": 0.65,
    "unsure": 0.1,
    "near_vocabulary": 0.5,
    "quran_mismatch": 0.8,
}
NAME_FACTOR = 0.5
#: without a calibration curve, a raw Whisper probability under this is low confidence
RAW_FLOOR = 0.3
#: at most this many adjacent words make one flag
MAX_SPAN = 4
#: the budget's floor for a short file
BUDGET_FLOOR = 5
#: a transcript "may be poor" past these
POOR_OVER_BUDGET = 0.25
POOR_SUSPECT_SHARE = 0.08
#: ...the share of suspect words counts only past this many words (a short clip is no
#: sample)
POOR_MIN_WORDS = 200


def combine(ps: Iterable[float]) -> float:
    out = 1.0
    for p in ps:
        out *= 1.0 - max(0.0, min(1.0, p))
    return 1.0 - out


def threshold(sensitivity: str, text_only: bool) -> float:
    t = THRESHOLDS.get(sensitivity, THRESHOLDS["normal"])
    return min(0.95, t + (TEXT_ONLY_RAISE if text_only else 0.0))


@dataclass(frozen=True, slots=True)
class Curve:
    """An isotonic map from Whisper's probability to P(wrong)."""

    x: tuple[float, ...]
    y: tuple[float, ...]
    calibrated: bool = True

    def __call__(self, prob: float) -> float:
        if not self.calibrated or not self.x:
            return 1.0 - prob
        i = bisect.bisect_left(self.x, prob)
        if i <= 0:
            return self.y[0]
        if i >= len(self.x):
            return self.y[-1]
        x0, x1, y0, y1 = self.x[i - 1], self.x[i], self.y[i - 1], self.y[i]
        if x1 == x0:
            return y1
        return y0 + (y1 - y0) * (prob - x0) / (x1 - x0)


IDENTITY = Curve((), (), calibrated=False)
_PACKS = Path(__file__).resolve().parent.parent / "language_packs"
_WARNED: set[tuple[str, str]] = set()


def _slug(model: str) -> str:
    return "".join(c if c.isalnum() or c in "-._" else "_" for c in model.split("/")[-1])


@lru_cache(maxsize=64)
def curve_for(language: str, model: str) -> Curve:
    """The fitted curve for a pack's language and the STT model, or the identity map
    (``calibration_missing`` logged once)."""
    path = _PACKS / language / "calibration" / f"{_slug(model)}.json"
    try:
        raw = json.loads(path.read_text(encoding="utf-8"))
        return Curve(tuple(float(v) for v in raw["x"]), tuple(float(v) for v in raw["y"]))
    except (OSError, ValueError, KeyError, TypeError):
        if (language, model) not in _WARNED:
            _WARNED.add((language, model))
            _log.info("calibration_missing", language=language, model=model[:80])
        return IDENTITY
