"""Event-loop heartbeat behind the pipeline's ``/healthz`` (Story 22.9).

The pipeline's HTTP port runs on its own daemon thread, so it keeps
answering even when the asyncio loop that does the real work is wedged.
A task on the loop beats every few seconds; ``/healthz`` answers 503 once
the last beat is older than the stale window, and the maktaba-server
supervisor then kills and restarts the process.

The stale window defaults to five minutes (``MAKTABA_PIPELINE_LOOP_STALE``,
seconds) so a stage that blocks the loop for a while on purpose is not
mistaken for a hang.

A beating loop is not a working worker: on 2026-11-13 the loop beat for hours
while nothing was dispatched. So the worker also registers what must keep
moving for jobs to run (:class:`Progress`: the claim loop's passes, the
database's answers), and ``/healthz`` answers 503 when one of them has not
moved within its own window (``MAKTABA_PIPELINE_DISPATCH_STALE``,
``MAKTABA_PIPELINE_DB_STALE``, seconds; five minutes each).
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
    "Progress",
    "clear_progress",
    "get_heartbeat",
    "progress_checks",
    "stale_from_env",
    "watch_progress",
    "run_heartbeat",
    "set_heartbeat",
]

DEFAULT_STALE_SEC = 300.0
BEAT_INTERVAL_SEC = 5.0


def stale_from_env(name: str = "MAKTABA_PIPELINE_LOOP_STALE") -> float:
    """The stale window ``name`` sets (seconds), else the default."""
    try:
        value = float(os.getenv(name, ""))
    except ValueError:
        return DEFAULT_STALE_SEC
    return value if value > 0 else DEFAULT_STALE_SEC


class LoopHeartbeat:
    """The time of the event loop's last beat; safe to read from any thread."""

    def __init__(
        self, stale_sec: float | None = None, clock: Callable[[], float] = time.monotonic
    ) -> None:
        self.stale_sec = stale_sec if stale_sec is not None else stale_from_env()
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


class Progress:
    """The last time something that must keep moving did (a pass of the claim loop, an
    answer from the database); safe to read from any thread. ``what`` says it in the
    health answer: "no pass of the claim loop", "no answer from the database"."""

    def __init__(
        self,
        name: str,
        what: str,
        stale_sec: float,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self.name = name
        self.what = what
        self.stale_sec = stale_sec
        self._clock = clock
        self._lock = threading.Lock()
        self._last = clock()

    def mark(self) -> None:
        with self._lock:
            self._last = self._clock()

    def age(self) -> float:
        with self._lock:
            return self._clock() - self._last

    def healthy(self) -> bool:
        return self.age() <= self.stale_sec

    def reason(self) -> str:
        return f"{self.what} for {self.age():.0f}s (stale after {self.stale_sec:.0f}s)"


_PROGRESS: dict[str, Progress] = {}
_PROGRESS_LOCK = threading.Lock()


def watch_progress(progress: Progress) -> Progress:
    """``/healthz`` answers for ``progress`` too, from now on (by name: one each)."""
    with _PROGRESS_LOCK:
        _PROGRESS[progress.name] = progress
    return progress


def progress_checks() -> list[Progress]:
    with _PROGRESS_LOCK:
        return list(_PROGRESS.values())


def clear_progress() -> None:
    """The worker stopped (and tests): ``/healthz`` is the loop's heartbeat alone again."""
    with _PROGRESS_LOCK:
        _PROGRESS.clear()


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
