"""Maktaba TV keeps airing the live broadcasts stored in the old dizqueTV settings.

The dizqueTV export is gone, but slot 0215 drops no data: ``rules.dizquetv`` stays, and
while the ``tv`` rules name no live broadcast of their own the scheduler reads its ``live``
list (``rules.legacy_tv_live``), so no channel loses a broadcast at the update.
"""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.channels.repo import SqlRepo
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.schedule.tv_settings import LiveSlot
from tests.stages.world import set_rules

NEWS: dict[str, Any] = {
    "channel": 105,
    "url": "https://www.youtube.com/@aljazeera/live",
    "title": "Live",
    "start": "20:00",
    "end": "22:00",
    "every_minutes": 60,
    "minutes": 10,
}
TALK: dict[str, Any] = {**NEWS, "channel": 102, "title": "Talk", "start": "08:00"}


async def _live(db: Database) -> tuple[LiveSlot, ...]:
    return tuple((await SqlRepo(db).load_tv()).live)


@pytest.mark.asyncio
async def test_maktaba_tv_live_falls_back_to_the_old_dizquetv_rows(db: Database) -> None:
    assert await _live(db) == ()
    # the export's other settings are of no use any more: only live is read
    await set_rules(db, "dizquetv", {"live": [NEWS], "filler_cooldown_min": 30})
    got = await _live(db)
    assert [(s.channel, s.title, s.start, s.end) for s in got] == [(105, "Live", 1200, 1320)]
    assert got == await rules.legacy_tv_live(db)


@pytest.mark.asyncio
async def test_the_tv_rules_own_live_list_wins(db: Database) -> None:
    await set_rules(db, "dizquetv", {"live": [NEWS]})
    await set_rules(db, "tv", {"live": [TALK]})
    assert [s.channel for s in await _live(db)] == [102]
    # an empty own list is "none of its own": the old rows air
    await set_rules(db, "tv", {"live": []})
    assert [s.channel for s in await _live(db)] == [105]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "stored",
    [
        {},
        {"live": []},
        {"live": "not a list"},
        {"live": [{"channel": "x", "url": 5}]},
        ["not", "an", "object"],
    ],
)
async def test_no_usable_old_list_airs_nothing(db: Database, stored: Any) -> None:
    await set_rules(db, "dizquetv", stored)
    assert await _live(db) == ()
