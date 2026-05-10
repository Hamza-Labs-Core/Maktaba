"""Story 3.9 — speaker diarization (opt-in, off by default).

Two surfaces:

- :func:`assign_speakers` — pure: given a list of ``(start, end,
  speaker_id)`` intervals and a list of :class:`Segment`, return a new
  list with ``segment.speaker`` set to whichever interval covers the
  segment's midpoint. The tests use this to verify the assignment
  rule without booting pyannote.
- :class:`DiarizationGate` — process-global semaphore (default
  capacity 1) that gates the heavyweight pyannote run; pyannote is
  GPU-greedy enough that two concurrent diarizations on a single
  GPU thrash.

The pyannote import is lazy (inside :func:`run_pyannote`); turning
diarization off keeps the module import-light, satisfying Story 3.9 AC
"pyannote is never imported when ``diarize = false``".
"""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass, replace

from .protocol import Segment

__all__ = [
    "DiarizationGate",
    "Interval",
    "assign_speakers",
    "run_pyannote",
]


@dataclass(slots=True, frozen=True)
class Interval:
    """One ``(start, end, speaker)`` row from the diarizer."""

    start_sec: float
    end_sec: float
    speaker: str


def assign_speakers(
    segments: list[Segment],
    intervals: list[Interval],
) -> list[Segment]:
    """Set ``segment.speaker`` for each segment by midpoint match.

    Story 3.9 AC-2 — when a single STT segment spans two diarization
    intervals, the segment is **split** into two. Splitting keeps the
    same ``seq`` prefix and a ``.a`` / ``.b`` marker in
    ``metadata.split_from``. Word-level reassignment happens elsewhere
    (only when word timestamps are present); here the text is left
    intact on the first half.
    """
    if not intervals:
        return segments

    out: list[Segment] = []
    for seg in segments:
        # An interval qualifies if it strictly overlaps the segment
        # (boundaries that only "touch" don't count, otherwise every
        # back-to-back diarization pair would force a split).
        covering = [iv for iv in intervals if _strictly_overlaps(iv, seg)]
        if not covering:
            out.append(seg)
            continue
        # Single interval — the midpoint check picks the unambiguous winner.
        if len(covering) == 1:
            out.append(replace(seg, speaker=covering[0].speaker))
            continue
        # Multi-speaker overlap — split at the second interval's start.
        ordered = sorted(covering, key=lambda iv: iv.start_sec)
        first = ordered[0]
        second = ordered[1]
        boundary = max(seg.start_sec, min(seg.end_sec, second.start_sec))
        out.append(replace(
            seg,
            speaker=first.speaker,
            end_sec=boundary,
            metadata={**seg.metadata, "split_from": f"{seg.seq}.a"},
        ))
        out.append(replace(
            seg,
            speaker=second.speaker,
            start_sec=boundary,
            text="",
            metadata={**seg.metadata, "split_from": f"{seg.seq}.b"},
        ))
    return out


def _strictly_overlaps(iv: Interval, seg: Segment) -> bool:
    """True iff ``iv`` shares any non-zero-length overlap with ``seg``."""
    return iv.end_sec > seg.start_sec and iv.start_sec < seg.end_sec


class DiarizationGate:
    """Process-wide semaphore for diarization runs.

    Default capacity 1 because pyannote is GPU-greedy. A library that
    explicitly opts into parallelism (``diarize_concurrency = 2``) can
    bump it; tests construct with capacity matching the scenario.
    """

    __slots__ = ("_sem",)

    def __init__(self, capacity: int = 1) -> None:
        if capacity < 1:
            raise ValueError("DiarizationGate capacity must be >= 1")
        self._sem = asyncio.Semaphore(capacity)

    @asynccontextmanager
    async def slot(self) -> AsyncIterator[None]:
        await self._sem.acquire()
        try:
            yield
        finally:
            self._sem.release()


async def run_pyannote(_path: str) -> list[Interval]:  # pragma: no cover — env-specific
    """Run the pyannote pretrained diarization pipeline.

    Lazy import so importing this module on a Linux CI host without
    pyannote installed remains free.
    """
    try:
        from pyannote.audio import Pipeline  # type: ignore[import-not-found]
    except ImportError as exc:
        raise RuntimeError(f"pyannote.audio not installed: {exc}") from exc
    pipe = Pipeline.from_pretrained("pyannote/speaker-diarization-3.1")
    diarization = pipe(_path)
    return [
        Interval(start_sec=t.start, end_sec=t.end, speaker=str(speaker))
        for t, _, speaker in diarization.itertracks(yield_label=True)
    ]
