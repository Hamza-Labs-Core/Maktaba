"""Reciprocal rank fusion tests."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.rrf import rrf_fuse


@pytest.mark.unit
def test_combines_two_lists() -> None:
    fts = [(1, 0.9), (2, 0.5)]
    vec = [(3, 0.8), (4, 0.4)]
    out = rrf_fuse(fts, vec)
    ids = {hit.doc_id for hit in out}
    assert ids == {1, 2, 3, 4}


@pytest.mark.unit
def test_shared_doc_wins_over_single_list() -> None:
    fts = [(1, 0.9), (2, 0.5)]
    vec = [(1, 0.8), (3, 0.4)]
    out = rrf_fuse(fts, vec)
    # Doc 1 appears in both lists, so it should rank first.
    assert out[0].doc_id == 1
    assert out[0].fts_rank == 1
    assert out[0].vector_rank == 1


@pytest.mark.unit
def test_default_k_is_60() -> None:
    out = rrf_fuse([(1, 1.0)], [])
    # Score == 1 / (60 + 1) ≈ 0.01639
    assert out[0].score == pytest.approx(1 / 61)


@pytest.mark.unit
def test_tiebreaker_is_doc_id_ascending() -> None:
    # Both docs in only-FTS at the same rank? Impossible — but we can
    # arrange same score by giving both docs identical rank in their
    # only lists.
    fts = [(5, 1.0)]
    vec = [(2, 1.0)]
    out = rrf_fuse(fts, vec)
    # Same score (both rank 1 in different lists) → ascending doc id.
    assert [h.doc_id for h in out] == [2, 5]


@pytest.mark.unit
def test_limit_applied() -> None:
    fts = [(i, 1.0 / (i + 1)) for i in range(100)]
    out = rrf_fuse(fts, [], limit=5)
    assert len(out) == 5


@pytest.mark.unit
def test_only_fts_input() -> None:
    out = rrf_fuse([(1, 0.9), (2, 0.5)], [])
    assert [h.doc_id for h in out] == [1, 2]
    assert all(h.vector_rank is None for h in out)


@pytest.mark.unit
def test_only_vector_input() -> None:
    out = rrf_fuse([], [(7, 0.9), (8, 0.5)])
    assert [h.doc_id for h in out] == [7, 8]
    assert all(h.fts_rank is None for h in out)


@pytest.mark.unit
def test_empty_inputs() -> None:
    assert rrf_fuse([], []) == []


@pytest.mark.unit
def test_zero_limit() -> None:
    assert rrf_fuse([(1, 1.0)], [], limit=0) == []
