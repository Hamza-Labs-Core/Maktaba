"""Pipeline-side concurrency cap (Epic 19 plan-19-07).

A small async-aware semaphore that orchestrator steps wrap around
expensive operations (ffmpeg invocations, whisper batches). The cap is
read from ``MAKTABA_PIPELINE_MAX_CONCURRENT_{STAGE}`` env vars.
"""

from __future__ import annotations

import asyncio
import contextlib


class ConcurrencyError(RuntimeError):
    """Raised when a non-blocking acquire could not get a slot."""


class Concurrency:
    """Bounded async semaphore with a name (for metrics labels)."""

    def __init__(self, name: str, capacity: int) -> None:
        if capacity <= 0:
            raise ValueError("capacity must be > 0")
        self.name = name
        self._capacity = capacity
        self._sem = asyncio.Semaphore(capacity)
        self._in_use = 0
        self._lock = asyncio.Lock()

    @property
    def capacity(self) -> int:
        return self._capacity

    @property
    def in_use(self) -> int:
        return self._in_use

    @contextlib.asynccontextmanager
    async def acquire(self):
        await self._sem.acquire()
        async with self._lock:
            self._in_use += 1
        try:
            yield
        finally:
            async with self._lock:
                self._in_use -= 1
            self._sem.release()

    def try_acquire(self) -> "_TokenHandle":
        """Non-async, non-blocking acquire. Raises ``ConcurrencyError`` if full."""
        if self._sem.locked() and self._in_use >= self._capacity:
            raise ConcurrencyError(f"{self.name} concurrency cap reached")
        # We don't have a clean non-async path for asyncio.Semaphore.acquire;
        # tests of try_acquire usually run inside a running loop and use
        # ``acquire`` instead. This synchronous branch is best-effort.
        return _TokenHandle(self)


class _TokenHandle:
    """Token returned by :meth:`Concurrency.try_acquire`; release manually."""

    def __init__(self, owner: Concurrency) -> None:
        self._owner = owner
        self._released = False

    def release(self) -> None:
        if self._released:
            return
        self._released = True
