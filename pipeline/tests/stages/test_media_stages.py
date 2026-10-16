# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""link, thumb_hash, style_sample and their input fingerprints."""

from __future__ import annotations

import json
from datetime import date
from pathlib import Path

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import Subject
from maktaba_pipeline.engine.media import ProcResult, reset_runner, set_runner
from maktaba_pipeline.engine.paths import PathMap
from maktaba_pipeline.engine.sql import loads, to_date
from maktaba_pipeline.engine.stages import link as link_stage
from maktaba_pipeline.engine.stages import thumb_hash
from maktaba_pipeline.engine.stages.link import Candidate, build_index, match_candidates
from tests.engine.factory import make_library, make_video
from tests.stages.world import add_channel, core_runtime, drain_core, make_fake_media


@pytest.fixture(autouse=True)
def _media():
    make_fake_media({})
    yield
    reset_runner()


async def _rows(db, stage):
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


# ------------------------------------------------------------------------------ link (pure)

CANDS = [
    Candidate("a1", "Episode One: The Start", 600.0),
    Candidate("a2", "Episode Two", 610.0),
    Candidate("a3", "Same Title", 300.0),
    Candidate("a4", "Same Title", 900.0),
    Candidate("a5", "A completely different phrase about patience and gratitude", 1200.0),
]


@pytest.mark.unit
def test_match_methods_and_confidence() -> None:
    idx = build_index(CANDS)
    # yt-dlp replaces ':' with a fullwidth look-alike in the filename
    m = match_candidates("Episode One： The Start", 600.0, CANDS, set(), index=idx)
    assert m is not None and (m.youtube_id, m.method, m.confidence) == ("a1", "exact_title", 1.0)
    m = match_candidates("episode two", 610.0, CANDS, set(), index=idx)
    assert m is not None and (m.youtube_id, m.method, m.confidence) == ("a2", "folded_title", 0.9)
    m = match_candidates("Same Title", 899.0, CANDS, set(), index=idx)
    assert m is not None and (m.youtube_id, m.method, m.confidence) == ("a4", "title_duration", 0.8)
    assert match_candidates("Same Title", None, CANDS, set(), index=idx) is None  # ambiguous
    m = match_candidates(
        "A completely different phrase about patience and thanks", 1200.5, CANDS, set(), index=idx
    )
    assert m is not None and (m.youtube_id, m.method, m.confidence) == (
        "a5",
        "fuzzy_title_duration",
        0.6,
    )


@pytest.mark.unit
def test_a_taken_youtube_id_is_never_linked_twice() -> None:
    idx = build_index(CANDS)
    assert match_candidates("episode two", 610.0, CANDS, {"a2"}, index=idx) is None


# ------------------------------------------------------------------------------ link (stage)


async def _yt(db, lib, yt_id, title, dur, channel="UCx"):
    await db.execute(
        "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality,"
        " duration_s, source) VALUES ($1,$2,$3,$4,'exact',$5,'youtube')",
        yt_id, channel, title, to_date(db, date(2024, 5, 1)), dur,
    )  # fmt: skip


@pytest.mark.asyncio
async def test_link_stage_matches_by_title_and_writes_the_enrichment_row(db) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    await add_channel(db, lib, "UCx")
    await _yt(db, lib, "ytA", "Patience in hardship", 1500.0)
    v = await make_video(db, lib, filename="Patience in hardship.mp4", duration=1500.0)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="link")
    await drain_core(db, rt, only={"link"})
    e = await db.fetchrow("SELECT * FROM media_metadata_enrichment WHERE video_id = $1", str(v))
    assert e["provider"] == "youtube" and e["external_id"] == "ytA" and e["confidence"] == 1.0
    assert loads(e["mapped"])["link_method"] == "exact_title"
    assert bool(e["is_accepted"]) is True
    job = (await _rows(db, "link"))[0]
    assert loads(job["output_summary"]) == {
        "linked": True,
        "youtube_id": "ytA",
        "method": "exact_title",
    }
    # the second video cannot take the same YouTube id
    v2 = await make_video(
        db, lib, filename="Patience in hardship.mp4", duration=1500.0, path="/lib/b/p.mp4"
    )
    await enqueue_job(db, subject_kind="video", subject_id=v2, stage="link")
    await drain_core(db, rt, only={"link"})
    assert (
        await db.fetchval(
            "SELECT count(*) FROM media_metadata_enrichment WHERE video_id = $1", str(v2)
        )
        == 0
    )


@pytest.mark.asyncio
async def test_link_never_auto_accepts_over_a_user_edit(db) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    await add_channel(db, lib, "UCx")
    await _yt(db, lib, "ytA", "Patience in hardship", 1500.0)
    v = await make_video(db, lib, filename="Patience in hardship.mp4", duration=1500.0)
    await db.execute(
        "INSERT INTO media_field_provenance (video_id, field, origin) VALUES ($1,'title','user')",
        str(v),
    )
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="link")
    await drain_core(db, rt, only={"link"})
    e = await db.fetchrow(
        "SELECT is_accepted FROM media_metadata_enrichment WHERE video_id = $1", str(v)
    )
    assert bool(e["is_accepted"]) is False


