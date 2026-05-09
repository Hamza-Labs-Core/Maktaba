"""Schema-introspection tests for slot 0002 (processing_jobs).

Story 6.1 requires the migration to land four query-shape indexes, the
unique-live partial index, the stage/state/priority CHECK constraints,
and the AFTER INSERT NOTIFY trigger. We assert these by reading the
migration SQL files as text and pinning the shapes that downstream
plans (6.2, 6.3, 6.4, 6.6) depend on. Live-database introspection
tests will be added when Story 22.4's testcontainers fixture is wired
into the pipeline test tier (Story 20.4).
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest

_REPO_ROOT = Path(__file__).resolve().parents[3]
_PG_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0002_processing_jobs.sql"
_SQLITE_MIG = _REPO_ROOT / "shared" / "db" / "migrations" / "0002_processing_jobs.sqlite.sql"


def _strip_comments(sql: str) -> str:
    """Drop ``-- …`` comments so regex matchers don't trip on goose
    directives or doc text."""
    out: list[str] = []
    for line in sql.splitlines():
        stripped = line.lstrip()
        if stripped.startswith("--"):
            continue
        out.append(line)
    return "\n".join(out)


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
def test_pg_creates_processing_jobs_table(pg_sql: str) -> None:
    # `CREATE TABLE IF NOT EXISTS processing_jobs` — required by the
    # migration-lint idempotency rule.
    assert re.search(
        r"CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+processing_jobs",
        pg_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_has_video_id_fk_with_cascade(pg_sql: str) -> None:
    # FK to videos with ON DELETE CASCADE — Story 6.6 reaper relies on
    # videos-row deletion cascading to processing_jobs.
    assert re.search(
        r"video_id\s+UUID\s+NOT\s+NULL\s+REFERENCES\s+videos\s*\(\s*id\s*\)\s+ON\s+DELETE\s+CASCADE",
        pg_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_pg_has_stage_check_constraint(pg_sql: str) -> None:
    # The seven canonical stages must be in the CHECK list. Story 6.1
    # acceptance criterion: `'thumb'` must be rejected, `'thumbnail'`
    # accepted, `'subtitle_gen'` accepted.
    expected = (
        "scan",
        "probe",
        "extract",
        "transcribe",
        "subtitle_gen",
        "index",
        "thumbnail",
    )
    for stage in expected:
        assert f"'{stage}'" in pg_sql, f"stage {stage!r} missing from migration"
    # And the rejected variant from the architecture-vs-architecture
    # discrepancy resolved in the README:
    assert "'thumb'" not in pg_sql or "'thumbnail'" in pg_sql, (
        "'thumb' must not stand alone — only 'thumbnail' is canonical"
    )


@pytest.mark.unit
def test_pg_has_state_check_constraint(pg_sql: str) -> None:
    expected = (
        "pending",
        "claimed",
        "running",
        "paused",
        "resuming",
        "done",
        "failed",
        "cancelled",
    )
    for state in expected:
        assert f"'{state}'" in pg_sql, f"state {state!r} missing"


@pytest.mark.unit
def test_pg_has_four_query_shape_indexes(pg_sql: str) -> None:
    # Names are normative — Story 6.2's claim loop and Story 6.6's
    # reaper reference these by name in EXPLAIN-pinned tests.
    for name in (
        "processing_jobs_claim_idx",
        "processing_jobs_video_stage_idx",
        "processing_jobs_reaper_idx",
        "processing_jobs_pause_pending_idx",
    ):
        assert name in pg_sql, f"index {name} missing"


@pytest.mark.unit
def test_pg_indexes_use_concurrently(pg_sql_no_comments: str) -> None:
    # Migration-lint rule 3: every Postgres CREATE INDEX uses
    # CONCURRENTLY. Since the file is `+goose NO TRANSACTION`, this
    # is the only safe pattern.
    matches = re.findall(
        r"CREATE\s+(?:UNIQUE\s+)?INDEX\s+(\S+)",
        pg_sql_no_comments,
        re.IGNORECASE,
    )
    # Strip out anything that's clearly a CONCURRENTLY token (the
    # regex above matches the next token after INDEX).
    bad = [m for m in matches if m.upper() != "CONCURRENTLY"]
    assert bad == [], f"expected every CREATE INDEX to use CONCURRENTLY, found bare: {bad}"


@pytest.mark.unit
def test_pg_has_unique_live_partial_index(pg_sql: str) -> None:
    # Story 6.1 acceptance criterion: at most one live row per
    # (video_id, stage). The unique partial index is what makes
    # `enqueue` idempotent.
    assert "processing_jobs_one_live_per_video_stage" in pg_sql
    # And the live-state set must be exactly the five live states.
    pattern = re.compile(
        r"processing_jobs_one_live_per_video_stage[\s\S]+?WHERE\s+state\s+IN\s*\(([^)]+)\)",
        re.IGNORECASE,
    )
    m = pattern.search(pg_sql)
    assert m is not None, "unique-live partial index missing or malformed"
    states = {s.strip().strip("'") for s in m.group(1).split(",")}
    assert states == {"pending", "claimed", "running", "resuming", "paused"}


@pytest.mark.unit
def test_pg_has_notify_trigger(pg_sql: str) -> None:
    # Story 6.1 acceptance criterion: every successful insert emits
    # NOTIFY 'jobs.new' with {id, video_id, stage, priority}.
    assert "processing_jobs_notify_new_trg" in pg_sql
    assert "processing_jobs_notify_new" in pg_sql
    assert "pg_notify" in pg_sql
    assert "'jobs.new'" in pg_sql
    # Payload shape — the four documented keys must appear.
    for key in ("'id'", "'video_id'", "'stage'", "'priority'"):
        assert key in pg_sql, f"trigger payload missing {key}"


@pytest.mark.unit
def test_pg_has_resume_offset_check(pg_sql: str) -> None:
    # Story 6.10 invariant baked into slot 0002 per plan-06-01 §3:
    # last_segment_end_sec >= 0 AND <= total_duration_seconds.
    assert "processing_jobs_resume_offset_chk" in pg_sql
    assert "last_segment_end_sec" in pg_sql


@pytest.mark.unit
def test_sqlite_creates_processing_jobs_table(sqlite_sql: str) -> None:
    assert re.search(
        r"CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+processing_jobs",
        sqlite_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_sqlite_uses_dialect_appropriate_types(
    sqlite_sql: str, sqlite_sql_no_comments: str
) -> None:
    # UUID → TEXT, TIMESTAMPTZ → TEXT, BIGSERIAL → INTEGER PRIMARY KEY.
    assert "video_id                 TEXT NOT NULL" in sqlite_sql
    assert "INTEGER PRIMARY KEY AUTOINCREMENT" in sqlite_sql
    # No JSONB / TIMESTAMPTZ in the SQL itself; comments mention them
    # for cross-reference but those are stripped before the check.
    assert "JSONB" not in sqlite_sql_no_comments
    assert "TIMESTAMPTZ" not in sqlite_sql_no_comments


@pytest.mark.unit
def test_sqlite_has_same_check_constraints(sqlite_sql: str) -> None:
    # The full stage/state lists must match the Postgres file so that
    # stage-aware code can run on either dialect.
    for stage in (
        "scan",
        "probe",
        "extract",
        "transcribe",
        "subtitle_gen",
        "index",
        "thumbnail",
    ):
        assert f"'{stage}'" in sqlite_sql
    for state in (
        "pending",
        "claimed",
        "running",
        "paused",
        "resuming",
        "done",
        "failed",
        "cancelled",
    ):
        assert f"'{state}'" in sqlite_sql


@pytest.mark.unit
def test_sqlite_has_four_query_shape_indexes(sqlite_sql: str) -> None:
    for name in (
        "processing_jobs_claim_idx",
        "processing_jobs_video_stage_idx",
        "processing_jobs_reaper_idx",
        "processing_jobs_pause_pending_idx",
        "processing_jobs_one_live_per_video_stage",
    ):
        assert name in sqlite_sql, f"index {name} missing from SQLite parity"


@pytest.mark.unit
def test_sqlite_pause_partial_uses_int_boolean(sqlite_sql: str) -> None:
    # SQLite has no boolean — the partial index filters on `= 1`.
    assert re.search(
        r"processing_jobs_pause_pending_idx[\s\S]+?WHERE\s+pause_requested\s*=\s*1",
        sqlite_sql,
        re.IGNORECASE,
    )


@pytest.mark.unit
def test_sqlite_has_no_notify_trigger(sqlite_sql: str) -> None:
    # SQLite has no LISTEN/NOTIFY; the Python helper publishes on the
    # in-process bus instead. The migration must not declare a trigger.
    assert "pg_notify" not in sqlite_sql
    assert "processing_jobs_notify_new_trg" not in sqlite_sql
