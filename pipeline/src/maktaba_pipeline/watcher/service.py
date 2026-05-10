"""Top-level :class:`Watcher` orchestrator + watchdog adapter.

Glues four collaborators:

- ``watchdog.observers.Observer`` (or :class:`~watchdog.observers.polling.PollingObserver`
  on network mounts and in tests). Runs on its own threads.
- :class:`maktaba_pipeline.watcher.LibraryObserver` — translates watchdog's
  per-platform event objects into :class:`RawEvent` and forwards them to
  the debouncer.
- :class:`maktaba_pipeline.watcher.Debouncer` — settles each path before
  emitting a :class:`SettledEvent`.
- :class:`maktaba_pipeline.watcher.WatcherDispatcher` — async consumer
  that maps each settled event to one DB transaction.

Threading: watchdog runs handlers on its own threads, the debouncer
fires timer callbacks on threadpool threads, and the dispatcher runs on
the asyncio loop. The bridge is an :class:`asyncio.Queue` populated via
``loop.call_soon_threadsafe`` from the debouncer's callback. The queue
is bounded (``cfg.queue_capacity = 4096``) so an event storm cannot pin
RAM (plan §6.4 / story TC5 backpressure requirement).
"""

from __future__ import annotations

import asyncio
import contextlib
import fnmatch
import os
from collections.abc import Callable, Iterable
from dataclasses import dataclass, field
from typing import Any, Protocol
from uuid import UUID

from watchdog.events import (
    DirCreatedEvent,
    DirDeletedEvent,
    DirModifiedEvent,
    DirMovedEvent,
    FileCreatedEvent,
    FileDeletedEvent,
    FileModifiedEvent,
    FileMovedEvent,
    FileSystemEvent,
    FileSystemEventHandler,
)
from watchdog.observers.api import BaseObserver

from ..scanner.walker import (
    DEFAULT_IGNORE_BASENAMES,
    DEFAULT_IGNORE_DIRNAMES,
    DEFAULT_VIDEO_EXTENSIONS,
)
from .debouncer import Debouncer, DebouncerConfig
from .dispatch import WatcherDispatcher, WatcherStore
from .events import Op, RawEvent, SettledEvent

__all__ = [
    "LibraryObserver",
    "ObserverFactory",
    "Watcher",
    "WatcherConfig",
]


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def debug(self, event: str, **kwargs: Any) -> Any: ...
    def error(self, event: str, **kwargs: Any) -> Any: ...


# A factory rather than a class so tests can inject a polling observer
# without touching the production import. Returning the abstract type
# lets the polling observer (a subclass of BaseObserver) substitute
# transparently.
ObserverFactory = Callable[[], BaseObserver]


def _default_observer() -> BaseObserver:
    """Return a fresh native :class:`watchdog.observers.Observer`.

    Imported lazily so the module is importable on systems where the
    native observer's compiled extensions are unavailable (rare; the
    test suite forces the polling observer regardless).
    """
    from watchdog.observers import Observer

    return Observer()


@dataclass(slots=True, frozen=True)
class WatcherConfig:
    """Knobs for :class:`Watcher` and its child observers.

    All defaults match plan-01-03 §5 except ``queue_capacity``, which we
    keep at 4096 to match the plan's bounded-channel sizing.
    """

    debouncer: DebouncerConfig = field(default_factory=DebouncerConfig)
    extensions: frozenset[str] = DEFAULT_VIDEO_EXTENSIONS
    ignore_basenames: tuple[str, ...] = DEFAULT_IGNORE_BASENAMES
    ignore_dirnames: frozenset[str] = DEFAULT_IGNORE_DIRNAMES
    queue_capacity: int = 4096


