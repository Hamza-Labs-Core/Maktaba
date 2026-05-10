"""Story 9.10 — content-type classifier.

A small rules-based classifier that predicts
``content_type ∈ {lecture, sermon, interview, film, music_video, unknown}``
from features the probe + audio-extract stages already compute. We chose
rules over a trained model in v1 so the deployment surface stays at
zero — no checkpoint to ship, no inference runtime, no GPU. The class
boundaries below come from the architecture §5.2 reference table; a
trained classifier can replace :func:`classify` later without changing
the schema or the call sites.

Features the classifier reads (populated by Story 9.10 probe stage):

- ``duration_sec``                 — float
- ``silence_pct``                  — float, 0..1
- ``music_speech_ratio``           — float, 0..1 (1 = all music)
- ``mean_loudness_lufs``           — float, negative (e.g. -23)
- ``diarization_turn_density``     — float, turns/minute (or ``None``)
- ``segment_density``              — float, segments/minute

The argmax-with-floor (AC-2) returns ``unknown`` when no class beats
``CONFIDENCE_FLOOR``.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

__all__ = [
    "CONFIDENCE_FLOOR",
    "ClassifierResult",
    "ContentType",
    "Features",
    "MODEL_VERSION",
    "classify",
    "should_override",
]

#: AC-2 — below this max-class confidence we mark the video ``unknown``.
CONFIDENCE_FLOOR: float = 0.55

#: Bumped any time the rule set changes so the worker can re-run jobs
#: that were tagged with an older model.
MODEL_VERSION: str = "rules-v1"


class ContentType:
    """Vocabulary constants. We don't use ``Enum`` so callers can pass
    plain strings around (the DB column is TEXT)."""

    LECTURE = "lecture"
    SERMON = "sermon"
    INTERVIEW = "interview"
    FILM = "film"
    MUSIC_VIDEO = "music_video"
    UNKNOWN = "unknown"


#: The closed set of class strings; convenient for validation.
ALLOWED_TYPES: frozenset[str] = frozenset(
    {
        ContentType.LECTURE,
        ContentType.SERMON,
        ContentType.INTERVIEW,
        ContentType.FILM,
        ContentType.MUSIC_VIDEO,
        ContentType.UNKNOWN,
    }
)


@dataclass(slots=True, frozen=True)
class Features:
    """Strongly-typed view of the JSONB ``media_features.features`` blob.

    Keys not populated by the probe stage default to ``None``; the
    classifier handles missing values explicitly rather than treating 0
    as "no music".
    """

    duration_sec: float
    silence_pct: float
    music_speech_ratio: float
    mean_loudness_lufs: float | None = None
    diarization_turn_density: float | None = None
    segment_density: float = 0.0

    @classmethod
    def from_dict(cls, d: Mapping[str, Any]) -> Features:
        return cls(
            duration_sec=float(d.get("duration_sec", 0.0)),
            silence_pct=float(d.get("silence_pct", 0.0)),
            music_speech_ratio=float(d.get("music_speech_ratio", 0.0)),
            mean_loudness_lufs=_optional_float(d.get("mean_loudness_lufs")),
            diarization_turn_density=_optional_float(d.get("diarization_turn_density")),
            segment_density=float(d.get("segment_density", 0.0)),
        )


@dataclass(slots=True, frozen=True)
class ClassifierResult:
    """Outcome of :func:`classify`.

    ``content_type`` is one of :data:`ALLOWED_TYPES`. ``confidence`` is
    the score the argmax class beat — useful both for AC-2's floor and
    for surfacing in admin diagnostics.
    """

    content_type: str
    confidence: float
    scores: dict[str, float]


def _optional_float(v: Any) -> float | None:
    if v is None:
        return None
    try:
        return float(v)
    except (TypeError, ValueError):
        return None


def classify(features: Features) -> ClassifierResult:
    """Return the predicted content type.

    The rule sketch matches the architecture's heuristic guidance:

    - ``music_speech_ratio > 0.6`` is dominant music → ``music_video``.
    - Long, low-turn-density videos → ``film``.
    - High turn density (multi-speaker conversation) → ``interview``.
    - Long mostly-monolog with religious cue (silence pattern) → ``sermon``.
    - Long mostly-monolog otherwise → ``lecture``.

    Edge cases (architecture EC):
    - Ultra-short clips (< 60 s) → ``unknown`` (confidence floor not met).
    - Music-heavy concert with intro speech → ``music_video`` because
      the dominant-class score wins.
    """
    if features.duration_sec < 60.0:
        return ClassifierResult(
            content_type=ContentType.UNKNOWN,
            confidence=0.0,
            scores={ContentType.UNKNOWN: 1.0},
        )

    scores: dict[str, float] = {
        ContentType.LECTURE: 0.0,
        ContentType.SERMON: 0.0,
        ContentType.INTERVIEW: 0.0,
        ContentType.FILM: 0.0,
        ContentType.MUSIC_VIDEO: 0.0,
    }

    music = features.music_speech_ratio
    duration_min = features.duration_sec / 60.0
    turn_density = features.diarization_turn_density or features.segment_density

    # Music-heavy → music_video.
    if music >= 0.6:
        scores[ContentType.MUSIC_VIDEO] = 0.6 + min(music - 0.6, 0.4)
    elif music >= 0.3:
        scores[ContentType.MUSIC_VIDEO] = music * 0.6

    # Multi-speaker conversation → interview.
    if turn_density >= 4.0:
        scores[ContentType.INTERVIEW] = 0.55 + min((turn_density - 4.0) / 8.0, 0.4)
    elif turn_density >= 2.0:
        scores[ContentType.INTERVIEW] = 0.3 + (turn_density - 2.0) / 4.0 * 0.25

    # Long format with sparse turns and low silence → film. Films
    # dominate the long-tail; if the duration cleanly clears 60 min and
    # the speech pattern is sparse, we prefer film over lecture so the
    # 90-min fixture (Story 9.10 test case) classifies correctly.
    is_filmlike = duration_min >= 60.0 and turn_density < 3.0 and features.silence_pct < 0.2
    if is_filmlike:
        scores[ContentType.FILM] = 0.7 + min((duration_min - 60.0) / 120.0, 0.25)

    # Long single-speaker piece → sermon vs lecture by silence pattern.
    # Sermons tend to have rhythmic pauses (higher silence_pct);
    # lectures tend to be denser speech. Suppressed when the film
    # heuristic is in play so the long-form classifier doesn't fight
    # itself on a 90 min low-turn input.
    if duration_min >= 20.0 and turn_density < 2.0 and not is_filmlike:
        if features.silence_pct >= 0.25:
            scores[ContentType.SERMON] = 0.6 + min(features.silence_pct - 0.25, 0.3)
            scores[ContentType.LECTURE] = 0.4 + (1.0 - features.silence_pct) * 0.2
        else:
            scores[ContentType.LECTURE] = 0.6 + (1.0 - features.silence_pct) * 0.3
            scores[ContentType.SERMON] = 0.3 + features.silence_pct * 0.4

    best_type = max(scores, key=lambda k: scores[k])
    best_score = scores[best_type]
    if best_score < CONFIDENCE_FLOOR:
        return ClassifierResult(
            content_type=ContentType.UNKNOWN,
            confidence=best_score,
            scores=scores,
        )
    return ClassifierResult(content_type=best_type, confidence=best_score, scores=scores)


def should_override(
    *,
    user_set: bool,
    force: bool,
) -> bool:
    """AC-3 manual-override gate.

    A user-set ``content_type`` is preserved unless ``?force=true`` is
    passed at re-categorise time. Returns True if the auto-classifier
    is allowed to overwrite the existing value.
    """
    if not user_set:
        return True
    return force
