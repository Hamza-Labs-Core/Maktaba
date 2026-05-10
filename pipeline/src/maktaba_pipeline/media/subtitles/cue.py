"""Cue and Segment dataclasses — the in-memory shape between shaper and writer.

A :class:`Segment` is what the DB view ``transcript_segments_v`` hands
us (one row per ASR segment). A :class:`Cue` is what a writer renders
(one display block, possibly multi-line, possibly with a speaker tag).
The :mod:`shaper` module is the only piece that knows how to turn the
first into the second.
"""

from __future__ import annotations

from dataclasses import dataclass

__all__ = ["Cue", "Segment"]


@dataclass(frozen=True, slots=True)
class Segment:
    """One row from ``transcript_segments_v``.

    Timestamps are seconds (float); the DB stores DOUBLE PRECISION.
    ``speaker`` is optional — when None the writer omits any speaker
    tag. ``seq`` is the segment's sequence number within its
    transcript; the shaper copies it forward to ``Cue.cue_id`` so a
    VTT consumer can map back to a transcript row.
    """

    seq: int
    start_sec: float
    end_sec: float
    text: str
    speaker: str | None = None


@dataclass(frozen=True, slots=True)
class Cue:
    """A display block ready for the SRT/VTT writers.

    ``lines`` is a tuple (immutable) so the writer can hash or
    memoize without surprises. ``cue_id`` becomes the WebVTT cue
    identifier line; ignored by SRT. ``speaker`` drives the
    ``<v Speaker>`` tag in VTT only.
    """

    start_sec: float
    end_sec: float
    lines: tuple[str, ...]
    speaker: str | None = None
    cue_id: str | None = None
