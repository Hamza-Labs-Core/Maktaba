"""Claim-loop runtime — the worker's outer loop.

The :class:`ClaimLoop` glues four pieces together:

1. A :class:`~maktaba_pipeline.pipeline.wakeup.WakeupSource` (LISTEN
   jobs.new on Postgres, the in-process bus on SQLite, or a poll-only
   fallback for tests). Each tick says "try claiming now".
2. The atomic claim from
   :func:`maktaba_pipeline.db.jobs_claim.claim_one`. On a hit the loop
   drains until the queue reports empty so a single notify can carry
   batched enqueues.
3. Per-stage :class:`asyncio.Semaphore` capacity gates. Story 6.7 owns
   the semaphore math; here we only honour the gate so a worker that
   already has its quota in flight doesn't claim more.
4. A user-supplied ``dispatch`` callback that runs the actual stage
   handler. The loop wraps the dispatch with a try/finally that
   releases the semaphore even when the handler raises.

The loop also owns:

- **Exponential backoff** when the queue is empty. Successive empty
  drains double the wakeup poll cadence up to ``claim_poll_max_sec``;
  one successful claim resets the cadence to ``claim_poll_sec``.
  The notify arm fires immediately regardless of the back-off, so
  legitimate enqueues never wait for the timer.
- **Graceful shutdown** via :class:`asyncio.Event`. SIGTERM/SIGINT
  set the event (see :func:`install_signal_handlers`); the loop
  finishes its current iteration, drains in-flight jobs, and exits.
  Story 6.8 layers the deadline-bound grace period on top.

The runner intentionally does *not* own retries (Story 6.5),
heartbeat updates (Story 6.3), pause/resume transitions (Story 6.4),
or reaper sweeping (Story 6.6). Those primitives layer on top via
the same DB connection.
"""

from __future__ import annotations

import asyncio
import os
import signal
import socket
from collections.abc import Awaitable, Callable
from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

from ..db.jobs import Job, Stage
from ..db.jobs_claim import claim_one
from ..log import bind_job_id, get_logger
from .wakeup import PgListenWakeup, PubsubWakeup, WakeupSource

__all__ = [
    "ClaimLoop",
    "WorkerConfig",
    "default_worker_id",
    "install_signal_handlers",
]


_log = get_logger()


def default_worker_id() -> str:
    """Return a stable-per-process worker id of the form ``host/pid/uuid``.

    The hostname locates the worker for operators reading reaper logs;
    the pid disambiguates restarts on the same host; the uuid makes
    the id unique even across rapid restarts (pid reuse is unlikely
    but possible). Story 6.6's reaper compares ``claimed_by`` against
    a fresh ``default_worker_id()`` to recognise dead claims.
    """
    return f"{socket.gethostname()}/{os.getpid()}/{uuid4().hex[:8]}"


@dataclass(slots=True)
class WorkerConfig:
    """Knobs the operator can dial at start-up.

    - ``worker_id`` is logged on every claim so reaper sweeps can
      attribute orphaned rows. Default is :func:`default_worker_id`.
    - ``supported_stages`` is the immutable per-process stage filter.
      A worker started with ``--stages transcribe`` claims only
      ``transcribe`` jobs even when a higher-priority ``index`` job
      is at the front of the queue. Mutating mid-run is unsupported;
      restart the worker.
    - ``claim_poll_sec`` is the safety-net poll cadence. The notify
      path normally wakes the loop sooner; the poll catches dropped
      notifications and bounds worst-case latency.
    - ``claim_poll_max_sec`` caps the exponential back-off so a quiet
      queue still gets checked at least once per ``claim_poll_max_sec``.
    """

    supported_stages: tuple[Stage, ...]
    worker_id: str = field(default_factory=default_worker_id)
    claim_poll_sec: float = 1.0
    claim_poll_max_sec: float = 30.0

    def __post_init__(self) -> None:
        if not self.supported_stages:
            raise ValueError("WorkerConfig.supported_stages must not be empty")
        if self.claim_poll_sec <= 0:
            raise ValueError("claim_poll_sec must be > 0")
        if self.claim_poll_max_sec < self.claim_poll_sec:
            raise ValueError(
                "claim_poll_max_sec must be >= claim_poll_sec",
            )


Dispatch = Callable[[Job], Awaitable[None]]


