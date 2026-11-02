"""Slot 0172: a stage pause always has an owner (2026-11-02).

The home's extract stage had been paused since 2026-10-22 by a bare ``UPDATE job_stages
SET paused = true`` run by hand: no ``paused_at``, no ``paused_by``, so nothing could
release it (the share-path conversion resumes only its own pauses). Slot 0172 resumes such
a pause with an audit row, and a trigger refuses any new pause without both columns. Both
dialects: SQLite always, Postgres when ``MAKTABA_TEST_PG_URL`` is set.
"""

from __future__ import annotations

import asyncio
import json
import os
import sqlite3
import uuid

import pytest

from tests.engine.migrate import MIGRATIONS_DIR, apply_sqlite, apply_statement, up_statements

PG_MIG = MIGRATIONS_DIR / "0172_stage_pause_owner_fair_claim.sql"
SQ_MIG = MIGRATIONS_DIR / "0172_stage_pause_owner_fair_claim.sqlite.sql"
PG_URL = os.environ.get("MAKTABA_TEST_PG_URL", "")
ACTION = "queue.stage_orphan_pause_resumed"

# extract: paused by nobody (the home's row). probe: an owner with no time. thumbnail:
# an admin's pause, as the API writes it. scan: not paused.
ORPHAN, NO_TIME, ADMIN, RUNNING = "extract", "probe", "thumbnail", "scan"


# ---------------------------------------------------------------- SQLite


def _sqlite_before() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    apply_sqlite(conn, upto=171)
    conn.execute("UPDATE job_stages SET paused = 1 WHERE stage = ?", (ORPHAN,))
    conn.execute(
        "UPDATE job_stages SET paused = 1, paused_by = 'admin' WHERE stage = ?", (NO_TIME,)
    )
    conn.execute(
        "UPDATE job_stages SET paused = 1, paused_at = '2026-10-30T10:00:00Z',"
        " paused_by = 'user:00000000-0000-0000-0000-000000000001' WHERE stage = ?",
        (ADMIN,),
    )
    conn.commit()
    return conn


def _sqlite_up(conn: sqlite3.Connection) -> None:
    for stmt in up_statements(SQ_MIG.read_text("utf-8")):
        apply_statement(conn, stmt)
    conn.commit()


def _sqlite_row(conn: sqlite3.Connection, stage: str) -> tuple[int, str | None, str | None]:
    row = conn.execute(
        "SELECT paused, paused_at, paused_by FROM job_stages WHERE stage = ?", (stage,)
    ).fetchone()
    return int(row[0]), row[1], row[2]


def test_sqlite_an_orphaned_pause_is_resumed_with_an_audit_row() -> None:
    conn = _sqlite_before()
    _sqlite_up(conn)
    assert _sqlite_row(conn, ORPHAN) == (0, None, None)
    paused, at, by = _sqlite_row(conn, NO_TIME)
    assert (paused, by) == (1, "admin") and at  # kept, with its row's updated_at as time
    assert _sqlite_row(conn, ADMIN) == (
        1,
        "2026-10-30T10:00:00Z",
        "user:00000000-0000-0000-0000-000000000001",
    )
    audit = conn.execute(
        "SELECT category, target_kind, target_id, payload FROM audit_log WHERE action = ?",
        (ACTION,),
    ).fetchall()
    assert [(a[0], a[1], a[2]) for a in audit] == [("admin", "job_stage", ORPHAN)]
    assert json.loads(audit[0][3])["migration"] == "0172"
    # run again (a retried migration): nothing more to repair, no second audit row
    _sqlite_up(conn)
    assert (
        conn.execute("SELECT count(*) FROM audit_log WHERE action = ?", (ACTION,)).fetchone()[0]
        == 1
    )


