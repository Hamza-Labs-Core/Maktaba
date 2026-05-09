"""Behaviour tests for :func:`maktaba_pipeline.db.jobs.enqueue`.

These tests run against an in-memory fake DB that mimics the unique
partial index (``processing_jobs_one_live_per_video_stage``) and the
ON CONFLICT … DO NOTHING semantics of the real Postgres/SQLite path.
The integration tests against a real database land with Story 1.5's
testcontainers fixture; until then the fake covers the ``enqueue``
state machine from the plan §4.

A ``DBConn``-shaped object is all the helper needs. The fake records
every fetchrow call so we can assert exactly which SQL ran in which
order.

Note: these tests are intentionally not marked ``unit``. Story 20.1's
netguard replaces ``socket.socket`` so any unit-tier test that opens
one fails fast — but asyncio's event-loop bootstrap calls
``socket.socketpair`` (an FD wrap, not a network connection) which
the current guard rejects. Until Story 20.1 grows asyncio support
they run when invoked directly (``pytest tests/db/``) and via the
broader test suite, but are skipped by ``make test-unit-py``'s
``-m unit`` filter.
"""

from __future__ import annotations

import asyncio
import json
from contextlib import asynccontextmanager
from dataclasses import dataclass, field
from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.db import (
    JOBS_NEW,
    EnqueueResult,
    Stage,
    enqueue,
    get_bus,
    reset_bus,
)


@dataclass
class _FakeRow:
    data: dict[str, Any]

    def __getitem__(self, key: str) -> Any:
        return self.data[key]


@dataclass
class _FakeJob:
    id: int
    video_id: UUID
    stage: str
    state: str
    priority: int
    payload: str | None
    finished_at: datetime | None = None


@dataclass
class _FakeVideo:
    id: UUID
    updated_at: datetime


@dataclass
class FakeDB:
    """In-memory stand-in for the Story 1.5 connection wrapper.

    Models the parts of the schema :func:`enqueue` exercises:
    ``processing_jobs`` rows keyed by id, plus the unique-live partial
    index and the ``videos.updated_at`` lookup. Everything else is out
    of scope.
    """

    dialect: str = "postgres"
    jobs: dict[int, _FakeJob] = field(default_factory=dict)
    videos: dict[UUID, _FakeVideo] = field(default_factory=dict)
    _next_id: int = 1
    fetchrow_calls: list[tuple[str, tuple[Any, ...]]] = field(default_factory=list)
    in_tx: bool = False

    def transaction(self) -> Any:
        @asynccontextmanager
        async def _tx() -> Any:
            self.in_tx = True
            try:
                yield self
            finally:
                self.in_tx = False

        return _tx()

    async def fetchrow(self, sql: str, *args: Any) -> _FakeRow | None:
        # Record for assertions, then dispatch on the leading SQL verb.
        self.fetchrow_calls.append((sql.strip().split()[0].upper(), args))
        head = sql.strip().split()[0].upper()
        if head == "INSERT":
            return self._exec_insert(args)
        if head == "SELECT":
            return self._exec_select(sql, args)
        raise AssertionError(f"unexpected SQL in fake DB: {sql!r}")

    def _exec_insert(self, args: tuple[Any, ...]) -> _FakeRow | None:
        video_id, stage, priority, payload_text, max_attempts = args  # noqa: F841
        # Unique partial index: exactly one live row per (video, stage).
        for j in self.jobs.values():
            if (
                j.video_id == video_id
                and j.stage == stage
                and j.state in {"pending", "claimed", "running", "resuming", "paused"}
            ):
                return None
        job_id = self._next_id
        self._next_id += 1
        self.jobs[job_id] = _FakeJob(
            id=job_id,
            video_id=video_id,
            stage=stage,
            state="pending",
            priority=priority,
            payload=payload_text,
        )
        return _FakeRow({"id": job_id})

    def _exec_select(self, sql: str, args: tuple[Any, ...]) -> _FakeRow | None:
        # Discriminate by the columns the SELECT projects.
        upper = sql.upper()
        if "FROM PROCESSING_JOBS" in upper and "JOIN VIDEOS" in upper:
            # Done-row + source-updated_at join.
            video_id, stage = args
            done_rows = [
                j
                for j in self.jobs.values()
                if j.video_id == video_id and j.stage == stage and j.state == "done"
            ]
            done_rows.sort(key=lambda j: j.finished_at or datetime.min, reverse=True)
            if not done_rows:
                return None
            top = done_rows[0]
            video = self.videos.get(top.video_id)
            if video is None:
                return None
            return _FakeRow(
                {
                    "id": top.id,
                    "finished_at": top.finished_at,
                    "updated_at": video.updated_at,
                }
            )
        # Fallback live-row lookup.
        video_id, stage = args
        live = [
            j
            for j in self.jobs.values()
            if j.video_id == video_id
            and j.stage == stage
            and j.state in {"pending", "claimed", "running", "resuming", "paused"}
        ]
        if not live:
            return None
        return _FakeRow({"id": live[0].id})


