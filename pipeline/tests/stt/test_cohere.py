"""The second listen's spelling dictionary (CAMeL Tools): a newer model.refine pack that
does not load keeps the dictionary in use (#637), and nothing is downloaded into a pack's
folder or while offline (#640)."""

from __future__ import annotations

import importlib.util
import os
import sys
import time
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.stt import cohere, engine
from maktaba_pipeline.stt.settings import WhisperSettings

pytestmark = pytest.mark.unit


def _current(root: Path, pack: str, version: str) -> None:
    (root / pack / "current").write_text(version + "\n", encoding="utf-8")


@pytest.fixture
def components(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    root = tmp_path / "components"
    root.mkdir()
    monkeypatch.setenv("MAKTABA_COMPONENTS_DIR", str(root))
    return root


def test_a_dictionary_that_fails_to_load_keeps_the_one_in_use(
    components: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    """A newer model.refine pack whose dictionary does not load (the old version's folder
    already deleted, a half-placed install) keeps the working dictionary, is tried again
    later, and is never downloaded into: a pack's folder is the install's alone."""
    for v in ("r13-1", "r13-2"):
        (components / "model.refine" / v / "camel_tools").mkdir(parents=True)
    _current(components, "model.refine", "r13-1")
    v1 = components / "model.refine" / "r13-1" / "camel_tools"
    monkeypatch.setenv("CAMELTOOLS_DATA", str(v1))
    fail = {"on": False}
    built: list[str] = []

    def fake(data_dir: str) -> Any:
        built.append(data_dir)
        if fail["on"]:
            raise FileNotFoundError(data_dir)
        return object()

    downloads: list[str] = []
    catalogue = type(
        "Catalogue", (), {"download_package": staticmethod(lambda name: downloads.append(name))}
    )
    data_mod = type(sys)("camel_tools.data")
    data_mod.CATALOGUE = catalogue  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "camel_tools", type(sys)("camel_tools"))
    monkeypatch.setitem(sys.modules, "camel_tools.data", data_mod)
    monkeypatch.setattr(cohere, "_new_dictionary", fake)
    monkeypatch.setattr(cohere, "_DICT", [])
    monkeypatch.setattr(cohere, "_DICT_FAILED", [], raising=False)
    monkeypatch.setattr(importlib.util, "find_spec", lambda _n: object())
    clock = {"t": 1000.0}
    monkeypatch.setattr("maktaba_pipeline.stt.cohere.time.monotonic", lambda: clock["t"])
    first = cohere.dictionary()
    assert first is not None

    fail["on"] = True
    _current(components, "model.refine", "r13-2")
    assert cohere.dictionary() is first  # the working one is kept
    assert downloads == []  # never downloaded into a pack's folder
    tries = len(built)
    assert cohere.dictionary() is first and len(built) == tries  # not at every call
    assert Path(os.environ["CAMELTOOLS_DATA"]) == v1

    fail["on"] = False
    clock["t"] += cohere.DICT_RETRY_S + 1  # tried again later: it loads now
    second = cohere.dictionary()
    assert second is not first and len(built) == tries + 1


def test_the_dictionary_is_not_downloaded_while_offline(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("MAKTABA_COMPONENTS_DIR", raising=False)
    monkeypatch.setenv("CAMELTOOLS_DATA", str(tmp_path / "camel_tools"))
    monkeypatch.setenv("MAKTABA_OFFLINE", "1")
    downloads: list[str] = []
    catalogue = type(
        "Catalogue", (), {"download_package": staticmethod(lambda name: downloads.append(name))}
    )
    data_mod = type(sys)("camel_tools.data")
    data_mod.CATALOGUE = catalogue  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "camel_tools", type(sys)("camel_tools"))
    monkeypatch.setitem(sys.modules, "camel_tools.data", data_mod)

    def missing(data_dir: str) -> Any:
        raise FileNotFoundError(data_dir)

    monkeypatch.setattr(cohere, "_new_dictionary", missing)
    monkeypatch.setattr(cohere, "_DICT", [])
    monkeypatch.setattr(cohere, "_DICT_FAILED", [], raising=False)
    monkeypatch.setattr(importlib.util, "find_spec", lambda _n: object())
    assert cohere.dictionary() is None
    assert downloads == []
    monkeypatch.setenv("MAKTABA_OFFLINE", "")
    monkeypatch.setattr(cohere, "_DICT", [])
    cohere.dictionary()
    assert downloads == ["morphology-db-msa-r13"]  # online, outside a pack: fetched once


# --- memory: an idle second listen lets its model go (stt/mlx_memory.py) ----------------

MODEL = "CohereLabs/cohere-transcribe-arabic-07-2026"


@pytest.fixture
def fake_cohere(monkeypatch: pytest.MonkeyPatch) -> dict[str, Any]:
    """The second listen loads a stand-in model, and MLX is a recorder."""
    state: dict[str, Any] = {"loads": 0, "limits": [], "cleared": 0}
    real_find = importlib.util.find_spec
    monkeypatch.setattr(
        importlib.util,
        "find_spec",
        lambda name, *a: object() if name == "mlx_audio" else real_find(name, *a),
    )
    monkeypatch.setattr(engine, "current", lambda: engine.MLX)

    class FakeMx:
        def set_memory_limit(self, n: int) -> None:
            state["limits"].append(("memory", n))

        def set_cache_limit(self, n: int) -> None:
            state["limits"].append(("cache", n))

        def get_cache_memory(self) -> int:
            return 0

        def get_active_memory(self) -> int:
            return 0

        def clear_cache(self) -> None:
            state["cleared"] += 1

    monkeypatch.setattr(cohere, "_mlx", lambda: FakeMx())

    def load(self: cohere.CohereRecogniser) -> Any:
        state["loads"] += 1
        return object()

    monkeypatch.setattr(cohere.CohereRecogniser, "_load", load)
    monkeypatch.setattr(cohere.CohereRecogniser, "_hear", lambda self, st: ["x" for _ in st])
    monkeypatch.setattr(cohere, "_REAPER", [])
    return state


def test_the_second_listen_sets_mlx_s_limits_when_it_loads_first(
    fake_cohere: dict[str, Any],
) -> None:
    # an ml runner whose first job is a second listen: MLX's cache was unbounded until
    # Whisper loaded
    s = WhisperSettings(gpu_limit_gb=20, gpu_cache_gb=1.5)
    cohere.recogniser(MODEL, None, s)
    assert fake_cohere["limits"] == [("memory", 20_000_000_000), ("cache", 1_500_000_000)]


def test_an_idle_second_listen_lets_its_model_go_and_loads_it_again(
    fake_cohere: dict[str, Any],
) -> None:
    s = WhisperSettings(unload_idle_s=600)
    rec = cohere.recogniser(MODEL, None, s)
    assert rec(["a", "b"]) == ["x", "x"]
    # not yet idle long enough
    assert cohere.reap_idle(rec.last_used + 599, s) == []
    assert cohere.reap_idle(rec.last_used + 601, s) == [MODEL]
    assert rec._model is None and rec.unloads == 1
    assert fake_cohere["cleared"] == 1  # MLX's freed buffers given back
    # a caller still holding it hears on: the model is loaded again for the call
    assert rec(["a"]) == ["x"]
    assert fake_cohere["loads"] == 2 and rec._model is not None
    assert cohere.recogniser(MODEL, None, s) is rec


def test_a_second_listen_in_use_or_kept_is_not_let_go(fake_cohere: dict[str, Any]) -> None:
    s = WhisperSettings(unload_idle_s=600)
    rec = cohere.recogniser(MODEL, None, s)
    rec._users = 1  # a call hearing now
    assert cohere.reap_idle(rec.last_used + 10_000, s) == []
    rec._users = 0
    # handed out again: idle from now
    before = rec.last_used
    time.sleep(0.01)
    assert cohere.recogniser(MODEL, None, s) is rec and rec.last_used > before
    # unloading off: kept however long
    off = WhisperSettings(unload_idle_s=0)
    assert cohere.reap_idle(rec.last_used + 10_000, off) == []
    assert rec._model is not None and fake_cohere["loads"] == 1
