"""Pipeline runtime — claim loop, wakeup sources, worker plumbing.

Story 6.2 lands the claim loop and the abstractions it sits on:

- :mod:`maktaba_pipeline.pipeline.wakeup` — :class:`WakeupSource`
  Protocol plus three concrete sources (Postgres ``LISTEN jobs.new``,
  the SQLite/in-process bus, and a poll-only fallback used by tests).
- :mod:`maktaba_pipeline.pipeline.runner` — :class:`ClaimLoop`,
  :class:`WorkerConfig`, and the SIGTERM handler that flips the
  shutdown event.

Stories 6.3 (heartbeat/progress), 6.4 (pause/resume/cancel), 6.5
(backoff/retry), 6.6 (reaper), 6.7 (concurrency caps), and 6.8
(graceful shutdown) layer on top of these primitives. The boundary
keeps each story's responsibility narrow: the claim loop only
*delivers* a job; downstream lifecycle is owned elsewhere.
"""

from __future__ import annotations

from .runner import ClaimLoop, WorkerConfig, install_signal_handlers
from .wakeup import (
    PgListenWakeup,
    PollOnlyWakeup,
    PubsubWakeup,
    WakeupSource,
)

__all__ = [
    "ClaimLoop",
    "PgListenWakeup",
    "PollOnlyWakeup",
    "PubsubWakeup",
    "WakeupSource",
    "WorkerConfig",
    "install_signal_handlers",
]
