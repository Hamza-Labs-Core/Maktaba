"""Typed view of ``processing_jobs`` plus the ``enqueue`` helper.

The schema is owned by ``shared/db/migrations/0002_processing_jobs.sql``
(Story 6.1). This module exposes:

- :class:`Stage` and :class:`JobState` enums — single source of truth
  for the values the migration's CHECK constraints accept.
- :class:`Job` and :class:`EnqueueResult` dataclasses — the shape the
  rest of the pipeline reads back.
- :func:`enqueue` — dialect-agnostic insert that returns the existing
  live row's id when the unique partial index swallows the INSERT, or
  skips when a ``done`` row exists and the source video is unchanged.

The DB connection is passed in via the :class:`DBConn` Protocol;
production callers wire ``asyncpg.Connection`` (with a thin facade) or
``aiosqlite.Connection``. The full connection wrapper lives in Story
1.5; this module stays pure-Python so it can be imported and unit-
tested without either driver installed.
"""

from __future__ import annotations

import json
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from datetime import datetime
from enum import StrEnum
from typing import Any, Protocol
from uuid import UUID

from .pubsub import JOBS_NEW, get_bus

__all__ = [
    "LIVE_STATES",
    "TERMINAL_STATES",
    "DBConn",
    "EnqueueResult",
    "Job",
    "JobState",
    "Stage",
    "enqueue",
    "enqueue_scan",
]


class JobState(StrEnum):
    """The eight job states from architecture §7.2.

    Mirrors the CHECK constraint ``processing_jobs_state_chk`` in the
    migration: any value not listed here will be rejected at insert
    time, providing defense-in-depth against typos.
    """

    PENDING = "pending"
    CLAIMED = "claimed"
    RUNNING = "running"
    PAUSED = "paused"
    RESUMING = "resuming"
    DONE = "done"
    FAILED = "failed"
    CANCELLED = "cancelled"


class Stage(StrEnum):
    """The seven canonical pipeline stages (Story 1.6 + ``subtitle_gen``).

    Mirrors the CHECK constraint ``processing_jobs_stage_chk`` in the
    migration. Adding a new stage requires (a) a new value here and
    (b) a follow-up migration that ALTERs the CHECK.
    """

    SCAN = "scan"
    PROBE = "probe"
    EXTRACT = "extract"
    TRANSCRIBE = "transcribe"
    SUBTITLE_GEN = "subtitle_gen"
    INDEX = "index"
    THUMBNAIL = "thumbnail"


LIVE_STATES: frozenset[JobState] = frozenset(
    {
        JobState.PENDING,
        JobState.CLAIMED,
        JobState.RUNNING,
        JobState.RESUMING,
        JobState.PAUSED,
    }
)

TERMINAL_STATES: frozenset[JobState] = frozenset(
    {
        JobState.DONE,
        JobState.FAILED,
        JobState.CANCELLED,
    }
)


@dataclass(slots=True, frozen=True)
class EnqueueResult:
    """The return value from :func:`enqueue`.

    ``outcome`` is one of ``inserted`` (a fresh row was created),
    ``reused`` (a live row already existed and we returned its id),
    or ``skipped_done_unchanged`` (a ``done`` row exists for an
    unchanged source video; no new work needed).
    """

    id: int
    outcome: str


@dataclass(slots=True, frozen=True)
class Job:
    """One ``processing_jobs`` row, decoded into Python types.

    Field order and types match the migration. ``payload`` is parsed
    JSON; the worker reads it back into the stage-specific options
    dict.

    Slot 0058 (gap-closure) made jobs scopeable two ways, enforced by
    the ``processing_jobs_scope_chk`` CHECK:

    - **per-video** stages (PROBE/EXTRACT/TRANSCRIBE/…) carry a
      ``video_id`` and a null ``library_id`` — exactly as before.
    - **library-scoped** SCAN carries a ``library_id`` and a null
      ``video_id`` (no ``videos`` row exists yet — the scan is what
      discovers them).

    So ``video_id`` is ``UUID | None`` and ``library_id`` is the new
    ``UUID | None`` companion; a handler reads whichever its stage
    uses.
    """

    id: int
    video_id: UUID | None
    library_id: UUID | None
    stage: Stage
    state: JobState
    priority: int
    attempts: int
    max_attempts: int
    claimed_by: str | None
    claimed_at: datetime | None
    last_heartbeat_at: datetime | None
    not_before: datetime | None
    error: str | None
    total_duration_seconds: float | None
    processed_seconds: float
    segments_completed: int
    last_segment_end_sec: float
    estimated_remaining_sec: float | None
    realtime_factor: float | None
    progress_updated_at: datetime | None
    pause_requested: bool
    cancel_requested: bool
    paused_at: datetime | None
    paused_at_sec: float | None
    paused_reason: str | None
    resumed_at: datetime | None
    resume_count: int
    metrics: dict[str, Any] | None
    payload: dict[str, Any] | None
    created_at: datetime
    finished_at: datetime | None


