"""Story 5.1 — transcript_segments → transcript_units chunker.

Public API:

- :func:`chunk_segments_into_units` — pure function over
  :class:`SegmentRow` lists. Used by tests and by the live indexer
  (Epic 5 plan-05-05) when segments are already in memory.
- :func:`chunk_for_transcript` — DB-driven entry point. Reads
  segments + language for a transcript, runs the chunker, and
  UPSERTs the results.
"""

from __future__ import annotations

from collections.abc import Sequence
from contextlib import AbstractAsyncContextManager
from dataclasses import replace
from typing import Any, Protocol

from .models import SegmentRow, UnitDraft
from .packer import Packer
from .persist import upsert_units
from .segmenter import split_into_sentences

__all__ = ["chunk_for_transcript", "chunk_segments_into_units"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class DBConn(Protocol):
    """Minimal DB shape this module needs.

    Provides ``fetchrow`` for the transcript metadata lookup and
    ``fetch`` for the segment list. Both shapes are present on the
    standard connection wrapper.
    """

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_LANG_SQL_PG = "SELECT language_code FROM transcripts WHERE id = $1"
_LANG_SQL_SQLITE = "SELECT language_code FROM transcripts WHERE id = ?"

_SEGMENTS_SQL_PG = (
    "SELECT id, seq, start_sec, end_sec, text "
    "FROM transcript_segments WHERE transcript_id = $1 ORDER BY seq"
)
_SEGMENTS_SQL_SQLITE = (
    "SELECT id, seq, start_sec, end_sec, text "
    "FROM transcript_segments WHERE transcript_id = ? ORDER BY seq"
)


def chunk_segments_into_units(
    segments: Sequence[SegmentRow],
    *,
    language: str,
) -> list[UnitDraft]:
    """Pure chunk pipeline: sentences → packer → language-tag.

    The packer leaves :attr:`UnitDraft.language` empty; this wrapper
    tags it with ``language`` so the caller does not need to know
    about the internal contract.
    """
    sentences = split_into_sentences(segments)
    units = Packer().pack(sentences)
    return [replace(u, language=language) for u in units]


async def chunk_for_transcript(db: DBConn, transcript_id: int) -> int:
    """Read segments for a transcript, chunk, and UPSERT units.

    Returns the number of units written. If the transcript has no
    segments yet, returns 0 without touching ``transcript_units``.
    Raises :class:`LookupError` when the transcript row is missing.
    """
    lang_sql = _LANG_SQL_PG if db.dialect == "postgres" else _LANG_SQL_SQLITE
    seg_sql = _SEGMENTS_SQL_PG if db.dialect == "postgres" else _SEGMENTS_SQL_SQLITE

    row = await db.fetchrow(lang_sql, transcript_id)
    if row is None:
        raise LookupError(f"transcript {transcript_id} not found")
    language = str(row["language_code"])

    rows = await db.fetch(seg_sql, transcript_id)
    segments = [
        SegmentRow(
            id=int(r["id"]),
            seq=int(r["seq"]),
            start_sec=float(r["start_sec"]),
            end_sec=float(r["end_sec"]),
            text=str(r["text"]),
        )
        for r in rows
    ]
    if not segments:
        return 0

    units = chunk_segments_into_units(segments, language=language)
    return await upsert_units(db, transcript_id, units)
