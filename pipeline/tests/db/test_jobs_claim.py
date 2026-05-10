"""Behaviour tests for :func:`maktaba_pipeline.db.jobs_claim.claim_one`.

The fake DB models the eligibility predicate, ordering, and SKIP
LOCKED contention semantics of the real Postgres/SQLite path. The
integration tests against a real database land with Story 1.5's
testcontainers fixture; until then the fake covers the claim state
machine from architecture §7.3 and plan-06-02 §3.

A ``FakeDB`` exposes ``fetchrow``, ``transaction``, and ``dialect``;
that's all :func:`claim_one` reaches for. The fake's claim
implementation runs under an internal :class:`asyncio.Lock` so
concurrent claim coroutines see the same atomicity the real DB
provides — exactly the property
``test_claim_atomic_under_contention`` is meant to pin.

Note: these tests are intentionally not marked ``unit``. Story 20.1's
netguard rejects asyncio's ``socket.socketpair`` bootstrap. They run
when invoked directly (``pytest tests/db/``) and via the broader test
suite, but are skipped by ``make test-unit-py``'s ``-m unit`` filter.
"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
from dataclasses import dataclass, field, replace
from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.db.jobs import JobState, Stage
from maktaba_pipeline.db.jobs_claim import (
    _CLAIM_SQL_PG,
    _SQLITE_CLAIM_SELECT,
    _SQLITE_CLAIM_UPDATE,
    _reset_sqlite_claim_lock,
    claim_one,
    claim_one_pg,
    claim_one_sqlite,
)


@dataclass
class FakeJobRow:
    """One ``processing_jobs`` row, populated with sensible defaults."""

    id: int
    video_id: UUID
    stage: str
    state: str = "pending"
    priority: int = 100
    attempts: int = 0
    max_attempts: int = 3
    claimed_by: str | None = None
    claimed_at: datetime | None = None
    last_heartbeat_at: datetime | None = None
    not_before: datetime | None = None
    error: str | None = None
    total_duration_seconds: float | None = None
    processed_seconds: float = 0.0
    segments_completed: int = 0
    last_segment_end_sec: float = 0.0
    estimated_remaining_sec: float | None = None
    realtime_factor: float | None = None
    progress_updated_at: datetime | None = None
    pause_requested: bool = False
    cancel_requested: bool = False
    paused_at: datetime | None = None
    paused_at_sec: float | None = None
    paused_reason: str | None = None
    resumed_at: datetime | None = None
    resume_count: int = 0
    metrics: dict[str, Any] | None = None
    payload: dict[str, Any] | None = None
    created_at: datetime = field(default_factory=lambda: datetime.now(UTC))
    finished_at: datetime | None = None

    def as_row(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "video_id": self.video_id,
            "stage": self.stage,
            "state": self.state,
            "priority": self.priority,
            "attempts": self.attempts,
            "max_attempts": self.max_attempts,
            "claimed_by": self.claimed_by,
            "claimed_at": self.claimed_at,
            "last_heartbeat_at": self.last_heartbeat_at,
            "not_before": self.not_before,
            "error": self.error,
            "total_duration_seconds": self.total_duration_seconds,
            "processed_seconds": self.processed_seconds,
            "segments_completed": self.segments_completed,
            "last_segment_end_sec": self.last_segment_end_sec,
            "estimated_remaining_sec": self.estimated_remaining_sec,
            "realtime_factor": self.realtime_factor,
            "progress_updated_at": self.progress_updated_at,
            "pause_requested": self.pause_requested,
            "cancel_requested": self.cancel_requested,
            "paused_at": self.paused_at,
            "paused_at_sec": self.paused_at_sec,
            "paused_reason": self.paused_reason,
            "resumed_at": self.resumed_at,
            "resume_count": self.resume_count,
            "metrics": self.metrics,
            "payload": self.payload,
            "created_at": self.created_at,
            "finished_at": self.finished_at,
        }


class _FakeRow(dict[str, Any]):
    """Mapping-shaped row, mimicking asyncpg/aiosqlite rows."""


@dataclass
class FakeDB:
    """Stand-in for the Story 1.5 connection wrapper.

    Models the parts of the schema :func:`claim_one` exercises:
    ``processing_jobs`` rows keyed by id and the eligibility/ordering
    rules from architecture §7.3. Concurrent ``fetchrow`` calls are
    serialised via an internal :class:`asyncio.Lock`, mirroring the
    SKIP LOCKED contention property the real DB provides.

    A ``yield_in_fetchrow`` knob inserts an ``await asyncio.sleep(0)``
    inside ``fetchrow`` so the contention test interleaves coroutines
    instead of running them in lock-step.
    """

    dialect: str = "postgres"
    rows: dict[int, FakeJobRow] = field(default_factory=dict)
    _next_id: int = 1
    _claim_lock: asyncio.Lock | None = None
    yield_in_fetchrow: bool = False
    _now_override: datetime | None = None

    def add(self, **kwargs: Any) -> FakeJobRow:
        row = FakeJobRow(
            id=self._next_id,
            video_id=kwargs.pop("video_id", uuid4()),
            stage=kwargs.pop("stage", Stage.PROBE.value),
            **kwargs,
        )
        self.rows[row.id] = row
        self._next_id += 1
        return row

    def now(self) -> datetime:
        return self._now_override or datetime.now(UTC)

    def set_now(self, when: datetime | None) -> None:
        self._now_override = when

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            yield self

        return _tx()

    def _lock(self) -> asyncio.Lock:
        if self._claim_lock is None:
            self._claim_lock = asyncio.Lock()
        return self._claim_lock

    async def fetchrow(self, sql: str, *args: Any) -> _FakeRow | None:
        if self.yield_in_fetchrow:
            await asyncio.sleep(0)

        s = sql.strip()
        if s == _CLAIM_SQL_PG.strip():
            worker_id, stages = args
            return await self._claim_pg(worker_id, list(stages))
        if s.startswith("SELECT id FROM processing_jobs"):
            stages = list(args)
            return await self._sqlite_select(stages)
        if s == _SQLITE_CLAIM_UPDATE.strip():
            worker_id, row_id = args
            return await self._sqlite_update(worker_id, int(row_id))
        raise AssertionError(f"unexpected SQL in fake DB: {sql!r}")

    async def _claim_pg(
        self, worker_id: str, stages: list[str]
    ) -> _FakeRow | None:
        async with self._lock():
            return self._do_claim(worker_id, stages)

    async def _sqlite_select(self, stages: list[str]) -> _FakeRow | None:
        eligible = self._eligible(stages)
        if not eligible:
            return None
        return _FakeRow({"id": eligible[0].id})

    async def _sqlite_update(
        self, worker_id: str, row_id: int
    ) -> _FakeRow | None:
        row = self.rows.get(row_id)
        if row is None or row.state not in {"pending", "paused"}:
            return None
        self._mark_claimed(row, worker_id)
        return _FakeRow(row.as_row())

    def _do_claim(
        self, worker_id: str, stages: list[str]
    ) -> _FakeRow | None:
        eligible = self._eligible(stages)
        if not eligible:
            return None
        row = eligible[0]
        self._mark_claimed(row, worker_id)
        return _FakeRow(row.as_row())

    def _eligible(self, stages: list[str]) -> list[FakeJobRow]:
        now = self.now()
        eligible = [
            r
            for r in self.rows.values()
            if r.state in {"pending", "paused"}
            and not r.pause_requested
            and not r.cancel_requested
            and (r.not_before is None or r.not_before <= now)
            and r.stage in stages
        ]
        eligible.sort(key=lambda r: (r.priority, r.id))
        return eligible

    def _mark_claimed(self, row: FakeJobRow, worker_id: str) -> None:
        now = self.now()
        row.state = "claimed"
        row.claimed_by = worker_id
        row.claimed_at = now
        row.last_heartbeat_at = now
        row.attempts += 1


@pytest.fixture
def video_id() -> UUID:
    return uuid4()


@pytest.fixture
def db() -> FakeDB:
    return FakeDB(dialect="postgres")


@pytest.fixture(autouse=True)
def _reset_sqlite_lock() -> None:
    # Each test gets a fresh asyncio lock so the lock isn't bound to a
    # dead event loop from a prior test.
    _reset_sqlite_claim_lock()


@pytest.mark.asyncio
async def test_claim_returns_pending_row(db: FakeDB, video_id: UUID) -> None:
    db.add(video_id=video_id, stage=Stage.PROBE.value)
    job = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert job is not None
    assert job.state == JobState.CLAIMED
    assert job.claimed_by == "w1"
    assert job.claimed_at is not None
    assert job.last_heartbeat_at is not None
    assert job.attempts == 1


@pytest.mark.asyncio
async def test_claim_returns_none_when_empty(db: FakeDB) -> None:
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is None


@pytest.mark.asyncio
async def test_claim_returns_none_when_only_terminal(
    db: FakeDB, video_id: UUID
) -> None:
    for state in ("done", "failed", "cancelled"):
        db.add(video_id=uuid4(), stage=Stage.PROBE.value, state=state)
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is None


@pytest.mark.asyncio
async def test_claim_respects_priority(db: FakeDB) -> None:
    high = db.add(video_id=uuid4(), stage=Stage.PROBE.value, priority=100)
    user_pressed = db.add(
        video_id=uuid4(), stage=Stage.PROBE.value, priority=50,
    )
    bulk = db.add(video_id=uuid4(), stage=Stage.PROBE.value, priority=200)
    first = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    second = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    third = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert first is not None and first.id == user_pressed.id
    assert second is not None and second.id == high.id
    assert third is not None and third.id == bulk.id


@pytest.mark.asyncio
async def test_claim_respects_id_tiebreak(db: FakeDB) -> None:
    earlier = db.add(video_id=uuid4(), stage=Stage.PROBE.value, priority=100)
    later = db.add(video_id=uuid4(), stage=Stage.PROBE.value, priority=100)
    first = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    second = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert first is not None and first.id == earlier.id
    assert second is not None and second.id == later.id


@pytest.mark.asyncio
async def test_claim_skips_not_before_in_future(db: FakeDB) -> None:
    future = datetime.now(UTC) + timedelta(seconds=60)
    db.add(video_id=uuid4(), stage=Stage.PROBE.value, not_before=future)
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is None


@pytest.mark.asyncio
async def test_claim_picks_up_not_before_in_past(db: FakeDB) -> None:
    past = datetime.now(UTC) - timedelta(seconds=1)
    row = db.add(video_id=uuid4(), stage=Stage.PROBE.value, not_before=past)
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is not None and res.id == row.id


@pytest.mark.asyncio
async def test_claim_picks_paused_when_pause_requested_false(
    db: FakeDB,
) -> None:
    # Resume path: a paused row whose pause flag has been cleared
    # is the worker's signal to walk into `resuming`.
    row = db.add(
        video_id=uuid4(),
        stage=Stage.PROBE.value,
        state="paused",
        pause_requested=False,
        attempts=2,
    )
    job = await claim_one(
        db, worker_id="w-resume", supported_stages=(Stage.PROBE,),
    )
    assert job is not None and job.id == row.id
    assert job.state == JobState.CLAIMED
    assert job.attempts == 3  # incremented from 2


@pytest.mark.asyncio
async def test_claim_skips_paused_when_pause_requested_true(
    db: FakeDB,
) -> None:
    db.add(
        video_id=uuid4(),
        stage=Stage.PROBE.value,
        state="paused",
        pause_requested=True,
    )
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is None


@pytest.mark.asyncio
async def test_claim_skips_pending_with_pause_requested(db: FakeDB) -> None:
    # A pause requested before the worker picks the row up must still
    # be honoured — otherwise the user clicking Pause between enqueue
    # and claim silently loses.
    db.add(
        video_id=uuid4(),
        stage=Stage.PROBE.value,
        state="pending",
        pause_requested=True,
    )
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is None


@pytest.mark.asyncio
async def test_claim_skips_cancel_requested(db: FakeDB) -> None:
    db.add(
        video_id=uuid4(),
        stage=Stage.PROBE.value,
        cancel_requested=True,
        priority=1,  # would win without the filter
    )
    other = db.add(video_id=uuid4(), stage=Stage.PROBE.value, priority=100)
    res = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert res is not None and res.id == other.id


@pytest.mark.asyncio
async def test_claim_filters_by_stage(db: FakeDB) -> None:
    transcribe_row = db.add(
        video_id=uuid4(), stage=Stage.TRANSCRIBE.value, priority=10,
    )
    db.add(video_id=uuid4(), stage=Stage.INDEX.value, priority=5)

    miss = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.EXTRACT,),
    )
    assert miss is None

    hit = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.TRANSCRIBE,),
    )
    assert hit is not None and hit.id == transcribe_row.id


@pytest.mark.asyncio
async def test_claim_increments_attempts(db: FakeDB) -> None:
    row = db.add(video_id=uuid4(), stage=Stage.PROBE.value)
    first = await claim_one(
        db, worker_id="w1", supported_stages=(Stage.PROBE,),
    )
    assert first is not None and first.attempts == 1

    # Reset to pending (simulating the reaper) and claim again.
    db.rows[row.id] = replace(db.rows[row.id], state="pending", claimed_by=None)
    second = await claim_one(
        db, worker_id="w2", supported_stages=(Stage.PROBE,),
    )
    assert second is not None
    assert second.attempts == 2
    assert second.claimed_by == "w2"


@pytest.mark.asyncio
async def test_claim_state_pause_truth_table(db: FakeDB) -> None:
    """Only (pending|paused) × pause_requested=false rows are claimable."""
    expected_claimable: dict[tuple[str, bool], bool] = {
        ("pending", False): True,
        ("pending", True): False,
        ("paused", False): True,
        ("paused", True): False,
        ("claimed", False): False,
        ("running", False): False,
        ("resuming", False): False,
        ("done", False): False,
        ("failed", False): False,
        ("cancelled", False): False,
    }
    rows: dict[tuple[str, bool], int] = {}
    for (state, pause_flag), _ in expected_claimable.items():
        row = db.add(
            video_id=uuid4(),
            stage=Stage.PROBE.value,
            state=state,
            pause_requested=pause_flag,
        )
        rows[(state, pause_flag)] = row.id

    claimable_ids = set()
    while True:
        job = await claim_one(
            db, worker_id="w1", supported_stages=(Stage.PROBE,),
        )
        if job is None:
            break
        claimable_ids.add(job.id)

    expected_ids = {rows[k] for k, v in expected_claimable.items() if v}
    assert claimable_ids == expected_ids


@pytest.mark.asyncio
async def test_claim_empty_stages_raises(db: FakeDB) -> None:
    with pytest.raises(ValueError, match="supported_stages"):
        await claim_one_pg(db, worker_id="w1", supported_stages=())
    with pytest.raises(ValueError, match="supported_stages"):
        await claim_one_sqlite(db, worker_id="w1", supported_stages=())


@pytest.mark.asyncio
async def test_claim_atomic_under_contention_postgres() -> None:
    db = FakeDB(dialect="postgres", yield_in_fetchrow=True)
    for _ in range(100):
        db.add(video_id=uuid4(), stage=Stage.PROBE.value)

    claimed: list[int] = []
    claimed_lock = asyncio.Lock()

    async def worker(name: str) -> None:
        local: list[int] = []
        while True:
            job = await claim_one(
                db, worker_id=name, supported_stages=(Stage.PROBE,),
            )
            if job is None:
                # One more tick in case another worker is mid-claim.
                await asyncio.sleep(0)
                job = await claim_one(
                    db, worker_id=name, supported_stages=(Stage.PROBE,),
                )
                if job is None:
                    break
            local.append(job.id)
        async with claimed_lock:
            claimed.extend(local)

    await asyncio.gather(*(worker(f"w{i}") for i in range(10)))

    # Each row claimed exactly once; total adds to 100.
    assert len(claimed) == 100
    assert len(set(claimed)) == 100
    # All rows ended up claimed.
    assert all(r.state == "claimed" for r in db.rows.values())
    # Every claimer left a stamp.
    workers = {r.claimed_by for r in db.rows.values()}
    assert workers <= {f"w{i}" for i in range(10)}


@pytest.mark.asyncio
async def test_claim_atomic_under_contention_sqlite() -> None:
    db = FakeDB(dialect="sqlite", yield_in_fetchrow=True)
    for _ in range(50):
        db.add(video_id=uuid4(), stage=Stage.PROBE.value)

    claimed: list[int] = []
    claimed_lock = asyncio.Lock()

    async def worker(name: str) -> None:
        local: list[int] = []
        while True:
            job = await claim_one(
                db, worker_id=name, supported_stages=(Stage.PROBE,),
            )
            if job is None:
                await asyncio.sleep(0)
                job = await claim_one(
                    db, worker_id=name, supported_stages=(Stage.PROBE,),
                )
                if job is None:
                    break
            local.append(job.id)
        async with claimed_lock:
            claimed.extend(local)

    await asyncio.gather(*(worker(f"w{i}") for i in range(5)))
    assert len(claimed) == 50
    assert len(set(claimed)) == 50


@pytest.mark.asyncio
async def test_sqlite_dialect_dispatches_to_sqlite_path(
    video_id: UUID,
) -> None:
    db = FakeDB(dialect="sqlite")
    row = db.add(video_id=video_id, stage=Stage.TRANSCRIBE.value)
    job = await claim_one(
        db, worker_id="w-sqlite", supported_stages=(Stage.TRANSCRIBE,),
    )
    assert job is not None and job.id == row.id
    assert job.state == JobState.CLAIMED


@pytest.mark.asyncio
async def test_pg_sql_matches_pinned_string(db: FakeDB, video_id: UUID) -> None:
    """Smoke check that the PG SQL is the canonical string the test fake matches.

    If this test ever fails it means the SQL was edited; the
    accompanying architecture §7.3 / plan-06-02 §3 must be updated in
    the same commit.
    """
    expected_fragments = [
        "UPDATE processing_jobs",
        "SET state             = 'claimed'",
        "claimed_by        = $1",
        "FOR UPDATE SKIP LOCKED",
        "ORDER BY priority ASC, id ASC",
        "stage = ANY($2::text[])",
        "pause_requested = false",
        "cancel_requested = false",
        "(not_before IS NULL OR not_before <= now())",
        "RETURNING *",
    ]
    for fragment in expected_fragments:
        assert fragment in _CLAIM_SQL_PG, fragment


def test_sqlite_sql_uses_qmark_placeholders() -> None:
    """SQLite path must use ``?`` and ``datetime('now')`` — not ``$N``/``now()``."""
    assert "?" in _SQLITE_CLAIM_UPDATE
    assert "datetime('now')" in _SQLITE_CLAIM_UPDATE
    assert "$1" not in _SQLITE_CLAIM_UPDATE
    assert "now()" not in _SQLITE_CLAIM_UPDATE
    assert "{placeholders}" in _SQLITE_CLAIM_SELECT
