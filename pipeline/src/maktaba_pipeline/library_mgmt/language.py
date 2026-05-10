"""Story 9.8 — auto-categorisation: language tag assignment.

After ``TRANSCRIBED``, the language detected by Whisper is written to
`videos.detected_language`. This module is the single decision: given
the transcript-side language signal and the per-library settings, what
goes on the video row?

The three branches are AC-1 / AC-3:

- AC-1 single-language → write detected.
- AC-3 low confidence  → write ``"und"``.
- Library-pinned       → always write the library's setting (the user
  knows their archive better than Whisper does).

Multi-audio (AC-2) is decided by the caller picking the *primary* track
before invoking this module — we just receive the chosen language /
confidence pair.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

__all__ = [
    "LANGUAGE_CONFIDENCE_THRESHOLD",
    "LanguageAssignment",
    "UNDETERMINED",
    "decide_language",
]

#: AC-3 — below this confidence we tag as ``und``.
LANGUAGE_CONFIDENCE_THRESHOLD: float = 0.6

#: The undetermined sentinel that the API surfaces as
#: ``detected_language="und"``. Matches the ISO 639-3 code used for
#: indeterminate language.
UNDETERMINED: str = "und"


@dataclass(slots=True, frozen=True)
class LanguageAssignment:
    """The decision: what to write to ``videos.detected_language`` and
    why. ``reason`` is one of
    {``library-pinned``, ``low-confidence``, ``detected``}.
    """

    language: str
    reason: str


def decide_language(
    detected: str | None,
    confidence: float,
    library_settings: Mapping[str, Any],
) -> LanguageAssignment:
    """Resolve the per-video language assignment.

    The library's ``language`` setting wins when it's a fixed ISO code:
    the user has pinned this library to e.g. Arabic, so any STT result
    is overridden (AC EC: forced library language always wins regardless
    of STT confidence).

    With ``language: "auto"`` (the default) we trust the STT result if
    it cleared the confidence threshold; otherwise return ``und``.
    """
    pinned = library_settings.get("language", "auto")
    if pinned and pinned != "auto":
        return LanguageAssignment(language=str(pinned), reason="library-pinned")

    if not detected:
        return LanguageAssignment(language=UNDETERMINED, reason="low-confidence")

    if confidence < LANGUAGE_CONFIDENCE_THRESHOLD:
        return LanguageAssignment(language=UNDETERMINED, reason="low-confidence")

    return LanguageAssignment(language=detected, reason="detected")
