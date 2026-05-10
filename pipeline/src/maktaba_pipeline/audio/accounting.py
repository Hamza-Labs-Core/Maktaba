"""Story 2.4 — extract concurrency cap and CPU throttle helper.

Two surfaces:

- :class:`ExtractAccountant` — async context manager wrapping
  :class:`asyncio.Semaphore`. The worker takes a slot before spawning
  ffmpeg and releases on stage exit (success **or** failure). The cap
  is per process; horizontal scaling means more processes, not a
  raised cap.
- :func:`cpu_throttle_not_before` — pure function that returns a
  ``timedelta`` to add to the next claim's ``not_before`` when load
  average exceeds ``threshold × cores``. Off by default; toggled via
  ``library.settings.pipeline.cpu_throttle_enabled``.

Priority ordering (Story 2.4 AC-2) is the claim-loop's responsibility,
not this module's — :func:`maktaba_pipeline.db.jobs_claim.claim_one`
already orders by ``priority`` before ``id`` so a priority-50 job
preempts queued priority-100s as soon as a slot frees.
"""

from __future__ import annotations

import asyncio
import os
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from datetime import datetime, timedelta

__all__ = [
    "DEFAULT_EXTRACT_CONCURRENCY",
    "DEFAULT_CPU_THROTTLE_DELAY_SEC",
    "ExtractAccountant",
    "cpu_throttle_not_before",
]

DEFAULT_EXTRACT_CONCURRENCY = 2
DEFAULT_CPU_THROTTLE_DELAY_SEC = 30.0


class ExtractAccountant:
    """Per-process semaphore for the ``extract`` stage.

    Attribute :attr:`in_flight` reports how many extract jobs are
    currently holding a slot — used by metrics and tests.
    """

    __slots__ = ("_sem", "_cap", "in_flight")

    def __init__(self, capacity: int = DEFAULT_EXTRACT_CONCURRENCY) -> None:
        if capacity < 1:
            raise ValueError("ExtractAccountant capacity must be >= 1")
        self._cap = capacity
        self._sem = asyncio.Semaphore(capacity)
        self.in_flight = 0

    @property
    def capacity(self) -> int:
        return self._cap

    @property
    def available(self) -> int:
        return self._cap - self.in_flight

    @asynccontextmanager
    async def slot(self) -> AsyncIterator[None]:
        """Block until a slot frees, then yield. Always release on exit."""
        await self._sem.acquire()
        self.in_flight += 1
        try:
            yield
        finally:
            self.in_flight -= 1
            self._sem.release()


def cpu_throttle_not_before(
    *,
    load_avg_5m: float,
    cores: int | None = None,
    threshold: float = 1.0,
    delay_sec: float = DEFAULT_CPU_THROTTLE_DELAY_SEC,
    now: datetime | None = None,
) -> datetime | None:
    """Return ``not_before`` for the next claim, or ``None`` to claim now.

    The check fires when ``load_avg_5m > threshold × cores``. The
    default threshold of 1.0 keeps the worker idle when the box is
    fully loaded; libraries that want to use spare cycles can lower
    the threshold (e.g. ``0.7``) to claim only on quiet boxes.
    """
    cores = cores or _detect_cores()
    if cores <= 0:
        return None
    if load_avg_5m <= threshold * cores:
        return None
    base = now or datetime.now()
    return base + timedelta(seconds=delay_sec)


def _detect_cores() -> int:
    try:
        return os.cpu_count() or 1
    except NotImplementedError:
        return 1
