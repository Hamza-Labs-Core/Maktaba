"""Where an installed pack's files are (Epic 52, story 52.16): components/packs.py."""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.components import packs


def _pack(root: Path, cid: str, current: str, make: bool = True) -> Path:
    d = root / cid
    d.mkdir(parents=True)
    if make:
        (d / current.strip()).mkdir()
    (d / "current").write_text(current, encoding="utf-8")
    return d


def test_a_good_pack(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _pack(tmp_path, "lang.hunspell.fr", "0.3.82_abcdef012345\n")
    assert (
        packs.pack_dir("lang.hunspell.fr", tmp_path)
        == tmp_path / "lang.hunspell.fr" / "0.3.82_abcdef012345"
    )
    monkeypatch.setenv(packs.ENV, str(tmp_path))
    assert packs.pack_dir("lang.hunspell.fr") is not None


@pytest.mark.parametrize("current", ["../x", ".hidden", "a/b", "", "one\ntwo\n"])
def test_a_bad_pointer_is_none(tmp_path: Path, current: str) -> None:
    d = tmp_path / "lang.ar"
    d.mkdir()
    (d / "current").write_text(current, encoding="utf-8")
    assert packs.pack_dir("lang.ar", tmp_path) is None


def test_missing_and_removing_are_none(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    _pack(tmp_path, "lang.ar", "v1_x", make=False)
    assert packs.pack_dir("lang.ar", tmp_path) is None
    d = _pack(tmp_path, "lang.en", "v1_y")
    (d / ".removing").write_text("", encoding="utf-8")
    assert packs.pack_dir("lang.en", tmp_path) is None
    assert packs.pack_dir("lang.none", tmp_path) is None
    monkeypatch.delenv(packs.ENV, raising=False)
    assert packs.pack_dir("lang.en") is None and packs.root() is None
