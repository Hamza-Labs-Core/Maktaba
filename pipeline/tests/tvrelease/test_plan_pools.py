# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""A channel plan's filler pool writes are stored compact: the added and retimed clips'
lengths only, never the whole pool (hundreds of pools of thousands of clips made every
plan's diff ~50 MB). The staged generation and the swap see the same pools as before."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any

import pytest

from maktaba_pipeline.engine.stages.write_channels import PoolPlan, PoolWrite
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.build import filler_item_id
from maktaba_pipeline.tvrelease.generation import pool_overlay
from maktaba_pipeline.tvrelease.stage import write_plan_pending
from tests.engine.factory import make_library, make_video

POOL = "00000000-0000-4000-8000-0000000000f1"


async def _pool(db: Database) -> list[str]:
    """A tool-written pool of clips v0 (kept), v1 (dropped), v2 (retimed); v3, v4 not
    in it (v3 added to it, v4 the clip of a new pool)."""
    lib = await make_library(db)
    vids = [str(await make_video(db, lib, title=f"clip {i}", duration=30.0)) for i in range(5)]
    await db.execute(
        "INSERT INTO filler_pools (id, name, external_key) VALUES ($1, 'Mix', 'mix.topic')", POOL
    )
    for i, v in enumerate(vids[:3]):
        await db.execute(
            "INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms) VALUES ($1, $2, $3, 'filler', 30000)",
            f"00000000-0000-4000-8000-0000000000a{i}",
            POOL,
            v,
        )
    return vids


def _writes(vids: list[str]) -> SimpleNamespace:
    want = {vids[0]: 30000, vids[2]: 45000, vids[3]: 20000}
    return SimpleNamespace(
        update=[],
        create=[],
        delete=[],
        pools=PoolPlan(
            write=[
                PoolWrite(
                    "mix.topic",
                    "Mix",
                    want,
                    pool_id=POOL,
                    add=[vids[3]],
                    drop=[vids[1]],
                    retime=[vids[2]],
                ),
                PoolWrite("mix.new", "New", {vids[4]: 10000}, add=[vids[4]]),
            ]
        ),
    )


def _after(vids: list[str]) -> dict[str, list[tuple[str, str, int]]]:
    """The pools as the swap leaves them."""
    return {
        "mix.topic": sorted(
            [
                ("00000000-0000-4000-8000-0000000000a0", vids[0], 30000),
                ("00000000-0000-4000-8000-0000000000a2", vids[2], 45000),
                (filler_item_id("mix.topic", vids[3]), vids[3], 20000),
            ],
            key=lambda c: c[1],
        ),
        "mix.new": [(filler_item_id("mix.new", vids[4]), vids[4], 10000)],
    }


@pytest.mark.asyncio
async def test_pool_writes_store_only_the_touched_clips(db: Database) -> None:
    vids = await _pool(db)
    pending = await write_plan_pending(db, _writes(vids))
    w = {p["key"]: p for p in pending["pools"]["write"]}
    # the kept clip v0 is not stored; the added and retimed ones are, with their lengths
    assert w["mix.topic"]["items"] == {vids[3]: 20000, vids[2]: 45000}
    assert w["mix.new"]["items"] == {vids[4]: 10000}
    assert await pool_overlay(db, pending) == _after(vids)


@pytest.mark.asyncio
async def test_an_older_change_with_the_whole_pool_reads_the_same(db: Database) -> None:
    """A change staged before (``items`` = the whole pool) overlays to the same pools."""
    vids = await _pool(db)
    pending: dict[str, Any] = await write_plan_pending(db, _writes(vids))
    pending["pools"]["write"][0]["items"] = {vids[0]: 30000, vids[2]: 45000, vids[3]: 20000}
    assert await pool_overlay(db, pending) == _after(vids)
    pending["pools"]["delete"] = [["mix.topic", POOL]]
    assert (await pool_overlay(db, pending))["mix.topic"] == []


@pytest.mark.asyncio
async def test_slot_0141_compacts_the_stored_pool_writes(db: Database) -> None:
    """The migration leaves each stored pool write with its added and retimed clips:
    the pools an older change describes, its other edits and other changes unchanged.
    A change that ended without airing loses its pool writes."""
    from maktaba_pipeline.engine.sql import is_pg, loads
    from maktaba_pipeline.tvrelease.store import create_change, get_change
    from tests.engine.migrate import MIGRATIONS_DIR, up_statements

    vids = await _pool(db)
    pending: dict[str, Any] = await write_plan_pending(db, _writes(vids))
    whole = {vids[0]: 30000, vids[2]: 45000, vids[3]: 20000}
    pending["pools"]["write"][0]["items"] = whole  # as a change staged before
    old = await create_change(
        db, state="draft", title="Channel plan #1", source={"kind": "write_channels"},
        pending=pending,
    )  # fmt: skip
    other = await create_change(
        db, state="draft", title="Schedule", source={"kind": "schedule"}, pending={"sched": ["x"]}
    )
    ended = await create_change(
        db, state="draft", title="Channel plan #0", source={"kind": "write_channels"},
        pending=pending,
    )  # fmt: skip
    await db.execute("UPDATE tv_changes SET state = 'failed' WHERE id = $1", ended)
    suffix = ".sql" if is_pg(db) else ".sqlite.sql"
    sql = (MIGRATIONS_DIR / f"0141_tv_changes_compact_pools{suffix}").read_text(encoding="utf-8")
    for stmt in up_statements(sql):
        await db.execute(stmt)
    ch = await get_change(db, old)
    assert ch is not None
    w = ch.pending["pools"]["write"]
    assert [p["key"] for p in w] == ["mix.topic", "mix.new"]
    assert w[0]["items"] == {vids[3]: 20000, vids[2]: 45000}
    assert w[1]["items"] == {vids[4]: 10000}
    assert {k: v for k, v in w[0].items() if k != "items"} == {
        k: v for k, v in pending["pools"]["write"][0].items() if k != "items"
    }
    assert ch.pending["channels"] == pending["channels"]
    assert await pool_overlay(db, ch.pending) == _after(vids)
    row = await db.fetchval("SELECT diff FROM tv_changes WHERE id = $1", other)
    assert loads(row) == {"pending": {"sched": ["x"]}}
    gone = await get_change(db, ended)
    assert gone is not None and "pools" not in gone.pending
    assert gone.pending["channels"] == pending["channels"]


@pytest.mark.asyncio
async def test_the_daily_clean_up_trims_the_ended_changes(db: Database) -> None:
    """tv_gc drops the pool writes of the changes that ended without airing (failed,
    cancelled, superseded) and keeps the others' (a draft, a rolled-back change)."""
    from maktaba_pipeline.tvrelease.gc import ENDED, collect
    from maktaba_pipeline.tvrelease.store import create_change, get_change

    vids = await _pool(db)
    pending = await write_plan_pending(db, _writes(vids))
    ids: dict[str, str] = {}
    for state in (*ENDED, "rolled_back", "draft"):
        ids[state] = await create_change(
            db, state="draft", title=state, source={"kind": "write_channels"}, pending=pending
        )
        await db.execute("UPDATE tv_changes SET state = $1 WHERE id = $2", state, ids[state])
    out = await collect(db)
    assert out["trimmed_changes"] == len(ENDED)
    for state, cid in ids.items():
        ch = await get_change(db, cid)
        assert ch is not None
        assert ("pools" in ch.pending) == (state not in ENDED), state
        assert ch.pending["channels"] == pending["channels"]
    assert (await collect(db))["trimmed_changes"] == 0
