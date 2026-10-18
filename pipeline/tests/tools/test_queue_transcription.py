"""queue_transcription: every untranscribed video once, aired first, then episodes."""

from __future__ import annotations

import pytest

from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tools.queue_transcription import queue_transcription
from tests.stages.world import core_runtime, make_show


@pytest.mark.asyncio
async def test_queues_each_untranscribed_video_once_by_tier(db: Database) -> None:
    await core_runtime(db)
    w = await make_show(db, episodes=3, loose=2)
    await db.execute(
        "INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'S')",
        "00000000-0000-0000-0000-0000000000aa",
        str(w.library),
    )
    await db.execute(
        "INSERT INTO series_episodes (series_id, video_id) VALUES ($1, $2)",
        "00000000-0000-0000-0000-0000000000aa",
        str(w.episodes[0]),
    )
    dry = await queue_transcription(db)
    assert dry.total == 5 and not dry.applied
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'transcribe'") == 0

    r = await queue_transcription(db, apply=True)
    assert dict(r.tiers) == {"on Live TV in the next 48 h": 0, "series episodes": 1, "the rest": 4}
    prio = await db.fetchval(
        "SELECT priority FROM processing_jobs WHERE stage = 'transcribe' AND subject_id = $1",
        str(w.episodes[0]),
    )
    assert prio == 20
    again = await queue_transcription(db, apply=True)  # open jobs are not queued twice
    assert again.total == 0


@pytest.mark.asyncio
async def test_limit_and_library_filter(db: Database) -> None:
    await core_runtime(db)
    w = await make_show(db, episodes=4, loose=0)
    r = await queue_transcription(db, apply=True, libraries=[str(w.library)], limit=2)
    assert r.total == 2
