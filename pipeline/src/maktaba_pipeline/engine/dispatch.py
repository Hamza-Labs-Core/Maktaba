"""Routing a claimed job to the engine or to a legacy handler (plan 06-11 section 3.5)."""

from __future__ import annotations

import traceback
from collections.abc import Awaitable, Callable, Mapping
from dataclasses import dataclass, field
from typing import Any

from ..cluster.agent import jobs as job_events
from ..db.jobs import Job
from ..db.jobs_state import StageError, mark_failed_or_retry
from ..extensions.store import ExtensionState
from ..log import get_logger
from ..pipeline.heartbeat import heartbeat_for
from .context import StageContext
from .graph import StageDef
from .health import NasProbe
from .paths import PathMap
from .propagate import after_legacy_done
from .run import execute

__all__ = ["EngineRuntime", "LegacyHandler", "dispatch_job"]

_log = get_logger()

LegacyHandler = Callable[[Any, Job], Awaitable[None]]


@dataclass
class EngineRuntime:
    """Everything a worker needs to run engine jobs."""

    graph: Mapping[str, StageDef]
    extension_states: Mapping[str, ExtensionState] = field(default_factory=dict)
    nas: NasProbe = field(default_factory=NasProbe)
    path_map: PathMap = field(default_factory=PathMap)

    async def context_for(self, db: Any, job: Job) -> StageContext:
        row = await db.fetchrow("SELECT write_mode FROM job_stages WHERE stage = $1", job.stage)
        return StageContext(
            db=db,
            job=job,
            stage=self.graph[job.stage],
            nas=self.nas,
            write_mode="auto" if row is None else str(row["write_mode"]),
            settings={n: s.settings for n, s in self.extension_states.items()},
            path_map=self.path_map,
        )


async def dispatch_job(
    db: Any,
    job: Job,
    *,
    runtime: EngineRuntime,
    legacy_handlers: Mapping[str, LegacyHandler],
) -> None:
    """Run ``job``: a legacy handler (then engine bookkeeping) or an engine stage."""
    stage = runtime.graph.get(job.stage)
    if stage is None:
        await mark_failed_or_retry(
            db,
            job_id=job.id,
            error=StageError(
                kind="dispatch_unknown_stage",
                message=f"no handler registered for stage {job.stage}",
                retryable=False,
            ),
        )
        return
    if stage.legacy:
        handler = legacy_handlers.get(job.stage)
        if handler is None:
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind="dispatch_unknown_stage",
                    message=f"no handler registered for stage {job.stage}",
                    retryable=False,
                ),
            )
            return
        job_events.claimed(job)
        try:
            async with heartbeat_for(db, job_id=job.id):
                await handler(db, job)
        except BaseException as exc:
            if not isinstance(exc, Exception):  # cancelled: the worker is stopping
                job_events.finished(
                    job.id,
                    "paused",
                    outcome=job_events.failure("interrupted", "worker stopping", retryable=True),
                )
                raise
            _log.exception("legacy_handler_failed", job_id=job.id, stage=job.stage)
            job_events.finished(
                job.id,
                "failed",
                outcome=job_events.failure(type(exc).__name__, str(exc), retryable=True),
            )
            await mark_failed_or_retry(
                db,
                job_id=job.id,
                error=StageError(
                    kind=type(exc).__name__,
                    message=str(exc),
                    traceback=traceback.format_exc(),
                    retryable=True,
                ),
            )
            return
        # a handler that failed or paused its job reported it through the state hooks;
        # whatever is still running here ended well
        job_events.finished(job.id, "done")
        await after_legacy_done(db, job.id, runtime.graph)
        return
    ctx = await runtime.context_for(db, job)
    async with heartbeat_for(db, job_id=job.id):
        await execute(db, job, stage=stage, graph=runtime.graph, ctx=ctx)
