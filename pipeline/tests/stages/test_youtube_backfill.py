"""Epic 47.10 and 47.12: the back catalogue (``youtube_backfill``) and seeding.

A tiny worker loop claims the ``network_yt`` pool the way the engine does (priority, then
id; one job at a time), with yt-dlp faked (:mod:`tests.stages.yt_fake`).
"""

from __future__ import annotations

import json
import pickle
import sys
import types
from collections.abc import Iterator
from datetime import date, timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.sql import is_pg
from maktaba_pipeline.runtime import Database
from tests.extensions.harness import error_of, run_stage, setup_runtime, summary_of
from tests.stages.test_youtube_fetch import (
    VIDEOS,
    add_source,
    link,
    make_lib,
    rows,
    want,
    yid,
)
from tests.stages.yt_fake import FakeYtDlp, entry

from . import test_youtube_fetch as _fetch_tests

#: the fake yt-dlp fixture of the fetch tests
fake = _fetch_tests.fake

POOL_STAGES = ["youtube_sync", "youtube_fetch", "youtube_backfill"]


async def want_old(db: Any, lib: UUID, sid: UUID, ids: list[str]) -> None:
    """Back-catalogue items dated 2010-01-01 onwards, one day apart."""
    for n, i in enumerate(ids):
        d = date(2010, 1, 1) + timedelta(days=n)
        await db.execute(
            "INSERT INTO youtube_downloads (library_id, youtube_id, source_id, state, backfill,"
            " upload_date) VALUES ($1,$2,$3,'wanted',$4,$5)",
            str(lib),
            i,
            str(sid),
            True if is_pg(db) else 1,
            d if is_pg(db) else d.isoformat(),
        )


async def enqueue(db: Any, lib: UUID, stage: str, priority: int) -> int:
    res = await enqueue_job(
        db, subject_kind="library", subject_id=lib, stage=stage, priority=priority
    )
    assert res.job_id is not None, res.outcome
    return res.job_id


async def work(db: Any, rt: EngineRuntime, max_jobs: int = 400) -> list[str]:
    """Claim and run ``network_yt`` jobs until none is claimable; the stages in order."""
    ran: list[str] = []
    for _ in range(max_jobs):
        job = await claim_for_pool(db, worker_id="w1", pool="network_yt", stages=POOL_STAGES)
        if job is None:
            return ran
        ran.append(job.stage)
        await dispatch_job(db, job, runtime=rt, legacy_handlers={})
    raise AssertionError(f"still claimable after {max_jobs} jobs: {ran}")


async def busy(db: Any) -> int:
    n = await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage IN ('youtube_fetch','youtube_backfill',"
        "'youtube_sync') AND state IN ('claimed','running','resuming')"
    )
    return int(n)


