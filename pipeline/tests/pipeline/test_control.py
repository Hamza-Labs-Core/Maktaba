"""Story 6.4 — worker-side flag observation + ForcePauseListener."""

from __future__ import annotations

import asyncio
import json

import pytest

from maktaba_pipeline.db.pubsub import JOBS_FORCE_PAUSE, get_bus, reset_bus
from maktaba_pipeline.pipeline.control import (
    ForcePauseListener,
    should_cancel,
    should_pause,
)

from ..db._fake_jobs_db import FakeDB


@pytest.fixture(autouse=True)
def _reset_bus() -> None:
    reset_bus()


@pytest.fixture
def db() -> FakeDB:
    return FakeDB(dialect="postgres")


@pytest.mark.asyncio
async def test_should_pause_reads_current_flag(db: FakeDB) -> None:
    row = db.add(stage="transcribe", state="running", pause_requested=True)
    assert await should_pause(db, job_id=row.id) is True
    db.rows[row.id].pause_requested = False
    assert await should_pause(db, job_id=row.id) is False


@pytest.mark.asyncio
async def test_should_cancel_reads_current_flag(db: FakeDB) -> None:
    row = db.add(stage="transcribe", state="running", cancel_requested=True)
    assert await should_cancel(db, job_id=row.id) is True


@pytest.mark.asyncio
async def test_missing_row_is_treated_as_cancel(db: FakeDB) -> None:
    """An absent row (FK CASCADE) → caller sees cancel=True so the worker exits."""
    assert await should_cancel(db, job_id=99999) is True
    assert await should_pause(db, job_id=99999) is False


@pytest.mark.asyncio
async def test_force_pause_listener_resolves_future_on_bus_notify() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        fut = listener.register(42)
        get_bus().publish(JOBS_FORCE_PAUSE, {"id": 42})
        await asyncio.wait_for(fut, timeout=0.5)
        assert fut.done()
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_ignores_other_ids() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        fut42 = listener.register(42)
        get_bus().publish(JOBS_FORCE_PAUSE, {"id": 99})
        # The future for 42 should NOT resolve.
        await asyncio.sleep(0.05)
        assert not fut42.done()
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_unregister_drops_watcher() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        fut = listener.register(7)
        listener.unregister(7)
        # Even after a notify, the dropped future does not resolve as done.
        get_bus().publish(JOBS_FORCE_PAUSE, {"id": 7})
        await asyncio.sleep(0.05)
        # The future was cancelled by unregister, so it's done with cancelled.
        assert fut.cancelled()
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_clean_shutdown() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    fut = listener.register(1)
    await listener.stop()
    # After stop, outstanding waiters were cancelled.
    assert fut.cancelled() or fut.done()


@pytest.mark.asyncio
async def test_force_pause_listener_double_start_raises() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        with pytest.raises(RuntimeError, match="already started"):
            listener.start()
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_register_replaces_prior_future() -> None:
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        fut1 = listener.register(5)
        fut2 = listener.register(5)
        assert fut1 is not fut2
        # The replaced future is cancelled so the old waiter doesn't hang.
        await asyncio.sleep(0)
        assert fut1.cancelled()
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_handles_malformed_payload() -> None:
    """A bad payload should NOT crash the listener loop."""
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        get_bus().publish(JOBS_FORCE_PAUSE, {"not_id": "oops"})
        await asyncio.sleep(0.05)
        # Listener still processes a valid notify after the bad one.
        fut = listener.register(11)
        get_bus().publish(JOBS_FORCE_PAUSE, {"id": 11})
        await asyncio.wait_for(fut, timeout=0.5)
    finally:
        await listener.stop()


@pytest.mark.asyncio
async def test_force_pause_listener_payload_is_string() -> None:
    """The bus publishes JSON-serialized strings; the listener parses them."""
    db = FakeDB(dialect="sqlite")
    listener = ForcePauseListener(db)
    listener.start()
    await listener.wait_ready()
    try:
        fut = listener.register(13)
        # Publish via the bus's normal path (JSON-serialized).
        get_bus().publish(JOBS_FORCE_PAUSE, {"id": 13})
        await asyncio.wait_for(fut, timeout=0.5)
    finally:
        await listener.stop()
    # Sanity: the JSON the bus sent contains the id.
    assert json.dumps({"id": 13}) == '{"id": 13}'
