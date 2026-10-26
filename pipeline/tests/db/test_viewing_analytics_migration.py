"""Slot 0158 (SQLite twin): analytics counts every way of watching (Epic 44 story 44.1) -
plays recorded from their stream session (one row per stream session), Live TV viewings
on each surface, and the API's use per app and day. Both directions."""

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

pytestmark = pytest.mark.unit

_MIG = MIGRATIONS_DIR / "0158_viewing_analytics.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


def test_0158_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=157)
    conn.execute("INSERT INTO users (id, username, pw_hash) VALUES ('u1', 'sara', 'x')")
    conn.execute(
        "INSERT INTO channels (id, number, name, slug, mode)"
        " VALUES ('c1', 101, 'One', 'one', 'shuffle')"
    )
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    plays = _cols(conn, "watch_sessions")
    assert {"stream_session_id", "client_app", "client_version"} <= plays
    viewings = _cols(conn, "tv_watch_sessions")
    assert {"surface", "seconds", "end_reason", "client_addr_hash"} <= viewings

    # a tuner viewing has no person; the surface is one of three
    viewing = "INSERT INTO tv_watch_sessions (id, channel_id, surface) VALUES (?, 'c1', ?)"
    conn.execute(viewing, ("t1", "tuner"))
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(viewing, ("t2", "plex"))

    # one row per day, app and platform; one per day, app and person
    usage = "INSERT INTO api_usage_daily (day, client_app, platform) VALUES (?, ?, ?)"
    conn.execute(usage, ("2026-10-26", "player", "ios"))
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(usage, ("2026-10-26", "player", "ios"))
    conn.execute(
        "INSERT INTO api_usage_people_daily (day, client_app, user_id) VALUES (?, ?, ?)",
        ("2026-10-26", "player", "u1"),
    )

    for stmt in down_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not _cols(conn, "tv_watch_sessions")
    assert not _cols(conn, "api_usage_daily")
    assert "stream_session_id" not in _cols(conn, "watch_sessions")
