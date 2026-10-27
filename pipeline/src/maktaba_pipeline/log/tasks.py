"""Background tasks whose failure would otherwise go unseen.

A task the runtime keeps a reference to never reaches asyncio's "exception was never
retrieved" report: its exception sits on the task until shutdown, where it is awaited
under ``suppress(Exception)``. A loop that died an hour ago (the reconciler, the
pruner, a dependency watcher) then leaves no trace while the work it did silently
stops. :func:`watch` logs such an end the moment it happens.
"""

from __future__ import annotations

import asyncio
from typing import Any

from . import get_logger

__all__ = ["log_task_end", "watch"]


def log_task_end(task: asyncio.Task[Any]) -> None:
    """Done callback: ``background_task_failed`` (with the traceback) when the task
    raised. A cancelled task or a clean return is the normal way to stop."""
    if task.cancelled():
        return
    exc = task.exception()  # retrieved: asyncio does not report it again
    if exc is None:
        return
    get_logger().error(
        "background_task_failed",
        task=task.get_name(),
        error=f"{type(exc).__name__}: {exc}",
        exc_info=exc,
    )


def watch[T: asyncio.Task[Any]](task: T) -> T:
    """``task``, with :func:`log_task_end` attached."""
    task.add_done_callback(log_task_end)
    return task
