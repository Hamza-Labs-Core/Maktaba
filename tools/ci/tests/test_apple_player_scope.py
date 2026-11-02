"""Whether a change to the Apple project file is the Player's (tools/ci/apple_player_scope.py),
on the real project file with one setting changed."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import apple_player_scope as scope  # noqa: E402

ROOT = Path(__file__).resolve().parents[3]
TEXT = (ROOT / scope.PBXPROJ).read_text(encoding="utf-8")


def _edit(old: str, new: str) -> str:
    assert old in TEXT, f"{old!r} is no longer in the project file: pick another setting"
    return TEXT.replace(old, new)


@pytest.mark.parametrize(
    ("name", "old", "new", "player"),
    [
        # the Remote's home-screen name (#508): the Remote's build settings only
        (
            "remote name",
            "INFOPLIST_KEY_CFBundleDisplayName = \"Maktaba Remote\";",
            "INFOPLIST_KEY_CFBundleDisplayName = \"Maktaba Remotes\";",
            False,
        ),
        ("remote icon", "ASSETCATALOG_COMPILER_APPICON_NAME = RemoteIcon;", "ASSETCATALOG_COMPILER_APPICON_NAME = R;", False),
        ("tv app", "PRODUCT_BUNDLE_IDENTIFIER = com.hamzalabs.maktaba.tv;", "PRODUCT_BUNDLE_IDENTIFIER = x.tv;", False),
        ("player", "PRODUCT_BUNDLE_IDENTIFIER = com.hamzalabs.maktaba.player;", "PRODUCT_BUNDLE_IDENTIFIER = x.p;", True),
        # the project's own build settings are every target's
        ("project settings", "SWIFT_VERSION = 6.0;", "SWIFT_VERSION = 6.1;", True),
        # the archive's header
        ("object version", "objectVersion = 77;", "objectVersion = 78;", True),
    ],
)
def test_a_change_is_the_players_or_not(name: str, old: str, new: str, player: bool) -> None:
    got, why = scope.touches_player(TEXT, _edit(old, new))
    assert got is player, f"{name}: {why}"


def test_a_file_the_player_builds() -> None:
    # a source file of the Player's: its file reference is the Player's
    got, _ = scope.touches_player(TEXT, _edit("path = WatchLogic.swift;", "path = WatchLogic2.swift;"))
    assert got is True


def test_unchanged_and_unreadable() -> None:
    assert scope.touches_player(TEXT, TEXT)[0] is False
    assert scope.touches_player(None, TEXT)[0] is True
    with pytest.raises(scope.ParseError):
        scope.touches_player(TEXT, TEXT[: len(TEXT) // 2])


def test_the_parser_reads_every_object() -> None:
    objects = scope.parse(TEXT)["objects"]
    assert isinstance(objects, dict) and len(objects) > 500
    names = {o.get("name") for o in objects.values() if isinstance(o, dict) and o.get("isa") == "PBXNativeTarget"}
    assert {"Maktaba Player", "Maktaba Remote", "Maktaba TV", "Maktaba Player Mac UITests"} <= names
