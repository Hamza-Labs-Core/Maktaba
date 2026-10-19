"""Canonical <-> local per OS, from recorded mount tables (story 31.3)."""

from __future__ import annotations

import unicodedata
from pathlib import Path

import pytest

from maktaba_pipeline.engine.paths import PathMap
from maktaba_pipeline.sharepath.mounts import (
    Mount,
    parse_mac_mount,
    parse_net_use,
    parse_proc_mounts,
)
from maktaba_pipeline.sharepath.resolver import (
    StorageServer,
    build_resolver,
    match_server,
    share_of,
)

pytestmark = pytest.mark.unit

DATA = Path(__file__).resolve().parents[3] / "shared" / "testdata" / "sharepath"
NAS = StorageServer(
    name="home-nas", addresses=("192.168.50.94", "Home-NAS", "home-nas.local"), id="s1"
)
VIDEO = "smb://home-nas/Originals/Backup/Youtube/Chan/a.mp4"


def _mounts(name: str) -> str:
    return (DATA / name).read_text(encoding="utf-8")


def _never(_: str) -> bool:
    return False


def test_match_server_by_any_address_case_insensitive() -> None:
    assert match_server([NAS], "HOME-NAS") is NAS
    assert match_server([NAS], "192.168.50.94") is NAS
    assert match_server([NAS], "10.0.0.9") is None
    # an alias resolving to the same IP as a known address
    ips = {"nas2.lan": frozenset({"192.168.50.94"}), "192.168.50.94": frozenset({"192.168.50.94"})}
    assert match_server([NAS], "nas2.lan", resolve=lambda h: ips.get(h, frozenset())) is NAS


def test_share_of_nfs_exports() -> None:
    nfs = Mount("nfs", "192.168.50.94", "/volume1/Originals/Backup", "/srv/o/", "proc")
    assert share_of(NAS, nfs) == ("smb", "Originals/Backup")
    odd = Mount("nfs", "192.168.50.94", "/export/media", "/srv/m/", "proc")
    assert share_of(NAS, odd) == ("nfs", "export/media")
    mapped = StorageServer(name="home-nas", exports=(("/export/media", "Media"),))
    assert share_of(mapped, odd) == ("smb", "Media")


def test_windows_drives_and_unc_fallback() -> None:
    r = build_resolver(
        servers=[NAS],
        mounts=parse_net_use(_mounts("net_use_windows.txt")),
        system="Windows",
        exists=_never,
    )
    # the longest share prefix wins: Y: is Originals/Backup/Youtube
    assert r.to_local(VIDEO) == "Y:/Chan/a.mp4"
    assert r.to_local("smb://home-nas/Originals/Movies/b.mkv") == "O:/Movies/b.mkv"
    # a share with no drive letter: the UNC root of the server
    assert r.to_local("smb://home-nas/Music/c.flac") == "//home-nas/Music/c.flac"
    assert r.to_local("smb://home-nas/Originals") == "O:/"
    # back: either drive gives the same canonical path
    assert r.to_db("Y:\\Chan\\a.mp4") == VIDEO
    assert r.to_db("o:/Backup/Youtube/Chan/a.mp4") == VIDEO
    assert r.to_db("//home-nas/Music/c.flac") == "smb://home-nas/Music/c.flac"


def test_macos_mounts() -> None:
    r = build_resolver(
        servers=[NAS],
        mounts=parse_mac_mount(_mounts("mount_macos.txt")),
        system="Darwin",
        exists=_never,
    )
    assert r.to_local(VIDEO) == "/Volumes/Originals/Backup/Youtube/Chan/a.mp4"
    assert r.to_db("/Volumes/Originals/Backup/Youtube/Chan/a.mp4") == VIDEO
    # the NFS mount of the same share resolves back to the same canonical path
    assert r.to_db("/Users/meywd/nas-nfs/Backup/Youtube/Chan/a.mp4") == VIDEO
    assert r.to_local("smb://home-nas/Video Library/x.mp4") == "/Volumes/Video Library/x.mp4"
    # a local disk is not a share
    assert r.canonical("/Volumes/Backup Disk/a.mp4") is None


def test_linux_mounts() -> None:
    r = build_resolver(
        servers=[NAS],
        mounts=parse_proc_mounts(_mounts("proc_mounts_linux.txt")),
        system="Linux",
        exists=_never,
    )
    assert r.to_local(VIDEO) == "/mnt/originals/Backup/Youtube/Chan/a.mp4"
    assert r.to_db("/srv/nfs/originals/Backup/Youtube/Chan/a.mp4") == VIDEO
    # no UNC fallback off Windows: unknown shares stay canonical (and fail to open)
    assert r.to_local("smb://home-nas/Music/c.flac") == "smb://home-nas/Music/c.flac"


