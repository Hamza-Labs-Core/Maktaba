"""Every job the engine runs reports its life to the agent (``job`` events), on both DBs."""

from __future__ import annotations

from collections.abc import Iterator
from typing import Any

import pytest

from maktaba_pipeline.cluster.agent import jobs as job_events
from maktaba_pipeline.db.jobs import DBConn, Job
from maktaba_pipeline.engine.context import JobDeferred, StageContext, StageFailure
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import StageResult
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.engine.toy import Recorder, drain, setup


@pytest.fixture
def events() -> Iterator[list[dict[str, Any]]]:
    seen: list[dict[str, Any]] = []
    t = job_events.tracker()
    t.add_sink(seen.append)
    yield seen
    t.remove_sink(seen.append)


def _for(events: list[dict[str, Any]], stage: str) -> list[str]:
    return [e["event"] for e in events if e["stage"] == stage]


@pytest.mark.asyncio
async def test_done_with_progress_and_output(db: Database, events: list[dict[str, Any]]) -> None:
    rec = Recorder()

    async def work(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        await ctx.progress("decode", 1, 4, force=True)
        await ctx.progress("decode", 2, 4)  # throttled
        return StageResult(output_hash="h1", summary={"audio_sec": 12.5})

    rec.hooks["t_a"] = work
    rt = await setup(db, rec)
    v = await make_video(db, await make_library(db))
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert _for(events, "t_a") == ["claimed", "progress", "done"]
    claimed, prog, done = [e for e in events if e["stage"] == "t_a"]
    assert claimed["subject_kind"] == "video" and claimed["subject_id"] == str(v)
    assert prog["progress"] == {"step": "decode", "done": 1, "total": 4}
    assert done["output"] == {"audio_sec": 12.5} and done["duration_ms"] >= 0
    assert done["title"] is not None  # the subject's label, read once at start
    assert job_events.tracker().running() == []


@pytest.mark.asyncio
async def test_failed_deferred_and_skipped(db: Database, events: list[dict[str, Any]]) -> None:
    rec = Recorder(inputs={"t_a": "in-1"})
    rt = await setup(db, rec)
    lib = await make_library(db)
    v = await make_video(db, lib)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    await enqueue_job(db, subject_kind="video", subject_id=v, stage="t_a")
    await drain(db, rt)
    assert _for(events, "t_a") == ["claimed", "done", "claimed", "skipped"]

    async def fail(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise StageFailure("ffmpeg_failed", "exit 1", retryable=False)

    async def later(db_: DBConn, job: Job, ctx: StageContext) -> StageResult:
        raise JobDeferred("short of memory", 60)

    rec.hooks["t_b"] = fail
    rec.hooks["t_c"] = later
    events.clear()
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="t_b")
    await drain(db, rt)
    failed = [e for e in events if e["stage"] == "t_b"]
    assert [e["event"] for e in failed] == ["claimed", "failed"]
    assert failed[1]["outcome"]["kind"] == "ffmpeg_failed"
    assert failed[1]["outcome"]["retryable"] is False
    assert failed[1]["outcome"]["error"] == "exit 1"
    events.clear()
    from maktaba_pipeline.engine.graph import CATALOG_ID

    await enqueue_job(db, subject_kind="catalog", subject_id=CATALOG_ID, stage="t_c")
    await drain(db, rt)
    deferred = [e for e in events if e["stage"] == "t_c"]
    assert [e["event"] for e in deferred] == ["claimed", "deferred"]
    assert deferred[1]["outcome"] == {
        "kind": "deferred",
        "retryable": True,
        "error": "short of memory",
    }
