"""Story 6.3 — :class:`HeartbeatTask` lifecycle."""

from __future__ import annotations

import asyncio

import pytest

from maktaba_pipeline.pipeline.heartbeat import (
    DEFAULT_HEARTBEAT_SEC,
    HeartbeatTask,
    heartbeat_for,
)

from ..db._fake_jobs_db import FakeDB


def test_default_interval_is_5_seconds() -> None:
    """Story 6.6's reaper assumes exactly 5 s; pin the constant here."""
    assert DEFAULT_HEARTBEAT_SEC == 5.0


@pytest.mark.asyncio
async def test_heartbeat_ticks_at_interval() -> None:
    db = FakeDB(dialect="sqlite")
    row = db.add(stage="probe", state="running")
    hb = HeartbeatTask(db, job_id=row.id, interval_sec=0.05)
    hb.start()
    await asyncio.sleep(0.18)
    await hb.stop()
    assert db.rows[row.id].last_heartbeat_at is not None


@pytest.mark.asyncio
async def test_heartbeat_for_context_starts_and_stops() -> None:
    db = FakeDB(dialect="sqlite")
    row = db.add(stage="probe", state="running")
    async with heartbeat_for(db, job_id=row.id, interval_sec=0.05):
        await asyncio.sleep(0.12)
    # After context exit the task is stopped; row updated at least once.
    assert db.rows[row.id].last_heartbeat_at is not None


@pytest.mark.asyncio
async def test_heartbeat_for_cleans_up_on_exception() -> None:
    db = FakeDB(dialect="sqlite")
    row = db.add(stage="probe", state="running")
    with pytest.raises(RuntimeError, match="boom"):
        async with heartbeat_for(db, job_id=row.id, interval_sec=0.05):
            await asyncio.sleep(0.05)
            raise RuntimeError("boom")
    # If the task wasn't cancelled the test would hang; reaching here proves it.


@pytest.mark.asyncio
async def test_heartbeat_silent_on_terminal_row() -> None:
    """Heartbeat against a done row is a no-op (state predicate filters)."""
    db = FakeDB(dialect="sqlite")
    row = db.add(stage="probe", state="done")
    hb = HeartbeatTask(db, job_id=row.id, interval_sec=0.05)
    hb.start()
    await asyncio.sleep(0.12)
    await hb.stop()
    # Row's last_heartbeat_at was never set because state filter excluded it.
    assert db.rows[row.id].last_heartbeat_at is None


def test_heartbeat_task_rejects_non_positive_interval() -> None:
    db = FakeDB(dialect="sqlite")
    with pytest.raises(ValueError, match="interval_sec"):
        HeartbeatTask(db, job_id=1, interval_sec=0)
    with pytest.raises(ValueError, match="interval_sec"):
        HeartbeatTask(db, job_id=1, interval_sec=-0.1)


@pytest.mark.asyncio
async def test_heartbeat_double_start_raises() -> None:
    db = FakeDB(dialect="sqlite")
    row = db.add(stage="probe", state="running")
    hb = HeartbeatTask(db, job_id=row.id, interval_sec=0.5)
    hb.start()
    with pytest.raises(RuntimeError, match="already started"):
        hb.start()
    await hb.stop()