@pytest.mark.asyncio
async def test_one_video_a_job_oldest_first_queueing_itself_at_300(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    ids = [yid(n) for n in range(30)]
    await want_old(db, lib, sid, ids)
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_backfill", "library", lib)
    assert job["state"] == "done", error_of(job)
    assert fake.downloaded == ids[:1]  # one video, the oldest; it goes on to convert
    assert summary_of(job)["remaining"] == 29
    nxt = await db.fetchrow(
        "SELECT * FROM processing_jobs WHERE stage = 'youtube_backfill' AND state = 'pending'"
    )
    assert nxt is not None and nxt["priority"] == 300  # through the graph (a request)
    progress = json.loads(job["progress"]) if isinstance(job["progress"], str) else job["progress"]
    assert progress["message"].startswith("backfill: 1 of 30")
    ran = await work(db, rt)
    assert ran == ["youtube_backfill"] * 29
    assert fake.downloaded == ids
    assert await db.fetchval(
        "SELECT count(*) FROM processing_jobs WHERE stage = 'youtube_backfill'"
        " AND state = 'pending'"
    ) == 0  # fmt: skip
    converts = await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'convert_mp4'")
    assert converts == 30


@pytest.mark.asyncio
async def test_a_batch_examines_at_most_25_items(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    ids = [yid(n) for n in range(30)]
    for i in ids:
        fake.downloads[i] = ["members_only"]  # nothing lands: a job goes through 25
    await want_old(db, lib, sid, ids)
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_backfill", "library", lib)
    assert summary_of(job)["skipped"] == 25 and summary_of(job)["remaining"] == 5
    assert await work(db, rt) == ["youtube_backfill"]


@pytest.mark.asyncio
async def test_new_uploads_go_first_and_never_alongside(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    back = [yid(n) for n in range(100)]
    new = [yid(n) for n in range(500, 502)]
    await want_old(db, lib, sid, back)
    await want(db, lib, sid, new)
    rt = await setup_runtime(db, [])
    await enqueue(db, lib, "youtube_backfill", 300)  # queued first
    await enqueue(db, lib, "youtube_fetch", 120)

    late = yid(900)

    async def check(y: str) -> None:
        assert await busy(db) == 1  # one download at a time, whichever stage
        if y == back[2]:
            # a new upload is listed while the back catalogue runs
            await want(db, lib, sid, [late])
            await enqueue(db, lib, "youtube_fetch", 120)

    fake.before_download = check
    ran = await work(db, rt)
    assert sorted(fake.downloaded[:2]) == sorted(new)  # the 2 new ones finish first
    # the late upload comes right after the back-catalogue item that was running
    assert fake.downloaded[2:6] == [back[0], back[1], back[2], late]
    assert sorted(fake.downloaded) == sorted([*back, *new, late])
    assert ran[:2] == ["youtube_fetch", "youtube_fetch"] and ran.count("youtube_fetch") == 3
    got = await rows(db, lib)
    assert all(r["state"] == "done" for r in got.values())


@pytest.mark.asyncio
async def test_a_running_backfill_cannot_share_the_pool(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    await want_old(db, lib, sid, [yid(1)])
    await want(db, lib, sid, [yid(2)])
    rt = await setup_runtime(db, [])
    await enqueue(db, lib, "youtube_backfill", 300)
    first = await claim_for_pool(db, worker_id="w1", pool="network_yt", stages=["youtube_backfill"])
    assert first is not None
    await enqueue(db, lib, "youtube_fetch", 120)
    other = await claim_for_pool(db, worker_id="w2", pool="network_yt", stages=POOL_STAGES)
    assert other is None  # the pool's one slot is taken
    await dispatch_job(db, first, runtime=rt, legacy_handlers={})
    assert await work(db, rt) == ["youtube_fetch"]


@pytest.mark.asyncio
async def test_pausing_the_stage_stops_after_the_current_item(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    ids = [yid(n) for n in range(10)]
    await want_old(db, lib, sid, ids)
    rt = await setup_runtime(db, [])
    yes, no = (True, False) if is_pg(db) else (1, 0)

    async def pause(y: str) -> None:
        if y == ids[2]:
            await db.execute(
                "UPDATE job_stages SET paused = $1 WHERE stage = 'youtube_backfill'", yes
            )

    fake.before_download = pause
    await enqueue(db, lib, "youtube_backfill", 300)
    assert await work(db, rt) == ["youtube_backfill"] * 3  # the item it was on finished
    assert fake.downloaded == ids[:3]
    states = await rows(db, lib)
    assert sorted(i for i, r in states.items() if r["state"] == "wanted") == ids[3:]
    assert await live_backfill(db) is not None  # the next one waits for the resume

    fake.before_download = None
    await db.execute("UPDATE job_stages SET paused = $1 WHERE stage = 'youtube_backfill'", no)
    assert await work(db, rt) == ["youtube_backfill"] * 7
    assert fake.downloaded == ids  # each once


async def live_backfill(db: Any) -> Any:
    return await db.fetchrow(
        "SELECT id FROM processing_jobs WHERE stage = 'youtube_backfill' AND state = 'pending'"
    )


@pytest.mark.asyncio
async def test_cancel_then_resume_carries_on_without_downloading_again(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    sid = await add_source(db, lib)
    ids = [yid(n) for n in range(5)]
    await want_old(db, lib, sid, ids)
    fake.downloads[ids[2]] = ["stop", "ok"]

    async def cancel(y: str) -> None:
        if y == ids[2]:
            await db.execute(
                "UPDATE processing_jobs SET cancel_requested = $1 WHERE stage = 'youtube_backfill'"
                " AND state IN ('claimed','running')",
                True if is_pg(db) else 1,
            )

    fake.before_download = cancel
    rt = await setup_runtime(db, [])
    await enqueue(db, lib, "youtube_backfill", 300)
    assert await work(db, rt) == ["youtube_backfill"] * 3
    last = await db.fetchrow(
        "SELECT state FROM processing_jobs WHERE stage = 'youtube_backfill' ORDER BY id DESC"
    )
    assert last["state"] == "cancelled"
    got = await rows(db, lib)
    assert [got[i]["state"] for i in ids] == ["done", "done", "wanted", "wanted", "wanted"]
    assert await live_backfill(db) is None  # cancel queues no next job

    fake.before_download = None
    await enqueue(db, lib, "youtube_backfill", 300)  # Resume backfill
    assert await work(db, rt) == ["youtube_backfill"] * 3
    assert fake.downloaded == ids  # the two done ones were not fetched again


@pytest.mark.asyncio
async def test_seeded_videos_are_never_backfilled(
    db: Database, tmp_path: Path, fake: FakeYtDlp
) -> None:
    lib = await make_lib(db, tmp_path)
    await add_source(db, lib)
    await link(db, lib, yid(1))
    fake.listings[VIDEOS] = [
        entry(yid(2), upload_date="20100102"),
        entry(yid(1), upload_date="20100101"),
    ]
    rt = await setup_runtime(db, [])
    sync = await run_stage(db, rt, "youtube_sync", "library", lib)
    assert summary_of(sync)["youtube_sources"]["seeded_library"] == 1
    got = await rows(db, lib)
    assert got[yid(1)]["state"] == "done" and got[yid(1)]["reason"] == "library"
    assert got[yid(1)]["video_id"] is not None
    assert got[yid(2)]["state"] == "wanted" and got[yid(2)]["backfill"]
    await work(db, rt)
    assert fake.downloaded == [yid(2)]
    archive = (tmp_path / "lib" / ".maktaba" / "yt-dlp-archive.txt").read_text()
    assert f"youtube {yid(1)}" in archive and f"youtube {yid(2)}" in archive


@pytest.fixture
def media_module() -> Iterator[types.ModuleType]:
    mod = types.ModuleType("media")

    class Video:
        pass

    Video.__module__ = "media"
    Video.__qualname__ = "Video"
    mod.Video = Video  # type: ignore[attr-defined]
    sys.modules["media"] = mod
    yield mod
    del sys.modules["media"]


@pytest.mark.asyncio
async def test_a_tartube_import_seeds_before_the_first_backfill(
    db: Database, tmp_path: Path, fake: FakeYtDlp, media_module: types.ModuleType
) -> None:
    tt = tmp_path / "tt"
    tt.mkdir()
    reg = {}
    for n, (v, flag) in enumerate(((yid(1), True), (yid(2), False)), 1):
        o = media_module.Video.__new__(media_module.Video)
        o.__dict__.update(dbid=n, vid=v, dl_flag=flag, file_name=v, file_ext=".mp4")
        reg[n] = o
    (tt / "tartube.db").write_bytes(pickle.dumps({"media_reg_dict": reg}))
    export = {"file_type": "db_export", "db_dict": {
        str(n): {"type": "video", "dbid": n, "vid": v, "file": f"{v}.mp4"}
        for n, v in ((1, yid(1)), (2, yid(2)))}}  # fmt: skip
    (tt / "tartube_db_export.json").write_text(json.dumps(export))

    lib = await make_lib(db, tmp_path)
    await add_source(db, lib, mode="off")  # nothing on yet: the import comes first
    rt = await setup_runtime(db, [])
    job = await run_stage(
        db, rt, "youtube_sync", "library", lib,
        payload={"tartube": {"path": str(tt / "tartube_db_export.json")}},
    )  # fmt: skip
    assert job["state"] == "done", error_of(job)
    rep = summary_of(job)["youtube_sources"]["tartube"]
    assert rep["seeded"] == 1 and rep["used_pickle"] and rep["not_downloaded"] == 1
    got = await rows(db, lib)
    assert set(got) == {yid(1)} and got[yid(1)]["reason"] == "tartube"

    # turned on later: the Tartube download is known, only the other one is wanted
    await db.execute("UPDATE youtube_sources SET mode = 'new_and_backfill'")
    fake.listings[VIDEOS] = [
        entry(yid(2), upload_date="20100102"),
        entry(yid(1), upload_date="20100101"),
    ]
    await run_stage(db, rt, "youtube_sync", "library", lib)
    await work(db, rt)
    assert fake.downloaded == [yid(2)]


@pytest.mark.asyncio
async def test_a_malicious_tartube_file_fails_the_job_and_runs_nothing(
    db: Database, tmp_path: Path, fake: FakeYtDlp, monkeypatch: pytest.MonkeyPatch
) -> None:
    import os

    class Evil:
        def __reduce__(self) -> Any:
            return (os.system, ("touch PWNED",))

    monkeypatch.chdir(tmp_path)
    bad = tmp_path / "tartube.db"
    bad.write_bytes(pickle.dumps({"media_reg_dict": {1: Evil()}}))
    lib = await make_lib(db, tmp_path)
    rt = await setup_runtime(db, [])
    job = await run_stage(db, rt, "youtube_sync", "library", lib,
                          payload={"tartube": {"path": str(bad)}})  # fmt: skip
    assert job["state"] == "failed" and error_of(job)["kind"] == "tartube_refused"
    assert not (tmp_path / "PWNED").exists()
