"""Every job this process runs, as it happens (design-agent.md section 3, ``job`` frames).

The engine calls :func:`claimed`, :func:`progress` and :func:`finished` from its job
lifecycle (``engine/run.py``, ``engine/context.py``); this module keeps the running set for
the metrics frame and hands each event to the sinks the agent registers (the channel and
the jobs file). With no sink registered an event costs a dict and a loop over nothing.

Nothing here may raise into the engine: every entry point swallows and counts its errors.

An event: ``{t: "job", event, job_id, stage, subject_kind, subject_id, title, attempt,
started_at, ts, duration_ms?, progress?, outcome?, output?}``.
"""

from __future__ import annotations

import threading
import time
from collections import deque
from collections.abc import Callable, Mapping
from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

from ... import progress_clock

__all__ = [
    "EVENTS",
    "JobTracker",
    "RunningJob",
    "claimed",
    "finished",
    "progress",
    "tracker",
]

EVENTS = ("claimed", "progress", "done", "failed", "skipped", "paused", "deferred")
#: a job's progress events are sent at most this often
PROGRESS_EVERY_SEC = 1.0
#: an error text in an event is cut to this many characters
ERROR_CHARS = 500

Sink = Callable[[dict[str, Any]], None]


def iso_now() -> str:
    return datetime.now(UTC).isoformat(timespec="milliseconds").replace("+00:00", "Z")


def _iso(value: Any) -> str | None:
    if value is None:
        return None
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=UTC)
        text: str = value.astimezone(UTC).isoformat(timespec="milliseconds")
        return text.replace("+00:00", "Z")
    return str(value)


@dataclass
class RunningJob:
    job_id: int
    stage: str
    subject_kind: str
    subject_id: str | None
    attempt: int
    started_at: str
    started_mono: float
    title: str | None = None
    progress: dict[str, Any] | None = None
    last_progress_sent: float = field(default=float("-inf"))

    def base(self, event: str) -> dict[str, Any]:
        """A ``job`` frame (section 5.3): every key present, null when it does not apply."""
        return {
            "t": "job",
            "event": event,
            "job_id": self.job_id,
            "stage": self.stage,
            "subject_kind": self.subject_kind,
            "subject_id": self.subject_id,
            "title": self.title,
            "attempt": self.attempt,
            "started_at": self.started_at,
            "ts": iso_now(),
            "duration_ms": None,
            "progress": None,
            "outcome": None,
            "output": None,
        }

    def snapshot(self) -> dict[str, Any]:
        """The metrics frame's ``pipeline.jobs`` entry (section 5.2)."""
        return {
            "job_id": self.job_id,
            "stage": self.stage,
            "subject": f"{self.subject_kind}:{self.subject_id}",
            "title": self.title,
            "since": self.started_at,
            "progress": None if self.progress is None else dict(self.progress),
        }


