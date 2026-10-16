"""Slots 0087-0091 (plan 06-11): text checks plus live SQLite / Postgres checks.

The live tests apply the shipped migration files (see tests/engine/migrate.py)
to a real SQLite database, and to a real Postgres when MAKTABA_TEST_PG_URL is set.
"""

from __future__ import annotations

import re
import sqlite3
from pathlib import Path

import pytest

from maktaba_pipeline.engine.graph import CATALOG_ID, GRAPH, POOLS
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.engine.migrate import apply_sqlite, list_migrations, up_statements

_MIG = Path(__file__).resolve().parents[3] / "shared" / "db" / "migrations"
_SLOTS = {
    87: "job_stages_registry",
    88: "processing_jobs_subjects",
    89: "queue_control_and_logs",
    90: "media_intel_store",
    91: "drop_legacy_live_indexes",
}
_DIRECTIVE = re.compile(r"^-- \+goose (Up|Down|StatementBegin|StatementEnd|NO TRANSACTION)\s*$")


def _read(slot: int, sqlite: bool) -> str:
    suffix = ".sqlite.sql" if sqlite else ".sql"
    return (_MIG / f"{slot:04d}_{_SLOTS[slot]}{suffix}").read_text(encoding="utf-8")


def _code(sql: str) -> str:
    return "\n".join(ln for ln in sql.splitlines() if not ln.lstrip().startswith("--"))


@pytest.mark.unit
@pytest.mark.parametrize("slot", sorted(_SLOTS))
@pytest.mark.parametrize("sqlite", [False, True])
def test_every_slot_ships_both_dialects_and_goose_safe_prose(slot: int, sqlite: bool) -> None:
    sql = _read(slot, sqlite)
    assert "-- +goose Up" in sql and "-- +goose Down" in sql
    for line in sql.splitlines():
        if "+goose" in line:
            assert _DIRECTIVE.match(line), line


@pytest.mark.unit
def test_manifest_claims_slots() -> None:
    manifest = (_MIG / "MANIFEST.md").read_text(encoding="utf-8")
    for slot in _SLOTS:
        assert f"`{slot:04d}`" in manifest


@pytest.mark.unit
def test_pg_0087_drops_all_three_stage_checks_and_adds_fk() -> None:
    code = _code(_read(87, False))
    for name in (
        "processing_jobs_stage_chk",
        "processing_jobs_stage_valid",
        "processing_jobs_stage_check",
    ):
        assert re.search(rf"DROP CONSTRAINT IF EXISTS {name}", code)
    assert "processing_jobs_stage_fk" in code
    assert "NOT VALID" in code and "VALIDATE CONSTRAINT" in code
    assert "ADD COLUMN IF NOT EXISTS updated_at" in code
    assert "processing_jobs_touch_trg" in code


@pytest.mark.unit
def test_pg_0088_uses_concurrently_and_no_transaction() -> None:
    sql = _read(88, False)
    assert sql.splitlines()[0] == "-- +goose NO TRANSACTION"
    code = _code(sql)
    assert "DROP CONSTRAINT IF EXISTS processing_jobs_scope_chk" in code
    for idx in (
        "processing_jobs_one_live_per_subject_stage",
        "processing_jobs_subject_history_idx",
        "processing_jobs_stage_state_idx",
        "processing_jobs_finished_idx",
        "processing_jobs_failed_cause_idx",
        "processing_jobs_unpropagated_idx",
        "processing_jobs_library_idx",
        "processing_jobs_created_idx",
    ):
        assert re.search(rf"CREATE (UNIQUE )?INDEX CONCURRENTLY IF NOT EXISTS {idx}\b", code), idx
    assert not re.search(r"CREATE (UNIQUE )?INDEX (?!CONCURRENTLY)", code)


