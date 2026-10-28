"""Periodic reaper of crashed/stale claims (Story 6.6).

A worker that died holding a ``claimed`` / ``running`` / ``resuming``
row must release it. This module owns the periodic loop; the SQL
lives in :mod:`maktaba_pipeline.db.jobs_reaper`.

Two cross-process exclusion strategies:

- **Postgres** uses :func:`pg_try_advisory_lock(REAPER_ADVISORY_LOCK_KEY)`
  so only one worker process drives the sweep at a time. The lock is
  session-scoped — a worker that crashes mid-tick releases it
  automatically when the connection dies.
- **SQLite** has no advisory locks; the loop uses a process-local
  :class:`asyncio.Lock` instead. Multi-process SQLite deployments are
  out of scope (architecture §8.0); for single-process dev rigs the
  local lock is sufficient.

The default ``stale_claim_sec=90 s`` is **18 ×** the canonical
``heartbeat_sec=5 s`` — the constructor enforces this ratio so a
config drift to one without the other fails fast at startup rather
than allowing a too-aggressive (false-positive reaps) or too-lax (slow
recovery) sweep.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator, Callable
from typing import Any

from ..db.jobs_reaper import REAPER_ADVISORY_LOCK_KEY, reap_once
from ..log import get_logger
from ..log.tasks import watch as watch_task

__all__ = [
    "DEFAULT_REAPER_INTERVAL_SEC",
    "DEFAULT_STALE_CLAIM_SEC",
    "STALE_TO_HEARTBEAT_RATIO",
    "Reaper",
]


_log = get_logger()


DEFAULT_REAPER_INTERVAL_SEC: float = 30.0
DEFAULT_STALE_CLAIM_SEC: float = 90.0

# Story 6.6 README §1.4.c: 90 s = 18 × 5 s heartbeat. Eighteen missed
# heartbeats is the threshold for "the worker is definitely dead, not
# just slow."
STALE_TO_HEARTBEAT_RATIO: float = 18.0


class Reaper:
    """Periodic stale-claim sweep, one instance per worker process.

    Lifecycle: :meth:`start` spawns the loop; :meth:`stop` flips the
    stop event and awaits the loop to drain. Construction enforces
    the ``stale_claim_sec / heartbeat_sec == 18`` invariant.
    """

    def __init__(
        self,
        db: Any,
        *,
        interval_sec: float = DEFAULT_REAPER_INTERVAL_SEC,
        stale_claim_sec: float = DEFAULT_STALE_CLAIM_SEC,
        heartbeat_sec: float = 5.0,
        paused: Callable[[], bool] | None = None,
    ) -> None:
        """``paused``: while it answers True the reaper writes nothing (a joined machine
        whose code does not fit the home's schema, cluster/schema.py)."""
        if interval_sec <= 0:
            raise ValueError("interval_sec must be > 0")
        if stale_claim_sec <= 0:
            raise ValueError("stale_claim_sec must be > 0")
        if heartbeat_sec > 0:
            ratio = stale_claim_sec / heartbeat_sec
            if abs(ratio - STALE_TO_HEARTBEAT_RATIO) > 1e-6:
                raise ValueError(
                    f"stale_claim_sec ({stale_claim_sec}) must equal "
                    f"{STALE_TO_HEARTBEAT_RATIO} × heartbeat_sec "
                    f"({heartbeat_sec}) = "
                    f"{STALE_TO_HEARTBEAT_RATIO * heartbeat_sec}"
                )

        self.db = db
        self.interval_sec = interval_sec
        self.stale_claim_sec = stale_claim_sec
        self._stop = asyncio.Event()
        self._task: asyncio.Task[None] | None = None
        self._local_lock: asyncio.Lock | None = None
        self._paused = paused

    @contextlib.asynccontextmanager
    async def _locked(self) -> AsyncIterator[bool]:
        """Hold the sweep's lock for one tick; yields ``False`` when it is busy.

        Postgres: ``pg_try_advisory_lock`` and its ``pg_advisory_unlock`` on *one*
        connection of the pool, held for the whole tick. A session lock belongs to the
        connection that took it: taken through the pool's ``fetchrow`` (any connection)
        it was unlocked on another one ("WARNING: you don't own a lock of type
        ExclusiveLock" every 30 s from every worker process) and stayed with the first,
        so the other processes' sweeps found it busy for good. SQLite: a local mutex.
        Non-blocking either way: a busy lock means another instance (or the previous
        tick of this instance) is still running.
        """
        if getattr(self.db, "dialect", "sqlite") == "postgres":
            pool = getattr(self.db, "_pool", None)
            if pool is None:  # a stand-in database without a pool (tests)
                row = await self.db.fetchrow(
                    "SELECT pg_try_advisory_lock($1) AS got", REAPER_ADVISORY_LOCK_KEY
                )
                got = bool(row["got"]) if row is not None else False
                try:
                    yield got
                finally:
                    if got:
                        await self.db.fetchrow(
                            "SELECT pg_advisory_unlock($1) AS released", REAPER_ADVISORY_LOCK_KEY
                        )
                return
            async with pool.acquire() as conn:
                got = bool(
                    await conn.fetchval("SELECT pg_try_advisory_lock($1)", REAPER_ADVISORY_LOCK_KEY)
                )
                try:
                    yield got
                finally:
                    if got:
                        await conn.fetchval(
                            "SELECT pg_advisory_unlock($1)", REAPER_ADVISORY_LOCK_KEY
                        )
            return

        if self._local_lock is None:
            self._local_lock = asyncio.Lock()
        if self._local_lock.locked():
            yield False
            return
        await self._local_lock.acquire()
        try:
            yield True
        finally:
            self._local_lock.release()

    async def tick(self) -> int:
        """One reaper tick. Returns the number of rows reaped.

        Public so tests can drive the loop deterministically without
        spinning the periodic timer.
        """
        if self._paused is not None and self._paused():
            return 0
        async with self._locked() as got:
            if not got:
                _log.debug("reaper_lock_busy")
                return 0
            reaped = await reap_once(
                self.db,
                stale_claim_sec=self.stale_claim_sec,
            )
            if reaped:
                _log.info(
                    "reaped_stale_claims",
                    count=len(reaped),
                    ids=[r.id for r in reaped],
                )
            return len(reaped)

    async def _run(self) -> None:
        _log.info(
            "reaper_started",
            interval_sec=self.interval_sec,
            stale_claim_sec=self.stale_claim_sec,
        )
        while not self._stop.is_set():
            try:
                await self.tick()
            except Exception:
                _log.exception("reaper_tick_failed")
            try:
                await asyncio.wait_for(
                    self._stop.wait(),
                    timeout=self.interval_sec,
                )
                return  # _stop set
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                continue

    def start(self) -> None:
        if self._task is not None:
            raise RuntimeError("Reaper already started")
        self._task = watch_task(asyncio.create_task(self._run(), name="reaper"))

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
            self._task = None
