"""Long-running supervisor that turns segment-committed events into chunk runs.

The supervisor is the only thing in the live-indexing path that owns
process-wide state: a single task subscribed to the
``segments.committed`` pubsub channel (or its Postgres LISTEN
equivalent) plus an optional catch-up pass that re-indexes any
transcripts whose ``last_indexed_segment_seq`` lags the latest
committed segment.

Production wiring depends on whichever pubsub abstraction the runner
hands in; this module stays driver-agnostic by exposing an explicit
:meth:`IndexerSupervisor.on_segment_committed` entry point so the
runner can pump events into it from either ``asyncpg.add_listener``
or :class:`maktaba_pipeline.db.pubsub.PubsubBus`.
"""

from __future__ import annotations

import asyncio
import contextlib
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from typing import Any, Protocol

__all__ = ["IndexerSupervisor", "SupervisorConfig"]


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def error(self, event: str, **kwargs: Any) -> Any: ...


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...


class _Dispatcher(Protocol):
    def schedule(self, transcript_id: int) -> None: ...

    async def shutdown(self) -> None: ...


class _Chunker(Protocol):
    async def chunk_for_transcript(self, db: Any, transcript_id: int) -> int: ...


@dataclass(slots=True, frozen=True)
class SupervisorConfig:
    """Knobs that govern catch-up behavior and debounce window.

    ``catch_up_on_start`` controls whether :meth:`IndexerSupervisor.start`
    runs the gap-fill pass before subscribing. ``debounce_ms`` is the
    window the dispatcher coalesces schedule() calls inside; the
    supervisor itself does not implement debouncing, but exposes the
    setting here so the runner can wire it through once.
    """

    catch_up_on_start: bool = True
    debounce_ms: int = 500


_CATCH_UP_SQL = """
SELECT t.id AS id
  FROM transcripts t
  LEFT JOIN (
    SELECT transcript_id, MAX(seq) AS max_seq
      FROM transcript_segments
     GROUP BY transcript_id
  ) s ON s.transcript_id = t.id
 WHERE t.state = 'running'
    OR COALESCE(t.last_indexed_segment_seq, -1) < COALESCE(s.max_seq, -1)
"""


class IndexerSupervisor:
    """Single owner of the live-indexing event loop.

    Responsibilities:

    1. On :meth:`start`, optionally walk the transcripts table and
       schedule any transcript whose ``last_indexed_segment_seq``
       trails its newest segment (catch-up).
    2. After catch-up, register a handler with the pubsub bus so
       inserts on ``transcript_segments`` route into the dispatcher.
    3. On :meth:`stop`, cancel the subscription task and drain the
       dispatcher's pending debounces.

    The supervisor is not responsible for chunking itself; it owns
    only the routing. Each scheduled run lands in the dispatcher which
    debounces per-transcript and ultimately calls the chunker.
    """

    def __init__(
        self,
        *,
        db: _DBConn,
        dispatcher: _Dispatcher,
        chunker: _Chunker,
        log: _Logger,
        config: SupervisorConfig | None = None,
    ) -> None:
        self._db = db
        self._dispatcher = dispatcher
        self._chunker = chunker
        self._log = log
        self._config = config or SupervisorConfig()
        self._subscriber_task: asyncio.Task[None] | None = None
        self._stopped = asyncio.Event()

    async def start(self) -> None:
        """Run catch-up (optional) and subscribe to the segments channel.

        The catch-up pass is best-effort: it logs and continues on any
        per-transcript error so a single bad row cannot block the
        whole live path from coming online.
        """
        if self._config.catch_up_on_start:
            await self.catch_up()
        self._log.info(
            "indexer_supervisor_started",
            catch_up=self._config.catch_up_on_start,
            debounce_ms=self._config.debounce_ms,
        )

    async def catch_up(self) -> int:
        """Schedule chunk runs for every lagging transcript.

        Returns the number of transcripts scheduled. The actual work
        happens asynchronously in the dispatcher; this method only
        primes the queue.
        """
        rows = await self._db.fetch(_CATCH_UP_SQL)
        scheduled = 0
        for row in rows:
            try:
                tid = int(row["id"])
            except (KeyError, TypeError, ValueError):
                self._log.warning("catch_up_bad_row", row=repr(row))
                continue
            self._dispatcher.schedule(tid)
            scheduled += 1
        self._log.info("indexer_catch_up", scheduled=scheduled)
        return scheduled

    def on_segment_committed(self, transcript_id: int) -> None:
        """Public entry point for the runner's pubsub adapter.

        The runner subscribes to ``segments.committed`` (Postgres
        LISTEN or :class:`PubsubBus`) and forwards each event here.
        Routing is synchronous — the dispatcher itself owns any
        async/debounce machinery.
        """
        self._dispatcher.schedule(transcript_id)

    async def stop(self) -> None:
        """Cancel the subscriber task and drain pending dispatches."""
        if self._subscriber_task is not None:
            self._subscriber_task.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._subscriber_task
            self._subscriber_task = None
        await self._dispatcher.shutdown()
        self._stopped.set()
        self._log.info("indexer_supervisor_stopped")
