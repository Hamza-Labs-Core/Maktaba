"""Story 47.1: slot 0166 (Epic 47) up and down on SQLite and Postgres, the stage rows the
graph defines (off until their story ships, trash_purge on), the ``convert`` pool, the
``dup_group`` subject, and the youtube_sources backfill (mode ``off``)."""

from __future__ import annotations

import asyncio
import os
import sqlite3
import uuid
from typing import Any

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import EPIC47_PRIORITIES, GRAPH, POOLS, get_stage
from maktaba_pipeline.engine.sql import is_pg, loads
from maktaba_pipeline.engine.stages.registry import get_impl
from maktaba_pipeline.engine.subjects import label_of
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.migrate import (
    MIGRATIONS_DIR,
    apply_postgres,
    apply_sqlite,
    apply_statement,
    down_statements,
    up_statements,
)

PG_MIG = MIGRATIONS_DIR / "0166_offline_convert_dedup.sql"
SQ_MIG = MIGRATIONS_DIR / "0166_offline_convert_dedup.sqlite.sql"

NEW_TABLES = (
    "file_actions",
    "video_copies",
    "duplicate_groups",
    "duplicate_members",
    "youtube_sources",
    "youtube_downloads",
)
EPIC47_STAGES = (
    "convert_mp4",
    "dedup_scan",
    "dedup_apply",
    "trash_purge",
    "youtube_fetch",
    "youtube_backfill",
)


# ---------------------------------------------------------------- SQLite up / down


def _tables(conn: sqlite3.Connection) -> set[str]:
    return {r[0] for r in conn.execute("SELECT name FROM sqlite_master WHERE type = 'table'")}


def _cols(conn: sqlite3.Connection, table: str) -> set[str]:
    return {r[1] for r in conn.execute(f"PRAGMA table_info({table})")}


def _seed_sqlite(conn: sqlite3.Connection) -> None:
    conn.execute("INSERT INTO libraries (id, name, roots) VALUES ('l1', 'Channel', '[\"/lib\"]')")
    conn.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url, status)"
        " VALUES ('UC1', 'l1', 'https://youtube.com/@one', 'ok')"
    )
    conn.execute(
        "INSERT INTO youtube_channels (channel_id, library_id, url) VALUES ('UC2', NULL, 'u2')"
    )
    conn.execute(
        "INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage, state)"
        " VALUES ('library', 'l1', 'l1', 'scan', 'done')"
    )


@pytest.mark.integration
def test_0166_sqlite_up_down() -> None:
    conn = sqlite3.connect(":memory:")
    conn.execute("PRAGMA foreign_keys = ON")
    apply_sqlite(conn, upto=165)
    _seed_sqlite(conn)
    for stmt in up_statements(SQ_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)

    assert set(NEW_TABLES) <= _tables(conn)
    assert {"full_hash", "superseded_by"} <= _cols(conn, "videos")
    # the rebuilt processing_jobs kept its rows and every index
    assert conn.execute("SELECT count(*) FROM processing_jobs").fetchone()[0] == 1
    idx = {
        r[0]
        for r in conn.execute(
            "SELECT name FROM sqlite_master WHERE tbl_name = 'processing_jobs'"
            " AND type IN ('index', 'trigger')"
        )
    }
    assert {
        "processing_jobs_one_live_per_subject_stage",
        "processing_jobs_parked_idx",
        "processing_jobs_fill_subject_trg",
        "processing_jobs_touch_trg",
    } <= idx
    # one source per linked channel, off
    src = conn.execute(
        "SELECT library_id, kind, external_id, mode, url, status FROM youtube_sources"
    ).fetchall()
    assert src == [("l1", "channel", "UC1", "off", "https://youtube.com/@one", "ok")]
    # dup_group subjects are accepted, with no video
    conn.execute(
        "INSERT INTO duplicate_groups (id, library_id, tier, members_key)"
        " VALUES ('g1', 'l1', 'exact', 'k')"
    )
    conn.execute(
        "INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage)"
        " VALUES ('dup_group', 'g1', 'l1', 'dedup_apply')"
    )
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO processing_jobs (subject_kind, subject_id, stage)"
            " VALUES ('bogus', 'x', 'scan')"
        )
    # the ledger's checks
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO file_actions (library_id, kind, state) VALUES ('l1', 'convert', 'nope')"
        )
    conn.execute("INSERT INTO file_actions (library_id, kind) VALUES ('l1', 'convert')")
    fa_id = conn.execute("SELECT id, state FROM file_actions").fetchone()
    assert len(fa_id[0]) == 36 and fa_id[1] == "planned"
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO youtube_sources (library_id, kind, url, external_id, mode)"
            " VALUES ('l1', 'channel', 'u', 'UC9', 'sometimes')"
        )
    rows = {
        r[0]: r
        for r in conn.execute("SELECT stage, enabled, disabled_reason, pool FROM job_stages")
    }
    assert rows["trash_purge"][1:] == (1, None, "nas_write")
    assert rows["convert_mp4"][1:] == (0, "not_configured", "convert")

    for stmt in down_statements(SQ_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    assert not set(NEW_TABLES) & _tables(conn)
    assert not {"full_hash", "superseded_by"} & _cols(conn, "videos")
    assert conn.execute("SELECT count(*) FROM processing_jobs").fetchone()[0] == 1
    assert not conn.execute(
        "SELECT 1 FROM job_stages WHERE stage IN ('convert_mp4', 'trash_purge')"
    ).fetchall()
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "INSERT INTO processing_jobs (subject_kind, subject_id, library_id, stage)"
            " VALUES ('dup_group', 'g1', 'l1', 'scan')"
        )


