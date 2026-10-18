"""PathMap: case-insensitive both ways, UNC prefixes round-trip (design section 6)."""

from __future__ import annotations

import pytest

from maktaba_pipeline.engine.paths import PathMap

pytestmark = pytest.mark.unit


def test_to_db_is_case_insensitive_like_to_local() -> None:
    pm = PathMap.parse("Y:/=/Volumes/Y")
    assert pm.to_local("y:/Channel/a.mp4") == "/Volumes/Y/Channel/a.mp4"
    assert pm.to_db("/volumes/y/Channel/a.mp4") == "Y:/Channel/a.mp4"
    win = PathMap.parse("/volume1/media=Z:/")
    assert win.to_db("z:\\Channel\\a.mp4") == "/volume1/media/Channel/a.mp4"


def test_unc_prefix_normalises_and_round_trips() -> None:
    pm = PathMap.parse("/volume1/media=\\\\nas\\share")
    assert pm.as_json() == {"/volume1/media/": "//nas/share/"}
    local = pm.to_local("/volume1/media/Channel/a.mp4")
    assert local == "//nas/share/Channel/a.mp4"
    assert pm.to_db(local) == "/volume1/media/Channel/a.mp4"
    assert pm.to_db("\\\\NAS\\Share\\Channel\\a.mp4") == "/volume1/media/Channel/a.mp4"
    # a UNC database path maps too
    db_unc = PathMap.parse('{"\\\\\\\\nas\\\\share": "/mnt/nas"}')
    assert db_unc.to_local("\\\\nas\\share\\x\\a.mp4") == "/mnt/nas/x/a.mp4"
    assert db_unc.to_db("/mnt/nas/x/a.mp4") == "//nas/share/x/a.mp4"


def test_longest_prefix_wins_and_unmapped_paths_pass_through() -> None:
    pm = PathMap.parse("Y:/=/Volumes/Y;Y:/Kids=/Volumes/Kids")
    assert pm.to_local("Y:/Kids/a.mp4") == "/Volumes/Kids/a.mp4"
    assert pm.to_db("/Volumes/Kids/a.mp4") == "Y:/Kids/a.mp4"
    assert pm.to_db("/elsewhere/a.mp4") == "/elsewhere/a.mp4"