@pytest.mark.asyncio
async def test_link_reads_the_ytdlp_info_json_sidecar_through_the_path_map(
    db, tmp_path: Path
) -> None:
    rt = await core_runtime(db)
    rt.path_map = PathMap.parse(f"Y:/={tmp_path.as_posix()}/")
    lib = await make_library(db)
    (tmp_path / "chan").mkdir()
    (tmp_path / "chan" / "ep1.mp4").write_bytes(b"x")
    (tmp_path / "chan" / "ep1.info.json").write_text(
        json.dumps(
            {"id": "ytSide", "title": "Side title", "upload_date": "20240301", "duration": 1500,
             "channel_id": "UCs", "playlist_id": "PLabc", "playlist_title": "Real list", "playlist_index": 3},
        ),
        encoding="utf-8",
    )  # fmt: skip
    v = await make_video(db, lib, filename="ep1.mp4", duration=1500.0, path="Y:/chan/ep1.mp4")
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="link")
    await drain_core(db, rt, only={"link"})
    e = await db.fetchrow("SELECT external_id, confidence, mapped FROM media_metadata_enrichment")
    assert (e["external_id"], e["confidence"]) == ("ytSide", 1.0)
    assert loads(e["mapped"])["link_method"] == "info_json"
    assert (
        await db.fetchval("SELECT title FROM youtube_videos WHERE youtube_id = 'ytSide'")
        == "Side title"
    )
    assert await db.fetchval(
        "SELECT is_real FROM youtube_playlists WHERE playlist_id = 'PLabc'"
    ) in (1, True)
    assert (
        await db.fetchval("SELECT position FROM youtube_playlist_items WHERE youtube_id = 'ytSide'")
        == 3
    )


@pytest.mark.asyncio
async def test_link_input_fingerprint_follows_its_inputs(db) -> None:
    lib = await make_library(db)
    v = await make_video(db, lib, filename="a.mp4")
    s = Subject("video", v)
    a = await link_stage.input_fingerprint(db, s)
    assert a == await link_stage.input_fingerprint(db, s)
    await db.execute("UPDATE videos SET title = 'new title' WHERE id = $1", str(v))
    assert await link_stage.input_fingerprint(db, s) != a


# ------------------------------------------------------------------------------ thumb_hash


@pytest.mark.asyncio
async def test_thumb_hash_hashes_the_sidecar_thumbnail(db, tmp_path: Path) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    (tmp_path / "show_01.mp4").write_bytes(b"v")
    (tmp_path / "show_01.jpg").write_bytes(b"jpg")
    v = await make_video(db, lib, filename="show_01.mp4", path=str(tmp_path / "show_01.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash"})
    sig = await db.fetchrow(
        "SELECT thumb_dhash, thumb_version FROM video_signals WHERE video_id = $1", str(v)
    )
    assert len(sig["thumb_dhash"]) == 16 and sig["thumb_version"] == 1
    job = (await _rows(db, "thumb_hash"))[0]
    assert loads(job["output_summary"])["source"] == "show_01.jpg"
    # re-queued with the file unchanged: skipped without touching ffmpeg again
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash"})
    assert (await _rows(db, "thumb_hash"))[-1]["outcome"] == "skipped_unchanged"


@pytest.mark.asyncio
async def test_thumb_hash_without_any_thumbnail_records_none(db, tmp_path: Path) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    (tmp_path / "a.mp4").write_bytes(b"v")
    v = await make_video(db, lib, filename="a.mp4", path=str(tmp_path / "a.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash"})
    assert (
        await db.fetchval("SELECT thumb_dhash FROM video_signals WHERE video_id = $1", str(v))
        is None
    )
    assert thumb_hash.find_thumbnail(str(tmp_path / "a.mp4"), None) is None


@pytest.mark.asyncio
async def test_thumb_hash_unreadable_image_fails_with_a_cause(db, tmp_path: Path) -> None:
    async def bad(args, timeout):
        return ProcResult(b"", b"Invalid data found when processing input", 1)

    set_runner(bad)
    rt = await core_runtime(db)
    lib = await make_library(db)
    (tmp_path / "a.mp4").write_bytes(b"v")
    (tmp_path / "a.webp").write_bytes(b"junk")
    v = await make_video(db, lib, filename="a.mp4", path=str(tmp_path / "a.mp4"))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="thumb_hash")
    await drain_core(db, rt, only={"thumb_hash"}, max_jobs=1)
    j = (await _rows(db, "thumb_hash"))[0]
    assert j["state"] == "pending" and j["error_cause"] == "ffmpeg_decode"


# ------------------------------------------------------------------------------ style_sample


@pytest.mark.asyncio
async def test_style_sample_stores_metrics_and_fails_cleanly_without_frames(db) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    v = await make_video(db, lib, filename="show_1.mp4", duration=1500.0)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="style_sample")
    await drain_core(db, rt, only={"style_sample"})
    sig = await db.fetchrow(
        "SELECT style_metrics, style_version FROM video_signals WHERE video_id = $1", str(v)
    )
    m = loads(sig["style_metrics"])
    assert m["sampled_s"] > 0 and sig["style_version"] == 1

    async def blank(args, timeout):
        return ProcResult(b"", b"", 0)

    set_runner(blank)
    v2 = await make_video(db, lib, filename="show_2.mp4", duration=1500.0, path="/lib/two.mp4")
    await enqueue_job(db, subject_kind="video", subject_id=v2, stage="style_sample")
    await drain_core(db, rt, only={"style_sample"}, max_jobs=1)
    j = [r for r in await _rows(db, "style_sample") if str(r["subject_id"]) == str(v2)][0]
    assert j["state"] == "pending" and j["error_cause"].startswith("no_frames")


@pytest.mark.asyncio
async def test_fingerprint_without_a_probed_duration_waits_for_inputs(db) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    v = await make_video(db, lib, filename="show_1.mp4", duration=None)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="fingerprint")
    await drain_core(db, rt, only={"fingerprint"})
    assert (await _rows(db, "fingerprint"))[0]["outcome"] == "waiting_inputs"
    assert await db.fetchval("SELECT count(*) FROM video_signals") == 0
