"""Schema-introspection tests for slots 0012–0027 (Phase 5 batch).

These tests pin the migration shapes that downstream plans rely on:
the `transcripts`, `transcript_segments`, `transcript_units`,
`subtitle_files`, FTS configuration, chapters, and suggestion-terms
tables, plus their indexes, triggers, and view definitions. Each test
reads the migration SQL as text (no live database) and asserts that
the load-bearing tokens are present in the Postgres and SQLite
siblings. Live-database introspection tests will be added when Story
22.4's testcontainers fixture is wired into the pipeline tier
(Story 20.4).
"""

from __future__ import annotations

from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_MIG_DIR = _REPO_ROOT / "shared" / "db" / "migrations"


def _strip_comments(sql: str) -> str:
    """Drop ``-- …`` comments so substring checks don't trip on goose
    directives or doc text."""
    out: list[str] = []
    for line in sql.splitlines():
        stripped = line.lstrip()
        if stripped.startswith("--"):
            continue
        out.append(line)
    return "\n".join(out)


def _load(slot: str, *, sqlite: bool = False) -> str:
    suffix = ".sqlite.sql" if sqlite else ".sql"
    matches = list(_MIG_DIR.glob(f"{slot}_*{suffix}"))
    # The glob matches both `.sql` and `.sqlite.sql` for the non-sqlite
    # variant; filter explicitly.
    if not sqlite:
        matches = [m for m in matches if not m.name.endswith(".sqlite.sql")]
    assert len(matches) == 1, (
        f"expected one migration file for slot {slot} (sqlite={sqlite}), got {matches}"
    )
    return _strip_comments(matches[0].read_text(encoding="utf-8"))


# --- Fixtures -----------------------------------------------------------------


@pytest.fixture(scope="module")
def sql_0012_pg() -> str:
    return _load("0012")


@pytest.fixture(scope="module")
def sql_0012_sqlite() -> str:
    return _load("0012", sqlite=True)


@pytest.fixture(scope="module")
def sql_0013_pg() -> str:
    return _load("0013")


@pytest.fixture(scope="module")
def sql_0013_sqlite() -> str:
    return _load("0013", sqlite=True)


@pytest.fixture(scope="module")
def sql_0014_pg() -> str:
    return _load("0014")


@pytest.fixture(scope="module")
def sql_0015_pg() -> str:
    return _load("0015")


@pytest.fixture(scope="module")
def sql_0015_sqlite() -> str:
    return _load("0015", sqlite=True)


@pytest.fixture(scope="module")
def sql_0016_pg() -> str:
    return _load("0016")


@pytest.fixture(scope="module")
def sql_0016_sqlite() -> str:
    return _load("0016", sqlite=True)


@pytest.fixture(scope="module")
def sql_0017_pg() -> str:
    return _load("0017")


@pytest.fixture(scope="module")
def sql_0017_sqlite() -> str:
    return _load("0017", sqlite=True)


@pytest.fixture(scope="module")
def sql_0018_pg() -> str:
    return _load("0018")


@pytest.fixture(scope="module")
def sql_0018_sqlite() -> str:
    return _load("0018", sqlite=True)


@pytest.fixture(scope="module")
def sql_0019_pg() -> str:
    return _load("0019")


@pytest.fixture(scope="module")
def sql_0019_sqlite() -> str:
    return _load("0019", sqlite=True)


@pytest.fixture(scope="module")
def sql_0020_pg() -> str:
    return _load("0020")


@pytest.fixture(scope="module")
def sql_0020_sqlite() -> str:
    return _load("0020", sqlite=True)


@pytest.fixture(scope="module")
def sql_0021_pg() -> str:
    return _load("0021")


@pytest.fixture(scope="module")
def sql_0022_sqlite() -> str:
    return _load("0022", sqlite=True)


@pytest.fixture(scope="module")
def sql_0023_pg() -> str:
    return _load("0023")


@pytest.fixture(scope="module")
def sql_0024_pg() -> str:
    return _load("0024")


@pytest.fixture(scope="module")
def sql_0025_pg() -> str:
    return _load("0025")


@pytest.fixture(scope="module")
def sql_0025_sqlite() -> str:
    return _load("0025", sqlite=True)


@pytest.fixture(scope="module")
def sql_0026_pg() -> str:
    return _load("0026")


@pytest.fixture(scope="module")
def sql_0026_sqlite() -> str:
    return _load("0026", sqlite=True)


@pytest.fixture(scope="module")
def sql_0027_pg() -> str:
    return _load("0027")


@pytest.fixture(scope="module")
def sql_0027_sqlite() -> str:
    return _load("0027", sqlite=True)


