"""Word review's settings (Epic 52, story 52.14): the home's defaults, ``rules.word_review``
(owned by the ``review.words`` component), and a library's own ``word_review`` key in its
settings JSON, which overrides them key by key.

The shared codes (reasons, statuses, candidate sources) live here too: the Go API
(``api/internal/handlers/review``) and the web (``web/src/lib/reviewApi.ts``) name the same
lists, and a test in each fails on a code the others do not know.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any, ClassVar

from .. import settings
from ..log import get_logger

__all__ = [
    "REASONS",
    "SENSITIVITIES",
    "SOURCES",
    "STATUSES",
    "Effective",
    "WordReviewConfig",
    "effective",
]

_log = get_logger()

REASONS: tuple[str, ...] = (
    "low_confidence",
    "not_in_dictionary",
    "listeners_disagree",
    "quran_mismatch",
    "named_entity",
    "near_vocabulary",
    "manual",
    "previous_fix",
    "over_budget",
)
STATUSES: tuple[str, ...] = (
    "open", "accepted", "corrected", "name", "dictionary", "skipped", "stale",
)  # fmt: skip
SOURCES: tuple[str, ...] = (
    "whisper", "second_listen", "dictionary", "vocabulary", "quran", "gazetteer", "previous_fix",
)  # fmt: skip
SENSITIVITIES: tuple[str, ...] = ("low", "normal", "high")

#: the reasons a library may switch off (``manual``, ``previous_fix`` and ``over_budget``
#: are made by people and the budget, never by a check)
SWITCHABLE: tuple[str, ...] = REASONS[:6]


@dataclass(frozen=True)
class WordReviewConfig:
    """How suspect transcript words are found for a home admin to review."""

    SCHEMA_VERSION: ClassVar[tuple[int, int]] = (1, 0)

    #: word_check runs on new transcripts
    enabled: bool = True
    #: existing transcripts are checked too, text only, at the lowest priority, while
    #: nothing waits to be transcribed
    backfill: bool = True
    #: low: fewer, surer flags; high: more
    sensitivity: str = "normal"
    #: the most open flags per hour of audio (the rest are kept as skipped)
    budget_per_hour: int = 40
    #: reasons that never flag a word
    reasons_off: frozenset[str] = frozenset()
    #: shown at the top of the review queue
    reviewers_note: str = ""


_G = "Word review"
settings.register(WordReviewConfig, {
    "enabled": settings.Help(
        _G, "Check new transcripts",
        "Each new transcript is checked for words that are not in the dictionary, look "
        "wrong or were heard with low confidence; they wait for a home admin in Word "
        "review."),
    "backfill": settings.Help(
        _G, "Check existing transcripts",
        "Transcripts made before word review are checked too, from their text alone, at "
        "the lowest priority and only while nothing waits to be transcribed."),
    "sensitivity": settings.Help(
        _G, "Sensitivity",
        "Low asks about fewer words, only the surest; high asks about more.",
        choices=SENSITIVITIES),
    "budget_per_hour": settings.Help(
        _G, "Most words per hour of audio",
        "At most this many words of a file wait for review per hour of its audio; the "
        "rest are kept, skipped, and a file with too many is marked as maybe poor.",
        minimum=10, maximum=200),
    "reasons_off": settings.Help(
        _G, "Reasons switched off",
        "Words flagged only for these reasons are not asked about: low_confidence, "
        "not_in_dictionary, listeners_disagree, quran_mismatch, named_entity, "
        "near_vocabulary. With quran_mismatch off, verse runs are left unchecked."),
    "reviewers_note": settings.Help(
        _G, "Note for reviewers",
        "Shown at the top of the review queue (for example: spell names as on the book "
        "covers)."),
})  # fmt: skip


@dataclass(frozen=True, slots=True)
class Effective:
    """A library's settings: its own key over the home's defaults."""

    enabled: bool
    backfill: bool
    sensitivity: str
    budget_per_hour: int
    reasons: frozenset[str]
    reviewers_note: str

    def fingerprint(self) -> dict[str, Any]:
        """What changes the flags (the note does not)."""
        return {
            "sensitivity": self.sensitivity,
            "budget": self.budget_per_hour,
            "reasons": sorted(self.reasons),
            "enabled": self.enabled,
        }


def _valid(k: str, v: Any) -> bool:
    """A library value of a plain key that can be used as it is."""
    if k in ("enabled", "backfill"):
        return isinstance(v, bool)
    if k == "sensitivity":
        return v in SENSITIVITIES
    if k == "budget_per_hour":
        return isinstance(v, int) and not isinstance(v, bool) and 10 <= v <= 200
    return False


def effective(home: WordReviewConfig, library: Any) -> Effective:
    """``library`` (the library's ``word_review`` JSON, or None) over ``home``. A value
    that cannot be used is ignored with a warning (the home default applies); the
    removed ``llm_judge`` keys of a test build are ignored the same way."""
    lib = library if isinstance(library, dict) else {}
    out: dict[str, Any] = {
        "enabled": home.enabled,
        "backfill": home.backfill,
        "sensitivity": home.sensitivity,
        "budget_per_hour": home.budget_per_hour,
        "reviewers_note": home.reviewers_note,
    }
    on = set(SWITCHABLE) - set(home.reasons_off)
    for k, v in lib.items():
        if _valid(k, v):
            out[k] = v
        elif k == "reviewers_note" and isinstance(v, str):
            out[k] = v[:500]
        elif k == "reasons" and isinstance(v, dict):
            for r, flag in v.items():
                if r in SWITCHABLE and isinstance(flag, bool):
                    (on.add if flag else on.discard)(r)
        else:
            _log.warning("word_review_setting_ignored", key=str(k)[:60])
    return Effective(
        enabled=bool(out["enabled"]),
        backfill=bool(out["backfill"]),
        sensitivity=str(out["sensitivity"]),
        budget_per_hour=int(out["budget_per_hour"]),
        reasons=frozenset(on),
        reviewers_note=str(out["reviewers_note"]),
    )
