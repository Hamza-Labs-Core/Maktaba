"""Story 9.4 — content-hash deduplication decisions.

Identity is BLAKE3 over first 4 MiB + last 4 MiB + size_bytes_le, and
the heavy lifting (the actual hash) lives in
:mod:`maktaba_pipeline.identity.hasher`. This module owns the *decision*
that turns a freshly-hashed file into one of:

- :class:`DedupOutcome.NEW`     — no row exists with this hash; insert.
- :class:`DedupOutcome.MOVED`   — a row exists but at a different path;
                                  update the path (single-write rename
                                  detection per AC-2).
- :class:`DedupOutcome.DUPLICATE` — a row exists at a different on-disk
                                  path *that still exists*; record the
                                  duplicate in the audit log and keep
                                  the catalog pointing to the old entry.

The path-canonicalisation guard (AC-1 EC) is enforced here too: before
trusting any candidate path we verify it lives inside one of the
registered library roots. A path-out-of-root candidate raises
:class:`PathOutOfRootError`; the caller (the scanner orchestrator) maps
this to a `path-out-of-root` audit event.
"""

from __future__ import annotations

import os
from collections.abc import Iterable
from dataclasses import dataclass
from enum import StrEnum

from .roots import canonicalise, paths_overlap

__all__ = [
    "DedupDecision",
    "DedupOutcome",
    "ExistingVideo",
    "PathOutOfRootError",
    "decide",
    "is_path_in_roots",
]


class DedupOutcome(StrEnum):
    """The three branches AC-1/AC-2 enumerates."""

    NEW = "new"
    MOVED = "moved"
    DUPLICATE = "duplicate"


@dataclass(slots=True, frozen=True)
class ExistingVideo:
    """Minimal projection of the existing `videos` row needed to decide.

    The full row stays in the orchestrator; we only need enough to
    resolve the move-vs-duplicate question.
    """

    video_id: str
    path: str
    library_id: str


@dataclass(slots=True, frozen=True)
class DedupDecision:
    """Outcome of :func:`decide`.

    ``existing`` is populated for ``MOVED`` and ``DUPLICATE`` so the
    caller can build the SQL UPDATE / audit_log INSERT without a second
    lookup.
    """

    outcome: DedupOutcome
    existing: ExistingVideo | None = None


class PathOutOfRootError(ValueError):
    """Raised by :func:`decide` when the candidate path escapes every
    registered root for the library.

    Carries the canonical path so the orchestrator can log it verbatim
    in the ``path-out-of-root`` audit event.
    """

    def __init__(self, canonical: str) -> None:
        super().__init__(f"path {canonical!r} is outside every registered root")
        self.canonical = canonical


def is_path_in_roots(
    candidate: str, roots_canonical: Iterable[str]
) -> bool:
    """True if a canonical candidate lives under (or equals) any root.

    Use this *after* :func:`roots.canonicalise` on both sides — the
    check is a string-prefix match plus a separator boundary so
    ``/mnt/media2`` does not match ``/mnt/media``.
    """
    sep = os.sep
    for root in roots_canonical:
        if candidate == root:
            return True
        if candidate.startswith(root + sep):
            return True
    return False


def decide(
    candidate_path: str,
    candidate_hash: str,
    library_id: str,
    roots_canonical: Iterable[str],
    existing: ExistingVideo | None,
    *,
    other_path_exists: bool = False,
) -> DedupDecision:
    """Classify a freshly-scanned file against the catalog.

    ``existing`` is the row found by ``SELECT ... FROM videos WHERE
    content_hash = $1`` (or None). ``other_path_exists`` is True if the
    caller verified the existing row's on-disk path still exists — used
    to distinguish DUPLICATE (both copies present) from MOVED (only the
    new path remains).

    Raises :class:`PathOutOfRootError` if the candidate is outside every
    registered root for ``library_id`` (AC-1 security check).
    """
    if not candidate_hash:
        raise ValueError("candidate_hash must not be empty")

    canonical = canonicalise(candidate_path)
    roots_canon = [canonicalise(r) for r in roots_canonical]
    if not is_path_in_roots(canonical, roots_canon):
        raise PathOutOfRootError(canonical)

    if existing is None:
        return DedupDecision(outcome=DedupOutcome.NEW)

    # Existing row at the same path is a no-op for the orchestrator
    # (we'd never be calling decide() in this case in practice, but
    # define it explicitly so a second emit collapses cleanly).
    existing_canon = canonicalise(existing.path)
    if paths_overlap(existing_canon, canonical) and existing_canon == canonical:
        # Same file — neither MOVED nor DUPLICATE; treat as MOVED with
        # zero-op path update so the caller still bumps last_seen_at.
        return DedupDecision(outcome=DedupOutcome.MOVED, existing=existing)

    if other_path_exists:
        return DedupDecision(outcome=DedupOutcome.DUPLICATE, existing=existing)
    return DedupDecision(outcome=DedupOutcome.MOVED, existing=existing)
