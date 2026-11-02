"""One leader at a time for a home-wide loop (Postgres): a session advisory lock.

The reconciler and the queue-health round run on whichever worker of the home holds
their lock. They used to take it with ``pg_try_advisory_xact_lock`` inside one
transaction over the whole pass, so nothing the pass wrote (a job it queued, a parked job
it released) could be claimed before the pass ended, and its row locks were held as long.

:func:`leader` takes ``pg_try_advisory_lock`` on a pool connection of its own, held for
the pass and used for nothing else; the pass runs its units on other pool connections,
each in its own short transaction (or a statement). The lock ends with the pass (unlocked
in ``finally``; the pool's reset unlocks everything a connection still holds when it is
given back), and with the connection: a worker that dies, or whose connection drops,
frees it on the server, and the next pass of any worker takes it.

The key is the same ``hashtext`` the transaction lock used: a session lock and a
transaction lock of one key conflict, so during an update a worker of the old release
and one of this release still never run passes at once.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from typing import Any

__all__ = ["leader"]


@contextlib.asynccontextmanager
async def leader(db: Any, key: str) -> AsyncIterator[bool]:
    """Hold the session advisory lock ``hashtext(key)`` for the block; yields ``False``
    (at once, never waiting) when another session holds it. Postgres only."""
    pool = getattr(db, "_pool", None)
    if pool is None:  # a stand-in with a single connection: the lock is that session's
        got = bool(await db.fetchval("SELECT pg_try_advisory_lock(hashtext($1))", key))
        try:
            yield got
        finally:
            if got:
                with contextlib.suppress(Exception):
                    await db.execute("SELECT pg_advisory_unlock(hashtext($1))", key)
        return
    async with pool.acquire() as conn:
        got = bool(await conn.fetchval("SELECT pg_try_advisory_lock(hashtext($1))", key))
        try:
            yield got
        finally:
            if got:
                await _unlock(conn, key)
            _give_back_if_dead(conn)


def _give_back_if_dead(conn: Any) -> None:
    """A connection the server ended (a dropped network, a terminated backend) gets its
    pool slot back only once it is terminated: asyncpg sometimes does that itself (the
    proxy is then detached and every call on it raises), sometimes not, and then the slot
    stays taken and the pool's close waits forever."""
    with contextlib.suppress(Exception):  # detached: the pool has the slot back already
        if conn.is_closed():
            conn.terminate()


async def _unlock(conn: Any, key: str) -> None:
    """``pg_advisory_unlock``, run to its end even when the caller is cancelled meanwhile:
    a cancel landing in it would leave the query half-done, so the pool would close the
    connection and the server free the lock only some time later, and the next pass could
    find it still held. A connection that died took the lock with it, and the pool's reset
    (``pg_advisory_unlock_all``) ends it on any other failure: an error here never
    replaces the pass's own outcome, and a cancel stays a cancel."""
    try:
        unlock = asyncio.ensure_future(conn.execute("SELECT pg_advisory_unlock(hashtext($1))", key))
    except Exception:  # noqa: BLE001 - the pool took a dead connection back already
        return
    try:
        with contextlib.suppress(Exception):
            await asyncio.shield(unlock)
    except asyncio.CancelledError:
        with contextlib.suppress(Exception):
            await unlock
        raise