class JobTracker:
    def __init__(self, *, clock: Callable[[], float] = time.monotonic) -> None:
        self._lock = threading.Lock()
        self._running: dict[int, RunningJob] = {}
        self._claims: deque[float] = deque(maxlen=10_000)
        self._sinks: list[Sink] = []
        self.clock = clock
        self.errors = 0

    # -- sinks -----------------------------------------------------------------

    def add_sink(self, sink: Sink) -> None:
        with self._lock:
            if sink not in self._sinks:
                self._sinks.append(sink)

    def remove_sink(self, sink: Sink) -> None:
        with self._lock:
            if sink in self._sinks:
                self._sinks.remove(sink)

    def _emit(self, event: dict[str, Any]) -> None:
        for sink in tuple(self._sinks):
            try:
                sink(event)
            except Exception:  # noqa: BLE001 - a sink never fails a job
                self.errors += 1

    # -- lifecycle ---------------------------------------------------------------

    def claimed(self, job: Any, *, title: str | None = None) -> None:
        try:
            started = getattr(job, "started_at", None) or getattr(job, "claimed_at", None)
            subject_id = getattr(job, "subject_id", None) or getattr(job, "video_id", None)
            subject_id = subject_id or getattr(job, "library_id", None)
            run = RunningJob(
                job_id=int(job.id),
                stage=str(job.stage),
                subject_kind=str(getattr(job, "subject_kind", "video") or "video"),
                subject_id=None if subject_id is None else str(subject_id),
                attempt=int(getattr(job, "attempts", 0) or 0),
                started_at=_iso(started) or iso_now(),
                started_mono=self.clock(),
                title=title,
            )
            with self._lock:
                self._running[run.job_id] = run
                self._claims.append(self.clock())
            self._emit(run.base("claimed"))
        except Exception:  # noqa: BLE001
            self.errors += 1

    def set_title(self, job_id: int, title: str | None) -> None:
        run = self._running.get(job_id)
        if run is not None and title:
            run.title = title

    def progress(
        self,
        job_id: int,
        *,
        step: str | None,
        done: float | None,
        total: float | None,
        force: bool = False,
    ) -> None:
        try:
            run = self._running.get(job_id)
            if run is None:
                return
            run.progress = {"step": step, "done": done, "total": total}
            now = self.clock()
            if not force and now - run.last_progress_sent < PROGRESS_EVERY_SEC:
                return
            run.last_progress_sent = now
            ev = run.base("progress")
            ev["progress"] = dict(run.progress)
            self._emit(ev)
        except Exception:  # noqa: BLE001
            self.errors += 1

    def finished(
        self,
        job_id: int,
        event: str,
        *,
        outcome: Mapping[str, Any] | None = None,
        output: Mapping[str, Any] | None = None,
        title: str | None = None,
    ) -> bool:
        """``event`` is one of done, failed, skipped, paused, deferred. Only a job this
        process has running ends (once: the engine and the state hooks may both report
        the same end; the first wins). True when an event was sent."""
        try:
            with self._lock:
                run = self._running.pop(int(job_id), None)
            if run is None:
                return False
            if title:
                run.title = title
            ev = run.base(event)
            ev["duration_ms"] = int((self.clock() - run.started_mono) * 1000)
            if outcome is not None:
                out = dict(outcome)
                if isinstance(out.get("error"), str):
                    out["error"] = out["error"][:ERROR_CHARS]
                ev["outcome"] = out
            if output is not None:
                ev["output"] = _jsonable(output)
            self._emit(ev)
            return True
        except Exception:  # noqa: BLE001
            self.errors += 1
            return False

    # -- readers -----------------------------------------------------------------

    def running(self) -> list[dict[str, Any]]:
        with self._lock:
            runs = list(self._running.values())
        return [r.snapshot() for r in runs]

    def claims_per_min(self) -> int:
        cutoff = self.clock() - 60.0
        with self._lock:
            while self._claims and self._claims[0] < cutoff:
                self._claims.popleft()
            return len(self._claims)


def _jsonable(value: Any, depth: int = 0) -> Any:
    """A JSON-safe copy of a stage summary (small: deep or long values are cut)."""
    if value is None or isinstance(value, bool | int | float | str):
        return value[:ERROR_CHARS] if isinstance(value, str) else value
    if depth > 3:
        return str(value)[:ERROR_CHARS]
    if isinstance(value, Mapping):
        return {str(k): _jsonable(v, depth + 1) for k, v in list(value.items())[:50]}
    if isinstance(value, list | tuple | set | frozenset):
        return [_jsonable(v, depth + 1) for v in list(value)[:50]]
    return str(value)[:ERROR_CHARS]


_TRACKER = JobTracker()


def tracker() -> JobTracker:
    return _TRACKER


def claimed(job: Any, *, title: str | None = None) -> None:
    _TRACKER.claimed(job, title=title)


def progress(
    job_id: int, *, step: str | None, done: float | None, total: float | None, force: bool = False
) -> None:
    # every progress path passes here: the job heartbeat's stall check reads it
    progress_clock.note_progress(job_id)
    _TRACKER.progress(job_id, step=step, done=done, total=total, force=force)


def finished(
    job_id: int,
    event: str,
    *,
    outcome: Mapping[str, Any] | None = None,
    output: Mapping[str, Any] | None = None,
    title: str | None = None,
) -> bool:
    return _TRACKER.finished(job_id, event, outcome=outcome, output=output, title=title)


def failure(kind: str, message: str | None, *, retryable: bool) -> dict[str, Any]:
    """An ``outcome`` for failed / deferred events."""
    return {"kind": kind, "retryable": retryable, "error": (message or "")[:ERROR_CHARS]}
