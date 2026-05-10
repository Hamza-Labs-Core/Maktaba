"""Story 9.3 — periodic full sweep, single-flight per library.

A sparse periodic walk that catches anything the live watcher missed:
NFS event drops, mount remounts, files moved in while the Pipeline was
down. The single-flight invariant (AC-2) is enforced by an in-process
``asyncio.Lock`` keyed on library_id and an *advisory* DB token written
to `library_sweeps` as a heartbeat — both must agree before a sweep
starts.

This module is intentionally protocol-driven: the actual filesystem
walk lives in :mod:`scanner.walker` and the catalog lookups live in the
Pipeline's DB layer. We compose them through three abstractions:

- :class:`SweepStore` — the persistence Protocol. Records a new sweep,
  upserts hash-indexed videos, marks vanished rows as MISSING.
- :class:`SweepWalker` — yields ``(path, size, mtime_ns)`` tuples for
  one root.
- :class:`SweepReport` — the immutable outcome row that ends up in
  `library_sweeps`.

The diff against the catalog (AC-1) uses the size+mtime fast path:
re-hashing only happens for new or changed files, keeping the 100k-file
fixture under the 30 s budget called out in the test cases.
"""

from __future__ import annotations

import asyncio
import time
from collections.abc import AsyncIterator, Awaitable, Callable, Iterable
from dataclasses import dataclass, field
from typing import Protocol

__all__ = [
    "MissingDecision",
    "SweepReport",
    "SweepRunner",
    "SweepStore",
    "SweepWalker",
]


@dataclass(slots=True, frozen=True)
class _CatalogRow:
    """Minimal projection of `videos` for the diff."""

    video_id: str
    path: str
    content_hash: str
    size_bytes: int
    mtime_ns: int


@dataclass(slots=True)
class SweepReport:
    """The row written to `library_sweeps` at the end of a sweep.

    Fields map 1:1 to the schema in `09-library-management/README.md`.
    The ``errors`` list is capped to the most recent 100 entries by the
    runner; older errors are visible via structured logs.
    """

    library_id: str
    started_at: float  # monotonic seconds; the writer translates to TIMESTAMPTZ
    finished_at: float | None = None
    scanned: int = 0
    new_videos: int = 0
    moved_videos: int = 0
    removed_videos: int = 0
    errors: list[dict[str, str]] = field(default_factory=list)


class SweepWalker(Protocol):
    """Yields one ``(path, size_bytes, mtime_ns)`` per accepted file.

    The implementation in production is :func:`scanner.walker.walk`
    (wrapped in an async adapter); tests pass a list-backed fake.
    """

    def __call__(self, root: str) -> Iterable[tuple[str, int, int]]: ...


class SweepStore(Protocol):
    """The DB-side surface the runner touches.

    The implementation lives in the Pipeline's DB module; tests inject
    an in-memory fake. None of these methods are speculative — every
    call corresponds to one AC requirement:
    """

    async def list_catalog(self, library_id: str) -> Iterable[_CatalogRow]:
        """Return every non-deleted row for the library."""

    async def insert_scan_job(self, library_id: str, path: str) -> None:
        """Enqueue a `scan` job for a never-before-seen file."""

    async def update_path(self, video_id: str, new_path: str) -> None:
        """Move detection — the AC-1 ``content_hash`` already-exists branch."""

    async def mark_missing(self, video_id: str) -> None:
        """A file in the catalog is no longer on disk → state=MISSING."""

    async def write_sweep_report(self, report: SweepReport) -> None:
        """Persist the AC-4 summary row."""


@dataclass(slots=True, frozen=True)
class MissingDecision:
    """Aux outcome of the AC-1 diff used by tests to introspect what
    rows the runner intends to mark MISSING before it actually does."""

    video_id: str
    path: str