@pytest.mark.unit
def test_sqlite_0088_rebuild_keeps_every_index_and_makes_video_id_nullable() -> None:
    code = _code(_read(88, True))
    assert re.search(r"video_id\s+TEXT REFERENCES videos", code)
    for idx in (
        "processing_jobs_claim_idx",
        "processing_jobs_video_stage_idx",
        "processing_jobs_reaper_idx",
        "processing_jobs_pause_pending_idx",
        "processing_jobs_one_live_per_video_stage",
        "processing_jobs_one_live_scan_per_library",
        "processing_jobs_one_live_per_subject_stage",
        "processing_jobs_subject_history_idx",
        "processing_jobs_stage_state_idx",
        "processing_jobs_finished_idx",
        "processing_jobs_failed_cause_idx",
        "processing_jobs_unpropagated_idx",
        "processing_jobs_library_idx",
        "processing_jobs_created_idx",
    ):
        assert idx in code, idx
    assert "SELECT *" not in code


@pytest.mark.unit
def test_0091_drops_both_legacy_indexes() -> None:
    for sqlite in (False, True):
        code = _code(_read(91, sqlite))
        assert "processing_jobs_one_live_per_video_stage" in code
        assert "processing_jobs_one_live_scan_per_library" in code


@pytest.fixture(scope="module")
def schema_at_87() -> sqlite3.Connection:
    """The schema as of slot 0087, built once: applying ~90 twins is the slow part."""
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=87)
    return conn


@pytest.mark.unit
def test_sqlite_rebuild_preserves_existing_rows(schema_at_87: sqlite3.Connection) -> None:
    """Upgrade path: rows written under the old schema survive the 0088 rebuild."""
    conn = sqlite3.connect(":memory:")
    schema_at_87.backup(conn)  # a private copy per test; the template stays untouched
    conn.execute("PRAGMA foreign_keys = ON")
    conn.execute("INSERT INTO libraries (id, name, roots) VALUES ('L1','a','[]')")
    conn.execute(
        "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)"
        " VALUES ('V1','L1','h','/p','f.mp4',1,'2024-01-01T00:00:00Z')"
    )
    conn.execute(
        "INSERT INTO processing_jobs (video_id, stage, state, finished_at)"
        " VALUES ('V1','probe','done','2024-01-02T00:00:00Z')"
    )
    conn.execute("INSERT INTO processing_jobs (video_id, stage) VALUES ('V1','extract')")
    for p in list_migrations(sqlite=True):
        if int(p.name[:4]) >= 88:
            for stmt in up_statements(p.read_text(encoding="utf-8")):
                conn.executescript(stmt if stmt.rstrip().endswith(";") else stmt + ";")
    rows = {
        r[0]: r
        for r in conn.execute(
            "SELECT stage, subject_kind, subject_id, outcome, library_id FROM processing_jobs"
        )
    }
    assert rows["probe"][1:] == ("video", "V1", "changed", "L1")
    assert rows["extract"][1:3] == ("video", "V1")


# ---------------------------------------------------------------- live checks


@pytest.mark.asyncio
async def test_seed_matches_graph(db: Database) -> None:
    rows = await db.fetch(
        "SELECT stage, pool, lane, lane_order, version, legacy, write_mode FROM job_stages"
    )
    seeded = {r["stage"]: r for r in rows}
    assert set(seeded) == {s.name for s in GRAPH}
    for s in GRAPH:
        r = seeded[s.name]
        assert (r["pool"], r["lane"], r["lane_order"], r["version"]) == (
            s.pool,
            s.lane,
            s.lane_order,
            s.version,
        )
        assert bool(r["legacy"]) == s.legacy
        assert r["write_mode"] == "auto"
    pools = {r["pool"] for r in await db.fetch("SELECT pool FROM worker_pools")}
    assert pools == {p.pool for p in POOLS}


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "stage", ["topic_recluster", "topic_assign", "categorize", "chapter_infer"]
)
async def test_f6_epic9_stages_are_accepted(db: Database, stage: str) -> None:
    lib = await make_library(db)
    if stage == "topic_recluster":
        col, kind, sid = "library_id", "library", lib
    else:
        col, kind, sid = "video_id", "video", await make_video(db, lib)
    await db.execute(
        f"INSERT INTO processing_jobs ({col}, stage, subject_kind, subject_id)"
        " VALUES ($1,$2,$3,$1)",
        str(sid),
        stage,
        kind,
    )
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = $1", stage) == 1


