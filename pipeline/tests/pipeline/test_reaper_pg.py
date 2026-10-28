"""The reaper's advisory lock on Postgres: taken and given back on one connection.

Through the pool's ``fetchrow`` the lock was taken on whichever connection the pool
handed out and unlocked on whichever came next. Postgres logged "WARNING: you don't own
a lock of type ExclusiveLock" every 30 s from every worker process (5 066 times in the
home's log of 2026-10-27), and the first connection kept the lock for good. Runs when
``MAKTABA_TEST_PG_URL`` is set, like the rest of the Postgres tier.
"""

from __future__ import annotations

import uuid
from collections.abc import AsyncIterator
from typing import Any

import pytest
import pytest_asyncio

from maktaba_pipeline.db.jobs_reaper import REAPER_ADVISORY_LOCK_KEY
from maktaba_pipeline.pipeline import reaper as reaper_mod
from maktaba_pipeline.pipeline.reaper import Reaper
from maktaba_pipeline.runtime import Database
from tests.engine.fixtures_db import PG_URL, _swap_db, pg_template  # noqa: F401

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]


@pytest_asyncio.fixture
async def pg_db(pg_template: str) -> AsyncIterator[Database]:  # noqa: F811
    import asyncpg  # type: ignore[import-untyped]  # no stubs ship with asyncpg

    name = "maktaba_t_" + uuid.uuid4().hex[:10]
    admin = await asyncpg.connect(PG_URL)
    await admin.execute(f'CREATE DATABASE "{name}" TEMPLATE "{pg_template}"')
    await admin.close()
    database = await Database.connect(_swap_db(PG_URL, name))
    try:
        yield database
    finally:
        await database.close()
        admin = await asyncpg.connect(PG_URL)
        await admin.execute(f'DROP DATABASE IF EXISTS "{name}" WITH (FORCE)')
        await admin.close()


async def _try_lock(conn: Any) -> bool:
    got = bool(await conn.fetchval("SELECT pg_try_advisory_lock($1)", REAPER_ADVISORY_LOCK_KEY))
    if got:
        await conn.fetchval("SELECT pg_advisory_unlock($1)", REAPER_ADVISORY_LOCK_KEY)
    return got


async def _advisory_locks(conn: Any) -> int:
    return int(
        await conn.fetchval(
            "SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND granted"
            " AND database = (SELECT oid FROM pg_database WHERE datname = current_database())"
        )
    )


async def test_the_lock_is_held_for_the_sweep_and_given_back_on_its_connection(
    pg_db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """While one worker sweeps, another worker's reaper finds the lock busy; after the
    tick no session holds it. Taken through the pool, the lock went with the connection
    back to the pool, whose reset (``pg_advisory_unlock_all``) dropped it before the
    sweep began: no two sweeps were kept apart, and the unlock that followed, on a
    connection holding nothing, was the warning."""
    import asyncpg

    other = await asyncpg.connect(pg_db._dsn)
    busy_during_sweep: list[bool] = []

    async def sweep(db: Any, *, stale_claim_sec: float) -> list[Any]:
        # the sweep's own queries borrow the pool's connections meanwhile
        await db.fetchrow("SELECT 1 AS x")
        busy_during_sweep.append(not await _try_lock(other))
        return []

    monkeypatch.setattr(reaper_mod, "reap_once", sweep)
    reaper = Reaper(pg_db, stale_claim_sec=90.0, heartbeat_sec=5.0)
    try:
        for _ in range(3):
            assert await reaper.tick() == 0
            assert await _advisory_locks(other) == 0, "a session kept the reaper's lock"
        assert busy_during_sweep == [True, True, True], "another reaper could sweep at once"
    finally:
        await other.close()


async def test_a_sweep_that_fails_gives_the_lock_back(
    pg_db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    import asyncpg

    async def sweep(db: Any, *, stale_claim_sec: float) -> list[Any]:
        raise RuntimeError("the sweep failed")

    monkeypatch.setattr(reaper_mod, "reap_once", sweep)
    other = await asyncpg.connect(pg_db._dsn)
    try:
        with pytest.raises(RuntimeError):
            await Reaper(pg_db, stale_claim_sec=90.0, heartbeat_sec=5.0).tick()
        assert await _advisory_locks(other) == 0
        assert await _try_lock(other)
    finally:
        await other.close()


async def test_a_lock_held_elsewhere_skips_the_sweep(
    pg_db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    import asyncpg

    swept = False

    async def sweep(db: Any, *, stale_claim_sec: float) -> list[Any]:
        nonlocal swept
        swept = True
        return []

    monkeypatch.setattr(reaper_mod, "reap_once", sweep)
    other = await asyncpg.connect(pg_db._dsn)
    try:
        assert await other.fetchval("SELECT pg_try_advisory_lock($1)", REAPER_ADVISORY_LOCK_KEY)
        assert await Reaper(pg_db, stale_claim_sec=90.0, heartbeat_sec=5.0).tick() == 0
        assert not swept
    finally:
        await other.close()
