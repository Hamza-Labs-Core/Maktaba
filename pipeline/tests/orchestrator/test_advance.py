"""Unit tests for ``advance_after_stage`` against a fake DB connection.

The shape under test is the lock → re-read → branch → update protocol.
SQL-side tests (CHECK rejection, NOTIFY trigger payload) live in
``pipeline/tests/db/test_states_migration.py``. Live-database
integration tests will land when Story 22.4's testcontainers fixture
is wired into this tier.
"""

from __future__ import annotations

import json
from contextlib import asynccontextmanager
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.domain.states import (
    IllegalStateTransition,
    Outcome,
    State,
    Trigger,
)
from maktaba_pipeline.orchestrator.advance import (
    VIDEOS_STATE_CHANGED,
    advance_after_stage,
    serialize_notify_payload,
)

VIDEO_ID = UUID("11111111-1111-1111-1111-111111111111")
LIBRARY_ID = UUID("22222222-2222-2222-2222-222222222222")


class _FakeRow(dict[str, object]):
    pass


class _FakeLogger:
    def __init__(self) -> None:
        self.events: list[tuple[str, dict[str, Any]]] = []

    def info(self, event: str, **kwargs: Any) -> None:
        self.events.append((event, kwargs))


class _FakeDB:
    """Minimal DBConn double for advance_after_stage.

    Records every SQL invocation so tests can assert the lock-then-
    update sequence. ``current_state`` controls what the lock SELECT
    returns; ``raise_on_execute`` lets a test simulate a write
    failure.
    """

    def __init__(self, current_state: State, dialect: str = "postgres") -> None:
        self.current_state = current_state
        self.dialect = dialect
        self.executes: list[tuple[str, tuple[Any, ...]]] = []
        self.fetches: list[tuple[str, tuple[Any, ...]]] = []
        self.tx_entered = 0
        self.tx_exited = 0

    @asynccontextmanager
    async def transaction(self):  # type: ignore[no-untyped-def]
        self.tx_entered += 1
        try:
            yield self
        finally:
            self.tx_exited += 1

    async def fetchrow(self, sql: str, *args: Any) -> _FakeRow | None:
        self.fetches.append((sql, args))
        if "SELECT state" in sql:
            return _FakeRow(state=self.current_state.value, library_id=LIBRARY_ID)
        return None

    async def execute(self, sql: str, *args: Any) -> Any:
        self.executes.append((sql, args))
        if "UPDATE videos SET state" in sql:
            self.current_state = State(args[0])
        return None


# -----------------------------------------------------------------
# Happy-path canonical edges
# -----------------------------------------------------------------


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "frm,trig,out,to",
    [
        (State.DISCOVERED, Trigger.PROBE, Outcome.OK, State.PROBED),
        (State.PROBED, Trigger.EXTRACT, Outcome.OK, State.AUDIO_EXTRACTED),
        (State.PROBED, Trigger.PROBE, Outcome.NO_AUDIO, State.READY_NO_AUDIO),
        (State.AUDIO_EXTRACTED, Trigger.TRANSCRIBE, Outcome.OK, State.TRANSCRIBED),
        (State.TRANSCRIBED, Trigger.SUBTITLE_GEN, Outcome.OK, State.INDEXED),
        (State.TRANSCRIBED, Trigger.INDEX, Outcome.OK, State.INDEXED),
        (State.INDEXED, Trigger.THUMBNAIL, Outcome.OK, State.THUMBNAILED),
        (State.THUMBNAILED, Trigger.SCAN, Outcome.ALL_GATES_OK, State.READY),
        (State.MISSING, Trigger.SCAN, Outcome.REDISCOVERED, State.DISCOVERED),
    ],
)
async def test_advance_canonical_edge(
    frm: State, trig: Trigger, out: Outcome, to: State
) -> None:
    db = _FakeDB(current_state=frm)
    log = _FakeLogger()

    got = await advance_after_stage(db, VIDEO_ID, trig, out, log=log)

    assert got is to
    assert db.current_state is to
    assert db.tx_entered == 1
    assert db.tx_exited == 1
    # Lock SELECT then UPDATE — exactly two statements.
    assert len(db.fetches) == 1
    assert "SELECT state" in db.fetches[0][0]
    assert len(db.executes) == 1
    assert "UPDATE videos SET state" in db.executes[0][0]
    # No late_stage_finish log.
    assert all(ev[0] != "late_stage_finish" for ev in log.events)