# ---------------------------------------------------------------- Postgres up / down

PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")


@pytest.mark.integration
@pytest.mark.skipif(not PG_URL, reason="MAKTABA_TEST_PG_URL not set")
def test_0166_postgres_up_down_up() -> None:
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    name = "maktaba_m166_" + uuid.uuid4().hex[:8]
    base = PG_URL.rpartition("/")[0]

    async def run() -> None:
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'CREATE DATABASE "{name}"')
        await admin.close()
        conn = await asyncpg.connect(f"{base}/{name}")
        try:
            await apply_postgres(conn, upto=165)
            lib = uuid.uuid4()
            await conn.execute(
                "INSERT INTO libraries (id, name, roots) VALUES ($1, 'Channel', ARRAY['/lib'])",
                lib,
            )
            await conn.execute(
                "INSERT INTO youtube_channels (channel_id, library_id, url, status)"
                " VALUES ('UC1', $1, 'https://youtube.com/@one', 'ok'),"
                " ('UC2', NULL, 'u2', NULL)",
                lib,
            )
            sql = PG_MIG.read_text("utf-8")
            for stmt in up_statements(sql):
                await conn.execute(stmt)
            for t in NEW_TABLES:
                assert await conn.fetchval("SELECT to_regclass($1)", t) is not None
            src = await conn.fetch("SELECT library_id, external_id, mode FROM youtube_sources")
            assert [(r["library_id"], r["external_id"], r["mode"]) for r in src] == [
                (lib, "UC1", "off")
            ]
            gid = await conn.fetchval(
                "INSERT INTO duplicate_groups (library_id, tier, members_key)"
                " VALUES ($1, 'exact', 'k') RETURNING id",
                lib,
            )
            await conn.execute("UPDATE job_stages SET enabled = true WHERE stage = 'dedup_apply'")
            row = await conn.fetchrow(
                "SELECT job_id, outcome"
                " FROM processing_jobs_enqueue('dup_group', $1, 'dedup_apply')",
                gid,
            )
            assert row["outcome"] == "inserted"
            assert (
                await conn.fetchval(
                    "SELECT library_id FROM processing_jobs WHERE id = $1", row["job_id"]
                )
                == lib
            )
            bad = await conn.fetchrow(
                "SELECT outcome FROM processing_jobs_enqueue('dup_group', $1, 'dedup_apply')",
                uuid.uuid4(),
            )
            assert bad["outcome"] == "invalid_subject"
            # the up section runs again harmlessly (a NO TRANSACTION file after a crash)
            for stmt in up_statements(sql):
                await conn.execute(stmt)
            assert await conn.fetchval("SELECT count(*) FROM youtube_sources") == 1

            for stmt in down_statements(sql):
                await conn.execute(stmt)
            for t in NEW_TABLES:
                assert await conn.fetchval("SELECT to_regclass($1)", t) is None
            assert not await conn.fetchval(
                "SELECT count(*) FROM information_schema.columns WHERE table_name = 'videos'"
                " AND column_name IN ('full_hash', 'superseded_by')"
            )
            for stmt in up_statements(sql):
                await conn.execute(stmt)
            assert await conn.fetchval("SELECT count(*) FROM youtube_sources") == 1
        finally:
            await conn.close()
            admin = await asyncpg.connect(PG_URL)
            await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
            await admin.close()

    asyncio.run(run())


# ---------------------------------------------------------------- the graph and the rows


