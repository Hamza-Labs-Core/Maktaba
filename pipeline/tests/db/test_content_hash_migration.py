"""Schema-introspection tests for slot 0003 (content_hash uniqueness).

Story 1.2 / plan-01-02 swaps the global ``UNIQUE (content_hash)`` from
slot 0001 for the per-library form ``UNIQUE (library_id, content_hash)``,
adds a CHECK pinning the 64-lower-hex shape, a content-hash lookup
index, and a GIN over ``metadata->'additional_paths'`` (Postgres only).

These tests assert the migration files declare those shapes by reading
them as text — same approach as ``test_jobs_migration.py``. Live-DB
introspection lands when Story 22.4's testcontainers fixture is wired
in.
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_PG_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0003_videos_content_hash.sql"
_SQLITE_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0003_videos_content_hash.sqlite.sql"
_PG_INIT = _REPO_ROOT / "shared" / "db" / "migrations" / "0001_init_libraries_and_videos.sql"


def _strip_comments(sql: str) -> str:
    """Drop ``-- …`` comment lines to keep regex matchers clean."""
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


@pytest.mark.unit
def test_migration_files_exist() -> None:
    assert _PG_MIG.is_file(), f"missing {_PG_MIG}"
    assert _SQLITE_MIG.is_file(), f"missing {_SQLITE_MIG}"


@pytest.mark.unit
def test_pg_drops_global_unique_constraint(pg_sql_no_comments: str) -> None:
    # Story-01-02 spec: the global UNIQUE on content_hash from slot 0001
    # is replaced by a per-library UNIQUE. The drop is the precondition.
    assert re.search(
        r"DROP\s+CONSTRAINT\s+IF\s+EXISTS\s+videos_content_hash_key",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_creates_per_library_unique_index(pg_sql_no_comments: str) -> None:
    # The replacement: UNIQUE (library_id, content_hash). A unique
    # index acts identically to a UNIQUE constraint for ON CONFLICT,
    # which is how the scanner upserts.
    assert re.search(
        r"CREATE\s+UNIQUE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+"
        r"videos_library_content_hash_key\s*\n?\s*ON\s+videos\s*"
        r"\(\s*library_id\s*,\s*content_hash\s*\)",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_creates_content_hash_lookup_index(pg_sql_no_comments: str) -> None:
    # "Find me every row in this library by content hash" — covers the
    # duplicate-detection path on insert.
    assert re.search(
        r"CREATE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+"
        r"videos_content_hash_lookup_idx",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_adds_format_check_constraint(pg_sql_no_comments: str) -> None:
    # 64 lowercase hex chars. Wrapped in DO/IF NOT EXISTS because PG
    # has no `ALTER TABLE … ADD CONSTRAINT … IF NOT EXISTS`.
    assert "videos_content_hash_format_chk" in pg_sql_no_comments
    assert re.search(
        r"CHECK\s*\(\s*content_hash\s*~\s*'\^\[0-9a-f\]\{64\}\$'\s*\)",
        pg_sql_no_comments,
    )


@pytest.mark.unit
def test_pg_validates_format_check(pg_sql_no_comments: str) -> None:
    # NOT VALID + VALIDATE pattern keeps the table writable while the
    # constraint scan runs. At slot 0003 the table is empty in any
    # practical scenario, but the pattern is correct for future backfills.
    assert re.search(
        r"VALIDATE\s+CONSTRAINT\s+videos_content_hash_format_chk",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_creates_additional_paths_gin_index(pg_sql_no_comments: str) -> None:
    # GIN over metadata->'additional_paths' enables fast "which row
    # owns this path?" lookup after a rename round-trip.
    assert re.search(
        r"CREATE\s+INDEX\s+CONCURRENTLY\s+IF\s+NOT\s+EXISTS\s+"
        r"videos_additional_paths_gin_idx[\s\S]+?USING\s+GIN[\s\S]+?"
        r"metadata\s*->\s*'additional_paths'",
        pg_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_uses_no_transaction_directive(pg_sql: str) -> None:
    # CONCURRENTLY requires NO TRANSACTION; without this directive the
    # migration would fail at apply time on Postgres.
    assert re.search(r"\+goose\s+NO\s+TRANSACTION", pg_sql, re.IGNORECASE)


@pytest.mark.unit
def test_pg_indexes_use_concurrently(pg_sql_no_comments: str) -> None:
    """Migration-lint rule: every Postgres CREATE INDEX uses CONCURRENTLY."""
    matches = re.findall(
        r"CREATE\s+(?:UNIQUE\s+)?INDEX\s+(\S+)",
        pg_sql_no_comments,
        re.IGNORECASE,
    )
    bad = [m for m in matches if m.upper() != "CONCURRENTLY"]
    assert bad == [], f"expected every CREATE INDEX to use CONCURRENTLY, found bare: {bad}"


@pytest.mark.unit
def test_pg_down_restores_global_unique(pg_sql: str) -> None:
    # Down-migration is dev-only but must be a proper inverse of the
    # Up so local rollbacks work. Read the raw SQL (with comments) so
    # the `-- +goose Down` directive — which the comment-stripper
    # discards — is still visible as a section delimiter.
    parts = pg_sql.split("+goose Down")
    assert len(parts) == 2, "missing +goose Down section"
    down_section = parts[1]
    assert "videos_content_hash_key" in down_section
    assert "UNIQUE (content_hash)" in down_section
    assert "DROP INDEX IF EXISTS videos_additional_paths_gin_idx" in down_section
    assert "DROP INDEX IF EXISTS videos_content_hash_lookup_idx" in down_section


@pytest.mark.unit
def test_sqlite_rebuilds_videos_table(sqlite_sql_no_comments: str) -> None:
    """SQLite has no DROP CONSTRAINT — rebuild via the 12-step procedure.

    Pin: a scratch table is created, data is copied, the original is
    dropped, and the scratch is renamed back to ``videos``.
    """
    sql = sqlite_sql_no_comments
    assert "_videos_03_rebuild" in sql
    assert re.search(
        r"CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+_videos_03_rebuild",
        sql,
        re.IGNORECASE,
    )
    assert re.search(
        r"INSERT\s+INTO\s+_videos_03_rebuild[\s\S]+?FROM\s+videos",
        sql,
        re.IGNORECASE,
    )
    assert re.search(r"DROP\s+TABLE\s+videos", sql, re.IGNORECASE)
    assert re.search(
        r"ALTER\s+TABLE\s+_videos_03_rebuild\s+RENAME\s+TO\s+videos",
        sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_sqlite_drops_leftover_rebuild_table_first(sqlite_sql_no_comments: str) -> None:
    """A leftover ``_videos_03_rebuild`` from a partial previous run is
    dropped at the top so the migration is re-runnable."""
    # Match the FIRST DROP TABLE statement in the file — it must be
    # the cleanup of the rebuild scratch table. The capture group
    # excludes punctuation so the trailing semicolon doesn't bleed in.
    first_drop = re.search(
        r"DROP\s+TABLE\s+IF\s+EXISTS\s+([A-Za-z_][A-Za-z0-9_]*)",
        sqlite_sql_no_comments,
        re.IGNORECASE,
    )
    assert first_drop is not None
    assert first_drop.group(1) == "_videos_03_rebuild"


@pytest.mark.unit
def test_sqlite_uses_per_library_unique(sqlite_sql_no_comments: str) -> None:
    # Table-level UNIQUE on (library_id, content_hash) replaces the
    # column-level UNIQUE from slot 0001. The rebuild also drops the
    # old auto-named UNIQUE index because the original table is gone.
    assert re.search(
        r"UNIQUE\s*\(\s*library_id\s*,\s*content_hash\s*\)",
        sqlite_sql_no_comments,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_sqlite_rebuilt_table_has_no_column_level_unique_on_content_hash(
    sqlite_sql_no_comments: str,
) -> None:
    # Find the rebuilt-table's content_hash column declaration; it must
    # NOT have UNIQUE attached. (The whole point of the rebuild.)
    m = re.search(
        r"_videos_03_rebuild[\s\S]+?content_hash\s+TEXT\s+NOT\s+NULL([^,\n]*)",
        sqlite_sql_no_comments,
        re.IGNORECASE,
    )
    assert m is not None, "rebuilt table missing content_hash column"
    assert "UNIQUE" not in m.group(1).upper(), (
        f"column-level UNIQUE leaked into rebuild: {m.group(0)!r}"
    )


@pytest.mark.unit
def test_sqlite_has_format_check(sqlite_sql_no_comments: str) -> None:
    # SQLite's regex support is limited; we use length + GLOB negation.
    # The result is the same shape constraint as Postgres'.
    assert "length(content_hash) = 64" in sqlite_sql_no_comments
    assert "content_hash NOT GLOB '*[^0-9a-f]*'" in sqlite_sql_no_comments


@pytest.mark.unit
def test_sqlite_recreates_slot_0001_indexes(sqlite_sql_no_comments: str) -> None:
    # The rebuild dropped the original table and its indexes; the
    # follow-up CREATE INDEX statements must rebuild every index from
    # slot 0001 plus the new lookup index.
    for name in (
        "videos_library_state_idx",
        "videos_library_path_idx",
        "videos_detected_language_idx",
        "videos_content_hash_lookup_idx",
    ):
        assert re.search(
            rf"CREATE\s+INDEX\s+IF\s+NOT\s+EXISTS\s+{name}",
            sqlite_sql_no_comments,
            re.IGNORECASE,
        ), f"missing CREATE INDEX for {name}"


@pytest.mark.unit
def test_sqlite_defers_foreign_keys_during_rebuild(sqlite_sql_no_comments: str) -> None:
    # Standard SQLite rebuild dance: PRAGMA defer_foreign_keys = ON so
    # the FK from `videos.library_id → libraries.id` doesn't trip
    # during the temporary period when the row is in the scratch table.
    assert "PRAGMA defer_foreign_keys = ON" in sqlite_sql_no_comments


@pytest.mark.unit
def test_sqlite_no_jsonb_or_timestamptz_types(sqlite_sql_no_comments: str) -> None:
    # Parity sanity check (mirrors the slot-0002 test). SQLite has
    # neither type; mentioning them in the SQL would mean a regression
    # against the dialect-substitution pattern documented in
    # 0001's SQLite parity sibling.
    assert "JSONB" not in sqlite_sql_no_comments
    assert "TIMESTAMPTZ" not in sqlite_sql_no_comments


@pytest.mark.unit
def test_slot_0003_listed_in_manifest() -> None:
    manifest = (_REPO_ROOT / "shared" / "db" / "migrations" / "MANIFEST.md").read_text(
        encoding="utf-8"
    )
    # Pin the row shape: slot, filename, owning plan link, depends on,
    # one-liner. Tolerate whitespace; the column separators are pipes.
    assert re.search(
        r"\|\s*`0003`\s*\|\s*`0003_videos_content_hash\.sql`\s*\|"
        r"\s*\[plan-01-02\]",
        manifest,
    )
