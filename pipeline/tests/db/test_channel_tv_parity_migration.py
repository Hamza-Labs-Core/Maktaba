"""Slot 0114 (SQLite twin): the channel_programs rebuild keeps its indexes.

A ``RENAME`` keeps the old indexes under their names, so without dropping them first the
``CREATE INDEX IF NOT EXISTS`` after the rebuild are no-ops and the ``DROP TABLE`` of the
aside table removes them, leaving the new table unindexed. Both directions are checked.
"""

from __future__ import annotations

import sqlite3

import pytest

from tests.engine.migrate import MIGRATIONS_DIR, apply_sqlite, apply_statement, down_statements

pytestmark = pytest.mark.unit

_INDEXES = {"channel_programs_channel_time_idx", "channel_programs_channel_seq_uniq"}
_MIG_0114 = MIGRATIONS_DIR / "0114_channel_tv_parity.sqlite.sql"


@pytest.fixture(scope="module")
def schema_at_114() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=114)
    return conn


def _indexes(conn: sqlite3.Connection) -> set[str]:
    rows = conn.execute(
        "SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'channel_programs'"
    ).fetchall()
    return {r[0] for r in rows}


def _copy(src: sqlite3.Connection) -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    src.backup(conn)
    return conn


def test_up_rebuild_keeps_indexes(schema_at_114: sqlite3.Connection) -> None:
    assert _indexes(schema_at_114) >= _INDEXES
    names = {
        r[0] for r in schema_at_114.execute("SELECT name FROM sqlite_master WHERE type='table'")
    }
    assert "channel_programs_old_0113" not in names


def test_down_rebuild_keeps_indexes(schema_at_114: sqlite3.Connection) -> None:
    conn = _copy(schema_at_114)
    # a leftover aside table from an interrupted attempt must not break the rebuild
    conn.execute("CREATE TABLE channel_programs_new_0113 (id TEXT)")
    for stmt in down_statements(_MIG_0114.read_text(encoding="utf-8")):
        apply_statement(conn, stmt)
    assert _indexes(conn) >= _INDEXES
