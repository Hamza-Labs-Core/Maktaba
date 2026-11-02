"""engine/leader.py: the session advisory lock a home-wide loop holds for a pass (Postgres)."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.engine.leader import leader
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.runtime import Database

KEY = "maktaba.test_leader"
#: a guard against a hang, not a timing check
HANG_SEC = 10.0


async def _other(db: Database) -> Database:
    if not is_pg(db):
        pytest.skip("Postgres only: SQLite has one process, always the leader")
    return await Database.connect(db._dsn)  # noqa: SLF001 - the test database's DSN


async def _holders(db: Database) -> list[int]:
    """The backends holding an advisory lock in the test database."""
    rows = await db.fetch(
        "SELECT pid FROM pg_locks WHERE locktype = 'advisory' AND granted"
        " AND database = (SELECT oid FROM pg_database WHERE datname = current_database())"
    )
    return [int(r["pid"]) for r in rows]


@pytest.mark.asyncio
async def test_one_holder_at_a_time_and_the_lock_ends_with_the_block(db: Database) -> None:
    other = await _other(db)
    try:
        async with leader(db, KEY) as got:
            assert got
            async with leader(other, KEY) as again:  # another worker
                assert not again
            async with leader(db, KEY) as same:  # another connection of the same pool
                assert not same
            assert len(await _holders(db)) == 1
        assert await _holders(db) == []
        async with leader(other, KEY) as got:
            assert got
    finally:
        await other.close()


@pytest.mark.asyncio
async def test_the_lock_is_released_when_the_block_fails_or_is_cancelled(db: Database) -> None:
    other = await _other(db)
    try:
        with pytest.raises(RuntimeError):
            async with leader(db, KEY) as got:
                assert got
                raise RuntimeError("the pass failed")
        assert await _holders(db) == []

        inside = asyncio.Event()

        async def held() -> None:
            async with leader(db, KEY) as got:
                assert got
                inside.set()
                await asyncio.sleep(HANG_SEC)

        task = asyncio.create_task(held())
        await asyncio.wait_for(inside.wait(), HANG_SEC)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        assert await _holders(db) == []
        async with leader(other, KEY) as got:
            assert got
    finally:
        await other.close()


@pytest.mark.asyncio
async def test_a_dead_connection_frees_the_lock_and_its_block_ends_quietly(db: Database) -> None:
    other = await _other(db)
    mine = await Database.connect(db._dsn)  # noqa: SLF001 - a worker whose connection dies
    try:
        async with leader(mine, KEY) as got:
            assert got
            (pid,) = await _holders(db)
            # the leader's backend dies (a crashed worker, a dropped network)
            assert await other.fetchval("SELECT pg_terminate_backend($1)", pid)
            for _ in range(100):  # the server ends the backend on its own time
                async with leader(other, KEY) as taken:
                    if taken:
                        break
                await asyncio.sleep(0.05)
            else:
                pytest.fail("the dead connection's lock was never freed")
        # leaving the block on the dead connection raised nothing, the pool still works,
        # and it got the dead connection's slot back (or its close would wait forever)
        assert await mine.fetchval("SELECT 1") == 1
        await asyncio.wait_for(mine.close(), HANG_SEC)
    finally:
        await mine.close()
        await other.close()
