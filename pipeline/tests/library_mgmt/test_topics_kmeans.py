"""Story 9.9 — whole-library k-means (numpy) and bulk assignment."""

from __future__ import annotations

import random

import pytest

from maktaba_pipeline.library_mgmt.topics import (
    TopicModel,
    assign_all,
    assign_topics,
    kmeans_fit,
)

pytest.importorskip("numpy")


def _clusters(n_per: int, centers: list[list[float]], seed: int = 1) -> list[list[float]]:
    rng = random.Random(seed)
    return [[x + rng.gauss(0.0, 0.05) for x in c] for c in centers for _ in range(n_per)]


CENTERS = [
    [1.0, 0.0, 0.0, 0.0],
    [0.0, 1.0, 0.0, 0.0],
    [0.0, 0.0, 1.0, 0.0],
]


@pytest.mark.unit
def test_kmeans_finds_separated_clusters_deterministically() -> None:
    pts = _clusters(50, CENTERS)
    a = kmeans_fit(pts, 3, seed=0)
    b = kmeans_fit(pts, 3, seed=0)
    assert a.centroids == b.centroids and a.video_count == b.video_count
    assert sorted(a.video_count) == [50, 50, 50]
    # every true centre has a centroid within cosine 0.99
    for c in CENTERS:
        assert max(sum(x * y for x, y in zip(c, cc, strict=True)) for cc in a.centroids) > 0.99


@pytest.mark.unit
def test_kmeans_caps_k_at_n_and_handles_empty_input() -> None:
    assert kmeans_fit([], 3).k == 0
    two = kmeans_fit([[1.0, 0.0], [0.0, 1.0]], 5)
    assert two.k == 2 and sorted(two.video_count) == [1, 1]
    with pytest.raises(ValueError):
        kmeans_fit([[1.0, 0.0]], 0)


@pytest.mark.unit
def test_assign_all_matches_assign_topics_and_names_each_topic() -> None:
    pts = _clusters(10, CENTERS, seed=3)
    model = kmeans_fit(pts, 3)
    rows, nearest = assign_all(pts, model, top_k=2)
    for p, row in zip(pts, rows, strict=True):
        single = assign_topics(p, model, top_k=2)
        assert [a.topic_id for a in row] == [a.topic_id for a in single]
        assert all(abs(a.score - b.score) < 1e-9 for a, b in zip(row, single, strict=True))
    assert len(nearest) == 3
    # the video naming a topic is assigned to it first
    for t, i in enumerate(nearest):
        assert rows[i][0].topic_id == t
    assert assign_all([], TopicModel(centroids=[], video_count=[])) == ([], [])


@pytest.mark.unit
def test_two_clusters_emptied_at_once_take_different_points() -> None:
    """Identical points: every start centroid coincides, so clusters 1 and 2 empty in
    the same iteration; each must take its own point, never the same one."""
    pts = [[1.0, 0.0, 0.0]] * 10
    model = kmeans_fit(pts, 3)
    assert model.k == 3
    assert all(c >= 1 for c in model.video_count), model.video_count
    assert sum(model.video_count) == 10
