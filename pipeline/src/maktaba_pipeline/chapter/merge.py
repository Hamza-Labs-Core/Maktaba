"""Enforce a minimum chapter length on a candidate boundary list.

The detector can produce boundaries that are seconds apart when a
speaker pauses and switches topics quickly. We drop the *weaker*
(lower distance) of any two boundaries that are closer together than
``min_chapter_sec`` so the final chapter list is readable.
"""

from __future__ import annotations

from collections.abc import Sequence

__all__ = ["enforce_min_chapter_sec"]


def enforce_min_chapter_sec(
    boundaries: Sequence[tuple[int, float]],
    unit_starts: Sequence[float],
    *,
    min_chapter_sec: float = 180.0,
) -> list[tuple[int, float]]:
    """Drop weaker boundaries that crowd a stronger one.

    Walks the boundary list in original order. For each candidate we
    look back at the most recent kept boundary; if the start-time
    gap is below ``min_chapter_sec``, we keep whichever has the
    higher distance and drop the other.

    ``unit_starts`` is the seconds-from-start of each unit, indexed
    so that ``unit_starts[boundary_index]`` is the start of the
    boundary's unit. Out-of-range indices raise ``IndexError`` (a
    bug in the caller, not a soft failure).

    Returns the surviving boundaries in their original (index-
    ascending) order.
    """
    if min_chapter_sec < 0:
        raise ValueError("min_chapter_sec must be non-negative")
    if not boundaries:
        return []

    kept: list[tuple[int, float]] = []
    for idx, dist in boundaries:
        start = float(unit_starts[idx])
        if not kept:
            kept.append((idx, dist))
            continue
        last_idx, last_dist = kept[-1]
        last_start = float(unit_starts[last_idx])
        if start - last_start < min_chapter_sec:
            # Keep whichever boundary is stronger (higher distance).
            if dist > last_dist:
                kept[-1] = (idx, dist)
            # else: drop the new (weaker) candidate.
        else:
            kept.append((idx, dist))
    return kept
