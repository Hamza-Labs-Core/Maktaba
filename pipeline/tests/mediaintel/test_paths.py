"""A cache written on Windows (backslash paths) must work on any OS."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel import db  # noqa: E402
from scripts.mediaintel.scan_plex import map_path  # noqa: E402

pytestmark = pytest.mark.unit


def test_windows_cache_is_migrated_to_posix_and_season_ids_follow(tmp_path: Path) -> None:
    conn = db.connect(tmp_path / "c.db")
    win = "Y:\\show"
    conn.execute(
        "INSERT INTO files (path, folder, stem, ext, size, mtime) VALUES (?,?,?,?,?,?)",
        (win + "\\ep1.mp4", win, "ep1", "mp4", 1, 1.0),
    )
    old = db.series_id(win, "key", "episodic", 1)
    meta = {"folder": win, "series_key": "key", "kind": "episodic", "season": 1,
            "extras": [win + "\\promo.mp4"]}  # fmt: skip
    conn.execute(
        "INSERT INTO series (id, library, name, metadata, detector_version) VALUES (?,?,?,?,?)",
        (old, "lib", "show", json.dumps(meta), 1),
    )
    conn.execute(
        "INSERT INTO series_episodes (series_id, path, season, episode) VALUES (?,?,?,?)",
        (old, win + "\\ep1.mp4", 1, 1),
    )
    conn.commit()
    assert db.normalize_paths(conn) == 1
    new = db.series_id("Y:/show", "key", "episodic", 1)
    assert conn.execute("SELECT path, folder FROM files").fetchone()[:] == (
        "Y:/show/ep1.mp4",
        "Y:/show",
    )
    row = conn.execute("SELECT id, metadata FROM series").fetchone()
    assert row[0] == new and json.loads(row[1])["extras"] == ["Y:/show/promo.mp4"]
    assert conn.execute("SELECT series_id, path FROM series_episodes").fetchone()[:] == (
        new,
        "Y:/show/ep1.mp4",
    )
    assert db.normalize_paths(conn) == 0  # idempotent


def test_plex_paths_map_to_posix_local_paths() -> None:
    m = [("/volume1/share/", "Y:/"), ("/srv/media/", "/mnt/media")]
    assert map_path("/volume1/share/a/b.mp4", m) == "Y:/a/b.mp4"
    assert map_path("/srv/media/a/b.mp4", m) == "/mnt/media/a/b.mp4"
    assert map_path("/other/x.mp4", m) is None
