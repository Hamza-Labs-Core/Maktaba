"""thumbs: one copy per image, and one failed copy never stops the others."""

from __future__ import annotations

import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[2]))  # pipeline/ (scripts package)

from scripts.mediaintel import db, thumbs  # noqa: E402

pytestmark = pytest.mark.unit


def _files(conn: object, rows: list[tuple[str, str]]) -> None:
    for i, (path, thumb) in enumerate(rows):
        conn.execute(  # type: ignore[attr-defined]
            "INSERT INTO files (path, folder, stem, ext, size, mtime, thumb_path)"
            " VALUES (?,?,?,?,?,?,?)",
            (path, "Y:/a", f"v{i}", ".mp4", 1, 1.0, thumb),
        )
    conn.commit()  # type: ignore[attr-defined]


def test_two_videos_sharing_an_image_make_one_copy(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    conn = db.connect(tmp_path / "mi.db")
    # an .mkv and its .mp4 copy share one sidecar image
    _files(conn, [("Y:/a/x.mp4", "Y:/a/x.jpg"), ("Y:/a/x.mkv", "Y:/a/x.jpg"),
                  ("Y:/a/y.mp4", "Y:/a/y.jpg")])  # fmt: skip
    calls: list[str] = []

    def fake_copy(src: str, dst: Path) -> str | None:
        calls.append(src)
        dst.write_bytes(b"jpg")
        return None

    monkeypatch.setattr(thumbs, "_copy", fake_copy)
    thumbs.ensure(conn, ["Y:/a/x.mp4", "Y:/a/x.mkv", "Y:/a/y.mp4"])
    assert sorted(calls) == ["Y:/a/x.jpg", "Y:/a/y.jpg"]  # x.jpg once, not twice
    # both videos of the shared image count as done: nothing is copied again
    calls.clear()
    thumbs.ensure(conn, ["Y:/a/x.mp4", "Y:/a/x.mkv", "Y:/a/y.mp4"])
    assert calls == []


def test_a_copy_that_crashes_never_stops_the_others(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    conn = db.connect(tmp_path / "mi.db")
    _files(conn, [(f"Y:/a/v{i}.mp4", f"Y:/a/v{i}.jpg") for i in range(5)])
    made: list[str] = []

    def flaky_copy(src: str, dst: Path) -> str | None:
        if src.endswith("v2.jpg"):
            raise FileNotFoundError("the copy vanished")
        dst.write_bytes(b"jpg")
        made.append(src)
        return None

    monkeypatch.setattr(thumbs, "_copy", flaky_copy)
    thumbs.ensure(conn, [f"Y:/a/v{i}.mp4" for i in range(5)])
    assert len(made) == 4  # the other four were copied