class LibraryObserver(FileSystemEventHandler):
    """Per-library watchdog handler.

    Filters events at the source — ignored extensions, hidden files,
    ``.maktaba/`` paths, and directory-only events never reach the
    debouncer. The result is one :class:`RawEvent` per accepted change,
    forwarded into the shared :class:`Debouncer`.

    The observer keeps a reference to the underlying watchdog
    ``BaseObserver`` so :meth:`stop` can detach the schedule cleanly.
    """

    def __init__(
        self,
        *,
        library_id: UUID,
        roots: Iterable[str],
        observer: BaseObserver,
        debouncer: Debouncer,
        cfg: WatcherConfig,
        log: _Logger,
    ) -> None:
        super().__init__()
        self._library_id = str(library_id)
        self._roots = tuple(roots)
        self._observer = observer
        self._debouncer = debouncer
        self._cfg = cfg
        self._log = log
        self._watches: list[Any] = []

    def start(self) -> None:
        """Schedule recursive watches for every library root.

        Watchdog's :meth:`schedule` is recursive on every supported
        platform, so we don't manage per-directory descriptors here —
        the kernel (or the polling observer) does.
        """
        for root in self._roots:
            try:
                watch = self._observer.schedule(self, root, recursive=True)
            except FileNotFoundError:
                self._log.warning(
                    "watcher.observer.root_missing",
                    library_id=self._library_id,
                    path=root,
                )
                continue
            except OSError as err:
                self._log.warning(
                    "watcher.observer.schedule_failed",
                    library_id=self._library_id,
                    path=root,
                    err=str(err),
                )
                continue
            self._watches.append(watch)
            self._log.debug(
                "watcher.observer.scheduled",
                library_id=self._library_id,
                path=root,
            )

    def stop(self) -> None:
        """Detach every scheduled watch and clear local state."""
        for watch in self._watches:
            with contextlib.suppress(Exception):
                self._observer.unschedule(watch)
        self._watches.clear()

    # ----- watchdog event handlers ---------------------------------

    def on_created(self, event: FileSystemEvent) -> None:
        if isinstance(event, DirCreatedEvent | DirModifiedEvent):
            return
        if isinstance(event, FileCreatedEvent):
            self._feed(Op.CREATE, str(event.src_path))

    def on_modified(self, event: FileSystemEvent) -> None:
        if isinstance(event, DirCreatedEvent | DirModifiedEvent):
            return
        if isinstance(event, FileModifiedEvent):
            self._feed(Op.MODIFY, str(event.src_path))

    def on_moved(self, event: FileSystemEvent) -> None:
        if isinstance(event, DirMovedEvent):
            return
        if isinstance(event, FileMovedEvent):
            src = str(event.src_path)
            dest = str(event.dest_path)
            # If a partial-download basename rolls over to a final
            # extension (".part" → ".mkv"), the source was filtered
            # and the dest is what we want to track. Fall through to
            # CREATE on the dest in that case.
            src_visible = self._is_eligible(src, allow_no_ext=True)
            dest_visible = self._is_eligible(dest, allow_no_ext=False)
            if not dest_visible:
                # Renamed to an ignored or non-video name — treat as
                # the source going away.
                if src_visible:
                    self._feed(Op.DELETED, src)
                return
            if not src_visible:
                # Final-extension promotion → fresh CREATE.
                self._feed(Op.CREATE, dest)
                return
            self._feed(Op.MOVED, src, dest_path=dest)

    def on_deleted(self, event: FileSystemEvent) -> None:
        if isinstance(event, DirDeletedEvent):
            return
        if isinstance(event, FileDeletedEvent):
            self._feed(Op.DELETED, str(event.src_path))

    # ----- helpers --------------------------------------------------

    def _feed(self, op: Op, path: str, *, dest_path: str | None = None) -> None:
        # Source path filtering applies to every op. For CREATE / MODIFY
        # the path must be eligible (correct extension, not a hidden /
        # sidecar / partial-download file). For DELETED we only filter
        # ``.maktaba/`` and the ignored-basename globs — we never want
        # to silently miss a delete just because the file looked
        # uninteresting at the time of the event (the row may already
        # exist from the bootstrap scan).
        if op == Op.DELETED:
            if not self._is_path_in_tree(path):
                return
            if not self._is_eligible(path, allow_no_ext=True):
                return
        elif op == Op.MOVED:
            # MOVED already routed through eligibility checks above.
            pass
        else:
            if not self._is_eligible(path, allow_no_ext=False):
                return

        ev = RawEvent(
            library_id=self._library_id,
            op=op,
            path=path,
            dest_path=dest_path,
        )
        self._debouncer.feed(ev)

    def _is_eligible(self, path: str, *, allow_no_ext: bool) -> bool:
        """Apply the same filter rules the bootstrap walker uses."""
        if not self._is_path_in_tree(path):
            return False
        base = os.path.basename(path)
        if not base:
            return False
        if base.startswith("."):
            return False
        # ``.maktaba/`` (and any other configured sidecar) anywhere in the
        # path means we drop the event — those directories belong to the
        # pipeline and should never be ingested as media.
        sep = os.sep
        for sidecar in self._cfg.ignore_dirnames:
            marker = f"{sep}{sidecar}{sep}"
            if marker in path or path.endswith(f"{sep}{sidecar}"):
                return False
        for pattern in self._cfg.ignore_basenames:
            if fnmatch.fnmatch(base, pattern):
                return False
        _, ext = os.path.splitext(base)
        if ext == "":
            return allow_no_ext
        return ext.lower() in self._cfg.extensions

    def _is_path_in_tree(self, path: str) -> bool:
        """True iff ``path`` lives under one of the configured roots."""
        for root in self._roots:
            try:
                rel = os.path.relpath(path, root)
            except ValueError:
                # Different drives on Windows — ignore.
                continue
            if rel == "." or not rel.startswith(".."):
                return True
        return False