@pytest.mark.asyncio
async def test_f7_updated_at_exists(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await db.execute(
        "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id)"
        " VALUES ($1,'probe','video',$1)",
        str(v),
    )
    assert await db.fetchval("SELECT updated_at FROM processing_jobs") is not None
    await db.execute("UPDATE processing_jobs SET priority = 5")
    assert await db.fetchval("SELECT priority FROM processing_jobs") == 5


@pytest.mark.asyncio
async def test_legacy_insert_without_subject_is_bridged(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    await db.execute("INSERT INTO processing_jobs (video_id, stage) VALUES ($1,'probe')", str(v))
    await db.execute("INSERT INTO processing_jobs (library_id, stage) VALUES ($1,'scan')", str(lib))
    rows = {
        r["stage"]: r
        for r in await db.fetch("SELECT stage, subject_kind, subject_id FROM processing_jobs")
    }
    assert rows["probe"]["subject_kind"] == "video"
    assert str(rows["probe"]["subject_id"]) == str(v)
    assert rows["scan"]["subject_kind"] == "library"
    assert str(rows["scan"]["subject_id"]) == str(lib)


@pytest.mark.asyncio
async def test_f5_library_scoped_scan_row_persists(db: Database) -> None:
    lib = await make_library(db)
    await db.execute(
        "INSERT INTO processing_jobs (library_id, video_id, stage, subject_kind, subject_id)"
        " VALUES ($1, NULL, 'scan', 'library', $1)",
        str(lib),
    )
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage='scan'") == 1


@pytest.mark.asyncio
async def test_subject_check_rejects_mismatched_pair(db: Database) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib)
    other = await make_video(db, lib)
    with pytest.raises(Exception):  # noqa: B017, PT011 - asyncpg and sqlite3 raise different types
        await db.execute(
            "INSERT INTO processing_jobs (video_id, stage, subject_kind, subject_id)"
            " VALUES ($1,'probe','video',$2)",
            str(v),
            str(other),
        )


@pytest.mark.asyncio
async def test_one_live_row_per_subject_stage(db: Database) -> None:
    lib = await make_library(db)
    _, season = await make_series_and_season(db, lib)
    sql = (
        "INSERT INTO processing_jobs (season_id, library_id, stage, subject_kind, subject_id)"
        " VALUES ($1,$2,'tag','season',$1)"
    )
    await db.execute(sql, str(season), str(lib))
    with pytest.raises(Exception):  # noqa: B017, PT011
        await db.execute(sql, str(season), str(lib))


@pytest.mark.asyncio
async def test_catalog_subject_pairing(db: Database) -> None:
    await db.execute(
        "INSERT INTO processing_jobs (stage, subject_kind, subject_id)"
        " VALUES ('plan_channels','catalog',$1)",
        str(CATALOG_ID),
    )
    with pytest.raises(Exception):  # noqa: B017, PT011
        await db.execute(
            "INSERT INTO processing_jobs (stage, subject_kind, subject_id)"
            " VALUES ('write_channels','catalog',$1)",
            "00000000-0000-0000-0000-000000000001",
        )


@pytest.mark.asyncio
async def test_new_tables_exist(db: Database) -> None:
    for table in (
        "queue_control", "job_events", "health_samples", "workers", "extensions",
        "video_signals", "youtube_channels", "youtube_videos", "youtube_playlists",
        "youtube_playlist_items", "season_tags", "video_topic_tags", "season_style",
        "external_links", "channel_plans", "series_seasons",
    ):  # fmt: skip
        await db.fetchval(f"SELECT count(*) FROM {table}")
    assert await db.fetchval("SELECT paused FROM queue_control WHERE id = 1") in (0, False)
