"""In-memory ChromaClient stub mirrors the upsert/query/delete API."""

from __future__ import annotations

import pytest

from maktaba_pipeline.search.chroma_client import make_in_memory_client


@pytest.mark.unit
def test_upsert_then_query_returns_closest() -> None:
    client = make_in_memory_client()
    col = client.collection("lib-1")
    col.upsert(
        unit_ids=["1", "2"],
        embeddings=[[1.0, 0.0], [0.0, 1.0]],
        metadatas=[{"language": "en"}, {"language": "ar"}],
        documents=["hello", "مرحبا"],
    )
    out = col.query(embedding=[1.0, 0.1], top_k=2)
    # First result is "1" (closer to [1, 0.1]).
    assert out[0][0] == "1"


@pytest.mark.unit
def test_collection_is_cached_per_library() -> None:
    client = make_in_memory_client()
    a = client.collection("lib-1")
    b = client.collection("lib-1")
    c = client.collection("lib-2")
    assert a is b
    assert a is not c


@pytest.mark.unit
def test_delete_removes_vectors() -> None:
    client = make_in_memory_client()
    col = client.collection("lib-1")
    col.upsert(
        unit_ids=["1"],
        embeddings=[[1.0, 0.0]],
        metadatas=[{"language": "en"}],
        documents=["hello"],
    )
    col.delete(["1"])
    assert col.query(embedding=[1.0, 0.0], top_k=5) == []


@pytest.mark.unit
def test_where_filter_eq() -> None:
    client = make_in_memory_client()
    col = client.collection("lib-1")
    col.upsert(
        unit_ids=["1", "2"],
        embeddings=[[1.0, 0.0], [1.0, 0.0]],
        metadatas=[{"language": "en"}, {"language": "ar"}],
        documents=["a", "b"],
    )
    out = col.query(
        embedding=[1.0, 0.0],
        top_k=5,
        where={"language": {"$eq": "ar"}},
    )
    assert [uid for uid, _ in out] == ["2"]


@pytest.mark.unit
def test_query_empty_collection_returns_empty() -> None:
    client = make_in_memory_client()
    col = client.collection("lib-1")
    assert col.query(embedding=[1.0, 0.0], top_k=5) == []
