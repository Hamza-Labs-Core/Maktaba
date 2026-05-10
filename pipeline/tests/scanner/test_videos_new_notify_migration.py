"""Schema-introspection tests for slot 0005 (``videos.new`` NOTIFY).

Story 1.1 AC2 — every successful insert into ``videos`` must produce
exactly one ``videos.new`` notification, so the WebSocket frame count
on ``/ws/library/{id}`` matches the inserted-row count. The trigger in
slot 0005 is what enforces that invariant. Live-Postgres assertions
land with Story 22.4's testcontainers fixture; until then we pin the
migration's shape by reading its SQL text.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_PG_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0005_videos_new_notify.sql"
_SQLITE_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0005_videos_new_notify.sqlite.sql"


@pytest.fixture(scope="module")
def pg_sql() -> str:
    return _PG_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_sql() -> str:
    return _SQLITE_MIG.read_text(encoding="utf-8")


@pytest.mark.unit
def test_migration_files_exist() -> None:
    assert _PG_MIG.is_file(), f"missing {_PG_MIG}"
    assert _SQLITE_MIG.is_file(), f"missing {_SQLITE_MIG}"


@pytest.mark.unit
def test_pg_creates_notify_function(pg_sql: str) -> None:
    assert re.search(
        r"CREATE\s+OR\s+REPLACE\s+FUNCTION\s+videos_notify_new\s*\(\s*\)",
        pg_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_function_calls_pg_notify_with_videos_new_channel(pg_sql: str) -> None:
    assert "pg_notify" in pg_sql
    assert "'videos.new'" in pg_sql


@pytest.mark.unit
def test_pg_payload_carries_documented_keys(pg_sql: str) -> None:
    """The payload shape is consumed by the API's WS layer; pin every key."""
    for key in (
        "'id'",
        "'library_id'",
        "'content_hash'",
        "'path'",
        "'filename'",
        "'state'",
    ):
        assert key in pg_sql, f"trigger payload missing {key}"


@pytest.mark.unit
def test_pg_trigger_fires_after_insert_on_videos(pg_sql: str) -> None:
    assert re.search(
        r"CREATE\s+OR\s+REPLACE\s+TRIGGER\s+videos_notify_new_trg\s+AFTER\s+INSERT\s+ON\s+videos\s+FOR\s+EACH\s+ROW",
        pg_sql,
        re.IGNORECASE | re.DOTALL,
    )


@pytest.mark.unit
def test_pg_drop_trigger_before_create_for_idempotency(pg_sql: str) -> None:
    """Re-running the migration must be a no-op."""
    assert "DROP TRIGGER IF EXISTS videos_notify_new_trg" in pg_sql


@pytest.mark.unit
def test_pg_down_drops_trigger_and_function(pg_sql: str) -> None:
    down = pg_sql.split("-- +goose Down", 1)[1]
    assert "DROP TRIGGER IF EXISTS videos_notify_new_trg" in down
    assert "DROP FUNCTION IF EXISTS videos_notify_new" in down


@pytest.mark.unit
def test_sqlite_has_no_pg_notify(sqlite_sql: str) -> None:
    """SQLite has no LISTEN/NOTIFY; the Python helper publishes on the bus."""
    assert "pg_notify" not in sqlite_sql
    assert "videos_notify_new" not in sqlite_sql
