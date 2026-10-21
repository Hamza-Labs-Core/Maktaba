"""Memory safety of transcription: the audio budget, the memory wait, the metrics."""

from __future__ import annotations

import asyncio
import sys
from typing import Any

import pytest

from maktaba_pipeline.engine import health
from maktaba_pipeline.engine.stages import transcribe as tr
from maktaba_pipeline.runtime import Database


@pytest.mark.asyncio
async def test_the_audio_budget_holds_jobs_until_audio_is_released() -> None:
    b = tr.AudioBudget(3600)
    first = await b.acquire(3000)
    waiter = asyncio.create_task(b.acquire(1200))  # 3000 + 1200 > 3600: waits
    await asyncio.sleep(0.05)
    assert not waiter.done()
    await b.release(first)
    assert await asyncio.wait_for(waiter, 1) == 1200
    huge = tr.AudioBudget(3600)
    assert await huge.acquire(10 * 3600) == 3600  # a file longer than the budget runs alone


@pytest.mark.asyncio
async def test_a_job_waits_while_memory_is_short(monkeypatch: pytest.MonkeyPatch) -> None:
    readings = iter([(100.0, 5.0), (100.0, 10.0), (100.0, 50.0)])
    monkeypatch.setattr(tr, "system_memory", lambda: next(readings))
    monkeypatch.setenv("MAKTABA_STT_MIN_FREE_PCT", "20")
    slept: list[float] = []

    async def fast_sleep(s: float) -> None:
        slept.append(s)

    monkeypatch.setattr(asyncio, "sleep", fast_sleep)

    class Ctx:
        notes: list[str] = []

        async def progress(self, *a: Any, **k: Any) -> None:
            self.notes.append(str(a[3]))

    ctx = Ctx()
    await tr.wait_for_memory(ctx)  # type: ignore[arg-type]
    assert len(slept) == 2 and "waiting for memory: 5%" in ctx.notes[0]


@pytest.mark.asyncio
async def test_registered_metrics_and_memory_are_measured() -> None:
    health.register_metric("stt.test_gauge", lambda: 4.5)
    try:
        got = await health.measure(health.HealthCollector())
        assert got["stt.test_gauge"] == 4.5
        assert 0 < got["mem.free_pct"] <= 100
        assert health.health_frame(health.HealthCollector())["stt.test_gauge"] == 4.5
    finally:
        health._METRICS.pop("stt.test_gauge", None)


@pytest.mark.unit
def test_windows_counts_its_own_free_memory(monkeypatch: pytest.MonkeyPatch) -> None:
    """The memory floor reads GlobalMemoryStatusEx on Windows (review of story 38.8, item
    4): before, nothing was read there and a job never waited."""
    monkeypatch.setattr(sys, "platform", "win32")
    monkeypatch.setattr(health, "windows_memory", lambda: (100 << 30, 5 << 30))
    assert health.system_memory() == (float(100 << 30), float(5 << 30))
    monkeypatch.setattr(health, "windows_memory", lambda: None)
    assert health.system_memory() is None


@pytest.mark.unit
@pytest.mark.skipif(sys.platform != "win32", reason="Windows' own count")
def test_windows_memory_is_this_machine_s() -> None:
    from maktaba_pipeline.cluster.capabilities import memory_bytes

    mem = health.windows_memory()
    assert mem is not None and 0 < mem[1] <= mem[0] == memory_bytes()
    total, avail = health.system_memory() or (0.0, 0.0)
    assert total == mem[0] and 0 < avail <= total


@pytest.mark.asyncio
async def test_the_final_readings_at_shutdown() -> None:
    health.register_metric("stt.test_lost", lambda: 1.0, final=lambda: 9.0)
    try:
        assert (await health.measure(health.HealthCollector()))["stt.test_lost"] == 1.0
        final = await health.measure(health.HealthCollector(), final=True)
        assert final["stt.test_lost"] == 9.0  # what a stop now throws away
    finally:
        health._METRICS.pop("stt.test_lost", None)
        health._FINAL.pop("stt.test_lost", None)


@pytest.mark.asyncio
async def test_a_long_memory_shortage_gives_the_job_back_without_using_an_attempt(
    db: Database, tmp_path: Any, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.engine.enqueue import enqueue_job
    from maktaba_pipeline.engine.stages import transcribe as tr
    from tests.stages.world import core_runtime, drain_core, make_show

    monkeypatch.setattr(tr, "system_memory", lambda: (100.0, 5.0))  # 5% free
    monkeypatch.setenv("MAKTABA_STT_MEMORY_WAIT_S", "0")
    rt = await core_runtime(db)
    w = await make_show(db, episodes=1, loose=0)
    media = tmp_path / "a.mp4"
    media.write_bytes(b"\x00" * 64)
    await db.execute("UPDATE videos SET path = $2 WHERE id = $1", str(w.episodes[0]), str(media))
    await enqueue_job(db, subject_kind="video", subject_id=w.episodes[0], stage="transcribe")
    await drain_core(db, rt, only={"transcribe"})
    row = await db.fetchrow(
        "SELECT state, attempts, paused_reason, not_before FROM processing_jobs"
        " WHERE stage = 'transcribe'"
    )
    assert row["state"] == "paused" and "memory" in str(row["paused_reason"])
    assert row["not_before"] is not None
    assert tr.audio_budget().used == 0  # the budget is given back too
    # claiming it again later does not count as a new attempt
    attempts = int(row["attempts"])
    from maktaba_pipeline.engine.claim import claim_for_pool

    await db.execute("UPDATE processing_jobs SET not_before = NULL WHERE stage = 'transcribe'")
    job = await claim_for_pool(db, worker_id="w2", pool="gpu", stages=["transcribe"])
    assert job is not None and job.attempts == attempts
