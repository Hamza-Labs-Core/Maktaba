"""The worker's watchdog: a worker that cannot work ends, so that it is started again.

On 2026-11-13 the home's database was restarted under the main worker. The worker stayed
up for hours, dispatched nothing, logged nothing, and its ``/healthz`` said "ok" (the
event loop was beating), so nothing restarted it. Two things end such a worker here, with
:data:`~maktaba_pipeline.cluster.exitcodes.EX_TEMPFAIL` (75, "start me again"):

- a **core loop ended** while the worker was not stopping (:meth:`Watchdog.guard`): the
  registry heartbeat, the stage selection, the components' runner, the reaper, the
  database watch, the claim loop. None of them returns before shutdown, so one that did
  was cancelled from outside or died, and the worker would run on without it;
- something that must keep moving **stayed still** (:class:`~..log.liveness.Progress`:
  the claim loop's passes, the database's answers) for its stale window plus
  ``grace_sec``. ``/healthz`` already answers 503 at the stale window, so a supervisor
  that probes it restarts the worker first; a worker nothing probes (a joined machine
  under its service manager) restarts itself.

The exit is the graceful one (the shutdown event: jobs in flight get their drain). A
worker stuck where the shutdown cannot reach it would never finish that, so
``hard_exit_sec`` later the process is ended outright.
"""

from __future__ import annotations

import asyncio
import contextlib
import os
import sys
from collections.abc import Callable
from typing import Any

from ..cluster.exitcodes import EX_TEMPFAIL
from ..log import get_logger
from ..log.liveness import Progress

__all__ = ["DEFAULT_GRACE_SEC", "Watchdog", "database_watch"]

_log = get_logger()

#: past a stale window, how long the worker still has to move again before it ends
DEFAULT_GRACE_SEC = 60.0
#: how often the watchdog looks
CHECK_INTERVAL_SEC = 5.0
#: how often the database is asked, and how long it has to answer
DB_PROBE_INTERVAL_SEC = 15.0
DB_PROBE_TIMEOUT_SEC = 10.0


def _exit_now(code: int) -> None:
    for stream in (sys.stdout, sys.stderr):
        with contextlib.suppress(Exception):
            stream.flush()
    os._exit(code)


class Watchdog:
    """Ends the worker (sets ``shutdown``, exit code 75) when it cannot work."""

    def __init__(
        self,
        shutdown: asyncio.Event,
        *,
        grace_sec: float = DEFAULT_GRACE_SEC,
        hard_exit_sec: float = 60.0,
        exit_now: Callable[[int], None] | None = None,
    ) -> None:
        self.shutdown = shutdown
        self.grace_sec = grace_sec
        self.hard_exit_sec = hard_exit_sec
        self._exit_now = exit_now
        self._progress: list[Progress] = []
        #: why the worker is ending (None: it is not, or it was asked to stop)
        self.reason: str | None = None
        self._hard: asyncio.TimerHandle | None = None

    @property
    def exit_code(self) -> int:
        return EX_TEMPFAIL if self.reason is not None else 0

    def watch(self, progress: Progress) -> Progress:
        self._progress.append(progress)
        return progress

    def guard[T: asyncio.Future[Any]](self, task: T, name: str | None = None) -> T:
        """``task`` is a core loop: its end before shutdown ends the worker."""
        label = name or (task.get_name() if isinstance(task, asyncio.Task) else "loop")

        def ended(t: asyncio.Future[Any]) -> None:
            if self.shutdown.is_set():
                return
            why = "was cancelled"
            if not t.cancelled():
                exc = t.exception()
                why = "returned" if exc is None else f"died: {type(exc).__name__}: {exc}"
            self.fail(f"the {label} loop {why}")

        task.add_done_callback(ended)
        return task

    def fail(self, reason: str) -> None:
        """End the worker for ``reason`` (the first one counts)."""
        if self.reason is not None or self.shutdown.is_set():
            return
        self.reason = reason
        _log.error(
            "pipeline_worker_cannot_work",
            reason=reason,
            exit_code=EX_TEMPFAIL,
            hard_exit_after_sec=self.hard_exit_sec,
        )
        self.shutdown.set()
        self._hard = asyncio.get_running_loop().call_later(self.hard_exit_sec, self._hard_exit)

    def _hard_exit(self) -> None:
        _log.error("pipeline_worker_exit_forced", reason=self.reason, exit_code=EX_TEMPFAIL)
        (self._exit_now or _exit_now)(EX_TEMPFAIL)

    def disarm(self) -> None:
        """The worker stopped by itself: no forced exit."""
        if self._hard is not None:
            self._hard.cancel()
            self._hard = None

    def check(self) -> str | None:
        """The first thing that stayed still past its stale window and the grace."""
        for p in self._progress:
            if p.age() > p.stale_sec + self.grace_sec:
                return p.reason()
        return None

    async def run(self, interval: float = CHECK_INTERVAL_SEC) -> None:
        while not self.shutdown.is_set():
            stuck = self.check()
            if stuck is not None:
                self.fail(stuck)
                return
            with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                await asyncio.wait_for(self.shutdown.wait(), timeout=interval)


async def database_watch(
    db: Any,
    progress: Progress,
    stop: asyncio.Event,
    *,
    interval: float = DB_PROBE_INTERVAL_SEC,
    timeout: float = DB_PROBE_TIMEOUT_SEC,
) -> None:
    """Ask the database a question every ``interval``; each answer marks ``progress``.
    An outage is said once, and its end once."""
    lost = False
    while not stop.is_set():
        try:
            await asyncio.wait_for(db.fetchval("SELECT 1"), timeout=timeout)
        except Exception as exc:  # noqa: BLE001 - the next round asks again
            if not lost:
                lost = True
                _log.warning("database_not_answering", error=f"{type(exc).__name__}: {exc}")
        else:
            progress.mark()
            if lost:
                lost = False
                _log.info("database_answering_again")
        with contextlib.suppress(TimeoutError, asyncio.TimeoutError):  # noqa: UP041
            await asyncio.wait_for(stop.wait(), timeout=interval)
