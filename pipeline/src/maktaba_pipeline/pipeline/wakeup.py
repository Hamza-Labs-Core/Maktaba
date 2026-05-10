"""Wakeup sources — what tells the claim loop "try claiming now".

The claim loop is a pure consumer of an ``async for _ in source.signals()``
stream. Each yielded item is a "wake up and try again" tick; the loop
itself owns the actual claim, dispatch, and backoff. This split lets
the wakeup source be tested independently and lets us swap in a
poll-only source for deterministic tests that don't want a bus.

Three implementations live here:

- :class:`PgListenWakeup` — Postgres LISTEN on ``jobs.new`` (the
  channel pinned in :data:`maktaba_pipeline.db.pubsub.JOBS_NEW`) plus
  a poll-tick safety net at :attr:`poll_sec`. The poll catches the
  rare cases where a notification is dropped or the listener silently
  reconnects, and bounds the worst-case latency to ``poll_sec``.
- :class:`PubsubWakeup` — for SQLite mode (no NOTIFY) and for tests.
  Subscribes to the in-process :class:`PubsubBus` from Story 6.1's
  ``pipeline.db.pubsub`` and ticks at ``poll_sec``.
- :class:`PollOnlyWakeup` — pure timer; no notification source. Useful
  for tests that want determinism without spinning a bus, and as a
  last-resort fallback when even the bus is unavailable.

Mutating ``poll_sec`` between iterations is supported and safe: each
loop iteration reads the current value, so the claim loop can apply
its own exponential backoff by setting longer poll intervals when the
queue has been consistently empty.
"""

from __future__ import annotations

import asyncio
import contextlib
from collections.abc import AsyncIterator
from typing import Any, Protocol, runtime_checkable

from ..db.pubsub import JOBS_NEW, PubsubBus, get_bus

__all__ = [
    "PgListenWakeup",
    "PollOnlyWakeup",
    "PubsubWakeup",
    "WakeupSource",
]


@runtime_checkable
class WakeupSource(Protocol):
    """A stream of "try claiming now" ticks.

    Implementations expose a mutable :attr:`poll_sec` so the claim
    loop can dial the safety-net cadence up or down between iterations
    (see exponential-backoff in :class:`ClaimLoop`).
    """

    poll_sec: float

    def signals(self) -> AsyncIterator[None]: ...

    async def aclose(self) -> None: ...


class PollOnlyWakeup:
    """Timer-only wakeup. No notification source.

    Used by tests that need a deterministic clock and as the
    last-resort fallback in production when the LISTEN/bus path is
    unavailable. Each iteration races the timer against the stop
    event so :meth:`aclose` returns within at most ``poll_sec``.
    """

    def __init__(self, *, poll_sec: float) -> None:
        if poll_sec <= 0:
            raise ValueError("poll_sec must be > 0")
        self.poll_sec = poll_sec
        self._stop = asyncio.Event()

    async def signals(self) -> AsyncIterator[None]:
        while not self._stop.is_set():
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(self._stop.wait(), timeout=self.poll_sec)
            if self._stop.is_set():
                return
            yield None

    async def aclose(self) -> None:
        self._stop.set()


class PubsubWakeup:
    """In-process pub/sub + poll-tick fallback.

    Subscribes to ``channel`` on the process-wide :class:`PubsubBus`
    and races each iteration between (a) a notify drop, (b) a poll
    timeout, and (c) the stop event. The poll timeout is the safety
    net the claim loop also uses for exponential backoff: setting
    ``poll_sec`` higher slows the timer arm without affecting how
    quickly a real notify wakes the loop.
    """

    def __init__(
        self,
        *,
        poll_sec: float,
        channel: str = JOBS_NEW,
        bus: PubsubBus | None = None,
    ) -> None:
        if poll_sec <= 0:
            raise ValueError("poll_sec must be > 0")
        self.poll_sec = poll_sec
        self._channel = channel
        self._bus = bus if bus is not None else get_bus()
        self._stop = asyncio.Event()

    async def signals(self) -> AsyncIterator[None]:
        queue = await self._bus.subscribe(self._channel)
        try:
            while not self._stop.is_set():
                done = await _race(
                    queue.get(),
                    asyncio.sleep(self.poll_sec),
                    self._stop.wait(),
                )
                # Drain the queue so a notify storm collapses into one
                # wakeup signal — the claim loop iterates "claim until
                # None" anyway, so additional ticks would be wasted.
                while not queue.empty():
                    queue.get_nowait()
                del done
                if self._stop.is_set():
                    return
                yield None
        finally:
            self._bus.unsubscribe(self._channel, queue)

    async def aclose(self) -> None:
        self._stop.set()


class PgListenWakeup:
    """Postgres LISTEN + poll-tick fallback.

    The connection wrapper from Story 1.5 is expected to expose
    ``acquire_listener()`` returning an object with
    ``add_listener(channel, callback)`` and ``remove_listener``. Until
    that lands, this class is wired up but exercised only via
    integration tests against a real DB. The implementation matches
    the shape asyncpg's ``Connection.add_listener`` exposes today, so
    the wrapper can pass the asyncpg listener through directly.
    """

    def __init__(
        self,
        db: Any,
        *,
        poll_sec: float,
        channel: str = JOBS_NEW,
    ) -> None:
        if poll_sec <= 0:
            raise ValueError("poll_sec must be > 0")
        self.poll_sec = poll_sec
        self._db = db
        self._channel = channel
        self._stop = asyncio.Event()

    async def signals(self) -> AsyncIterator[None]:
        listener = await self._db.acquire_listener()
        events: asyncio.Queue[None] = asyncio.Queue()

        def _on_notify(*_args: Any) -> None:
            # Queue is unbounded; QueueFull is unreachable in practice.
            # Suppression exists only so a wedged subscriber can't
            # crash the listener callback.
            with contextlib.suppress(asyncio.QueueFull):
                events.put_nowait(None)

        await listener.add_listener(self._channel, _on_notify)
        try:
            while not self._stop.is_set():
                await _race(
                    events.get(),
                    asyncio.sleep(self.poll_sec),
                    self._stop.wait(),
                )
                while not events.empty():
                    events.get_nowait()
                if self._stop.is_set():
                    return
                yield None
        finally:
            with contextlib.suppress(Exception):
                # Tear-down is best-effort: the listener may already
                # have been closed, the connection may have died, or
                # the wrapper may have removed it under us.
                await listener.remove_listener(self._channel, _on_notify)

    async def aclose(self) -> None:
        self._stop.set()


async def _race(*coros: Any) -> set[asyncio.Task[Any]]:
    """Run ``coros`` until the first completes; cancel and await the rest.

    Wrapping each coro in a task lets us cancel the losers cleanly.
    Awaiting cancelled tasks (with the CancelledError suppressed)
    avoids "Task was destroyed but it is pending" warnings.
    """
    tasks = [asyncio.create_task(c) for c in coros]
    done, pending = await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    for task in pending:
        task.cancel()
    for task in pending:
        try:
            await task
        except asyncio.CancelledError:
            pass
        except Exception:  # noqa: BLE001 — losing race; result is irrelevant
            pass
    return done
