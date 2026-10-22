"""Whose claim a job's writes apply to.

A machine that lost the home for longer than the claim lease may still be running a job
the home has since handed to another machine (the reaper gives a claim with no heartbeat
for ``stale_claim_sec`` back, and another worker claims it). The writes that beat or end a
claimed job (:mod:`.jobs_progress`, :mod:`.jobs_state`, the engine's own) apply only while
this worker still holds the claim: the claim loop names the worker for each job it runs
(:func:`running_for`), and those writes add ``claimed_by = <worker>`` to their predicate,
so a late one changes nothing. A write outside a job the claim loop runs (tests, scripts)
names no worker and is not limited.
"""

from __future__ import annotations

from collections.abc import Iterator
from contextlib import contextmanager
from contextvars import ContextVar
from typing import Any

__all__ = ["current", "guard", "running_for"]

_owner: ContextVar[str | None] = ContextVar("maktaba_claim_owner", default=None)


def current() -> str | None:
    """The worker whose claim the running job is, or ``None`` outside a claimed job."""
    return _owner.get()


@contextmanager
def running_for(worker: str | None) -> Iterator[None]:
    """The job run in this block (and the tasks it starts) is ``worker``'s claim."""
    token = _owner.set(worker)
    try:
        yield
    finally:
        _owner.reset(token)


def guard(db: Any, n: int) -> str:
    """The predicate limiting a write to the claim of parameter ``$n`` (the worker, or
    NULL for no limit), for a statement written with ``$N`` placeholders."""
    if getattr(db, "dialect", "postgres") == "postgres":
        return f"(${n}::text IS NULL OR claimed_by = ${n}::text)"
    return f"(${n} IS NULL OR claimed_by = ${n})"
