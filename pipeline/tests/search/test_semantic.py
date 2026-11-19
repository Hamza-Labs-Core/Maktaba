"""The semantic index: the model configuration and the e5 embedder (the store is
Postgres: tests/search/test_pgstore.py)."""

from __future__ import annotations

import types
from typing import Any

import pytest

from maktaba_pipeline.search import semantic
from maktaba_pipeline.search.semantic import (
    DEFAULT_EMBED_MODEL,
    E5Embedder,
    embed_model,
    pick_device,
    search_available,
)
from tests.search.semantic_fakes import FakeModel

pytestmark = pytest.mark.unit


# --------------------------------------------------------------------------- configuration


def test_the_model_comes_from_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_EMBED_MODEL", raising=False)
    assert embed_model() == DEFAULT_EMBED_MODEL == "intfloat/multilingual-e5-small"
    monkeypatch.setenv("MAKTABA_EMBED_MODEL", "intfloat/multilingual-e5-base")
    assert embed_model() == "intfloat/multilingual-e5-base"


def test_chroma_is_retired() -> None:
    """Story 43.5: nothing here opens Chroma any more, nor names its folder."""
    for gone in ("SemanticIndex", "open_collection", "close_clients", "chroma_dir"):
        assert not hasattr(semantic, gone), gone


def test_search_available_finds_the_extra_without_importing_it(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    import importlib.util
    import sys

    found: dict[str, object] = {"sentence_transformers": object()}
    monkeypatch.setattr(importlib.util, "find_spec", lambda name: found.get(name))
    for name in found:
        monkeypatch.delitem(sys.modules, name, raising=False)
    assert search_available() is True
    assert "sentence_transformers" not in sys.modules
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
