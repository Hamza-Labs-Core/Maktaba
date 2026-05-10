"""Periodic ``last_heartbeat_at`` updater for stages without per-segment cadence.

Architecture §7.10 and Story 6.3 own the contract. A stage handler
wraps its main loop in :func:`heartbeat_for`; the context manager
spawns a coroutine that fires :func:`tick_heartbeat` every
``interval_sec`` seconds until the context exits.

For transcribe specifically, the heartbeat task is *also* started (not
just the per-segment progress tick) so a 60 s ffmpeg decode inside one
segment doesn't trip the reaper. The double-tick (progress UPDATE +
heartbeat UPDATE in the same window) is harmless: both update the same
column, and the trigger picks the later one.
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

from ..db.jobs import DBConn
from ..db.jobs_progress import tick_heartbeat
from ..log import get_logger

__all__ = [
    "DEFAULT_HEARTBEAT_SEC",
    "HeartbeatTask",
    "heartbeat_for",
]


_log = get_logger()


# 5 s default per architecture §7.10 / plan-06-03 §0. Story 6.6's
# reaper assumes this exact value (90 s = 18 × 5 s); changing the
# default requires updating the reaper's parity assertion.
DEFAULT_HEARTBEAT_SEC: float = 5.0


class HeartbeatTask:
    """Periodic ``tick_heartbeat`` driver bound to one job's lifetime.

    Lifecycle: started at the top of the stage handler, stopped at the
    end (success, failure, or pause). The progress tick path is always
    a stronger signal — start the heartbeat task only for stages that
    do NOT call :func:`tick_progress` on a frequent enough cadence, or
    alongside it for stages where a single processing unit can run
    longer than ``stale_claim_sec`` (e.g., transcribe with a slow
    decoder).
    """

    def __init__(self, db: DBConn, *, job_id: int, interval_sec: float) -> None:
        if interval_sec <= 0:
            raise ValueError("interval_sec must be > 0")
        self.db = db
        self.job_id = job_id
        self.interval_sec = interval_sec
        self._task: asyncio.Task[None] | None = None
        self._stop = asyncio.Event()

    async def _run(self) -> None:
        while not self._stop.is_set():
            try:
                await asyncio.wait_for(
                    self._stop.wait(),
                    timeout=self.interval_sec,
                )
                return  # _stop set → exit cleanly without firing again.
            except (TimeoutError, asyncio.TimeoutError):  # noqa: UP041
                try:
                    await tick_heartbeat(self.db, job_id=self.job_id)
                except Exception:
                    # The reaper is the safety net; never let a transient
                    # heartbeat failure tear down the stage handler.
                    _log.exception(
                        "heartbeat_tick_failed",
                        job_id=self.job_id,
                    )

    def start(self) -> None:
        if self._task is not None:
            raise RuntimeError("HeartbeatTask already started")
        self._task = asyncio.create_task(
            self._run(),
            name=f"heartbeat-{self.job_id}",
        )

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
            self._task = None


@asynccontextmanager
async def heartbeat_for(
    db: DBConn,
    *,
    job_id: int,
    interval_sec: float = DEFAULT_HEARTBEAT_SEC,
) -> AsyncIterator[HeartbeatTask]:
    """``async with`` wrapper that starts/stops a :class:`HeartbeatTask`.

    Stage handlers wrap their main loop with this so the heartbeat
    task is guaranteed to be cancelled on exit (success, failure, or
    pause). The yielded task object is rarely needed; callers usually
    discard it.
    """
    hb = HeartbeatTask(db, job_id=job_id, interval_sec=interval_sec)
    hb.start()
    try:
        yield hb
    finally:
        await hb.stop()
