"""Story 5.1 — :mod:`maktaba_pipeline.search.units` tests.

Verifies the in-memory shape of :class:`TranscriptUnit` and that
``upsert_units`` shapes the parameter tuples correctly. Uses a fake DB
to avoid pulling in asyncpg.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.search.units import TranscriptUnit, upsert_units


@dataclass
class _FakeDB:
    dialect: str = "postgres"
    last_sql: str = ""
    last_args: list[tuple[Any, ...]] = field(default_factory=list)

    async def executemany(self, sql: str, args: list[tuple[Any, ...]]) -> None:
        self.last_sql = sql
        self.last_args = list(args)


@pytest.mark.asyncio
async def test_upsert_units_shapes_rows() -> None:
    transcript_id = uuid4()
    video_id = uuid4()
    units = [
        TranscriptUnit(
            transcript_id=transcript_id,
            video_id=video_id,
            unit_index=0,
            start_sec=0.0,
            end_sec=2.5,
            text="hello world",
            segment_id=42,
            language="en",
            embedding_id="vec-1",
        ),
        TranscriptUnit(
            transcript_id=transcript_id,
            video_id=video_id,
            unit_index=1,
            start_sec=2.5,
            end_sec=5.0,
            text="follow-up sentence",
        ),
    ]
    db = _FakeDB()

    written = await upsert_units(db, units)

    assert written == 2
    assert "INSERT INTO transcript_units" in db.last_sql
    assert "ON CONFLICT (transcript_id, unit_index) DO UPDATE" in db.last_sql
    # Each tuple has 9 positional params matching the INSERT shape.
    assert all(len(row) == 9 for row in db.last_args)
    first = db.last_args[0]
    assert first[0] == transcript_id
    assert first[1] == video_id
    assert first[2] == 42  # segment_id
    assert first[3] == 0  # unit_index
    assert first[6] == "hello world"
    assert first[8] == "vec-1"
    # Second unit has the no-segment / no-language defaults.
    second = db.last_args[1]
    assert second[2] is None
    assert second[7] is None
    assert second[8] is None


@pytest.mark.asyncio
async def test_upsert_units_empty_iterable_is_noop() -> None:
    db = _FakeDB()
    written = await upsert_units(db, [])
    assert written == 0
    assert db.last_sql == ""
    assert db.last_args == []
