"""Plugins' questions share the lookup pool with core stages (Epic 54, story 54.21 §2).

A plugin's question to the language model holds a ``compute_leases`` row while it runs: the
claim counts a live one as busy, so core stages and plugins ask the model one at a time.
Each core claim of the pool counts in ``claim_shares`` (owner ``plugin_share:lookup``); the
API lets a plugin take a slot while core work waits only after two of them
(``api/internal/plugins/compute.go``), so core keeps at least 2 of every 3 slots. Plugins
run on a Postgres home only: these run on Postgres.
"""

from __future__ import annotations

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season

POOL = "lookup"
STAGE = "check_tags"


async def _seasons(db: Database, n: int) -> None:
    lib = await make_library(db)
    for i in range(n):
        _, season = await make_series_and_season(db, lib, name=f"Show {i}")
        await enqueue_job(db, subject_kind="season", subject_id=season, stage=STAGE)


async def _share(db: Database) -> int | None:
    row = await db.fetchrow("SELECT pass FROM claim_shares WHERE owner = 'plugin_share:lookup'")
    return None if row is None else int(row["pass"])


@pytest.mark.asyncio
async def test_core_lookup_claims_count_toward_the_plugin_share(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("plugins run on a Postgres home only")
    await db.execute("UPDATE worker_pools SET size = 1 WHERE pool = $1", POOL)
    await _seasons(db, 3)
    job = await claim_for_pool(db, worker_id="w", pool=POOL, stages=[STAGE])
    assert job is not None
    assert await _share(db) == 1
    await db.execute("UPDATE processing_jobs SET state = 'done' WHERE id = $1", job.id)
    assert await claim_for_pool(db, worker_id="w", pool=POOL, stages=[STAGE]) is not None
    assert await _share(db) == 2


@pytest.mark.asyncio
async def test_a_live_plugin_lease_holds_the_slot(db: Database) -> None:
    if not is_pg(db):
        pytest.skip("plugins run on a Postgres home only")
    await db.execute("UPDATE worker_pools SET size = 1 WHERE pool = $1", POOL)
    await _seasons(db, 1)
    await db.execute(
        "INSERT INTO compute_leases (pool, plugin_id, expires_at)"
        " VALUES ($1, 'dev.example.x', now() + interval '1 minute')",
        POOL,
    )
    assert await claim_for_pool(db, worker_id="w", pool=POOL, stages=[STAGE]) is None
    # one the API left behind (it died mid-question) stops holding the slot at expires_at
    await db.execute("UPDATE compute_leases SET expires_at = now() - interval '1 second'")
    assert await claim_for_pool(db, worker_id="w", pool=POOL, stages=[STAGE]) is not None
