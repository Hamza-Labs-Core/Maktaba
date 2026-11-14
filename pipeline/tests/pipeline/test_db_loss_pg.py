"""A real Postgres that stops answering and then drops its connections: no task of the
worker stays behind (the hang of 2026-11-13, see ``test_db_loss_recovery.py``).

The server is reached through a small TCP relay the test can freeze (the bytes stop:
the database "half answers") and cut (every connection dropped: a restart). A statement
is cancelled while the relay is frozen, as the worker heartbeat's 8 s timeout did, then
the connections are cut. Before the fix the task never left ``Database.transaction()``
(asyncpg's ROLLBACK waits, without limit, for the cancelled statement's acknowledgement)
or the pool's release. Runs when ``MAKTABA_TEST_PG_URL`` is set, like the rest of the
Postgres tier.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncGenerator, AsyncIterator
from typing import Any, cast
from urllib.parse import urlsplit, urlunsplit

import pytest
import pytest_asyncio

from maktaba_pipeline import runtime
from maktaba_pipeline.pipeline.wakeup import PgListenWakeup
from maktaba_pipeline.runtime import Database
from tests.engine.fixtures_db import PG_URL

pytestmark = [pytest.mark.integration, pytest.mark.asyncio]


class _Relay:
    """127.0.0.1:<port> -> the test server, with a freeze and a cut."""

    def __init__(self, host: str, port: int) -> None:
        self._upstream = (host, port)
        self._flow = asyncio.Event()
        self._flow.set()
        self._writers: set[asyncio.StreamWriter] = set()
        self._pumps: set[asyncio.Task[None]] = set()
        self._server: asyncio.Server | None = None
        self.port = 0

    async def start(self) -> None:
        self._server = await asyncio.start_server(self._accept, "127.0.0.1", 0)
        self.port = self._server.sockets[0].getsockname()[1]

    async def _accept(self, reader: asyncio.StreamReader, writer: asyncio.StreamWriter) -> None:
        try:
            up_reader, up_writer = await asyncio.open_connection(*self._upstream)
        except OSError:
            writer.close()
            return
        self._writers |= {writer, up_writer}
        for src, dst in ((reader, up_writer), (up_reader, writer)):
            pump = asyncio.ensure_future(self._pump(src, dst))
            self._pumps.add(pump)
            pump.add_done_callback(self._pumps.discard)

    async def _pump(self, src: asyncio.StreamReader, dst: asyncio.StreamWriter) -> None:
        with contextlib.suppress(Exception):
            while data := await src.read(65536):
                await self._flow.wait()
                dst.write(data)
                await dst.drain()
        with contextlib.suppress(Exception):
            dst.close()

    def freeze(self) -> None:
        """Connections stay open and nothing passes: the database does not answer."""
        self._flow.clear()

    def cut(self) -> None:
        """Every connection is dropped, and new ones pass again: the database restarted."""
        for w in list(self._writers):
            with contextlib.suppress(Exception):
                w.transport.abort()
        self._writers.clear()
        for pump in list(self._pumps):
            pump.cancel()
        self._flow.set()

    async def close(self) -> None:
        self.cut()
        if self._server is not None:
            self._server.close()
            await self._server.wait_closed()


@pytest_asyncio.fixture
async def relayed(monkeypatch: pytest.MonkeyPatch) -> AsyncIterator[tuple[Database, _Relay]]:
    if not PG_URL:
        pytest.skip("MAKTABA_TEST_PG_URL not set")
    monkeypatch.setattr(runtime, "PG_RELEASE_TIMEOUT_SEC", 2.0)
    monkeypatch.setattr(runtime, "PG_ROLLBACK_TIMEOUT_SEC", 2.0)
    parts = urlsplit(PG_URL)
    relay = _Relay(parts.hostname or "127.0.0.1", parts.port or 5432)
    await relay.start()
    auth = f"{parts.username}:{parts.password}@" if parts.username else ""
    # no TLS through the relay: the test server's is not what is tested here
    url = urlunsplit(
        (parts.scheme, f"{auth}127.0.0.1:{relay.port}", parts.path, "sslmode=disable", "")
    )
    database = await Database.connect(url)
    try:
        yield database, relay
    finally:
        relay.cut()
        with contextlib.suppress(Exception):
            await asyncio.wait_for(database.close(), timeout=10)
        await relay.close()


async def _statement(db: Database) -> Any:
    return await db.fetchval("SELECT pg_sleep(30)")


async def _in_transaction(db: Database) -> Any:
    async with db.transaction():
        return await db.fetchval("SELECT pg_sleep(30)")


async def _in_savepoint(db: Database) -> Any:
    async with db.transaction(), db.transaction():
        return await db.fetchval("SELECT pg_sleep(30)")


@pytest.mark.parametrize("work", [_statement, _in_transaction, _in_savepoint])
async def test_a_statement_cancelled_on_a_database_that_went_silent_then_dropped_ends(
    relayed: tuple[Database, _Relay], work: Any
) -> None:
    db, relay = relayed
    assert await db.fetchval("SELECT 1") == 1

    async def timed() -> None:
        # the caller's own timeout: the worker heartbeat's, a stage's
        await asyncio.wait_for(work(db), timeout=1.0)

    task = asyncio.ensure_future(timed())
    await asyncio.sleep(0.3)  # the statement is running on the server
    relay.freeze()  # the database stops answering; the timeout cancels the statement
    await asyncio.sleep(1.5)
    relay.cut()  # the server is restarted: every connection is dropped

    done, _pending = await asyncio.wait({task}, timeout=20)
    assert done, "the task never left the database call (the 2026-11-13 hang)"
    assert isinstance(task.exception(), (TimeoutError, OSError)), task.exception()

    # and the worker goes on: the pool hands out working connections again
    assert await asyncio.wait_for(db.fetchval("SELECT 41 + 1"), timeout=20) == 42
    async with db.transaction():
        assert await asyncio.wait_for(db.fetchval("SELECT 2"), timeout=20) == 2
    pool = db._pool
    assert pool._queue.qsize() == len(pool._holders), "every pool slot is free"


async def test_every_connection_dropped_at_once_and_the_pool_recovers(
    relayed: tuple[Database, _Relay],
) -> None:
    db, relay = relayed

    async def one(i: int) -> str:
        try:
            if i % 2:
                async with db.transaction():
                    await db.fetchval("SELECT pg_sleep(5)")
            else:
                await db.fetchval("SELECT pg_sleep(5)")
        except Exception as exc:  # noqa: BLE001 - which error is not the point
            return type(exc).__name__
        return "ok"

    tasks = [asyncio.ensure_future(one(i)) for i in range(8)]
    await asyncio.sleep(0.5)
    relay.cut()  # terminating connection due to administrator command
    results = await asyncio.wait_for(asyncio.gather(*tasks), timeout=20)
    assert "ok" not in results, results
    for _ in range(12):  # more than the pool holds: no slot was lost
        assert await asyncio.wait_for(db.fetchval("SELECT 1"), timeout=20) == 1


async def test_the_claim_wakeup_listens_again_after_the_server_dropped_it(
    relayed: tuple[Database, _Relay],
) -> None:
    db, relay = relayed
    # the poll is far away: only LISTEN/NOTIFY (or the connection's end) wakes the loop
    wake = PgListenWakeup(db, poll_sec=3600, channel="stall_test_jobs", extra_channels=())
    gen = cast(AsyncGenerator[None], wake.signals())
    try:
        await asyncio.wait_for(gen.__anext__(), timeout=20)  # listening
        await db.execute("SELECT pg_notify('stall_test_jobs', '')")
        await asyncio.wait_for(gen.__anext__(), timeout=20)

        relay.cut()  # the LISTEN connection is gone with the others
        await asyncio.wait_for(gen.__anext__(), timeout=20)  # its end wakes the loop
        # listening again makes it look once more (in that same pass when the loop saw
        # the connection closed before it waited): let that pass, then nothing is due
        waiting = asyncio.ensure_future(gen.__anext__())
        if (await asyncio.wait({waiting}, timeout=2.0))[0]:
            waiting = asyncio.ensure_future(gen.__anext__())
        await asyncio.sleep(0.3)
        assert not waiting.done(), "the poll is an hour away"

        await db.execute("SELECT pg_notify('stall_test_jobs', '')")  # queued after the restart
        await asyncio.wait_for(waiting, timeout=20)
    finally:
        await wake.aclose()
        await asyncio.wait_for(gen.aclose(), timeout=20)
