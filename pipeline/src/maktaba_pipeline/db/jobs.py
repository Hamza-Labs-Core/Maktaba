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

from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass, field
from datetime import datetime
from enum import StrEnum
from typing import TYPE_CHECKING, Any, Protocol
from uuid import UUID

from .pubsub import JOBS_NEW, get_bus  # noqa: F401 - re-exported for tests

if TYPE_CHECKING:
    from ..engine.graph import Subject

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
    """Well-known stage names, for typing legacy call sites.

    Since slot 0087 the source of truth is the ``job_stages`` table, filled from
    ``engine/graph.py``; there is no stage CHECK to keep in sync. A job's
    ``stage`` is a plain ``str`` (extension stages are not enumerable here).
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
    stage: str
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
    # plan 06-11 (slot 0088): the subject and the change-driven columns
    subject_kind: str = "video"
    subject_id: UUID | None = None
    series_id: UUID | None = None
    season_id: UUID | None = None
    stage_version: int | None = None
    input_hash: str | None = None
    output_hash: str | None = None
    outcome: str | None = None
    output_summary: dict[str, Any] | None = None
    force: bool = False
    force_cascade: bool = False
    reasons: list[Any] = field(default_factory=list)
    enqueue_count: int = 1
    triggered_by: int | None = None
    triggered_by_stage: str | None = None
    rerun_requested: bool = False
    rerun_force: bool = False
    rerun_reasons: list[Any] = field(default_factory=list)
    propagated_at: datetime | None = None
    error_cause: str | None = None
    progress: dict[str, Any] | None = None
    started_at: datetime | None = None

    @property
    def subject(self) -> Subject:
        """The :class:`~maktaba_pipeline.engine.graph.Subject` this job acts on."""
        from ..engine.graph import CATALOG_ID, Subject

        sid = self.subject_id
        if sid is None:
            fallback = self.video_id or self.library_id
            sid = CATALOG_ID if self.subject_kind == "catalog" else fallback
        assert sid is not None
        return Subject(kind=self.subject_kind, id=sid, library_id=self.library_id)  # type: ignore[arg-type]


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

    async def fetch(self, sql: str, *args: Any) -> list[Any]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


# SQL is parameterised with $N placeholders for asyncpg; the SQLite code path
# rewrites them to ``?`` in the connection facade, keeping this module
# dialect-agnostic at the call site.

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

_LEGACY_STAGES = frozenset(
    {
        "scan",
        "probe",
        "extract",
        "transcribe",
        "subtitle_gen",
        "index",
        "thumbnail",
        "topic_recluster",
        "topic_assign",
        "categorize",
        "chapter_infer",
    }
)


def _legacy_outcome(outcome: str) -> str:
    """Map the engine's outcomes onto the legacy ``EnqueueResult`` vocabulary."""
    return "inserted" if outcome == "inserted" else "reused"


async def enqueue(
    db: DBConn,
    *,
    video_id: UUID,
    stage: Stage | str,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult:
    """Enqueue a per-video stage (thin wrapper over :func:`engine.enqueue.enqueue_job`).

    Kept for the existing handlers and their signature. Outcomes:

    - ``skipped_done_unchanged`` - **legacy stages only**: a ``done`` row exists
      and the video has not changed since (``videos.updated_at <= finished_at``).
      The change-driven stages decide this themselves, in the worker, from their
      input fingerprint.
    - ``reused`` - a live row already existed (the engine reports ``coalesced``
      or ``rerun_requested``; both mean "your trigger is already on the queue").
    - ``inserted`` - a fresh pending row.
    """
    from ..engine.enqueue import enqueue_job

    stage_name = str(stage)
    async with db.transaction():
        if stage_name in _LEGACY_STAGES:
            done = await db.fetchrow(_DONE_ROW_SQL, str(video_id), stage_name)
            if done is not None and done["updated_at"] <= done["finished_at"]:
                return EnqueueResult(id=int(done["id"]), outcome="skipped_done_unchanged")
        res = await enqueue_job(
            db,
            subject_kind="video",
            subject_id=video_id,
            stage=stage_name,
            priority=priority,
            payload=payload,
            max_attempts=max_attempts,
            reason={"why": "enqueue", "by": "system"},
        )
    return _wrap(res.job_id, res.outcome, "enqueue")


async def enqueue_scan(
    db: DBConn,
    *,
    library_id: UUID,
    priority: int = 100,
    payload: dict[str, Any] | None = None,
    max_attempts: int = 3,
) -> EnqueueResult:
    """Enqueue a library-scoped SCAN job (wrapper over :func:`enqueue_job`).

    There is no ``skipped_done_unchanged`` branch: a library is always
    re-scannable, its tree changes out of band.
    """
    from ..engine.enqueue import enqueue_job

    res = await enqueue_job(
        db,
        subject_kind="library",
        subject_id=library_id,
        stage="scan",
        priority=priority,
        payload=payload,
        max_attempts=max_attempts,
        reason={"why": "scan", "by": "system"},
    )
    return _wrap(res.job_id, res.outcome, "enqueue_scan")


def _wrap(job_id: int | None, outcome: str, who: str) -> EnqueueResult:
    if job_id is None:
        if outcome == "disabled":
            return EnqueueResult(id=0, outcome="disabled")
        raise ValueError(f"{who}: refused ({outcome})")
    return EnqueueResult(id=job_id, outcome=_legacy_outcome(outcome))
