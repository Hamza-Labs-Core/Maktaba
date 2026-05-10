"""Story 5.1 — `transcript_units` model and persistence helpers.

The `index` stage chunks `transcript_segments` into larger semantic
units before embedding. The on-disk shape is migration slot 0050; this
module owns the in-process row shape (`TranscriptUnit`) and the bulk
upsert SQL the indexer uses.

The unit is intentionally lighter than :class:`SegmentDoc` in
``search.embedder`` — that record carries the metadata Chroma stores,
while a :class:`TranscriptUnit` is the row that lands in Postgres
alongside an `embedding_id` pointer back to the vector store.
"""

from __future__ import annotations

from collections.abc import Iterable
from dataclasses import dataclass
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "TranscriptUnit",
    "upsert_units",
]


@dataclass(slots=True, frozen=True)
class TranscriptUnit:
    """One indexable chunk of transcript text.

    ``unit_index`` is the per-transcript ordinal — together with
    ``transcript_id`` it forms the row's natural key (matching the
    UNIQUE constraint in slot 0050). ``segment_id`` is the source
    segment when chunking is 1:1; for multi-segment merges the field
    can stay ``None`` and consumers should fall back to the time
    window.
    """

    transcript_id: UUID
    video_id: UUID
    unit_index: int
    start_sec: float
    end_sec: float
    text: str
    segment_id: int | None = None
    language: str | None = None
    embedding_id: str | None = None


class _UnitsDB(Protocol):
    dialect: str

    async def executemany(self, sql: str, args: list[tuple[Any, ...]]) -> Any: ...


_PG_UPSERT_UNIT = """
INSERT INTO transcript_units
       (transcript_id, video_id, segment_id, unit_index,
        start_sec, end_sec, text, language, embedding_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
ON CONFLICT (transcript_id, unit_index) DO UPDATE SET
    video_id     = EXCLUDED.video_id,
    segment_id   = EXCLUDED.segment_id,
    start_sec    = EXCLUDED.start_sec,
    end_sec      = EXCLUDED.end_sec,
    text         = EXCLUDED.text,
    language     = EXCLUDED.language,
    embedding_id = EXCLUDED.embedding_id
"""


async def upsert_units(db: _UnitsDB, units: Iterable[TranscriptUnit]) -> int:
    """Write ``units`` into `transcript_units`, returning the count.

    Idempotent: re-running with the same `(transcript_id, unit_index)`
    pair updates the row in place. This matches the indexer's contract
    that re-chunking a transcript replaces the prior units in lockstep
    with the Chroma upsert.
    """
    rows = [
        (
            unit.transcript_id,
            unit.video_id,
            unit.segment_id,
            unit.unit_index,
            unit.start_sec,
            unit.end_sec,
            unit.text,
            unit.language,
            unit.embedding_id,
        )
        for unit in units
    ]
    if not rows:
        return 0
    await db.executemany(_PG_UPSERT_UNIT, rows)
    return len(rows)