def test_sqlite_no_writer_can_pause_a_stage_without_an_owner() -> None:
    conn = _sqlite_before()
    _sqlite_up(conn)
    with pytest.raises(sqlite3.IntegrityError, match="paused without paused_at and paused_by"):
        conn.execute("UPDATE job_stages SET paused = 1 WHERE stage = ?", (RUNNING,))
    with pytest.raises(sqlite3.IntegrityError):
        conn.execute(
            "UPDATE job_stages SET paused = 1, paused_at = '2026-11-02T00:00:00Z' WHERE stage = ?",
            (RUNNING,),
        )
    insert = (
        "INSERT INTO job_stages (stage, label, subject_kinds, pool, lane, lane_order,"
        " paused{cols})"
        " VALUES ('x_new', 'X', json_array('video'), 'cpu', 'media', 99, 1{vals})"
    )
    with pytest.raises(sqlite3.IntegrityError, match="paused without paused_at and paused_by"):
        conn.execute(insert.format(cols="", vals=""))
    # the same row with an owner goes in: the refusal above was the trigger's
    conn.execute(
        insert.format(cols=", paused_at, paused_by", vals=", '2026-11-02T00:00:00Z', 'admin'")
    )
    # the API's pause and the share-path conversion's (both columns) still work
    conn.execute(
        "UPDATE job_stages SET paused = 1, paused_at = '2026-11-02T00:00:00Z',"
        " paused_by = 'storage_canonicalize' WHERE stage = ?",
        (RUNNING,),
    )
    # a resume clears both, and editing a paused stage's other columns is fine
    conn.execute("UPDATE job_stages SET label = 'Scan!' WHERE stage = ?", (RUNNING,))
    conn.execute(
        "UPDATE job_stages SET paused = 0, paused_at = NULL, paused_by = NULL WHERE stage = ?",
        (RUNNING,),
    )
    assert _sqlite_row(conn, RUNNING) == (0, None, None)


# ---------------------------------------------------------------- Postgres


@pytest.mark.integration
@pytest.mark.skipif(not PG_URL, reason="MAKTABA_TEST_PG_URL not set")
def test_postgres_an_orphaned_pause_is_resumed_and_no_writer_can_make_one() -> None:
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    from tests.engine.migrate import apply_postgres

    name = "maktaba_m172_" + uuid.uuid4().hex[:8]
    base = PG_URL.rpartition("/")[0]

    async def run() -> None:
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'CREATE DATABASE "{name}"')
        await admin.close()
        conn = await asyncpg.connect(f"{base}/{name}")
        try:
            await apply_postgres(conn, upto=171)
            await conn.execute("UPDATE job_stages SET paused = true WHERE stage = $1", ORPHAN)
            await conn.execute(
                "UPDATE job_stages SET paused = true, paused_by = 'admin' WHERE stage = $1",
                NO_TIME,
            )
            await conn.execute(
                "UPDATE job_stages SET paused = true, paused_at = now(), paused_by = 'user:x'"
                " WHERE stage = $1",
                ADMIN,
            )
            sql = PG_MIG.read_text("utf-8")
            for stmt in up_statements(sql):
                await conn.execute(stmt)

            async def row(stage: str) -> tuple[bool, object, object]:
                r = await conn.fetchrow(
                    "SELECT paused, paused_at, paused_by FROM job_stages WHERE stage = $1", stage
                )
                return bool(r["paused"]), r["paused_at"], r["paused_by"]

            assert await row(ORPHAN) == (False, None, None)
            paused, at, by = await row(NO_TIME)
            assert paused and by == "admin" and at is not None
            paused, at, by = await row(ADMIN)
            assert paused and by == "user:x" and at is not None
            audit = await conn.fetch(
                "SELECT category, target_kind, target_id, actor_source, payload"
                " FROM audit_log WHERE action = $1",
                ACTION,
            )
            assert [(a["category"], a["target_kind"], a["target_id"]) for a in audit] == [
                ("admin", "job_stage", ORPHAN)
            ]
            assert audit[0]["actor_source"] == "migration"
            assert json.loads(audit[0]["payload"])["migration"] == "0172"

            # a NO TRANSACTION file cut part way runs again whole: harmless twice
            for stmt in up_statements(sql):
                await conn.execute(stmt)
            assert (
                await conn.fetchval("SELECT count(*) FROM audit_log WHERE action = $1", ACTION) == 1
            )

            # no writer can pause without both columns
            with pytest.raises(asyncpg.CheckViolationError, match="paused without"):
                await conn.execute("UPDATE job_stages SET paused = true WHERE stage = $1", RUNNING)
            with pytest.raises(asyncpg.CheckViolationError):
                await conn.execute(
                    "UPDATE job_stages SET paused = true, paused_by = 'x' WHERE stage = $1",
                    RUNNING,
                )
            # the admin pause as api/internal/handlers/queues/control.go writes it
            await conn.execute(
                "UPDATE job_stages SET paused = TRUE, paused_at = now(), paused_by = 'user:y',"
                " updated_at = now() WHERE stage = $1",
                RUNNING,
            )
            await conn.execute(
                "UPDATE job_stages SET paused = FALSE, paused_at = NULL, paused_by = NULL,"
                " updated_at = now() WHERE stage = $1",
                RUNNING,
            )
            assert await row(RUNNING) == (False, None, None)
            assert (
                await conn.fetchval("SELECT to_regclass('processing_jobs_claimable_idx')")
                is not None
            )
        finally:
            await conn.close()
            admin = await asyncpg.connect(PG_URL)
            await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
            await admin.close()

    asyncio.run(run())
