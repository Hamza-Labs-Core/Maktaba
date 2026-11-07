"""``store.write`` (Epic 52, story 52.6): a transcript's flags go in one batch, and a flag a
person resolved is left alone. On a recording fake database: no SQL runs."""

from __future__ import annotations

from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.word_review import store
from maktaba_pipeline.word_review.signals import Flag

pytestmark = pytest.mark.integration  # an event loop opens a socket pair


class _Db:
    dialect = "sqlite"

    def __init__(self, existing: list[dict[str, Any]]) -> None:
        self.existing = existing
        self.statements: list[str] = []
        self.batches: list[list[tuple[Any, ...]]] = []
        self.in_tx = False

    @asynccontextmanager
    async def transaction(self) -> AsyncIterator[None]:
        self.in_tx = True
        try:
            yield
        finally:
            self.in_tx = False

    async def fetchval(self, sql: str, *args: Any) -> Any:
        return 1

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        return self.existing

    async def execute(self, sql: str, *args: Any) -> None:
        self.statements.append(sql)

    async def executemany(self, sql: str, rows: list[tuple[Any, ...]]) -> None:
        assert self.in_tx, "the batch runs inside the flags transaction"
        self.statements.append(sql)
        self.batches.append(list(rows))


def _flag(seq: int, cs: int = 0) -> Flag:
    return Flag(kind="word", segment_seq=seq, word_index=0, word_count=1, char_start=cs,
                char_len=3, start_sec=0.0, end_sec=0.4, original="كتب", context="كتب",
                language="ar", reasons=["low_prob"], scores={}, priority=0.5,
                candidates=[], key="كتب", pack="lang.ar")  # fmt: skip


@pytest.mark.asyncio
async def test_the_flags_are_one_batch_and_a_resolved_one_is_kept() -> None:
    """2026-11-07: a statement per flag held the transaction for minutes on a busy home."""
    resolved = {"id": "r", "segment_seq": 1, "char_start": 0, "status": "accepted",
                "resolved_by": "someone", "reasons": "[]", "key": "كتب", "language": "ar",
                "scores": "{}"}  # fmt: skip
    db = _Db([resolved])
    transcript = {"id": str(uuid4()), "video_id": str(uuid4()), "library_id": str(uuid4()),
                  "audio_track_id": None}  # fmt: skip
    flags = [_flag(seq) for seq in range(200)]

    done = await store.write(db, transcript, flags, check_version=3)

    assert done == {"written": 199, "removed": 0, "accepted": 0, "kept": 1}
    assert len(db.batches) == 1 and len(db.batches[0]) == 199
    assert sum("INSERT INTO word_flags" in q for q in db.statements) == 1
    assert {row[6] for row in db.batches[0]} == set(range(200)) - {1}
    assert all(row[-1] == 3 and len(row) == 24 for row in db.batches[0])


@pytest.mark.asyncio
async def test_no_flags_runs_no_insert() -> None:
    db = _Db([])
    transcript = {"id": str(uuid4()), "video_id": str(uuid4()), "library_id": str(uuid4()),
                  "audio_track_id": None}  # fmt: skip
    assert (await store.write(db, transcript, [], check_version=1))["written"] == 0
    assert db.batches == []
