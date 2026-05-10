"""Frozen dataclasses for the chunker → indexer → engine pipeline.

These types are the contract between :mod:`segmenter`, :mod:`packer`,
:mod:`chunker`, and :mod:`persist`. They stay pure data and import
nothing from ``maktaba_pipeline.db`` so unit tests can construct
fixtures without any driver installed.
"""

from __future__ import annotations

from dataclasses import dataclass, field

__all__ = ["SegmentRow", "Sentence", "UnitDraft"]


@dataclass(frozen=True, slots=True)
class SegmentRow:
    """One row from ``transcript_segments`` as the chunker reads it.

    Only the columns the chunker needs are modelled; speaker and
    confidence are dropped because chunking is language-only at this
    stage (Story 5.1 §3).
    """

    id: int
    seq: int
    start_sec: float
    end_sec: float
    text: str


@dataclass(frozen=True, slots=True)
class Sentence:
    """A sentence-bounded slice of one or more segments.

    Produced by :func:`segmenter.split_into_sentences`. The
    ``segment_ids`` tuple is ordered (segment ``seq`` ascending) and
    deduplicated; ``start_sec`` and ``end_sec`` are derived from the
    first / last overlapping segment.
    """

    text: str
    start_sec: float
    end_sec: float
    segment_ids: tuple[int, ...]


@dataclass(frozen=True, slots=True)
class UnitDraft:
    """A packed transcript-unit ready to upsert.

    ``seq`` is 1-based per transcript (matches the UNIQUE constraint
    on ``transcript_units(transcript_id, seq)`` from slot 0017). The
    ``language`` field is tagged by the chunker — the packer itself
    is language-agnostic, but the column is NOT NULL.
    """

    seq: int
    start_sec: float
    end_sec: float
    text: str
    language: str
    segment_ids: tuple[int, ...]
    metadata: dict[str, object] = field(default_factory=dict)
