# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Story 33.3: schedule generations - copy to the boundary (TC1), an unchanged channel
copied whole (TC2), re-anchoring (TC3), the top-up of open generations (TC4), the
live-unique numbers (TC6), the GC (TC7), and the live readers."""

from __future__ import annotations

from datetime import timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.channels.pass_ import lock_key, topup_once
from maktaba_pipeline.channels.repo import live_gen
from maktaba_pipeline.engine.sql import parse_ts, to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.build import Release
from maktaba_pipeline.tvrelease.commit import commit_change
from maktaba_pipeline.tvrelease.gc import collect
from maktaba_pipeline.tvrelease.generation import boundary, build_generation, reanchor
from maktaba_pipeline.tvrelease.stage import stage_change
from maktaba_pipeline.tvrelease.store import create_change, get_change, update_change
from tests.tvrelease.helpers import NOW, add_channel, blocks, channel_def, library_with

pytestmark = pytest.mark.integration


async def _two_channels(db: Database) -> tuple[str, str, Any]:
    lib = await library_with(db, 8, tag="first")
    other = await library_with(db, 8, tag="second")
    a = await add_channel(db, lib, 101)
    b = await add_channel(db, lib, 102)
    assert await topup_once(db, now=NOW) == 2  # generation 0, as today
    return a, b, other


async def _stage(db: Database, pending: dict[str, Any]) -> tuple[str, Release]:
    from maktaba_pipeline.tvrelease.build import load_release

    cid = await create_change(
        db, state="draft", title="t", source={"kind": "manual"}, pending=pending
    )
    assert await stage_change(db, cid, clock=NOW) == "staged"
    ch = await get_change(db, cid)
    assert ch is not None
    rel = await load_release(db, str(ch.row["release_id"]))
    assert rel is not None
    return cid, rel


@pytest.mark.asyncio
async def test_generation_copy_to_boundary(db: Database) -> None:
    """TC1 / AC3: N+1 equals N up to the end of the block on air at the planned swap;
    the changed channel is regenerated after it; nothing on the air changes."""
    a, b, other = await _two_channels(db)
    before = await blocks(db, a, 0)
    d = await channel_def(db, a)
    cid, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other)}}})
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "staged" and ch.row["kinds"] == ["schedule"]
    assert rel.sched_gen >= 1 and a in rel.sched_changed and b not in rel.sched_changed
    assert await live_gen(db) == 0 and await blocks(db, a, 0) == before  # the air is untouched
    at = parse_ts(ch.row["anchor_at"])
    assert at is not None and at > NOW
    b_c = await boundary(db, a, 0, at)
    stored = await db.fetchval(
        "SELECT boundary_at FROM tv_release_channels WHERE release_id = $1 AND channel_id = $2",
        rel.id,
        a,
    )
    assert parse_ts(stored) == b_c
    new = await blocks(db, a, rel.sched_gen)
    old_head = [x for x in before if parse_ts(x[1]) < b_c]  # type: ignore[operator]
    new_head = [x for x in new if parse_ts(x[1]) < b_c]  # type: ignore[operator]
    assert new_head == old_head and old_head  # the programme on air (and before) is kept
    second = {
        str(r["id"])
        for r in await db.fetch("SELECT id FROM videos WHERE library_id = $1", str(other))
    }
    tail = [x for x in new if parse_ts(x[1]) >= b_c]  # type: ignore[operator]
    assert tail and all(v in second for v, _, _ in tail if v is not None)
    assert ch.row["diff"]["schedules"][0]["divergence_at"] == b_c.isoformat()


@pytest.mark.asyncio
async def test_generation_unchanged_channel_full_copy(db: Database) -> None:
    """TC2: a channel whose schedule does not change is copied whole."""
    a, b, other = await _two_channels(db)
    d = await channel_def(db, a)
    _, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other)}}})
    assert await blocks(db, b, rel.sched_gen) == await blocks(db, b, 0)


@pytest.mark.asyncio
async def test_reanchor_moves_boundary(db: Database) -> None:
    """TC3 / AC4: a later swap time moves the boundary; N+1 equals N up to the new one."""
    a, _, other = await _two_channels(db)
    d = await channel_def(db, a)
    _, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other)}}})
    later = NOW + timedelta(hours=3)
    moved = await reanchor(db, rel, at=later, clock=NOW)
    b_new = await boundary(db, a, 0, later)
    assert moved == {a: b_new.isoformat()}
    old = await blocks(db, a, 0)
    new = await blocks(db, a, rel.sched_gen)
    assert [x for x in new if parse_ts(x[1]) < b_new] == [  # type: ignore[operator]
        x
        for x in old
        if parse_ts(x[1]) < b_new  # type: ignore[operator]
    ]
    assert await reanchor(db, rel, at=later, clock=NOW) == {}  # unchanged: nothing redone


@pytest.mark.asyncio
async def test_topup_open_generations(db: Database) -> None:
    """TC4 / AC5: the top-up extends the live and the staged generation, each under its
    own lock; a change waiting for days still has its horizon."""
    a, _, other = await _two_channels(db)
    d = await channel_def(db, a)
    _, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other)}}})

    async def horizon(gen: int) -> Any:
        return parse_ts(
            await db.fetchval(
                "SELECT horizon_until FROM channel_schedule_state WHERE channel_id = $1 AND sched_gen = $2",
                a,
                gen,
            )
        )

    h0, h1 = await horizon(0), await horizon(rel.sched_gen)
    later = NOW + timedelta(days=5)
    assert await topup_once(db, now=later) >= 2
    assert await horizon(0) > h0 and await horizon(rel.sched_gen) > h1
    assert lock_key(UUID(a), 0) != lock_key(UUID(a), rel.sched_gen)
    assert await db.fetchval("SELECT count(*) FROM tv_changes") == 1  # a top-up is no change


@pytest.mark.asyncio
async def test_lifecycle_unique_numbers(db: Database) -> None:
    """TC6 / AC6: a staged channel may use a number a live one has; two live may not."""
    lib = await library_with(db, 1)
    await add_channel(db, lib, 5)
    staged = await add_channel(db, None, 7)
    await db.execute(
        "UPDATE channels SET lifecycle = 'staged', number = 5, slug = 'ch-5' WHERE id = $1", staged
    )
    with pytest.raises(Exception):  # noqa: B017 - either dialect's unique violation
        await add_channel(db, lib, 5)


@pytest.mark.asyncio
async def test_swap_commit_flips_the_generation_and_live_readers_follow(db: Database) -> None:
    """The interim swap: the lineup and the routing flip in one commit; the live view
    shows the new generation only; the guide keeps the programme on the air."""
    a, _, other = await _two_channels(db)
    d = await channel_def(db, a)
    cid, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other), "number": 111}}})
    await update_change(db, cid, state="swapping")
    report = await commit_change(db, cid, clock=NOW + timedelta(minutes=1))
    assert report["to_gen"] == rel.sched_gen and report["epoch"] == 1
    assert report["readback"]["ok"] is True
    ch = await get_change(db, cid)
    assert ch is not None and ch.state == "live"
    route = await db.fetchrow("SELECT * FROM tv_routing WHERE scope = 'cluster'")
    assert int(route["live_gen"]) == rel.sched_gen and str(route["live_release_id"]) == rel.id
    assert int(route["previous_gen"]) == 0
    assert (await channel_def(db, a))["number"] == 111
    live_rows = await db.fetchval(
        "SELECT count(*) FROM channel_programs_live WHERE channel_id = $1", a
    )
    assert live_rows == len(await blocks(db, a, rel.sched_gen))
    # GC: the previous generation is kept for the rollback retention, then goes
    assert (await collect(db, clock=NOW + timedelta(hours=1)))["generations"] == 0
    out = await collect(db, clock=NOW + timedelta(hours=25))
    assert out["generations"] == 1 and await blocks(db, a, 0) == []


@pytest.mark.asyncio
async def test_gc_retention(db: Database) -> None:
    """TC7 / AC8: retired channels past the retention and staged channels of a cancelled
    change are deleted; a staged generation of a cancelled change too."""
    a, _, _ = await _two_channels(db)
    new_id = "00000000-0000-0000-0000-00000000a0d1"
    d = {**await channel_def(db, a), "id": new_id, "number": 150, "slug": "new"}
    cid, rel = await _stage(db, {"channels": {new_id: d}})
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", new_id) == "staged"
    await update_change(db, cid, state="cancelled")
    gone = await db.fetchval("SELECT id FROM channels WHERE number = 102")
    await db.execute(
        "UPDATE channels SET lifecycle = 'retired', retired_at = $2 WHERE id = $1",
        str(gone),
        to_ts(db, NOW - timedelta(days=2)),
    )
    out = await collect(db, clock=NOW)
    assert out["staged_channels"] == 1 and out["retired_channels"] == 1
    assert out["generations"] >= 1 and await blocks(db, a, rel.sched_gen) == []
    assert await db.fetchval("SELECT count(*) FROM channels WHERE id = $1", new_id) == 0


@pytest.mark.asyncio
async def test_a_new_channel_takes_a_removed_ones_number(db: Database) -> None:
    """The swap retires removals before it writes additions: a plan that replaces
    channel 102 by a new 102 goes on the air in one commit."""
    a, b, _ = await _two_channels(db)
    new_id = "00000000-0000-0000-0000-00000000a0d2"
    d = {**await channel_def(db, b), "id": new_id, "slug": "ch-102-new"}
    cid, rel = await _stage(db, {"channels": {b: None, new_id: d}})
    await update_change(db, cid, state="swapping")
    report = await commit_change(db, cid, clock=NOW + timedelta(minutes=1))
    assert report["lineup"] == {"channels": 2, "added": 1, "retired": 1}
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", b) == "retired"
    assert await db.fetchval("SELECT number FROM channels WHERE id = $1", new_id) == 102
    assert await db.fetchval("SELECT lifecycle FROM channels WHERE id = $1", new_id) == "live"
    assert await blocks(db, new_id, rel.sched_gen)
    assert a in rel.defs


@pytest.mark.asyncio
async def test_edit_saved_while_staging_is_not_lost(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review fix: an edit saved into the draft while tv_stage builds it is staged too
    (the stage used to overwrite diff.pending with what it had read at the start)."""
    import maktaba_pipeline.tvrelease.stage as stage_mod

    a, b, other = await _two_channels(db)
    da = await channel_def(db, a)
    db_ = await channel_def(db, b)
    cid = await create_change(
        db, state="draft", title="t", source={"kind": "editor"},
        pending={"channels": {a: {**da, "name": "First edit"}}},
    )  # fmt: skip
    real = build_generation
    calls = {"n": 0}

    async def build_and_edit(*args: Any, **kw: Any) -> Any:
        out = await real(*args, **kw)
        calls["n"] += 1
        if calls["n"] == 1:
            # an admin saves another edit into the same draft meanwhile (Drafts.Save)
            ch = await get_change(db, cid)
            assert ch is not None and ch.row["release_id"] is not None  # GC keeps it
            pending = ch.pending
            pending["channels"][b] = {**db_, "name": "Second edit"}
            await update_change(db, cid, diff={**ch.row["diff"], "pending": pending})
        return out

    monkeypatch.setattr(stage_mod, "build_generation", build_and_edit)
    assert await stage_change(db, cid, clock=NOW) == "staged"
    assert calls["n"] == 2  # built again with the edit
    ch = await get_change(db, cid)
    assert ch is not None
    assert set(ch.pending["channels"]) == {a, b}
    from maktaba_pipeline.tvrelease.build import load_release

    rel = await load_release(db, str(ch.row["release_id"]))
    assert rel is not None and rel.defs[b]["name"] == "Second edit"