def test_the_graph_has_the_epic47_stages() -> None:
    by = {s.name: s for s in GRAPH}
    assert set(EPIC47_STAGES) <= set(by)
    assert {p.pool for p in POOLS} >= {"convert"}
    convert = by["convert_mp4"]
    assert (convert.pool, convert.placement, convert.subject_kinds) == (
        "convert",
        "home",
        ("video",),
    )
    assert by["dedup_apply"].subject_kinds == ("dup_group",)
    assert by["trash_purge"].pool == "nas_write" and not by["trash_purge"].unreleased
    assert by["youtube_backfill"].pool == by["youtube_fetch"].pool == "network_yt"
    for name in EPIC47_STAGES:
        # no automatic trigger yet: nothing points at them and they point at nothing
        assert not by[name].edges and not by[name].entry_for
        # the YouTube downloads are queued by the listing (story 47.8)
        requesters = {s.name for s in GRAPH if name in s.may_request}
        assert requesters <= ({"youtube_sync"} if name.startswith("youtube_") else set())
        assert all(e.to != name for s in GRAPH for e in s.edges)
        assert get_impl(name) is not None and get_impl(name).run is not None  # type: ignore[union-attr]
    assert EPIC47_PRIORITIES["youtube_fetch"] < 150 < EPIC47_PRIORITIES["youtube_backfill"]


@pytest.mark.asyncio
async def test_the_migrated_rows_are_the_graphs_and_stay_off(db: Database) -> None:
    rows = {
        r["stage"]: r
        for r in await db.fetch(
            "SELECT stage, label, subject_kinds, pool, lane, lane_order, version,"
            " max_attempts, external_write, placement, enabled, disabled_reason"
            " FROM job_stages"
        )
    }
    for name in EPIC47_STAGES:
        s = get_stage(name)
        assert s is not None
        r = rows[name]
        kinds = r["subject_kinds"] if is_pg(db) else loads(r["subject_kinds"])
        assert (r["label"], list(kinds), r["pool"], r["lane"], r["lane_order"]) == (
            s.label,
            list(s.subject_kinds),
            s.pool,
            s.lane,
            s.lane_order,
        )
        assert (r["version"], r["max_attempts"], bool(r["external_write"]), r["placement"]) == (
            s.version,
            s.max_attempts,
            s.external_write,
            s.placement,
        )
        # the migration seeded them off but trash_purge (each one's story turns it on)
        assert bool(r["enabled"]) == (name == "trash_purge")
    # a worker's graph sync keeps the unreleased ones off and trash_purge on
    await sync_graph(db, GRAPH)
    after = {
        r["stage"]: (bool(r["enabled"]), r["disabled_reason"])
        for r in await db.fetch("SELECT stage, enabled, disabled_reason FROM job_stages")
    }
    assert after["convert_mp4"] == (False, "not_configured")
    assert after["youtube_backfill"] == (False, "not_configured")
    assert after["trash_purge"] == (True, None)
    assert after["youtube_fetch"] == (True, None)  # story 47.9 shipped
    pool = await db.fetchrow("SELECT scope, size FROM worker_pools WHERE pool = 'convert'")
    assert (pool["scope"], pool["size"]) == ("global", 1)


async def _group(db: Any, library_id: uuid.UUID) -> uuid.UUID:
    gid = uuid.uuid4()
    await db.execute(
        "INSERT INTO duplicate_groups (id, library_id, tier, members_key)"
        " VALUES ($1, $2, 'exact', 'k')",
        str(gid),
        str(library_id),
    )
    return gid


@pytest.mark.asyncio
async def test_a_dup_group_is_a_subject(db: Database) -> None:
    lib = await make_library(db)
    vid = await make_video(db, lib, title="Lecture one")
    gid = await _group(db, lib)
    await db.execute(
        "INSERT INTO duplicate_members (group_id, video_id, path) VALUES ($1,$2,'/lib/a.mp4')",
        str(gid),
        str(vid),
    )
    # off until story 47.7: the enqueue says so
    off = await enqueue_job(db, subject_kind="dup_group", subject_id=gid, stage="dedup_apply")
    assert (off.job_id, off.outcome) == (None, "disabled")
    await db.execute("UPDATE job_stages SET enabled = $1 WHERE stage = 'dedup_apply'", True)
    res = await enqueue_job(
        db,
        subject_kind="dup_group",
        subject_id=gid,
        stage="dedup_apply",
        priority=EPIC47_PRIORITIES["dedup_apply"],
    )
    assert res.outcome == "inserted" and res.job_id is not None
    row = await db.fetchrow(
        "SELECT subject_kind, library_id, video_id, priority FROM processing_jobs WHERE id = $1",
        res.job_id,
    )
    assert row["subject_kind"] == "dup_group"
    assert str(row["library_id"]) == str(lib) and row["video_id"] is None
    assert row["priority"] == 150
    missing = await enqueue_job(
        db, subject_kind="dup_group", subject_id=uuid.uuid4(), stage="dedup_apply"
    )
    assert missing.outcome == "invalid_subject"
    wrong = await enqueue_job(db, subject_kind="library", subject_id=lib, stage="dedup_apply")
    assert wrong.outcome == "invalid_subject"
    assert await label_of(db, "dup_group", gid) == "Lecture one"
