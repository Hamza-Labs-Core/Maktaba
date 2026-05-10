"""Story 6.8 — :class:`ShutdownOrchestrator` drain semantics."""

from __future__ import annotations

import asyncio
import contextlib
import os
import signal
from unittest.mock import patch

import pytest

from maktaba_pipeline.pipeline.shutdown import (
    DEFAULT_SHUTDOWN_GRACE_SEC,
    ShutdownOrchestrator,
)

from ..db._fake_jobs_db import FakeDB


def test_default_grace_sec() -> None:
    assert DEFAULT_SHUTDOWN_GRACE_SEC == 120.0


def test_constructor_rejects_invalid_grace() -> None:
    db = FakeDB()
    with pytest.raises(ValueError, match="grace_sec"):
        ShutdownOrchestrator(db, worker_id="w", grace_sec=-1.0)
    with pytest.raises(ValueError, match="poll_sec"):
        ShutdownOrchestrator(db, worker_id="w", poll_sec=0)


@pytest.mark.asyncio
async def test_drain_with_no_claims_exits_immediately() -> None:
    db = FakeDB(dialect="postgres")
    db.add(state="running", claimed_by="other-worker")
    orch = ShutdownOrchestrator(db, worker_id="w1", grace_sec=2.0, poll_sec=0.05)
    result = await orch.drain()
    assert result == {"cooperative": 0, "forced": 0}


@pytest.mark.asyncio
async def test_drain_sets_pause_requested_for_owned_rows() -> None:
    db = FakeDB(dialect="postgres")
    r1 = db.add(state="running", claimed_by="w1")
    r2 = db.add(state="claimed", claimed_by="w1")
    db.add(state="running", claimed_by="other-worker")
    orch = ShutdownOrchestrator(
        db,
        worker_id="w1",
        grace_sec=0.05,
        poll_sec=0.01,
    )

    # Cooperatively pause both rows after a tick; drain finishes immediately.
    async def helper_pauses() -> None:
        await asyncio.sleep(0.01)
        for row_id in (r1.id, r2.id):
            row = db.rows[row_id]
            row.state = "paused"
            row.pause_requested = False
            row.claimed_by = None

    helper = asyncio.create_task(helper_pauses())
    result = await orch.drain()
    await helper
    assert result["cooperative"] == 2
    assert result["forced"] == 0
    # Rows from other-worker were NOT touched.
    others = [r for r in db.rows.values() if r.id not in (r1.id, r2.id)]
    assert all(r.state == "running" and r.claimed_by == "other-worker" for r in others)


@pytest.mark.asyncio
async def test_drain_force_pauses_after_grace_window() -> None:
    """Stragglers that ignore pause_requested get force-paused."""
    db = FakeDB(dialect="postgres")
    r = db.add(
        state="running",
        claimed_by="w1",
        last_segment_end_sec=12.0,
    )
    orch = ShutdownOrchestrator(
        db,
        worker_id="w1",
        grace_sec=0.05,
        poll_sec=0.02,
    )
    result = await orch.drain()
    assert result == {"cooperative": 0, "forced": 1}
    after = db.rows[r.id]
    assert after.state == "paused"
    assert after.paused_reason == "shutdown"
    assert after.paused_at_sec == 12.0
    assert after.claimed_by is None


@pytest.mark.asyncio
async def test_drain_filters_strictly_by_worker_id() -> None:
    """Force-pause UPDATE only touches the orchestrator's own rows."""
    db = FakeDB(dialect="postgres")
    db.add(state="running", claimed_by="w1")
    other = db.add(state="running", claimed_by="w2")
    orch = ShutdownOrchestrator(
        db,
        worker_id="w1",
        grace_sec=0.05,
        poll_sec=0.02,
    )
    await orch.drain()
    # w2's row is untouched.
    assert db.rows[other.id].state == "running"
    assert db.rows[other.id].claimed_by == "w2"


@pytest.mark.asyncio
async def test_drain_sqlite_path() -> None:
    db = FakeDB(dialect="sqlite")
    r = db.add(state="running", claimed_by="w1", last_segment_end_sec=3.0)
    orch = ShutdownOrchestrator(
        db,
        worker_id="w1",
        grace_sec=0.05,
        poll_sec=0.02,
    )
    await orch.drain()
    assert db.rows[r.id].state == "paused"
    assert db.rows[r.id].paused_reason == "shutdown"


@pytest.mark.asyncio
async def test_run_after_signal_waits_for_event() -> None:
    """`run_after_signal` blocks until the shutdown_event fires."""
    db = FakeDB(dialect="sqlite")
    db.add(state="running", claimed_by="w1")
    orch = ShutdownOrchestrator(
        db,
        worker_id="w1",
        grace_sec=0.05,
        poll_sec=0.02,
    )

    async def trigger() -> None:
        await asyncio.sleep(0.02)
        orch.shutdown_event.set()

    triggerer = asyncio.create_task(trigger())
    result = await orch.run_after_signal()
    await triggerer
    # Either cooperative or forced, depending on timing — but a result happened.
    assert result["cooperative"] + result["forced"] == 1


def test_signal_handler_increments_counter() -> None:
    """Without a running loop, _on_signal still bumps the counter."""
    db = FakeDB(dialect="sqlite")
    orch = ShutdownOrchestrator(db, worker_id="w1", grace_sec=10.0)
    orch._on_signal(signal.SIGTERM)
    assert orch._signal_count == 1
    assert orch.shutdown_event.is_set()


def test_second_signal_calls_os_exit() -> None:
    db = FakeDB(dialect="sqlite")
    orch = ShutdownOrchestrator(db, worker_id="w1", grace_sec=10.0)
    orch._on_signal(signal.SIGTERM)
    with patch.object(os, "_exit") as mock_exit:
        orch._on_signal(signal.SIGTERM)
        mock_exit.assert_called_once_with(130)


@pytest.mark.asyncio
async def test_install_wires_signal_handlers() -> None:
    """install() registers SIGTERM and SIGINT on the loop."""
    db = FakeDB(dialect="sqlite")
    orch = ShutdownOrchestrator(db, worker_id="w1", grace_sec=10.0)
    loop = asyncio.get_event_loop()
    orch.install(loop)
    # Send SIGTERM to ourselves; the orchestrator should pick it up
    # and set the event. Wrapped in a deadline so the test can't hang.
    os.kill(os.getpid(), signal.SIGTERM)
    try:
        await asyncio.wait_for(orch.shutdown_event.wait(), timeout=1.0)
    finally:
        # Restore default handlers so the next test isn't affected.
        for sig in (signal.SIGTERM, signal.SIGINT):
            with contextlib.suppress(NotImplementedError, RuntimeError):
                loop.remove_signal_handler(sig)
    assert orch.shutdown_event.is_set()
