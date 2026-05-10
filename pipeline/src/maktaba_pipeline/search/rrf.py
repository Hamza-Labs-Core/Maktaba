"""Reciprocal Rank Fusion — Story 5.4 / plan-05-04.

Combines the FTS and vector result lists by their *rank*, not their
raw score, so the two engines' incomparable score scales don't bias
the fused ranking. The standard formula with ``k=60`` matches what
the plan and the original Cormack et al. paper use.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass

__all__ = ["RrfHit", "rrf_fuse"]


@dataclass(frozen=True, slots=True)
class RrfHit:
    """One fused result with provenance from each input list.

    ``fts_rank`` / ``vector_rank`` are ``None`` when the doc didn't
    appear in that list; the score is then the contribution from
    the other list only.
    """

    doc_id: int
    score: float
    fts_rank: int | None
    vector_rank: int | None


def rrf_fuse(
    fts_hits: Sequence[tuple[int, float]],
    vector_hits: Sequence[tuple[int, float]],
    *,
    k: int = 60,
    limit: int = 50,
) -> list[RrfHit]:
    """Fuse two result lists into a ranked list of :class:`RrfHit`\\ s.

    Both inputs are ``[(doc_id, score)]`` sorted by *descending* score
    (higher = better). Rank is 1-based by position. The RRF score is
    ``sum(1 / (k + rank))`` summed over each list the doc appears in;
    docs present in *both* lists naturally rise to the top because
    they accumulate two reciprocals.

    Tiebreaker is ``doc_id`` ascending (deterministic ordering for
    cached responses and tests).
    """
    if k < 0:
        raise ValueError("k must be >= 0")
    if limit <= 0:
        return []

    fts_rank: dict[int, int] = {}
    for idx, (doc_id, _score) in enumerate(fts_hits):
        # Keep the first-seen rank if the same doc appears twice.
        if doc_id not in fts_rank:
            fts_rank[doc_id] = idx + 1

    vector_rank: dict[int, int] = {}
    for idx, (doc_id, _score) in enumerate(vector_hits):
        if doc_id not in vector_rank:
            vector_rank[doc_id] = idx + 1

    fused: dict[int, float] = {}
    for doc_id, rank in fts_rank.items():
        fused[doc_id] = fused.get(doc_id, 0.0) + 1.0 / (k + rank)
    for doc_id, rank in vector_rank.items():
        fused[doc_id] = fused.get(doc_id, 0.0) + 1.0 / (k + rank)

    # Sort by score desc, doc_id asc (stable tiebreaker).
    ordered = sorted(fused.items(), key=lambda kv: (-kv[1], kv[0]))
    return [
        RrfHit(
            doc_id=doc_id,
            score=score,
            fts_rank=fts_rank.get(doc_id),
            vector_rank=vector_rank.get(doc_id),
        )
        for doc_id, score in ordered[:limit]
    ]
