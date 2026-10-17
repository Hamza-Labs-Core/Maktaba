"""Cross-platform wiring of process shutdown signals onto an asyncio loop.

One helper used by both :func:`maktaba_pipeline.pipeline.runner.install_signal_handlers`
and :meth:`maktaba_pipeline.pipeline.shutdown.ShutdownOrchestrator.install`
so the platform differences live in exactly one place:

- **POSIX (Linux, macOS)**: ``loop.add_signal_handler`` for ``SIGTERM``
  and ``SIGINT``. The callback runs on the loop, never inside the raw
  signal handler.
- **Windows**: the Proactor loop has no ``add_signal_handler``
  (``NotImplementedError``). We fall back to :func:`signal.signal` for
  ``SIGINT`` (Ctrl-C), ``SIGBREAK`` (Ctrl-Break and console close) and
  ``SIGTERM`` (only reachable via :func:`signal.raise_signal` — Windows
  has no external SIGTERM; ``os.kill(pid, SIGTERM)`` is
  ``TerminateProcess``). The raw handler hands the callback to the loop
  with :meth:`~asyncio.AbstractEventLoop.call_soon_threadsafe`, which
  also wakes a loop blocked in its selector/IOCP wait.

Signals that do not exist on the host are skipped, and a failure to
install one (e.g. :func:`signal.signal` called off the main thread)
is logged and skipped — installation never raises and never blocks.
"""

from __future__ import annotations

import asyncio
import contextlib
import signal
from collections.abc import Callable
from types import FrameType
from typing import Any

from ..log import get_logger

__all__ = [
    "SHUTDOWN_SIGNAL_NAMES",
    "install_shutdown_handlers",
    "shutdown_signals",
]


_log = get_logger()


SHUTDOWN_SIGNAL_NAMES: tuple[str, ...] = ("SIGTERM", "SIGINT", "SIGBREAK")
"""Candidate shutdown signals, in install order.

``SIGBREAK`` exists only on Windows, so on POSIX the effective set is
exactly ``(SIGTERM, SIGINT)``.
"""


SignalCallback = Callable[[signal.Signals], None]
_RawHandler = Callable[[int, FrameType | None], Any] | int | None


def shutdown_signals() -> tuple[signal.Signals, ...]:
    """The shutdown signals that exist on this platform."""
    out: list[signal.Signals] = []
    for name in SHUTDOWN_SIGNAL_NAMES:
        sig = getattr(signal, name, None)
        if isinstance(sig, signal.Signals):
            out.append(sig)
    return tuple(out)


def install_shutdown_handlers(
    loop: asyncio.AbstractEventLoop,
    callback: SignalCallback,
) -> Callable[[], None]:
    """Route every :func:`shutdown_signals` signal to ``callback`` on ``loop``.

    ``callback`` always runs on the loop thread, on every platform.

    Returns an ``uninstall`` callable that restores the previous
    handlers; it is safe to call more than once.
    """
    restore: list[Callable[[], None]] = []

    for sig in shutdown_signals():
        try:
            loop.add_signal_handler(sig, callback, sig)
        except (NotImplementedError, RuntimeError, ValueError):
            pass
        else:
            restore.append(_loop_restorer(loop, sig))
            continue

        def _raw(_signum: int, _frame: FrameType | None, _sig: signal.Signals = sig) -> None:
            try:
                loop.call_soon_threadsafe(callback, _sig)
            except RuntimeError:
                # Loop already closed: run inline rather than drop it.
                callback(_sig)

        try:
            previous = signal.signal(sig, _raw)
        except (ValueError, OSError, RuntimeError) as exc:
            # Not the main thread, or the platform refuses this signal.
            _log.warning("shutdown_signal_not_installed", signal=sig.name, error=str(exc))
            continue
        restore.append(_signal_restorer(sig, previous))

    def uninstall() -> None:
        while restore:
            restore.pop()()

    return uninstall


def _loop_restorer(loop: asyncio.AbstractEventLoop, sig: signal.Signals) -> Callable[[], None]:
    def _restore() -> None:
        with contextlib.suppress(NotImplementedError, RuntimeError, ValueError):
            loop.remove_signal_handler(sig)

    return _restore


def _signal_restorer(sig: signal.Signals, previous: _RawHandler) -> Callable[[], None]:
    def _restore() -> None:
        with contextlib.suppress(ValueError, OSError, RuntimeError):
            signal.signal(sig, previous if previous is not None else signal.SIG_DFL)

    return _restore
