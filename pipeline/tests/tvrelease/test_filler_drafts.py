# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""Story 33.12 AC6: hand-made filler edits drafted while staging is on. The staged
generation plans with them, the swap writes them, and a change of them alone is no
noop."""

from __future__ import annotations

from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.channels.base import ChannelDef, ChannelMode
from maktaba_pipeline.channels.repo import ReleaseView, SqlRepo
from maktaba_pipeline.engine.sql import now, to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.build import build_release
from maktaba_pipeline.tvrelease.commit import _apply_filler
from tests.engine.factory import make_library, make_video
from tests.tvrelease.helpers import add_channel

pytestmark = pytest.mark.integration


POOL = "00000000-0000-4000-8000-0000000000f1"
NEW_POOL = "00000000-0000-4000-8000-0000000000f2"
LIVE_ITEM = "00000000-0000-4000-8000-0000000000a1"
ADD_ITEM = "00000000-0000-4000-8000-0000000000a2"
NEW_ITEM = "00000000-0000-4000-8000-0000000000a3"


async def _world(db: Database) -> tuple[str, list[str]]:
    lib = await make_library(db)
    vids = [str(await make_video(db, lib, title=f"clip {i}", duration=30.0 + i)) for i in range(3)]
    ch = await add_channel(db, lib, 101)
    await db.execute(
        "INSERT INTO filler_pools (id, channel_id, name) VALUES ($1, $2, 'Bumpers')", POOL, ch
    )
    await db.execute(
        "INSERT INTO filler_items (id, pool_id, video_id, type, duration_ms) VALUES ($1, $2, $3, 'filler', 30000)",
        LIVE_ITEM,
        POOL,
        vids[0],
    )
    return ch, vids


def _edits(ch: str, vids: list[str]) -> dict[str, Any]:
    return {
        "pools": {
            POOL: {"name": "Stings", "channel_id": ch},
            NEW_POOL: {"name": "Station ids", "channel_id": ch, "created": True},
        },
        "items": {
            LIVE_ITEM: None,
            ADD_ITEM: {"pool_id": POOL, "video_id": vids[1], "type": "bumper"},
            NEW_ITEM: {"pool_id": NEW_POOL, "video_id": vids[2], "type": "station_id"},
        },
    }


def _channel(ch: str) -> ChannelDef:
    return ChannelDef(
        id=UUID(ch), mode=ChannelMode.SHUFFLE, mode_config={}, source_filter=None, library_id=None
    )


def _lists(fs: Any) -> dict[str, list[tuple[str, str]]]:
    return {x.key: [(str(i.filler_item_id), i.kind.value) for i in x.items] for x in fs.lists}


@pytest.mark.asyncio
async def test_the_staged_generation_plans_with_the_drafted_filler(db: Database) -> None:
    ch, vids = await _world(db)
    live = _lists(await SqlRepo(db).load_filler(_channel(ch)))
    assert live == {POOL: [(LIVE_ITEM, "filler")]}
    staged_repo = SqlRepo(db, release=ReleaseView(defs={}, filler=_edits(ch, vids)))
    staged = _lists(await staged_repo.load_filler(_channel(ch)))
    assert staged == {POOL: [(ADD_ITEM, "bumper")], NEW_POOL: [(NEW_ITEM, "filler")]}
    # a pool the change removes plays nothing in the staged generation
    gone = {"pools": {POOL: None}, "items": {}}
    assert (
        _lists(
            await SqlRepo(db, release=ReleaseView(defs={}, filler=gone)).load_filler(_channel(ch))
        )
        == {}
    )
    # nothing on the air changed
    assert await db.fetchval("SELECT count(*) FROM filler_items") == 1


@pytest.mark.asyncio
async def test_the_swap_writes_the_drafted_filler(db: Database) -> None:
    ch, vids = await _world(db)
    await _apply_filler(db, _edits(ch, vids), to_ts(db, now()))
    pools = {
        str(r["id"]): (r["name"], str(r["channel_id"]))
        for r in await db.fetch("SELECT id, name, channel_id FROM filler_pools")
    }
    assert pools == {POOL: ("Stings", ch), NEW_POOL: ("Station ids", ch)}
    items = {
        str(r["id"]): (str(r["pool_id"]), str(r["video_id"]), r["type"], r["duration_ms"])
        for r in await db.fetch("SELECT id, pool_id, video_id, type, duration_ms FROM filler_items")
    }
    assert items == {
        ADD_ITEM: (POOL, vids[1], "bumper", 31000),
        NEW_ITEM: (NEW_POOL, vids[2], "station_id", 32000),
    }
    # a removed pool goes with its clips; a clip drafted into it is not written
    await _apply_filler(
        db,
        {
            "pools": {POOL: None},
            "items": {
                "00000000-0000-4000-8000-0000000000a4": {
                    "pool_id": POOL,
                    "video_id": vids[0],
                    "type": "filler",
                }
            },
        },
        to_ts(db, now()),
    )
    assert {str(r["id"]) for r in await db.fetch("SELECT id FROM filler_pools")} == {NEW_POOL}
    assert {str(r["id"]) for r in await db.fetch("SELECT id FROM filler_items")} == {NEW_ITEM}


@pytest.mark.asyncio
async def test_a_change_of_filler_alone_is_no_noop(db: Database) -> None:
    ch, vids = await _world(db)
    rel, _, _ = await build_release(db, {"filler": _edits(ch, vids)})
    assert rel is not None  # it goes on the air at its swap
    other, _, _ = await build_release(
        db, {"filler": {"pools": {POOL: {"name": "Other", "channel_id": ch}}}}
    )
    assert other is not None and other.id != rel.id  # other edits: another release
    noop, _, _ = await build_release(db, {"filler": {"pools": {}, "items": {}}})
    assert noop is None
