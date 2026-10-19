"""Mount discovery parsers on recorded outputs (story 31.3).

``net_use_windows.txt`` is this repo's Windows node (``O:`` and ``Y:`` mapped to the
Synology by its host name, ``Y:`` a sub folder of the ``Originals`` share); the macOS
and Linux files follow those systems' formats with the same NAS.
"""

from __future__ import annotations

from pathlib import Path

import pytest

from maktaba_pipeline.sharepath.mounts import (
    Mount,
    parse_mac_mount,
    parse_net_use,
    parse_proc_mounts,
    windows_drive_mounts,
)

pytestmark = pytest.mark.unit

DATA = Path(__file__).resolve().parents[3] / "shared" / "testdata" / "sharepath"


def _read(name: str) -> str:
    return (DATA / name).read_text(encoding="utf-8")


def test_net_use() -> None:
    got = parse_net_use(_read("net_use_windows.txt"))
    assert got == [
        Mount("smb", "home-nas", "Originals", "O:/", "net use"),
        Mount("smb", "home-nas", "Originals/Backup/Youtube", "Y:/", "net use"),
        Mount("smb", "192.168.50.94", "Archive", "Z:/", "net use"),
    ]


def test_net_use_unc_without_a_letter() -> None:
    text = "OK                     \\\\192.168.50.94\\Originals   Microsoft Windows Network\n"
    assert parse_net_use(text) == [
        Mount("smb", "192.168.50.94", "Originals", "//192.168.50.94/Originals/", "net use")
    ]


def test_windows_drives_by_wnet() -> None:
    remotes = {"O": r"\\Home-NAS\Originals", "Y": r"\\Home-NAS\Originals\Backup\My Youtube"}
    got = windows_drive_mounts(remotes.get)
    assert got == [
        Mount("smb", "home-nas", "Originals", "O:/", "drive"),
        Mount("smb", "home-nas", "Originals/Backup/My Youtube", "Y:/", "drive"),
    ]


def test_mac_mount() -> None:
    got = parse_mac_mount(_read("mount_macos.txt"))
    assert got == [
        Mount("smb", "192.168.50.94", "Originals", "/Volumes/Originals/", "mount"),
        Mount("smb", "home-nas.local", "Video Library", "/Volumes/Video Library/", "mount"),
        Mount("nfs", "192.168.50.94", "/volume1/Originals", "/Users/meywd/nas-nfs/", "mount"),
    ]


def test_proc_mounts() -> None:
    got = parse_proc_mounts(_read("proc_mounts_linux.txt"))
    assert got == [
        Mount("smb", "192.168.50.94", "Originals", "/mnt/originals/", "proc"),
        Mount("smb", "home-nas.local", "Video Library", "/media/meywd/Video Library/", "proc"),
        Mount("nfs", "192.168.50.94", "/volume1/Originals", "/srv/nfs/originals/", "proc"),
    ]
