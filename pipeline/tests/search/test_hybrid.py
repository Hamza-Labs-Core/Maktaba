"""Epic 5 — :mod:`maktaba_pipeline.search.hybrid` tests."""

from __future__ import annotations

from uuid import uuid4

import pytest

from maktaba_pipeline.search.embedder import SemanticHit
from maktaba_pipeline.search.fts import FTSHit
from maktaba_pipeline.search.hybrid import (
    DEFAULT_RRF_K,
    HybridHit,
    reciprocal_rank_fusion,
)


def _fts(segment_id: int, *, rank_score: float = 0.5) -> FTSHit:
    return FTSHit(
        segment_id=segment_id,
        transcript_id=uuid4(),
        video_id=uuid4(),
        start_sec=0.0,
        end_sec=1.0,
        text=f"f-{segment_id}",
        rank=rank_score,
    )


def _sem(segment_id: int, *, score: float = 0.5) -> SemanticHit:
    return SemanticHit(
        segment_id=segment_id,
        transcript_id=uuid4(),
        video_id=uuid4(),
        start_sec=0.0,
        end_sec=1.0,
        text=f"s-{segment_id}",
        score=score,
    )


def test_rrf_overlapping_documents_rank_higher() -> None:
    # Segment 2 is rank 1 in both lists — the maximum possible score
    # for k=60: 2/(60+1) ≈ 0.0328. Every other segment can pick up at
    # most 1/61 from one list, so 2 wins decisively.
    fts = [_fts(2), _fts(1), _fts(3)]
    sem = [_sem(2), _sem(4), _sem(5)]
    out = reciprocal_rank_fusion(fts, sem)
    assert out[0].segment_id == 2


def test_rrf_preserves_per_list_ranks() -> None:
    fts = [_fts(1), _fts(2)]
    sem = [_sem(2), _sem(3)]
    out = {h.segment_id: h for h in reciprocal_rank_fusion(fts, sem)}
    assert out[1].fts_rank == 1
    assert out[1].semantic_rank is None
    assert out[2].fts_rank == 2
    assert out[2].semantic_rank == 1
    assert out[3].fts_rank is None
    assert out[3].semantic_rank == 2


def test_rrf_score_matches_formula() -> None:
    # One doc, only in FTS list at rank 1 → score = 1 / (k + 1).
    out = reciprocal_rank_fusion([_fts(1)], [], k=60)
    expected = 1.0 / 61.0
    assert abs(out[0].score - expected) < 1e-9


def test_rrf_limit_truncates() -> None:
    fts = [_fts(i) for i in range(1, 11)]
    out = reciprocal_rank_fusion(fts, [], limit=3)
    assert len(out) == 3
    # Top-3 IDs are the first three from the FTS list.
    assert [h.segment_id for h in out] == [1, 2, 3]


def test_rrf_empty_inputs_return_empty_list() -> None:
    assert reciprocal_rank_fusion([], []) == []


def test_rrf_returns_hybridhit_instances() -> None:
    out = reciprocal_rank_fusion([_fts(1)], [_sem(1)])
    assert all(isinstance(h, HybridHit) for h in out)


def test_rrf_zero_limit_returns_empty() -> None:
    assert reciprocal_rank_fusion([_fts(1)], [_sem(2)], limit=0) == []


def test_rrf_rejects_non_positive_k() -> None:
    with pytest.raises(ValueError):
        reciprocal_rank_fusion([], [], k=0)


def test_rrf_default_k_is_published_constant() -> None:
    # Sanity check: the published Cormack et al. constant is 60.
    assert DEFAULT_RRF_K == 60


def test_rrf_stable_tie_break_prefers_fts_order() -> None:
    # Two segments appearing only once each, in separate lists at the
    # same rank → identical RRF score. The FTS arrival wins.
    fts = [_fts(1)]
    sem = [_sem(2)]
    out = reciprocal_rank_fusion(fts, sem)
    assert [h.segment_id for h in out] == [1, 2]
