"""Stub embedding service is deterministic and shaped right."""

from __future__ import annotations

import math

import pytest

from maktaba_pipeline.search.embedder import StubEmbeddingService


@pytest.mark.unit
def test_dim_is_64() -> None:
    e = StubEmbeddingService()
    assert e.dim() == 64


@pytest.mark.unit
def test_embed_passages_returns_dim_64_vectors() -> None:
    e = StubEmbeddingService()
    out = e.embed_passages(["hello", "world"])
    assert len(out) == 2
    assert all(len(v) == 64 for v in out)


@pytest.mark.unit
def test_deterministic_for_same_text() -> None:
    e = StubEmbeddingService()
    a = e.embed_query("hello")
    b = e.embed_query("hello")
    assert a == b


@pytest.mark.unit
def test_different_text_yields_different_vector() -> None:
    e = StubEmbeddingService()
    a = e.embed_query("hello")
    b = e.embed_query("goodbye")
    assert a != b


@pytest.mark.unit
def test_query_and_passage_differ() -> None:
    # Same text encoded as query vs passage should differ because of
    # the distinct prefixes.
    e = StubEmbeddingService()
    q = e.embed_query("hello")
    p = e.embed_passages(["hello"])[0]
    assert q != p


@pytest.mark.unit
def test_vectors_are_unit_length() -> None:
    e = StubEmbeddingService()
    v = e.embed_query("hello")
    norm = math.sqrt(sum(x * x for x in v))
    assert norm == pytest.approx(1.0, abs=1e-6)


@pytest.mark.unit
def test_embed_passages_empty_list() -> None:
    assert StubEmbeddingService().embed_passages([]) == []
