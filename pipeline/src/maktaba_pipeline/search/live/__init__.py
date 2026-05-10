"""Live (incremental) indexing — Epic 5 Story 5.5.

Supervisor subscribes to segment-committed events and routes them
through a per-transcript debouncing dispatcher to an
:class:`IncrementalIndexJob`. Dead-letter helpers buffer units that
fail vector-store writes so a periodic drain can retry them.
"""

from __future__ import annotations

from .deadletter import drain_dead_letter, enqueue_dead_letter
from .dispatcher import DispatcherConfig, IndexerDispatcher
from .job import IncrementalIndexJob
from .supervisor import IndexerSupervisor, SupervisorConfig

__all__ = [
    "DispatcherConfig",
    "IncrementalIndexJob",
    "IndexerDispatcher",
    "IndexerSupervisor",
    "SupervisorConfig",
    "drain_dead_letter",
    "enqueue_dead_letter",
]
