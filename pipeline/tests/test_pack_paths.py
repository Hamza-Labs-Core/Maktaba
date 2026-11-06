"""#637: a running pipeline follows a pack's ``current`` pointer. A tool.ffmpeg or
model.refine pack installed or updated after the pipeline started is used at the next
call, with no restart."""

from __future__ import annotations

import importlib.util
import os
import stat
import sys
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline import binaries, pack_paths
from maktaba_pipeline.engine import media
from maktaba_pipeline.stt import cohere


def _program(path: Path) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text("#!/bin/sh\n", encoding="utf-8")
    path.chmod(path.stat().st_mode | stat.S_IXUSR)
    return path


def _ffmpeg_pack(root: Path, version: str) -> Path:
    name = "ffmpeg.exe" if sys.platform == "win32" else "ffmpeg"
    return _program(root / "tool.ffmpeg" / version / "ffmpeg" / name)


def _current(root: Path, pack: str, version: str) -> None:
    (root / pack / "current").write_text(version + "\n", encoding="utf-8")


@pytest.fixture
def components(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    root = tmp_path / "components"
    root.mkdir()
    monkeypatch.setenv("MAKTABA_COMPONENTS_DIR", str(root))
    return root


def test_follow_moves_a_path_to_the_current_version(components: Path) -> None:
    v1, v2 = _ffmpeg_pack(components, "7.1-1"), _ffmpeg_pack(components, "7.1-2")
    _current(components, "tool.ffmpeg", "7.1-1")
    assert pack_paths.follow(str(v1)) == str(v1)
    _current(components, "tool.ffmpeg", "7.1-2")
    assert Path(pack_paths.follow(str(v1))) == v2.resolve()
    assert pack_paths.current_dir("tool.ffmpeg") == components / "tool.ffmpeg" / "7.1-2"
    # outside the components folder, a missing place in the new version, a bad pointer
    assert pack_paths.follow("/usr/bin/ffmpeg") == "/usr/bin/ffmpeg"
    gone = components / "tool.ffmpeg" / "7.1-1" / "ffmpeg" / "extra"
    assert pack_paths.follow(str(gone)) == str(gone)
    _current(components, "tool.ffmpeg", "../elsewhere")
    assert pack_paths.follow(str(v1)) == str(v1)
    assert pack_paths.current_dir("tool.ffmpeg") is None


def test_follow_without_a_components_folder(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("MAKTABA_COMPONENTS_DIR", raising=False)
    assert pack_paths.follow("/x/tool.ffmpeg/1/ffmpeg/ffmpeg") == "/x/tool.ffmpeg/1/ffmpeg/ffmpeg"


def test_the_ffmpeg_the_supervisor_named_follows_an_updated_pack(
    components: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    v1, v2 = _ffmpeg_pack(components, "7.1-1"), _ffmpeg_pack(components, "7.1-2")
    _current(components, "tool.ffmpeg", "7.1-1")
    # what the supervisor passed when the pipeline started: the pack's version then
    monkeypatch.setenv("MAKTABA_FFMPEG", str(v1))
    assert binaries.ffmpeg_binary() == str(v1)
    assert str(v1) == media.FFMPEG and media.ffmpeg() == str(v1)
    _current(components, "tool.ffmpeg", "7.1-2")  # the pack updated while it runs
    assert Path(binaries.ffmpeg_binary()) == v2.resolve()
    assert Path(media.FFMPEG) == v2.resolve()
    # an operator's own ffmpeg outside the packs is kept
    own = _program(components.parent / "own" / "ffmpeg")
    monkeypatch.setenv("MAKTABA_FFMPEG", str(own))
    assert binaries.ffmpeg_binary() == str(own)


def test_a_pack_installed_after_the_start_is_found(
    components: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.delenv("MAKTABA_FFMPEG", raising=False)
    monkeypatch.setenv("PATH", "")
    assert media.ffmpeg() == "ffmpeg"
    v1 = _ffmpeg_pack(components, "7.1-1")
    _current(components, "tool.ffmpeg", "7.1-1")
    assert media.ffmpeg() == str(v1)


def test_the_dictionary_is_read_again_when_its_pack_moves(
    components: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    for v in ("r13-1", "r13-2"):
        (components / "model.refine" / v / "camel_tools").mkdir(parents=True)
    _current(components, "model.refine", "r13-1")
    monkeypatch.setenv(
        "CAMELTOOLS_DATA", str(components / "model.refine" / "r13-1" / "camel_tools")
    )
    built: list[str] = []

    def fake(data_dir: str) -> Any:
        built.append(data_dir)
        return object()

    monkeypatch.setattr(cohere, "_new_dictionary", fake)
    monkeypatch.setattr(cohere, "_DICT", [])
    monkeypatch.setattr(importlib.util, "find_spec", lambda _n: object())
    first = cohere.dictionary()
    assert cohere.dictionary() is first and len(built) == 1
    _current(components, "model.refine", "r13-2")  # a newer dictionary installed
    second = cohere.dictionary()
    assert second is not first and len(built) == 2
    assert Path(built[1]) == (components / "model.refine" / "r13-2" / "camel_tools").resolve()
    assert Path(os.environ["CAMELTOOLS_DATA"]) == Path(built[1])
    assert cohere.dictionary() is second


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


def test_inside_a_pack(components: Path) -> None:
    assert pack_paths.inside(str(components / "model.refine" / "r13-1" / "camel_tools"))
    assert not pack_paths.inside(str(components / "model.refine"))
    assert not pack_paths.inside(str(components.parent / "models" / "camel_tools"))
