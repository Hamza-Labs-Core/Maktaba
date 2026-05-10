"""Story 6.3 — progress + heartbeat UPDATEs and notify payload shape."""

from __future__ import annotations

import json
from datetime import UTC, datetime

import pytest

from maktaba_pipeline.db import (
    JOBS_HEARTBEAT,
    JOBS_PROGRESS,
    ProgressTick,
    get_bus,
    reset_bus,
    tick_heartbeat,
    tick_progress,
)

from ._fake_jobs_db import FakeDB


@pytest.fixture(autouse=True)
def _reset_bus() -> None:
    reset_bus()


@pytest.fixture
def db() -> FakeDB:
    return FakeDB(dialect="postgres")


@pytest.fixture
def sqlite_db() -> FakeDB:
    return FakeDB(dialect="sqlite")


@pytest.mark.asyncio
async def test_tick_progress_advances_counters(db: FakeDB) -> None:
    row = db.add(stage="transcribe", state="running")
    await tick_progress(
        db,
        ProgressTick(
            job_id=row.id,
            processed_seconds=10.0,
            segments_completed_delta=1,
            last_segment_end_sec=10.0,
        ),
    )
    assert db.rows[row.id].processed_seconds == 10.0
    assert db.rows[row.id].segments_completed == 1
    assert db.rows[row.id].last_segment_end_sec == 10.0


@pytest.mark.asyncio
async def test_tick_progress_bumps_heartbeat(db: FakeDB) -> None:
    """Progress UPDATE doubles as a heartbeat (same now() on both columns)."""
    row = db.add(stage="transcribe", state="running")
    before = datetime.now(UTC)
    await tick_progress(db, ProgressTick(job_id=row.id, processed_seconds=1.0))
    r = db.rows[row.id]
    assert r.progress_updated_at is not None
    assert r.last_heartbeat_at is not None
    # Same UPDATE → same now() value (within tens of ms).
    delta = abs((r.progress_updated_at - r.last_heartbeat_at).total_seconds())
    assert delta < 0.05
    assert r.progress_updated_at >= before


@pytest.mark.asyncio
async def test_tick_progress_no_op_on_terminal_row(db: FakeDB) -> None:
    row = db.add(stage="transcribe", state="done", processed_seconds=42.0)
    await tick_progress(db, ProgressTick(job_id=row.id, processed_seconds=99.0))
    assert db.rows[row.id].processed_seconds == 42.0


@pytest.mark.asyncio
async def test_tick_progress_no_op_on_paused_row(db: FakeDB) -> None:
    row = db.add(stage="transcribe", state="paused", processed_seconds=42.0)
    await tick_progress(db, ProgressTick(job_id=row.id, processed_seconds=99.0))
    assert db.rows[row.id].processed_seconds == 42.0


@pytest.mark.asyncio
async def test_tick_heartbeat_only_bumps_heartbeat(db: FakeDB) -> None:
    row = db.add(stage="probe", state="running")
    await tick_heartbeat(db, job_id=row.id)
    r = db.rows[row.id]
    assert r.last_heartbeat_at is not None
    assert r.progress_updated_at is None  # untouched


@pytest.mark.asyncio
async def test_tick_heartbeat_no_op_on_paused_row(db: FakeDB) -> None:
    row = db.add(stage="probe", state="paused")
    await tick_heartbeat(db, job_id=row.id)
    assert db.rows[row.id].last_heartbeat_at is None


@pytest.mark.asyncio
async def test_sqlite_tick_progress_publishes_to_bus(
    sqlite_db: FakeDB,
) -> None:
    """SQLite path publishes the canonical §7.10 payload on JOBS_PROGRESS."""
    bus = get_bus()
    queue = await bus.subscribe(JOBS_PROGRESS)
    row = sqlite_db.add(stage="transcribe", state="running", total_duration_seconds=100.0)
    await tick_progress(
        sqlite_db,
        ProgressTick(
            job_id=row.id,
            processed_seconds=25.0,
            segments_completed_delta=2,
            last_segment_end_sec=25.0,
            realtime_factor=0.4,
            estimated_remaining_sec=60.0,
        ),
    )
    raw = await queue.get()
    payload = json.loads(raw)
    expected_keys = {
        "id",
        "video_id",
        "stage",
        "state",
        "last_segment_end_sec",
        "processed_seconds",
        "total_duration_seconds",
        "segments_completed",
        "realtime_factor",
        "estimated_remaining_sec",
        "updated_at",
    }
    assert set(payload.keys()) == expected_keys
    assert payload["id"] == row.id
    assert payload["processed_seconds"] == 25.0
    assert payload["segments_completed"] == 2
    assert payload["realtime_factor"] == 0.4
    assert payload["estimated_remaining_sec"] == 60.0
    assert payload["total_duration_seconds"] == 100.0


@pytest.mark.asyncio
async def test_sqlite_tick_heartbeat_publishes_to_bus(
    sqlite_db: FakeDB,
) -> None:
    bus = get_bus()
    queue = await bus.subscribe(JOBS_HEARTBEAT)
    row = sqlite_db.add(stage="probe", state="running")
    await tick_heartbeat(sqlite_db, job_id=row.id)
    raw = await queue.get()
    payload = json.loads(raw)
    assert set(payload.keys()) == {"id", "stage", "last_heartbeat_at"}
    assert payload["id"] == row.id
    assert payload["stage"] == "probe"


@pytest.mark.asyncio
async def test_sqlite_tick_heartbeat_silent_on_paused(
    sqlite_db: FakeDB,
) -> None:
    """Paused row → no UPDATE → no bus publish."""
    bus = get_bus()
    queue = await bus.subscribe(JOBS_HEARTBEAT)
    row = sqlite_db.add(stage="probe", state="paused")
    await tick_heartbeat(sqlite_db, job_id=row.id)
    assert queue.empty()


@pytest.mark.asyncio
async def test_progress_payload_no_jobs_heartbeat_publish(
    sqlite_db: FakeDB,
) -> None:
    """Progress tick must NOT publish on JOBS_HEARTBEAT (only JOBS_PROGRESS)."""
    bus = get_bus()
    hb_queue = await bus.subscribe(JOBS_HEARTBEAT)
    row = sqlite_db.add(stage="transcribe", state="running")
    await tick_progress(sqlite_db, ProgressTick(job_id=row.id, processed_seconds=5.0))
    assert hb_queue.empty()


@pytest.mark.asyncio
async def test_tick_progress_missing_row_is_silent(db: FakeDB) -> None:
    """Row id that doesn't exist yields no error, no exception."""
    await tick_progress(db, ProgressTick(job_id=99999, processed_seconds=1.0))
    # No exception — that's the contract.
