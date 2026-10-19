"""Slot 0125 (SQLite twin): the remote viewing tables (input pool, pushes, sessions, daily
usage, a running push's ingest secret). One active push per channel; one open session per
user, channel and device. Both directions."""

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

_MIG = MIGRATIONS_DIR / "0125_tv_remote.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


@pytest.fixture(scope="module")
def schema_at_125() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=124)
    conn.execute(
        "INSERT INTO channels (id, number, name, slug, mode)"
        " VALUES ('c1', 101, 'One', 'one', 'shuffle')"
    )
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    return conn


def test_0125_up_down(schema_at_125: sqlite3.Connection) -> None:
    conn = schema_at_125
    assert {"cf_uid", "name", "state", "channel_id", "ingest", "last_video_uid"} <= _cols(
        conn, "tv_remote_inputs"
    )
    assert {
        "channel_id", "input_id", "host_id", "generation", "state", "video_uid", "ingest",
        "attempt", "relay_state", "minutes_delivered", "recording_deleted_at",
    } <= _cols(conn, "tv_remote_pushes")  # fmt: skip
    assert {"user_id", "channel_id", "push_id", "device", "last_beat_at", "token_exp"} <= _cols(
        conn, "tv_remote_sessions"
    )
    assert {"day", "minutes_delivered", "peak_recording_minutes", "cost_usd", "source"} <= _cols(
        conn, "tv_remote_usage_daily"
    )
    assert {"push_id", "url", "key", "stream_id", "expires_at"} <= _cols(conn, "tv_push_secrets")
    # no secret in the pool table
    assert not {"key", "stream_key", "passphrase"} & _cols(conn, "tv_remote_inputs")

    conn.execute(
        "INSERT INTO tv_remote_inputs (id, cf_uid, name) VALUES ('i1', 'u1', 'maktaba-x-1')"
    )
    conn.execute(
        "INSERT INTO tv_remote_inputs (id, cf_uid, name) VALUES ('i2', 'u2', 'maktaba-x-2')"
    )
    conn.execute(
        "INSERT INTO tv_remote_pushes (id, channel_id, input_id, state)"
        " VALUES ('p1', 'c1', 'i1', 'live')"
    )
    # one active push per channel
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO tv_remote_pushes (id, channel_id, input_id, state)"
            " VALUES ('p2', 'c1', 'i2', 'requested')"
        )
    conn.execute("UPDATE tv_remote_pushes SET state = 'done' WHERE id = 'p1'")
    conn.execute(
        "INSERT INTO tv_remote_pushes (id, channel_id, input_id, state)"
        " VALUES ('p2', 'c1', 'i2', 'requested')"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("UPDATE tv_remote_pushes SET state = 'paused' WHERE id = 'p2'")

    # one open session per user, channel and device; an ended one does not count
    conn.execute(
        "INSERT INTO tv_remote_sessions (id, user_id, channel_id, push_id, device)"
        " VALUES ('s1', 'u', 'c1', 'p2', 'phone')"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO tv_remote_sessions (id, user_id, channel_id, push_id, device)"
            " VALUES ('s2', 'u', 'c1', 'p2', 'phone')"
        )
    conn.execute("UPDATE tv_remote_sessions SET ended_at = '2026-10-19T00:00:00Z' WHERE id = 's1'")
    conn.execute(
        "INSERT INTO tv_remote_sessions (id, user_id, channel_id, push_id, device)"
        " VALUES ('s2', 'u', 'c1', 'p2', 'phone')"
    )

    for stmt in down_statements(_MIG.read_text(encoding="utf-8")):
        apply_statement(conn, stmt)
    for t in (
        "tv_remote_inputs", "tv_remote_pushes", "tv_remote_sessions", "tv_remote_usage_daily",
        "tv_push_secrets",
    ):  # fmt: skip
        assert not _cols(conn, t), t
