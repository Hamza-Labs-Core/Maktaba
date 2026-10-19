"""Event-loop heartbeat behind the pipeline's ``/healthz`` (Story 22.9).

The pipeline's HTTP port runs on its own daemon thread, so it keeps
answering even when the asyncio loop that does the real work is wedged.
A task on the loop beats every few seconds; ``/healthz`` answers 503 once
the last beat is older than the stale window, and the maktaba-server
supervisor then kills and restarts the process.

The stale window defaults to five minutes (``MAKTABA_PIPELINE_LOOP_STALE``,
seconds) so a stage that blocks the loop for a while on purpose is not
mistaken for a hang.
"""

from __future__ import annotations

import asyncio
import os
import threading
import time
from collections.abc import Callable

__all__ = [
    "DEFAULT_STALE_SEC",
    "LoopHeartbeat",
    "get_heartbeat",
    "run_heartbeat",
    "set_heartbeat",
]

DEFAULT_STALE_SEC = 300.0
BEAT_INTERVAL_SEC = 5.0


def _stale_from_env() -> float:
    try:
        value = float(os.getenv("MAKTABA_PIPELINE_LOOP_STALE", ""))
    except ValueError:
        return DEFAULT_STALE_SEC
    return value if value > 0 else DEFAULT_STALE_SEC


class LoopHeartbeat:
    """The time of the event loop's last beat; safe to read from any thread."""

    def __init__(
        self, stale_sec: float | None = None, clock: Callable[[], float] = time.monotonic
    ) -> None:
        self.stale_sec = stale_sec if stale_sec is not None else _stale_from_env()
        self._clock = clock
        self._lock = threading.Lock()
        self._last = clock()

    def beat(self) -> None:
        with self._lock:
            self._last = self._clock()

    def age(self) -> float:
        with self._lock:
            return self._clock() - self._last

    def healthy(self) -> bool:
        return self.age() <= self.stale_sec


_HEARTBEAT: LoopHeartbeat | None = None


def set_heartbeat(hb: LoopHeartbeat | None) -> None:
    global _HEARTBEAT
    _HEARTBEAT = hb


def get_heartbeat() -> LoopHeartbeat | None:
    return _HEARTBEAT


async def run_heartbeat(hb: LoopHeartbeat, interval: float = BEAT_INTERVAL_SEC) -> None:
    """Beat until cancelled. Runs on the loop it vouches for."""
    while True:
        hb.beat()
        await asyncio.sleep(interval)
