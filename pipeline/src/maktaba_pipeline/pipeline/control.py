"""Worker-side control plane for pause/cancel/force-pause (Story 6.4).

Two cheap mechanisms cooperate with the API's request flags:

- :func:`should_pause` / :func:`should_cancel` — thin wrappers around
  :func:`maktaba_pipeline.db.jobs_state.read_flags`. The stage handler
  calls these between per-segment commits (architecture §7.7); the
  cost is one PK SELECT < 1 ms warm.
- :class:`ForcePauseListener` — async listener that subscribes to
  ``jobs.force_pause`` (Postgres LISTEN or the in-process bus) and
  resolves a per-job future when the API hits ``?force=true``. The
  stage handler awaits the future as a fast-abort signal so a long
  ffmpeg/STT subprocess can be killed without waiting for the next
  per-segment commit.

The actual subprocess kill chain (SIGTERM → grace → SIGKILL) lives in
the audio decoder context manager (Epic 2 Story 2.3). This module only
delivers the abort signal; the stage handler wires it to its decoder.
"""

from __future__ import annotations

import asyncio
import contextlib
import json

from ..db.jobs import DBConn
from ..db.jobs_state import read_flags
from ..db.pubsub import JOBS_FORCE_PAUSE, PubsubBus, get_bus
from ..log import get_logger

__all__ = [
    "ForcePauseListener",
    "should_cancel",
    "should_pause",
]


_log = get_logger()


async def should_pause(db: DBConn, *, job_id: int) -> bool:
    """True iff the API has set ``pause_requested = true`` for this job."""
    return (await read_flags(db, job_id=job_id)).pause


async def should_cancel(db: DBConn, *, job_id: int) -> bool:
    """True iff the API has set ``cancel_requested = true`` for this job."""
    return (await read_flags(db, job_id=job_id)).cancel


class ForcePauseListener:
    """Subscribe to ``jobs.force_pause`` and resolve per-job futures.

    Stage handlers call :meth:`register` before entering their main
    loop and ``await``-poll the returned future as a non-blocking
    abort check (typically via ``if fut.done(): subprocess.terminate()``).
    On exit (success, failure, or normal pause) the handler calls
    :meth:`unregister` so memory doesn't leak.

    Two transport paths:

    - Postgres: opens a ``LISTEN jobs.force_pause`` via the connection
      wrapper's ``acquire_listener()`` (Story 1.5 contract).
    - SQLite / tests: subscribes to the in-process :class:`PubsubBus`.

    Both paths parse the payload's ``id`` field and resolve the
    matching future.
    """

    def __init__(self, db: DBConn, *, bus: PubsubBus | None = None) -> None:
        self.db = db
        self._bus = bus if bus is not None else get_bus()
        self._registry: dict[int, asyncio.Future[None]] = {}
        self._stop = asyncio.Event()
        self._ready = asyncio.Event()
        self._task: asyncio.Task[None] | None = None

    def register(self, job_id: int) -> asyncio.Future[None]:
        """Start watching for a force-pause notify on ``job_id``.

        Returns a future that resolves with ``None`` when the notify
        arrives. Calling :meth:`register` twice for the same id
        replaces the prior future — the previous waiter is cancelled
        so the stage handler can't accidentally see a stale signal.
        """
        prev = self._registry.get(job_id)
        if prev is not None and not prev.done():
            prev.cancel()
        fut: asyncio.Future[None] = asyncio.get_event_loop().create_future()
        self._registry[job_id] = fut
        return fut

    def unregister(self, job_id: int) -> None:
        """Drop the watcher for ``job_id``. Safe to call multiple times."""
        fut = self._registry.pop(job_id, None)
        if fut is not None and not fut.done():
            fut.cancel()

    def _deliver(self, payload: str) -> None:
        try:
            decoded = json.loads(payload)
            job_id = int(decoded["id"])
        except (ValueError, KeyError, TypeError):
            _log.exception("force_pause_payload_malformed", payload=payload)
            return
        fut = self._registry.get(job_id)
        if fut is not None and not fut.done():
            fut.set_result(None)

    async def _run(self) -> None:
        if getattr(self.db, "dialect", "sqlite") == "postgres":
            await self._run_postgres()
        else:
            await self._run_pubsub()

    async def _run_postgres(self) -> None:
        listener = await self.db.acquire_listener()  # type: ignore[attr-defined]

        def _on_notify(*args: object) -> None:
            payload = args[-1]
            if isinstance(payload, str):
                self._deliver(payload)

        await listener.add_listener(JOBS_FORCE_PAUSE, _on_notify)
        self._ready.set()
        try:
            await self._stop.wait()
        finally:
            with contextlib.suppress(Exception):
                await listener.remove_listener(JOBS_FORCE_PAUSE, _on_notify)

    async def _run_pubsub(self) -> None:
        queue = await self._bus.subscribe(JOBS_FORCE_PAUSE)
        self._ready.set()
        try:
            while not self._stop.is_set():
                get_task = asyncio.create_task(queue.get())
                stop_task = asyncio.create_task(self._stop.wait())
                done, pending = await asyncio.wait(
                    {get_task, stop_task},
                    return_when=asyncio.FIRST_COMPLETED,
                )
                for t in pending:
                    t.cancel()
                if self._stop.is_set():
                    return
                if get_task in done:
                    payload = get_task.result()
                    self._deliver(payload)
        finally:
            self._bus.unsubscribe(JOBS_FORCE_PAUSE, queue)

    def start(self) -> None:
        if self._task is not None:
            raise RuntimeError("ForcePauseListener already started")
        self._task = asyncio.create_task(
            self._run(),
            name="force-pause-listener",
        )

    async def wait_ready(self) -> None:
        """Block until the underlying transport is subscribed.

        Tests call this so a publish doesn't race the listener's
        subscription. Production code rarely needs it; the listener is
        started long before any force-pause notify could arrive.
        """
        await self._ready.wait()

    async def stop(self) -> None:
        self._stop.set()
        if self._task is not None:
            await self._task
            self._task = None
        # Cancel any outstanding waiters so awaiters don't hang forever.
        for fut in list(self._registry.values()):
            if not fut.done():
                fut.cancel()
        self._registry.clear()
