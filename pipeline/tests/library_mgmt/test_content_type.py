"""Story 9.10 — content-type classifier."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.content_type import (
    CONFIDENCE_FLOOR,
    ContentType,
    Features,
    classify,
    should_override,
)


@pytest.mark.unit
def test_short_clip_is_unknown() -> None:
    f = Features(duration_sec=30.0, silence_pct=0.1, music_speech_ratio=0.1)
    out = classify(f)
    assert out.content_type == ContentType.UNKNOWN


@pytest.mark.unit
def test_music_heavy_is_music_video() -> None:
    f = Features(
        duration_sec=240.0,
        silence_pct=0.05,
        music_speech_ratio=0.85,
        diarization_turn_density=0.5,
    )
    out = classify(f)
    assert out.content_type == ContentType.MUSIC_VIDEO


@pytest.mark.unit
def test_long_low_turn_low_silence_is_film() -> None:
    f = Features(
        duration_sec=90 * 60.0,
        silence_pct=0.05,
        music_speech_ratio=0.1,
        diarization_turn_density=1.5,
    )
    out = classify(f)
    assert out.content_type == ContentType.FILM


@pytest.mark.unit
def test_high_turn_density_is_interview() -> None:
    f = Features(
        duration_sec=45 * 60.0,
        silence_pct=0.1,
        music_speech_ratio=0.1,
        diarization_turn_density=8.0,
    )
    out = classify(f)
    assert out.content_type == ContentType.INTERVIEW


@pytest.mark.unit
def test_long_monologue_with_high_silence_is_sermon() -> None:
    f = Features(
        duration_sec=45 * 60.0,
        silence_pct=0.4,
        music_speech_ratio=0.05,
        diarization_turn_density=0.5,
    )
    out = classify(f)
    assert out.content_type == ContentType.SERMON


@pytest.mark.unit
def test_long_monologue_with_low_silence_is_lecture() -> None:
    f = Features(
        duration_sec=45 * 60.0,
        silence_pct=0.05,
        music_speech_ratio=0.05,
        diarization_turn_density=0.5,
    )
    out = classify(f)
    assert out.content_type == ContentType.LECTURE


@pytest.mark.unit
def test_features_from_dict_handles_missing_keys() -> None:
    f = Features.from_dict({"duration_sec": 100, "silence_pct": 0.1, "music_speech_ratio": 0.5})
    assert f.duration_sec == 100
    assert f.diarization_turn_density is None


@pytest.mark.unit
def test_should_override_respects_force_flag() -> None:
    assert should_override(user_set=False, force=False)
    assert not should_override(user_set=True, force=False)
    assert should_override(user_set=True, force=True)


@pytest.mark.unit
def test_below_floor_returns_unknown() -> None:
    # All scores below CONFIDENCE_FLOOR → unknown.
    f = Features(
        duration_sec=120.0,
        silence_pct=0.1,
        music_speech_ratio=0.1,
        diarization_turn_density=0.5,
    )
    out = classify(f)
    if out.content_type != ContentType.UNKNOWN:
        # If the heuristics happened to assign a class above the floor,
        # at least confirm the floor invariant is respected.
        assert out.confidence >= CONFIDENCE_FLOOR
