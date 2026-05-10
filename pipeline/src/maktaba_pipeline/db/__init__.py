"""Database layer for the pipeline service.

Houses the typed Python view of the schema landed by the goose
migrations under ``shared/db/migrations``. Each submodule maps to one
foundation table:

- :mod:`maktaba_pipeline.db.jobs` — ``processing_jobs`` (slot 0002,
  Story 6.1).
- :mod:`maktaba_pipeline.db.pubsub` — canonical NOTIFY channel-name
  constants and the in-process PubsubBus shim used in SQLite mode.

The connection wrapper (a thin typed facade over ``asyncpg`` and
``aiosqlite``) lands with Story 1.5; until then this package exposes
the schema-level types and the dialect-agnostic enqueue helper, and
callers wire their own connection.
"""

from __future__ import annotations

from .jobs import (
    LIVE_STATES,
    TERMINAL_STATES,
    DBConn,
    EnqueueResult,
    Job,
    JobState,
    Stage,
    enqueue,
)
from .jobs_claim import claim_one, claim_one_pg, claim_one_sqlite
from .jobs_progress import ProgressTick, tick_heartbeat, tick_progress
from .jobs_reaper import REAPER_ADVISORY_LOCK_KEY, ReapedRow, reap_once
from .jobs_state import (
    FailureOutcome,
    StageError,
    mark_cancelled,
    mark_done,
    mark_failed_or_retry,
    mark_paused,
    read_flags,
    retry_failed,
)
from .pubsub import (
    JOBS_FLAG_SET,
    JOBS_FORCE_PAUSE,
    JOBS_HEARTBEAT,
    JOBS_NEW,
    JOBS_PROGRESS,
    JOBS_REAPED,
    VIDEOS_NEW,
    PubsubBus,
    get_bus,
    reset_bus,
)

__all__ = [
    "JOBS_FLAG_SET",
    "JOBS_FORCE_PAUSE",
    "JOBS_HEARTBEAT",
    "JOBS_NEW",
    "JOBS_PROGRESS",
    "JOBS_REAPED",
    "LIVE_STATES",
    "REAPER_ADVISORY_LOCK_KEY",
    "TERMINAL_STATES",
    "VIDEOS_NEW",
    "DBConn",
    "EnqueueResult",
    "FailureOutcome",
    "Job",
    "JobState",
    "ProgressTick",
    "PubsubBus",
    "ReapedRow",
    "Stage",
    "StageError",
    "claim_one",
    "claim_one_pg",
    "claim_one_sqlite",
    "enqueue",
    "get_bus",
    "mark_cancelled",
    "mark_done",
    "mark_failed_or_retry",
    "mark_paused",
    "read_flags",
    "reap_once",
    "reset_bus",
    "retry_failed",
    "tick_heartbeat",
    "tick_progress",
]
