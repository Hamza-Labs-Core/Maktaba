"""PgListenWakeup's own LISTEN connection is closed when its loop ends, and opened
again when the server ended it (a database restart)."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncGenerator
from typing import Any, cast

import pytest

from maktaba_pipeline.db.pubsub import JOBS_CONTROL, JOBS_NEW
from maktaba_pipeline.pipeline.wakeup import PgListenWakeup


class _Listener:
    def __init__(self) -> None:
        self.channels: list[str] = []
        self.closed = False
        self.notify: Any = None
        self.on_end: Any = None

    async def add_listener(self, channel: str, cb: Any) -> None:
        self.channels.append(channel)
        self.notify = cb

    def add_termination_listener(self, cb: Any) -> None:
        self.on_end = cb

    def is_closed(self) -> bool:
        return self.closed

    def server_ends_it(self) -> None:
        """The server terminated the connection (asyncpg tells the termination
        listeners; nothing is delivered on it from then on)."""
        self.closed = True
        self.on_end(self)

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


class _RestartingDB:
    """Hands out a new listener each time; refuses while ``down``."""

    dialect = "postgres"

    def __init__(self) -> None:
        self.listeners: list[_Listener] = []
        self.down = False
        self.refused = 0

    async def acquire_listener(self) -> _Listener:
        if self.down:
            self.refused += 1
            raise ConnectionRefusedError("the database is starting up")
        self.listeners.append(_Listener())
        return self.listeners[-1]


async def _ticks(gen: AsyncGenerator[None], n: int) -> None:
    for _ in range(n):
        await asyncio.wait_for(gen.__anext__(), timeout=5)


@pytest.mark.asyncio
async def test_a_listener_the_server_ended_is_opened_again() -> None:
    db = _RestartingDB()
    # the poll is far away: only a notification (or the connection's end) wakes the loop
    wake = PgListenWakeup(db, poll_sec=3600)
    gen = cast(AsyncGenerator[None], wake.signals())
    await _ticks(gen, 1)  # listening: the first look at the queue
    first = db.listeners[0]
    assert first.channels == [JOBS_NEW, JOBS_CONTROL]

    waiting = asyncio.ensure_future(gen.__anext__())
    await asyncio.sleep(0.05)
    assert not waiting.done(), "nothing to wake it: the poll is an hour away"
    first.server_ends_it()
    await asyncio.wait_for(waiting, timeout=5)  # the end of the connection is a wakeup
    # the loop listens again, and looks at once for what was notified meanwhile
    await _ticks(gen, 1)
    assert len(db.listeners) == 2 and not db.listeners[1].closed
    assert db.listeners[1].channels == [JOBS_NEW, JOBS_CONTROL]

    db.listeners[1].notify()  # a job queued after the restart wakes the loop
    await _ticks(gen, 1)
    await wake.aclose()
    await gen.aclose()
    assert db.listeners[1].closed


@pytest.mark.asyncio
async def test_the_poll_goes_on_while_the_database_takes_no_listener() -> None:
    db = _RestartingDB()
    db.down = True
    wake = PgListenWakeup(db, poll_sec=0.01)
    gen = cast(AsyncGenerator[None], wake.signals())
    await _ticks(gen, 3)  # no LISTEN connection: the poll is the wakeup
    assert db.refused >= 3 and db.listeners == [], "each tick tries to listen again"

    db.down = False
    await _ticks(gen, 2)
    assert len(db.listeners) == 1 and db.listeners[0].channels == [JOBS_NEW, JOBS_CONTROL]
    await wake.aclose()
    await gen.aclose()
