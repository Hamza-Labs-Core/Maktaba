"""Library scanner — bootstrap walk over a library's roots (Story 1.1).

The scanner is the source of every newly-discovered ``videos`` row in
Maktaba. Given a library and its configured roots, it walks each root
once, hashes each candidate file with the head+tail+size BLAKE3 formula
from Story 1.2, INSERTs a ``videos`` row, and enqueues a ``probe`` job
on ``processing_jobs``. The slot 0005 ``videos_notify_new_trg`` trigger
fans the insert out to the API's WebSocket layer via
``pg_notify('videos.new', …)``; SQLite callers receive the same payload
shape via the in-process :class:`PubsubBus`.

Public surface:

- :func:`walk` — pure filesystem walker that yields :class:`Candidate`
  records (path + signature) for every file matching the configured
  extensions, with hidden / partial-download / sidecar entries filtered
  out and permission errors swallowed.
- :class:`WalkConfig` — knobs for :func:`walk` (extensions, ignore
  globs, follow-symlinks toggle).
- :class:`Candidate` — one accepted file with its size + mtime.
- :class:`Scanner`, :class:`ScanConfig`, :class:`ScanResult`,
  :class:`LibraryRecord` — the orchestrator and the records it returns.

The walker stays a separate module because it has no DB, hashing, or
async dependencies — it's importable on its own for unit tests and can
be reused by the filesystem watcher in Story 1.3.
"""

from __future__ import annotations

from .service import (
    ExistingVideo,
    LibraryRecord,
    SaveCandidateParams,
    SaveCandidateResult,
    ScanConfig,
    ScanError,
    Scanner,
    ScanResult,
    ScanStore,
)
from .walker import (
    DEFAULT_IGNORE_BASENAMES,
    DEFAULT_IGNORE_DIRNAMES,
    DEFAULT_VIDEO_EXTENSIONS,
    Candidate,
    WalkConfig,
    walk,
)

__all__ = [
    "DEFAULT_IGNORE_BASENAMES",
    "DEFAULT_IGNORE_DIRNAMES",
    "DEFAULT_VIDEO_EXTENSIONS",
    "Candidate",
    "ExistingVideo",
    "LibraryRecord",
    "SaveCandidateParams",
    "SaveCandidateResult",
    "ScanConfig",
    "ScanError",
    "ScanResult",
    "ScanStore",
    "Scanner",
    "WalkConfig",
    "walk",
]
