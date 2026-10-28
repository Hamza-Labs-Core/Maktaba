"""The owner's rule of 2026-10-28, "only manual needs approval": who made a TV change
(``tv_changes.origin``, slot 0169) decides whether it waits for an admin. The channel
plan's and the schedule's changes are automatic; a person's change, or one a person's
edit joined, is manual."""

from __future__ import annotations

import pytest

from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.config import origin_for
from maktaba_pipeline.tvrelease.stage import collapse_open, stage_change
from maktaba_pipeline.tvrelease.store import create_change, get_change
from tests.tvrelease.helpers import NOW, add_channel, library_with, set_staging

pytestmark = pytest.mark.integration


def test_origin_for() -> None:
    for kind in ("write_channels", "schedule", "auto_update"):
        assert origin_for(kind) == "auto"
    for kind in ("editor", "settings", "manual", ""):
        assert origin_for(kind) == "manual"
    assert origin_for("rollback") == "auto"  # an automatic rollback
    assert origin_for("restage", "11111111-1111-4111-8111-111111111111") == "manual"


@pytest.mark.asyncio
async def test_a_channel_plan_is_automatic_and_goes_on_by_its_rule(db: Database) -> None:
    await set_staging(db, enabled=True)
    lib = await library_with(db, 4)
    chan = await add_channel(db, lib, 101)
    cid = await create_change(
        db, state="draft", title="Channel plan #51", source={"kind": "write_channels"},
        pending={"sched": [chan]}, actor_source="write_channels",
    )  # fmt: skip
    ch = await get_change(db, cid)
    assert ch is not None and ch.row["origin"] == "auto"
    assert await stage_change(db, cid, clock=NOW) == "staged"
    ch = await get_change(db, cid)
    assert ch is not None and ch.row["auto_rule"] == "immediate"


@pytest.mark.asyncio
async def test_a_persons_change_joining_makes_it_manual(db: Database) -> None:
    plan = await create_change(
        db, state="draft", title="Channel plan #52", source={"kind": "write_channels"},
        pending={"sched": []}, actor_source="write_channels",
    )  # fmt: skip
    mine = await create_change(
        db, state="draft", title="TV settings", source={"kind": "settings"},
        pending={"settings": {"tv": {"guide_hours": 6}}}, actor_source="settings",
    )  # fmt: skip
    got = await get_change(db, mine)
    assert got is not None and got.row["origin"] == "manual"
    joined = await collapse_open(db)
    assert joined is not None and joined.id == plan
    assert joined.row["origin"] == "manual"