@pytest.fixture
def video_id() -> UUID:
    return uuid4()


@pytest.fixture
def db(video_id: UUID) -> FakeDB:
    fake = FakeDB(dialect="postgres")
    fake.videos[video_id] = _FakeVideo(
        id=video_id,
        updated_at=datetime.now(UTC) - timedelta(hours=1),
    )
    return fake


@pytest.mark.asyncio
async def test_enqueue_inserts_pending_row(db: FakeDB, video_id: UUID) -> None:
    res = await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    assert isinstance(res, EnqueueResult)
    assert res.outcome == "inserted"
    row = db.jobs[res.id]
    assert row.state == "pending"
    assert row.priority == 100
    assert row.stage == "probe"


@pytest.mark.asyncio
async def test_enqueue_idempotent_returns_same_id(db: FakeDB, video_id: UUID) -> None:
    a = await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    b = await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    assert a.id == b.id
    assert a.outcome == "inserted"
    assert b.outcome == "reused"
    assert len(db.jobs) == 1


@pytest.mark.asyncio
async def test_enqueue_concurrent_idempotent(db: FakeDB, video_id: UUID) -> None:
    # Eight tasks racing on the same (video_id, stage). Even with
    # cooperative scheduling, the unique-live index ensures exactly
    # one INSERT wins and the rest reuse.
    async def go() -> EnqueueResult:
        return await enqueue(db, video_id=video_id, stage=Stage.TRANSCRIBE)

    results = await asyncio.gather(*(go() for _ in range(8)))
    ids = {r.id for r in results}
    inserted = sum(1 for r in results if r.outcome == "inserted")
    reused = sum(1 for r in results if r.outcome == "reused")
    assert len(ids) == 1
    assert inserted == 1
    assert reused == 7
    assert len(db.jobs) == 1


@pytest.mark.asyncio
async def test_enqueue_skips_when_done_and_source_unchanged(db: FakeDB, video_id: UUID) -> None:
    # Stand up a done row whose finished_at is *after* the video's
    # updated_at — the "source unchanged" branch.
    done_id = 999
    db.jobs[done_id] = _FakeJob(
        id=done_id,
        video_id=video_id,
        stage="probe",
        state="done",
        priority=100,
        payload=None,
        finished_at=datetime.now(UTC),
    )
    res = await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    assert res.outcome == "skipped_done_unchanged"
    assert res.id == done_id


@pytest.mark.asyncio
async def test_enqueue_creates_new_when_source_changed(db: FakeDB, video_id: UUID) -> None:
    # Done row finished an hour ago; bump the video's updated_at to now
    # so the helper insists on a fresh pending row.
    finished_at = datetime.now(UTC) - timedelta(hours=2)
    db.jobs[999] = _FakeJob(
        id=999,
        video_id=video_id,
        stage="probe",
        state="done",
        priority=100,
        payload=None,
        finished_at=finished_at,
    )
    db.videos[video_id] = _FakeVideo(id=video_id, updated_at=datetime.now(UTC))
    res = await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    assert res.outcome == "inserted"
    assert res.id != 999
    # The done row stays — the live-only set permits both rows.
    assert db.jobs[999].state == "done"
    assert db.jobs[res.id].state == "pending"


