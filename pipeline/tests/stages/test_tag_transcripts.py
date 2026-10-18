# ruff: noqa: E501  (long SQL strings read better unwrapped)
"""tag reads what was said: active transcripts are evidence, and a new one re-tags its season."""

from __future__ import annotations

import uuid
from collections.abc import Iterator
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.engine.stages import tag
from maktaba_pipeline.runtime import Database
from tests.stages.world import core_runtime, drain_core, make_fake_media, make_show

SEERAH = (
    "يعني يا إخواني نكمل اليوم في السيرة النبوية، وصلنا إلى وفاة عبد المطلب وكفالة أبو طالب، "
    "ثم زواجه من خديجة، وبعدها البعثة ونزول الوحي في غار حراء، ثم الهجرة النبوية."
)


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def add_transcript(
    db: Any, video: UUID, segments: list[str], *, active: bool = True, track: int = 0
) -> str:
    """An audio track, a transcript on it and its segments (one a minute)."""
    await db.execute(
        "INSERT INTO audio_tracks (video_id, track_index) VALUES ($1, $2)", str(video), track
    )
    track_id = await db.fetchval(
        "SELECT id FROM audio_tracks WHERE video_id = $1 AND track_index = $2", str(video), track
    )
    tid = str(uuid.uuid4())
    flag: Any = active if is_pg(db) else int(active)
    await db.execute(
        "INSERT INTO transcripts (id, video_id, audio_track_id, language, backend, model, is_active)"
        " VALUES ($1, $2, $3, 'ar', 'test', 'test', $4)",
        tid,
        str(video),
        track_id,
        flag,
    )
    for seq, text in enumerate(segments):
        await db.execute(
            "INSERT INTO transcript_segments (transcript_id, seq, start_sec, end_sec, text)"
            " VALUES ($1, $2, $3, $4, $5)",
            tid,
            seq,
            float(seq * 60),
            float(seq * 60 + 59),
            text,
        )
    return tid


async def _season_tags(db: Database) -> dict[str, str]:
    rows = await db.fetch("SELECT tag, evidence FROM season_tags")
    return {str(r["tag"]): str(r["evidence"]) for r in rows}


@pytest.mark.asyncio
async def test_a_transcript_retags_its_season_with_what_was_said(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db, name="سلسلة دروس", episodes=12, loose=2)
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt)
    before = await _season_tags(db)
    assert before and "topic:seerah" not in before  # the titles name no topic

    sid = UUID(str(await db.fetchval("SELECT id FROM series_seasons")))
    fp = await tag.input_fingerprint(db, Subject("season", sid))
    # an inactive (superseded) transcript is not read and changes nothing
    await add_transcript(db, w.episodes[0], [SEERAH] * 3, active=False)
    assert await tag.input_fingerprint(db, Subject("season", sid)) == fp

    for v in w.episodes:
        # segments past the reading window (15 min) are not loaded
        await add_transcript(db, v, [SEERAH] * 3 + ["كلام عام"] * 20 + [SEERAH] * 5, track=1)
    assert await tag.input_fingerprint(db, Subject("season", sid)) != fp

    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="tag")
    await drain_core(db, rt)
    after = await _season_tags(db)
    assert "topic:seerah" in after
    assert "transcript 12/12:" in after["topic:seerah"]


@pytest.mark.asyncio
async def test_loose_videos_read_their_transcripts(db: Database) -> None:
    rt = await core_runtime(db)
    w = await make_show(db, name="سلسلة دروس", episodes=12, loose=2)
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt)
    lib = Subject("library", w.library)
    fp = await tag.input_fingerprint(db, lib)
    await add_transcript(db, w.loose[0], [SEERAH] * 3)
    assert await tag.input_fingerprint(db, lib) != fp

    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="tag")
    await drain_core(db, rt)
    tags = {
        str(r["tag"])
        for r in await db.fetch(
            "SELECT tag FROM video_topic_tags WHERE video_id = $1", str(w.loose[0])
        )
    }
    assert "topic:seerah" in tags
    others = {
        str(r["tag"])
        for r in await db.fetch(
            "SELECT tag FROM video_topic_tags WHERE video_id = $1", str(w.loose[1])
        )
    }
    assert "topic:seerah" not in others


@pytest.mark.asyncio
async def test_only_a_loose_video_s_transcript_re_tags_its_library(db: Database) -> None:
    from maktaba_pipeline.engine.claim import row_to_job
    from maktaba_pipeline.engine.graph import Edge
    from maktaba_pipeline.engine.propagate import resolve_edge

    w = await make_show(db, episodes=2, loose=1)
    ep, loose = w.episodes[0], w.loose[0]
    sid = str(uuid.uuid4())
    await db.execute(
        "INSERT INTO series (id, library_id, name) VALUES ($1, $2, 'S')", sid, str(w.library)
    )
    await db.execute(
        "INSERT INTO series_episodes (series_id, video_id, season, episode) VALUES ($1, $2, 1, 1)",
        sid,
        str(ep),
    )
    edge = Edge("tag", "loose_library_of")
    for vid, want in ((ep, []), (loose, [("library", w.library)])):
        r = await enqueue_job(db, subject_kind="video", subject_id=vid, stage="transcribe")
        row = await db.fetchrow("SELECT * FROM processing_jobs WHERE id = $1", r.job_id)
        got = await resolve_edge(db, edge, row_to_job(row), [])
        assert [(k, UUID(str(i))) for k, i in got] == want
