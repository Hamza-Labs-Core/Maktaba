"""Stories 33.7 and 33.9 on the pipeline's side: with TV slots the swap commit writes the
front door's swap operation (one row per channel, at its programme boundary) and leaves
the change ``swapping``; the reverse commit puts the previous release back (lineup,
generation, routing), marks the change ``rolled_back`` and keeps its edits in a draft."""

from __future__ import annotations

from datetime import timedelta
from typing import Any

import pytest

from maktaba_pipeline.channels.pass_ import topup_once
from maktaba_pipeline.engine.sql import is_pg, parse_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.build import Release, load_release
from maktaba_pipeline.tvrelease.commit import commit_change, slots_runtime
from maktaba_pipeline.tvrelease.generation import boundary
from maktaba_pipeline.tvrelease.rollback import rollback_change
from maktaba_pipeline.tvrelease.stage import stage_change
from maktaba_pipeline.tvrelease.store import create_change, get_change, update_change
from tests.tvrelease.helpers import NOW, add_channel, channel_def, library_with


async def _staged(db: Database) -> tuple[str, str, str, Release]:
    lib = await library_with(db, 8, tag="first")
    other = await library_with(db, 8, tag="second")
    a = await add_channel(db, lib, 101)
    b = await add_channel(db, lib, 102)
    assert await topup_once(db, now=NOW) == 2
    d = await channel_def(db, a)
    new_id = "00000000-0000-0000-0000-00000000b0d1"
    added = {**d, "id": new_id, "number": 150, "slug": "added"}
    pending: dict[str, Any] = {
        "channels": {a: {**d, "library_id": str(other), "number": 111}, new_id: added}
    }
    cid = await create_change(
        db, state="draft", title="t", source={"kind": "manual"}, pending=pending
    )
    assert await stage_change(db, cid, clock=NOW) == "staged"
    ch = await get_change(db, cid)
    assert ch is not None
    rel = await load_release(db, str(ch.row["release_id"]))
    assert rel is not None
    await update_change(db, cid, state="swapping")
    return cid, a, b, rel


async def _live_slot(db: Database) -> None:
    await db.execute(
        "INSERT INTO tv_slots (host_id, slot, role, role_epoch, state, beat_at)"
        " VALUES ('home', 'blue', 'live', 1, 'healthy', now())"
    )


@pytest.mark.asyncio
async def test_swap_commit_writes_the_front_door_operation(db: Database) -> None:
    """With a live TV slot the commit is the database half: the operation and one row
    per channel of both lineups at its programme boundary; the change stays swapping."""
    if not is_pg(db):
        pytest.skip("TV slots run on a Postgres home")
    cid, a, b, rel = await _staged(db)
    await _live_slot(db)
    assert await slots_runtime(db)
    clock = NOW + timedelta(minutes=1)
    report = await commit_change(db, cid, clock=clock)
    assert report["front_door"] is True and report["interim"] is False
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "swapping"
    op = await db.fetchrow("SELECT * FROM tv_swap_ops WHERE id = $1", report["op_id"])
    assert op["kind"] == "swap" and op["committed"] and str(op["release_id"]) == rel.id
    assert op["state"] == "planned" and str(op["change_id"]) == cid
    rows = {
        str(r["channel_id"]): r
        for r in await db.fetch("SELECT * FROM tv_channel_switch WHERE op_id = $1", op["id"])
    }
    added = "00000000-0000-0000-0000-00000000b0d1"
    assert set(rows) == {a, b, added}
    # a watched channel switches at the end of the programme on the air (capped)
    want = await boundary(db, b, 0, clock)
    want = min(want, clock + timedelta(minutes=30))
    assert parse_ts(rows[b]["switch_at"]) == want
    assert parse_ts(rows[added]["switch_at"]) == clock and not rows[added]["removed"]
    # the guide already shows the release
    route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
    assert int(route["live_gen"]) == rel.sched_gen
    ev = await db.fetchval(
        "SELECT count(*) FROM tv_change_events WHERE change_id = $1 AND action = 'swap_commit'",
        cid,
    )
    assert ev == 1


@pytest.mark.asyncio
async def test_rollback_restores_the_previous_release(db: Database) -> None:
    """TC1 of 33.9: the reverse commit restores the previous lineup (renumber back, the
    added channel staged again), the previous generation and settings, ends the change
    rolled_back with its reason, and keeps its edits in a draft."""
    cid, a, b, rel = await _staged(db)
    await commit_change(db, cid, clock=NOW + timedelta(minutes=1))
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "live"
    assert (await channel_def(db, a))["number"] == 111
    added = "00000000-0000-0000-0000-00000000b0d1"
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", added) == "live"
    report = await rollback_change(db, cid, reason="auto:crash", clock=NOW + timedelta(minutes=5))
    assert report["to_gen"] == 0 and report["reason"] == "auto:crash"
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "rolled_back" and ch.row["end_reason"] == "auto:crash"
    assert (await channel_def(db, a))["number"] == 101
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", added) == "staged"
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", b) == "live"
    route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
    assert int(route["live_gen"]) == 0 and route["previous_release_id"] is None
    assert str(route["live_release_id"]) != rel.id
    draft = await db.fetchrow(
        "SELECT * FROM tv_changes WHERE state = 'draft' AND id = $1", report["restage_draft"]
    )
    assert draft is not None
    # no second rollback: the rollback point is gone
    with pytest.raises(LookupError):
        await rollback_change(db, cid)


@pytest.mark.asyncio
async def test_no_rollback_before_the_swap_commit(db: Database) -> None:
    """A change in ``swapping`` whose commit has not run is not on the air: tv_routing's
    previous release is the one before the live one, so a reverse commit now would put a
    release two changes old on the air. It is refused and nothing changes."""
    cid, a, _, rel = await _staged(db)
    # an earlier swap left a rollback point (previous) behind the live release
    await db.execute("DELETE FROM tv_routing WHERE scope = 'cluster'")
    await db.execute(
        "INSERT INTO tv_routing (scope, live_release_id, live_gen, previous_release_id,"
        " previous_gen, epoch) VALUES ('cluster', NULL, 0, $1, 0, 3)",
        rel.id,
    )
    with pytest.raises(LookupError):
        await rollback_change(db, cid, reason="manual", clock=NOW + timedelta(minutes=1))
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "swapping"
    route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
    assert int(route["epoch"]) == 3 and route["previous_release_id"] is not None
    assert (await channel_def(db, a))["number"] == 101
