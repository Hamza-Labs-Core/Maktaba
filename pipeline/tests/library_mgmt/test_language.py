"""Story 9.8 — language assignment from STT output."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.language import (
    LANGUAGE_CONFIDENCE_THRESHOLD,
    UNDETERMINED,
    decide_language,
)


@pytest.mark.unit
def test_high_confidence_detection_wins() -> None:
    out = decide_language("ar", 0.9, {"language": "auto"})
    assert out.language == "ar"
    assert out.reason == "detected"


@pytest.mark.unit
def test_low_confidence_returns_undetermined() -> None:
    out = decide_language("ar", 0.4, {"language": "auto"})
    assert out.language == UNDETERMINED
    assert out.reason == "low-confidence"


@pytest.mark.unit
def test_threshold_boundary_is_inclusive_above() -> None:
    out = decide_language("en", LANGUAGE_CONFIDENCE_THRESHOLD, {"language": "auto"})
    assert out.language == "en"


@pytest.mark.unit
def test_library_pinned_language_overrides_low_confidence() -> None:
    out = decide_language("en", 0.99, {"language": "ar"})
    assert out.language == "ar"
    assert out.reason == "library-pinned"


@pytest.mark.unit
def test_no_detection_returns_undetermined() -> None:
    out = decide_language(None, 0.0, {"language": "auto"})
    assert out.language == UNDETERMINED


@pytest.mark.unit
def test_library_default_auto_is_explicit() -> None:
    # ``language: "auto"`` and a missing key should behave identically.
    out_a = decide_language("en", 0.9, {})
    out_b = decide_language("en", 0.9, {"language": "auto"})
    assert out_a.language == out_b.language == "en"
