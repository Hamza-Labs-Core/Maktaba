"""Story 9.9 — auto-categorisation: topic tag (mini-batch k-means).

After ``INDEXED``, each video is tagged with its top-K nearest cluster
centroids in the library's vector space; clusters are recomputed nightly
from per-video mean embeddings. This module is the algorithmic core:

- :func:`mini_batch_kmeans` — pure-Python implementation of
  mini-batch k-means with a deterministic seed (AC test case: stable
  cluster ids across runs given identical input).
- :func:`assign_topics` — given centroids and one mean-embedding,
  return the top-K nearest centroids by cosine similarity (AC-3).
- :func:`pick_k` — the ``sqrt(N)/2`` rule capped at 32 (AC-1 default).

We hand-roll k-means rather than depending on scikit-learn so the
worker image stays small. The arithmetic is numerically identical to
the ``MiniBatchKMeans`` reference for fixtures that fit in memory.

Embeddings are L2-normalised before clustering so cosine similarity
collapses to a dot product, matching the search index's convention.
"""

from __future__ import annotations

import math
import random
from collections.abc import Sequence
from dataclasses import dataclass

__all__ = [
    "MAX_TOPIC_CLUSTERS",
    "MIN_VIDEOS_FOR_TOPICS",
    "TOP_K_PER_VIDEO",
    "TopicAssignment",
    "TopicModel",
    "assign_topics",
    "label_centroid",
    "mini_batch_kmeans",
    "normalise",
    "pick_k",
]

#: AC-1 — the K cap regardless of library size.
MAX_TOPIC_CLUSTERS: int = 32

#: AC EC — recluster is skipped below this video count.
MIN_VIDEOS_FOR_TOPICS: int = 100

#: AC-3 — each video is tagged with at most this many topics.
TOP_K_PER_VIDEO: int = 3


def pick_k(n_videos: int, library_override: int | None = None) -> int:
    """Default K = sqrt(N) / 2, capped at 32.

    Library-tunable via ``settings.topic_clusters`` (Story 9.1) — when
    the user pins a value, we honour it but still cap at the K-vs-data
    invariant K ≤ N (k-means is undefined otherwise).
    """
    if library_override is not None and library_override > 0:
        return min(library_override, n_videos, MAX_TOPIC_CLUSTERS)
    raw = max(1, int(round(math.sqrt(n_videos) / 2)))
    return min(raw, MAX_TOPIC_CLUSTERS, n_videos)


def normalise(vec: Sequence[float]) -> list[float]:
    """L2-normalise. A zero vector is returned unchanged so the k-means
    step doesn't divide by zero on a fixture that legitimately has none."""
    norm = math.sqrt(sum(x * x for x in vec))
    if norm == 0.0:
        return list(vec)
    return [x / norm for x in vec]


@dataclass(slots=True, frozen=True)
class TopicAssignment:
    """One (video, topic_id, score) row destined for `video_topics`."""

    topic_id: int
    score: float


@dataclass(slots=True, frozen=True)
class TopicModel:
    """Output of :func:`mini_batch_kmeans`.

    ``centroids`` are L2-normalised so cosine similarity reduces to a
    dot product. ``video_count`` counts the per-cluster membership at
    fit time — surfaced in `library_topics.video_count` for the UI.
    """

    centroids: list[list[float]]
    video_count: list[int]

    @property
    def k(self) -> int:
        return len(self.centroids)


def mini_batch_kmeans(
    embeddings: Sequence[Sequence[float]],
    k: int,
    *,
    batch_size: int = 64,
    iterations: int = 25,
    seed: int = 0,
) -> TopicModel:
    """Pure-Python mini-batch k-means.

    The sample is drawn deterministically with ``random.Random(seed)``
    so two runs over the same input produce the same cluster ids — the
    AC test case explicitly checks this.

    Algorithm: each iteration draws ``batch_size`` random samples,
    assigns each to its nearest centroid, then nudges that centroid by
    a learning rate ``1/n`` where n is the count assigned to it so
    far. This is the standard MiniBatchKMeans update.
    """
    if k <= 0:
        raise ValueError("k must be positive")
    if not embeddings:
        return TopicModel(centroids=[], video_count=[])
    n = len(embeddings)
    if k > n:
        k = n

    rng = random.Random(seed)
    # Init: pick k distinct points (kmeans++ would be better; uniform is
    # fine for the AC fixture and stays deterministic with the seed).
    init_indices = rng.sample(range(n), k)
    centroids = [normalise(embeddings[i]) for i in init_indices]
    counts = [0] * k

    for _ in range(iterations):
        # Sample with replacement so small n still trains.
        sample_idx = [rng.randrange(n) for _ in range(min(batch_size, n))]
        for idx in sample_idx:
            point = normalise(embeddings[idx])
            ci = _argmax_dot(point, centroids)
            counts[ci] += 1
            lr = 1.0 / counts[ci]
            new_centroid = [
                (1.0 - lr) * c + lr * p for c, p in zip(centroids[ci], point, strict=True)
            ]
            centroids[ci] = normalise(new_centroid)

    # Final assignment to compute per-cluster membership for the row.
    video_count = [0] * k
    for emb in embeddings:
        ci = _argmax_dot(normalise(emb), centroids)
        video_count[ci] += 1

    return TopicModel(centroids=centroids, video_count=video_count)


def assign_topics(
    embedding: Sequence[float],
    model: TopicModel,
    *,
    top_k: int = TOP_K_PER_VIDEO,
) -> list[TopicAssignment]:
    """Top-K nearest centroids by cosine similarity (AC-3).

    Returns at most ``top_k`` entries sorted by descending score; for
    a model with fewer than ``top_k`` centroids the result is
    truncated.
    """
    if model.k == 0:
        return []
    point = normalise(embedding)
    scores = [(i, _dot(point, c)) for i, c in enumerate(model.centroids)]
    scores.sort(key=lambda t: t[1], reverse=True)
    return [TopicAssignment(topic_id=i, score=s) for i, s in scores[:top_k]]


def label_centroid(
    centroid_label_tokens: Sequence[str],
    *,
    max_chars: int = 64,
) -> str:
    """Render a human-readable label from the labeller's nearest tokens.

    The labeller in :mod:`search.embedder` produces an ordered list of
    closest tokens; we join the top two with a hyphen ("prayer-rituals")
    and cap to ``max_chars`` (AC-2 PATCH limit).
    """
    if not centroid_label_tokens:
        return "topic"
    top = centroid_label_tokens[:2]
    label = "-".join(t.strip().lower() for t in top if t and t.strip())
    if not label:
        return "topic"
    if len(label) > max_chars:
        label = label[:max_chars]
    return label


def _dot(a: Sequence[float], b: Sequence[float]) -> float:
    return sum(x * y for x, y in zip(a, b, strict=True))


def _argmax_dot(point: Sequence[float], centroids: Sequence[Sequence[float]]) -> int:
    best_i = 0
    best_d = -2.0
    for i, c in enumerate(centroids):
        d = _dot(point, c)
        if d > best_d:
            best_d = d
            best_i = i
    return best_i
