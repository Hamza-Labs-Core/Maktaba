"""Slot 0126 (SQLite twin): simulcast through Cloudflare live outputs (story 32.13) -
destinations (the stream key stays in home_secrets), attachments (channel x destination,
always or on a schedule, the rights confirmation), outputs (one open row per attachment),
the push's holds and the day's output minutes. Both directions."""

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

_MIG = MIGRATIONS_DIR / "0126_tv_simulcast.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


@pytest.fixture(scope="module")
def schema_at_126() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=125)
    conn.execute(
        "INSERT INTO channels (id, number, name, slug, mode)"
        " VALUES ('c1', 101, 'One', 'one', 'shuffle')"
    )
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    return conn


def test_0126_up_down(schema_at_126: sqlite3.Connection) -> None:
    conn = schema_at_126
    assert {"name", "kind", "ingest_url", "key_secret"} <= _cols(conn, "tv_simulcast_destinations")
    # the key is in home_secrets, never here
    assert not {"key", "stream_key"} & _cols(conn, "tv_simulcast_destinations")
    assert {
        "channel_id", "destination_id", "mode", "rules", "enabled", "manual", "manual_until",
        "rights_confirmed_by", "rights_confirmed_at",
    } <= _cols(conn, "tv_simulcast_attachments")  # fmt: skip
    assert {
        "attachment_id", "push_id", "input_id", "input_uid", "cf_output_uid", "state",
        "window_start", "window_end", "started_at", "ended_at", "minutes", "error",
    } <= _cols(conn, "tv_simulcast_outputs")  # fmt: skip
    assert "holds" in _cols(conn, "tv_remote_pushes")
    assert "simulcast_minutes" in _cols(conn, "tv_remote_usage_daily")

    conn.execute(
        "INSERT INTO tv_simulcast_destinations (id, name, kind, ingest_url, key_secret)"
        " VALUES ('d1', 'YT', 'youtube', 'rtmp://a.rtmp.youtube.com/live2', 'simulcast_d1_key')"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO tv_simulcast_destinations (id, name, kind, ingest_url, key_secret)"
            " VALUES ('d2', 'X', 'twitch', 'rtmp://x/live', 'k')"
        )
    conn.execute(
        "INSERT INTO tv_simulcast_attachments (id, channel_id, destination_id, mode)"
        " VALUES ('a1', 'c1', 'd1', 'always')"
    )
    # one attachment per channel and destination
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO tv_simulcast_attachments (id, channel_id, destination_id, mode)"
            " VALUES ('a2', 'c1', 'd1', 'schedule')"
        )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("UPDATE tv_simulcast_attachments SET manual = 'pause' WHERE id = 'a1'")
    conn.execute(
        "INSERT INTO tv_simulcast_outputs (id, attachment_id, state) VALUES ('o1', 'a1', 'live')"
    )
    # one open output per attachment; an ended one does not count
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO tv_simulcast_outputs (id, attachment_id, state)"
            " VALUES ('o2', 'a1', 'starting')"
        )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("UPDATE tv_simulcast_outputs SET state = 'paused' WHERE id = 'o1'")
    conn.execute(
        "UPDATE tv_simulcast_outputs SET state = 'off', ended_at = '2026-10-19T00:00:00Z'"
        " WHERE id = 'o1'"
    )
    conn.execute(
        "INSERT INTO tv_simulcast_outputs (id, attachment_id, state)"
        " VALUES ('o2', 'a1', 'starting')"
    )
    assert conn.execute("SELECT holds FROM tv_remote_pushes LIMIT 1").fetchall() == []

    for stmt in down_statements(_MIG.read_text(encoding="utf-8")):
        apply_statement(conn, stmt)
    for t in ("tv_simulcast_destinations", "tv_simulcast_attachments", "tv_simulcast_outputs"):
        assert not _cols(conn, t), t
    assert "holds" not in _cols(conn, "tv_remote_pushes")
    assert "simulcast_minutes" not in _cols(conn, "tv_remote_usage_daily")
