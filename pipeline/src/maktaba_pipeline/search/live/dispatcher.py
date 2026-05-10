"""Per-transcript debouncing dispatcher for incremental chunk runs.

The dispatcher fans many ``segments.committed`` events down to one
chunk run per transcript per window. Each :meth:`schedule` call
cancels any pending timer for that transcript and arms a fresh
``asyncio.sleep`` for ``window_ms`` milliseconds; when the sleep
elapses the dispatcher invokes the chunker.

A small pause registry lets the orchestrator stop a transcript from
being indexed (e.g. while the user has paused the job). When paused,
:meth:`run` short-circuits and the pending task simply exits.
"""

from __future__ import annotations

import asyncio
import contextlib
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from typing import Any, Protocol

__all__ = ["DispatcherConfig", "IndexerDispatcher"]


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def error(self, event: str, **kwargs: Any) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...


class _Chunker(Protocol):
    async def chunk_for_transcript(self, db: Any, transcript_id: int) -> int: ...


class _FTSWriter(Protocol):
    async def sync_transcript(self, db: Any, transcript_id: int) -> int: ...


@dataclass(slots=True, frozen=True)
class DispatcherConfig:
    """Single knob — the debounce window in milliseconds."""

    window_ms: int = 500


class IndexerDispatcher:
    """Coalesces per-transcript schedule calls into one chunk run.

    The dispatcher maintains a ``dict[int, asyncio.Task]`` keyed by
    transcript id. :meth:`schedule` cancels the existing task (if any)
    and replaces it with a new ``asyncio.sleep`` → :meth:`run`
    pipeline. This means rapid bursts of segment-committed events
    collapse to a single chunk invocation once the burst settles for
    ``window_ms``.

    The dispatcher does not own the supervisor's event loop — it
    creates tasks against the running loop via
    :func:`asyncio.create_task`. Callers must ensure they invoke
    :meth:`schedule` from within an event loop.
    """

    def __init__(
        self,
        *,
        db: _DBConn,
        chunker: _Chunker,
        fts_writer: _FTSWriter | None = None,
        log: _Logger,
        config: DispatcherConfig | None = None,
    ) -> None:
        self._db = db
        self._chunker = chunker
        self._fts_writer = fts_writer
        self._log = log
        self._config = config or DispatcherConfig()
        self._pending: dict[int, asyncio.Task[None]] = {}
        self._paused: set[int] = set()

    def schedule(self, transcript_id: int) -> None:
        """Arm (or rearm) a debounce timer for ``transcript_id``.

        Cancels any pending task for the same transcript and schedules
        a new one. The caller is non-async by design — this is the
        hot path off the pubsub callback and should never block.
        """
        existing = self._pending.get(transcript_id)
        if existing is not None and not existing.done():
            existing.cancel()
        task = asyncio.create_task(self._debounced_run(transcript_id))
        self._pending[transcript_id] = task

    async def _debounced_run(self, transcript_id: int) -> None:
        """Sleep ``window_ms`` then call :meth:`run` if not cancelled."""
        try:
            await asyncio.sleep(self._config.window_ms / 1000.0)
        except asyncio.CancelledError:
            return
        try:
            await self.run(transcript_id)
        except Exception as exc:  # noqa: BLE001 — log-and-continue
            self._log.error(
                "indexer_dispatch_error",
                transcript_id=transcript_id,
                error=repr(exc),
            )
        finally:
            # Clear the slot only if it still points at this task; a
            # follow-up schedule() may have already installed a fresh
            # one.
            current = self._pending.get(transcript_id)
            if current is asyncio.current_task():
                self._pending.pop(transcript_id, None)

    async def run(self, transcript_id: int) -> None:
        """Invoke the chunker for ``transcript_id`` unless paused.

        FTS sync is best-effort: it only runs when an ``fts_writer``
        was wired in (the SQLite path). Postgres relies on the trigger
        installed by the slot 0017 migration and so passes ``None``.
        """
        if transcript_id in self._paused:
            self._log.info("indexer_skip_paused", transcript_id=transcript_id)
            return
        unit_count = await self._chunker.chunk_for_transcript(self._db, transcript_id)
        fts_count = 0
        if self._fts_writer is not None:
            fts_count = await self._fts_writer.sync_transcript(self._db, transcript_id)
        self._log.info(
            "indexer_chunk_done",
            transcript_id=transcript_id,
            units=unit_count,
            fts_synced=fts_count,
        )

    def set_paused(self, transcript_id: int, paused: bool) -> None:
        """Toggle the paused flag for one transcript.

        When paused, :meth:`run` short-circuits. The pending debounce
        task is *not* cancelled — the timer simply expires into a
        skip. This lets the orchestrator flip a transcript paused and
        unpaused without racing against the dispatcher's internal
        task table.
        """
        if paused:
            self._paused.add(transcript_id)
        else:
            self._paused.discard(transcript_id)

    async def shutdown(self) -> None:
        """Cancel every pending task and wait for them to settle.

        Called by the supervisor on stop. The dispatcher is single-
        use after shutdown; callers must build a fresh instance to
        restart.
        """
        tasks = list(self._pending.values())
        for task in tasks:
            task.cancel()
        for task in tasks:
            # We deliberately swallow Exception here as well: any
            # error inside a debounce task has already been logged
            # by _debounced_run; shutdown should not raise.
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await task
        self._pending.clear()
        self._log.info("indexer_dispatcher_shutdown", drained=len(tasks))
