"""PgListenWakeup's own LISTEN connection is closed when its loop ends."""

from __future__ import annotations

from collections.abc import AsyncGenerator
from typing import Any, cast

import pytest

from maktaba_pipeline.db.pubsub import JOBS_CONTROL, JOBS_NEW
from maktaba_pipeline.pipeline.wakeup import PgListenWakeup


class _Listener:
    def __init__(self) -> None:
        self.channels: list[str] = []
        self.closed = False

    async def add_listener(self, channel: str, _cb: Any) -> None:
        self.channels.append(channel)

    async def remove_listener(self, channel: str, _cb: Any) -> None:
        self.channels.remove(channel)

    async def close(self) -> None:
        self.closed = True


class _GoneListener(_Listener):
    async def remove_listener(self, channel: str, _cb: Any) -> None:
        raise ConnectionError("the home went away")


class _DB:
    dialect = "postgres"

    def __init__(self, *, fail_remove: bool = False) -> None:
        self.listener = _GoneListener() if fail_remove else _Listener()

    async def acquire_listener(self) -> _Listener:
        return self.listener


@pytest.mark.asyncio
async def test_the_listener_connection_closes_when_the_loop_ends() -> None:
    db = _DB()
    wake = PgListenWakeup(db, poll_sec=0.01)
    async for _ in wake.signals():
        assert db.listener.channels == [JOBS_NEW, JOBS_CONTROL]
        await wake.aclose()
    assert db.listener.closed
    assert db.listener.channels == []


@pytest.mark.asyncio
async def test_the_listener_connection_closes_when_unlisten_fails() -> None:
    db = _DB(fail_remove=True)
    wake = PgListenWakeup(db, poll_sec=0.01)
    async for _ in wake.signals():
        await wake.aclose()
    assert db.listener.closed


@pytest.mark.asyncio
async def test_the_listener_connection_closes_when_the_consumer_breaks() -> None:
    db = _DB()
    gen = cast(AsyncGenerator[None], PgListenWakeup(db, poll_sec=0.01).signals())
    async for _ in gen:
        break
    await gen.aclose()
    assert db.listener.closed
