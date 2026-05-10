"""Schema-introspection tests for slot 0004 (video state machine).

Story 1.6 requires:

- A CHECK constraint on ``videos.state`` enumerating exactly the 12
  canonical states.
- An idempotent re-assertion of the ``processing_jobs.stage`` CHECK
  with the canonical 7 stages, plus a ``thumb → thumbnail`` rewrite for
  any pre-0002 dump that ships ``thumb`` rows.
- An AFTER UPDATE trigger that fires
  ``pg_notify('videos.state_changed', …)`` on real state changes.
- A SQLite parity sibling rebuilding ``videos`` with the inline CHECK.

We assert these by reading the migration SQL files as text — the same
pattern slot 0002's tests use. Live-database introspection lands when
Story 22.4's testcontainers fixture is wired in.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_PG_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0004_video_states_and_stages.sql"
_SQLITE_MIG = (
    _REPO_ROOT / "shared" / "db" / "migrations" / "0004_video_states_and_stages.sqlite.sql"
)


def _strip_comments(sql: str) -> str:
    out: list[str] = []
    for line in sql.splitlines():
        if line.lstrip().startswith("--"):
            continue
        out.append(line)
    return "\n".join(out)


@pytest.fixture(scope="module")
def pg_sql() -> str:
    return _PG_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def pg_no_comments(pg_sql: str) -> str:
    return _strip_comments(pg_sql)


@pytest.fixture(scope="module")
def sqlite_sql() -> str:
    return _SQLITE_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_no_comments(sqlite_sql: str) -> str:
    return _strip_comments(sqlite_sql)


# -----------------------------------------------------------------
# File presence
# -----------------------------------------------------------------


@pytest.mark.unit
def test_migration_files_exist() -> None:
    assert _PG_MIG.is_file(), f"missing {_PG_MIG}"
    assert _SQLITE_MIG.is_file(), f"missing {_SQLITE_MIG}"


# -----------------------------------------------------------------
# videos.state CHECK constraint — Postgres
# -----------------------------------------------------------------


@pytest.mark.unit
def test_pg_videos_state_check_lists_all_12_states(pg_sql: str) -> None:
    expected = (
        "discovered",
        "probed",
        "audio_extracted",
        "transcribed",
        "indexed",
        "thumbnailed",
        "ready",
        "ready_no_audio",
        "missing",
        "superseded",
        "corrupted",
        "failed",
    )
    # Locate the CHECK block by name and assert each value appears
    # inside it.
    pattern = re.compile(
        r"CONSTRAINT\s+videos_state_valid\s+CHECK\s*\(\s*state\s+IN\s*\(([^)]+)\)\s*\)",
        re.IGNORECASE | re.DOTALL,
    )
    m = pattern.search(pg_sql)
    assert m is not None, "videos_state_valid CHECK missing or malformed"
    found = {s.strip().strip("'") for s in m.group(1).split(",")}
    assert found == set(expected), (
        f"videos.state CHECK members differ: got {found}, want {set(expected)}"
    )


@pytest.mark.unit
def test_pg_drops_then_adds_videos_state_check_for_idempotency(pg_sql: str) -> None:
    # The plan demands DROP IF EXISTS before ADD so re-running 0004 is
    # a no-op.
    assert "DROP CONSTRAINT IF EXISTS videos_state_valid" in pg_sql
    assert "ADD CONSTRAINT videos_state_valid" in pg_sql


# -----------------------------------------------------------------
# processing_jobs.stage CHECK — Postgres re-assertion
# -----------------------------------------------------------------


@pytest.mark.unit
def test_pg_processing_jobs_stage_check_lists_canonical_7(pg_sql: str) -> None:
    expected = ("scan", "probe", "extract", "transcribe", "subtitle_gen", "index", "thumbnail")
    pattern = re.compile(
        r"ADD\s+CONSTRAINT\s+processing_jobs_stage_valid\s+CHECK\s*\(\s*stage\s+IN\s*\(([^)]+)\)\s*\)",
        re.IGNORECASE | re.DOTALL,
    )
    m = pattern.search(pg_sql)
    assert m is not None, "processing_jobs_stage_valid CHECK missing or malformed"
    found = {s.strip().strip("'") for s in m.group(1).split(",")}
    assert found == set(expected)


@pytest.mark.unit
def test_pg_uses_not_valid_then_validate_for_processing_jobs(pg_sql: str) -> None:
    # The plan explicitly calls for NOT VALID + VALIDATE so the write
    # window stays short on the hot table.
    assert "NOT VALID" in pg_sql
    assert "VALIDATE CONSTRAINT processing_jobs_stage_valid" in pg_sql


@pytest.mark.unit
def test_pg_rewrites_legacy_thumb_to_thumbnail(pg_sql: str) -> None:
    # Pre-0002 dumps may contain stage='thumb'; we rewrite before the
    # CHECK lands. (Slot 0002 already prevents new 'thumb' rows.)
    assert re.search(
        r"UPDATE\s+processing_jobs\s+SET\s+stage\s*=\s*'thumbnail'\s+WHERE\s+stage\s*=\s*'thumb'",
        pg_sql,
        re.IGNORECASE,
    )


# -----------------------------------------------------------------
# NOTIFY trigger on videos.state changes — Postgres
# -----------------------------------------------------------------


@pytest.mark.unit
def test_pg_has_state_changed_notify_trigger(pg_sql: str) -> None:
    # Function and trigger must be present.
    assert "videos_state_change_notify()" in pg_sql
    assert "videos_state_change_notify_trg" in pg_sql
    assert "pg_notify" in pg_sql
    assert "'videos.state_changed'" in pg_sql

    # Trigger fires on UPDATE OF state, AFTER UPDATE, FOR EACH ROW.
    assert re.search(
        r"AFTER\s+UPDATE\s+OF\s+state\s+ON\s+videos[\s\S]+?FOR\s+EACH\s+ROW",
        pg_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_notify_payload_carries_canonical_keys(pg_sql: str) -> None:
    for key in ("'video_id'", "'library_id'", "'old_state'", "'new_state'", "'updated_at'"):
        assert key in pg_sql, f"trigger payload missing {key}"


@pytest.mark.unit
def test_pg_notify_filters_no_op_state_writes(pg_sql: str) -> None:
    # The trigger function should fire only on real transitions; spurious
    # UPDATEs that re-write the same value must not produce a notify.
    assert "IS DISTINCT FROM" in pg_sql


# -----------------------------------------------------------------
# Idempotency — re-running the migration is safe
# -----------------------------------------------------------------


@pytest.mark.unit
def test_pg_drop_trigger_uses_if_exists(pg_sql: str) -> None:
    assert "DROP TRIGGER IF EXISTS videos_state_change_notify_trg" in pg_sql


@pytest.mark.unit
def test_pg_function_uses_create_or_replace(pg_sql: str) -> None:
    assert "CREATE OR REPLACE FUNCTION videos_state_change_notify" in pg_sql


# -----------------------------------------------------------------
# SQLite parity
# -----------------------------------------------------------------


@pytest.mark.unit
def test_sqlite_rebuilds_videos_with_state_check(sqlite_sql: str) -> None:
    # SQLite cannot ALTER TABLE … ADD CONSTRAINT, so the parity migration
    # rebuilds via videos__new + INSERT … SELECT + DROP TABLE + RENAME.
    assert "CREATE TABLE IF NOT EXISTS videos__new" in sqlite_sql
    assert "INSERT INTO videos__new" in sqlite_sql
    assert "DROP TABLE IF EXISTS videos" in sqlite_sql
    assert "ALTER TABLE videos__new RENAME TO videos" in sqlite_sql


@pytest.mark.unit
def test_sqlite_inline_check_enumerates_all_12_states(sqlite_sql: str) -> None:
    expected = (
        "discovered",
        "probed",
        "audio_extracted",
        "transcribed",
        "indexed",
        "thumbnailed",
        "ready",
        "ready_no_audio",
        "missing",
        "superseded",
        "corrupted",
        "failed",
    )
    for state in expected:
        assert f"'{state}'" in sqlite_sql, f"state {state!r} missing from SQLite parity"


@pytest.mark.unit
def test_sqlite_recreates_indexes_after_rebuild(sqlite_sql: str) -> None:
    # The slot 0001 / 0007 indexes must be re-created on the new table
    # or queries that depend on them break silently.
    for idx in (
        "videos_library_state_idx",
        "videos_library_path_idx",
        "videos_detected_language_idx",
        "videos_missing_idx",
    ):
        assert f"CREATE INDEX IF NOT EXISTS {idx}" in sqlite_sql, (
            f"missing index re-create: {idx}"
        )


@pytest.mark.unit
def test_sqlite_has_no_notify_trigger(sqlite_sql: str) -> None:
    # SQLite has no LISTEN/NOTIFY; the Python helper publishes manually.
    assert "pg_notify" not in sqlite_sql
    assert "videos_state_change_notify" not in sqlite_sql


@pytest.mark.unit
def test_sqlite_rewrites_legacy_thumb(sqlite_sql: str) -> None:
    assert re.search(
        r"UPDATE\s+processing_jobs\s+SET\s+stage\s*=\s*'thumbnail'\s+WHERE\s+stage\s*=\s*'thumb'",
        sqlite_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_sqlite_disables_fk_during_rebuild(sqlite_sql: str) -> None:
    # The standard SQLite ALTER-via-rebuild pattern toggles
    # foreign_keys off/on around the table swap so child rows aren't
    # cascade-deleted.
    assert "PRAGMA foreign_keys = OFF" in sqlite_sql
    assert "PRAGMA foreign_keys = ON" in sqlite_sql


# -----------------------------------------------------------------
# Cross-binding parity — the SQL CHECK must agree with the Python enum
# -----------------------------------------------------------------


@pytest.mark.unit
def test_pg_check_matches_python_state_enum(pg_sql: str) -> None:
    from maktaba_pipeline.domain.states import State

    pattern = re.compile(
        r"CONSTRAINT\s+videos_state_valid\s+CHECK\s*\(\s*state\s+IN\s*\(([^)]+)\)\s*\)",
        re.IGNORECASE | re.DOTALL,
    )
    m = pattern.search(pg_sql)
    assert m is not None
    sql_values = {s.strip().strip("'") for s in m.group(1).split(",")}
    py_values = {s.value for s in State}
    assert sql_values == py_values
