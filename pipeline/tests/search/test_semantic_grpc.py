"""The SemanticSearch and Embed RPCs over the shared semantic index.

``asyncio.run`` opens a socket pair for its loop, so like the other gRPC suites these are
not ``unit``-marked (the unit tier's netguard forbids sockets).
"""

from __future__ import annotations

import asyncio
import json
import uuid
from typing import Any

import pytest

from maktaba_pipeline import grpc_server
from maktaba_pipeline.grpc_server import PipelineService, _dispatch
from maktaba_pipeline.search import semantic
from maktaba_pipeline.search.semantic import IndexedSegment, SemanticIndex
from tests.search.semantic_fakes import FakeCollection, FakeEmbedder


def _segments(tid: uuid.UUID, texts: list[str]) -> list[IndexedSegment]:
    return [
        IndexedSegment(
            segment_id=100 + i,
            transcript_id=tid,
            seq=i,
            start_sec=float(i * 5),
            end_sec=float(i * 5 + 5),
            text=t,
        )
        for i, t in enumerate(texts)
    ]


def _call(service: PipelineService, method: Any, payload: dict[str, Any]) -> dict[str, Any]:
    raw = asyncio.run(_dispatch(json.dumps(payload).encode(), method))
    out: dict[str, Any] = json.loads(raw)
    return out


def test_semantic_search_rpc_returns_hits_and_validates() -> None:
    seen: list[tuple[str, int, list[str], list[str]]] = []

    async def fake(q: str, k: int, libs: list[str], langs: list[str]) -> list[dict[str, Any]]:
        seen.append((q, k, libs, langs))
        return [{"segment_id": 1, "video_id": "v", "start_sec": 0.0, "end_sec": 1.0, "score": 0.9}]

    svc = PipelineService(semantic=fake)
    out = _call(svc, svc.semantic_search, {"q": " صلاة ", "k": 5000, "library_ids": ["L"]})
    assert out["hits"][0]["segment_id"] == 1
    assert seen == [("صلاة", grpc_server.MAX_SEMANTIC_K, ["L"], [])]
    _call(svc, svc.semantic_search, {"q": "x", "languages": ["en"]})
    assert seen[-1][3] == ["en"]
    assert "languages" in _call(svc, svc.semantic_search, {"q": "x", "languages": "en"})["error"]
    assert "q is required" in _call(svc, svc.semantic_search, {"q": ""})["error"]
    assert "k must be" in _call(svc, svc.semantic_search, {"q": "x", "k": 0})["error"]
    assert "library_ids" in _call(svc, svc.semantic_search, {"q": "x", "library_ids": "L"})["error"]
    bare = PipelineService()
    assert "not configured" in _call(bare, bare.semantic_search, {"q": "x"})["error"]


def test_default_backends_use_the_shared_index(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(semantic, "search_available", lambda: False)
    assert grpc_server.default_search_backends() == (None, None, None)

    coll, emb = FakeCollection(), FakeEmbedder()
    vid, lib = uuid.uuid4(), uuid.uuid4()
    SemanticIndex(coll, emb).replace_video(
        video_id=vid, library_id=lib, segments=_segments(uuid.uuid4(), ["صوم رمضان"]), language="ar"
    )
    monkeypatch.setattr(semantic, "search_available", lambda: True)
    monkeypatch.setattr(semantic, "get_query_embedder", lambda model=None: emb)
    monkeypatch.setattr(
        semantic.SemanticIndex, "for_queries", classmethod(lambda cls: cls(coll, emb))
    )
    embed, search, warm = grpc_server.default_search_backends()
    assert embed is not None and search is not None and warm is emb
    svc = PipelineService(embedder=embed, semantic=search)
    out = _call(svc, svc.semantic_search, {"q": "صوم رمضان", "k": 3, "library_ids": [str(lib)]})
    assert [h["video_id"] for h in out["hits"]] == [str(vid)]
    # the language filter applies inside the index, before the top k
    req = {"q": "صوم رمضان", "k": 3, "library_ids": [str(lib)], "languages": ["en"]}
    assert _call(svc, svc.semantic_search, req)["hits"] == []
    req["languages"] = ["ar", "en"]
    assert len(_call(svc, svc.semantic_search, req)["hits"]) == 1
    vec = _call(svc, svc.embed, {"text": "صوم"})["vector"]
    assert len(vec) > 0 and emb.queries[-1] == "صوم"
