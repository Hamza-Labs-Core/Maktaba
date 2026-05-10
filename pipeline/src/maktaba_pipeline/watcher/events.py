"""Event types shared between the watchdog adapter, debouncer, and dispatcher.

The watcher pipeline has three logical stages: a *raw* event arrives
from watchdog, the debouncer turns a burst of raw events for the same
path into one *settled* event, and the dispatcher turns each settled
event into a single Postgres transaction. Keeping the event types in
one module lets each stage import only what it needs without a cycle.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass

__all__ = ["Op", "RawEvent", "SettledEvent"]


class Op(enum.StrEnum):
    """Logical filesystem operation, post-classification.

    Watchdog surfaces ``Created`` / ``Modified`` / ``Moved`` / ``Deleted``
    plus a directory bit; the debouncer classifies down to four file-level
    cases. CHMOD-only events from watchdog are dropped before reaching the
    debouncer (they cannot affect identity or content).
    """

    CREATE = "create"
    MODIFY = "modify"
    MOVED = "moved"
    DELETED = "deleted"


@dataclass(slots=True, frozen=True)
class RawEvent:
    """One unsettled event handed from watchdog into the debouncer.

    ``dest_path`` is only populated for :attr:`Op.MOVED`. ``library_id``
    is the UUID stringified — the debouncer is dialect-free and never
    touches the DB, so it works with whatever opaque identifier the
    caller supplies.
    """

    library_id: str
    op: Op
    path: str
    dest_path: str | None = None


@dataclass(slots=True, frozen=True)
class SettledEvent:
    """Event the dispatcher consumes after the debounce + settle gate.

    ``size_bytes`` is the size at the moment the file settled — captured
    by the debouncer's final ``os.stat`` so the dispatcher does not have
    to re-stat. ``mtime_ns`` is similarly cached. For :attr:`Op.DELETED`
    and :attr:`Op.MOVED` only the path fields are meaningful; ``size_bytes``
    is ``-1`` to mark "not applicable".
    """

    library_id: str
    op: Op
    path: str
    dest_path: str | None = None
    size_bytes: int = -1
    mtime_ns: int = 0
