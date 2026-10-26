"""Slot 0153 (SQLite twin): the public TV site's channels (story 34.1) - the public flag,
the unique slug and the admin's rights confirmation, kept when a channel turns private.
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

_MIG = MIGRATIONS_DIR / "0153_tv_public.sqlite.sql"


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


def test_0153_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=152)
    conn.execute(
        "INSERT INTO channels (id, number, name, slug, mode)"
        " VALUES ('c1', 101, 'One', 'one', 'shuffle'), ('c2', 102, 'Two', 'two', 'shuffle')"
    )
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert {
        "channel_id", "public", "slug", "rights_confirmed_by", "rights_confirmed_at",
        "rights_note", "rights_sig", "updated_at", "updated_by",
    } <= _cols(conn, "tv_public_channels")  # fmt: skip
    conn.execute(
        "INSERT INTO tv_public_channels (channel_id, public, slug, rights_confirmed_by)"
        " VALUES ('c1', 1, 'news', 'admin-1')"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO tv_public_channels (channel_id, slug) VALUES ('c2', 'news')")
    for stmt in down_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not _cols(conn, "tv_public_channels")
