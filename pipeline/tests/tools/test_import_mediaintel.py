# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""The mediaintel cache import: dry run by default, and afterwards nothing is recomputed."""

from __future__ import annotations

import json
import sqlite3
from datetime import UTC, datetime, timedelta
from pathlib import Path
from uuid import UUID

import pytest

from maktaba_pipeline.audio.chromaprint import encode
from maktaba_pipeline.classify.production_style import STYLE_VERSION, StyleMetrics
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import SIG_VERSION
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import loads, to_ts
from maktaba_pipeline.tools.import_mediaintel import parse_path_maps, run_import
from tests.engine.factory import make_library, make_video
from tests.stages.world import core_runtime, drain_core, make_fake_media

SCHEMA = """
CREATE TABLE files (path TEXT PRIMARY KEY, folder TEXT, stem TEXT, ext TEXT, size INTEGER, mtime REAL,
  duration_s REAL, width INTEGER, height INTEGER, thumb_path TEXT, thumb_dhash TEXT, youtube_id TEXT,
  link_method TEXT, plex_rating_key TEXT, audio_head TEXT, audio_tail TEXT, audio_version INTEGER,
  frame_hashes TEXT, frames_version INTEGER);
CREATE TABLE folders (folder TEXT PRIMARY KEY, channel_id TEXT, channel_url TEXT, channel TEXT);
CREATE TABLE yt_videos (youtube_id TEXT PRIMARY KEY, title TEXT, upload_date TEXT, date_quality TEXT,
  duration_s REAL, channel_id TEXT, channel TEXT, description TEXT, tab TEXT, source TEXT);
CREATE TABLE yt_playlists (playlist_id TEXT PRIMARY KEY, title TEXT, channel_id TEXT, channel TEXT,
  item_count INTEGER, is_real INTEGER, source TEXT);
CREATE TABLE yt_playlist_items (playlist_id TEXT, youtube_id TEXT, position INTEGER);
CREATE TABLE series (id TEXT PRIMARY KEY, library TEXT, name TEXT, year INTEGER, metadata TEXT, detector_version INTEGER);
CREATE TABLE series_episodes (series_id TEXT, path TEXT, season INTEGER, episode INTEGER, is_user_override INTEGER DEFAULT 0);
CREATE TABLE series_tags (series_id TEXT, tag TEXT, score REAL, evidence TEXT);
CREATE TABLE video_tags (path TEXT, tag TEXT, score REAL);
CREATE TABLE series_style (series_id TEXT, style TEXT, tier INTEGER, confidence REAL, reasons TEXT, sampled INTEGER, version INTEGER);
CREATE TABLE style_overrides (library TEXT, series TEXT, style TEXT);
CREATE TABLE visual_metrics (path TEXT PRIMARY KEY, probed_size INTEGER, probed_mtime REAL, metrics TEXT, version INTEGER);
CREATE TABLE plex_collections (series_id TEXT, section TEXT, rating_key TEXT, title TEXT);
CREATE TABLE index_jobs (path TEXT, stage TEXT, version INTEGER, size INTEGER, mtime REAL, state TEXT, attempts INTEGER, max_attempts INTEGER, error TEXT);
"""

N = 12
EPOCH = datetime(2024, 1, 1, tzinfo=UTC)


def _metrics() -> str:
    m = StyleMetrics(
        sampled_s=120.0, cuts_per_min=0.2, motion=0.8, motion_p90=1.5, still_share=0.1,
        luma_mean=120.0, luma_std=5.0, dark_share=0.0, sat_mean=40.0, sat_std=3.0,
        chroma_spread=2.0, looks_per_min=0.5, top3_share=0.9, revisit_share=0.8,
        insert_share=0.0, insert_luma=0.0, insert_sat=0.0, edge_density=0.05,
    )  # fmt: skip
    return json.dumps(m.to_dict())


