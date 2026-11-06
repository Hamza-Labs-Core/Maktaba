"""Precision, recall and flag rate of a flagger (Epic 52, story 52.2)."""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from dataclasses import dataclass, field
from typing import Any

__all__ = ["Counts", "Report", "score"]


@dataclass(slots=True)
class Counts:
    words: int = 0
    wrong: int = 0
    flagged: int = 0
    flagged_wrong: int = 0

    @property
    def precision(self) -> float:
        return self.flagged_wrong / self.flagged if self.flagged else 1.0

    @property
    def recall(self) -> float:
        return self.flagged_wrong / self.wrong if self.wrong else 1.0

    @property
    def flag_rate(self) -> float:
        return self.flagged / self.words if self.words else 0.0

    def as_dict(self) -> dict[str, Any]:
        return {
            "words": self.words,
            "wrong": self.wrong,
            "flagged": self.flagged,
            "precision": round(self.precision, 4),
            "recall": round(self.recall, 4),
            "flag_rate": round(self.flag_rate, 4),
        }


@dataclass(slots=True)
class Report:
    total: Counts = field(default_factory=Counts)
    by_language: dict[str, Counts] = field(default_factory=dict)
    by_reason: dict[str, Counts] = field(default_factory=dict)
    spots_total: int = 0
    spots_hit: int = 0
    spots_missed: list[str] = field(default_factory=list)
    audio_sec: float = 0.0

    def as_dict(self) -> dict[str, Any]:
        hours = self.audio_sec / 3600 if self.audio_sec else 0.0
        flagged = sum(c.flagged for c in self.by_language.values())
        return {
            "languages": {k: v.as_dict() for k, v in sorted(self.by_language.items())},
            "reasons": {k: self._reason(v) for k, v in sorted(self.by_reason.items())},
            "hearing_test": {
                "spots": self.spots_total,
                "flagged": self.spots_hit,
                "missed": sorted(self.spots_missed),
            },  # fmt: skip
            "flags_per_hour": round(flagged / hours, 2) if hours else None,
        }

    def _reason(self, c: Counts) -> dict[str, Any]:
        """A reason's precision, and its recall of every wrong word."""
        return Counts(self.total.words, self.total.wrong, c.flagged, c.flagged_wrong).as_dict()


def score(
    report: Report,
    language: str,
    labels: Iterable[str],
    near_deletions: Iterable[int],
    flagged: Mapping[int, Iterable[str]],
) -> None:
    """Add one clip: each hypothesis word's label, deletions beside it, and the reasons
    of each flagged word index."""
    lang = report.by_language.setdefault(language, Counts())
    for i, (label, dels) in enumerate(zip(labels, near_deletions, strict=True)):
        wrong = label != "ok" or dels > 0
        reasons = list(flagged.get(i, ()))
        for c in (lang, report.total):
            c.words += 1
            c.wrong += wrong
            if reasons:
                c.flagged += 1
                c.flagged_wrong += wrong
        for r in reasons:
            rc = report.by_reason.setdefault(r, Counts())
            rc.flagged += 1
            rc.flagged_wrong += wrong
