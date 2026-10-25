"""The search index's writes in this process: one at a time, never cut short.

Chroma keeps its index in files a write updates in several steps; a process killed in
the middle leaves them unusable ("Failed to apply logs to the hnsw segment writer",
"Error constructing hnsw segment"). It happened when the index worker stopped with 27
index jobs in flight: their writes ran together in threads, the drain cancelled them,
the threads kept writing and the supervisor killed the process.

So every write (:func:`write`) runs in a thread under one lock, and a job cancelled
while its write runs waits for that write before it ends: the claim loop's drain, and
the process's exit after it, wait for the write. Once the worker stops
(:func:`close`) no write starts: a job that asks for one ends as interrupted (it is
claimed again). Reads (:func:`read`) do not take the lock, a kill cannot break a read;
they are refused after :func:`close` too, as the clients are closed then.
"""

from __future__ import annotations

import asyncio
import threading
import weakref
from collections.abc import Callable
from typing import Any

from ..log import get_logger

__all__ = ["IndexClosed", "close", "is_closed", "read", "reopen", "wait_idle", "write"]

_log = get_logger()

_write_lock = threading.Lock()
_closed = threading.Event()
#: the asyncio side of the lock, so waiting jobs queue on the loop, not in threads
#: (a cancelled waiter then ends at once, having written nothing)
_queues: weakref.WeakKeyDictionary[asyncio.AbstractEventLoop, asyncio.Lock] = (
    weakref.WeakKeyDictionary()
)


class IndexClosed(asyncio.CancelledError):
    """The worker is stopping: no new write to the search index. A cancellation, so the
    job ends as interrupted and is claimed again, not as failed."""


def is_closed() -> bool:
    return _closed.is_set()


def close() -> None:
    """No write starts from now on (the write in progress finishes)."""
    if not _closed.is_set():
        _closed.set()
        _log.info("search_index_closing")


def reopen() -> None:
    """Tests: writes allowed again."""
    _closed.clear()


def _queue() -> asyncio.Lock:
    loop = asyncio.get_running_loop()
    lock = _queues.get(loop)
    if lock is None:
        lock = _queues[loop] = asyncio.Lock()
    return lock


_REFUSED: Any = object()


def _locked[T](fn: Callable[..., T], *args: Any, **kwargs: Any) -> Any:
    with _write_lock:
        if _closed.is_set():
            return _REFUSED  # closed while this write waited for the thread
        return fn(*args, **kwargs)


async def write[T](fn: Callable[..., T], *args: Any, **kwargs: Any) -> T:
    """Run ``fn`` (a Chroma write: delete, upsert) in a thread, alone; it is not cut
    short: a cancellation while it runs takes effect once it returned."""
    if _closed.is_set():
        raise IndexClosed("the search index is closing")
    async with _queue():
        if _closed.is_set():
            raise IndexClosed("the search index is closing")
        fut = asyncio.ensure_future(asyncio.to_thread(_locked, fn, *args, **kwargs))
        cancelled = False
        while not fut.done():
            try:
                await asyncio.shield(fut)
            except asyncio.CancelledError:
                cancelled = True  # the write goes on (or just ended): wait, then end
        if cancelled:
            raise asyncio.CancelledError
        result = fut.result()  # its own exception, when it raised one
        if result is _REFUSED:
            raise IndexClosed("the search index is closing")
        return result  # type: ignore[no-any-return]


async def read[T](fn: Callable[..., T], *args: Any, **kwargs: Any) -> T:
    """Run ``fn`` (a Chroma read: get, query) in a thread; refused once closed."""
    if _closed.is_set():
        raise IndexClosed("the search index is closing")
    return await asyncio.to_thread(fn, *args, **kwargs)


def wait_idle(timeout: float) -> bool:
    """Wait up to ``timeout`` s for the write in progress; False when it did not end."""
    if not _write_lock.acquire(timeout=max(0.0, timeout)):
        return False
    _write_lock.release()
    return True
