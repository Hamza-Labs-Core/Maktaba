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


@pytest.fixture(scope="module")
def schema_at_128() -> sqlite3.Connection:
    """The schema as of slot 0128, built once: applying ~130 twins is the slow part."""
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=128)
    return conn


def test_0129_up_down(schema_at_128: sqlite3.Connection) -> None:
    conn = sqlite3.connect(":memory:")
    schema_at_128.backup(conn)  # a private copy; the template stays untouched
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert {
        "host_id", "slot", "role", "role_epoch", "state", "release_id", "base_url", "pid",
        "version", "warm", "encoders_used", "started_at", "beat_at", "last_exit",
    } <= _cols(conn, "tv_slots")  # fmt: skip
    ins = "INSERT INTO tv_slots (host_id, slot, role, role_epoch) VALUES (?, ?, ?, 1)"
    conn.execute(ins, ("h", "blue", "live"))
    conn.execute(ins, ("h", "green", "standby"))
    # one live slot per host; only the three instances and the known roles
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("UPDATE tv_slots SET role = 'live' WHERE slot = 'green'")
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO tv_slots (host_id, slot) VALUES ('h', 'purple')")
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO tv_slots (host_id, slot, role) VALUES ('h', 'amber', 'boss')")
    conn.execute("INSERT INTO tv_slots (host_id, slot, role) VALUES ('other', 'blue', 'live')")
    row = conn.execute(
        "SELECT state, warm, encoders_used FROM tv_slots WHERE slot = 'green'"
    ).fetchone()
    assert row == ("stopped", "[]", 0)
    for stmt in down_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not _cols(conn, "tv_slots")