# -----------------------------------------------------------------
# Story acceptance: TRANSCRIBED stays put on partial; advances on ok
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_subtitle_gen_does_not_advance_to_indexed_alone() -> None:
    db = _FakeDB(current_state=State.TRANSCRIBED)
    log = _FakeLogger()

    # subtitle_gen finishes alone — gate helper would report partial.
    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.SUBTITLE_GEN, Outcome.PARTIAL, log=log
    )
    assert got is State.TRANSCRIBED
    # Self-loop still issues an UPDATE so updated_at advances and the
    # NOTIFY trigger fires for observability.
    assert any("UPDATE videos SET state" in sql for sql, _ in db.executes)

    # index then finishes — gate helper reports ok, state advances.
    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.INDEX, Outcome.OK, log=log
    )
    assert got is State.INDEXED


# -----------------------------------------------------------------
# Story acceptance: CORRUPTED blocks further processing
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_corrupted_blocks_further_processing() -> None:
    db = _FakeDB(current_state=State.CORRUPTED)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.THUMBNAIL, Outcome.OK, log=log
    )
    # Late-stage drop: state unchanged, no UPDATE issued.
    assert got is State.CORRUPTED
    assert db.executes == []
    # late_stage_finish was logged.
    assert any(ev[0] == "late_stage_finish" for ev in log.events)


@pytest.mark.asyncio
@pytest.mark.parametrize("src", [State.SUPERSEDED, State.CORRUPTED, State.FAILED])
async def test_late_stage_finish_logs_and_returns_current(src: State) -> None:
    db = _FakeDB(current_state=src)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.THUMBNAIL, Outcome.OK, log=log
    )
    assert got is src
    assert db.executes == []
    event_names = [ev[0] for ev in log.events]
    assert "late_stage_finish" in event_names


# -----------------------------------------------------------------
# Invalid triples raise without mutating state
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_advance_rejects_invalid_outcome() -> None:
    db = _FakeDB(current_state=State.DISCOVERED)
    log = _FakeLogger()

    with pytest.raises(IllegalStateTransition) as exc_info:
        await advance_after_stage(
            db, VIDEO_ID, Trigger.PROBE, "weird", log=log
        )
    assert exc_info.value.from_ is State.DISCOVERED
    assert exc_info.value.trigger is Trigger.PROBE
    assert exc_info.value.outcome == "weird"
    # State was not changed.
    assert db.current_state is State.DISCOVERED
    assert db.executes == []


@pytest.mark.asyncio
async def test_advance_rejects_invalid_trigger_for_state() -> None:
    db = _FakeDB(current_state=State.DISCOVERED)
    log = _FakeLogger()

    with pytest.raises(IllegalStateTransition):
        await advance_after_stage(
            db, VIDEO_ID, Trigger.TRANSCRIBE, Outcome.OK, log=log
        )
    assert db.current_state is State.DISCOVERED
    assert db.executes == []


@pytest.mark.asyncio
async def test_advance_rejects_extract_on_no_audio() -> None:
    db = _FakeDB(current_state=State.READY_NO_AUDIO)
    log = _FakeLogger()

    with pytest.raises(IllegalStateTransition):
        await advance_after_stage(
            db, VIDEO_ID, Trigger.EXTRACT, Outcome.OK, log=log
        )
    assert db.current_state is State.READY_NO_AUDIO


# -----------------------------------------------------------------
# MISSING → DISCOVERED round-trip via scan/rediscovered
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_missing_to_discovered_round_trip() -> None:
    db = _FakeDB(current_state=State.MISSING)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.SCAN, Outcome.REDISCOVERED, log=log
    )
    assert got is State.DISCOVERED
    assert db.current_state is State.DISCOVERED