@dataclass(slots=True, frozen=True)
class _LibraryEntry:
    """Internal pairing of an observer adapter and its watchdog observer.

    The watchdog observer is per-library so a misbehaving library
    can't poison the others — stopping one library's observer does not
    affect any other. Plan §2.1 calls this out as a process-wide design
    choice.
    """

    observer: BaseObserver
    handler: LibraryObserver


class Watcher:
    """Process-wide live-filesystem watcher.

    One :class:`Watcher` per pipeline process. Tracks one
    :class:`LibraryObserver` per library, all of which feed into a
    single :class:`Debouncer`. Settled events flow through a bounded
    :class:`asyncio.Queue` to the dispatcher loop.

    Lifecycle:

    1. ``await watcher.start()`` — start the dispatcher loop. Idempotent.
    2. ``await watcher.add_library(lib)`` — schedule the per-library
       observer and remember the projection on the dispatcher.
    3. ``await watcher.remove_library(library_id)`` — stop the observer
       and forget the projection.
    4. ``await watcher.stop()`` — drain the queue, stop every observer,
       cancel every pending settle.
    """

    def __init__(
        self,
        store: WatcherStore,
        *,
        log: _Logger,
        config: WatcherConfig | None = None,
        observer_factory: ObserverFactory = _default_observer,
    ) -> None:
        self._store = store
        self._log = log
        self._cfg = config or WatcherConfig()
        self._observer_factory = observer_factory
        self._dispatcher = WatcherDispatcher(store, log=log)
        self._libraries: dict[UUID, _LibraryEntry] = {}
        self._loop: asyncio.AbstractEventLoop | None = None
        self._queue: asyncio.Queue[SettledEvent] | None = None
        self._consumer: asyncio.Task[None] | None = None
        self._debouncer = Debouncer(
            on_settled=self._on_settled_threadsafe,
            log=log,
            config=self._cfg.debouncer,
        )
        self._dropped_events = 0

    @property
    def dispatcher(self) -> WatcherDispatcher:
        """Expose the dispatcher for tests and metrics."""
        return self._dispatcher

    @property
    def dropped_events(self) -> int:
        """Number of settled events the queue rejected as full."""
        return self._dropped_events

    async def start(self) -> None:
        """Start the dispatcher loop. Idempotent.

        Must be awaited from the asyncio loop on which dispatches run.
        That loop is captured here and used by the threaded
        :meth:`_on_settled_threadsafe` to hand events back over.
        """
        if self._consumer is not None:
            return
        self._loop = asyncio.get_running_loop()
        self._queue = asyncio.Queue(maxsize=self._cfg.queue_capacity)
        self._consumer = self._loop.create_task(self._run_dispatch())

    async def stop(self) -> None:
        """Stop every observer and drain the dispatcher loop."""
        for entry in list(self._libraries.values()):
            entry.handler.stop()
            entry.observer.stop()
            entry.observer.join(timeout=5.0)
        self._libraries.clear()
        self._debouncer.shutdown()

        if self._consumer is not None:
            self._consumer.cancel()
            with contextlib.suppress(asyncio.CancelledError):
                await self._consumer
            self._consumer = None
        self._queue = None
        self._loop = None

    async def add_library(self, library_id: UUID) -> None:
        """Schedule a new per-library observer.

        Re-scheduling an already-watched library is a no-op (idempotent
        so callers don't have to deduplicate).
        """
        if library_id in self._libraries:
            return
        lib = await self._store.get_library(library_id)
        if lib is None:
            raise LookupError(f"library {library_id} not found")
        if not lib.roots:
            self._log.warning(
                "watcher.add_library.no_roots",
                library_id=str(lib.id),
                name=lib.name,
            )
            self._dispatcher.remember_library(lib)
            return

        observer = self._observer_factory()
        handler = LibraryObserver(
            library_id=lib.id,
            roots=lib.roots,
            observer=observer,
            debouncer=self._debouncer,
            cfg=self._cfg,
            log=self._log,
        )
        handler.start()
        observer.start()
        self._libraries[lib.id] = _LibraryEntry(observer=observer, handler=handler)
        self._dispatcher.remember_library(lib)
        self._log.info(
            "watcher.library_added",
            library_id=str(lib.id),
            roots=list(lib.roots),
        )

    async def remove_library(self, library_id: UUID) -> None:
        """Stop the per-library observer; idempotent."""
        entry = self._libraries.pop(library_id, None)
        if entry is None:
            return
        entry.handler.stop()
        entry.observer.stop()
        entry.observer.join(timeout=5.0)
        self._dispatcher.forget_library(library_id)
        self._log.info("watcher.library_removed", library_id=str(library_id))

    # ----- internals ------------------------------------------------

    def _on_settled_threadsafe(self, ev: SettledEvent) -> None:
        """Debouncer callback — runs on a timer thread.

        Hands the event off to the asyncio loop via
        :meth:`asyncio.AbstractEventLoop.call_soon_threadsafe`. If the
        bounded queue is full we drop the event with a counter bump —
        the periodic sweep (Story 9.3) is the documented backstop.
        """
        loop = self._loop
        queue = self._queue
        if loop is None or queue is None:
            return
        loop.call_soon_threadsafe(self._enqueue, ev)

    def _enqueue(self, ev: SettledEvent) -> None:
        queue = self._queue
        if queue is None:
            return
        try:
            queue.put_nowait(ev)
        except asyncio.QueueFull:
            self._dropped_events += 1
            self._log.warning(
                "watcher.queue_full",
                path=ev.path,
                op=ev.op.value,
                dropped_total=self._dropped_events,
            )

    async def _run_dispatch(self) -> None:
        assert self._queue is not None
        queue = self._queue
        while True:
            try:
                ev = await queue.get()
            except asyncio.CancelledError:
                return
            try:
                await self._dispatcher.dispatch(ev)
            except Exception as err:  # noqa: BLE001 — never tear down the loop
                self._log.error(
                    "watcher.dispatch_failed",
                    path=ev.path,
                    op=ev.op.value,
                    err=str(err),
                )
            finally:
                queue.task_done()
