"""Slot 0120 (SQLite twin): home_secrets, both directions; the Postgres slot revokes the
machines' group role (its real-role test is api/internal/secrets/pg_test.go)."""

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

_MIG = MIGRATIONS_DIR / "0120_home_secrets.sqlite.sql"
_PG = MIGRATIONS_DIR / "0120_home_secrets.sql"
_INSERT = (
    "INSERT INTO home_secrets (name, ciphertext, nonce, key_id) VALUES ('a', x'00', x'01', 'k')"
)


@pytest.fixture(scope="module")
def schema_at_119() -> sqlite3.Connection:
    """Migrated to 119 (setup is not timed against the unit soft cap)."""
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=119)
    return conn


def test_up_and_down(schema_at_119: sqlite3.Connection) -> None:
    conn = schema_at_119
    for stmt in up_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    cols = {r[1]: r[2] for r in conn.execute("PRAGMA table_info(home_secrets)")}
    assert cols == {
        "name": "TEXT",
        "ciphertext": "BLOB",
        "nonce": "BLOB",
        "key_id": "TEXT",
        "updated_at": "TEXT",
        "updated_by": "TEXT",
    }
    conn.execute(_INSERT)
    assert conn.execute("SELECT updated_at FROM home_secrets").fetchone()[0]
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(_INSERT)
    for stmt in down_statements(_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not conn.execute("PRAGMA table_info(home_secrets)").fetchall()


def test_postgres_slot_revokes_the_machines_role() -> None:
    sql = _PG.read_text("utf-8")
    assert "CREATE TABLE IF NOT EXISTS home_secrets" in sql
    assert "REVOKE ALL ON home_secrets FROM maktaba_worker" in sql
    assert "rolname = 'maktaba_worker'" in sql  # guarded like slot 0103
