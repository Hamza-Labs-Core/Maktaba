"""End-to-end engine test with a fake DB and the in-memory chroma stub.

The fake DB only needs to answer two SQL shapes: the SQLite FTS
search (``SELECT f.unit_id, bm25 …``) and the hydration SELECT
(``SELECT u.id, u.segment_ids, … WHERE u.id IN (...)``). Recognised
by uniquely-identifying keywords so a future SQL edit will surface
as a clear failure rather than a silent drift.
"""

from __future__ import annotations

import json
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from typing import Any

import pytest

from maktaba_pipeline.search.chroma_client import make_in_memory_client
from maktaba_pipeline.search.embedder import StubEmbeddingService
from maktaba_pipeline.search.engine import SearchRequest, search


@dataclass
class _Unit:
    unit_id: int
    transcript_id: int
    video_id: str
    text: str
    language: str
    start_sec: float
    end_sec: float
    segment_ids: list[int]


@dataclass
class FakeEngineDB:
    dialect: str = "sqlite"
    units: dict[int, _Unit] = field(default_factory=dict)
    # FTS hits: ``{normalized_query_substring: [(unit_id, score)]}``.
    fts_hits: dict[str, list[tuple[int, float]]] = field(default_factory=dict)

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    async def fetchrow(self, sql: str, *args: Any) -> dict[str, Any] | None:
        # Engine never calls fetchrow today.
        return None

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        squashed = " ".join(sql.split())
        # FTS search (SQLite shape).
        if "transcripts_fts" in squashed and "bm25" in squashed:
            match_text = str(args[0])
            for key, hits in self.fts_hits.items():
                if key in match_text:
                    # The engine *negates* bm25; emit a low (good) raw
                    # score so the inversion produces a high "better"
                    # value.
                    return [{"unit_id": uid, "score": -score} for uid, score in hits]
            return []
        # Hydration: SELECT u.id, u.segment_ids, ...
        if squashed.startswith("SELECT u.id AS unit_id"):
            ids = [int(a) for a in args]
            out: list[dict[str, Any]] = []
            for uid in ids:
                u = self.units.get(uid)
                if u is None:
                    continue
                out.append(
                    {
                        "unit_id": u.unit_id,
                        "segment_ids": json.dumps(u.segment_ids),
                        "start_sec": u.start_sec,
                        "end_sec": u.end_sec,
                        "text": u.text,
                        "video_id": u.video_id,
                        "transcript_id": u.transcript_id,
                    }
                )
            return out
        raise AssertionError(f"unexpected SQL: {squashed!r}")

    async def execute(self, sql: str, *args: Any) -> None:
        return None


@pytest.mark.asyncio
async def test_hybrid_search_returns_top_hit() -> None:
    db = FakeEngineDB()
    db.units[1] = _Unit(
        unit_id=1,
        transcript_id=10,
        video_id="00000000-0000-0000-0000-000000000001",
        text="The quick brown fox jumps over the lazy dog.",
        language="en",
        start_sec=0.0,
        end_sec=4.0,
        segment_ids=[101, 102],
    )
    db.units[2] = _Unit(
        unit_id=2,
        transcript_id=10,
        video_id="00000000-0000-0000-0000-000000000001",
        text="Unrelated passage about geometry and triangles.",
        language="en",
        start_sec=4.0,
        end_sec=8.0,
        segment_ids=[103],
    )
    # FTS returns unit 1 first for queries containing "fox".
    db.fts_hits["fox"] = [(1, 0.9), (2, 0.1)]

    embedder = StubEmbeddingService()
    chroma = make_in_memory_client()
    col = chroma.collection("lib-1")
    # Pre-embed both units as passages and write them so the vector
    # path has something to return.
    texts = [db.units[1].text, db.units[2].text]
    embs = embedder.embed_passages(texts)
    col.upsert(
        unit_ids=["1", "2"],
        embeddings=embs,
        metadatas=[
            {"unit_id": 1, "video_id": db.units[1].video_id, "language": "en"},
            {"unit_id": 2, "video_id": db.units[2].video_id, "language": "en"},
        ],
        documents=texts,
    )

    req = SearchRequest(query="fox", library_id="lib-1", mode="hybrid", limit=5)
    resp = await search(req, db=db, embedder=embedder, chroma=chroma)  # type: ignore[arg-type]

    assert resp.hits, "expected at least one hit"
    assert resp.hits[0].unit_id == 1
    assert resp.hits[0].segment_id == 101
    assert "<mark>" in resp.hits[0].snippet
    assert resp.metadata["mode"] == "hybrid"


@pytest.mark.asyncio
async def test_fts_only_mode_skips_vector_path() -> None:
    db = FakeEngineDB()
    db.units[1] = _Unit(
        unit_id=1,
        transcript_id=10,
        video_id="00000000-0000-0000-0000-000000000001",
        text="hello world",
        language="en",
        start_sec=0.0,
        end_sec=1.0,
        segment_ids=[1],
    )
    db.fts_hits["hello"] = [(1, 0.9)]

    embedder = StubEmbeddingService()
    chroma = make_in_memory_client()

    req = SearchRequest(query="hello", library_id="lib-1", mode="fts")
    resp = await search(req, db=db, embedder=embedder, chroma=chroma)  # type: ignore[arg-type]

    assert resp.metadata["mode"] == "fts"
    assert resp.metadata["vector_count"] == 0
    assert [h.unit_id for h in resp.hits] == [1]


@pytest.mark.asyncio
async def test_semantic_only_mode_skips_fts() -> None:
    db = FakeEngineDB()
    db.units[1] = _Unit(
        unit_id=1,
        transcript_id=10,
        video_id="00000000-0000-0000-0000-000000000001",
        text="only vector path",
        language="en",
        start_sec=0.0,
        end_sec=1.0,
        segment_ids=[1],
    )

    embedder = StubEmbeddingService()
    chroma = make_in_memory_client()
    col = chroma.collection("lib-1")
    col.upsert(
        unit_ids=["1"],
        embeddings=[embedder.embed_passages([db.units[1].text])[0]],
        metadatas=[{"unit_id": 1, "language": "en"}],
        documents=[db.units[1].text],
    )

    req = SearchRequest(query="only vector path", library_id="lib-1", mode="semantic")
    resp = await search(req, db=db, embedder=embedder, chroma=chroma)  # type: ignore[arg-type]

    assert resp.metadata["mode"] == "semantic"
    assert resp.metadata["fts_count"] == 0
    assert resp.hits[0].unit_id == 1
