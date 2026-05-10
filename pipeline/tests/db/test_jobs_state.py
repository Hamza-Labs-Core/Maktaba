"""Story 6.4 + 6.5 — state-transition helpers in :mod:`db.jobs_state`."""

from __future__ import annotations

import json
import random
from datetime import UTC, datetime

import pytest

from maktaba_pipeline.db.jobs_state import (
    FailureOutcome,
    StageError,
    mark_cancelled,
    mark_done,
    mark_failed_or_retry,
    mark_paused,
    read_flags,
    retry_failed,
)

from ._fake_jobs_db import FakeDB


@pytest.fixture
def db() -> FakeDB:
    return FakeDB(dialect="postgres")


@pytest.fixture
def sqlite_db() -> FakeDB:
    return FakeDB(dialect="sqlite")


# ---- read_flags --------------------------------------------------------


@pytest.mark.asyncio
async def test_read_flags_returns_state(db: FakeDB) -> None:
    row = db.add(state="running", pause_requested=True, cancel_requested=False)
    flags = await read_flags(db, job_id=row.id)
    assert flags.pause is True
    assert flags.cancel is False


@pytest.mark.asyncio
async def test_read_flags_sqlite_path(sqlite_db: FakeDB) -> None:
    row = sqlite_db.add(state="running", pause_requested=False, cancel_requested=True)
    flags = await read_flags(sqlite_db, job_id=row.id)
    assert flags.pause is False
    assert flags.cancel is True


@pytest.mark.asyncio
async def test_read_flags_missing_row_is_cancel(db: FakeDB) -> None:
    flags = await read_flags(db, job_id=999)
    assert flags.cancel is True


# ---- mark_paused / cancelled / done ------------------------------------


@pytest.mark.asyncio
async def test_mark_paused_sets_state_and_offset(db: FakeDB) -> None:
    row = db.add(state="running", last_segment_end_sec=42.0)
    out = await mark_paused(db, job_id=row.id, at_sec=42.0, reason="user")
    assert out is not None
    assert db.rows[row.id].state == "paused"
    assert db.rows[row.id].paused_at_sec == 42.0
    assert db.rows[row.id].paused_reason == "user"
    assert db.rows[row.id].claimed_by is None
    assert db.rows[row.id].pause_requested is False


@pytest.mark.asyncio
async def test_mark_paused_sqlite(sqlite_db: FakeDB) -> None:
    row = sqlite_db.add(state="running")
    await mark_paused(sqlite_db, job_id=row.id, at_sec=1.5, reason="shutdown")
    assert sqlite_db.rows[row.id].state == "paused"
    assert sqlite_db.rows[row.id].paused_reason == "shutdown"


@pytest.mark.asyncio
async def test_mark_paused_no_op_on_terminal(db: FakeDB) -> None:
    row = db.add(state="done")
    out = await mark_paused(db, job_id=row.id, at_sec=0.0)
    assert out is None
    assert db.rows[row.id].state == "done"


@pytest.mark.asyncio
async def test_mark_cancelled_sets_state(db: FakeDB) -> None:
    row = db.add(state="running")
    out = await mark_cancelled(db, job_id=row.id)
    assert out is not None
    assert db.rows[row.id].state == "cancelled"
    assert db.rows[row.id].cancel_requested is False


@pytest.mark.asyncio
async def test_mark_cancelled_works_on_paused(db: FakeDB) -> None:
    """Cancel after pause is the AC for `test_cancel_after_pause_is_consistent`."""
    row = db.add(state="paused")
    out = await mark_cancelled(db, job_id=row.id)
    assert out is not None
    assert db.rows[row.id].state == "cancelled"


@pytest.mark.asyncio
async def test_mark_cancelled_no_op_on_done(db: FakeDB) -> None:
    row = db.add(state="done")
    out = await mark_cancelled(db, job_id=row.id)
    assert out is None
    assert db.rows[row.id].state == "done"


@pytest.mark.asyncio
async def test_mark_done_terminal_transition(db: FakeDB) -> None:
    row = db.add(state="running", claimed_by="w1")
    out = await mark_done(db, job_id=row.id)
    assert out is not None
    assert db.rows[row.id].state == "done"
    assert db.rows[row.id].claimed_by is None
    assert db.rows[row.id].finished_at is not None


# ---- mark_failed_or_retry (Story 6.5) ----------------------------------


