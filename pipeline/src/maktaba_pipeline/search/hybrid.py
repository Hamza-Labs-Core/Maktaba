"""Reciprocal-rank fusion of FTS + semantic search results.

RRF combines multiple ranked lists with no need to calibrate the
underlying scores. For a document ``d`` appearing at rank ``r_i`` in
list ``i``, its fused score is

    score(d) = Σ_i  1 / (k + r_i)

where ``k`` is a fixed dampening constant (the original paper uses 60).
Documents missing from a list contribute zero. The aggregate score
ranks documents by combined evidence rather than by either list's
absolute magnitude — useful here because ``ts_rank_cd`` and Chroma's
``1 - distance`` have wildly different scales.

The output preserves provenance: every :class:`HybridHit` carries the
ranks (or ``None``) from both input lists, so the API can surface
"matched in text" vs. "matched in meaning" badges.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass

from .embedder import SemanticHit
from .fts import FTSHit

__all__ = [
    "HybridHit",
    "reciprocal_rank_fusion",
]

# Magic constant from Cormack et al. (2009). Lower values weight the
# head of each list more aggressively; higher values flatten the
# contribution curve. 60 is the published default and is what most
# production hybrid-search rollups use.
DEFAULT_RRF_K: int = 60


@dataclass(slots=True, frozen=True)
class HybridHit:
    """One fused result row.

    The two ``*_rank`` fields are 1-based positions in the source list,
    or ``None`` when the document was absent from that list. ``score``
    is the RRF sum across the lists that contributed.
    """

    segment_id: int
    text: str
    fts_rank: int | None
    semantic_rank: int | None
    score: float
    fts_hit: FTSHit | None = None
    semantic_hit: SemanticHit | None = None


def reciprocal_rank_fusion(
    fts_hits: Iterable[FTSHit],
    semantic_hits: Iterable[SemanticHit],
    *,
    k: int = DEFAULT_RRF_K,
    limit: int = 50,
) -> list[HybridHit]:
    """Fuse FTS + semantic ranked lists into one ranked list.

    Stable tie-break: when two documents have identical ``score`` the
    order from the FTS list wins (deterministic for the same input).
    Documents are deduplicated by ``segment_id``.
    """
    if k <= 0:
        raise ValueError("RRF dampening constant k must be positive")
    if limit <= 0:
        return []

    # Accumulators keyed on segment_id.
    score_by_id: dict[int, float] = {}
    fts_rank_by_id: dict[int, int] = {}
    sem_rank_by_id: dict[int, int] = {}
    fts_hit_by_id: dict[int, FTSHit] = {}
    sem_hit_by_id: dict[int, SemanticHit] = {}
    arrival_order: dict[int, int] = {}
    ordinal = 0

    for rank, fts_hit in enumerate(fts_hits, start=1):
        sid = fts_hit.segment_id
        score_by_id[sid] = score_by_id.get(sid, 0.0) + 1.0 / (k + rank)
        fts_rank_by_id.setdefault(sid, rank)
        fts_hit_by_id.setdefault(sid, fts_hit)
        arrival_order.setdefault(sid, ordinal)
        ordinal += 1

    for rank, sem_hit in enumerate(semantic_hits, start=1):
        sid = sem_hit.segment_id
        score_by_id[sid] = score_by_id.get(sid, 0.0) + 1.0 / (k + rank)
        sem_rank_by_id.setdefault(sid, rank)
        sem_hit_by_id.setdefault(sid, sem_hit)
        arrival_order.setdefault(sid, ordinal)
        ordinal += 1

    fused = [
        HybridHit(
            segment_id=sid,
            text=(
                fts_hit_by_id[sid].text
                if sid in fts_hit_by_id
                else sem_hit_by_id[sid].text
            ),
            fts_rank=fts_rank_by_id.get(sid),
            semantic_rank=sem_rank_by_id.get(sid),
            score=score_by_id[sid],
            fts_hit=fts_hit_by_id.get(sid),
            semantic_hit=sem_hit_by_id.get(sid),
        )
        for sid in score_by_id
    ]
    # Sort by descending score, breaking ties by FTS arrival order
    # (preserves the FTS-first preference noted in the docstring).
    fused.sort(key=lambda h: (-h.score, arrival_order[h.segment_id]))
    return fused[:limit]
