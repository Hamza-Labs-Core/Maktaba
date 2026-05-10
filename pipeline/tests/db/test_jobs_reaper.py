"""Story 6.6 — reaper SQL behaviour."""

from __future__ import annotations

import json
from datetime import UTC, datetime, timedelta

import pytest

from maktaba_pipeline.db.jobs_reaper import REAPER_ADVISORY_LOCK_KEY, reap_once
from maktaba_pipeline.db.pubsub import JOBS_REAPED, get_bus, reset_bus

from ._fake_jobs_db import FakeDB


def _ago(seconds: float) -> datetime:
    return datetime.now(UTC) - timedelta(seconds=seconds)


@pytest.fixture(autouse=True)
def _reset_bus() -> None:
    reset_bus()


@pytest.fixture
def db() -> FakeDB:
    return FakeDB(dialect="postgres")


@pytest.fixture
def sqlite_db() -> FakeDB:
    return FakeDB(dialect="sqlite")


def test_advisory_lock_key_is_pinned() -> None:
    """The 32-bit key is reserved; refactors must not change it."""
    assert REAPER_ADVISORY_LOCK_KEY == 0x6A6F6273


@pytest.mark.asyncio
async def test_reaper_pauses_stale_claim(db: FakeDB) -> None:
    row = db.add(
        stage="transcribe",
        state="running",
        claimed_by="worker-dead",
        claimed_at=_ago(120),
        last_heartbeat_at=_ago(120),
        last_segment_end_sec=42.5,
    )
    reaped = await reap_once(db, stale_claim_sec=90.0)
    assert len(reaped) == 1
    r = reaped[0]
    assert r.id == row.id
    assert r.prev_state == "running"
    assert r.paused_at_sec == 42.5
    # Row mutated.
    after = db.rows[row.id]
    assert after.state == "paused"
    assert after.paused_reason == "crash"
    assert after.claimed_by is None


@pytest.mark.asyncio
async def test_reaper_skips_fresh_heartbeats(db: FakeDB) -> None:
    db.add(
        stage="probe",
        state="running",
        claimed_by="worker-alive",
        last_heartbeat_at=_ago(1),
    )
    reaped = await reap_once(db, stale_claim_sec=90.0)
    assert reaped == []


@pytest.mark.asyncio
async def test_reaper_skips_terminal_states(db: FakeDB) -> None:
    for state in ("done", "failed", "cancelled", "paused", "pending"):
        db.add(
            stage="probe",
            state=state,
            last_heartbeat_at=_ago(1000),
        )
    reaped = await reap_once(db, stale_claim_sec=90.0)
    assert reaped == []


@pytest.mark.asyncio
async def test_reaper_returns_prev_state(db: FakeDB) -> None:
    db.add(state="claimed", last_heartbeat_at=_ago(120))
    db.add(state="running", last_heartbeat_at=_ago(120))
    db.add(state="resuming", last_heartbeat_at=_ago(120))
    reaped = await reap_once(db, stale_claim_sec=90.0)
    states = sorted(r.prev_state for r in reaped)
    assert states == ["claimed", "resuming", "running"]


@pytest.mark.asyncio
async def test_reaper_emits_jobs_reaped_pg(db: FakeDB) -> None:
    db.add(state="running", last_heartbeat_at=_ago(120), last_segment_end_sec=7.0)
    await reap_once(db, stale_claim_sec=90.0)
    notifies = [n for n in db.notifies if n[0] == "jobs.reaped"]
    assert len(notifies) == 1
    payload = json.loads(notifies[0][1])
    assert set(payload.keys()) == {"id", "prev_state", "paused_at_sec"}
    assert payload["paused_at_sec"] == 7.0
    assert payload["prev_state"] == "running"


@pytest.mark.asyncio
async def test_reaper_publishes_to_bus_sqlite(sqlite_db: FakeDB) -> None:
    bus = get_bus()
    queue = await bus.subscribe(JOBS_REAPED)
    sqlite_db.add(
        state="running",
        last_heartbeat_at=_ago(120),
        last_segment_end_sec=11.0,
    )
    await reap_once(sqlite_db, stale_claim_sec=90.0)
    raw = await queue.get()
    payload = json.loads(raw)
    assert payload["paused_at_sec"] == 11.0
    assert payload["prev_state"] == "running"


@pytest.mark.asyncio
async def test_reaper_no_payload_when_nothing_stale(db: FakeDB) -> None:
    db.add(state="running", last_heartbeat_at=_ago(1))
    await reap_once(db, stale_claim_sec=90.0)
    assert [n for n in db.notifies if n[0] == "jobs.reaped"] == []


@pytest.mark.asyncio
async def test_reaper_handles_zero_offset(db: FakeDB) -> None:
    """A row with zero last_segment_end_sec yields paused_at_sec=0."""
    db.add(state="running", last_heartbeat_at=_ago(120), last_segment_end_sec=0.0)
    reaped = await reap_once(db, stale_claim_sec=90.0)
    assert reaped[0].paused_at_sec == 0.0
