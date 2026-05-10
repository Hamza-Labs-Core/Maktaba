"""Data integrity helpers (Epic 24).

Four surfaces:

* :mod:`atomic` — durable atomic file writes (temp-file + rename + fsync).
* :mod:`idempotency` — idempotency-key store helpers so retried Pipeline
  jobs do not double-commit segments.
* :mod:`backup` — backup/restore planner that writes JSON manifests.
* :mod:`verify` — per-video integrity check (file present, size match,
  hash match, segment count). Writes a row to ``integrity_checks``.
"""

from .atomic import AtomicWriteError, atomic_write_bytes, atomic_write_text
from .backup import BackupManifest, BackupPlanner
from .idempotency import IdempotencyKey, IdempotencyStore, MemoryIdempotencyStore
from .verify import IntegrityResult, verify_video

__all__ = [
    "AtomicWriteError",
    "BackupManifest",
    "BackupPlanner",
    "IdempotencyKey",
    "IdempotencyStore",
    "IntegrityResult",
    "MemoryIdempotencyStore",
    "atomic_write_bytes",
    "atomic_write_text",
    "verify_video",
]
