"""Async dispatcher: settled-event → Postgres transaction.

Plan §2 — every settled event becomes one DB transaction. The dispatcher
factors the four cases (CREATE / MODIFY / MOVED / DELETED) onto the
:class:`WatcherStore` Protocol so the production Postgres / SQLite store
can implement the SQL once and the test suite can drive a fully
in-memory fake.

Behaviour by op:

- **CREATE / MODIFY** — hash the file (Story 1.2 BLAKE3 head+tail+size).
  Try ``find_video_by_path`` first to decide between insert and update.
  If a row already exists at this path with the same hash, the bytes
  are unchanged and we no-op. If it exists with a *different* hash,
  the file was overwritten in place: re-hash, re-set state to
  ``discovered`` via ``rediscover``, and enqueue a fresh probe job.
  If no row exists by path, fall back to ``find_video_by_hash`` to
  detect an out-of-tree move (a file that left a ``MISSING`` row by
  the same content): if found, transition that row back to
  ``discovered`` via the FSM (Trigger.SCAN, Outcome.REDISCOVERED) and
  ``update_video_path``. Otherwise INSERT a new row + probe job.
- **MOVED** — try the source path first. If the row exists, update its
  path to ``dest_path`` (no rehash, no new probe). If the source had
  no row (e.g. the move came from outside the tree), hash the dest and
  fall through to the CREATE path.
- **DELETED** — soft-delete via the FSM: ``advance_after_stage`` with
  ``Trigger.FILESYSTEM`` + ``Outcome.DELETED`` → ``MISSING``. The
  ``WatcherStore.soft_delete_by_path`` method encapsulates the lookup +
  FSM call so the dispatcher stays SQL-free.

Everything below this layer is deliberately store-shaped: no inline SQL,
no asyncpg / aiosqlite imports. Story 1.5's connection wrapper plugs in
later by implementing :class:`WatcherStore` against the canonical
``DBConn`` Protocol.
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any, Protocol
from uuid import UUID

from ..db.pubsub import VIDEOS_NEW, get_bus
from ..identity import hash_file
from ..scanner.service import (
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
)
from .events import Op, SettledEvent

__all__ = ["WatcherDispatcher", "WatcherStore", "DispatchOutcome"]


class _Logger(Protocol):
    def info(self, event: str, **kwargs: Any) -> Any: ...
    def warning(self, event: str, **kwargs: Any) -> Any: ...
    def debug(self, event: str, **kwargs: Any) -> Any: ...
    def error(self, event: str, **kwargs: Any) -> Any: ...


class WatcherStore(Protocol):
    """Persistence boundary for the watcher dispatcher.

    A strict superset of :class:`maktaba_pipeline.scanner.ScanStore`: the
    scanner only needs ``get_library``, ``find_video_by_path``, and
    ``save_candidate``. The watcher additionally needs to look a video
    up by content hash (rename / rediscovery) and to mutate path / state
    on existing rows.

    The Postgres implementation runs each method inside its own
    transaction and relies on the slot 0004 NOTIFY trigger for the
    ``videos.state_changed`` fan-out; the SQLite implementation
    publishes manually on the in-process pubsub bus, exactly as the
    scanner does.
    """

    dialect: str

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        """Return the library projection or ``None`` if absent."""

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        """Look up the video row at ``(library_id, path)``, if any."""

    async def find_video_by_hash(
        self,
        library_id: UUID,
        content_hash: str,
    ) -> ExistingVideo | None:
        """Look up the video row by ``(library_id, content_hash)``, if any.

        Used by the rename / out-of-tree-move branch to recover the
        existing row's identity without a fresh INSERT.
        """

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        """Insert (video, probe-job) atomically. Idempotent on conflict."""

    async def update_video_path(
        self,
        video_id: UUID,
        new_path: str,
    ) -> None:
        """Update only the ``videos.path`` column. No state change.

        The FSM is not consulted: a same-library rename does not
        re-trigger the pipeline. The slot 0004 ``updated_at`` trigger
        bumps the row's mtime as a side-effect.
        """

    async def rediscover(
        self,
        video_id: UUID,
        new_path: str,
    ) -> None:
        """Transition a ``MISSING`` row back to ``DISCOVERED``.

        Implementations must call
        :func:`maktaba_pipeline.orchestrator.advance.advance_after_stage`
        with ``Trigger.SCAN`` + ``Outcome.REDISCOVERED`` so the canonical
        FSM table is honoured (story 1.6 explicit transition). The path
        is updated alongside the state in the same transaction.
        """

    async def soft_delete_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> UUID | None:
        """Transition the row at ``(library_id, path)`` to ``MISSING``.

        Returns the affected ``video_id`` or ``None`` if no row matched.
        Implementations must call ``advance_after_stage`` with
        ``Trigger.FILESYSTEM`` + ``Outcome.DELETED`` so derived rows
        (transcripts, index entries) are preserved.
        """


@dataclass(slots=True, frozen=True)
class DispatchOutcome:
    """Diagnostic record returned by :meth:`WatcherDispatcher.dispatch`.

    Tests assert against this; production logs only the summary fields.
    ``video_id`` is ``None`` when the dispatch was a no-op (e.g. an
    unchanged-content MODIFY) or the file was already gone.
    """

    op: Op
    path: str
    video_id: UUID | None
    inserted: bool = False
    updated_path: bool = False
    rediscovered: bool = False
    soft_deleted: bool = False
    no_op: bool = False


class WatcherDispatcher:
    """Async dispatcher: one settled event → one DB transaction.

    The dispatcher caches :class:`LibraryRecord` projections to avoid
    a per-event ``get_library`` round-trip. Cache invalidation happens
    on library removal; the watcher service evicts the entry when it
    tears down the per-library observer.
    """

    def __init__(self, store: WatcherStore, *, log: _Logger) -> None:
        self._store = store
        self._log = log
        self._library_cache: dict[UUID, LibraryRecord] = {}

    def remember_library(self, lib: LibraryRecord) -> None:
        """Cache a :class:`LibraryRecord` so dispatches skip a fetch."""
        self._library_cache[lib.id] = lib

    def forget_library(self, library_id: UUID) -> None:
        """Drop the cached projection for ``library_id``."""
        self._library_cache.pop(library_id, None)

    async def dispatch(self, ev: SettledEvent) -> DispatchOutcome:
        """Handle one :class:`SettledEvent` end-to-end."""
        lib_id = UUID(ev.library_id)
        lib = self._library_cache.get(lib_id)
        if lib is None:
            fresh = await self._store.get_library(lib_id)
            if fresh is None:
                self._log.warning(
                    "watcher.dispatch.unknown_library",
                    library_id=str(lib_id),
                    path=ev.path,
                )
                return DispatchOutcome(op=ev.op, path=ev.path, video_id=None, no_op=True)
            lib = fresh
            self._library_cache[lib.id] = lib

        if ev.op == Op.DELETED:
            return await self._handle_delete(lib, ev)
        if ev.op == Op.MOVED:
            return await self._handle_move(lib, ev)
        return await self._handle_upsert(lib, ev)

    async def _handle_delete(
        self,
        lib: LibraryRecord,
        ev: SettledEvent,
    ) -> DispatchOutcome:
        video_id = await self._store.soft_delete_by_path(lib.id, ev.path)
        if video_id is None:
            self._log.debug(
                "watcher.dispatch.delete_no_row",
                library_id=str(lib.id),
                path=ev.path,
            )
            return DispatchOutcome(op=Op.DELETED, path=ev.path, video_id=None, no_op=True)
        self._log.info(
            "watcher.dispatch.soft_deleted",
            library_id=str(lib.id),
            video_id=str(video_id),
            path=ev.path,
        )
        return DispatchOutcome(
            op=Op.DELETED,
            path=ev.path,
            video_id=video_id,
            soft_deleted=True,
        )

    async def _handle_move(
        self,
        lib: LibraryRecord,
        ev: SettledEvent,
    ) -> DispatchOutcome:
        if ev.dest_path is None:
            # Watchdog can emit a "moved out of tree" event with no
            # dest. Treat as a delete (the caller should already have
            # ignored cross-watch moves; this is a defensive net).
            return await self._handle_delete(lib, ev)

        existing = await self._store.find_video_by_path(lib.id, ev.path)
        if existing is not None:
            await self._store.update_video_path(existing.id, ev.dest_path)
            self._log.info(
                "watcher.dispatch.path_updated",
                library_id=str(lib.id),
                video_id=str(existing.id),
                old_path=ev.path,
                new_path=ev.dest_path,
            )
            return DispatchOutcome(
                op=Op.MOVED,
                path=ev.dest_path,
                video_id=existing.id,
                updated_path=True,
            )

        # No row at the source — fall through to the upsert path on the
        # destination. This handles the "atomic mv from outside watched
        # root" edge case in the story spec: hash the destination, see
        # if its content matches a MISSING row, treat as rediscovery.
        synthetic = SettledEvent(
            library_id=ev.library_id,
            op=Op.CREATE,
            path=ev.dest_path,
            size_bytes=ev.size_bytes,
            mtime_ns=ev.mtime_ns,
        )
        return await self._handle_upsert(lib, synthetic)

    async def _handle_upsert(
        self,
        lib: LibraryRecord,
        ev: SettledEvent,
    ) -> DispatchOutcome:
        # Library disabled? Story 1.1 still inserts rows but does not
        # enqueue probe jobs. Mirror that here so manual control via
        # Story 1.4 keeps working when a watcher is running.
        try:
            hash_result = hash_file(ev.path)
        except FileNotFoundError:
            # File vanished between settle and dispatch — ignore. The
            # subsequent DELETED event will clean up.
            self._log.debug("watcher.dispatch.gone_before_hash", path=ev.path)
            return DispatchOutcome(op=ev.op, path=ev.path, video_id=None, no_op=True)
        except (OSError, ValueError) as err:
            self._log.error(
                "watcher.dispatch.hash_failed",
                path=ev.path,
                err=str(err),
            )
            return DispatchOutcome(op=ev.op, path=ev.path, video_id=None, no_op=True)

        existing_by_path = await self._store.find_video_by_path(lib.id, ev.path)
        if (
            existing_by_path is not None
            and existing_by_path.content_hash == hash_result.content_hash
        ):
            # Same path, same bytes: a redundant Modified event (e.g.
            # touch with no content change). No state to mutate.
            self._log.debug(
                "watcher.dispatch.unchanged",
                video_id=str(existing_by_path.id),
                path=ev.path,
            )
            return DispatchOutcome(
                op=ev.op,
                path=ev.path,
                video_id=existing_by_path.id,
                no_op=True,
            )

        # Out-of-tree move (or rename across libraries) → look the
        # content up. find_video_by_hash returns the row regardless of
        # current state; the rediscover() helper enforces the FSM.
        existing_by_hash = await self._store.find_video_by_hash(lib.id, hash_result.content_hash)
        if existing_by_hash is not None and existing_by_path is None:
            await self._store.rediscover(existing_by_hash.id, ev.path)
            self._log.info(
                "watcher.dispatch.rediscovered",
                library_id=str(lib.id),
                video_id=str(existing_by_hash.id),
                path=ev.path,
            )
            return DispatchOutcome(
                op=ev.op,
                path=ev.path,
                video_id=existing_by_hash.id,
                rediscovered=True,
            )

        # Brand new content (or a same-path-different-bytes overwrite —
        # save_candidate must absorb the conflict on (library_id,
        # content_hash) and return inserted=False if another scan has
        # already claimed the hash).
        params = SaveCandidateParams(
            library_id=lib.id,
            content_hash=hash_result.content_hash,
            path=ev.path,
            filename=os.path.basename(ev.path),
            size_bytes=hash_result.size_bytes,
            mtime=_mtime_ns_to_db(ev.mtime_ns),
            last_seen_at=datetime.now(tz=UTC),
            enqueue_probe=not lib.disabled,
        )
        save = await self._store.save_candidate(params)

        if save.inserted:
            self._log.info(
                "watcher.dispatch.inserted",
                library_id=str(lib.id),
                video_id=str(save.video_id),
                path=ev.path,
                content_hash=hash_result.content_hash,
            )
            if self._store.dialect == "sqlite":
                # Mirror the scanner's manual NOTIFY fan-out so an
                # in-process subscriber sees the same event shape on
                # SQLite as on Postgres.
                get_bus().publish(
                    VIDEOS_NEW,
                    {
                        "id": str(save.video_id),
                        "library_id": str(lib.id),
                        "content_hash": hash_result.content_hash,
                        "path": ev.path,
                        "filename": params.filename,
                        "state": "discovered",
                    },
                )
            return DispatchOutcome(
                op=ev.op,
                path=ev.path,
                video_id=save.video_id,
                inserted=True,
            )

        # ON CONFLICT swallowed the insert: another mutation got there
        # first (concurrent scan, racing watcher). The dispatcher keeps
        # the existing row's id and treats the event as a no-op.
        self._log.debug(
            "watcher.dispatch.conflict_swallowed",
            library_id=str(lib.id),
            video_id=str(save.video_id),
            path=ev.path,
        )
        return DispatchOutcome(
            op=ev.op,
            path=ev.path,
            video_id=save.video_id,
            no_op=True,
        )


def _mtime_ns_to_db(mtime_ns: int) -> datetime:
    """Convert a nanosecond mtime to a UTC datetime at microsecond precision.

    Mirrors the scanner's helper so settled events land at the same
    resolution Postgres ``TIMESTAMPTZ`` records.
    """
    if mtime_ns <= 0:
        return datetime.now(tz=UTC)
    seconds, ns_remainder = divmod(int(mtime_ns), 1_000_000_000)
    micros = ns_remainder // 1_000
    base = datetime.fromtimestamp(seconds, tz=UTC)
    return base.replace(microsecond=micros)