async def _library(db) -> tuple[UUID, list[UUID]]:
    lib = await make_library(db, "Cached")
    vids = [
        await make_video(
            db, lib, filename=f"ep{i:02d}.mp4", duration=1500.0 + i, size=1000 + i,
            mtime=EPOCH + timedelta(seconds=i), path=f"Y:/chan/ep{i:02d}.mp4",
        )
        for i in range(N)
    ]  # fmt: skip
    return lib, vids


def _cache(path: Path, *, drop_one: bool = False) -> Path:
    c = sqlite3.connect(path)
    c.executescript(SCHEMA)
    head = encode([0x12345678 + i for i in range(400)])
    c.execute(
        "INSERT INTO folders VALUES ('Y:/chan','UCc','https://youtube.com/channel/UCc','Sheikh')"
    )
    c.execute(
        "INSERT INTO yt_playlists VALUES ('PLcache','Prophets','UCc','Sheikh',12,1,'info_json')"
    )
    for i in range(N):
        p = f"Y:/chan/ep{i:02d}.mp4"
        c.execute("INSERT INTO files VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)", (
            p, "Y:/chan", f"ep{i:02d}", "mp4", 1000 + i, (EPOCH + timedelta(seconds=i)).timestamp(),
            1500.0 + i, 1280, 720, None, f"{0x0F0F0F0F0F0F0F0F + i:016x}", f"yt{i:02d}",
            "info_json" if i % 2 else "exact_title", None, head, head, 1, "f0f0f0f0f0f0f0f0,0f0f0f0f0f0f0f0f", 1,
        ))  # fmt: skip
        c.execute("INSERT INTO yt_videos VALUES (?,?,?,?,?,?,?,?,?,?)", (
            f"yt{i:02d}", f"Prophets - Episode {i + 1}", f"2024-03-{i + 1:02d}", "exact", 1500.0 + i, "UCc", "Sheikh", None, "videos", "info_json"))  # fmt: skip
        c.execute("INSERT INTO yt_playlist_items VALUES ('PLcache',?,?)", (f"yt{i:02d}", i + 1))
        c.execute(
            "INSERT INTO series_episodes VALUES ('s1',?,1,?,?)", (p, i + 1, 1 if i == 0 else 0)
        )
        c.execute(
            "INSERT INTO visual_metrics VALUES (?,?,?,?,?)",
            (p, 1000 + i, (EPOCH + timedelta(seconds=i)).timestamp(), _metrics(), STYLE_VERSION),
        )
        for stage, ver in (
            ("thumbnail", 1),
            ("signature", SIG_VERSION),
            ("visual_style", STYLE_VERSION),
            ("probe", 1),
        ):
            c.execute(
                "INSERT INTO index_jobs VALUES (?,?,?,?,?,?,?,?,?)",
                (p, stage, ver, 1000 + i, 0.0, "done", 1, 3, None),
            )
    c.execute(
        "INSERT INTO series VALUES ('s1','Cached','Prophets',2024,?,2)",
        (json.dumps({"series_key": "prophets", "kind": "episodic", "season": 1, "playlists": ["PLcache"],
                     "extras": [], "attached": [], "duplicates": [], "first_air": "2024-03-01",
                     "last_air": "2024-03-12", "median_duration_s": 1505.0, "folder": "Y:/chan"}),),
    )  # fmt: skip
    c.execute("INSERT INTO series_tags VALUES ('s1','topic:seerah',0.9,'name')")
    c.execute("INSERT INTO series_tags VALUES ('s1','genre:seerah',0.9,'primary')")
    c.execute(
        "INSERT INTO series_style VALUES ('s1','talk',1,0.8,'{\"reasons\": [], \"scores\": {}}',3,?)",
        (STYLE_VERSION,),
    )
    c.execute("INSERT INTO plex_collections VALUES ('s1','2','4242','Cached - Prophets')")
    c.execute(
        "INSERT INTO index_jobs VALUES ('Y:/chan/ep03.mp4','signature',101,1003,0.0,'failed',3,3,'[WinError 121] semaphore timeout')"
    ) if drop_one else None
    c.commit()
    c.close()
    return path