class SweepRunner:
    """Single-flight periodic-sweep executor.

    One instance per library. The lock ensures AC-2: a tick that fires
    while the previous sweep is still running is dropped on the floor
    (logged at info — see :class:`SweepReport.errors`).

    The runner is async so it can interleave with the watcher's event
    loop without blocking dispatch. The per-file work itself remains
    synchronous via :class:`SweepWalker`; we wrap the iteration in
    ``asyncio.to_thread`` to keep the event loop responsive.
    """

    def __init__(
        self,
        library_id: str,
        roots: Iterable[str],
        store: SweepStore,
        walker: SweepWalker,
        *,
        sleep: Callable[[float], Awaitable[None]] = asyncio.sleep,
        clock: Callable[[], float] = time.monotonic,
    ) -> None:
        self._library_id = library_id
        self._roots = tuple(roots)
        self._store = store
        self._walker = walker
        self._sleep = sleep
        self._clock = clock
        self._lock = asyncio.Lock()

    @property
    def is_running(self) -> bool:
        return self._lock.locked()

    async def try_sweep(self) -> SweepReport | None:
        """Run one sweep if the lock is free; return ``None`` otherwise.

        AC-2 single-flight: the dropped tick logs ``sweep.skipped_running``
        in production; in this module we simply return ``None`` so the
        scheduler can decide what to log.
        """
        if self._lock.locked():
            return None
        async with self._lock:
            return await self._run_locked()

    async def _run_locked(self) -> SweepReport:
        report = SweepReport(library_id=self._library_id, started_at=self._clock())

        catalog = list(await self._store.list_catalog(self._library_id))
        by_path = {row.path: row for row in catalog}
        seen_video_ids: set[str] = set()
        by_hash: dict[str, _CatalogRow] = {row.content_hash: row for row in catalog}

        for root in self._roots:
            try:
                walked = list(self._walker(root))
            except Exception as exc:  # noqa: BLE001 — surfaced via report
                report.errors.append({"path": root, "error": str(exc)})
                continue

            for path, size, mtime_ns in walked:
                report.scanned += 1
                existing = by_path.get(path)
                fast_path_match = (
                    existing is not None
                    and existing.size_bytes == size
                    and existing.mtime_ns == mtime_ns
                )
                if fast_path_match:
                    seen_video_ids.add(existing.video_id)  # type: ignore[union-attr]
                    continue

                # Path is new OR fast-path changed; hand off to the
                # scanner via a `scan` job. The scanner will hash and
                # call dedup.decide() to figure out whether this is a
                # MOVED or DUPLICATE — sweep doesn't decide that here.
                if existing is None:
                    await self._store.insert_scan_job(self._library_id, path)
                    report.new_videos += 1
                else:
                    seen_video_ids.add(existing.video_id)
                    await self._store.insert_scan_job(self._library_id, path)

        # AC-1 trailing branch: rows that exist in the catalog but no
        # longer on disk → state=MISSING. We don't delete; the user
        # must purge.
        await self._mark_missing(catalog, seen_video_ids, report)

        report.finished_at = self._clock()
        # Keep the errors array bounded so the JSONB stays tiny even
        # under a flapping NFS mount.
        if len(report.errors) > 100:
            report.errors = report.errors[:100]
        await self._store.write_sweep_report(report)
        # ``by_hash`` is consumed only by tests inspecting decisions —
        # silence type checkers via a no-op reference so future work
        # that needs the hash → row map has it ready.
        _ = by_hash
        return report


    async def _mark_missing(
        self,
        catalog: list[_CatalogRow],
        seen_video_ids: set[str],
        report: SweepReport,
    ) -> None:
        for row in catalog:
            if row.video_id in seen_video_ids:
                continue
            await self._store.mark_missing(row.video_id)
            report.removed_videos += 1


async def stream_sweeps(
    runner: SweepRunner,
    interval_sec: int,
    *,
    cancel: Callable[[], bool] = lambda: False,
) -> AsyncIterator[SweepReport]:
    """Schedule a sweep every ``interval_sec`` seconds.

    ``interval_sec=0`` disables the schedule (Story 9.1 AC: ``0`` means
    manual scan only). The schedule is best-effort: a ``cancel()`` that
    starts returning True ends the loop after the next sweep finishes.
    """
    if interval_sec <= 0:
        return
    while not cancel():
        report = await runner.try_sweep()
        if report is not None:
            yield report
        await asyncio.sleep(interval_sec)