class ClaimLoop:
    """Drive the wakeup → claim → dispatch cycle for one worker."""

    def __init__(
        self,
        db: Any,
        cfg: WorkerConfig,
        dispatch: Dispatch,
        *,
        semaphores: dict[Stage, asyncio.Semaphore] | None = None,
        shutdown_event: asyncio.Event | None = None,
        wakeup: WakeupSource | None = None,
    ) -> None:
        self.db = db
        self.cfg = cfg
        self.dispatch = dispatch
        self.semaphores = semaphores or {
            stage: asyncio.Semaphore(1) for stage in cfg.supported_stages
        }
        self.shutdown_event = shutdown_event or asyncio.Event()
        self._wakeup: WakeupSource = wakeup or self._default_wakeup()

    def _default_wakeup(self) -> WakeupSource:
        dialect = getattr(self.db, "dialect", "sqlite")
        if dialect == "postgres":
            return PgListenWakeup(self.db, poll_sec=self.cfg.claim_poll_sec)
        return PubsubWakeup(poll_sec=self.cfg.claim_poll_sec)

    async def run(self) -> None:
        """Run until :attr:`shutdown_event` is set.

        After the event fires the loop stops accepting new claims and
        awaits all in-flight dispatch tasks before returning. The
        deadline-bound grace period (Story 6.8) wraps this method
        with ``asyncio.wait_for``.
        """
        _log.info(
            "claim_loop_started",
            worker_id=self.cfg.worker_id,
            stages=[s.value for s in self.cfg.supported_stages],
            poll_sec=self.cfg.claim_poll_sec,
        )
        in_flight: set[asyncio.Task[None]] = set()
        backoff = self.cfg.claim_poll_sec
        try:
            async for _ in self._wakeup.signals():
                if self.shutdown_event.is_set():
                    break
                claimed = await self._drain(in_flight)
                if claimed:
                    backoff = self.cfg.claim_poll_sec
                else:
                    backoff = min(backoff * 2.0, self.cfg.claim_poll_max_sec)
                self._wakeup.poll_sec = backoff
        finally:
            await self._wakeup.aclose()
            if in_flight:
                _log.info(
                    "claim_loop_draining",
                    worker_id=self.cfg.worker_id,
                    in_flight=len(in_flight),
                )
                await asyncio.gather(*in_flight, return_exceptions=True)
            _log.info("claim_loop_stopped", worker_id=self.cfg.worker_id)

    async def _drain(self, in_flight: set[asyncio.Task[None]]) -> int:
        """Claim every eligible row the per-stage caps permit. Return the count."""
        count = 0
        while not self.shutdown_event.is_set():
            eligible = self._stages_with_capacity()
            if not eligible:
                break
            job = await claim_one(
                self.db,
                worker_id=self.cfg.worker_id,
                supported_stages=eligible,
            )
            if job is None:
                break
            count += 1
            sem = self.semaphores[job.stage]
            await sem.acquire()
            task = asyncio.create_task(
                self._run_job(job, sem),
                name=f"job-{job.id}-{job.stage.value}",
            )
            in_flight.add(task)
            task.add_done_callback(in_flight.discard)
        return count

    def _stages_with_capacity(self) -> tuple[Stage, ...]:
        """Stages whose semaphore has at least one free slot.

        Filtering before the claim avoids the wasted UPDATE that would
        return a row this worker can't dispatch. The semaphore's
        internal value count is read non-atomically here — a stage
        can fill between this check and the ``acquire`` below, in
        which case the ``await sem.acquire()`` blocks rather than
        races. That's acceptable: the loop is single-threaded, and
        any blocking acquire just means the next iteration starts
        later.
        """
        return tuple(
            stage
            for stage in self.cfg.supported_stages
            if self._sem_value(self.semaphores.get(stage)) > 0
        )

    @staticmethod
    def _sem_value(sem: asyncio.Semaphore | None) -> int:
        if sem is None:
            return 0
        # asyncio.Semaphore exposes the remaining permit count via
        # ``_value``; there is no public accessor. Story 6.7's wrapper
        # will replace this with a typed property.
        return int(getattr(sem, "_value", 0))

    async def _run_job(self, job: Job, sem: asyncio.Semaphore) -> None:
        try:
            with bind_job_id(str(job.id)):
                _log.info(
                    "job_dispatched",
                    job_id=job.id,
                    stage=job.stage.value,
                    attempts=job.attempts,
                    worker_id=self.cfg.worker_id,
                )
                await self.dispatch(job)
        except Exception:
            # Story 6.5 owns the failure → backoff transition. Here
            # we only log and re-raise so the dispatch contract is
            # preserved; the gather() in run() suppresses the
            # exception so one bad job doesn't kill the loop.
            _log.exception(
                "job_dispatch_raised",
                job_id=job.id,
                stage=job.stage.value,
            )
            raise
        finally:
            sem.release()


def install_signal_handlers(
    shutdown_event: asyncio.Event,
    *,
    loop: asyncio.AbstractEventLoop | None = None,
) -> None:
    """Wire SIGTERM and SIGINT to set ``shutdown_event``.

    Idempotent — re-installing replaces the existing handler. Falls
    back to :func:`signal.signal` on platforms without
    :meth:`asyncio.AbstractEventLoop.add_signal_handler` (Windows).
    Re-entrant signals are coalesced because :class:`asyncio.Event`
    is set-once.
    """
    loop = loop or asyncio.get_event_loop()

    def _trip(sig_name: str) -> None:
        if not shutdown_event.is_set():
            _log.info("shutdown_signal_received", signal=sig_name)
        shutdown_event.set()

    for sig in (signal.SIGTERM, signal.SIGINT):
        try:
            loop.add_signal_handler(sig, _trip, sig.name)
        except (NotImplementedError, RuntimeError):
            # add_signal_handler is unavailable on Windows and inside
            # some embedded interpreters. Fall back to the sync API
            # which still flips the event from a signal handler.
            signal.signal(
                sig,
                lambda *_args, _name=sig.name: _trip(_name),
            )