async def _count(db, table: str) -> int:
    return int(await db.fetchval(f"SELECT count(*) FROM {table}"))


@pytest.fixture(autouse=True)
def _media():
    counter: dict[str, int] = {}
    make_fake_media(counter)
    yield counter
    reset_runner()


@pytest.mark.asyncio
async def test_dry_run_reports_and_writes_nothing(db, tmp_path: Path) -> None:
    lib, _ = await _library(db)
    cache = _cache(tmp_path / "cache.db")
    before = {
        t: await _count(db, t)
        for t in ("video_signals", "series_seasons", "processing_jobs", "season_tags")
    }
    report = await run_import(db, cache, ["Cached"], apply=False)
    assert not report.applied and report.matched == N and report.unmatched == []
    assert report.tables["series_seasons"] == (0, 1) and report.tables["video_signals"] == (0, N)
    assert report.jobs_imported > 0
    assert {t: await _count(db, t) for t in before} == before  # rolled back
    assert "DRY RUN" in report.render()
    assert lib


@pytest.mark.asyncio
async def test_apply_imports_data_and_every_live_job_is_then_skipped(
    db, tmp_path: Path, _media
) -> None:
    lib, vids = await _library(db)
    cache = _cache(tmp_path / "cache.db")
    report = await run_import(db, cache, [str(lib)], apply=True)
    assert report.applied and report.matched == N

    # data
    sig = await db.fetchrow("SELECT * FROM video_signals WHERE video_id = $1", str(vids[0]))
    assert (
        sig["thumb_dhash"]
        and sig["audio_head"]
        and sig["style_metrics"]
        and sig["fp_version"] == SIG_VERSION
    )
    assert (
        await _count(db, "youtube_videos") == N and await _count(db, "youtube_playlist_items") == N
    )
    assert (
        await db.fetchval(
            "SELECT count(*) FROM media_metadata_enrichment WHERE provider = 'youtube'"
        )
        == N
    )
    season = await db.fetchrow("SELECT id, episode_count, name FROM series_seasons")
    assert season["episode_count"] == N and season["name"] == "Prophets"
    assert (
        await db.fetchval(
            "SELECT episode_override FROM series_episodes WHERE video_id = $1", str(vids[0])
        )
        == 1
    )
    assert (
        await db.fetchval("SELECT tag FROM season_tags WHERE tag LIKE 'topic:%'") == "topic:seerah"
    )
    assert await db.fetchval("SELECT style FROM season_style") == "talk"
    link = await db.fetchrow(
        "SELECT external_id, created_by_tool FROM external_links WHERE target = 'plex_collection'"
    )
    assert link["external_id"] == "4242" and link["created_by_tool"]

    # synthetic history
    imp = await db.fetch(
        "SELECT stage, outcome, state FROM processing_jobs WHERE outcome = 'imported'"
    )
    stages = {r["stage"] for r in imp}
    assert {
        "thumb_hash",
        "fingerprint",
        "style_sample",
        "link",
        "detect",
        "tag",
        "rate_style",
        "youtube_sync",
    } <= stages
    assert "probe" not in stages
    assert (
        await db.fetchval(
            "SELECT count(*) FROM processing_jobs WHERE stage = 'detect' AND state = 'pending'"
        )
        == 1
    )

    # live: queue every stage again; nothing may be recomputed and the NAS is never touched
    rt = await core_runtime(db)
    before = await _count(db, "processing_jobs")
    for stage in ("thumb_hash", "fingerprint", "style_sample", "link"):
        for v in vids:
            await enqueue_job(db, subject_kind="video", subject_id=v, stage=stage)
    await enqueue_job(db, subject_kind="season", subject_id=UUID(str(season["id"])), stage="tag")
    await enqueue_job(
        db, subject_kind="season", subject_id=UUID(str(season["id"])), stage="rate_style"
    )
    await drain_core(db, rt)
    live = (await db.fetch("SELECT stage, outcome FROM processing_jobs ORDER BY id"))[before - 0 :]
    outcomes = {(r["stage"], r["outcome"]) for r in live}
    assert {o for _, o in outcomes} <= {"skipped_unchanged"}, outcomes
    assert _media.get("calls", 0) == 0
    # the detect queued by the import ends skipped as well: the imported seasons stand
    assert (
        await db.fetchval(
            "SELECT outcome FROM processing_jobs WHERE stage = 'detect' AND outcome <> 'imported'"
        )
        == "skipped_unchanged"
    )
    assert await _count(db, "series_seasons") == 1


