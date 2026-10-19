"""Slot 0117 (SQLite twin): channel_runtime gains the on-air columns, and the hdhr_device
rebuild changes only the tuner_count default (the row is kept as it is). Both directions."""

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

_MIG = MIGRATIONS_DIR / "0117_tv_core_runtime.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> dict[str, str | None]:
    return {r[1]: r[4] for r in conn.execute(f"PRAGMA table_info({table})")}


@pytest.fixture(scope="module")
def schema_at_117() -> sqlite3.Connection:
    """Migrated to 116, a device row with the old default, then slot 117 (setup is not
    timed against the unit soft cap)."""
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=116)
    conn.execute(
        "INSERT INTO hdhr_device (id, device_id, device_uuid, tuner_count, enabled)"
        " VALUES (1, 'ABCD1234', 'u-1', 4, 1)"
    )
    for stmt in up_statements((MIGRATIONS_DIR / "0117_tv_core_runtime.sqlite.sql").read_text("utf-8")):
        apply_statement(conn, stmt)
    return conn


def test_up_and_down(schema_at_117: sqlite3.Connection) -> None:
    conn = schema_at_117
    rt = _cols(conn, "channel_runtime")
    assert {"host_id", "stream_base_url", "encoder", "restarts", "nvenc_fallbacks", "heartbeat_at"} <= set(rt)
    hosts = _cols(conn, "streaming_hosts")
    assert {"host_id", "role", "max_active", "active_channels", "nvenc_free", "vt_free", "heartbeat_at"} <= set(hosts)
    assert _cols(conn, "hdhr_device")["tuner_count"] == "20"
    assert conn.execute("SELECT device_id, tuner_count, enabled FROM hdhr_device").fetchall() == [
        ("ABCD1234", 4, 1)
    ]
    conn.execute("DELETE FROM hdhr_device")
    conn.execute("INSERT INTO hdhr_device (id, device_id, device_uuid) VALUES (1, 'X', 'u')")
    assert conn.execute("SELECT tuner_count FROM hdhr_device").fetchone() == (20,)

    for stmt in down_statements(_MIG.read_text(encoding="utf-8")):
        apply_statement(conn, stmt)
    assert "encoder" not in _cols(conn, "channel_runtime")
    assert not _cols(conn, "streaming_hosts")
    assert _cols(conn, "hdhr_device")["tuner_count"] == "4"
    assert conn.execute("SELECT device_id FROM hdhr_device").fetchall() == [("X",)]
