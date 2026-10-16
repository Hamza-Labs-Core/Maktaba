# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""The two-pass fingerprint flow: detect -> samples -> fp_gate -> candidates -> detect.

Covers the termination argument of plan 06-11 section 3.4 and the convergence tests of section 13:
the loop reaches quiescence, detect runs a bounded number of times, and running the cascade a
second time changes nothing.
"""

from __future__ import annotations

import random
from datetime import date, timedelta

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import loads
from tests.engine.factory import make_library
from tests.stages.world import (
    World,
    add_channel,
    add_youtube_video,
    core_runtime,
    drain_core,
    make_fake_media,
    set_playlist,
)


@pytest.fixture(autouse=True)
def _media():
    counter: dict[str, int] = {}
    make_fake_media(counter)
    yield counter
    reset_runner()


async def _rows(db, stage=None):
    if stage is None:
        return list(await db.fetch("SELECT * FROM processing_jobs ORDER BY id"))
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def _library_with_candidates(db, *, n_eps=12, n_cands=4, n_decoys=2) -> World:
    lib = await make_library(db)
    await add_channel(db, lib)
    w = World(library=lib)
    for i in range(n_eps):
        v, yt = await add_youtube_video(
            db, lib, title=f"Stories of the Prophets - Episode {i + 1}", filename=f"show_{i + 1:02d}.mp4",
            upload=date(2024, 3, 1) + timedelta(days=i), duration=1500.0 + i,
        )  # fmt: skip
        w.episodes.append(v)
        w.youtube[v] = yt
    # unlisted uploads whose titles do not name the series: only the intro can place them
    for i in range(n_cands):
        v, yt = await add_youtube_video(
            db, lib, title=f"Upload number {i} qzx", filename=f"show_c{i + 1:02d}.mp4",
            upload=date(2024, 3, 13) + timedelta(days=i), duration=1510.0 + i,
        )  # fmt: skip
        w.loose.append(v)
        w.youtube[v] = yt
    # same length and dates, a different intro: must stay unplaced
    for i in range(n_decoys):
        v, yt = await add_youtube_video(
            db, lib, title=f"Another thing {i} wvy", filename=f"other_d{i + 1:02d}.mp4",
            upload=date(2024, 3, 14) + timedelta(days=i), duration=1505.0 + i,
        )  # fmt: skip
        w.loose.append(v)
        w.youtube[v] = yt
    await set_playlist(
        db, "PL1", "Stories of the Prophets", [(v, w.youtube[v]) for v in w.episodes]
    )
    return w


@pytest.mark.asyncio
async def test_two_pass_flow_places_candidates_with_a_shared_intro(db, _media) -> None:
    rt = await core_runtime(db)
    w = await _library_with_candidates(db)
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    ran = await drain_core(db, rt)

    season = await db.fetchrow("SELECT id, episode_count, metadata FROM series_seasons")
    meta = loads(season["metadata"])
    assert meta["signature"] == "shared"
    assert season["episode_count"] == 12 + 4  # the four show candidates joined
    placed = {str(r["video_id"]) for r in await db.fetch("SELECT video_id FROM series_episodes")}
    assert {str(v) for v in w.loose[:4]} <= placed
    assert not ({str(v) for v in w.loose[4:]} & placed)  # decoys stay out

    # how much of the NAS was read: samples + candidates only, never all 18 videos
    fps = await _rows(db, "fingerprint")
    fp_videos = {str(j["subject_id"]) for j in fps}
    assert len(fp_videos) == 4 + 6  # 4 samples, then 6 candidates (4 show + 2 decoys)
    assert len(fp_videos & {str(v) for v in w.episodes}) == 4  # the other 8 episodes: never read
    detect_runs = await _rows(db, "detect")
    assert 2 <= len(detect_runs) <= 3
    assert ran.count("fp_gate") >= 1


@pytest.mark.asyncio
async def test_season_without_a_shared_signature_never_reads_its_candidates(db, _media) -> None:
    rt = await core_runtime(db)
    lib = await make_library(db)
    await add_channel(db, lib)
    w = World(library=lib)
    # every episode has a different "intro" (a different filename family)
    for i in range(12):
        v, yt = await add_youtube_video(
            db, lib, title=f"Stories of the Prophets - Episode {i + 1}", filename=f"fam{i}_{i}.mp4",
            upload=date(2024, 3, 1) + timedelta(days=i), duration=1500.0 + i,
        )  # fmt: skip
        w.episodes.append(v)
        w.youtube[v] = yt
    cand, _ = await add_youtube_video(
        db,
        lib,
        title="Upload qzx",
        filename="fam0_c.mp4",
        upload=date(2024, 3, 13),
        duration=1510.0,
    )
    await set_playlist(
        db, "PL1", "Stories of the Prophets", [(v, w.youtube[v]) for v in w.episodes]
    )
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await drain_core(db, rt)
    meta = loads((await db.fetchrow("SELECT metadata FROM series_seasons"))["metadata"])
    assert meta["signature"] == "none"
    assert str(cand) not in {str(j["subject_id"]) for j in await _rows(db, "fingerprint")}


@pytest.mark.asyncio
async def test_running_the_cascade_twice_changes_nothing(db, _media) -> None:
    rt = await core_runtime(db)
    w = await _library_with_candidates(db)
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect")
    await drain_core(db, rt)
    reads_after_first = _media["calls"]
    before = len(await _rows(db))
    await enqueue_job(db, subject_kind="library", subject_id=w.library, stage="detect", force=True)
    await drain_core(db, rt)
    new = (await _rows(db))[before:]
    assert new, "the forced detect itself must have run"
    assert [j["outcome"] for j in new if j["outcome"] == "changed"] == []
    assert _media["calls"] == reads_after_first  # not one more read of the NAS


@pytest.mark.asyncio
async def test_failed_sample_fingerprint_is_not_a_signature(db, _media) -> None:
    """A partial read keeps what was read, marks fp_partial and fails (cause nas_partial_read)."""
    from maktaba_pipeline.engine import media
    from maktaba_pipeline.engine.media import ProcResult

    async def flaky(args, timeout):
        if "chromaprint" in args:
            return ProcResult(b"", b"Invalid argument reading input via ffmpeg", 1)
        return ProcResult(bytes((i * 37) % 251 for i in range(72)), b"", 0)

    media.set_runner(flaky)
    rt = await core_runtime(db)
    w = await _library_with_candidates(db, n_cands=2, n_decoys=0)
    v = w.episodes[0]
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="fingerprint")
    await drain_core(db, rt, only={"fingerprint"}, max_jobs=1)
    job = (await _rows(db, "fingerprint"))[0]
    assert job["state"] == "pending" and job["error_cause"] == "nas_partial_read"
    sig = await db.fetchrow(
        "SELECT fp_partial, frame_hashes FROM video_signals WHERE video_id = $1", str(v)
    )
    assert bool(sig["fp_partial"]) is True and sig["frame_hashes"]


# ------------------------------------------------------------------ convergence property


def _random_library_spec(rng: random.Random):
    shows = []
    for s in range(rng.randint(1, 3)):
        shows.append(
            {
                "family": f"fam{s}",
                "name": f"Series{s} Name",
                "episodes": rng.randint(8, 12),
                "cands": rng.randint(0, 4),
                "decoys": rng.randint(0, 2),
                "start": date(2024, 1, 1) + timedelta(days=200 * s),
            }
        )
    return shows


@pytest.mark.asyncio
@pytest.mark.parametrize("seed", range(6))
async def test_random_libraries_reach_quiescence_within_the_bound(db, _media, seed: int) -> None:
    rng = random.Random(1000 + seed)
    rt = await core_runtime(db)
    lib = await make_library(db)
    await add_channel(db, lib)
    spec = _random_library_spec(rng)
    n_videos = 0
    for s in spec:
        members = []
        for i in range(s["episodes"]):
            v, yt = await add_youtube_video(
                db, lib, title=f"{s['name']} - Episode {i + 1}", filename=f"{s['family']}_{i:02d}.mp4",
                upload=s["start"] + timedelta(days=i), duration=1500.0 + i,
            )  # fmt: skip
            members.append((v, yt))
            n_videos += 1
        for i in range(s["cands"]):
            await add_youtube_video(
                db, lib, title=f"Clip zq{seed}{i} {s['family']}", filename=f"{s['family']}_c{i:02d}.mp4",
                upload=s["start"] + timedelta(days=s["episodes"] + i), duration=1510.0 + i,
            )  # fmt: skip
            n_videos += 1
        for i in range(s["decoys"]):
            await add_youtube_video(
                db, lib, title=f"Thing wv{seed}{i}", filename=f"decoy_{s['family']}{i}.mp4",
                upload=s["start"] + timedelta(days=s["episodes"] + i), duration=1505.0 + i,
            )  # fmt: skip
            n_videos += 1
        await set_playlist(db, f"PL{s['family']}", s["name"], members)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await drain_core(db, rt, limit=800)  # raises if it does not quiesce

    seasons = await db.fetchval("SELECT count(*) FROM series_seasons WHERE retired_at IS NULL")
    total = len(await _rows(db))
    bound = 3 * n_videos + 2 * seasons + 20
    assert total <= bound, f"{total} jobs for {n_videos} videos, {seasons} seasons (bound {bound})"
    assert len(await _rows(db, "detect")) <= 3

    before = len(await _rows(db))
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect", force=True)
    await drain_core(db, rt, limit=800)
    again = (await _rows(db))[before:]
    assert not [j for j in again if j["outcome"] == "changed"]
