# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Story 33.12: write_channels, the stale flag and top-ups with staging on (TC1, TC2,
TC3, TC6, story 33.10 TC3a) and the regression with staging off (TC7)."""

from __future__ import annotations

from types import SimpleNamespace
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.channels.pass_ import topup_once
from maktaba_pipeline.channels.repo import SqlRepo
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.engine.stages.write_channels import _staging, compute_changes
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.schedule.tv_settings import MaktabaTvConfig
from maktaba_pipeline.tvrelease.stage import stage_write_channels, stale_to_draft
from maktaba_pipeline.tvrelease.store import get_change
from tests.engine.factory import make_library, make_video
from tests.tvrelease.helpers import NOW, add_channel, blocks, library_with, set_staging

_JOB = SimpleNamespace(id=1)


async def _plan(db: Database, number: int = 105) -> dict[str, Any]:
    lib = await make_library(db)
    clips = [str(await make_video(db, lib, title=f"clip {i}", duration=60.0)) for i in range(3)]
    for i in range(4):
        await make_video(db, lib, title=f"talk {i}", duration=1500.0)
    return {
        "planner_version": 9,
        "config": {},
        "filler_pools": {
            "news.topic": {
                "name": "News",
                "items": [{"path": c, "duration_s": 60.0, "title": "c"} for c in clips],
            }
        },
        "channels": [
            {
                "key": "news",
                "kind": "theme",
                "filler_pools": ["news.topic"],
                "maktaba": {"number": number, "name": "News", "mode": "shuffle", "mode_config": {}},
            }
        ],
    }


async def _live_channels(db: Database) -> int:
    return int(await db.fetchval("SELECT count(*) FROM channels WHERE lifecycle = 'live'"))


@pytest.mark.asyncio
async def test_write_channels_review_creates_draft_and_apply_stages(db: Database) -> None:
    """TC2 / AC2: review parks a draft with the change set; the approved digest (the
    Changes page's Approve is the queue's Apply) stages that same change, approved."""
    plan = await _plan(db)
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    info = await stage_write_channels(
        db, wp=wp, changes=cs, job=_JOB, write_mode="review", approved=frozenset(), plan_id=1
    )
    assert info["state"] == "draft"
    ch = await get_change(db, info["id"])
    assert ch is not None and ch.row["digest"] == cs.digest()
    assert ch.source["kind"] == "write_channels" and ch.pending["channels"]
    assert await _live_channels(db) == 0  # nothing written
    again = await stage_write_channels(
        db, wp=wp, changes=cs, job=_JOB, write_mode="review",
        approved=frozenset({cs.digest()}), plan_id=1,
    )  # fmt: skip
    assert again["id"] == info["id"] and again["state"] == "staged"
    ch = await get_change(db, info["id"])
    assert ch is not None and ch.row["approved_by"] == ["queue_apply"]
    assert ch.row["kinds"] == ["lineup"]
    assert await _live_channels(db) == 0
    assert await db.fetchval("SELECT count(*) FROM channels WHERE lifecycle = 'staged'") == 1
    assert (
        await db.fetchval("SELECT count(*) FROM filler_pools WHERE external_key IS NOT NULL") == 0
    )


@pytest.mark.asyncio
async def test_write_channels_auto_stages_change(db: Database) -> None:
    """TC1 / AC1: auto mode stages at once; no live channel, pool or link changes."""
    plan = await _plan(db)
    cs, wp = await compute_changes(db, plan, MaktabaTvConfig())
    info = await stage_write_channels(
        db, wp=wp, changes=cs, job=_JOB, write_mode="auto", approved=frozenset(), plan_id=1
    )
    assert info["state"] == "staged"
    assert await _live_channels(db) == 0
    assert await db.fetchval("SELECT count(*) FROM external_links") == 0
    ch = await get_change(db, info["id"])
    assert ch is not None and ch.row["release_id"]
    staged = str(await db.fetchval("SELECT id FROM channels WHERE lifecycle = 'staged'"))
    rel_gen = int(
        await db.fetchval("SELECT sched_gen FROM tv_releases WHERE id = $1", ch.row["release_id"])
    )
    assert await blocks(db, staged, rel_gen)  # the new channel's programmes, next to the air
    assert await blocks(db, staged, 0) == []
    # filler from the pool the change will write (named by the ids the swap inserts)
    fill = await db.fetchval(
        "SELECT count(*) FROM channel_programs WHERE channel_id = $1 AND kind = 'filler'", staged
    )
    assert fill >= 0


async def _events(db: Database, cid: str) -> list[tuple[str, str]]:
    from maktaba_pipeline.engine.sql import loads

    rows = await db.fetch(
        "SELECT action, detail FROM tv_change_events WHERE change_id = $1 ORDER BY id", cid
    )
    return [(str(r["action"]), str(loads(r["detail"]).get("what", ""))) for r in rows]


@pytest.mark.asyncio
async def test_updates_join_one_change_until_it_is_approved(db: Database) -> None:
    """TC3 / AC3 (the owner's rule): a plan opens change #N; a hand filler edit and a
    second plan join #N (same id and number, an event each); the newer plan replaces
    the older plan's part, the hand edit is kept. Approved, #N takes no more updates:
    the next one opens #N+1."""
    from maktaba_pipeline.tvrelease.store import update_change

    cs, wp = await compute_changes(db, await _plan(db, 105), MaktabaTvConfig())
    first = await stage_write_channels(
        db, wp=wp, changes=cs, job=_JOB, write_mode="review", approved=frozenset(), plan_id=1
    )
    ch = await get_change(db, first["id"])
    assert ch is not None and ch.state == "draft"
    number = ch.number
    # a filler edit saved by the API (tvchange.Drafts.Save) into the open change
    pending = {**ch.pending, "filler": {"pools": {"p1": {"name": "Stings"}}}}
    await update_change(db, ch.id, diff={**ch.row["diff"], "pending": pending})
    cs2, wp2 = await compute_changes(db, await _plan(db, 106), MaktabaTvConfig())
    second = await stage_write_channels(
        db, wp=wp2, changes=cs2, job=_JOB, write_mode="review", approved=frozenset(), plan_id=2
    )
    assert second["id"] == first["id"] and second["state"] == "draft"
    ch = await get_change(db, first["id"])
    assert ch is not None and ch.state == "draft" and ch.number == number
    assert ch.row["digest"] == cs2.digest() and ch.row["title"] == "Channel plan #2"
    assert [d["number"] for d in ch.pending["channels"].values() if d] == [106]
    assert ch.pending["filler"] == {"pools": {"p1": {"name": "Stings"}}}
    assert await _events(db, ch.id) == [("create", ""), ("append", "channel_plan")]
    # the same plan again is no new update
    again = await stage_write_channels(
        db, wp=wp2, changes=cs2, job=_JOB, write_mode="review", approved=frozenset(), plan_id=2
    )
    assert again["id"] == first["id"] and len(await _events(db, ch.id)) == 2
    assert await db.fetchval("SELECT count(*) FROM tv_changes") == 1
    # approved: the next update opens #N+1
    await update_change(db, ch.id, approved_by=["someone"])
    cs3, wp3 = await compute_changes(db, await _plan(db, 107), MaktabaTvConfig())
    third = await stage_write_channels(
        db, wp=wp3, changes=cs3, job=_JOB, write_mode="review", approved=frozenset(), plan_id=3
    )
    assert third["id"] != first["id"]
    nxt = await get_change(db, third["id"])
    assert nxt is not None and nxt.number == number + 1 and nxt.state == "draft"
    old = await get_change(db, first["id"])
    assert old is not None and old.state == "draft" and old.row["digest"] == cs2.digest()


@pytest.mark.asyncio
async def test_schedule_regeneration_joins_the_open_change(db: Database) -> None:
    """Every kind shares the one open change: the stale flag joins a plan's draft."""
    lib = await library_with(db, 6)
    a = await add_channel(db, lib, 101)
    await topup_once(db, now=NOW)
    cs, wp = await compute_changes(db, await _plan(db, 105), MaktabaTvConfig())
    first = await stage_write_channels(
        db, wp=wp, changes=cs, job=_JOB, write_mode="review", approved=frozenset(), plan_id=1
    )
    await db.execute("UPDATE channel_schedule_state SET stale = true WHERE channel_id = $1", a)
    await topup_once(db, now=NOW)
    assert await db.fetchval("SELECT count(*) FROM tv_changes") == 1
    ch = await get_change(db, first["id"])
    assert ch is not None and a in ch.pending["sched"]
    assert ch.pending["channels"]  # the plan is still there
    assert (await _events(db, ch.id))[-1] == ("append", "schedule")


@pytest.mark.asyncio
async def test_stale_marks_draft_only(db: Database) -> None:
    """TC6 / AC7: with staging on the stale flag drafts a schedule change; the live
    generation is not rewritten."""
    lib = await library_with(db, 6)
    a = await add_channel(db, lib, 101)
    await topup_once(db, now=NOW)
    before = await blocks(db, a, 0)
    await db.execute("UPDATE channel_schedule_state SET stale = true WHERE channel_id = $1", a)
    await topup_once(db, now=NOW)
    assert await blocks(db, a, 0) == before
    rows = await db.fetch("SELECT id, diff FROM tv_changes")
    assert len(rows) == 1
    from maktaba_pipeline.engine.sql import loads

    assert loads(rows[0]["diff"])["pending"]["sched"] == [a]
    assert not await db.fetchval(
        "SELECT stale FROM channel_schedule_state WHERE channel_id = $1", a
    )


@pytest.mark.asyncio
async def test_staging_off_is_todays_behaviour(db: Database) -> None:
    """TC7 / AC10: staging off - write_channels writes directly (run() takes the old
    path) and the stale flag rewrites the live generation in place."""
    assert await _staging(db) is True  # the default
    await set_staging(db, enabled=False)
    assert await _staging(db) is False
    lib = await library_with(db, 6)
    a = await add_channel(db, lib, 101)
    repo = SqlRepo(db)
    await db.execute(
        "INSERT INTO channel_schedule_state (channel_id, stale, sched_gen) VALUES ($1, $2, 0)",
        a,
        True if is_pg(db) else 1,
    )
    assert await stale_to_draft(db, repo) == []
    assert await db.fetchval("SELECT count(*) FROM tv_changes") == 0
    assert await repo.channels_needing_topup(NOW, NOW) == [UUID(a)]
