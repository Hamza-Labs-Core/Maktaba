"""The second listen's spelling dictionary (CAMeL Tools): a newer model.refine pack that
does not load keeps the dictionary in use (#637), and nothing is downloaded into a pack's
folder or while offline (#640)."""

from __future__ import annotations

import importlib.util
import os
import sys
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.stt import cohere

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
    monkeypatch.setattr(cohere.time, "monotonic", lambda: clock["t"])
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
