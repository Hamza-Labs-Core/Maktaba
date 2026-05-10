"""Story 6.6 — :class:`Reaper` loop, advisory lock, and parity check."""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta
from typing import Any

import pytest

from maktaba_pipeline.pipeline.reaper import (
    DEFAULT_REAPER_INTERVAL_SEC,
    DEFAULT_STALE_CLAIM_SEC,
    STALE_TO_HEARTBEAT_RATIO,
    Reaper,
)

from ..db._fake_jobs_db import FakeDB


def _ago(seconds: float) -> datetime:
    return datetime.now(UTC) - timedelta(seconds=seconds)


def test_default_constants() -> None:
    """Pin the canonical 30 / 90 / 18 trio."""
    assert DEFAULT_REAPER_INTERVAL_SEC == 30.0
    assert DEFAULT_STALE_CLAIM_SEC == 90.0
    assert STALE_TO_HEARTBEAT_RATIO == 18.0


def test_constructor_enforces_18x_heartbeat_ratio() -> None:
    """Story 6.6 README §1.4.c — config drift fails fast."""
    Reaper(db=FakeDB(), stale_claim_sec=90.0, heartbeat_sec=5.0)  # OK
    with pytest.raises(ValueError, match="18"):
        Reaper(db=FakeDB(), stale_claim_sec=60.0, heartbeat_sec=5.0)
    with pytest.raises(ValueError, match="18"):
        Reaper(db=FakeDB(), stale_claim_sec=90.0, heartbeat_sec=10.0)


def test_constructor_skips_ratio_when_heartbeat_zero() -> None:
    """`heartbeat_sec=0` disables the parity check (used by reaper-only tests)."""
    Reaper(db=FakeDB(), stale_claim_sec=10.0, heartbeat_sec=0.0)


def test_constructor_rejects_non_positive_intervals() -> None:
    with pytest.raises(ValueError, match="interval_sec"):
        Reaper(db=FakeDB(), interval_sec=0, heartbeat_sec=0)
    with pytest.raises(ValueError, match="stale_claim_sec"):
        Reaper(db=FakeDB(), stale_claim_sec=0, heartbeat_sec=0)


@pytest.mark.asyncio
async def test_tick_reaps_stale_rows() -> None:
    db = FakeDB(dialect="postgres")
    db.add(
        state="running",
        last_heartbeat_at=_ago(120),
        last_segment_end_sec=4.0,
    )
    db.add(
        state="claimed",
        last_heartbeat_at=_ago(120),
        last_segment_end_sec=10.0,
    )
    db.add(
        state="running",
        last_heartbeat_at=_ago(1),
    )
    reaper = Reaper(
        db=db,
        stale_claim_sec=90.0,
        heartbeat_sec=5.0,
        interval_sec=30.0,
    )
    n = await reaper.tick()
    assert n == 2


@pytest.mark.asyncio
async def test_tick_busy_lock_returns_zero() -> None:
    """Second concurrent tick on the same Postgres advisory lock is a no-op."""
    db = FakeDB(dialect="postgres")
    db.add(state="running", last_heartbeat_at=_ago(120))
    db.pg_advisory_locked = True  # simulate another instance holding it
    reaper = Reaper(
        db=db,
        stale_claim_sec=90.0,
        heartbeat_sec=5.0,
        interval_sec=30.0,
    )
    n = await reaper.tick()
    assert n == 0
    # Row still in running (not reaped).
    assert all(r.state == "running" for r in db.rows.values())


@pytest.mark.asyncio
async def test_sqlite_local_lock_serializes_ticks() -> None:
    """SQLite's local mutex prevents two ticks from overlapping in one process."""
    db = FakeDB(dialect="sqlite")
    db.add(state="running", last_heartbeat_at=_ago(120))
    reaper = Reaper(
        db=db,
        stale_claim_sec=90.0,
        heartbeat_sec=5.0,
        interval_sec=30.0,
    )
    # First tick acquires + releases. Second should still get the lock.
    n1 = await reaper.tick()
    n2 = await reaper.tick()
    assert n1 == 1
    assert n2 == 0  # row already reaped


@pytest.mark.asyncio
async def test_loop_runs_periodically_and_stops() -> None:
    db = FakeDB(dialect="sqlite")
    db.add(state="running", last_heartbeat_at=_ago(120))
    reaper = Reaper(
        db=db,
        stale_claim_sec=10.0,
        heartbeat_sec=0.0,
        interval_sec=0.05,
    )
    reaper.start()
    await asyncio.sleep(0.12)
    await reaper.stop()
    assert db.rows[1].state == "paused"
    assert db.rows[1].paused_reason == "crash"


@pytest.mark.asyncio
async def test_loop_swallows_db_error_and_keeps_running() -> None:
    """If `tick` raises, the loop logs and continues to the next iteration."""

    class FlakyDB(FakeDB):
        raised: bool = False

        async def fetchrow(self, sql: str, *args: Any) -> Any:
            if not FlakyDB.raised and "WITH stale AS" in " ".join(sql.split()):
                FlakyDB.raised = True
                raise RuntimeError("boom")
            return await super().fetchrow(sql, *args)

    db = FlakyDB(dialect="postgres")
    db.add(state="running", last_heartbeat_at=_ago(120))
    reaper = Reaper(
        db=db,
        stale_claim_sec=10.0,
        heartbeat_sec=0.0,
        interval_sec=0.05,
    )
    reaper.start()
    await asyncio.sleep(0.18)
    await reaper.stop()


@pytest.mark.asyncio
async def test_double_start_raises() -> None:
    db = FakeDB(dialect="sqlite")
    reaper = Reaper(db=db, stale_claim_sec=10.0, heartbeat_sec=0.0, interval_sec=1.0)
    reaper.start()
    try:
        with pytest.raises(RuntimeError, match="already started"):
            reaper.start()
    finally:
        await reaper.stop()