# --- 0012 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0012_creates_transcripts_table(sql_0012_pg: str, sql_0012_sqlite: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS transcripts" in sql_0012_pg
    assert "is_active" in sql_0012_pg
    assert "metadata        JSONB" in sql_0012_pg
    assert "transcripts_video_active_uq" in sql_0012_pg
    assert "WHERE is_active = TRUE" in sql_0012_pg
    # SQLite sibling defines the same table.
    assert "CREATE TABLE IF NOT EXISTS transcripts" in sql_0012_sqlite
    assert "is_active" in sql_0012_sqlite


# --- 0013 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0013_creates_segment_commit_function(sql_0013_pg: str, sql_0013_sqlite: str) -> None:
    assert "CREATE OR REPLACE FUNCTION commit_segment" in sql_0013_pg
    assert (
        "pg_notify(\n        'segments.committed'" in sql_0013_pg
        or "pg_notify('segments.committed'" in sql_0013_pg
    )
    assert "CREATE TABLE IF NOT EXISTS transcript_segments" in sql_0013_pg
    assert "seq" in sql_0013_pg
    assert "committed_at" in sql_0013_pg
    # SQLite has the table but no PL/pgSQL helper.
    assert "CREATE TABLE IF NOT EXISTS transcript_segments" in sql_0013_sqlite


# --- 0014 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0014_speaker_partial_index(sql_0014_pg: str) -> None:
    assert "transcript_segments_tid_speaker_idx" in sql_0014_pg
    assert "WHERE speaker IS NOT NULL" in sql_0014_pg
    assert "CONCURRENTLY" in sql_0014_pg


# --- 0015 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0015_subtitle_files_full_shape(sql_0015_pg: str, sql_0015_sqlite: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS subtitle_files" in sql_0015_pg
    assert "is_external" in sql_0015_pg
    assert "is_embedded" in sql_0015_pg
    assert "track_index" in sql_0015_pg
    assert "CHECK (NOT (is_external AND is_embedded))" in sql_0015_pg
    assert "subtitle_files_internal_uq" in sql_0015_pg
    assert "subtitle_files_embedded_uq" in sql_0015_pg
    # SQLite sibling has the same flags + partial indexes.
    assert "CREATE TABLE IF NOT EXISTS subtitle_files" in sql_0015_sqlite
    assert "is_external" in sql_0015_sqlite
    assert "is_embedded" in sql_0015_sqlite
    assert "subtitle_files_internal_uq" in sql_0015_sqlite
    assert "subtitle_files_embedded_uq" in sql_0015_sqlite


@pytest.mark.unit
def test_0015_subtitle_files_notify_trigger(sql_0015_pg: str) -> None:
    assert "subtitle_files_changed_notify" in sql_0015_pg
    assert "'subtitle_files.changed'" in sql_0015_pg
    assert "subtitle_files_changed_notify_trg" in sql_0015_pg


# --- 0016 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0016_transcript_segments_view(sql_0016_pg: str, sql_0016_sqlite: str) -> None:
    assert "transcript_segments_v" in sql_0016_pg
    assert "VIEW transcript_segments_v" in sql_0016_pg
    assert "t.is_active" in sql_0016_pg
    assert "WHERE t.is_active = TRUE" in sql_0016_pg
    # SQLite sibling defines the same view (boolean spelled as 1).
    assert "transcript_segments_v" in sql_0016_sqlite
    assert "WHERE t.is_active = 1" in sql_0016_sqlite


# --- 0017 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0017_transcript_units_table(sql_0017_pg: str, sql_0017_sqlite: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS transcript_units" in sql_0017_pg
    assert "segment_ids     JSONB" in sql_0017_pg
    assert "transcript_units_lang_idx" in sql_0017_pg
    assert "transcript_units_unindexed_idx" in sql_0017_pg
    assert "WHERE indexed_at IS NULL" in sql_0017_pg
    assert "transcript_units_tid_start_idx" in sql_0017_pg
    assert "(transcript_id, start_sec)" in sql_0017_pg
    # SQLite sibling creates the table.
    assert "CREATE TABLE IF NOT EXISTS transcript_units" in sql_0017_sqlite


# --- 0018 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0018_units_notify_trigger(sql_0018_pg: str, sql_0018_sqlite: str) -> None:
    assert "transcript_units_committed_notify" in sql_0018_pg
    assert "'transcript_units.committed'" in sql_0018_pg
    assert "transcript_units_committed_notify_trg" in sql_0018_pg
    # SQLite is a parity placeholder (`SELECT 1`).
    assert "SELECT 1" in sql_0018_sqlite
    assert "transcript_units_committed_notify" not in sql_0018_sqlite


