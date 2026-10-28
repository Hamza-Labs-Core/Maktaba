"""Epic 47.12: reading a Tartube database without Tartube, and without running its pickle.

Every fixture is synthetic (built here); the home's real ``tartube.db`` is never opened.
"""

from __future__ import annotations

import json
import os
import pickle
import subprocess
import sys
import types
from collections.abc import Iterator
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.library_mgmt.tartube_import import (
    StubUnpickler,
    TartubeRefused,
    downloaded_ids,
    load_pickle,
    read_export,
    records_from_pickle,
)

A, B, C, D = "AAAAAAAAAAA", "BBBBBBBBBBB", "CCCCCCCCCCC", "DDDDDDDDDDD"


@pytest.fixture
def media_module() -> Iterator[types.ModuleType]:
    """A stand-in for Tartube's ``media`` module, there only while the fixture pickles."""
    mod = types.ModuleType("media")

    class Channel:
        pass

    class Folder:
        pass

    class Video:
        pass

    for cls in (Channel, Folder, Video):
        cls.__module__ = "media"
        cls.__qualname__ = cls.__name__
        setattr(mod, cls.__name__, cls)
    sys.modules["media"] = mod
    yield mod
    del sys.modules["media"]


def _obj(cls: Any, **attrs: Any) -> Any:
    o = cls.__new__(cls)
    o.__dict__.update(attrs)
    return o


def make_db(path: Path, media: types.ModuleType, folder: Path) -> Path:
    chan = _obj(media.Channel, dbid=1, name="Chan", source="https://www.youtube.com/@chan")
    other = _obj(media.Channel, dbid=5, name="Other", source="https://www.youtube.com/@other")
    sub = _obj(media.Folder, dbid=6, name="Sub", parent_obj=other)
    reg = {
        1: chan,
        5: other,
        6: sub,
        2: _obj(media.Video, dbid=2, vid=A, dl_flag=True, file_dir=str(folder),
                file_name="a", file_ext=".mp4", parent_obj=chan),
        3: _obj(media.Video, dbid=3, vid=B, dl_flag=False, file_dir=str(folder),
                file_name="b", file_ext=".mp4", parent_obj=chan),
        4: _obj(media.Video, dbid=4, vid=C, dl_flag=True, file_dir=str(folder),
                file_name="c-missing", file_ext=".mp4", parent_obj=chan),
        7: _obj(media.Video, dbid=7, vid=D, dl_flag=True, file_dir="/nowhere/at/all",
                file_name="d", file_ext=".mkv", parent_obj=sub),
    }  # fmt: skip
    root = {"media_reg_dict": reg, "db_version": 2005000, "options": {"x": {1, 2}}}
    path.write_bytes(pickle.dumps(root, protocol=4))
    return path


def make_export(path: Path) -> Path:
    """Tartube's own export: nested, and without ``dl_flag``."""
    doc = {
        "script_name": "Tartube",
        "file_type": "db_export",
        "db_dict": {
            "1": {
                "type": "channel", "dbid": 1, "name": "Chan",
                "source": "https://www.youtube.com/@chan",
                "db_dict": {
                    str(n): {"type": "video", "dbid": n, "vid": v, "name": v, "file": f}
                    for n, v, f in ((2, A, "a.mp4"), (3, B, "b.mp4"), (4, C, "c-missing.mp4"))
                },
            },
            "5": {
                "type": "channel", "dbid": 5, "name": "Other",
                "source": "https://www.youtube.com/@other",
                "db_dict": {"6": {"type": "folder", "dbid": 6, "db_dict": {
                    "7": {"type": "video", "dbid": 7, "vid": D, "file": "d.mkv"}}}},
            },
        },
    }  # fmt: skip
    path.write_text(json.dumps(doc))
    return path


@pytest.fixture
def library(tmp_path: Path) -> Path:
    folder = tmp_path / "Chan"
    folder.mkdir()
    (folder / "a.mp4").write_bytes(b"a")
    (folder / "b.mp4").write_bytes(b"b")
    return folder


def test_the_database_loads_with_stubs_only(
    tmp_path: Path, media_module: types.ModuleType, library: Path
) -> None:
    db = make_db(tmp_path / "tartube.db", media_module, library)
    del sys.modules["media"]  # Tartube's code is not importable from here on
    try:
        root = load_pickle(str(db))
    finally:
        sys.modules["media"] = media_module
    video = root["media_reg_dict"][2]
    assert type(video).__module__ == "tartube_stub.media" and type(video).__name__ == "Video"
    assert root["options"] == {"x": {1, 2}}
    recs = {r.vid: r for r in records_from_pickle(root)}
    assert set(recs) == {A, B, C, D}
    assert recs[A].dl_flag is True and recs[B].dl_flag is False
    assert recs[D].parent == ("Other", "https://www.youtube.com/@other")  # through the folder


