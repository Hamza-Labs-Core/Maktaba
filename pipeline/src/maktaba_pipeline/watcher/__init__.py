"""Live filesystem watcher for the pipeline (Story 1.3).

The watcher complements :class:`maktaba_pipeline.scanner.Scanner`: where
the scanner does a one-shot bootstrap walk over a library's roots, the
watcher subscribes to filesystem events and converts each settled
change into the same DB shape (a ``videos`` row plus a ``probe`` job).
The boot order — scan first, watcher second — closes the
"missed-during-downtime" hole called out in plan-01-03 §2.1.

Public surface:

- :class:`Watcher`, :class:`WatcherConfig` — top-level orchestrator.
  Takes one :class:`WatcherStore`, runs one :func:`asyncio.Task` per
  library, and dispatches every settled event through a single async
  loop so DB transactions serialise per process.
- :class:`Debouncer` — pure (no IO besides ``os.stat``) per-path settle
  timer. Tests drive it directly; the production observer drives it
  through :class:`LibraryObserver`.
- :class:`SettledEvent`, :class:`Op` — the canonical event shape the
  dispatcher consumes. Every settled emission is one of CREATE, MODIFY,
  MOVED, DELETED.
- :class:`WatcherStore` — persistence Protocol. The scanner's
  :class:`maktaba_pipeline.scanner.ScanStore` is a strict subset; the
  watcher adds three methods (``find_video_by_hash``,
  ``update_video_path``, ``soft_delete_by_path``) the rename / delete
  branches need.
"""

from __future__ import annotations

from .debouncer import Debouncer, DebouncerConfig
from .dispatch import WatcherDispatcher, WatcherStore
from .events import Op, RawEvent, SettledEvent
from .service import LibraryObserver, Watcher, WatcherConfig

__all__ = [
    "Debouncer",
    "DebouncerConfig",
    "LibraryObserver",
    "Op",
    "RawEvent",
    "SettledEvent",
    "Watcher",
    "WatcherConfig",
    "WatcherDispatcher",
    "WatcherStore",
]
