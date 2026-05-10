"""Dead-letter UPSERT and drain pathways against a fake DB.

The fake DB records every (sql, args) pair so we can assert the
INSERT … ON CONFLICT path runs on enqueue, and that drain calls
DELETE on success and the BUMP UPDATE on failure.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.search.live.deadletter import (
    drain_dead_letter,
    enqueue_dead_letter,
)

LIBRARY_ID = UUID("33333333-3333-3333-3333-333333333333")


class _FakeRow(dict[str, object]):
    pass


class _FakeDB:
    dialect = "postgres"

    def __init__(
        self,
        *,
        due_rows: list[_FakeRow] | None = None,
        given_up_count: int = 0,
    ) -> None:
        self.executes: list[tuple[str, tuple[Any, ...]]] = []
        self.fetches: list[tuple[str, tuple[Any, ...]]] = []
        self.due_rows = due_rows or []
        self.given_up_count = given_up_count

    @asynccontextmanager
    async def transaction(self):  # type: ignore[no-untyped-def]
        yield self

    async def execute(self, sql: str, *args: Any) -> Any:
        self.executes.append((sql, args))
        return None

    async def fetch(self, sql: str, *args: Any) -> list[Any]:
        self.fetches.append((sql, args))
        # The drain runs the SELECT due once.
        return list(self.due_rows)

    async def fetchrow(self, sql: str, *args: Any) -> Any:
        self.fetches.append((sql, args))
        if "COUNT(*)" in sql:
            return _FakeRow(n=self.given_up_count)
        return None


@pytest.mark.asyncio
async def test_enqueue_dead_letter_runs_upsert() -> None:
    db = _FakeDB()

    await enqueue_dead_letter(
        db,
        unit_id=11,
        library_id=LIBRARY_ID,
        transcript_id=7,
        error="boom",
    )

    assert len(db.executes) == 1
    sql, args = db.executes[0]
    assert "INSERT INTO vector_index_dead_letter" in sql
    assert "ON CONFLICT (unit_id) DO UPDATE" in sql
    assert args == (11, LIBRARY_ID, 7, "boom")


class _GoodIndexer:
    def __init__(self) -> None:
        self.calls: list[list[int]] = []

    async def index_unit_batch(self, unit_ids: list[int]) -> None:
        self.calls.append(unit_ids)


class _FlakyIndexer:
    """Always raises — every drain attempt fails."""

    async def index_unit_batch(self, unit_ids: list[int]) -> None:
        _ = unit_ids
        raise RuntimeError("vector backend down")


@pytest.mark.asyncio
async def test_drain_dead_letter_deletes_on_success() -> None:
    db = _FakeDB(
        due_rows=[
            _FakeRow(unit_id=1, library_id=LIBRARY_ID, transcript_id=7, attempts=1),
            _FakeRow(unit_id=2, library_id=LIBRARY_ID, transcript_id=7, attempts=2),
        ],
        given_up_count=0,
    )

    out = await drain_dead_letter(db, indexer=_GoodIndexer(), max_attempts=5)

    assert out["reindexed"] == 2
    assert out["still_failing"] == 0
    assert out["given_up"] == 0

    # We should have run a DELETE per successful unit.
    delete_calls = [e for e in db.executes if "DELETE FROM" in e[0]]
    assert len(delete_calls) == 2


@pytest.mark.asyncio
async def test_drain_dead_letter_bumps_on_failure() -> None:
    db = _FakeDB(
        due_rows=[
            _FakeRow(unit_id=10, library_id=LIBRARY_ID, transcript_id=7, attempts=1),
        ],
        given_up_count=3,
    )

    out = await drain_dead_letter(db, indexer=_FlakyIndexer(), max_attempts=5)

    assert out["reindexed"] == 0
    assert out["still_failing"] == 1
    assert out["given_up"] == 3

    bump_calls = [e for e in db.executes if "UPDATE vector_index_dead_letter" in e[0]]
    assert len(bump_calls) == 1
    bump_sql, bump_args = bump_calls[0]
    assert "attempts = attempts + 1" in bump_sql
    assert bump_args[0] == 10
    assert "vector backend down" in bump_args[1]