def test_precedence_explicit_then_admin_then_mounts_then_legacy() -> None:
    mounts = [Mount("smb", "home-nas", "Originals", "O:/", "drive")]
    r = build_resolver(
        servers=[NAS],
        mounts=mounts,
        system="Windows",
        admin={"smb://HOME-NAS/Originals/": "P:/"},
        legacy={"/Volumes/Originals/": "O:/"},
        exists=_never,
    )
    assert r.to_local(VIDEO) == "P:/Backup/Youtube/Chan/a.mp4"
    # a path stored before the migration still resolves through the legacy prefix
    assert r.to_local("/Volumes/Originals/Backup/x.mp4") == "O:/Backup/x.mp4"
    r2 = build_resolver(
        servers=[NAS],
        mounts=mounts,
        system="Windows",
        admin={"smb://home-nas/Originals/": "P:/"},
        explicit=PathMap.parse("smb://home-nas/Originals/=Q:/"),
        exists=_never,
    )
    assert r2.to_local(VIDEO) == "Q:/Backup/Youtube/Chan/a.mp4"


def test_file_scheme_is_the_path_itself() -> None:
    r = build_resolver(system="Linux")
    assert r.to_local("file:///home/me/a.mp4") == "/home/me/a.mp4"
    assert r.to_local("file:///C:/Videos/a.mp4") == "C:/Videos/a.mp4"
    # no servers, no entries: legacy paths are untouched
    assert r.to_local("/Volumes/x/a.mp4") == "/Volumes/x/a.mp4"
    assert r.to_db("/Volumes/x/a.mp4") == "/Volumes/x/a.mp4"


def test_char_map_on_smb_only() -> None:
    nas = StorageServer(name="home-nas", addresses=("192.168.50.94",), char_map="fullwidth")
    mounts = [
        Mount("smb", "192.168.50.94", "Originals", "/Volumes/Originals/", "mount"),
        Mount("nfs", "192.168.50.94", "/volume1/Originals", "/Users/me/nfs/", "mount"),
    ]
    r = build_resolver(servers=[nas], mounts=mounts, system="Darwin", exists=_never)
    canon = 'smb://home-nas/Originals/Q "live": why?.mp4'
    # the SMB client sees the fullwidth twins; the path keeps the NAS's own name
    assert r.to_local(canon) == "/Volumes/Originals/Q ＂live＂： why？.mp4"
    assert r.to_db("/Volumes/Originals/Q ＂live＂： why？.mp4") == canon
    # over NFS the names are the NAS's own
    assert r.to_db('/Users/me/nfs/Q "live": why?.mp4') == canon


def test_nfc_stored_nfd_fallback_when_only_that_exists(tmp_path: Path) -> None:
    name = "Café.mp4"
    nfd = unicodedata.normalize("NFD", name)
    existing = {f"/Volumes/Originals/{nfd}"}
    mounts = [Mount("smb", "home-nas", "Originals", "/Volumes/Originals/", "mount")]
    r = build_resolver(
        servers=[NAS], mounts=mounts, system="Darwin", exists=lambda p: p in existing
    )
    assert r.to_local(f"smb://home-nas/Originals/{name}") == f"/Volumes/Originals/{nfd}"
    # to_db always stores NFC
    assert r.to_db(f"/Volumes/Originals/{nfd}") == f"smb://home-nas/Originals/{name}"


def test_undeclared_fullwidth_found_by_probe() -> None:
    mounts = [Mount("smb", "home-nas", "Originals", "O:/", "drive")]
    existing = {"O:/a＂b.mp4"}
    r = build_resolver(servers=[NAS], mounts=mounts, system="Windows", exists=existing.__contains__)
    assert r.to_local('smb://home-nas/Originals/a"b.mp4') == "O:/a＂b.mp4"


def test_share_list_for_reporting() -> None:
    r = build_resolver(
        servers=[NAS], mounts=parse_net_use(_mounts("net_use_windows.txt")), system="Windows"
    )
    shares = {(s["share"], s["local"]) for s in r.share_list()}
    assert ("smb://home-nas/Originals", "O:/") in shares
    assert ("smb://home-nas/Originals/Backup/Youtube", "Y:/") in shares
    assert ("smb://home-nas", "//home-nas/") in shares


@pytest.mark.parametrize(
    "name",
    ["اخسأ، فلن تعدو قدرك 🔥🔥🔥.mp4", "أبو عمر الباحث يسحق صاحب النكسة الفكرية من جديد.mp4"],
)
def test_real_names_resolve_whatever_form_the_share_holds(tmp_path: Path, name: str) -> None:
    from maktaba_pipeline.engine import paths

    paths.forget_listings()
    share = tmp_path / "Originals"
    folder = share / "Backup" / "Youtube" / "AntiShubohat"
    folder.mkdir(parents=True)
    on_disk = folder / unicodedata.normalize("NFD", name)  # written by the Mac
    on_disk.write_bytes(b"v")
    r = build_resolver(
        servers=[NAS],
        mounts=[Mount("smb", "192.168.50.94", "Originals", share.as_posix(), "mount")],
        system="Linux",
    )
    local = r.to_local(f"smb://home-nas/Originals/Backup/Youtube/AntiShubohat/{name}")
    assert Path(local).read_bytes() == b"v"