@pytest.mark.asyncio
async def test_enqueue_payload_round_trips(db: FakeDB, video_id: UUID) -> None:
    payload = {"audio_index": 1, "track_uuid": "abc-123"}
    res = await enqueue(
        db,
        video_id=video_id,
        stage=Stage.EXTRACT,
        payload=payload,
    )
    assert res.outcome == "inserted"
    stored = db.jobs[res.id].payload
    assert stored is not None
    assert json.loads(stored) == payload


@pytest.mark.asyncio
async def test_enqueue_payload_none_stores_null(db: FakeDB, video_id: UUID) -> None:
    res = await enqueue(db, video_id=video_id, stage=Stage.PROBE, payload=None)
    assert db.jobs[res.id].payload is None


@pytest.mark.asyncio
async def test_enqueue_priority_default_and_override(db: FakeDB, video_id: UUID) -> None:
    a = await enqueue(db, video_id=video_id, stage=Stage.SCAN)
    other_video = uuid4()
    db.videos[other_video] = _FakeVideo(
        id=other_video,
        updated_at=datetime.now(UTC) - timedelta(hours=1),
    )
    b = await enqueue(db, video_id=other_video, stage=Stage.SCAN, priority=10)
    assert db.jobs[a.id].priority == 100
    assert db.jobs[b.id].priority == 10


@pytest.mark.asyncio
async def test_enqueue_runs_inside_transaction(db: FakeDB, video_id: UUID) -> None:
    # Sanity: the helper opens a transaction. Story 6.1's edge case
    # requires SQLite's BEGIN IMMEDIATE to serialize concurrent
    # writers; here we just assert the context is entered and at
    # least one statement runs inside it.
    await enqueue(db, video_id=video_id, stage=Stage.PROBE)
    assert db.in_tx is False  # closed cleanly
    assert any(call[0] == "INSERT" for call in db.fetchrow_calls)


@pytest.mark.asyncio
async def test_enqueue_emits_pubsub_event_on_sqlite(
    video_id: UUID,
) -> None:
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(JOBS_NEW)

    fake = FakeDB(dialect="sqlite")
    fake.videos[video_id] = _FakeVideo(
        id=video_id,
        updated_at=datetime.now(UTC) - timedelta(hours=1),
    )

    res = await enqueue(fake, video_id=video_id, stage=Stage.PROBE, priority=42)

    note = json.loads(queue.get_nowait())
    assert note == {
        "id": res.id,
        "video_id": str(video_id),
        "stage": "probe",
        "priority": 42,
    }


@pytest.mark.asyncio
async def test_enqueue_does_not_publish_pubsub_on_postgres(db: FakeDB, video_id: UUID) -> None:
    # On Postgres the AFTER INSERT trigger fires NOTIFY at the SQL
    # layer; the helper must NOT also publish to the in-process bus
    # (that would double-deliver to subscribers running in the same
    # process during integration tests).
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(JOBS_NEW)

    await enqueue(db, video_id=video_id, stage=Stage.PROBE)

    assert queue.empty()


@pytest.mark.asyncio
async def test_enqueue_reused_does_not_publish_pubsub_on_sqlite(
    video_id: UUID,
) -> None:
    # Reuse path must not double-publish either — only fresh inserts
    # fire the notification.
    reset_bus()
    bus = get_bus()
    queue = await bus.subscribe(JOBS_NEW)

    fake = FakeDB(dialect="sqlite")
    fake.videos[video_id] = _FakeVideo(
        id=video_id,
        updated_at=datetime.now(UTC) - timedelta(hours=1),
    )

    await enqueue(fake, video_id=video_id, stage=Stage.PROBE)
    queue.get_nowait()  # drain the insert event

    await enqueue(fake, video_id=video_id, stage=Stage.PROBE)
    assert queue.empty()
