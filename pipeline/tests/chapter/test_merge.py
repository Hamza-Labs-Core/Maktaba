"""enforce_min_chapter_sec: weaker neighbour drops when too close."""

from __future__ import annotations

import pytest

from maktaba_pipeline.chapter.merge import enforce_min_chapter_sec


@pytest.mark.unit
def test_keeps_stronger_of_two_close_boundaries() -> None:
    boundaries = [(3, 0.4), (5, 0.6)]
    # unit_starts[3] = 60, unit_starts[5] = 120 — gap 60s, below min.
    unit_starts = [0.0, 20.0, 40.0, 60.0, 90.0, 120.0]
    kept = enforce_min_chapter_sec(
        boundaries, unit_starts, min_chapter_sec=180.0
    )
    # The second (stronger, dist=0.6) wins.
    assert kept == [(5, 0.6)]


@pytest.mark.unit
def test_keeps_both_when_far_enough_apart() -> None:
    boundaries = [(2, 0.5), (6, 0.5)]
    unit_starts = [0.0, 30.0, 60.0, 120.0, 180.0, 240.0, 300.0]
    kept = enforce_min_chapter_sec(
        boundaries, unit_starts, min_chapter_sec=180.0
    )
    assert kept == [(2, 0.5), (6, 0.5)]


@pytest.mark.unit
def test_empty_input_returns_empty() -> None:
    assert enforce_min_chapter_sec([], [], min_chapter_sec=180.0) == []


@pytest.mark.unit
def test_drops_weaker_when_new_candidate_is_weaker() -> None:
    boundaries = [(2, 0.8), (3, 0.2)]
    unit_starts = [0.0, 30.0, 60.0, 90.0]
    kept = enforce_min_chapter_sec(boundaries, unit_starts, min_chapter_sec=180.0)
    # First boundary wins (stronger, dist=0.8); second is dropped.
    assert kept == [(2, 0.8)]


@pytest.mark.unit
def test_chain_of_close_boundaries_keeps_one() -> None:
    boundaries = [(1, 0.4), (2, 0.5), (3, 0.6), (4, 0.45)]
    unit_starts = [0.0, 10.0, 20.0, 30.0, 40.0]
    kept = enforce_min_chapter_sec(boundaries, unit_starts, min_chapter_sec=60.0)
    # All within 60s of each other → keep the strongest, dist=0.6.
    assert kept == [(3, 0.6)]


@pytest.mark.unit
def test_rejects_negative_min() -> None:
    with pytest.raises(ValueError):
        enforce_min_chapter_sec([], [], min_chapter_sec=-1.0)