# -----------------------------------------------------------------
# Broadcast edges via the orchestrator
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_filesystem_deleted_drives_to_missing() -> None:
    db = _FakeDB(current_state=State.PROBED)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.FILESYSTEM, Outcome.DELETED, log=log
    )
    assert got is State.MISSING


@pytest.mark.asyncio
async def test_library_replaced_drives_to_superseded() -> None:
    db = _FakeDB(current_state=State.READY)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.LIBRARY, Outcome.REPLACED, log=log
    )
    assert got is State.SUPERSEDED


@pytest.mark.asyncio
async def test_integrity_fail_drives_to_corrupted() -> None:
    db = _FakeDB(current_state=State.INDEXED)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.INTEGRITY, Outcome.FAIL, log=log
    )
    assert got is State.CORRUPTED


@pytest.mark.asyncio
async def test_exhausted_drives_to_failed() -> None:
    db = _FakeDB(current_state=State.AUDIO_EXTRACTED)
    log = _FakeLogger()

    got = await advance_after_stage(
        db, VIDEO_ID, Trigger.TRANSCRIBE, Outcome.EXHAUSTED, log=log
    )
    assert got is State.FAILED


# -----------------------------------------------------------------
# SQLite path: in-process pubsub publication on commit
# -----------------------------------------------------------------


@pytest.mark.asyncio
async def test_sqlite_publishes_state_changed_on_bus() -> None:
    # The SQLite path publishes manually on the in-process bus because
    # SQLite has no LISTEN/NOTIFY.
    from maktaba_pipeline.db import pubsub as pubsub_mod

    pubsub_mod.reset_bus()
    bus = pubsub_mod.get_bus()
    queue = await bus.subscribe(VIDEOS_STATE_CHANGED)

    db = _FakeDB(current_state=State.DISCOVERED, dialect="sqlite")
    log = _FakeLogger()

    await advance_after_stage(
        db, VIDEO_ID, Trigger.PROBE, Outcome.OK, log=log
    )

    # One message should be queued; payload contains the four canonical
    # keys and matches the Postgres NOTIFY shape.
    msg = await queue.get()
    payload = json.loads(msg)
    assert payload["video_id"] == str(VIDEO_ID)
    assert payload["library_id"] == str(LIBRARY_ID)
    assert payload["old_state"] == "discovered"
    assert payload["new_state"] == "probed"


@pytest.mark.asyncio
async def test_postgres_path_does_not_publish_on_bus() -> None:
    # On Postgres the AFTER UPDATE trigger fires NOTIFY at the SQL
    # layer; the helper must NOT also publish on the in-process bus
    # (would double-deliver to subscribers in tests that wire both).
    from maktaba_pipeline.db import pubsub as pubsub_mod

    pubsub_mod.reset_bus()
    bus = pubsub_mod.get_bus()
    queue = await bus.subscribe(VIDEOS_STATE_CHANGED)

    db = _FakeDB(current_state=State.DISCOVERED, dialect="postgres")
    log = _FakeLogger()

    await advance_after_stage(
        db, VIDEO_ID, Trigger.PROBE, Outcome.OK, log=log
    )

    assert queue.empty()


# -----------------------------------------------------------------
# Helper: serialize_notify_payload matches the Postgres trigger shape
# -----------------------------------------------------------------


@pytest.mark.unit
def test_serialize_notify_payload_shape() -> None:
    out = json.loads(
        serialize_notify_payload(
            VIDEO_ID, LIBRARY_ID, State.DISCOVERED, State.PROBED
        )
    )
    assert out == {
        "video_id": str(VIDEO_ID),
        "library_id": str(LIBRARY_ID),
        "old_state": "discovered",
        "new_state": "probed",
    }


@pytest.mark.unit
def test_serialize_notify_payload_handles_null_library_id() -> None:
    out = json.loads(
        serialize_notify_payload(VIDEO_ID, None, State.PROBED, State.MISSING)
    )
    assert out["library_id"] is None
