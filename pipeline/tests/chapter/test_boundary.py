"""Boundary detection against a known three-cluster fixture.

Nine embeddings live in three tight clusters along orthogonal axes;
the detector should produce at least two boundaries (one between
each pair of adjacent clusters).
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.chapter.boundary import (
    cosine_distance,
    detect_boundaries,
)

# Three clusters of three vectors, each cluster on its own axis.
_FIXTURE: list[list[float]] = [
    # Cluster A — along x.
    [1.0, 0.0, 0.0],
    [0.95, 0.05, 0.0],
    [0.9, 0.1, 0.0],
    # Cluster B — along y.
    [0.0, 1.0, 0.0],
    [0.05, 0.95, 0.0],
    [0.1, 0.9, 0.0],
    # Cluster C — along z.
    [0.0, 0.0, 1.0],
    [0.05, 0.0, 0.95],
    [0.1, 0.0, 0.9],
]


@pytest.mark.unit
def test_cosine_distance_basics() -> None:
    assert cosine_distance([1.0, 0.0], [1.0, 0.0]) == pytest.approx(0.0)
    assert cosine_distance([1.0, 0.0], [-1.0, 0.0]) == pytest.approx(2.0)
    assert cosine_distance([1.0, 0.0], [0.0, 1.0]) == pytest.approx(1.0)


@pytest.mark.unit
def test_cosine_distance_handles_zero_vectors() -> None:
    assert cosine_distance([0.0, 0.0], [1.0, 1.0]) == 1.0
    assert cosine_distance([1.0, 1.0], [0.0, 0.0]) == 1.0


@pytest.mark.unit
def test_detect_boundaries_finds_cluster_transitions() -> None:
    boundaries = detect_boundaries(_FIXTURE, threshold=0.35, smoothing_window=1)
    # We expect boundaries at the A→B and B→C transitions; depending
    # on smoothing window, there may be additional weak signals.
    assert len(boundaries) >= 2
    indices = [idx for idx, _ in boundaries]
    # The 3 → 4 transition is A→B, the 6 → 7 transition is B→C.
    # detect_boundaries reports the index where the new cluster
    # *starts*, so indices 3 and 6 are the expected anchors.
    assert 3 in indices
    assert 6 in indices


@pytest.mark.unit
def test_detect_boundaries_with_smoothing() -> None:
    # Smoothing smears the immediate jumps but a low threshold still
    # picks up the dominant cluster transitions.
    boundaries = detect_boundaries(_FIXTURE, threshold=0.05, smoothing_window=3)
    assert len(boundaries) >= 2


@pytest.mark.unit
def test_detect_boundaries_empty_or_singleton() -> None:
    assert detect_boundaries([], threshold=0.35) == []
    assert detect_boundaries([[1.0, 0.0]], threshold=0.35) == []


@pytest.mark.unit
def test_detect_boundaries_rejects_invalid_smoothing() -> None:
    with pytest.raises(ValueError):
        detect_boundaries(_FIXTURE, smoothing_window=0)
