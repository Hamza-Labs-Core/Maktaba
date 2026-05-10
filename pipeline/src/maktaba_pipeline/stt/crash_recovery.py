"""Story 3.8 — graceful shutdown + reaper integration for transcribe.

Two surfaces:

- :class:`ShutdownPolicy` — signals + grace period for the running
  worker. SIGTERM/SIGINT count as ``pause_requested = true`` for every
  in-flight transcribe job; the worker commits its current segment
  and flips the row to ``paused`` with ``paused_reason = 'shutdown'``.
  A second signal forces ``hard_exit_after_sec`` (default 5 s).
- :func:`reaper_predicate` — pure SQL fragment for the existing reaper
  (Epic 6 Story 6.6). It selects rows whose ``last_heartbeat_at <
  now() - stale_claim_sec`` AND state is in the in-flight set. The
  fragment is used by the reaper job (already wired in
  :mod:`maktaba_pipeline.db.jobs_reaper`); we surface it here so the
  Story 3.8 invariant is documented near the transcribe code that
  cares.

The crash-recovery contract: no segment row is partially committed,
because :func:`maktaba_pipeline.stt.segment_commit.commit_segment`
issues a single transaction. On crash, the resume restarts from
``last_segment_end_sec``; verified by the chaos test in
``tests/stt/test_crash_recovery.py``.
"""

from __future__ import annotations

import asyncio
import signal
from collections.abc import Callable
from contextlib import suppress
from dataclasses import dataclass

__all__ = [
    "DEFAULT_GRACE_SEC",
    "DEFAULT_HARD_EXIT_AFTER_SEC",
    "DEFAULT_STALE_CLAIM_SEC",
    "REAPER_STALE_PREDICATE",
    "ShutdownPolicy",
    "install_shutdown_handlers",
]


DEFAULT_GRACE_SEC = 120.0
DEFAULT_HARD_EXIT_AFTER_SEC = 5.0
DEFAULT_STALE_CLAIM_SEC = 90.0


# Reaper SQL fragment — the existing reaper module has its own version;
# duplicating the literal here lets the Story 3.8 invariant live next
# to the transcribe code that depends on it. CI's grep guard fails the
# build if the two strings diverge.
REAPER_STALE_PREDICATE = (
    "last_heartbeat_at IS NOT NULL "
    "AND last_heartbeat_at < (now() - make_interval(secs => $1)) "
    "AND state IN ('claimed','running','resuming')"
)


@dataclass(slots=True)
class ShutdownPolicy:
    """Knobs for the worker's signal handler.

    - ``grace_sec`` — total budget after the first SIGTERM for in-flight
      segments to commit and the row to flip to ``paused``.
    - ``hard_exit_after_sec`` — second SIGTERM aborts within this
      budget; matches Story 3.8 AC-2.
    - ``stale_claim_sec`` — heartbeat staleness threshold; the reaper
      flips affected rows to ``paused`` with ``paused_reason='crash'``.
    """

    grace_sec: float = DEFAULT_GRACE_SEC
    hard_exit_after_sec: float = DEFAULT_HARD_EXIT_AFTER_SEC
    stale_claim_sec: float = DEFAULT_STALE_CLAIM_SEC


def install_shutdown_handlers(
    on_first: Callable[[], asyncio.Future[None] | asyncio.Task[None] | None],
    on_second: Callable[[], None],
    *,
    loop: asyncio.AbstractEventLoop | None = None,
) -> Callable[[], None]:
    """Wire SIGTERM/SIGINT to the two-stage shutdown protocol.

    Returns a callable that uninstalls the handlers (used by tests).
    The first signal calls ``on_first`` (which should request pause
    on every in-flight job and start the grace clock); the second
    signal calls ``on_second`` (which should call ``sys.exit`` after
    the hard-exit grace).
    """
    loop = loop or asyncio.get_event_loop()
    fired = {"count": 0}

    def _handler() -> None:
        fired["count"] += 1
        if fired["count"] == 1:
            ret = on_first()
            if asyncio.iscoroutine(ret):
                loop.create_task(ret)
            return
        on_second()

    handlers = []
    for sig in (signal.SIGTERM, signal.SIGINT):
        with suppress(NotImplementedError):
            loop.add_signal_handler(sig, _handler)
            handlers.append(sig)

    def _uninstall() -> None:
        for sig in handlers:
            with suppress(NotImplementedError):
                loop.remove_signal_handler(sig)

    return _uninstall
