"""The canonical form: parse / format, NFC, the character maps (story 31.3)."""

from __future__ import annotations

import unicodedata

import pytest

from maktaba_pipeline.sharepath.canonical import (
    FULLWIDTH,
    SFM,
    CharMap,
    format_canonical,
    is_canonical,
    nfc,
    normalise_rel,
    parse_canonical,
    parse_char_map,
    share_prefix,
)

pytestmark = pytest.mark.unit


def test_format_and_parse_round_trip() -> None:
    p = format_canonical("SMB", "Home-NAS", "Originals", "Backup\\Youtube/Chan/a b%.mp4")
    assert p == "smb://home-nas/Originals/Backup/Youtube/Chan/a b%.mp4"
    sp = parse_canonical(p)
    assert sp is not None
    assert (sp.scheme, sp.server, sp.share, sp.rel) == (
        "smb",
        "home-nas",
        "Originals",
        "Backup/Youtube/Chan/a b%.mp4",
    )
    assert str(sp) == p
    assert sp.prefix == "smb://home-nas/Originals/"


def test_share_root_without_rel() -> None:
    assert format_canonical("smb", "nas", "Originals") == "smb://nas/Originals"
    sp = parse_canonical("smb://nas/Originals")
    assert sp is not None and sp.rel == "" and sp.share == "Originals"


def test_nfs_export_keeps_its_segments() -> None:
    assert (
        format_canonical("nfs", "nas", "/export/media/", "a.mkv") == "nfs://nas/export/media/a.mkv"
    )


def test_file_paths() -> None:
    assert format_canonical("file", "", "", "/Users/me/a.mp4") == "file:///Users/me/a.mp4"
    assert format_canonical("file", "", "", "c:\\Videos\\a.mp4") == "file:///C:/Videos/a.mp4"
    sp = parse_canonical("file:///Users/me/a.mp4")
    assert sp is not None and sp.rel == "/Users/me/a.mp4"
    sp = parse_canonical("file:///C:/Videos/a.mp4")
    assert sp is not None and sp.rel == "C:/Videos/a.mp4"


@pytest.mark.parametrize(
    "legacy",
    ["/Volumes/Originals/a.mp4", "Y:/Chan/a.mp4", "//nas/share/a.mp4", "http://x/a.mp4", ""],
)
def test_legacy_paths_are_not_canonical(legacy: str) -> None:
    assert parse_canonical(legacy) is None
    assert not is_canonical(legacy)


def test_nfc_storage() -> None:
    nfd = unicodedata.normalize("NFD", "Café – مكتبة.mp4")
    assert nfd != "Café – مكتبة.mp4"
    assert nfc(nfd) == "Café – مكتبة.mp4"
    assert normalise_rel("a/./" + nfd) == "a/Café – مكتبة.mp4"
    assert format_canonical("smb", "nas", "O", nfd).endswith("Café – مكتبة.mp4")


def test_dotdot_refused() -> None:
    with pytest.raises(ValueError):
        normalise_rel("a/../b")


def test_share_prefix_lower_cases_the_server_only() -> None:
    assert share_prefix("smb", "NAS", "Originals") == "smb://nas/Originals/"


def test_char_map_presets_both_ways() -> None:
    disk = 'Q&A "live": why? <1|2> a*b.mp4'
    fw = CharMap("fullwidth")
    client = fw.to_client(disk)
    assert client == "Q&A ＂live＂： why？ ＜1｜2＞ a＊b.mp4"
    assert fw.to_disk(client) == disk
    sfm = CharMap("sfm")
    assert sfm.to_client('"') == "\uf020"
    assert sfm.to_disk(sfm.to_client(disk)) == disk
    assert CharMap().to_client(disk) == disk and not CharMap().active
    assert set(FULLWIDTH) == set(SFM)


def test_char_map_catia_list() -> None:
    assert parse_char_map("0x22:0xff02, 0x3a:0xff1a") == {'"': "＂", ":": "："}
    with pytest.raises(ValueError):
        parse_char_map("0x22")
    with pytest.raises(ValueError):
        parse_char_map("0x2f:0xff0f")
