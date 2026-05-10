"""Library-management behavioural layer (Epic 9).

Where the API package owns the REST surface and the scanner/watcher
modules own raw filesystem walking, this package owns the business rules
that turn a folder on disk into a curated library:

- :mod:`config` — Story 9.1, the per-library settings schema, defaults
  inheritance, and PATCH validation.
- :mod:`roots` — Story 9.16, root-path canonicalisation and overlap
  detection across all libraries.
- :mod:`ignore` — Story 9.5, the built-in + glob ignore rules shared by
  the scanner and the watcher.
- :mod:`dedup` — Story 9.4, BLAKE3 head/tail dedup decisions.
- :mod:`sweep` — Story 9.3, the periodic single-flight catalog/disk diff.
- :mod:`manual_scan` — Story 9.6, progress reporting + ?rehash.
- :mod:`audit` — Story 9.17, the append-only `audit_log` writer
  shared by the deletion (9.15), settings change (9.1), and merge (9.11)
  paths.
- :mod:`language` — Story 9.8, post-transcribe language assignment.
- :mod:`content_type` — Story 9.10, lecture/sermon/interview classifier.
- :mod:`speakers` — Story 9.11, voiceprint matching and unknown-id
  allocation.
- :mod:`topics` — Story 9.9, mini-batch k-means recluster + per-video
  assignment.
- :mod:`chapter` — Story 9.18, transcript-shift chapter inference.

Every module is import-light (no DB, no model loading at import time) so
the test suite can exercise units without spinning up a worker.
"""

from __future__ import annotations

from . import (
    audit,
    chapter,
    config,
    content_type,
    dedup,
    ignore,
    language,
    manual_scan,
    roots,
    speakers,
    sweep,
    topics,
)

__all__ = [
    "audit",
    "chapter",
    "config",
    "content_type",
    "dedup",
    "ignore",
    "language",
    "manual_scan",
    "roots",
    "speakers",
    "sweep",
    "topics",
]
