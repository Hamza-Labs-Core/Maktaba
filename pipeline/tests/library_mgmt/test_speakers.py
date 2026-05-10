"""Story 9.11 — voiceprint matching, naming, merge support."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.speakers import (
    DEFAULT_MATCH_THRESHOLD,
    MatchAssignment,
    NewSpeaker,
    SpeakerCandidate,
    cosine_distance,
    decide,
    next_unknown_index,
    pack_voiceprint,
    unknown_display_name,
    unpack_voiceprint,
)


def _v(*xs: float) -> list[float]:
    return list(xs)


@pytest.mark.unit
def test_pack_unpack_roundtrip() -> None:
    vec = _v(1.0, 2.5, -3.25, 0.0)
    blob = pack_voiceprint(vec)
    assert unpack_voiceprint(blob) == pytest.approx(vec)


@pytest.mark.unit
def test_unpack_rejects_misaligned_blob() -> None:
    with pytest.raises(ValueError):
        unpack_voiceprint(b"abc")  # length not divisible by 4


@pytest.mark.unit
def test_cosine_distance_identical_vectors_is_zero() -> None:
    a = _v(1.0, 2.0, 3.0)
    assert cosine_distance(a, a) == pytest.approx(0.0, abs=1e-6)


@pytest.mark.unit
def test_cosine_distance_orthogonal_is_one() -> None:
    a = _v(1.0, 0.0)
    b = _v(0.0, 1.0)
    assert cosine_distance(a, b) == pytest.approx(1.0, abs=1e-6)


@pytest.mark.unit
def test_cosine_distance_anti_parallel_is_two() -> None:
    a = _v(1.0, 0.0)
    b = _v(-1.0, 0.0)
    assert cosine_distance(a, b) == pytest.approx(2.0, abs=1e-6)


@pytest.mark.unit
def test_cosine_distance_dim_mismatch_raises() -> None:
    with pytest.raises(ValueError):
        cosine_distance(_v(1.0), _v(1.0, 2.0))


@pytest.mark.unit
def test_decide_returns_match_below_threshold() -> None:
    candidates = [SpeakerCandidate(speaker_id="s1", voiceprint=_v(1.0, 0.0))]
    out = decide(_v(1.0, 0.05), candidates)
    assert isinstance(out, MatchAssignment)
    assert out.speaker_id == "s1"


@pytest.mark.unit
def test_decide_creates_new_when_far_from_all() -> None:
    candidates = [SpeakerCandidate(speaker_id="s1", voiceprint=_v(1.0, 0.0))]
    out = decide(_v(0.0, 1.0), candidates)  # orthogonal → distance 1
    assert isinstance(out, NewSpeaker)
    assert out.unknown_index == 1


@pytest.mark.unit
def test_decide_creates_new_with_no_candidates() -> None:
    out = decide(_v(1.0, 0.0), [])
    assert isinstance(out, NewSpeaker)
    assert out.unknown_index == 1


@pytest.mark.unit
def test_next_unknown_index_skips_used_slots() -> None:
    cands = [
        SpeakerCandidate("s1", _v(1.0), unknown_index=1),
        SpeakerCandidate("s2", _v(1.0), unknown_index=3),
    ]
    assert next_unknown_index(cands) == 2  # fills the lowest free slot


@pytest.mark.unit
def test_next_unknown_index_ignores_named_speakers() -> None:
    cands = [
        SpeakerCandidate("s1", _v(1.0), name="Sheikh Hamza", unknown_index=None),
    ]
    assert next_unknown_index(cands) == 1


@pytest.mark.unit
def test_unknown_display_name_format() -> None:
    assert unknown_display_name(7) == "unknown-7"


@pytest.mark.unit
def test_default_match_threshold_pinned() -> None:
    # Sanity: the 0.35 default is what the AC bakes into the test plan.
    assert DEFAULT_MATCH_THRESHOLD == 0.35
