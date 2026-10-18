"""The semantic index against a real persistent Chroma in a temp directory.

Runs only when the optional ``search`` extra is installed; Chroma's startup is slower
than the unit tier's per-test cap, so this is not ``unit``-marked.
"""

from __future__ import annotations

import uuid
from pathlib import Path

import pytest

from maktaba_pipeline.search import semantic
from maktaba_pipeline.search.semantic import IndexedSegment, SemanticIndex
from tests.search.semantic_fakes import FakeEmbedder


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


def test_real_chroma_round_trip(tmp_path: Path) -> None:
    pytest.importorskip("chromadb")
    coll = semantic.open_collection("fake/e5", str(tmp_path / "chroma"))
    idx = SemanticIndex(coll, FakeEmbedder())
    vid, lib, tid = uuid.uuid4(), uuid.uuid4(), uuid.uuid4()
    idx.replace_video(video_id=vid, library_id=lib, segments=_segments(tid, ["a b", "c d"]))
    idx.replace_video(video_id=vid, library_id=lib, segments=_segments(tid, ["a b", "c d"]))
    hits = idx.search("a b", k=5, library_ids=[str(lib)])
    assert [h.segment_id for h in hits] == [100, 101]
    assert hits[0].score == pytest.approx(1.0, abs=1e-4)
    assert idx.search("a b", k=5, library_ids=[str(uuid.uuid4())]) == []
    idx.delete_video(vid)
    assert idx.search("a b", k=5) == []
