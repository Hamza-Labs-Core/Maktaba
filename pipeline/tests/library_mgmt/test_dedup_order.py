"""Epic 47 "Pipeline order": per video, convert then dedup, through the queue (real
engine: claim, dispatch, the graph edge; real clips; real SQLite, Postgres with
MAKTABA_TEST_PG_URL).

* A's duplicate check is queued the moment A's conversion finishes, while B is still
  waiting for its own; it compares A with converted files only.
* No item gets a check before its conversion; the library's "Find duplicates" fans out
  to the converted items and queues the conversion of the others.
* Two checks that see one pair at once make one group, and its apply is one job.
* A library with nothing to convert still gets its checks.
"""

from __future__ import annotations

import asyncio
from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.db.jobs import Job
from maktaba_pipeline.engine.claim import claim_for_pool
from maktaba_pipeline.engine.dispatch import EngineRuntime, dispatch_job
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import EPIC47_PRIORITIES, bound_graph
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages import thumb_small
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.library_mgmt import convert as cv
from maktaba_pipeline.runtime import Database
from tests.library_mgmt.dedup_world import probe_into_db, scan, video_at
from tests.library_mgmt.epic47_world import World, make_world
from tests.library_mgmt.media_fixtures import mp4_faststart, needs_ffmpeg, reencode_to_mp4

_POOL = {"convert_mp4": "convert", "dedup_scan": "nas_read", "dedup_apply": "nas_write"}


