"""Story 9.16 — root canonicalisation and overlap detection.

A library can have N roots. Roots must not overlap with another
library's roots; within a library, multiple roots are independent. This
module is the canonical implementation of:

- :func:`canonicalise` — resolve symlinks, strip trailing slashes,
  normalise ``..`` (AC-3 rule).
- :func:`paths_overlap` — true if one canonical path is a prefix of the
  other (or they're equal).
- :func:`find_overlap` — the create/update gate (AC-2): given a set of
  proposed roots and a registry of existing roots, return the offending
  pair or ``None``.
- :func:`detect_runtime_overlap` — the periodic-sweep guard (AC-4): a
  remount can make two previously non-overlapping declared roots resolve
  to the same physical path; this is a runtime drift, not a config bug.

Pure functions only — DB access lives in the API handler that calls
:func:`find_overlap` against `library_roots`. Path canonicalisation uses
``os.path.realpath`` so a symlink pointing into another library's root
is caught at create time.
"""

from __future__ import annotations

import os
from collections.abc import Iterable
from dataclasses import dataclass

__all__ = [
    "Overlap",
    "canonicalise",
    "detect_runtime_overlap",
    "find_overlap",
    "find_self_overlap",
    "paths_overlap",
]


@dataclass(slots=True, frozen=True)
class Overlap:
    """One detected overlap.

    ``existing`` is the path that was already in the registry;
    ``proposed`` is the offending new entry. For a self-overlap (two
    roots in the *same* library), the same library_id appears in
    ``existing_library_id`` and ``proposed_library_id`` and the caller
    knows to surface it as a 422 too (AC-3 edge case).
    """

    existing: str
    proposed: str
    existing_library_id: str
    proposed_library_id: str


def canonicalise(path: str) -> str:
    """Resolve symlinks, normalise `..`/`.`, strip trailing slashes.

    Two semantically equal paths must produce the same canonical string
    so the prefix check in :func:`paths_overlap` is correct. Returns the
    input unchanged if the OS resolution fails (e.g., a not-yet-mounted
    NFS path) — the caller has already verified existence via the API
    layer's PathChecker.
    """
    if not path:
        return path
    try:
        resolved = os.path.realpath(path)
    except OSError:
        resolved = path
    # ``os.path.realpath`` already normalises ``..`` and ``.``; strip a
    # trailing separator (except on the root itself).
    if len(resolved) > 1 and resolved.endswith(os.sep):
        resolved = resolved.rstrip(os.sep)
    return resolved


def paths_overlap(a: str, b: str) -> bool:
    """True if a canonical path is a prefix of (or equal to) the other.

    Both inputs must already be canonical — call :func:`canonicalise`
    first. The check is symmetric: ``paths_overlap("/x", "/x/y")`` and
    ``paths_overlap("/x/y", "/x")`` both return True.
    """
    if a == b:
        return True
    sep = os.sep
    if a.startswith(b + sep):
        return True
    return b.startswith(a + sep)


def find_self_overlap(roots: Iterable[str]) -> Overlap | None:
    """Reject ``["/a", "/a/b"]`` in the same library (AC-3 EC).

    Returns the first offending pair after canonicalising every root;
    callers map this to a 422.
    """
    canonical = [canonicalise(p) for p in roots]
    for i, a in enumerate(canonical):
        for j in range(i + 1, len(canonical)):
            b = canonical[j]
            if paths_overlap(a, b):
                return Overlap(
                    existing=a,
                    proposed=b,
                    existing_library_id="",
                    proposed_library_id="",
                )
    return None


def find_overlap(
    proposed_library_id: str,
    proposed_roots: Iterable[str],
    existing: Iterable[tuple[str, str]],
) -> Overlap | None:
    """Test proposed roots against an existing registry.

    ``existing`` is an iterable of ``(library_id, path_canonical)`` rows
    from `library_roots`. Rows whose ``library_id`` matches
    ``proposed_library_id`` are skipped — a library updating its own
    roots may shrink/extend without the check tripping on its old self.

    Returns the first offending :class:`Overlap` or ``None``.
    """
    canonical_proposed = [canonicalise(p) for p in proposed_roots]
    for ex_id, ex_path in existing:
        if ex_id == proposed_library_id:
            continue
        for pp in canonical_proposed:
            if paths_overlap(ex_path, pp):
                return Overlap(
                    existing=ex_path,
                    proposed=pp,
                    existing_library_id=ex_id,
                    proposed_library_id=proposed_library_id,
                )
    return None


def detect_runtime_overlap(
    declared: Iterable[tuple[str, str, str]],
) -> list[Overlap]:
    """Periodic-sweep guard (AC-4) — return every runtime overlap.

    ``declared`` is an iterable of ``(library_id, path, path_canonical)``
    rows. We re-canonicalise the *current* on-disk path and compare; if
    a remount has redirected one root onto another, we report it. The
    caller logs ``library-roots-runtime-overlap`` and writes an audit
    row but does *not* abort the sweep.

    Returns every distinct overlap seen, not just the first, so the
    operator can fix one mount layout in a single pass.
    """
    fresh: list[tuple[str, str, str]] = []
    for lib_id, path, _stored in declared:
        current = canonicalise(path)
        fresh.append((lib_id, path, current))

    out: list[Overlap] = []
    for i, (a_id, _a_path, a_canon) in enumerate(fresh):
        for j in range(i + 1, len(fresh)):
            b_id, _b_path, b_canon = fresh[j]
            if a_id == b_id:
                # Self-overlap from a remount is also worth surfacing —
                # it would silently double-walk the same files.
                pass
            if paths_overlap(a_canon, b_canon):
                out.append(
                    Overlap(
                        existing=a_canon,
                        proposed=b_canon,
                        existing_library_id=a_id,
                        proposed_library_id=b_id,
                    )
                )
    return out