@pytest.mark.asyncio
async def test_first_failure_retries(db: FakeDB) -> None:
    row = db.add(state="running", attempts=1, max_attempts=3)
    err = StageError(kind="net", message="boom", retryable=True)
    rng = random.Random(0)
    outcome = await mark_failed_or_retry(db, job_id=row.id, error=err, rng=rng)
    assert outcome.state == "pending"
    assert outcome.not_before is not None
    # ~60s ± 25%.
    seconds = (outcome.not_before - datetime.now(UTC)).total_seconds()
    assert 40.0 <= seconds <= 80.0
    assert db.rows[row.id].state == "pending"
    assert db.rows[row.id].claimed_by is None


@pytest.mark.asyncio
async def test_max_attempts_terminal_fail(db: FakeDB) -> None:
    row = db.add(state="running", attempts=3, max_attempts=3)
    err = StageError(kind="net", message="last", retryable=True)
    outcome = await mark_failed_or_retry(db, job_id=row.id, error=err)
    assert outcome.state == "failed"
    assert outcome.not_before is None
    assert db.rows[row.id].state == "failed"


@pytest.mark.asyncio
async def test_non_retryable_skips_retries(db: FakeDB) -> None:
    row = db.add(state="running", attempts=1, max_attempts=5)
    err = StageError(kind="oom", message="memory", retryable=False)
    outcome = await mark_failed_or_retry(db, job_id=row.id, error=err)
    assert outcome.state == "failed"
    assert db.rows[row.id].state == "failed"


@pytest.mark.asyncio
async def test_max_attempts_one_no_retries(db: FakeDB) -> None:
    """`max_attempts=1` means the first failure is terminal."""
    row = db.add(state="running", attempts=1, max_attempts=1)
    err = StageError(kind="any", message="x", retryable=True)
    outcome = await mark_failed_or_retry(db, job_id=row.id, error=err)
    assert outcome.state == "failed"


@pytest.mark.asyncio
async def test_failure_writes_error_json(db: FakeDB) -> None:
    row = db.add(state="running", attempts=2, max_attempts=3)
    err = StageError(
        kind="net",
        message="down",
        traceback="line 42",
        retryable=True,
    )
    await mark_failed_or_retry(db, job_id=row.id, error=err)
    parsed = json.loads(db.rows[row.id].error or "{}")
    assert parsed == {
        "kind": "net",
        "message": "down",
        "traceback": "line 42",
        "retryable": True,
    }


@pytest.mark.asyncio
async def test_failure_after_force_pause_is_noop(db: FakeDB) -> None:
    """Row already moved to paused → outcome 'noop', no clobber."""
    row = db.add(state="paused", attempts=1, max_attempts=3)
    err = StageError(kind="x", message="y", retryable=True)
    outcome = await mark_failed_or_retry(db, job_id=row.id, error=err)
    assert outcome == FailureOutcome(state="noop")
    assert db.rows[row.id].state == "paused"


@pytest.mark.asyncio
async def test_failure_missing_row_is_noop(db: FakeDB) -> None:
    err = StageError(kind="x", message="y", retryable=True)
    outcome = await mark_failed_or_retry(db, job_id=99999, error=err)
    assert outcome.state == "noop"


@pytest.mark.asyncio
async def test_sqlite_failure_path(sqlite_db: FakeDB) -> None:
    row = sqlite_db.add(state="running", attempts=1, max_attempts=3)
    err = StageError(kind="net", message="x", retryable=True)
    outcome = await mark_failed_or_retry(
        sqlite_db,
        job_id=row.id,
        error=err,
        rng=random.Random(0),
    )
    assert outcome.state == "pending"


# ---- retry_failed -----------------------------------------------------


@pytest.mark.asyncio
async def test_retry_failed_resets_state(db: FakeDB) -> None:
    row = db.add(
        state="failed",
        attempts=3,
        max_attempts=3,
        error='{"kind":"x"}',
        finished_at=datetime.now(UTC),
    )
    out = await retry_failed(db, job_id=row.id)
    assert out is not None
    r = db.rows[row.id]
    assert r.state == "pending"
    assert r.attempts == 0
    assert r.not_before is None
    assert r.error is None
    assert r.finished_at is None


@pytest.mark.asyncio
async def test_retry_failed_no_op_on_running(db: FakeDB) -> None:
    """Operator can't accidentally retry a live row."""
    row = db.add(state="running")
    out = await retry_failed(db, job_id=row.id)
    assert out is None
    assert db.rows[row.id].state == "running"


@pytest.mark.asyncio
async def test_retry_failed_no_op_on_missing(db: FakeDB) -> None:
    out = await retry_failed(db, job_id=99999)
    assert out is None
