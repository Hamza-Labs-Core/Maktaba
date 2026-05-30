"""Schema-introspection tests for slot 0058 (library-scoped SCAN jobs).

Gap-closure (HLB-257/255) decouples library-scoped jobs from the
per-video ``processing_jobs.video_id NOT NULL`` invariant so the real
SCAN stage can enqueue one job per *library* (no video exists yet at
scan time). The additive migration must:

- add ``processing_jobs.library_id UUID NULL REFERENCES libraries(id)
  ON DELETE CASCADE``,
- drop ``NOT NULL`` on ``processing_jobs.video_id`` (make it nullable),
- add a partial UNIQUE index ``processing_jobs_one_live_scan_per_library``
  on ``(library_id, stage)`` for ``stage='scan'`` restricted to the
  *same* five live states the per-video unique index uses,
- keep the existing ``(video_id, stage)`` per-video unique index intact
  (PROBE/EXTRACT/TRANSCRIBE unaffected),
- add a CHECK that scan jobs carry ``library_id`` (and ``video_id``
  null) while every other stage carries ``video_id``.

Same text-introspection approach as ``test_jobs_migration.py`` —
live-DB introspection lands with Story 22.4's testcontainers fixture.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

pytestmark = pytest.mark.unit

_REPO_ROOT = Path(__file__).resolve().parents[3]
_MIG_DIR = _REPO_ROOT / "shared" / "db" / "migrations"
_PG_MIG = _MIG_DIR / "0058_processing_jobs_library_scoped.sql"
_SQLITE_MIG = _MIG_DIR / "0058_processing_jobs_library_scoped.sqlite.sql"


def _strip_comments(sql: str) -> str:
    """Drop ``-- …`` comment lines so regex matchers stay clean."""
    return "\n".join(line for line in sql.splitlines() if not line.lstrip().startswith("--"))


@pytest.fixture(scope="module")
def pg_sql() -> str:
    return _PG_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def pg_sql_no_comments(pg_sql: str) -> str:
    return _strip_comments(pg_sql)


@pytest.fixture(scope="module")
def sqlite_sql() -> str:
    return _SQLITE_MIG.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_sql_no_comments(sqlite_sql: str) -> str:
    return _strip_comments(sqlite_sql)


def test_migration_files_exist() -> None:
    assert _PG_MIG.is_file(), f"missing {_PG_MIG}"
    assert _SQLITE_MIG.is_file(), f"missing {_SQLITE_MIG}"


def test_pg_adds_library_id_fk_nullable_cascade(pg_sql_no_comments: str) -> None:
    # Additive nullable FK to libraries with ON DELETE CASCADE so
    # deleting a library reaps its scan jobs.
    assert re.search(
        r"ADD\s+COLUMN\s+IF\s+NOT\s+EXISTS\s+library_id\s+UUID",
        pg_sql_no_comments,
        re.IGNORECASE,
    )
    assert re.search(
        r"library_id[\s\S]{0,80}REFERENCES\s+libraries\s*\(\s*id\s*\)\s+ON\s+DELETE\s+CASCADE",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


def test_pg_drops_video_id_not_null(pg_sql_no_comments: str) -> None:
    # video_id becomes nullable — a SCAN job has no video yet. DROP NOT
    # NULL is a metadata-only catalog flip (no table scan), unlike SET
    # NOT NULL which the migration-lint long-running rule forbids.
    assert re.search(
        r"ALTER\s+TABLE\s+processing_jobs\s+ALTER\s+COLUMN\s+video_id\s+DROP\s+NOT\s+NULL",
        pg_sql_no_comments,
        re.IGNORECASE,
    )
    # And we must NOT re-introduce SET NOT NULL anywhere in the up path.
    assert "SET NOT NULL" not in pg_sql_no_comments.upper()


def test_pg_adds_one_live_scan_per_library_partial_unique(
    pg_sql: str, pg_sql_no_comments: str
) -> None:
    assert "processing_jobs_one_live_scan_per_library" in pg_sql
    # Restricted to scan stage… (match against the comment-stripped SQL
    # so prose mentioning `state IN (…)` can't shadow the real DDL).
    m = re.search(
        r"CREATE\s+UNIQUE\s+INDEX[\s\S]+?processing_jobs_one_live_scan_per_library"
        r"[\s\S]+?WHERE\s+([\s\S]+?);",
        pg_sql_no_comments,
        re.IGNORECASE,
    )
    assert m is not None, "partial unique index missing or malformed"
    where = m.group(1)
    assert "stage = 'scan'" in where or "stage='scan'" in where.replace(" ", "")
    # …and the same five live states the per-video index uses.
    states_m = re.search(r"state\s+IN\s*\(([^)]+)\)", where, re.IGNORECASE)
    assert states_m is not None
    states = {s.strip().strip("'") for s in states_m.group(1).split(",")}
    assert states == {"pending", "claimed", "running", "resuming", "paused"}


def test_pg_index_uses_concurrently(pg_sql_no_comments: str) -> None:
    # Migration-lint rule 3: every Postgres CREATE INDEX uses
    # CONCURRENTLY (file is non-transactional).
    for m in re.finditer(
        r"CREATE\s+(?:UNIQUE\s+)?INDEX\s+(\S+)",
        pg_sql_no_comments,
        re.IGNORECASE,
    ):
        assert m.group(1).upper() == "CONCURRENTLY", (
            f"CREATE INDEX must use CONCURRENTLY, found bare token {m.group(1)!r}"
        )


def test_pg_keeps_per_video_unique_index_intact(pg_sql: str) -> None:
    # The migration must NOT drop the existing per-video unique index —
    # PROBE/EXTRACT/TRANSCRIBE idempotency depends on it.
    assert "DROP INDEX IF EXISTS processing_jobs_one_live_per_video_stage" not in pg_sql, (
        "must not drop the per-video unique index in the up path"
    )


def test_pg_adds_scope_check_constraint(pg_sql: str) -> None:
    # scan ⇒ library_id set + video_id null; every other stage ⇒
    # video_id set. Guarded by a DO/pg_constraint block (Postgres has
    # no ADD CONSTRAINT IF NOT EXISTS) — same pattern slot 0003 uses.
    assert "processing_jobs_scope_chk" in pg_sql
    flat = " ".join(pg_sql.split())
    assert "stage = 'scan'" in flat
    assert "library_id IS NOT NULL" in flat
    assert "video_id IS NULL" in flat
    assert "video_id IS NOT NULL" in flat


def test_pg_down_drops_additive_objects(pg_sql: str) -> None:
    # Down is dev-only (README §6) and need not re-assert video_id NOT
    # NULL (that direction scans the table — lint-forbidden). It must
    # still drop every object the up path added.
    assert "+goose Down" in pg_sql
    assert "DROP INDEX IF EXISTS processing_jobs_one_live_scan_per_library" in pg_sql
    assert "DROP CONSTRAINT IF EXISTS processing_jobs_scope_chk" in pg_sql
    assert re.search(
        r"ALTER\s+TABLE\s+processing_jobs\s+DROP\s+COLUMN\s+IF\s+EXISTS\s+library_id",
        pg_sql,
        re.IGNORECASE,
    )


def test_sqlite_parity_shapes(sqlite_sql: str, sqlite_sql_no_comments: str) -> None:
    # SQLite cannot DROP NOT NULL via ALTER; the parity sibling
    # documents the divergence (fresh SQLite installs build the column
    # nullable from the start in a future consolidated baseline). It
    # must still ship the library_id column add + the partial unique
    # index so stage-aware code runs on either dialect.
    assert "library_id" in sqlite_sql
    assert "processing_jobs_one_live_scan_per_library" in sqlite_sql
    assert "JSONB" not in sqlite_sql_no_comments
    assert "TIMESTAMPTZ" not in sqlite_sql_no_comments
    # SQLite partial index also keyed on stage='scan' + the five live
    # states.
    m = re.search(
        r"CREATE\s+UNIQUE\s+INDEX[\s\S]+?processing_jobs_one_live_scan_per_library"
        r"[\s\S]+?WHERE\s+([\s\S]+?);",
        sqlite_sql_no_comments,
        re.IGNORECASE,
    )
    assert m is not None
    states_m = re.search(r"state\s+IN\s*\(([^)]+)\)", m.group(1), re.IGNORECASE)
    assert states_m is not None
    states = {s.strip().strip("'") for s in states_m.group(1).split(",")}
    assert states == {"pending", "claimed", "running", "resuming", "paused"}
