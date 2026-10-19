"""Slot 0122 (SQLite twin): streaming_hosts gains the cluster columns (the home stays on,
nodes stay off), channel_placements, the generation counter and the cluster stream key.
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

_MIG = MIGRATIONS_DIR / "0122_tv_stream_cluster.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> dict[str, str | None]:
    return {r[1]: r[4] for r in conn.execute(f"PRAGMA table_info({table})")}


@pytest.fixture(scope="module")
def schema_at_122() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=121)
    conn.execute("INSERT INTO streaming_hosts (host_id, host, role) VALUES ('h', 'mac', 'home')")
    conn.execute("INSERT INTO streaming_hosts (host_id, host, role) VALUES ('n', 'pc', 'node')")
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    return conn


def test_0122_up_down(schema_at_122: sqlite3.Connection) -> None:
    conn = schema_at_122
    hosts = _cols(conn, "streaming_hosts")
    assert {
        "enabled",
        "draining",
        "stream_base_url_source",
        "encoders",
        "capacity_override",
        "weight",
        "active",
        "load",
        "can_push",
        "can_live",
        "unfit",
        "version",
    } <= set(hosts)
    assert hosts["stream_base_url_source"] == "'reported'"
    assert hosts["weight"] == "1"
    # the home keeps streaming; a node waits for the admin
    assert conn.execute(
        "SELECT host_id, enabled FROM streaming_hosts ORDER BY host_id"
    ).fetchall() == [
        ("h", 1),
        ("n", 0),
    ]
    assert {"channel_id", "host_id", "generation", "state", "reason", "acked_at"} <= set(
        _cols(conn, "channel_placements")
    )
    assert _cols(conn, "tv_cluster_secret")
    assert _cols(conn, "tv_placement_generation_seq")
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "UPDATE streaming_hosts SET stream_base_url_source = 'guess' WHERE host_id = 'h'"
        )

    for stmt in down_statements(_MIG.read_text(encoding="utf-8")):
        apply_statement(conn, stmt)
    assert "enabled" not in _cols(conn, "streaming_hosts")
    assert "max_active" in _cols(conn, "streaming_hosts")
    assert not _cols(conn, "channel_placements")
    assert not _cols(conn, "tv_cluster_secret")
