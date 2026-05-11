"""Schema-introspection tests for slot 0050 (`transcript_units`) and slot
0051 (`transcript_segments_v`).

Story 5.1 requires a `transcript_units` table with the architecture
§8.1 shape (FK back to `transcripts`, `videos`, `transcript_segments`,
`unit_index` ordinal, `embedding_id` pointer to Chroma) plus a SQLite
parity sibling. Story 4.5 requires a `transcript_segments_v` view that
joins active transcripts.

We assert these against the migration SQL files as text — the same
pattern slot 0004 uses. Live-database introspection lands when Story
22.4's testcontainers fixture is wired in.
"""

from __future__ import annotations

from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_PG_UNITS = _REPO_ROOT / "shared" / "db" / "migrations" / "0050_transcript_units.sql"
_SQLITE_UNITS = _REPO_ROOT / "shared" / "db" / "migrations" / "0050_transcript_units.sqlite.sql"
_PG_VIEW = _REPO_ROOT / "shared" / "db" / "migrations" / "0051_transcript_segments_view.sql"
_SQLITE_VIEW = (
    _REPO_ROOT / "shared" / "db" / "migrations" / "0051_transcript_segments_view.sqlite.sql"
)


@pytest.fixture(scope="module")
def pg_units_sql() -> str:
    return _PG_UNITS.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_units_sql() -> str:
    return _SQLITE_UNITS.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def pg_view_sql() -> str:
    return _PG_VIEW.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def sqlite_view_sql() -> str:
    return _SQLITE_VIEW.read_text(encoding="utf-8")


@pytest.mark.unit
def test_pg_transcript_units_columns(pg_units_sql: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS transcript_units" in pg_units_sql
    for column in (
        "transcript_id   UUID NOT NULL REFERENCES transcripts(id)",
        "video_id        UUID NOT NULL REFERENCES videos(id)",
        "segment_id      BIGINT REFERENCES transcript_segments(id)",
        "unit_index      INT NOT NULL",
        "start_sec       REAL NOT NULL",
        "end_sec         REAL NOT NULL",
        "text            TEXT NOT NULL",
        "embedding_id    TEXT",
    ):
        assert column in pg_units_sql, f"missing column declaration: {column!r}"
    assert "UNIQUE (transcript_id, unit_index)" in pg_units_sql


@pytest.mark.unit
def test_pg_transcript_units_indexes(pg_units_sql: str) -> None:
    for idx in (
        "transcript_units_video_idx",
        "transcript_units_segment_idx",
        "transcript_units_time_idx",
    ):
        assert idx in pg_units_sql, f"missing index: {idx}"


@pytest.mark.unit
def test_sqlite_transcript_units_columns(sqlite_units_sql: str) -> None:
    assert "CREATE TABLE IF NOT EXISTS transcript_units" in sqlite_units_sql
    for column in (
        "transcript_id   TEXT    NOT NULL REFERENCES transcripts(id)",
        "video_id        TEXT    NOT NULL REFERENCES videos(id)",
        "segment_id      INTEGER REFERENCES transcript_segments(id)",
        "unit_index      INTEGER NOT NULL",
        "embedding_id    TEXT",
    ):
        assert column in sqlite_units_sql, f"missing column declaration: {column!r}"
    assert "UNIQUE (transcript_id, unit_index)" in sqlite_units_sql


@pytest.mark.unit
def test_pg_view_joins_active_transcripts(pg_view_sql: str) -> None:
    assert "CREATE OR REPLACE VIEW transcript_segments_v" in pg_view_sql
    assert "JOIN transcripts t ON t.id = s.transcript_id" in pg_view_sql
    assert "t.is_active = true" in pg_view_sql


@pytest.mark.unit
def test_sqlite_view_joins_active_transcripts(sqlite_view_sql: str) -> None:
    assert "CREATE VIEW IF NOT EXISTS transcript_segments_v" in sqlite_view_sql
    assert "JOIN transcripts t ON t.id = s.transcript_id" in sqlite_view_sql
    assert "t.is_active = 1" in sqlite_view_sql
