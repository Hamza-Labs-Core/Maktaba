"""Story 26.7 §4/D5 — debounced, coalesced group passes."""

from __future__ import annotations

import asyncio
from uuid import uuid4

import pytest

from maktaba_pipeline.classify.group_scheduler import (
    GroupScheduler,
    mark_group_pending,
    run_group,
    take_group_pending,
)

from ..enrich._fake_enrich_db import FakeEnrichDB


@pytest.mark.asyncio
async def test_take_pending_is_one_shot() -> None:
    db = FakeEnrichDB()
    lib = uuid4()
    await mark_group_pending(db, lib)
    assert await take_group_pending(db, lib) is True
    # Second take finds nothing → coalesced.
    assert await take_group_pending(db, lib) is False


@pytest.mark.asyncio
async def test_run_group_runs_both_passes_once() -> None:
    db = FakeEnrichDB()
    lib = uuid4()
    calls: list[str] = []

    async def series(conn: object, library_id: object) -> None:
        calls.append("series")

    async def collections(conn: object, library_id: object) -> None:
        calls.append("collections")

    await mark_group_pending(db, lib)
    ran = await run_group(db, lib, series_detect=series, auto_collections=collections)
    assert ran is True
    assert calls == ["series", "collections"]

    # A second run without a fresh mark is coalesced away.
    ran2 = await run_group(db, lib, series_detect=series, auto_collections=collections)
    assert ran2 is False
    assert calls == ["series", "collections"]


@pytest.mark.asyncio
async def test_debounce_coalesces_burst_into_one_pass() -> None:
    # test_group_passes_debounced_and_coalesced: a burst of N schedules
    # for one library yields exactly one series + one collection pass.
    db = FakeEnrichDB()
    lib = uuid4()
    runs = {"series": 0, "collections": 0}

    async def series(conn: object, library_id: object) -> None:
        runs["series"] += 1

    async def collections(conn: object, library_id: object) -> None:
        runs["collections"] += 1

    sched = GroupScheduler(db, series_detect=series, auto_collections=collections, delay=0.02)
    for _ in range(50):
        await sched.schedule(lib)
    # Let the single (last) debounce timer fire.
    await asyncio.sleep(0.05)

    assert runs == {"series": 1, "collections": 1}