@pytest.fixture(autouse=True)
def _dirs(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(cv, "WORK_ROOT", str(tmp_path / "work-convert"))
    monkeypatch.setattr(thumb_small, "ROOT", str(tmp_path / "thumbs"))


class Worker:
    """A home worker on the real graph."""

    def __init__(self, db: Any) -> None:
        self.db = db
        graph = bound_graph()
        self.graph = graph
        self.rt = EngineRuntime(graph={s.name: s for s in graph})

    async def run(self, stage: str) -> Job | None:
        job = await claim_for_pool(
            self.db, worker_id="home", pool=_POOL[stage], stages=[stage], scope="global"
        )
        if job is not None:
            await dispatch_job(self.db, job, runtime=self.rt, legacy_handlers={})
        return job


async def worker(db: Any) -> Worker:
    w = Worker(db)
    await sync_graph(db, w.graph)
    return w


async def jobs(db: Any, stage: str) -> list[tuple[str, str, str | None]]:
    """(subject id, state, outcome) of a stage's jobs, oldest first."""
    return [
        (str(r["subject_id"]), str(r["state"]), None if r["outcome"] is None else str(r["outcome"]))
        for r in await db.fetch(
            "SELECT subject_id, state, outcome FROM processing_jobs WHERE stage = $1 ORDER BY id",
            stage,
        )
    ]


async def convert(db: Any, vid: str) -> None:
    await enqueue_job(
        db,
        subject_kind="video",
        subject_id=vid,
        stage="convert_mp4",
        priority=EPIC47_PRIORITIES["convert_mp4"],
    )


async def _pair(db: Database, tmp_path: Path) -> tuple[World, str, str]:
    """Two MP4 files of one lecture (B re-encoded from A): the same video, two files."""
    w = await make_world(db, tmp_path)
    (w.root / "a").mkdir()
    (w.root / "b").mkdir()
    a = mp4_faststart(w.root / "a" / "Lecture 1.mp4", seconds=6.0)
    b = reencode_to_mp4(a, w.root / "b" / "Lecture 1.mp4")
    await scan(db, w)
    va, vb = await video_at(db, a), await video_at(db, b)
    for v, p in ((va, a), (vb, b)):
        await probe_into_db(db, v, p)
    return w, va, vb


@needs_ffmpeg
@pytest.mark.asyncio
async def test_a_check_follows_its_own_conversion_not_the_librarys(
    db: Database, tmp_path: Path
) -> None:
    w, va, vb = await _pair(db, tmp_path)
    h = await worker(db)
    await convert(db, va)
    await convert(db, vb)
    assert await jobs(db, "dedup_scan") == []  # nothing before a conversion

    # A converts (already an MP4: done at once); its check is queued, B still waits
    assert (await h.run("convert_mp4")) is not None
    conv = await jobs(db, "convert_mp4")
    assert [s for _, s, _ in conv] == ["done", "pending"]
    assert await jobs(db, "dedup_scan") == [(va, "pending", None)]

    # A's check sees no candidate: B is not converted yet
    assert (await h.run("dedup_scan")) is not None
    ((_, state, _),) = await jobs(db, "dedup_scan")
    assert state == "done"
    assert await db.fetchval("SELECT COUNT(*) FROM duplicate_groups") == 0

    # B converts; its check finds A
    assert (await h.run("convert_mp4")) is not None
    assert (await jobs(db, "dedup_scan"))[-1] == (vb, "pending", None)
    assert (await h.run("dedup_scan")) is not None
    g = await db.fetchrow("SELECT id, tier, status FROM duplicate_groups")
    assert (g["tier"], g["status"]) == ("same_video", "open")
    members = {str(r["video_id"]) for r in await db.fetch("SELECT video_id FROM duplicate_members")}
    assert members == {va, vb}


@needs_ffmpeg
@pytest.mark.asyncio
async def test_two_checks_at_once_make_one_group_and_one_apply(
    db: Database, tmp_path: Path
) -> None:
    w, va, vb = await _pair(db, tmp_path)
    h = await worker(db)
    await convert(db, va)
    await convert(db, vb)
    await h.run("convert_mp4")
    await h.run("convert_mp4")
    assert len(await jobs(db, "dedup_scan")) == 2
    # both checks claimed, then run at the same time
    a = await claim_for_pool(
        db, worker_id="w1", pool="nas_read", stages=["dedup_scan"], scope="global"
    )
    b = await claim_for_pool(
        db, worker_id="w2", pool="nas_read", stages=["dedup_scan"], scope="global"
    )
    assert a is not None and b is not None
    await asyncio.gather(
        dispatch_job(db, a, runtime=h.rt, legacy_handlers={}),
        dispatch_job(db, b, runtime=h.rt, legacy_handlers={}),
    )
    assert [s for _, s, _ in await jobs(db, "dedup_scan")] == ["done", "done"]
    groups = await db.fetch("SELECT id FROM duplicate_groups")
    assert len(groups) == 1
    gid = str(groups[0]["id"])
    # an apply asked twice is one live job (one per subject and stage)
    for _ in range(2):
        await enqueue_job(
            db, subject_kind="dup_group", subject_id=gid, stage="dedup_apply", priority=150
        )
    assert [(s, st) for s, st, _ in await jobs(db, "dedup_apply")] == [(gid, "pending")]


@needs_ffmpeg
@pytest.mark.asyncio
async def test_the_library_fans_out_to_converted_items_and_converts_the_rest(
    db: Database, tmp_path: Path
) -> None:
    w = await make_world(db, tmp_path)
    for d in ("a", "b", "c"):
        (w.root / d).mkdir()
    a = mp4_faststart(w.root / "a" / "One.mp4", seconds=3.0)
    b = mp4_faststart(w.root / "b" / "Two.mp4", seconds=4.0)
    c = mp4_faststart(w.root / "c" / "Three.mp4", seconds=5.0)
    await scan(db, w)
    va, vb, vc = await video_at(db, a), await video_at(db, b), await video_at(db, c)
    h = await worker(db)
    await convert(db, va)
    await h.run("convert_mp4")  # A converted; its edge queued A's check
    await convert(db, vc)  # C's conversion still queued; B never planned
    await db.execute("DELETE FROM processing_jobs WHERE stage = 'dedup_scan'")

    await enqueue_job(
        db,
        subject_kind="library",
        subject_id=w.library_id,
        stage="dedup_scan",
        priority=EPIC47_PRIORITIES["dedup_scan"],
    )
    assert (await h.run("dedup_scan")) is not None
    lib = await db.fetchrow(
        "SELECT output_summary FROM processing_jobs WHERE stage = 'dedup_scan'"
        " AND subject_kind = 'library'"
    )
    summary = loads(lib["output_summary"])
    assert (summary["checks"], summary["conversions"], summary["converting"]) == (1, 1, 1)
    checks = [(s, st) for s, st, _ in await jobs(db, "dedup_scan") if s != str(w.library_id)]
    assert checks == [(va, "pending")]  # only the converted item
    pending_conv = {s for s, st, _ in await jobs(db, "convert_mp4") if st == "pending"}
    assert pending_conv == {vb, vc}  # B queued for conversion now, C still queued

    # nothing to convert (both MP4s): each finishes at once and still gets its check
    await h.run("convert_mp4")
    await h.run("convert_mp4")
    checked = {s for s, _, _ in await jobs(db, "dedup_scan") if s != str(w.library_id)}
    assert checked == {va, vb, vc}


@needs_ffmpeg
@pytest.mark.asyncio
async def test_an_unconverted_item_is_never_checked(db: Database, tmp_path: Path) -> None:
    """A check queued by hand before the item's conversion compares nothing; the
    converted item's check does not see it."""
    w, va, vb = await _pair(db, tmp_path)
    h = await worker(db)
    await enqueue_job(db, subject_kind="video", subject_id=vb, stage="dedup_scan", priority=150)
    assert (await h.run("dedup_scan")) is not None
    row = await db.fetchrow("SELECT output_summary FROM processing_jobs WHERE stage = 'dedup_scan'")
    assert loads(row["output_summary"])["skipped"] == "not_converted"
    await convert(db, va)
    await h.run("convert_mp4")
    await h.run("dedup_scan")
    assert await db.fetchval("SELECT COUNT(*) FROM duplicate_groups") == 0
