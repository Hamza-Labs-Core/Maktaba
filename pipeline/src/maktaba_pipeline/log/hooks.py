"""Process-wide error routes into the structured log.

Without these, three kinds of failure bypass structlog and land (at best) as bare
text on stderr: a stdlib ``logging`` record from a library (grpc, asyncio, aiosqlite,
watchdog), an exception nobody awaited ("Task exception was never retrieved"), and an
exception that kills a thread or the process. :func:`install` routes all three
through the configured structlog chain, so they are redacted, reach the ring buffer
and the sinks, and render like every other line.

Called once by the daemon entry point after :func:`maktaba_pipeline.log.init`;
:func:`asyncio_exception_handler` is set on the running loop by the code that owns
it.
"""

from __future__ import annotations

import asyncio
import logging
import sys
import threading
from types import TracebackType
from typing import Any

import structlog

__all__ = ["StructlogForwarder", "asyncio_exception_handler", "install", "uninstall"]

#: library records below this are dropped: their INFO/DEBUG chatter is not ours
DEFAULT_STDLIB_LEVEL = logging.WARNING


class StructlogForwarder(logging.Handler):
    """A stdlib handler that re-emits each record through structlog (event = the
    formatted message, ``logger`` = the stdlib logger's name)."""

    def emit(self, record: logging.LogRecord) -> None:
        try:
            log = structlog.get_logger()
            kw: dict[str, Any] = {"logger": record.name}
            if record.exc_info and record.exc_info[0] is not None:
                kw["exc_info"] = record.exc_info
            log.log(record.levelno, record.getMessage(), **kw)
        except Exception:  # noqa: BLE001 - a log handler never raises into its caller
            self.handleError(record)


def _excepthook(
    exc_type: type[BaseException],
    exc: BaseException,
    tb: TracebackType | None,
) -> None:
    if issubclass(exc_type, KeyboardInterrupt):
        sys.__excepthook__(exc_type, exc, tb)
        return
    structlog.get_logger().critical(
        "unhandled_exception", source="main", exc_info=(exc_type, exc, tb)
    )


def _thread_excepthook(args: threading.ExceptHookArgs) -> None:
    if args.exc_type is SystemExit:
        return
    structlog.get_logger().error(
        "unhandled_exception",
        source="thread",
        thread=args.thread.name if args.thread is not None else None,
        exc_info=(args.exc_type, args.exc_value, args.exc_traceback),
    )


def asyncio_exception_handler(loop: asyncio.AbstractEventLoop, context: dict[str, Any]) -> None:
    """``loop.set_exception_handler`` target: "Task exception was never retrieved", a
    failing callback, an unclosed transport."""
    exc = context.get("exception")
    task = context.get("task") or context.get("future")
    name = task.get_name() if isinstance(task, asyncio.Task) else None
    kw: dict[str, Any] = {"source": "asyncio", "message": context.get("message"), "task": name}
    if isinstance(exc, BaseException):
        kw["exc_info"] = (type(exc), exc, exc.__traceback__)
    structlog.get_logger().error("unhandled_exception", **kw)


def install(*, stdlib_level: int = DEFAULT_STDLIB_LEVEL) -> None:
    """Route stdlib logging (``stdlib_level`` and up) into structlog and install the
    ``sys`` / ``threading`` exception hooks. Idempotent."""
    global _root_level_before
    root = logging.getLogger()
    if not any(isinstance(h, StructlogForwarder) for h in root.handlers):
        _root_level_before = root.level
        root.addHandler(StructlogForwarder(level=stdlib_level))
    for h in root.handlers:
        if isinstance(h, StructlogForwarder):
            h.setLevel(stdlib_level)
    if root.level == logging.NOTSET or root.level > stdlib_level:
        root.setLevel(stdlib_level)
    sys.excepthook = _excepthook
    threading.excepthook = _thread_excepthook


_root_level_before: int | None = None


def uninstall() -> None:
    """Undo :func:`install` (tests that ran the entry point in-process)."""
    global _root_level_before
    root = logging.getLogger()
    for h in [h for h in root.handlers if isinstance(h, StructlogForwarder)]:
        root.removeHandler(h)
    if _root_level_before is not None:
        root.setLevel(_root_level_before)
        _root_level_before = None
    if sys.excepthook is _excepthook:
        sys.excepthook = sys.__excepthook__
    if threading.excepthook is _thread_excepthook:
        threading.excepthook = threading.__excepthook__
