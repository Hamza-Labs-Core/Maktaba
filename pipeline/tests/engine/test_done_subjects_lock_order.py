"""Slot 0184 (#642): processing_done_subjects rows are locked in key order across a
transaction's statements, and the test databases refuse a transaction that does not."""

from __future__ import annotations

import hashlib

import pytest

from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.runtime import Database

LIB = "18400000-0000-4000-8000-000000000001"
V_A = "18400000-0000-4000-8001-00000000000a"
V_B = "18400000-0000-4000-8001-00000000000b"


async def _seed(db: Database) -> dict[str, int]:
    await db.execute("INSERT INTO libraries (id, name, roots) VALUES ($1, 'L', $2)", LIB, ["/l"])
    ids: dict[str, int] = {}
    for v in (V_A, V_B):
        await db.execute(
            "INSERT INTO videos (id, library_id, content_hash, path, filename, size_bytes, mtime)"
            " VALUES ($1, $2, $3, $4, $5, 1, now())",
            v,
            LIB,
            hashlib.sha256(v.encode()).hexdigest(),
            f"/l/{v}.mp4",
            f"{v}.mp4",
        )
        ids[v] = int(
            await db.fetchval(
                "INSERT INTO processing_jobs (video_id, library_id, stage, state, started_at)"
                " VALUES ($1, $2, 'probe', 'running', now()) RETURNING id",
                v,
                LIB,
            )
        )
    return ids


FINISH = (
    "UPDATE processing_jobs SET state = 'done', finished_at = now(), outcome = 'changed'"
    " WHERE id = $1"
)


@pytest.mark.asyncio
async def test_the_test_databases_are_strict(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("Postgres's row locks")
    assert await db.fetchval("SELECT current_setting('maktaba.lock_order_strict', true)") == "on"


@pytest.mark.asyncio
async def test_out_of_order_across_statements_is_refused(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("SQLite has one lock for the database: nothing to order")
    ids = await _seed(db)
    with pytest.raises(Exception, match="lock order"):
        async with db.transaction():
            await db.execute(FINISH, ids[V_B])
            await db.execute(FINISH, ids[V_A])
    # in key order, across statements, it goes through
    async with db.transaction():
        await db.execute(FINISH, ids[V_A])
        await db.execute(FINISH, ids[V_B])
    n = await db.fetchval(
        "SELECT count(*) FROM processing_done_subjects WHERE library_id = $1", LIB
    )
    assert n == 2


@pytest.mark.asyncio
async def test_a_transaction_that_holds_the_table_may_take_any_order(db: Database) -> None:
    """The mediaintel import holds the whole summary (no other writer runs beside it), so
    it writes its done jobs stage after stage, in no single key order."""
    if not is_pg(db):
        pytest.skip("Postgres's row locks")
    ids = await _seed(db)
    async with db.transaction():
        await db.execute("LOCK TABLE processing_done_subjects IN SHARE ROW EXCLUSIVE MODE")
        await db.execute(FINISH, ids[V_B])
        await db.execute(FINISH, ids[V_A])
    n = await db.fetchval(
        "SELECT count(*) FROM processing_done_subjects WHERE library_id = $1", LIB
    )
    assert n == 2


class _Rollback(Exception):
    pass


@pytest.mark.asyncio
async def test_a_dry_run_writes_and_locks_no_summary_row(db: Database) -> None:
    """Slot 0186: the mediaintel import's dry run (always rolled back) sets
    maktaba.done_subjects_dry_run; the touch then writes nothing, so it locks no row and
    takes no order, and needs no table lock."""
    if not is_pg(db):
        pytest.skip("Postgres's row locks")
    ids = await _seed(db)
    seen = -1
    with pytest.raises(_Rollback):
        async with db.transaction():
            await db.execute("SELECT set_config('maktaba.done_subjects_dry_run', 'on', true)")
            await db.execute(FINISH, ids[V_B])
            await db.execute(FINISH, ids[V_A])  # out of order: not refused
            seen = int(await db.fetchval("SELECT count(*) FROM processing_done_subjects"))
            raise _Rollback
    assert seen == 0
    # the setting is the transaction's own: the next one keeps the summary again
    async with db.transaction():
        await db.execute(FINISH, ids[V_A])
    assert await db.fetchval("SELECT count(*) FROM processing_done_subjects") == 1
