"""Slot 0129 (SQLite twin): the TV slots (Epic 33 story 33.1) - one row per host and
slot instance (blue, green, amber) with its role (the front door's) and the epoch it
was written under, its state, release and heartbeat; at most one live slot per host.
Both directions."""

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

_MIG = MIGRATIONS_DIR / "0129_tv_slots.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


def test_0129_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=128)
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert {
        "host_id", "slot", "role", "role_epoch", "state", "release_id", "base_url", "pid",
        "version", "warm", "encoders_used", "started_at", "beat_at", "last_exit",
    } <= _cols(conn, "tv_slots")  # fmt: skip
    conn.execute("INSERT INTO tv_slots (host_id, slot, role, role_epoch) VALUES ('h', 'blue', 'live', 1)")
    conn.execute("INSERT INTO tv_slots (host_id, slot, role, role_epoch) VALUES ('h', 'green', 'standby', 1)")
    # one live slot per host; only the three instances and the known roles
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("UPDATE tv_slots SET role = 'live' WHERE slot = 'green'")
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO tv_slots (host_id, slot) VALUES ('h', 'purple')")
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO tv_slots (host_id, slot, role) VALUES ('h', 'amber', 'boss')")
    conn.execute("INSERT INTO tv_slots (host_id, slot, role) VALUES ('other', 'blue', 'live')")
    row = conn.execute("SELECT state, warm, encoders_used FROM tv_slots WHERE slot = 'green'").fetchone()
    assert row == ("stopped", "[]", 0)
    for stmt in down_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not _cols(conn, "tv_slots")
