"""Memory safety of transcription: the audio budget, the memory wait, the metrics."""

from __future__ import annotations

import asyncio
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
async def test_registered_metrics_and_memory_are_sampled(db: Database) -> None:
    health.register_metric("stt.test_gauge", lambda: 4.5)
    got = await health.sample_once(db, health.HealthCollector(), host="h1")
    assert got["stt.test_gauge"] == 4.5
    assert 0 < got["mem.free_pct"] <= 100
    n = await db.fetchval(
        "SELECT count(*) FROM health_samples WHERE host = 'h1' AND metric = 'stt.test_gauge'"
    )
    assert n == 1
    health._METRICS.pop("stt.test_gauge", None)


@pytest.mark.asyncio
async def test_the_sampler_takes_a_last_round_with_the_final_readings(db: Database) -> None:
    import asyncio

    health.register_metric("stt.test_lost", lambda: 1.0, final=lambda: 9.0)
    stop = asyncio.Event()
    stop.set()  # stops at once: one round, then the final one
    try:
        await health.run_sampler(db, health.HealthCollector(), stop, host="h2")
        values = await db.fetch(
            "SELECT value FROM health_samples WHERE host = 'h2' AND metric = 'stt.test_lost'"
            " ORDER BY at"
        )
        assert [float(r["value"]) for r in values][-1] == 9.0
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
