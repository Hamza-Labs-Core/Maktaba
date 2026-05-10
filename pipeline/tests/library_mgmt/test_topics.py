"""Story 9.9 — k-means topic clustering and assignment."""

from __future__ import annotations

import pytest

from maktaba_pipeline.library_mgmt.topics import (
    MAX_TOPIC_CLUSTERS,
    MIN_VIDEOS_FOR_TOPICS,
    TOP_K_PER_VIDEO,
    assign_topics,
    label_centroid,
    mini_batch_kmeans,
    normalise,
    pick_k,
)


@pytest.mark.unit
def test_pick_k_default_is_sqrt_n_over_2() -> None:
    # 100 → sqrt(100)/2 = 5
    assert pick_k(100) == 5
    assert pick_k(10000) == MAX_TOPIC_CLUSTERS  # capped at 32
    assert pick_k(1) == 1


@pytest.mark.unit
def test_pick_k_honours_library_override_capped_at_n() -> None:
    assert pick_k(50, library_override=64) == 32  # cap
    assert pick_k(10, library_override=64) == 10  # k <= n
    assert pick_k(50, library_override=8) == 8


@pytest.mark.unit
def test_normalise_preserves_zero_vector() -> None:
    assert normalise([0.0, 0.0]) == [0.0, 0.0]


@pytest.mark.unit
def test_normalise_unit_vector() -> None:
    out = normalise([3.0, 4.0])
    assert pytest.approx(sum(x * x for x in out)) == 1.0


@pytest.mark.unit
def test_kmeans_is_deterministic_with_seed() -> None:
    pts = [[1.0, 0.0], [0.9, 0.1], [-1.0, 0.0], [-0.9, 0.1]]
    a = mini_batch_kmeans(pts, k=2, seed=42)
    b = mini_batch_kmeans(pts, k=2, seed=42)
    assert a.video_count == b.video_count


@pytest.mark.unit
def test_kmeans_separates_two_obvious_clusters() -> None:
    pts = [[1.0, 0.0]] * 5 + [[-1.0, 0.0]] * 5
    model = mini_batch_kmeans(pts, k=2, seed=0)
    assert sum(model.video_count) == 10
    assert sorted(model.video_count) == [5, 5]


@pytest.mark.unit
def test_kmeans_handles_k_greater_than_n() -> None:
    pts = [[1.0, 0.0], [0.0, 1.0]]
    model = mini_batch_kmeans(pts, k=5, seed=0)
    assert model.k == 2


@pytest.mark.unit
def test_assign_topics_returns_top_k_sorted_by_score() -> None:
    pts = [[1.0, 0.0], [-1.0, 0.0]]
    model = mini_batch_kmeans(pts, k=2, seed=0)
    out = assign_topics([1.0, 0.1], model)
    assert len(out) == 2
    assert out[0].score >= out[1].score


@pytest.mark.unit
def test_assign_topics_truncates_to_top_k_per_video() -> None:
    pts = [[1.0, 0.0]] * 4 + [[-1.0, 0.0]] * 4 + [[0.0, 1.0]] * 4 + [[0.0, -1.0]] * 4
    model = mini_batch_kmeans(pts, k=4, seed=1)
    out = assign_topics([1.0, 0.0], model)
    assert len(out) == TOP_K_PER_VIDEO


@pytest.mark.unit
def test_label_centroid_joins_top_two_with_hyphen() -> None:
    assert label_centroid(["Prayer", "Rituals", "Quran"]) == "prayer-rituals"


@pytest.mark.unit
def test_label_centroid_caps_to_max_chars() -> None:
    label = label_centroid(["a" * 100, "b" * 100], max_chars=10)
    assert len(label) == 10


@pytest.mark.unit
def test_label_centroid_falls_back_when_empty() -> None:
    assert label_centroid([]) == "topic"
    assert label_centroid([""]) == "topic"


@pytest.mark.unit
def test_min_videos_for_topics_constant() -> None:
    # AC EC: recluster is skipped below this threshold.
    assert MIN_VIDEOS_FOR_TOPICS == 100
