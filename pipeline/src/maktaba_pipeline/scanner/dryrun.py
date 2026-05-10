"""Dry-run :class:`ScanStore` (Story 1.4 AC #3).

A :class:`DryRunStore` walks the same orchestrator path as the real
store but emits one JSONL line per candidate to a configured writer
instead of touching the database. The CLI plumbs ``sys.stdout``;
tests pipe in a :class:`io.StringIO` and assert on the captured lines.

Concurrent walkers are not used inside one scan today, but the lock
is taken anyway so the implementation stays drop-in safe if a future
plan adds a hash pool.
"""

from __future__ import annotations

import json
import threading
from datetime import datetime
from typing import TYPE_CHECKING, TextIO
from uuid import UUID, uuid4

if TYPE_CHECKING:
    from .service import ExistingVideo, LibraryRecord, SaveCandidateParams, SaveCandidateResult

__all__ = ["DryRunStore"]


class DryRunStore:
    """In-memory ScanStore that prints would-be inserts and writes nothing.

    The store also holds the synthetic :class:`LibraryRecord` the CLI
    builds from a ``--root`` flag so the orchestrator's
    :meth:`get_library` call resolves without a DB.
    """

    dialect: str = "sqlite"

    def __init__(self, library: LibraryRecord, writer: TextIO) -> None:
        self._library = library
        self._writer = writer
        self._lock = threading.Lock()

    async def get_library(self, library_id: UUID) -> LibraryRecord | None:
        if library_id != self._library.id:
            return None
        return self._library

    async def find_video_by_path(
        self,
        library_id: UUID,
        path: str,
    ) -> ExistingVideo | None:
        # Dry-run never has a prior row — the scanner therefore always
        # falls through to the hash + would_insert path.
        return None

    async def save_candidate(
        self,
        params: SaveCandidateParams,
    ) -> SaveCandidateResult:
        from .service import SaveCandidateResult as _Result

        line = {
            "action": "would_insert",
            "library_id": str(params.library_id),
            "content_hash": params.content_hash,
            "path": params.path,
            "filename": params.filename,
            "size_bytes": params.size_bytes,
            "mtime": _iso(params.mtime),
            "enqueue_probe": params.enqueue_probe,
        }
        encoded = json.dumps(line, sort_keys=True)
        with self._lock:
            self._writer.write(encoded + "\n")
        # Synthetic id; no row is written. Caller treats every dry-run
        # file as "inserted" so totals match a live run.
        return _Result(video_id=uuid4(), inserted=True, job_id=None)


def _iso(dt: datetime) -> str:
    return dt.isoformat()
