"""``topup_once``: a transaction per channel, one writer per channel (pg advisory lock)."""

from __future__ import annotations

import contextlib
from collections.abc import AsyncIterator
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.channels import pass_
from maktaba_pipeline.channels.repo import SqlRepo


class _DB:
    dialect = "postgres"

    def __init__(self, locked: set[int]) -> None:
        self.locked = locked
        self.transactions = 0

    async def fetchrow(self, sql: str, *_: object) -> None:
        assert "app_settings" in sql  # the tv rules: none stored, the defaults
        return None

    async def fetchval(self, sql: str, key: int, channel: int) -> bool:
        assert "pg_try_advisory_xact_lock" in sql and key == pass_.TOPUP_ADVISORY_LOCK_KEY
        return channel not in self.locked

    @contextlib.asynccontextmanager
    async def transaction(self) -> AsyncIterator[None]:
        self.transactions += 1
        yield


@pytest.mark.asyncio
async def test_each_due_channel_in_its_own_transaction_skipping_locked(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    a, b, c = uuid4(), uuid4(), uuid4()
    ran: list[UUID] = []

    async def due(self: Any, *_: Any) -> list[UUID]:
        return [a, b, c]

    async def run(repo: Any, channel_id: UUID, **_: Any) -> int:
        if channel_id == c:
            raise RuntimeError("bad channel")
        ran.append(channel_id)
        return 1

    monkeypatch.setattr(SqlRepo, "channels_needing_topup", due)
    monkeypatch.setattr(pass_, "run_schedule", run)
    db = _DB(locked={pass_._seed_for(b)})  # another worker is on b
    assert await pass_.topup_once(db) == 1
    assert ran == [a]
    assert db.transactions == 3