class _Row(Protocol):
    """Mapping-shaped row, as returned by asyncpg/aiosqlite."""

    def __getitem__(self, key: str) -> Any: ...


class DBConn(Protocol):
    """The minimal connection shape :func:`enqueue` needs.

    Both ``asyncpg.Connection`` (with a thin facade) and
    ``aiosqlite.Connection`` (wrapped to expose ``fetchrow``) satisfy
    this; the project's connection wrapper from Story 1.5 will land as
    the canonical implementation. Until then callers can pass any
    object exposing these three methods.

    ``dialect`` is one of ``"postgres"`` or ``"sqlite"``. The Postgres
    NOTIFY trigger fires at the SQL level, so the helper only needs to
    publish to the in-process bus on SQLite.
    """

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...


# SQL is parameterised with $N placeholders for asyncpg; the SQLite
# code path rewrites them to ``?`` at the connection-wrapper layer
# (Story 1.5), keeping this module dialect-agnostic at the call site.

_INSERT_SQL = """
INSERT INTO processing_jobs
       (video_id, stage, state, priority, payload, max_attempts)
VALUES ($1, $2, 'pending', $3, $4, $5)
ON CONFLICT (video_id, stage)
   WHERE state IN ('pending','claimed','running','resuming','paused')
DO NOTHING
RETURNING id
"""

_FALLBACK_LIVE_SQL = """
SELECT id FROM processing_jobs
 WHERE video_id = $1
   AND stage    = $2
   AND state IN ('pending','claimed','running','resuming','paused')
 LIMIT 1
"""

_DONE_ROW_SQL = """
SELECT pj.id          AS id,
       pj.finished_at AS finished_at,
       v.updated_at   AS updated_at
  FROM processing_jobs pj
  JOIN videos v ON v.id = pj.video_id
 WHERE pj.video_id = $1
   AND pj.stage    = $2
   AND pj.state    = 'done'
 ORDER BY pj.finished_at DESC
 LIMIT 1
"""

# --- Library-scoped SCAN enqueue (slot 0058) ----------------------------
#
# A SCAN job has no video — it is what discovers them — so it cannot use
# the per-video `enqueue` above. It rides the slot 0058 partial unique
# index `processing_jobs_one_live_scan_per_library`
# (UNIQUE (library_id, stage) WHERE stage='scan' AND state IN <live>) the
# exact same race-free way: concurrent inserts collide on the index, the
# loser's INSERT is a no-op, the caller falls back to the existing live
# row's id. There is no "skip when done" branch — a library is always
# re-scannable (the source tree changes out of band), unlike a finished
# per-video stage whose inputs are immutable.

_INSERT_SCAN_SQL = """
INSERT INTO processing_jobs
       (library_id, video_id, stage, state, priority, payload, max_attempts)
VALUES ($1, NULL, 'scan', 'pending', $2, $3, $4)
ON CONFLICT (library_id, stage)
   WHERE stage = 'scan'
     AND state IN ('pending','claimed','running','resuming','paused')
DO NOTHING
RETURNING id
"""

_FALLBACK_LIVE_SCAN_SQL = """
SELECT id FROM processing_jobs
 WHERE library_id = $1
   AND stage      = 'scan'
   AND state IN ('pending','claimed','running','resuming','paused')
 LIMIT 1
"""


