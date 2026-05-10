"""Dispatcher debouncing — multiple schedule() calls coalesce to one run.

These tests use a fake chunker that records every call. The dispatcher
should drop the older debounce task each time ``schedule`` is called
again before the window elapses, so a tight burst produces exactly
one chunk invocation.
"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
from typing import Any

import pytest

from maktaba_pipeline.search.live.dispatcher import (
    DispatcherConfig,
    IndexerDispatcher,
)


class _FakeLogger:
    def __init__(self) -> None:
        self.events: list[tuple[str, dict[str, Any]]] = []

    def info(self, event: str, **kwargs: Any) -> None:
        self.events.append((event, kwargs))

    def warning(self, event: str, **kwargs: Any) -> None:
        self.events.append((event, kwargs))

    def error(self, event: str, **kwargs: Any) -> None:
        self.events.append((event, kwargs))


class _FakeDB:
    dialect = "sqlite"

    @asynccontextmanager
    async def transaction(self):  # type: ignore[no-untyped-def]
        yield self


class _CountingChunker:
    def __init__(self) -> None:
        self.calls: list[int] = []

    async def chunk_for_transcript(self, db: Any, transcript_id: int) -> int:
        self.calls.append(transcript_id)
        return 1


@pytest.mark.asyncio
async def test_debounce_coalesces_burst_into_one_run() -> None:
    chunker = _CountingChunker()
    dispatcher = IndexerDispatcher(
        db=_FakeDB(),
        chunker=chunker,
        log=_FakeLogger(),
        config=DispatcherConfig(window_ms=50),
    )

    # Five tight schedule() calls within the window. Only the last
    # should land a chunk run.
    for _ in range(5):
        dispatcher.schedule(transcript_id=42)
        await asyncio.sleep(0.005)

    # Wait long enough for the debounce window to lapse.
    await asyncio.sleep(0.2)
    await dispatcher.shutdown()

    assert chunker.calls == [42], f"expected one call to transcript 42, got {chunker.calls}"


@pytest.mark.asyncio
async def test_different_transcripts_run_independently() -> None:
    chunker = _CountingChunker()
    dispatcher = IndexerDispatcher(
        db=_FakeDB(),
        chunker=chunker,
        log=_FakeLogger(),
        config=DispatcherConfig(window_ms=30),
    )

    dispatcher.schedule(1)
    dispatcher.schedule(2)
    dispatcher.schedule(3)

    await asyncio.sleep(0.15)
    await dispatcher.shutdown()

    assert sorted(chunker.calls) == [1, 2, 3]


@pytest.mark.asyncio
async def test_paused_transcript_is_skipped() -> None:
    chunker = _CountingChunker()
    dispatcher = IndexerDispatcher(
        db=_FakeDB(),
        chunker=chunker,
        log=_FakeLogger(),
        config=DispatcherConfig(window_ms=20),
    )

    dispatcher.set_paused(7, True)
    dispatcher.schedule(7)
    await asyncio.sleep(0.1)
    await dispatcher.shutdown()

    assert chunker.calls == []


@pytest.mark.asyncio
async def test_shutdown_cancels_pending_tasks() -> None:
    chunker = _CountingChunker()
    dispatcher = IndexerDispatcher(
        db=_FakeDB(),
        chunker=chunker,
        log=_FakeLogger(),
        config=DispatcherConfig(window_ms=500),
    )

    dispatcher.schedule(99)
    # Shutdown before the window elapses — task should be cancelled
    # and never invoke the chunker.
    await dispatcher.shutdown()
    await asyncio.sleep(0.1)

    assert chunker.calls == []