def test_ids_from_the_database_need_dl_flag_and_the_file(
    tmp_path: Path, media_module: types.ModuleType, library: Path
) -> None:
    db = make_db(tmp_path / "tartube.db", media_module, library)
    ids, rep = downloaded_ids(str(db))
    # A: there; B: not downloaded; C: its folder is here, the file is not; D: a folder
    # this machine does not have, taken on dl_flag's word
    assert sorted(ids) == [A, D]
    assert (rep.verified, rep.missing, rep.not_downloaded, rep.unverified) == (1, 1, 1, 1)
    assert rep.used_pickle and "media.Video" in rep.stubbed


def test_the_export_first_and_the_database_for_what_it_lacks(
    tmp_path: Path, media_module: types.ModuleType, library: Path
) -> None:
    make_db(tmp_path / "tartube.db", media_module, library)
    export = make_export(tmp_path / "tartube_db_export.json")
    recs = read_export(str(export))
    assert {r.vid for r in recs} == {A, B, C, D} and all(r.dl_flag is None for r in recs)
    ids, rep = downloaded_ids(str(export))
    assert sorted(ids) == [A, D] and rep.used_pickle
    only, rep2 = downloaded_ids(str(export), channels=["Chan"])
    assert only == [A] and rep2.filtered == 1


def test_an_export_alone_says_unknown_without_the_database(tmp_path: Path) -> None:
    export = make_export(tmp_path / "export.json")
    ids, rep = downloaded_ids(str(export))
    assert ids == [] and rep.unknown == 4 and not rep.used_pickle


def test_a_make_it_offline_export(tmp_path: Path, library: Path) -> None:
    doc = {
        "version": "1.0",
        "source": "tartube",
        "channels": [{"dbid": 1, "name": "Chan", "url": "https://www.youtube.com/@chan"}],
        "videos": [
            {"dbid": 2, "videoId": A, "channelDbid": 1, "isDownloaded": True,
             "fileName": str(library / "a.mp4")},
            {"dbid": 3, "videoId": B, "channelDbid": 1, "isDownloaded": False},
            {"dbid": 4, "videoId": "short", "isDownloaded": True},
        ],
    }  # fmt: skip
    p = tmp_path / "mio.json"
    p.write_text(json.dumps(doc))
    ids, rep = downloaded_ids(str(p))
    assert ids == [A] and rep.verified == 1 and rep.by_parent == {"Chan": 1}


class _System:
    def __reduce__(self) -> Any:
        return (os.system, ("touch PWNED",))


class _Eval:
    def __reduce__(self) -> Any:
        return (eval, ("open('PWNED', 'w')",))


class _Popen:
    def __reduce__(self) -> Any:
        return (subprocess.Popen, (["touch", "PWNED"],))


class _Exec:
    def __reduce__(self) -> Any:
        return (exec, ("open('PWNED', 'w')",))


@pytest.mark.parametrize("payload", [_System(), _Eval(), _Popen(), _Exec()])
def test_a_malicious_pickle_runs_nothing(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, payload: Any
) -> None:
    monkeypatch.chdir(tmp_path)
    evil = tmp_path / "tartube.db"
    evil.write_bytes(pickle.dumps({"media_reg_dict": {1: payload}}, protocol=2))
    with pytest.raises(TartubeRefused):
        load_pickle(str(evil))
    with pytest.raises(TartubeRefused):
        downloaded_ids(str(evil))
    assert not (tmp_path / "PWNED").exists()


def test_an_unknown_global_is_only_an_inert_stub(tmp_path: Path) -> None:
    # a callable from a module nobody listed: called by the pickle, it builds a stub
    data = b"\x80\x02csomepkg.danger\nrun\nq\x00X\x05\x00\x00\x00rm -rq\x01\x85q\x02Rq\x03."
    got = StubUnpickler(__import__("io").BytesIO(data)).load()
    assert type(got).__name__ == "run" and got._args == ("rm -r",)
    # dunder names and refused modules never resolve
    with pytest.raises(TartubeRefused):
        StubUnpickler(__import__("io").BytesIO(b"\x80\x02cmedia\n__class__\n.")).load()
