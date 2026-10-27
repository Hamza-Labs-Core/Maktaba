"""When each running job last reported progress, in this process.

The job heartbeat (:mod:`.heartbeat`) proves the worker is alive, not that the job
moves: a decoder stuck on a share that stopped answering keeps ticking it for ever.
Every progress path (``tick_progress``, the engine's ``ctx.progress``, the legacy
``on_progress`` mirror) notes the job here; the heartbeat compares the two and logs
``job_stalled`` when a job has run a long time without any.

No imports beyond the standard library: the database and engine layers note progress
here, and the heartbeat reads it.
"""

from __future__ import annotations

import time

__all__ = ["forget", "last_progress", "note_progress"]

_last: dict[int, float] = {}

#: a bound on the map: entries are dropped when their job's heartbeat stops, this only
#: guards against a path that notes progress for a job no heartbeat watches
_MAX_ENTRIES = 4096


def note_progress(job_id: int, *, now: float | None = None) -> None:
    """Record that ``job_id`` moved (monotonic clock)."""
    if len(_last) >= _MAX_ENTRIES and job_id not in _last:
        _last.clear()
    _last[job_id] = time.monotonic() if now is None else now


def last_progress(job_id: int) -> float | None:
    """The monotonic time ``job_id`` last moved, or ``None`` if it never reported."""
    return _last.get(job_id)


def forget(job_id: int) -> None:
    _last.pop(job_id, None)