@pytest.mark.asyncio
async def test_second_change_cannot_go_in_flight_while_one_builds(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    """Review fix: the in-flight check is repeated at the transition, so a change staged
    by another job while this one was built leaves this one a draft."""
    import maktaba_pipeline.tvrelease.stage as stage_mod
    from maktaba_pipeline.tvrelease.stage import ChangeInFlight

    a, b, _ = await _two_channels(db)
    da = await channel_def(db, a)
    db_ = await channel_def(db, b)
    first = await create_change(
        db, state="draft", title="t", source={"kind": "editor"},
        pending={"channels": {a: {**da, "name": "A"}}},
    )  # fmt: skip
    second = await create_change(
        db, state="draft", title="u", source={"kind": "manual"},
        pending={"channels": {b: {**db_, "name": "B"}}},
    )  # fmt: skip
    real = build_generation
    done = {"x": False}

    async def build_and_race(*args: Any, **kw: Any) -> Any:
        out = await real(*args, **kw)
        if not done["x"]:
            done["x"] = True
            assert await stage_change(db, second, clock=NOW) == "staged"
        return out

    monkeypatch.setattr(stage_mod, "build_generation", build_and_race)
    with pytest.raises(ChangeInFlight):
        await stage_change(db, first, clock=NOW)
    ch = await get_change(db, first)
    assert ch is not None and ch.state == "draft"


@pytest.mark.asyncio
async def test_boundary_capped_by_max_wait(db: Database) -> None:
    """``boundary_max_wait_minutes``: a programme running past the cap ends there in
    N+1 and the new schedule starts at the cap - the instant the Changes page shows
    (store.Boundaries) and the front door switches at (commit's switch plan)."""
    from tests.tvrelease.helpers import set_staging

    a, _, other = await _two_channels(db)
    await set_staging(db, boundary_max_wait_minutes=5)
    d = await channel_def(db, a)
    _, rel = await _stage(db, {"channels": {a: {**d, "library_id": str(other)}}})
    old = await blocks(db, a, 0)
    # a little after a programme started, well before it ends (30 min programmes)
    start = next(parse_ts(x[1]) for x in old if parse_ts(x[1]) > NOW + timedelta(hours=2))  # type: ignore[operator]
    assert start is not None
    at = start + timedelta(minutes=1)
    cap = at + timedelta(minutes=5)
    assert await boundary(db, a, 0, at) > cap
    moved = await reanchor(db, rel, at=at, clock=NOW)
    assert moved == {a: cap.isoformat()}
    new = await blocks(db, a, rel.sched_gen)
    ends = {parse_ts(x[2]) for x in new}
    starts = {parse_ts(x[1]) for x in new}
    assert cap in ends and cap in starts  # cut at the cap, the new schedule from it
    for _, s, e in new:  # no block spans the cap
        assert not (parse_ts(s) < cap < parse_ts(e))  # type: ignore[operator]
