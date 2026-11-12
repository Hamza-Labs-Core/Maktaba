"""Slot 0224 on SQLite (Epic 57, story 57.17, T05): the table of what the content screen
detected. Up, up again (nothing changes), down, and up once more; the MANIFEST names the
slot. The Postgres file is proved by the API's tests (it holds the trigger and the role)."""

from __future__ import annotations

import sqlite3

import pytest

from tests.engine.migrate import (
    MIGRATIONS_DIR,
    apply_sqlite,
    apply_statement,
    down_statements,
    up_statements,
)

SLOT = "0224_content_signals"
TABLE = "video_content_signals"
INDEX = "video_content_signals_review_idx"


def _has(conn: sqlite3.Connection, kind: str, name: str) -> bool:
    return bool(
        conn.execute(
            "SELECT 1 FROM sqlite_master WHERE type = ? AND name = ?", (kind, name)
        ).fetchone()
    )


@pytest.mark.unit
def test_0224_sqlite_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    conn.execute("PRAGMA foreign_keys = ON")
    apply_sqlite(conn, upto=223)
    sql = (MIGRATIONS_DIR / f"{SLOT}.sqlite.sql").read_text("utf-8")
    assert not _has(conn, "table", TABLE)
    for _ in range(2):  # the second run changes nothing
        for stmt in up_statements(sql):
            apply_statement(conn, stmt)
    assert _has(conn, "table", TABLE) and _has(conn, "index", INDEX)
    cols = {r[1] for r in conn.execute(f"PRAGMA table_info({TABLE})")}
    assert cols == {"video_id", "tier", "confidence", "categories", "not_checked", "model",
                    "input_hash", "attempts", "failed_hash", "failed_runs", "reviewed_by",
                    "reviewed_at", "updated_at"}  # fmt: skip

    lib = "11111111-1111-1111-1111-111111111111"
    vid = "22222222-2222-2222-2222-222222222222"
    conn.execute("INSERT INTO libraries (id, name, roots) VALUES (?, 'm', '[]')", (lib,))
    video_cols = {r[1]: r for r in conn.execute("PRAGMA table_info(videos)")}
    need = {"id": vid, "library_id": lib, "path": "/m/a.mkv", "title": "a"}
    # whatever else videos demands and has no default for
    for name, r in video_cols.items():
        if r[3] and r[4] is None and name not in need:
            need[name] = "0" if "INT" in str(r[2]).upper() else "x"
    names = ", ".join(need)
    conn.execute(
        f"INSERT INTO videos ({names}) VALUES ({', '.join('?' for _ in need)})",  # noqa: S608
        tuple(need.values()),
    )
    conn.execute(f"INSERT INTO {TABLE} (video_id, tier, confidence) VALUES (?, 13, 0.9)", (vid,))  # noqa: S608
    for bad in (
        f"UPDATE {TABLE} SET tier = 12",  # not a tier of the ladder
        f"UPDATE {TABLE} SET confidence = 1.5",
        f"UPDATE {TABLE} SET not_checked = 'because'",
        f"UPDATE {TABLE} SET not_checked = 'no_transcript'",  # a tier and a reason not to have one
    ):
        with pytest.raises(sqlite3.IntegrityError):
            conn.execute(bad)
    conn.execute(f"UPDATE {TABLE} SET tier = NULL, not_checked = 'unsupported_language'")  # noqa: S608
    # the row goes with its video
    conn.execute("DELETE FROM videos WHERE id = ?", (vid,))
    assert conn.execute(f"SELECT count(*) FROM {TABLE}").fetchone()[0] == 0  # noqa: S608

    for stmt in down_statements(sql):
        apply_statement(conn, stmt)
    assert not _has(conn, "table", TABLE) and not _has(conn, "index", INDEX)
    for stmt in up_statements(sql):
        apply_statement(conn, stmt)
    assert _has(conn, "table", TABLE) and _has(conn, "index", INDEX)


@pytest.mark.unit
def test_0224_is_in_the_manifest_in_both_dialects() -> None:
    assert (MIGRATIONS_DIR / f"{SLOT}.sql").is_file()
    assert (MIGRATIONS_DIR / f"{SLOT}.sqlite.sql").is_file()
    assert SLOT in (MIGRATIONS_DIR / "MANIFEST.md").read_text("utf-8")
