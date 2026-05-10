"""Story 9.6 — manual-scan trigger and progress reporting.

The HTTP entry point lives in the API package
(``api/internal/handlers/libraries`` ``Scan`` handler); this module
implements the *worker side*: how the scan job reports progress against
the canonical `processing_jobs` schema, and how the ``?rehash=true``
mode overrides the size+mtime fast path.

Progress repurposing (AC-3):

    processing_jobs.processed_seconds  →  files scanned
    processing_jobs.total_duration_sec →  files to scan (estimate)

The estimate comes from a fast pre-walk count (``count_files``); on a
50k-file library this is ~3 s on local SSD. Without the estimate the WS
event would have no denominator so the UI couldn't render a progress
bar.

The ``?rehash=true`` toggle (AC-2) is implemented as a ``ScanMode``
enum so the worker, the watcher, and the test fixtures all agree on
the vocabulary instead of passing booleans around.
"""

from __future__ import annotations

import time
from collections.abc import Callable, Iterable
from dataclasses import dataclass
from enum import StrEnum
from typing import Protocol

__all__ = [
    "PROGRESS_TICK_HZ",
    "ProgressReporter",
    "ScanMode",
    "ScanProgress",
    "count_files",
    "should_rehash",
]


#: AC-3 wires the worker to push at most one progress update per second
#: to keep the WS quiet on big libraries. The runner samples at this
#: rate; events between ticks are coalesced.
PROGRESS_TICK_HZ: float = 1.0


class ScanMode(StrEnum):
    """Vocabulary the worker uses to choose the per-file branch."""

    DEFAULT = "default"
    """Honour the size+mtime fast path; only re-hash on change."""

    REHASH = "rehash"
    """Re-hash every file regardless of size+mtime (AC-2)."""


def count_files(
    roots: Iterable[str],
    walker: Callable[[str], Iterable[str]],
) -> int:
    """Fast pre-walk count for the AC-3 progress denominator.

    The walker should be the same one used by the scan itself so the
    count and the per-file visit are guaranteed to agree (no
    "estimated 1000, scanned 1043" drift). Caller passes a thin wrapper
    around :func:`scanner.walker.walk` that yields paths only.
    """
    total = 0
    for root in roots:
        for _ in walker(root):
            total += 1
    return total


def should_rehash(mode: ScanMode, fast_path_match: bool) -> bool:
    """Decide whether to re-hash the file at hand.

    - ``REHASH`` mode always re-hashes (AC-2).
    - ``DEFAULT`` mode re-hashes only when the size+mtime fast path
      missed (the scanner has already determined this).
    """
    if mode == ScanMode.REHASH:
        return True
    return not fast_path_match


@dataclass(slots=True)
class ScanProgress:
    """One tick of scan progress.

    Mirrors the WS event shape so the worker can hand the dataclass
    straight to the structured-logger / pubsub sink without further
    munging. ``files_scanned`` ↔ ``processed_seconds`` and
    ``files_to_scan`` ↔ ``total_duration_seconds`` per AC-3.
    """

    job_id: int
    files_scanned: int
    files_to_scan: int
    started_at: float
    last_path: str | None = None

    @property
    def fraction(self) -> float:
        if self.files_to_scan <= 0:
            return 0.0
        return min(1.0, self.files_scanned / self.files_to_scan)

    @property
    def elapsed_sec(self) -> float:
        return time.monotonic() - self.started_at

    @property
    def estimated_remaining_sec(self) -> float | None:
        if self.files_scanned <= 0:
            return None
        rate = self.files_scanned / max(self.elapsed_sec, 1e-3)
        remaining_files = max(0, self.files_to_scan - self.files_scanned)
        if rate <= 0:
            return None
        return remaining_files / rate


class _ProgressSink(Protocol):
    """Where ticks land. In production this is the
    :func:`db.jobs_progress.update_progress` writer wired to NOTIFY."""

    async def __call__(self, progress: ScanProgress) -> None: ...


class ProgressReporter:
    """Coalesces per-file ticks down to ~1 Hz.

    Every per-file completion calls :meth:`note_file`; the reporter
    decides whether to flush by comparing the wall-clock to the last
    flush. The final tick is always flushed via :meth:`finish` so the
    UI sees ``files_scanned == files_to_scan``.
    """

    def __init__(
        self,
        job_id: int,
        files_to_scan: int,
        sink: _ProgressSink,
        *,
        clock: Callable[[], float] = time.monotonic,
        tick_hz: float = PROGRESS_TICK_HZ,
    ) -> None:
        self._job_id = job_id
        self._files_to_scan = files_to_scan
        self._sink = sink
        self._clock = clock
        self._interval = 1.0 / tick_hz if tick_hz > 0 else 1.0
        self._files_scanned = 0
        # Initialise to ``-interval`` so the first :meth:`note_file`
        # always flushes immediately — the WS gets a 0/N tick before
        # any work starts so the UI bar appears straight away.
        self._started = clock()
        self._last_flush = self._started - self._interval
        self._last_path: str | None = None

    @property
    def files_scanned(self) -> int:
        return self._files_scanned

    async def note_file(self, path: str) -> None:
        """Record a per-file completion. Flushes if the tick window has
        elapsed; otherwise just bumps the counter."""
        self._files_scanned += 1
        self._last_path = path
        now = self._clock()
        if now - self._last_flush >= self._interval:
            await self._flush(now)

    async def finish(self) -> None:
        """Force a final flush so the UI sees the terminal state."""
        await self._flush(self._clock())

    async def _flush(self, now: float) -> None:
        self._last_flush = now
        await self._sink(
            ScanProgress(
                job_id=self._job_id,
                files_scanned=self._files_scanned,
                files_to_scan=self._files_to_scan,
                started_at=self._started,
                last_path=self._last_path,
            )
        )
