"""Cancel-polling and dry-run tests for the scanner orchestrator.

Story 1.4 AC #2 (cancellation): a scanner that observes
``cancel_requested`` mid-walk exits at the next file boundary with
:class:`ScanCancelled`, leaves no half-inserted rows, and never
returns a non-error result.

Story 1.4 AC #3 (dry-run): a scanner driven through :class:`DryRunStore`
walks the same number of candidates as a normal store, emits one
JSONL line per file, and writes nothing back through the polling
control hooks (which a dry-run skips entirely).
"""

from __future__ import annotations

import io
import json
import os
import struct
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.scanner import (
    DryRunStore,
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
    ScanCancelled,
    ScanLibraryDeleted,
    Scanner,
    ScanOptions,
)
from maktaba_pipeline.scanner.service import ScanControl

# ---------------------------------------------------------------------------
# helpers
# ---------------------------------------------------------------------------


@dataclass
class _LoggerSink:
    events: list[tuple[str, str, dict[str, Any]]] = field(default_factory=list)

    def info(self, event: str, **kwargs: Any) -> None:
        self.events.append(("info", event, kwargs))

    def warning(self, event: str, **kwargs: Any) -> None:
        self.events.append(("warning", event, kwargs))

    def debug(self, event: str, **kwargs: Any) -> None:
        self.events.append(("debug", event, kwargs))

    def error(self, event: str, **kwargs: Any) -> None:
        self.events.append(("error", event, kwargs))

    def names(self, level: str) -> list[str]:
        return [event for lvl, event, _ in self.events if lvl == level]


@dataclass
class _ControlAwareStore:
    """Fake ScanStore + ScanControlStore that tracks every poll.

    ``cancel_after`` is the number of polls before a poll returns
    ``cancel_requested=True``. Set it to ``-1`` to disable. Same for
    ``deleted_after``. The store records every call so tests can
    assert the orchestrator's exact behaviour at each boundary.
    """

    library: LibraryRecord
    cancel_after: int = -1
    deleted_after: int = -1
    poll_calls: list[tuple[int, int]] = field(default_factory=list)
    cleared: int = 0
    last_error: str | None = None

    saved: list[SaveCandidateParams] = field(default_factory=list)
    dialect: str = "postgres"

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        if library_id != self.library.id:
            return None
        return self.library

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        return None

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        self.saved.append(params)
        return SaveCandidateResult(video_id=uuid4(), inserted=True, job_id=1)

    async def clear_scan_control(self, library_id: UUID) -> None:
        self.cleared += 1

    async def poll_scan_control(
        self,
        library_id: UUID,
        files_walked: int,
        files_inserted: int,
    ) -> ScanControl:
        self.poll_calls.append((files_walked, files_inserted))
        n = len(self.poll_calls)
        cancel = self.cancel_after >= 0 and n > self.cancel_after
        deleted = self.deleted_after >= 0 and n > self.deleted_after
        pct = min(99.0, files_walked * 1.0)
        return ScanControl(
            cancel_requested=cancel,
            progress_pct=pct,
            library_deleted=deleted,
        )

    async def record_scan_error(
        self,
        library_id: UUID,
        message: str,
    ) -> None:
        self.last_error = message


# Minimal MP4 header so the existing walker accepts the file as a
# media candidate. Story 1.1's walker filters by extension, so a
# non-empty file with the right extension is sufficient.
def _spawn_files(root: Path, n: int, ext: str = ".mp4") -> list[Path]:
    paths: list[Path] = []
    for i in range(n):
        p = root / f"v{i:04d}{ext}"
        # Each file gets unique bytes so content_hash differs.
        p.write_bytes(struct.pack(">Q", i) * 64)
        paths.append(p)
    # Touch with deterministic mtimes for log readability.
    for i, p in enumerate(paths):
        os.utime(p, (time.time() - i, time.time() - i))
    return paths


# ---------------------------------------------------------------------------
# tests
# ---------------------------------------------------------------------------


@pytest.mark.asyncio
async def test_cancel_polled_and_raises_within_one_boundary(tmp_path: Path) -> None:
    """A store that flips cancel_requested mid-walk causes ScanCancelled."""
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    store = _ControlAwareStore(library=library, cancel_after=1)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=20)

    with pytest.raises(ScanCancelled) as excinfo:
        await scanner.run(library.id, ScanOptions(cancel_poll_every=5))

    # Two polls: the first returns cancel=False, the second cancel=True.
    assert len(store.poll_calls) == 2
    # Cancellation observed before all files were processed.
    assert excinfo.value.result.files_walked < 20
    # The orchestrator persisted the error so the GET handler can
    # surface it.
    assert store.last_error == "cancelled"
    # Pre-walk cleared exactly once.
    assert store.cleared == 1


@pytest.mark.asyncio
async def test_no_cancel_runs_to_completion(tmp_path: Path) -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    store = _ControlAwareStore(library=library)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=15)

    result = await scanner.run(library.id, ScanOptions(cancel_poll_every=5))

    assert result.files_walked == 15
    assert result.files_inserted == 15
    assert store.last_error is None
    # Three polls: at file 5, 10, 15.
    assert len(store.poll_calls) == 3
    assert store.poll_calls[-1] == (15, 15)


@pytest.mark.asyncio
async def test_library_deleted_mid_scan_aborts(tmp_path: Path) -> None:
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    store = _ControlAwareStore(library=library, deleted_after=1)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=20)

    with pytest.raises(ScanLibraryDeleted) as excinfo:
        await scanner.run(library.id, ScanOptions(cancel_poll_every=5))

    assert store.last_error == "library_deleted"
    assert excinfo.value.result.files_walked < 20


@pytest.mark.asyncio
async def test_dry_run_skips_polling_and_clearing(tmp_path: Path) -> None:
    """Dry-run path uses the DryRunStore; no clear, no poll, no DB writes."""
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    buf = io.StringIO()
    store = DryRunStore(library=library, writer=buf)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=10)

    result = await scanner.run(library.id, ScanOptions(dry_run=True, cancel_poll_every=2))

    assert result.files_walked == 10
    assert result.files_inserted == 10  # DryRunStore reports inserted=True
    lines = buf.getvalue().splitlines()
    assert len(lines) == 10
    decoded = [json.loads(line) for line in lines]
    assert all(d["action"] == "would_insert" for d in decoded)


@pytest.mark.asyncio
async def test_dry_run_against_control_store_still_skips_poll(tmp_path: Path) -> None:
    """Even if a ControlStore is wired, dry_run=True bypasses the poll."""
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    store = _ControlAwareStore(library=library, cancel_after=0)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=10)

    # cancel_after=0 means the *first* poll returns cancel=True. If the
    # dry-run path were polling, this would raise ScanCancelled — but
    # it doesn't, because dry-runs never poll.
    result = await scanner.run(library.id, ScanOptions(dry_run=True, cancel_poll_every=2))

    assert result.files_walked == 10
    assert store.poll_calls == []
    assert store.cleared == 0


@pytest.mark.asyncio
async def test_clear_invoked_before_walk(tmp_path: Path) -> None:
    """The pre-walk clear runs once per scan, before any candidate is read."""
    library = LibraryRecord(id=uuid4(), name="lib", roots=(str(tmp_path),))
    store = _ControlAwareStore(library=library)
    scanner = Scanner(store=store, log=_LoggerSink())

    _spawn_files(tmp_path, n=3)

    await scanner.run(library.id, ScanOptions(cancel_poll_every=10))

    assert store.cleared == 1
