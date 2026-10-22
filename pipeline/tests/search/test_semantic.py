"""The semantic index: model / store configuration, the e5 embedder, replace and search."""

from __future__ import annotations

import types
import uuid
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.search.embedder import embed_id_for
from maktaba_pipeline.search.semantic import (
    DEFAULT_EMBED_MODEL,
    E5Embedder,
    IndexedSegment,
    SemanticIndex,
    chroma_dir,
    collection_name,
    embed_model,
    parse_hits,
    pick_device,
    search_available,
)
from tests.search.semantic_fakes import FakeCollection, FakeEmbedder, FakeModel

pytestmark = pytest.mark.unit


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


# --------------------------------------------------------------------------- configuration


def test_model_and_store_come_from_the_environment(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.delenv("MAKTABA_EMBED_MODEL", raising=False)
    monkeypatch.delenv("MAKTABA_CHROMA_DIR", raising=False)
    assert embed_model() == DEFAULT_EMBED_MODEL == "intfloat/multilingual-e5-small"
    assert chroma_dir() == str(Path("~/.maktaba/chroma").expanduser())
    monkeypatch.setenv("MAKTABA_EMBED_MODEL", "intfloat/multilingual-e5-base")
    monkeypatch.setenv("MAKTABA_CHROMA_DIR", str(tmp_path / "vec"))
    assert embed_model() == "intfloat/multilingual-e5-base"
    assert chroma_dir() == str(tmp_path / "vec")


def test_each_model_gets_its_own_valid_collection(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_CHROMA_COLLECTION", raising=False)
    small = collection_name("intfloat/multilingual-e5-small")
    assert small == "maktaba_segments-intfloat-multilingual-e5-small"
    assert small != collection_name("intfloat/multilingual-e5-base")
    assert collection_name("x/y", prefix="p") == "p-x-y"


def test_search_available_finds_the_extra_without_importing_it(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import importlib.util
    import sys

    found: dict[str, object] = {"chromadb": object(), "sentence_transformers": object()}
    monkeypatch.setattr(importlib.util, "find_spec", lambda name: found.get(name))
    for name in found:
        monkeypatch.delitem(sys.modules, name, raising=False)
    assert search_available() is True
    assert "chromadb" not in sys.modules and "sentence_transformers" not in sys.modules
    found.pop("sentence_transformers")
    assert search_available() is False


def test_device_is_apple_s_gpu_else_the_cpu(monkeypatch: pytest.MonkeyPatch) -> None:
    """Not an NVIDIA GPU of its own accord (review of #209): Whisper and the second listen
    are sized to fill it, without the embedder's 0.5-1 GB."""
    monkeypatch.delenv("MAKTABA_EMBED_DEVICE", raising=False)

    def fake_torch(cuda: bool, mps: bool) -> Any:
        return types.SimpleNamespace(
            cuda=types.SimpleNamespace(is_available=lambda: cuda),
            backends=types.SimpleNamespace(mps=types.SimpleNamespace(is_available=lambda: mps)),
        )

    assert pick_device(fake_torch(True, False)) == "cpu"
    assert pick_device(fake_torch(False, True)) == "mps"
    assert pick_device(fake_torch(False, False)) == "cpu"
    monkeypatch.setenv("MAKTABA_EMBED_DEVICE", "cuda")  # asked for: the operator's
    assert pick_device(fake_torch(True, False)) == "cuda"
    monkeypatch.setenv("MAKTABA_EMBED_DEVICE", "cpu")
    assert pick_device(fake_torch(False, True)) == "cpu"


# --------------------------------------------------------------------------- embedder


def test_e5_embedder_prefixes_batches_and_loads_once() -> None:
    model = FakeModel()
    loads: list[tuple[str, str]] = []

    def loader(name: str, device: str) -> FakeModel:
        loads.append((name, device))
        return model

    emb = E5Embedder("intfloat/multilingual-e5-small", device="mps", batch=16, loader=loader)
    vecs = emb.embed_passages(["بسم الله", "الحمد لله"])
    q = emb.embed_query("الرحمن")
    assert loads == [("intfloat/multilingual-e5-small", "mps")]
    assert model.calls[0] == (["passage: بسم الله", "passage: الحمد لله"], 16)
    assert model.calls[1] == (["query: الرحمن"], 16)
    assert len(vecs) == 2 and len(q) == len(vecs[0])
    assert emb.embed_passages([]) == []


# --------------------------------------------------------------------------- index


def test_replace_video_drops_old_vectors_and_writes_in_batches() -> None:
    coll, emb = FakeCollection(), FakeEmbedder()
    idx = SemanticIndex(coll, emb)
    vid, lib = uuid.uuid4(), uuid.uuid4()
    old, new = uuid.uuid4(), uuid.uuid4()
    other = uuid.uuid4()
    idx.replace_video(video_id=other, library_id=lib, segments=_segments(uuid.uuid4(), ["x"]))
    idx.replace_video(video_id=vid, library_id=lib, segments=_segments(old, ["a", "b", "c"]))
    n = idx.replace_video(
        video_id=vid,
        library_id=lib,
        segments=_segments(new, ["one", "  ", "two", "three", "four", "five"]),
        batch=2,
    )
    assert n == 5  # the blank segment is skipped
    mine = [m for m in coll.metas() if m["video_id"] == str(vid)]
    assert {m["transcript_id"] for m in mine} == {str(new)}  # nothing left of the old one
    assert len(mine) == 5
    assert any(m["video_id"] == str(other) for m in coll.metas())  # other videos untouched
    assert embed_id_for(new, 0) in coll.rows
    meta = coll.rows[embed_id_for(new, 2)][1]
    assert meta == {
        "video_id": str(vid),
        "library_id": str(lib),
        "language": "",
        "transcript_id": str(new),
        "segment_id": 102,
        "seq": 2,
        "start_sec": 10.0,
        "end_sec": 15.0,
    }
    assert [len(b) for b in emb.passages[-3:]] == [1, 2, 2]  # batches of 2, blanks dropped


def test_search_ranks_by_similarity_and_filters_by_library() -> None:
    coll, emb = FakeCollection(), FakeEmbedder()
    idx = SemanticIndex(coll, emb)
    lib_a, lib_b = uuid.uuid4(), uuid.uuid4()
    va, vb = uuid.uuid4(), uuid.uuid4()
    idx.replace_video(
        video_id=va,
        library_id=lib_a,
        segments=_segments(uuid.uuid4(), ["صلاة الفجر", "زكاة المال"]),
    )
    idx.replace_video(
        video_id=vb, library_id=lib_b, segments=_segments(uuid.uuid4(), ["صلاة الفجر"])
    )
    hits = idx.search("صلاة الفجر", k=10)
    assert len(hits) == 3 and hits[0].score == pytest.approx(1.0)
    assert hits[-1].segment_id == 101  # the unrelated segment ranks last
    only_a = idx.search("صلاة الفجر", k=10, library_ids=[str(lib_a)])
    assert {h.video_id for h in only_a} == {str(va)}
    both = idx.search("صلاة الفجر", k=10, library_ids=[str(lib_a), str(lib_b)])
    assert len(both) == 3
    top = idx.search("صلاة الفجر", k=1)
    assert len(top) == 1 and top[0].start_sec == 0.0 and top[0].end_sec == 5.0
    assert idx.search("   ", k=10) == [] and idx.search("x", k=0) == []
    assert emb.queries[0] == "صلاة الفجر"


def test_parse_hits_skips_rows_without_metadata() -> None:
    res = {
        "ids": [["a", "b"]],
        "distances": [[0.25, 0.5]],
        "metadatas": [
            [{"segment_id": 7, "video_id": "v", "start_sec": 1, "end_sec": 2}, {"video_id": "v"}]
        ],
        "documents": [["text", None]],
    }
    hits = parse_hits(res)
    assert len(hits) == 1
    assert hits[0].to_json() == {
        "segment_id": 7,
        "video_id": "v",
        "start_sec": 1.0,
        "end_sec": 2.0,
        "score": 0.75,
        "text": "text",
    }
    assert parse_hits({}) == []
