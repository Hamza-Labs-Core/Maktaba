"""Slot 0171 (both dialects): an ``audio_tracks`` row with no codec (transcribe recorded
the stream it decoded before the probe ran, and the probe's insert did nothing on the
conflict) gets its stream's facts from ``media_info.raw_ffprobe`` by audio rank. Rows
with a codec are left alone; a second run changes nothing."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.engine.sql import dumps, is_pg, loads
from maktaba_pipeline.runtime import Database
from tests.engine.migrate import MIGRATIONS_DIR, up_statements
from tests.stages.world import make_show

SLOT = "0171_audio_tracks_backfill_codec"

# a matroska file: h264, then cover art, then two audio streams, a subtitle between them
FFPROBE: dict[str, Any] = {
    "format": {"format_name": "matroska,webm"},
    "streams": [
        {"codec_type": "video", "codec_name": "h264"},
        {"codec_type": "video", "codec_name": "mjpeg", "disposition": {"attached_pic": 1}},
        {
            "codec_type": "audio",
            "codec_name": "opus",
            "channels": 2,
            "sample_rate": "48000",
            "tags": {"language": "ara", "title": "Arabic"},
            "disposition": {"default": 1},
        },
        {"codec_type": "subtitle", "codec_name": "subrip"},
        {"codec_type": "audio", "codec_name": "ac3", "channels": 6, "sample_rate": "44100"},
    ],
}


async def _run(db: Database) -> None:
    sql = (MIGRATIONS_DIR / f"{SLOT}{'' if is_pg(db) else '.sqlite'}.sql").read_text("utf-8")
    for stmt in up_statements(sql):
        await db.execute(stmt)


async def _tracks(db: Database, vid: Any) -> list[tuple[Any, ...]]:
    rows = await db.fetch(
        "SELECT track_index, codec, channels, sample_rate, language, title, is_default,"
        " disposition FROM audio_tracks WHERE video_id = $1 ORDER BY track_index",
        str(vid),
    )
    out = []
    for r in rows:
        disp = loads(r["disposition"])
        out.append(
            (
                r["track_index"],
                r["codec"],
                r["channels"],
                r["sample_rate"],
                r["language"],
                r["title"],
                bool(r["is_default"]),
                disp,
            )
        )
    return out


@pytest.mark.asyncio
async def test_0171_fills_codec_less_tracks_from_the_probe(db: Database) -> None:
    w = await make_show(db, episodes=5, loose=0)
    bare, second, kept, unprobed, mismatch = w.episodes
    pg = is_pg(db)
    yes: Any = True if pg else 1
    no: Any = False if pg else 0
    cast = "::jsonb" if pg else ""

    async def probed(vid: Any, raw: Any) -> None:
        await db.execute(
            f"INSERT INTO media_info (video_id, container, video_codec, raw_ffprobe)"
            f" VALUES ($1, 'matroska,webm', 'h264', $2{cast})",
            str(vid),
            raw if isinstance(raw, str) else dumps(raw),
        )

    async def track(vid: Any, idx: int, codec: str | None, default: Any) -> None:
        await db.execute(
            "INSERT INTO audio_tracks (video_id, track_index, codec, is_default)"
            " VALUES ($1, $2, $3, $4)",
            str(vid),
            idx,
            codec,
            default,
        )

    # transcribe's bare track 0, then the probe's DO NOTHING: the live case
    await probed(bare, FFPROBE)
    await track(bare, 0, None, yes)
    # both tracks bare (one empty string); track 1 is the second *audio* stream
    await probed(second, FFPROBE)
    await track(second, 0, "", yes)
    await track(second, 1, None, no)
    # a row with a codec keeps it, even when the probe's output says otherwise
    await probed(kept, FFPROBE)
    await track(kept, 0, "aac", yes)
    # not probed: nothing to fill from
    await track(unprobed, 0, None, yes)
    # a track past the file's audio streams, and output that names no streams
    await probed(mismatch, {"format": {}, "streams": "none"})
    await track(mismatch, 0, None, yes)

    await _run(db)
    opus: tuple[Any, ...] = (0, "opus", 2, 48000, "ara", "Arabic", True, {"default": 1})
    ac3: tuple[Any, ...] = (1, "ac3", 6, 44100, "und", None, False, {})
    assert await _tracks(db, bare) == [opus]
    assert await _tracks(db, second) == [opus, ac3]
    assert await _tracks(db, kept) == [(0, "aac", None, None, "und", None, True, {})]
    assert await _tracks(db, unprobed) == [(0, None, None, None, "und", None, True, {})]
    assert await _tracks(db, mismatch) == [(0, None, None, None, "und", None, True, {})]

    # idempotent: a second run changes nothing, and does not undo a probe's later write
    await db.execute(
        "UPDATE audio_tracks SET channels = 8 WHERE video_id = $1 AND track_index = 0", str(bare)
    )
    await _run(db)
    assert (await _tracks(db, bare))[0][2] == 8
    assert await _tracks(db, second) == [opus, ac3]
