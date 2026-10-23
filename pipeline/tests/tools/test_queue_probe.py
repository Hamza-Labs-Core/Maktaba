"""queue_probe: every unprobed video once, watched first, then the newest."""

from __future__ import annotations

from datetime import UTC, datetime

import pytest

from maktaba_pipeline.engine.sql import to_ts
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tools.queue_probe import queue_probe
from tests.stages.world import core_runtime, make_show

_USER = "7a3e1c52-9b0d-4f6e-8a21-5c4d3b2a1f90"


@pytest.mark.asyncio
async def test_queues_each_unprobed_video_once_watched_then_newest(db: Database) -> None:
    await core_runtime(db)
    w = await make_show(db, episodes=3, loose=2)
    videos = [*w.episodes, *w.loose]
    probed, watched, *rest = videos
    # already probed: never queued
    await db.execute(
        "INSERT INTO media_info (video_id, container, video_codec) VALUES ($1, 'mp4', 'h264')",
        str(probed),
    )
    # watched once: queued ahead of the rest
    await db.execute("INSERT INTO users (id, username, pw_hash) VALUES ($1, 'u', 'x')", _USER)
    await db.execute(
        "INSERT INTO playback_state (user_id, video_id, position_sec) VALUES ($1, $2, 10)",
        _USER,
        str(watched),
    )
    # the rest, newest file first
    for i, v in enumerate(rest):
        await db.execute(
            "UPDATE videos SET mtime = $2 WHERE id = $1",
            str(v),
            to_ts(db, datetime(2026, 1, i + 1, tzinfo=UTC)),
        )

    dry = await queue_probe(db)
    assert dry.total == 4 and not dry.applied
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'probe'") == 0

    r = await queue_probe(db, apply=True)
    assert dict(r.tiers) == {"watched, most first": 1, "the rest, newest first": 3}
    rows = await db.fetch(
        "SELECT CAST(subject_id AS TEXT) AS v, priority FROM processing_jobs"
        " WHERE stage = 'probe' ORDER BY priority, id"
    )
    order = [str(r_["v"]) for r_ in rows]
    assert order == [str(watched), *(str(v) for v in reversed(rest))]
    assert rows[0]["priority"] == 90
    again = await queue_probe(db, apply=True)  # open jobs are not queued twice
    assert again.total == 0


@pytest.mark.asyncio
async def test_limit(db: Database) -> None:
    await core_runtime(db)
    await make_show(db, episodes=4, loose=0)
    r = await queue_probe(db, apply=True, limit=2)
    assert r.total == 2
