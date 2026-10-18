"""Path-map inference and the storage check, with temp dirs standing in for mounts."""

from __future__ import annotations

import os
from pathlib import Path

import pytest

from maktaba_pipeline.cluster import storage
from maktaba_pipeline.cluster.storage import (
    LibrarySample,
    candidate_roots,
    check_storage,
    infer_path_map,
    network_mounts,
)
from maktaba_pipeline.engine.paths import PathMap

pytestmark = pytest.mark.unit


def _file(path: Path, size: int) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(b"x" * size)


def _lib(videos: list[tuple[str, int]], roots: tuple[str, ...] = ()) -> LibrarySample:
    return LibrarySample(library_id="lib1", roots=roots, videos=tuple(videos))


def _slash(p: Path) -> str:
    return str(p).replace("\\", "/")


def test_macos_volumes_mount_of_a_windows_scanned_library(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    volumes = tmp_path / "Volumes"
    _file(volumes / "Y" / "Channel" / "a.mp4", 10)
    _file(volumes / "Y" / "Channel" / "b.mp4", 20)
    (volumes / "Other").mkdir()
    monkeypatch.setattr(storage, "MAC_VOLUMES", str(volumes))
    cands = candidate_roots(system="Darwin", env={}, home=tmp_path / "nohome")
    assert cands == [str(volumes / "Other"), str(volumes / "Y")]
    lib = _lib([("Y:/Channel/a.mp4", 10), ("Y:/Channel/b.mp4", 20)])
    assert infer_path_map([lib], cands) == {"Y:/": _slash(volumes / "Y") + "/"}


def test_macos_home_symlink_to_a_mount(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    if os.name == "nt":
        pytest.skip("symlinks need privileges on Windows")
    share = tmp_path / "nfs-export"
    _file(share / "Show" / "e1.mp4", 5)
    home = tmp_path / "home"
    home.mkdir()
    (home / "nas-nfs").symlink_to(share)
    (home / "plain").mkdir()
    monkeypatch.setattr(storage, "MAC_VOLUMES", str(tmp_path / "none"))
    cands = candidate_roots(system="Darwin", env={}, home=home)
    assert cands == [str(home / "nas-nfs")]
    assert infer_path_map([_lib([("/volume1/media/Show/e1.mp4", 5)])], cands) == {
        "/volume1/media/": _slash(home / "nas-nfs") + "/"
    }


PROC_MOUNTS = """\
/dev/sda1 / ext4 rw,relatime 0 0
proc /proc proc rw 0 0
nas:/volume1/media {mnt} nfs4 rw,vers=4.1 0 0
//nas/share /mnt/smb\\040share cifs rw 0 0
user@h:/x /home/u/sshfs fuse.sshfs rw 0 0
tmpfs /run tmpfs rw 0 0
"""


def test_linux_proc_mounts_parsing() -> None:
    assert network_mounts(PROC_MOUNTS.format(mnt="/mnt/nas")) == [
        "/mnt/nas",
        "/mnt/smb share",
        "/home/u/sshfs",
    ]


def test_linux_nfs_mount_found_through_proc_mounts(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    mnt = tmp_path / "nas"
    _file(mnt / "Channel" / "a.mp4", 7)
    _file(mnt / "Channel" / "b.mp4", 8)
    media_user = tmp_path / "media" / "u" / "usb"
    media_user.mkdir(parents=True)
    monkeypatch.setattr(storage, "LINUX_PARENTS", (str(tmp_path / "empty"),))
    monkeypatch.setattr(storage, "LINUX_MEDIA", str(tmp_path / "media"))
    cands = candidate_roots(system="Linux", env={}, proc_mounts=PROC_MOUNTS.format(mnt=_slash(mnt)))
    assert cands[0] == _slash(mnt) and str(media_user) in cands
    lib = _lib([("/Volumes/nas/Channel/a.mp4", 7), ("/Volumes/nas/Channel/b.mp4", 8)])
    assert infer_path_map([lib], cands) == {"/Volumes/nas/": _slash(mnt) + "/"}


def test_storage_hints_come_first(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(storage, "LINUX_PARENTS", ())
    monkeypatch.setattr(storage, "LINUX_MEDIA", str(tmp_path / "none"))
    hints = os.pathsep.join(["/a", "/b"])
    cands = candidate_roots(system="Linux", env={"MAKTABA_STORAGE_HINTS": hints}, proc_mounts="")
    assert cands == ["/a", "/b"]


def _fake_fs(files: dict[str, int]) -> storage.SizeOf:
    norm = {k.replace("\\", "/").lower(): v for k, v in files.items()}
    return lambda p: norm.get(p.replace("\\", "/").lower())


def test_windows_drive_letter(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(storage, "_drive_exists", lambda d: d in {"C", "Z"})
    cands = candidate_roots(system="Windows", env={}, net_use="")
    assert cands == ["C:/", "Z:/"]
    fs = _fake_fs({"Z:/Channel/a.mp4": 3, "Z:/Channel/b.mp4": 4})
    lib = _lib([("/Volumes/Y/Channel/a.mp4", 3), ("/Volumes/Y/Channel/b.mp4", 4)])
    assert infer_path_map([lib], cands, size_of=fs) == {"/Volumes/Y/": "Z:/"}


NET_USE = r"""
New connections will be remembered.

Status       Local     Remote                    Network
-------------------------------------------------------------------------------
OK                     \\nas\media               Microsoft Windows Network
OK           Y:        \\nas\y                   Microsoft Windows Network
The command completed successfully.
"""


def test_windows_unc_from_net_use_and_the_database_path(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(storage, "_drive_exists", lambda d: False)
    cands = candidate_roots(
        system="Windows", env={}, net_use=NET_USE, db_paths=["Y:/Show/a.mp4", "//nas/z/x.mp4"]
    )
    assert cands == ["//nas/media", "//nas/y", "Y:/", "//nas/z/"]
    fs = _fake_fs({"//nas/media/Show/a.mp4": 3, "//nas/media/Show/b.mp4": 4})
    lib = _lib([("/volume1/media/Show/a.mp4", 3), ("/volume1/media/Show/b.mp4", 4)])
    inferred = infer_path_map([lib], cands, size_of=fs)
    assert inferred == {"/volume1/media/": "//nas/media/"}
    # and the map round-trips through PathMap
    pm = PathMap.parse(str(inferred).replace("'", '"'))
    assert pm.to_db(pm.to_local("/volume1/media/Show/a.mp4")) == "/volume1/media/Show/a.mp4"


def test_needs_two_agreeing_samples_or_the_only_video() -> None:
    fs = _fake_fs({"/m/a.mp4": 1, "/m/b.mp4": 99})
    two = _lib([("Y:/a.mp4", 1), ("Y:/b.mp4", 2)])  # one size differs
    assert infer_path_map([two], ["/m"], size_of=fs) == {}
    only = _lib([("Y:/a.mp4", 1)])
    assert infer_path_map([only], ["/m"], size_of=fs) == {"Y:/": "/m/"}


def test_most_samples_win_and_reachable_libraries_need_nothing() -> None:
    fs = _fake_fs(
        {
            "/m1/x/a.mp4": 1,
            "/m1/x/b.mp4": 2,
            "/m1/x/c.mp4": 3,
            "/m2/a.mp4": 1,
            "/m2/b.mp4": 2,
            "/as/is/a.mp4": 5,
            "/as/is/b.mp4": 6,
        }
    )
    lib = _lib([("Y:/x/a.mp4", 1), ("Y:/x/b.mp4", 2), ("Y:/x/c.mp4", 3)])
    # /m2 would match a and b through the shorter suffix; the longer suffix is tried
    # first, and /m1 holds all three under it
    assert infer_path_map([lib], ["/m2", "/m1"], size_of=fs) == {"Y:/": "/m1/"}
    # samples split between two mounts: the one most samples agree on wins
    split = _fake_fs({"/p/a.mp4": 1, "/p/b.mp4": 2, "/q/c.mp4": 3})
    assert infer_path_map([lib], ["/p", "/q"], size_of=split) == {"Y:/x/": "/p/"}
    here = LibrarySample("lib2", (), (("/as/is/a.mp4", 5), ("/as/is/b.mp4", 6)))
    assert infer_path_map([here], ["/m1"], size_of=fs) == {}


def test_storage_check_read_and_write(tmp_path: Path) -> None:
    root = tmp_path / "share"
    _file(root / "a.mp4", 3)
    lib = LibrarySample("lib1", ("Y:/",), (("Y:/a.mp4", 3), ("Y:/gone.mp4", 9)))
    pm = PathMap.parse(f"Y:/={_slash(root)}")
    ok = check_storage([lib], pm, "6f1c2a3b")
    assert ok["read"] == "ok" and ok["write"] == "ok" and ok["detail"] is None
    assert ok["checked_at"].endswith("Z")
    assert sorted(p.name for p in root.iterdir()) == ["a.mp4"]  # the probe file is gone

    bad = check_storage([lib], PathMap.parse(f"Y:/={_slash(tmp_path / 'missing')}"), "6f1c2a3b")
    assert bad["read"] == "error" and bad["write"] == "error"
    assert "lib1" in bad["detail"]
    none = check_storage([LibrarySample("e", (), ())], pm, "x")
    assert none["read"] == "ok" and none["write"] == "skipped"
