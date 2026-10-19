"""The handle a stage handler receives (plan 06-11 section 6.1)."""

from __future__ import annotations

import time
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from typing import Any

from ..db.jobs import Job
from ..db.jobs_state import read_flags
from .events import job_ref, log_event, publish
from .graph import StageDef
from .health import NasProbe
from .paths import PathMap
from .sql import dumps, now, to_ts

__all__ = ["JobCancelled", "StageContext", "StageFailure"]

PROGRESS_INTERVAL_SEC = 2.0


class JobCancelled(Exception):
    """Raised by :meth:`StageContext.check_cancel` when the operator cancelled the job."""


class JobDeferred(Exception):
    """A handler's way to give its job back for later, not as a failure: the job is
    paused (resuming it is not a new attempt) and not claimed for ``retry_after_sec``.
    For a worker that cannot run it now (the host is short of memory, say)."""

    def __init__(self, reason: str, retry_after_sec: float) -> None:
        super().__init__(reason)
        self.reason = reason
        self.retry_after_sec = retry_after_sec


class StageFailure(Exception):
    """A handler's way to fail with a structured, classifiable error."""

    def __init__(self, kind: str, message: str, *, retryable: bool = True) -> None:
        super().__init__(f"{kind}: {message}")
        self.kind = kind
        self.message = message
        self.retryable = retryable


@dataclass
class StageContext:
    db: Any
    job: Job
    stage: StageDef
    nas: NasProbe = field(default_factory=NasProbe)
    #: ``auto`` applies change sets; ``review`` parks them (reserved, see changes.py)
    write_mode: str = "auto"
    #: extension name -> its stored settings (secrets included; never log these)
    settings: Mapping[str, Mapping[str, str]] = field(default_factory=dict)
    path_map: PathMap = field(default_factory=PathMap)
    clock: Callable[[], float] = time.monotonic
    #: digests of the change sets this run parked for review (``apply_or_review``); the
    #: job's ``output_summary.review_digests``, which an operator's Apply approves
    review_digests: list[str] = field(default_factory=list)
    _last_progress: float = field(default=float("-inf"), repr=False)
    _rules: dict[str, Any] = field(default_factory=dict, repr=False)

    def local_path(self, db_path: str) -> str:
        """This host's file-system path for a path stored in the database."""
        return self.path_map.to_local(db_path)

    def extension_settings(self, name: str) -> Mapping[str, str]:
        return self.settings.get(name, {})

    async def rules(self, section: str) -> Any:
        """The rules of ``section`` (``detect`` / ``groups`` / ``planner``): the built-in
        defaults with what was changed in the settings (``app_settings`` key
        ``rules.<section>``), read once per job. A stored value that cannot be used is
        skipped and logged; its default applies."""
        if section not in self._rules:
            from ..rules import read

            cfg, changes, bad = await read(self.db, section)
            if changes:
                await self.log("info", f"rules.{section}: {', '.join(sorted(changes))} changed")
            for name, why in bad.items():
                await self.log("warn", f"rules.{section}.{name} skipped, default used: {why}")
            self._rules[section] = cfg
        return self._rules[section]

    async def progress(
        self,
        step: str,
        done: int,
        total: int,
        message: str | None = None,
        *,
        steps: list[str] | None = None,
        force: bool = False,
    ) -> None:
        """Record progress (throttled to one write per 2 s unless ``force``)."""
        from ..cluster.agent import jobs as job_events

        job_events.progress(self.job.id, step=step, done=done, total=total, force=force)
        t = self.clock()
        if not force and t - self._last_progress < PROGRESS_INTERVAL_SEC:
            return
        self._last_progress = t
        doc: dict[str, Any] = {
            "step": step,
            "steps": steps if steps is not None else (self.job.progress or {}).get("steps", []),
            "done_units": done,
            "total_units": total,
        }
        if message:
            doc["message"] = message
        await self.db.execute(
            "UPDATE processing_jobs SET progress = $2, progress_updated_at = $3 WHERE id = $1",
            self.job.id,
            dumps(doc),
            to_ts(self.db, now()),
        )
        ref = await job_ref(self.db, self.job.id)
        if ref is not None:
            await publish(self.db, "job.progress", {"job": ref})

    async def log(self, level: str, message: str, data: dict[str, Any] | None = None) -> None:
        """Append a ``step`` event to the job log."""
        await log_event(
            self.db,
            job_id=self.job.id,
            subject_kind=self.job.subject_kind,
            subject_id=self.job.subject.id,
            library_id=self.job.library_id,
            stage=self.job.stage,
            level=level,
            kind="step",
            message=message,
            data=data,
        )

    async def check_cancel(self) -> None:
        flags = await read_flags(self.db, job_id=self.job.id)
        if flags.cancel:
            raise JobCancelled(f"job {self.job.id} cancelled")