@pytest.mark.asyncio
async def test_a_second_apply_adds_no_duplicate_history(db, tmp_path: Path) -> None:
    lib, _ = await _library(db)
    cache = _cache(tmp_path / "cache.db")
    await run_import(db, cache, ["Cached"], apply=True)
    n = await _count(db, "processing_jobs")
    second = await run_import(db, cache, ["Cached"], apply=True)
    assert second.jobs_imported == 0 and second.jobs_skipped_existing > 0
    assert await _count(db, "processing_jobs") == n
    assert lib


@pytest.mark.asyncio
async def test_unmatched_and_changed_files_are_reported_not_imported(db, tmp_path: Path) -> None:
    lib = await make_library(db, "Cached")
    await make_video(
        db, lib, filename="ep00.mp4", size=999, mtime=EPOCH, path="Y:/chan/ep00.mp4"
    )  # size differs
    cache = _cache(tmp_path / "cache.db")
    report = await run_import(db, cache, ["Cached"], apply=True)
    assert report.matched == 0 and len(report.unmatched) == N
    assert any("size or mtime differs" in u for u in report.unmatched)
    assert await _count(db, "video_signals") == 0 and await _count(db, "series_seasons") == 0


@pytest.mark.asyncio
async def test_path_map_rewrites_the_cache_prefix(db, tmp_path: Path) -> None:
    lib = await make_library(db, "Cached")
    for i in range(N):
        await make_video(
            db, lib, filename=f"ep{i:02d}.mp4", size=1000 + i, mtime=EPOCH + timedelta(seconds=i),
            path=f"/mnt/nas/chan/ep{i:02d}.mp4",
        )  # fmt: skip
    cache = _cache(tmp_path / "cache.db")
    report = await run_import(
        db, cache, ["Cached"], path_maps=parse_path_maps(["Y:/=/mnt/nas/"]), apply=False
    )
    assert report.matched == N


@pytest.mark.asyncio
async def test_failed_cache_rows_become_failed_jobs_with_a_cause(db, tmp_path: Path) -> None:
    lib, vids = await _library(db)
    cache = _cache(tmp_path / "cache.db", drop_one=True)
    c = sqlite3.connect(cache)
    c.execute(
        "DELETE FROM index_jobs WHERE path = 'Y:/chan/ep03.mp4' AND stage = 'signature' AND state = 'done'"
    )
    c.commit()
    c.close()
    report = await run_import(db, cache, ["Cached"], apply=True)
    assert report.jobs_failed == 1
    row = await db.fetchrow(
        "SELECT state, error_cause, subject_id FROM processing_jobs WHERE state = 'failed'"
    )
    assert row["error_cause"] == "nas_timeout" and str(row["subject_id"]) == str(vids[3])
    assert lib


@pytest.mark.unit
def test_path_map_parsing() -> None:
    assert parse_path_maps(["Y:\\=/mnt/y/", "Y:/deep/=/other/"]) == [
        ("Y:/deep/", "/other/"),
        ("Y:/", "/mnt/y/"),
    ]
    with pytest.raises(SystemExit):
        parse_path_maps(["nonsense"])
    assert loads(None) is None and to_ts is not None