async def enqueue(
    db: DBConn,
    *,
    video_id: UUID,
    stage: Stage,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult:
    """Insert a ``pending`` row, or return the existing live row's id.

    Idempotency rules (matching Story 6.1's acceptance criteria):

    - **Done row exists and source unchanged**
      (``videos.updated_at <= done.finished_at``) → return the done
      row's id with ``outcome='skipped_done_unchanged'``. The check
      runs *before* the INSERT because the unique-live partial index
      doesn't cover ``done`` rows; a naive INSERT-first approach would
      happily create a duplicate pending row alongside the done one.
    - **Live row exists** (state in ``{pending, claimed, running,
      resuming, paused}``) → return its id with ``outcome='reused'``.
      Detected by the unique partial index swallowing the INSERT.
    - **Otherwise** → INSERT and return ``outcome='inserted'``. On
      Postgres the AFTER INSERT trigger fires
      ``pg_notify('jobs.new', …)``; on SQLite the helper publishes
      manually on the in-process :class:`PubsubBus`.

    Concurrent callers race on the unique partial index
    ``processing_jobs_one_live_per_video_stage``: exactly one INSERT
    wins, the rest fall through to the SELECT branch and return the
    same id.
    """
    payload_text = json.dumps(payload) if payload is not None else None

    async with db.transaction():
        # Skip-when-done check first. The unique-live partial index
        # only covers live states, so without this check a stale done
        # row would let the INSERT through and we'd create unwanted
        # re-runs of finished work.
        done = await db.fetchrow(_DONE_ROW_SQL, video_id, stage.value)
        if done is not None and done["updated_at"] <= done["finished_at"]:
            return EnqueueResult(
                id=int(done["id"]),
                outcome="skipped_done_unchanged",
            )

        # Try the insert. On conflict the unique-live partial index
        # silently swallows it and we fall through to the live-row
        # lookup.
        row = await db.fetchrow(
            _INSERT_SQL,
            video_id,
            stage.value,
            priority,
            payload_text,
            max_attempts,
        )
        if row is not None:
            new_id = int(row["id"])
            if db.dialect == "sqlite":
                # Postgres has the AFTER INSERT trigger; SQLite needs
                # us to publish manually because there's no NOTIFY.
                get_bus().publish(
                    JOBS_NEW,
                    {
                        "id": new_id,
                        "video_id": str(video_id),
                        "stage": stage.value,
                        "priority": priority,
                    },
                )
            return EnqueueResult(id=new_id, outcome="inserted")

        # Conflict on the unique-live partial index → reuse the
        # existing live row.
        live = await db.fetchrow(_FALLBACK_LIVE_SQL, video_id, stage.value)
        if live is not None:
            return EnqueueResult(id=int(live["id"]), outcome="reused")

        # Truly impossible by construction: the INSERT only fails when
        # a live row exists, so the SELECT above must find one. A miss
        # here means the schema invariant was violated.
        raise RuntimeError(
            "enqueue: INSERT swallowed but no live row found — schema invariant violated",
        )


async def enqueue_scan(
    db: DBConn,
    *,
    library_id: UUID,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult:
    """Insert a library-scoped ``pending`` SCAN row, or reuse the live one.

    The library-scoped sibling of :func:`enqueue`. Mirrors its
    conventions (same signature shape minus ``video_id``/``stage``,
    same :class:`EnqueueResult`, same SQLite manual-publish on the
    in-process bus) but honours the slot 0058 partial unique index
    ``processing_jobs_one_live_scan_per_library`` instead of the
    per-video one.

    Idempotency: a live scan row for the library → return its id with
    ``outcome='reused'`` (the partial unique index swallows the
    duplicate INSERT). Otherwise INSERT and return
    ``outcome='inserted'``. There is no ``skipped_done_unchanged``
    branch — a finished scan does not block the next one (the library's
    on-disk tree mutates out of band; "unchanged source" has no stable
    meaning for a whole library the way it does for one immutable
    video).
    """
    payload_text = json.dumps(payload) if payload is not None else None

    async with db.transaction():
        row = await db.fetchrow(
            _INSERT_SCAN_SQL,
            library_id,
            priority,
            payload_text,
            max_attempts,
        )
        if row is not None:
            new_id = int(row["id"])
            if db.dialect == "sqlite":
                # Postgres has the slot 0002 AFTER INSERT trigger;
                # SQLite has no NOTIFY so publish manually. video_id is
                # null for a scan job — keep the key present (as None)
                # so subscribers see a stable payload shape.
                get_bus().publish(
                    JOBS_NEW,
                    {
                        "id": new_id,
                        "video_id": None,
                        "library_id": str(library_id),
                        "stage": Stage.SCAN.value,
                        "priority": priority,
                    },
                )
            return EnqueueResult(id=new_id, outcome="inserted")

        live = await db.fetchrow(_FALLBACK_LIVE_SCAN_SQL, library_id)
        if live is not None:
            return EnqueueResult(id=int(live["id"]), outcome="reused")

        raise RuntimeError(
            "enqueue_scan: INSERT swallowed but no live scan row found "
            "— schema invariant violated",
        )
