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
from collections.abc import Awaitable, Callable, Iterator, Mapping, Sequence
from contextlib import ExitStack, contextmanager
from dataclasses import dataclass, field
from typing import Any
from uuid import uuid4

import structlog

from ..db import claim_owner
from ..db.jobs import Job, Stage
from ..db.jobs_claim import claim_one
from ..engine.claim import PoolConfig, claim_for_pool, load_pools
from ..log import bind_job_id, bind_video_id, get_logger
from .signals import install_shutdown_handlers
from .wakeup import PgListenWakeup, PubsubWakeup, WakeupSource

__all__ = [
    "ClaimLoop",
    "WorkerConfig",
    "default_worker_id",
    "install_signal_handlers",
]


_log = get_logger()


@contextmanager
def _job_log_context(job: Job) -> Iterator[None]:
    """Bind the job's id, stage and subject onto every log line its dispatch writes
    (the handlers, the engine and the libraries they call log without passing them)."""
    with ExitStack() as stack:
        stack.enter_context(bind_job_id(str(job.id)))
        stack.enter_context(structlog.contextvars.bound_contextvars(stage=str(job.stage)))
        if job.video_id is not None:
            stack.enter_context(bind_video_id(str(job.video_id)))
        if job.subject_id is not None and job.subject_id != job.video_id:
            stack.enter_context(
                structlog.contextvars.bound_contextvars(
                    subject_kind=job.subject_kind, subject_id=str(job.subject_id)
                )
            )
        yield


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

    supported_stages: tuple[Stage | str, ...]
    worker_id: str = field(default_factory=default_worker_id)
    claim_poll_sec: float = 1.0
    claim_poll_max_sec: float = 30.0
    #: On shutdown, how long the jobs in flight may take to finish before they are
    #: cancelled (they end as interrupted and are claimed again). Bounded so the worker
    #: exits inside its supervisor's stop grace (maktaba-server: 30 s; the index worker
    #: gets a longer drain and grace, MAKTABA_PIPELINE_DRAIN_SEC). A job cancelled
    #: during a search index write ends once the write did (search/gate.py).
    shutdown_drain_sec: float = 15.0
    #: Pool mode (plan 06-11 section 5): claim per resource pool through
    #: ``engine.claim.claim_for_pool``. ``stage_pools`` maps every supported
    #: stage to its pool. Off by default so the original per-stage loop (and its
    #: tests) keep working; the daemon turns it on.
    pool_mode: bool = False
    stage_pools: Mapping[str, str] = field(default_factory=dict)
    #: Pool mode only: the stages to claim, read on every drain (Epic 31 design section
    #: 5: the served set changes while the worker runs, and may be empty for a while, for
    #: example while draining). ``stage_pools`` must then cover every stage it can return.
    dynamic_stages: Callable[[], Sequence[str]] | None = None

    def __post_init__(self) -> None:
        if not self.supported_stages and self.dynamic_stages is None:
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
        semaphores: dict[Any, asyncio.Semaphore] | None = None,
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
        self._pools: dict[str, PoolConfig] = {}
        self._pool_sems: dict[str, asyncio.Semaphore] = {}
        self._pool_sem_size: dict[str, int] = {}
        self._pool_inflight: dict[str, int] = {}
        #: the jobs this loop dispatched that have not ended
        self.in_flight: set[asyncio.Task[None]] = set()
        self._drain_failed_logged = False

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
            stages=[str(s) for s in self.cfg.supported_stages],
            poll_sec=self.cfg.claim_poll_sec,
        )
        in_flight = self.in_flight
        backoff = self.cfg.claim_poll_sec
        # the wakeup waits up to its poll (backed off to claim_poll_max_sec on a quiet
        # queue): a shutdown ends that wait at once, not at the next tick
        stopper = asyncio.create_task(self._close_wakeup_on_shutdown(), name="claim-stop")
        try:
            async for _ in self._wakeup.signals():
                if self.shutdown_event.is_set():
                    break
                try:
                    claimed = await self._drain(in_flight)
                except Exception as exc:  # noqa: BLE001 - the database went away: next tick
                    # a node's beat notices the home gone and closes the gate; until it
                    # comes back each tick fails here, backing off like an empty one
                    if not self._drain_failed_logged:
                        self._drain_failed_logged = True
                        _log.warning(
                            "claim_loop_drain_failed",
                            error=f"{type(exc).__name__}: {exc}",
                            exc_info=True,
                        )
                    claimed = 0
                else:
                    if self._drain_failed_logged:
                        self._drain_failed_logged = False
                        _log.info("claim_loop_drain_recovered")
                if claimed:
                    backoff = self.cfg.claim_poll_sec
                else:
                    backoff = min(backoff * 2.0, self.cfg.claim_poll_max_sec)
                self._wakeup.poll_sec = backoff
        finally:
            stopper.cancel()
            await self._wakeup.aclose()
            if in_flight:
                _log.info(
                    "claim_loop_draining",
                    worker_id=self.cfg.worker_id,
                    in_flight=len(in_flight),
                )
                _, pending = await asyncio.wait(in_flight, timeout=self.cfg.shutdown_drain_sec)
                if pending:
                    _log.warning(
                        "claim_loop_drain_timeout",
                        worker_id=self.cfg.worker_id,
                        cancelled=self.stop_in_flight(),
                        after_sec=self.cfg.shutdown_drain_sec,
                    )
                await asyncio.gather(*in_flight, return_exceptions=True)
            _log.info("claim_loop_stopped", worker_id=self.cfg.worker_id)

    async def _close_wakeup_on_shutdown(self) -> None:
        await self.shutdown_event.wait()
        await self._wakeup.aclose()

    def stop_in_flight(self) -> int:
        """Cancel every job in flight (they end as interrupted, writing nothing); the
        number cancelled. A node does it when the home has been gone close to the claim
        lease: past it the home hands those jobs to another machine."""
        tasks = [t for t in self.in_flight if not t.done()]
        for task in tasks:
            task.cancel()
        return len(tasks)

    #: Pools are visited in a fixed order so a slow pool cannot starve the others.
    POOL_ORDER: tuple[str, ...] = (
        "gpu",
        "nas_read",
        "network_plex",
        "network_tv",
        "network_yt",
        "cpu",
    )

    async def reload_pools(self) -> None:
        """Re-read ``worker_pools``; resize per-worker semaphores whose size changed.

        A resized semaphore is *replaced* (jobs in flight release into the one
        they acquired), sized so that in-flight plus free slots equals the new size.
        """
        self._pools = await load_pools(self.db)
        for name, pool in self._pools.items():
            if pool.scope != "per_worker":
                continue
            size = pool.effective_size
            if self._pool_sem_size.get(name) == size and name in self._pool_sems:
                continue
            free = max(0, size - self._pool_inflight.get(name, 0))
            self._pool_sems[name] = asyncio.Semaphore(free)
            self._pool_sem_size[name] = size

    def _current_stages(self) -> tuple[Stage | str, ...]:
        if self.cfg.dynamic_stages is not None:
            return tuple(self.cfg.dynamic_stages())
        return self.cfg.supported_stages

    def _pool_order(self, stages: Sequence[Stage | str] | None = None) -> list[str]:
        if stages is None:
            stages = self._current_stages()
        used = {self.cfg.stage_pools.get(str(s)) for s in stages}
        known = [p for p in self.POOL_ORDER if p in used]
        extra = sorted(p for p in used if p is not None and p not in self.POOL_ORDER)
        return known + extra

    async def _drain_pools(self, in_flight: set[asyncio.Task[None]]) -> int:
        current = self._current_stages()
        if not current:
            return 0
        await self.reload_pools()
        count = 0
        for pool_name in self._pool_order(current):
            pool = self._pools.get(pool_name)
            if pool is None:
                continue
            stages = [str(s) for s in current if self.cfg.stage_pools.get(str(s)) == pool_name]
            # at most the pool's size per pass, then the next pool: jobs that end faster
            # than a claim takes (probes) kept a slot free at every claim, and the loop
            # never left their pool (no Plex, link or tag job ran for as long as probes
            # were queued)
            taken = 0
            while not self.shutdown_event.is_set() and taken < pool.effective_size:
                sem = self._pool_sems.get(pool_name) if pool.scope == "per_worker" else None
                if pool.scope == "per_worker" and (sem is None or self._sem_value(sem) <= 0):
                    break
                job = await claim_for_pool(
                    self.db,
                    worker_id=self.cfg.worker_id,
                    pool=pool_name,
                    stages=stages,
                    scope=pool.scope,
                )
                if job is None:
                    break
                count += 1
                taken += 1
                if sem is not None:
                    await sem.acquire()
                self._pool_inflight[pool_name] = self._pool_inflight.get(pool_name, 0) + 1
                task = asyncio.create_task(
                    self._run_pool_job(job, sem, pool_name),
                    name=f"job-{job.id}-{job.stage}",
                )
                in_flight.add(task)
                task.add_done_callback(in_flight.discard)
        return count

    async def _run_pool_job(self, job: Job, sem: asyncio.Semaphore | None, pool_name: str) -> None:
        try:
            await self._dispatch_job(job)
        finally:
            self._pool_inflight[pool_name] = max(0, self._pool_inflight.get(pool_name, 1) - 1)
            if sem is not None:
                sem.release()

    async def _dispatch_job(self, job: Job) -> None:
        try:
            # the job's writes apply while this worker holds its claim (db/claim_owner.py)
            with _job_log_context(job), claim_owner.running_for(self.cfg.worker_id):
                _log.info(
                    "job_dispatched",
                    job_id=job.id,
                    stage=str(job.stage),
                    attempts=job.attempts,
                    worker_id=self.cfg.worker_id,
                )
                await self.dispatch(job)
        except Exception:
            _log.exception("job_dispatch_raised", job_id=job.id, stage=str(job.stage))
            raise

    async def _drain(self, in_flight: set[asyncio.Task[None]]) -> int:
        """Claim every eligible row the capacity caps permit. Return the count."""
        if self.cfg.pool_mode:
            return await self._drain_pools(in_flight)
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
                name=f"job-{job.id}-{job.stage}",
            )
            in_flight.add(task)
            task.add_done_callback(in_flight.discard)
        return count

    def _stages_with_capacity(self) -> tuple[Stage | str, ...]:
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
            with _job_log_context(job), claim_owner.running_for(self.cfg.worker_id):
                _log.info(
                    "job_dispatched",
                    job_id=job.id,
                    stage=str(job.stage),
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
                stage=str(job.stage),
            )
            raise
        finally:
            sem.release()


def install_signal_handlers(
    shutdown_event: asyncio.Event,
    *,
    loop: asyncio.AbstractEventLoop | None = None,
) -> Callable[[], None]:
    """Wire the platform's shutdown signals to set ``shutdown_event``.

    SIGTERM and SIGINT on POSIX; SIGINT, SIGBREAK and SIGTERM on
    Windows, where :meth:`asyncio.AbstractEventLoop.add_signal_handler`
    is unavailable and :func:`signal.signal` is used instead (see
    :mod:`maktaba_pipeline.pipeline.signals`). Idempotent — re-installing
    replaces the existing handler. Re-entrant signals are coalesced
    because :class:`asyncio.Event` is set-once. Returns a callable that
    restores the previous handlers.
    """
    loop = loop or asyncio.get_event_loop()

    def _trip(sig: signal.Signals) -> None:
        if not shutdown_event.is_set():
            _log.info("shutdown_signal_received", signal=sig.name)
        shutdown_event.set()

    return install_shutdown_handlers(loop, _trip)
