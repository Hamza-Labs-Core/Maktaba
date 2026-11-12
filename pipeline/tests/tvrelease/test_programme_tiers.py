"""Epic 57 story 57.10: a planned programme carries its video's age tier
(``channel_programs.age_tier``), and a copied generation keeps it."""

from __future__ import annotations

from datetime import timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.channels.base import Block, BlockKind
from maktaba_pipeline.channels.repo import SqlRepo
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tvrelease.generation import _copy_channel
from tests.tvrelease.helpers import NOW, add_channel, library_with

pytestmark = pytest.mark.integration


async def _tiers(db: Any, channel: str, gen: int) -> list[int | None]:
    rows = await db.fetch(
        "SELECT age_tier FROM channel_programs WHERE channel_id = $1 AND sched_gen = $2"
        " ORDER BY start_at",
        channel,
        gen,
    )
    return [None if r["age_tier"] is None else int(r["age_tier"]) for r in rows]


@pytest.mark.asyncio
async def test_planned_programmes_carry_their_video_s_tier(db: Database) -> None:
    lib = await library_with(db, 2)
    ids = [
        UUID(str(r["id"]))
        for r in await db.fetch(
            "SELECT id FROM videos WHERE library_id = $1 ORDER BY title", str(lib)
        )
    ]
    await db.execute("UPDATE videos SET age_tier = 16 WHERE id = $1", str(ids[0]))
    ch = await add_channel(db, lib, 301)

    def block(seq: int, kind: BlockKind, video: UUID | None) -> Block:
        start = NOW + timedelta(minutes=30 * seq)
        return Block(
            channel_id=UUID(ch),
            seq=seq,
            kind=kind,
            start_at=start,
            end_at=start + timedelta(minutes=30),
            source_offset=0,
            source_duration=30 * 60_000,
            video_id=video,
        )

    plan = [
        block(0, BlockKind.PROGRAM, ids[0]),  # rated 16
        block(1, BlockKind.PROGRAM, ids[1]),  # unrated
        block(2, BlockKind.SLATE, None),  # no video
    ]
    await SqlRepo(db, gen=0).replace_future_blocks(UUID(ch), from_at=NOW, blocks=plan)
    assert await _tiers(db, ch, 0) == [16, None, None]

    # the tier is the video's when the guide is planned again
    await db.execute("UPDATE videos SET age_tier = 18 WHERE id = $1", str(ids[1]))
    await SqlRepo(db, gen=0).replace_future_blocks(UUID(ch), from_at=NOW, blocks=plan)
    assert await _tiers(db, ch, 0) == [16, 18, None]

    # a copied generation keeps what was planned
    assert await _copy_channel(db, ch, 0, 7) == 3
    assert await _tiers(db, ch, 7) == [16, 18, None]
