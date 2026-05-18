"""Schema-introspection tests for slot 0059 (append-only audit_log).

Gap-closure (HLB-359 / HLB-311). `audit_log` was described as an
"append-only security log" since slot 0036 but nothing at the SQL layer
enforced it — any writer could silently UPDATE/DELETE history. Slot
0059 adds a row-level guard:

- Postgres: a ``audit_log_no_mutate()`` PL/pgSQL function that
  ``RAISE EXCEPTION`` and a ``audit_log_append_only_trg`` BEFORE
  UPDATE OR DELETE trigger. INSERT is deliberately *not* guarded so the
  pipeline ``AuditWriter`` (INSERT-only) keeps working.
- SQLite: equivalent BEFORE UPDATE / BEFORE DELETE ``RAISE(ABORT, …)``
  triggers.
- Partitioning is explicitly DEFERRED in-file (declarative range
  partitioning on a populated table is a data-moving migration), not
  silently dropped.

Same text-introspection approach as
``test_jobs_library_scope_migration.py`` — live-DB introspection lands
with Story 22.4's testcontainers fixture.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

_REPO_ROOT = Path(__file__).resolve().parents[3]
_MIG_DIR = _REPO_ROOT / "shared" / "db" / "migrations"
_PG_MIG = _MIG_DIR / "0059_audit_log_append_only.sql"
_SQLITE_MIG = _MIG_DIR / "0059_audit_log_append_only.sqlite.sql"


@pytest.fixture(scope="module")
def pg_sql() -> str:
    return _PG_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_sql() -> str:
    return _SQLITE_MIG.read_text(encoding="utf-8")


def test_migration_files_exist() -> None:
    assert _PG_MIG.is_file(), f"missing {_PG_MIG}"
    assert _SQLITE_MIG.is_file(), f"missing {_SQLITE_MIG}"


def test_pg_declares_guard_function(pg_sql: str) -> None:
    assert re.search(
        r"CREATE\s+OR\s+REPLACE\s+FUNCTION\s+audit_log_no_mutate\s*\(\s*\)",
        pg_sql,
        re.IGNORECASE,
    )
    # Must actually raise (not just return) so the mutation is rejected.
    assert "RAISE EXCEPTION" in pg_sql


def test_pg_trigger_fires_on_update_and_delete_only(pg_sql: str) -> None:
    assert re.search(
        r"CREATE\s+OR\s+REPLACE\s+TRIGGER\s+audit_log_append_only_trg",
        pg_sql,
        re.IGNORECASE,
    )
    assert re.search(
        r"BEFORE\s+UPDATE\s+OR\s+DELETE\s+ON\s+audit_log",
        pg_sql,
        re.IGNORECASE,
    )
    # INSERT must stay open — guarding it would break the pipeline
    # AuditWriter and both api writers (all INSERT-only).
    assert "BEFORE INSERT" not in pg_sql.upper()
    assert "INSERT OR UPDATE" not in pg_sql.upper()
    assert "INSERT OR DELETE" not in pg_sql.upper()


def test_pg_down_drops_trigger_and_function(pg_sql: str) -> None:
    assert "+goose Down" in pg_sql
    assert "DROP TRIGGER IF EXISTS audit_log_append_only_trg ON audit_log" in pg_sql
    assert "DROP FUNCTION IF EXISTS audit_log_no_mutate()" in pg_sql


def test_pg_partitioning_is_documented_deferral(pg_sql: str) -> None:
    low = pg_sql.lower()
    assert "partitioning" in low
    assert "defer" in low
    # No table rewrite slipped in (README §4 forbids it in one slot).
    assert "PARTITION BY" not in pg_sql.upper()
    assert not re.search(r"\bALTER\s+TABLE\s+audit_log\s+RENAME\b", pg_sql, re.IGNORECASE)


def test_sqlite_parity_guards_update_and_delete(sqlite_sql: str) -> None:
    assert re.search(
        r"CREATE\s+TRIGGER\s+IF\s+NOT\s+EXISTS\s+audit_log_no_update_trg",
        sqlite_sql,
        re.IGNORECASE,
    )
    assert re.search(
        r"CREATE\s+TRIGGER\s+IF\s+NOT\s+EXISTS\s+audit_log_no_delete_trg",
        sqlite_sql,
        re.IGNORECASE,
    )
    assert re.search(r"BEFORE\s+UPDATE\s+ON\s+audit_log", sqlite_sql, re.IGNORECASE)
    assert re.search(r"BEFORE\s+DELETE\s+ON\s+audit_log", sqlite_sql, re.IGNORECASE)
    assert "RAISE(ABORT" in sqlite_sql.upper()
    assert "BEFORE INSERT" not in sqlite_sql.upper()


def test_sqlite_down_drops_both_triggers(sqlite_sql: str) -> None:
    assert "+goose Down" in sqlite_sql
    assert "DROP TRIGGER IF EXISTS audit_log_no_delete_trg" in sqlite_sql
    assert "DROP TRIGGER IF EXISTS audit_log_no_update_trg" in sqlite_sql
