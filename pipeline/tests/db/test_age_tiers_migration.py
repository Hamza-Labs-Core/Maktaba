"""Slot 0220 (both dialects): the age tiers (Epic 57 story 57.01, T09) - the tier columns
on videos and on what holds them, YouTube's age limit, and the recompute's queue. Up, up
again (nothing changes), down, and up once more; the MANIFEST names the slot."""

from __future__ import annotations

import sqlite3

import pytest

from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.runtime import Database
from tests.engine.migrate import (
    MIGRATIONS_DIR,
    apply_sqlite,
    apply_statement,
    down_statements,
    up_statements,
)

SLOT = "0220_age_tiers"
CONTAINERS = ("libraries", "series", "collections", "channels", "youtube_sources")
TRIGGERS = {
    "collection_items_parental",
    "series_episodes_parental",
    "youtube_downloads_parental",
    "videos_parental_ins",
    "videos_parental_upd",
    "media_metadata_enrichment_parental",
    "youtube_videos_parental_ins",
    "youtube_videos_parental_upd",
    "youtube_videos_parental_del",
    "plex_items_parental",
    "channel_programs_parental",
}


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


@pytest.mark.unit
def test_0220_sqlite_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=219)
    sql = (MIGRATIONS_DIR / f"{SLOT}.sqlite.sql").read_text("utf-8")
    assert "age_tier" not in _cols(conn, "videos")
    for _ in range(2):  # the second run changes nothing
        for stmt in up_statements(sql):
            apply_statement(conn, stmt)
    assert {"age_tier", "age_tier_source", "age_tier_admin"} <= _cols(conn, "videos")
    for table in (*CONTAINERS, "channel_programs"):
        assert "age_tier" in _cols(conn, table), table
    assert "age_limit" in _cols(conn, "youtube_videos")
    assert {"kind", "subject", "cursor_at", "cursor_id", "batches"} <= _cols(
        conn, "parental_recompute_jobs"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute("INSERT INTO parental_recompute_jobs (kind) VALUES ('everything')")
    for stmt in down_statements(sql):  # SQLite keeps the columns: nothing reads them there
        apply_statement(conn, stmt)


async def _pg_state(db: Database) -> tuple[set[str], set[str], bool, bool]:
    cols = {
        f"{r['table_name']}.{r['column_name']}"
        for r in await db.fetch(
            "SELECT table_name, column_name FROM information_schema.columns"
            " WHERE table_schema = current_schema()"
            "   AND column_name IN ('age_tier', 'age_tier_source', 'age_tier_admin', 'age_limit')"
        )
    }
    triggers = {
        r["tgname"]
        for r in await db.fetch(
            "SELECT tgname FROM pg_trigger WHERE NOT tgisinternal AND tgname LIKE '%parental%'"
        )
    }
    queue = await db.fetchval("SELECT to_regclass('parental_recompute_jobs') IS NOT NULL")
    enqueue = await db.fetchval(
        "SELECT to_regprocedure('parental_recompute_enqueue(text,text)') IS NOT NULL"
    )
    return cols, triggers, bool(queue), bool(enqueue)


@pytest.mark.asyncio
async def test_0220_postgres_up_down(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("the Postgres file of slot 0220")
    sql = (MIGRATIONS_DIR / f"{SLOT}.sql").read_text("utf-8")
    want = {
        "videos.age_tier",
        "videos.age_tier_source",
        "videos.age_tier_admin",
        "channel_programs.age_tier",
        "youtube_videos.age_limit",
        *(f"{t}.age_tier" for t in CONTAINERS),
    }
    cols, triggers, queue, enqueue = await _pg_state(db)
    assert (cols, triggers, queue, enqueue) == (want, TRIGGERS, True, True)
    # a tier is one of the five, and nobody but the owner calls the enqueue function
    lib = await db.fetchval("INSERT INTO libraries (name, roots) VALUES ('m', '{/m}') RETURNING id")
    with pytest.raises(Exception, match="age_tier"):
        await db.execute("UPDATE libraries SET age_tier = 12 WHERE id = $1", lib)
    assert not await db.fetchval(
        "SELECT has_function_privilege('public', 'parental_recompute_enqueue(text,text)',"
        " 'EXECUTE')"
    )

    for stmt in down_statements(sql):
        await db.execute(stmt)
    assert await _pg_state(db) == (set(), set(), False, False)
    for _ in range(2):  # up again, and a second run changes nothing
        for stmt in up_statements(sql):
            await db.execute(stmt)
        cols, triggers, queue, enqueue = await _pg_state(db)
        assert (cols, triggers, queue, enqueue) == (want, TRIGGERS, True, True)
    # each run asks for the whole home once more; the recompute is idempotent
    assert await db.fetchval("SELECT count(*) FROM parental_recompute_jobs WHERE kind = 'all'") >= 1


@pytest.mark.unit
def test_0220_is_in_the_manifest() -> None:
    manifest = (MIGRATIONS_DIR / "MANIFEST.md").read_text("utf-8")
    row = next(ln for ln in manifest.splitlines() if ln.startswith("| `0220` |"))
    assert "`0220_age_tiers.sql`" in row and "story 57.01" in row
