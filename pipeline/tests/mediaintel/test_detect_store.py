"""Saving a detection rebuilds one folder's seasons and keeps manual work."""

from __future__ import annotations

import sqlite3
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from maktaba_pipeline.classify.series_detector import Detection, Season, Series  # noqa: E402
from scripts.mediaintel import db, detect  # noqa: E402

pytestmark = pytest.mark.unit


def _files(conn: sqlite3.Connection, folder: str, n: int) -> list[str]:
    paths = [f"{folder}/ep{i}.mp4" for i in range(1, n + 1)]
    conn.executemany(
        "INSERT INTO files (path, folder, stem, ext, size, mtime) VALUES (?,?,?,?,?,?)",
        [(p, folder, Path(p).stem, "mp4", 1, 1.0) for p in paths],
    )
    return paths


def _det(name: str, eps: list[str]) -> Detection:
    return Detection(series=[Series(name, name, [Season(1, eps)])], unassigned=[])


def test_another_folder_in_the_library_keeps_its_seasons(tmp_path: Path) -> None:
    conn = db.connect(tmp_path / "c.db")
    a, b = _files(conn, "/lib/a", 3), _files(conn, "/lib/b", 3)
    detect.store(conn, "lib", "/lib/a", _det("show a", a))
    detect.store(conn, "lib", "/lib/b", Detection(series=[], unassigned=b))
    assert conn.execute("SELECT count(*) FROM series_episodes").fetchone()[0] == 3


def test_rerun_keeps_manual_episodes_of_a_season(tmp_path: Path) -> None:
    conn = db.connect(tmp_path / "c.db")
    eps = _files(conn, "/lib/a", 4)
    detect.store(conn, "lib", "/lib/a", _det("show", eps[:3]))
    sid = conn.execute("SELECT id FROM series").fetchone()[0]
    conn.execute(
        "INSERT INTO series_episodes (series_id, path, season, episode, is_user_override) "
        "VALUES (?,?,1,9,1)",
        (sid, eps[3]),
    )
    conn.commit()
    detect.store(conn, "lib", "/lib/a", _det("show", eps[:3]))
    rows = conn.execute(
        "SELECT path, episode FROM series_episodes WHERE is_user_override=1"
    ).fetchall()
    assert [tuple(r) for r in rows] == [(eps[3], 9)]


def test_same_named_shows_get_their_own_ids(tmp_path: Path) -> None:
    conn = db.connect(tmp_path / "c.db")
    eps = _files(conn, "/lib/a", 6)
    det = Detection(
        series=[
            Series("show", "show", [Season(1, eps[:3])]),
            Series("show", "show", [Season(1, eps[3:])]),
        ],
        unassigned=[],
    )
    detect.store(conn, "lib", "/lib/a", det)
    assert conn.execute("SELECT count(*) FROM series").fetchone()[0] == 2
    assert conn.execute("SELECT count(*) FROM series_episodes").fetchone()[0] == 6