# --- 0019 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0019_arabic_fts_config(sql_0019_pg: str, sql_0019_sqlite: str) -> None:
    assert "maktaba_normalize" in sql_0019_pg
    assert "CREATE OR REPLACE FUNCTION maktaba_normalize" in sql_0019_pg
    assert "arabic_simple" in sql_0019_pg
    assert "CREATE TEXT SEARCH CONFIGURATION arabic_simple" in sql_0019_pg
    assert "language_to_regconfig" in sql_0019_pg
    # SQLite is a parity placeholder.
    assert "SELECT 1" in sql_0019_sqlite
    assert "maktaba_normalize" not in sql_0019_sqlite


# --- 0020 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0020_sqlite_fts5_table(sql_0020_pg: str, sql_0020_sqlite: str) -> None:
    assert "CREATE VIRTUAL TABLE IF NOT EXISTS transcripts_fts USING fts5" in sql_0020_sqlite
    assert "tokenize = 'unicode61 remove_diacritics 2'" in sql_0020_sqlite
    # Postgres side is a parity placeholder.
    assert "SELECT 1" in sql_0020_pg
    assert "VIRTUAL TABLE" not in sql_0020_pg


# --- 0021 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0021_tsv_generated_column(sql_0021_pg: str) -> None:
    assert "ALTER TABLE transcript_units" in sql_0021_pg
    assert "ADD COLUMN IF NOT EXISTS tsv tsvector" in sql_0021_pg
    assert "GENERATED ALWAYS AS" in sql_0021_pg
    assert "STORED" in sql_0021_pg
    assert "maktaba_normalize" in sql_0021_pg
    assert "language_to_regconfig" in sql_0021_pg


# --- 0022 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0022_sqlite_fts_triggers(sql_0022_sqlite: str) -> None:
    assert "CREATE TRIGGER transcript_units_fts_ai AFTER INSERT" in sql_0022_sqlite
    assert "CREATE TRIGGER transcript_units_fts_ad AFTER DELETE" in sql_0022_sqlite
    assert "CREATE TRIGGER transcript_units_fts_au AFTER UPDATE" in sql_0022_sqlite
    assert "INSERT INTO transcripts_fts" in sql_0022_sqlite
    assert "DELETE FROM transcripts_fts" in sql_0022_sqlite


# --- 0023 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0023_tsv_gin_index(sql_0023_pg: str) -> None:
    assert "transcript_units_tsv_idx" in sql_0023_pg
    assert "USING GIN (tsv)" in sql_0023_pg
    assert "CONCURRENTLY" in sql_0023_pg


# --- 0024 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0024_postgres_compat_view(sql_0024_pg: str) -> None:
    assert "CREATE OR REPLACE VIEW transcripts_fts" in sql_0024_pg
    assert "FROM transcript_units" in sql_0024_pg
    # Column shape must mirror the SQLite FTS5 table (rowid/text/transcript_id/unit_id/language).
    for col in ("rowid", "transcript_id", "unit_id", "language"):
        assert col in sql_0024_pg, f"compat view missing column {col!r}"


# --- 0025 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0025_incremental_indexing(sql_0025_pg: str, sql_0025_sqlite: str) -> None:
    assert "indexed_at_in_chroma" in sql_0025_pg
    assert "ALTER TABLE transcript_units" in sql_0025_pg
    assert "ADD COLUMN IF NOT EXISTS indexed_at_in_chroma" in sql_0025_pg
    assert "CREATE TABLE IF NOT EXISTS vector_index_dead_letter" in sql_0025_pg
    assert "transcript_units_unindexed_chroma_idx" in sql_0025_pg
    # SQLite sibling adds the same column + dead-letter table.
    assert "indexed_at_in_chroma" in sql_0025_sqlite
    assert "CREATE TABLE IF NOT EXISTS vector_index_dead_letter" in sql_0025_sqlite


# --- 0026 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0026_chapters_table(sql_0026_pg: str, sql_0026_sqlite: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS chapters" in sql_0026_pg
    assert "source" in sql_0026_pg
    for src in ("'inferred'", "'embedded'", "'manual'"):
        assert src in sql_0026_pg, f"chapters.source CHECK missing {src!r}"
    # SQLite sibling has same shape.
    assert "CREATE TABLE IF NOT EXISTS chapters" in sql_0026_sqlite
    for src in ("'inferred'", "'embedded'", "'manual'"):
        assert src in sql_0026_sqlite, f"sqlite chapters.source CHECK missing {src!r}"


# --- 0027 ---------------------------------------------------------------------


@pytest.mark.unit
def test_0027_search_suggestion_terms(sql_0027_pg: str, sql_0027_sqlite: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS search_suggestion_terms" in sql_0027_pg
    assert "search_suggestion_terms_prefix_idx" in sql_0027_pg
    assert "text_pattern_ops" in sql_0027_pg
    # SQLite sibling has the table and a plain btree prefix index.
    assert "CREATE TABLE IF NOT EXISTS search_suggestion_terms" in sql_0027_sqlite
    assert "search_suggestion_terms_prefix_idx" in sql_0027_sqlite
