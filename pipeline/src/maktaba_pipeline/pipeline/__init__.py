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

from .backoff import BASE_SEC, CAP_SEC, JITTER_FRAC, compute_backoff
from .concurrency import (
    DEFAULT_CONCURRENCY,
    GPU_STAGES,
    ConcurrencyManager,
    Reservation,
)
from .control import ForcePauseListener, should_cancel, should_pause
from .devices import DeviceID, enumerate_devices
from .heartbeat import DEFAULT_HEARTBEAT_SEC, HeartbeatTask, heartbeat_for
from .reaper import (
    DEFAULT_REAPER_INTERVAL_SEC,
    DEFAULT_STALE_CLAIM_SEC,
    STALE_TO_HEARTBEAT_RATIO,
    Reaper,
)
from .runner import ClaimLoop, WorkerConfig, install_signal_handlers
from .shutdown import DEFAULT_SHUTDOWN_GRACE_SEC, ShutdownOrchestrator
from .wakeup import (
    PgListenWakeup,
    PollOnlyWakeup,
    PubsubWakeup,
    WakeupSource,
)

__all__ = [
    "BASE_SEC",
    "CAP_SEC",
    "DEFAULT_CONCURRENCY",
    "DEFAULT_HEARTBEAT_SEC",
    "DEFAULT_REAPER_INTERVAL_SEC",
    "DEFAULT_SHUTDOWN_GRACE_SEC",
    "DEFAULT_STALE_CLAIM_SEC",
    "GPU_STAGES",
    "JITTER_FRAC",
    "STALE_TO_HEARTBEAT_RATIO",
    "ClaimLoop",
    "ConcurrencyManager",
    "DeviceID",
    "ForcePauseListener",
    "HeartbeatTask",
    "PgListenWakeup",
    "PollOnlyWakeup",
    "PubsubWakeup",
    "Reaper",
    "Reservation",
    "ShutdownOrchestrator",
    "WakeupSource",
    "WorkerConfig",
    "compute_backoff",
    "enumerate_devices",
    "heartbeat_for",
    "install_signal_handlers",
    "should_cancel",
    "should_pause",
]
