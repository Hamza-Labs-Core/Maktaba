"""Scoring and settings (Epic 52, stories 52.6 and 52.14)."""

from __future__ import annotations

import pytest

from maktaba_pipeline import rules, settings
from maktaba_pipeline.word_review import scoring
from maktaba_pipeline.word_review.settings import (
    REASONS,
    SOURCES,
    STATUSES,
    SWITCHABLE,
    WordReviewConfig,
    effective,
)


def test_signals_combine_as_independent_errors() -> None:
    assert scoring.combine([]) == 0.0
    assert scoring.combine([0.5]) == pytest.approx(0.5)
    assert scoring.combine([0.5, 0.6]) == pytest.approx(0.8)


def test_sensitivity_moves_the_threshold_and_text_only_raises_it() -> None:
    assert scoring.threshold("low", False) > scoring.threshold("normal", False)
    assert scoring.threshold("normal", False) > scoring.threshold("high", False)
    assert scoring.threshold("normal", True) == pytest.approx(0.6)
    # one dictionary signal flags an old transcript, a name match halves it below any
    assert scoring.P["not_in_dictionary"] >= scoring.threshold("normal", True)
    assert scoring.P["not_in_dictionary"] * scoring.NAME_FACTOR < scoring.threshold("high", False)


def test_without_a_curve_the_identity_map() -> None:
    c = scoring.curve_for("xx", "no-such-model")
    assert not c.calibrated and c(0.2) == pytest.approx(0.8)
    fitted = scoring.Curve((0.0, 0.5, 1.0), (0.9, 0.4, 0.0))
    assert fitted(0.25) == pytest.approx(0.65) and fitted(2.0) == 0.0


def test_the_home_defaults() -> None:
    c = WordReviewConfig()
    assert c.enabled and c.backfill and c.sensitivity == "normal" and c.budget_per_hour == 40
    assert WordReviewConfig.SCHEMA_VERSION == (1, 0)
    assert "word_review" in rules.SECTIONS


def test_the_budget_has_limits() -> None:
    with pytest.raises(settings.SettingsError):
        settings.apply(WordReviewConfig, {"budget_per_hour": 300})
    with pytest.raises(settings.SettingsError):
        settings.apply(WordReviewConfig, {"sensitivity": "extreme"})


def test_a_library_key_overrides_the_home_default() -> None:
    home = WordReviewConfig(reasons_off=frozenset({"near_vocabulary"}))
    reasons = {"not_in_dictionary": False, "near_vocabulary": True}
    e = effective(home, {"sensitivity": "low", "budget_per_hour": 80, "reasons": reasons})
    assert e.sensitivity == "low" and e.budget_per_hour == 80
    assert "not_in_dictionary" not in e.reasons and "near_vocabulary" in e.reasons
    never = effective(WordReviewConfig(), None)
    assert never.backfill is True and never.reasons == frozenset(SWITCHABLE)


def test_bad_or_removed_keys_are_ignored() -> None:
    e = effective(WordReviewConfig(), {"budget_per_hour": 300, "llm_judge": {"on": True},
                                       "sensitivity": "loud", "enabled": "yes"})  # fmt: skip
    assert e.budget_per_hour == 40 and e.sensitivity == "normal" and e.enabled is True


def test_the_shared_codes_have_no_llm() -> None:
    assert "llm_disagrees" not in REASONS and "llm" not in SOURCES
    assert "quran_mismatch" in REASONS and "quran" in SOURCES
    assert STATUSES[0] == "open" and "stale" in STATUSES
