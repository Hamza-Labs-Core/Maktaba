"""Epic 5 — :mod:`maktaba_pipeline.search.embedder` tests.

Uses an in-process fake Chroma collection — the real chromadb
dependency is not installed and the production code imports it lazily.
"""

from __future__ import annotations

from collections.abc import Sequence
from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

from maktaba_pipeline.search.embedder import (
    SegmentDoc,
    embed_id_for,
    index_segments,
    semantic_search,
)


@dataclass
class _FakeCollection:
    """In-memory stand-in for ``chromadb.Collection``.

    Records upserts so tests can assert on the metadata shape and
    returns canned ``query`` payloads in the same nested-list layout
    Chroma uses.
    """

    name: str = "test"
    canned: dict[str, Any] = field(default_factory=dict)
    upserts: list[dict[str, Any]] = field(default_factory=list)
    last_query: dict[str, Any] | None = None

    def upsert(
        self,
        ids: Sequence[str],
        documents: Sequence[str],
        metadatas: Sequence[dict[str, Any]],
        embeddings: Sequence[Sequence[float]] | None = None,
    ) -> None:
        self.upserts.append(
            {
                "ids": list(ids),
                "documents": list(documents),
                "metadatas": [dict(m) for m in metadatas],
                "embeddings": [list(e) for e in embeddings] if embeddings else None,
            }
        )

    def delete(self, ids: Sequence[str]) -> None:
        pass

    def query(
        self,
        query_texts: Sequence[str] | None = None,
        query_embeddings: Sequence[Sequence[float]] | None = None,
        n_results: int = 10,
        where: dict[str, Any] | None = None,
    ) -> dict[str, Any]:
        self.last_query = {
            "query_texts": list(query_texts) if query_texts is not None else None,
            "query_embeddings": (
                [list(e) for e in query_embeddings] if query_embeddings else None
            ),
            "n_results": n_results,
            "where": where,
        }
        return self.canned


def _doc(seq: int, *, transcript_id: Any | None = None, video_id: Any | None = None) -> SegmentDoc:
    return SegmentDoc(
        segment_id=seq + 100,
        transcript_id=transcript_id or uuid4(),
        video_id=video_id or uuid4(),
        seq=seq,
        start_sec=float(seq),
        end_sec=float(seq + 1),
        text=f"segment {seq}",
        language="ara",
        speaker=None,
    )


def test_embed_id_for_is_deterministic() -> None:
    tid = uuid4()
    assert embed_id_for(tid, 5) == f"{tid}:5"


def test_index_segments_returns_zero_for_empty_input() -> None:
    coll = _FakeCollection()
    assert index_segments(coll, []) == 0
    assert coll.upserts == []


def test_index_segments_passes_metadata_and_ids() -> None:
    coll = _FakeCollection()
    tid = uuid4()
    docs = [_doc(1, transcript_id=tid), _doc(2, transcript_id=tid)]
    n = index_segments(coll, docs)
    assert n == 2
    call = coll.upserts[0]
    assert call["ids"] == [f"{tid}:1", f"{tid}:2"]
    # Metadata carries the canonical fields.
    md = call["metadatas"][0]
    assert md["segment_id"] == 101
    assert md["start_sec"] == 1.0
    assert md["language"] == "ara"


def test_index_segments_supplies_embeddings_when_callback_given() -> None:
    coll = _FakeCollection()
    calls: list[Sequence[str]] = []

    def embed(texts: Sequence[str]) -> list[list[float]]:
        calls.append(list(texts))
        return [[float(len(t))] for t in texts]

    index_segments(coll, [_doc(1)], embed=embed)
    assert calls == [["segment 1"]]
    # The embedding got handed to upsert.
    assert coll.upserts[0]["embeddings"] == [[len("segment 1")]]


def test_semantic_search_short_circuits_empty_query() -> None:
    coll = _FakeCollection()
    out = semantic_search(coll, "")
    assert out == []
    assert coll.last_query is None


def test_semantic_search_passes_where_for_video_scope() -> None:
    coll = _FakeCollection(
        canned={"ids": [[]], "documents": [[]], "metadatas": [[]], "distances": [[]]}
    )
    vid = uuid4()
    semantic_search(coll, "hi", video_id=vid)
    assert coll.last_query is not None
    assert coll.last_query["where"] == {"video_id": str(vid)}


def test_semantic_search_parses_chroma_results() -> None:
    tid = uuid4()
    vid = uuid4()
    coll = _FakeCollection(
        canned={
            "ids": [[f"{tid}:1", f"{tid}:2"]],
            "documents": [["doc one", "doc two"]],
            "metadatas": [
                [
                    {
                        "segment_id": 1,
                        "transcript_id": str(tid),
                        "video_id": str(vid),
                        "start_sec": 0.0,
                        "end_sec": 1.0,
                    },
                    {
                        "segment_id": 2,
                        "transcript_id": str(tid),
                        "video_id": str(vid),
                        "start_sec": 1.0,
                        "end_sec": 2.0,
                    },
                ]
            ],
            "distances": [[0.1, 0.4]],
        }
    )
    hits = semantic_search(coll, "hi")
    assert [h.segment_id for h in hits] == [1, 2]
    # score = 1 - distance, so smaller distance → larger score.
    assert hits[0].score > hits[1].score


def test_semantic_search_uses_custom_embedder() -> None:
    coll = _FakeCollection(
        canned={"ids": [[]], "documents": [[]], "metadatas": [[]], "distances": [[]]}
    )

    def embed(texts: Sequence[str]) -> list[list[float]]:
        return [[1.0, 2.0]]

    semantic_search(coll, "hi", embed=embed)
    assert coll.last_query is not None
    assert coll.last_query["query_embeddings"] == [[1.0, 2.0]]
    # Should NOT pass query_texts when embeddings are provided.
    assert coll.last_query["query_texts"] is None


def test_semantic_search_drops_malformed_rows() -> None:
    # Missing required metadata fields → row is silently skipped.
    coll = _FakeCollection(
        canned={
            "ids": [["a", "b"]],
            "documents": [["x", "y"]],
            "metadatas": [
                [
                    {"segment_id": 1},  # missing transcript_id / video_id / times
                    {
                        "segment_id": 2,
                        "transcript_id": str(uuid4()),
                        "video_id": str(uuid4()),
                        "start_sec": 0.0,
                        "end_sec": 1.0,
                    },
                ]
            ],
            "distances": [[0.0, 0.0]],
        }
    )
    hits = semantic_search(coll, "hi")
    assert [h.segment_id for h in hits] == [2]
