"""Stateless one-shot job: chunk a transcript and advance its watermark.

The job is the unit of work the dispatcher invokes. It chunks via
the configured chunker, optionally pushes rows into a SQLite FTS
mirror (Postgres uses a trigger), and updates
``transcripts.last_indexed_segment_seq`` to the max segment seq the
chunker covered. The watermark drives the catch-up pass in
:class:`IndexerSupervisor`.
"""

from __future__ import annotations

from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol

__all__ = ["IncrementalIndexJob"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetchrow(self, sql: str, *args: Any) -> _Row | None: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


class _Chunker(Protocol):
    async def chunk_for_transcript(self, db: Any, transcript_id: int) -> int: ...


class _FTSWriter(Protocol):
    async def sync_transcript(self, db: Any, transcript_id: int) -> int: ...


_MAX_SEG_SEQ_SQL = """
SELECT MAX(seq) AS max_seq
  FROM transcript_segments
 WHERE transcript_id = $1
"""

_UPDATE_WATERMARK_SQL = """
UPDATE transcripts
   SET last_indexed_segment_seq = $1
 WHERE id = $2
"""


class IncrementalIndexJob:
    """Idempotent re-runnable job: chunk + watermark.

    The job is intentionally stateless — every call to :meth:`run`
    re-reads the segment table to determine the new watermark. A
    second concurrent run for the same transcript is harmless: the
    chunker's UPSERT semantics make repeated work a no-op, and the
    watermark update is monotonic at the SQL layer.
    """

    async def run(
        self,
        *,
        db: _DBConn,
        transcript_id: int,
        chunker: _Chunker,
        fts_writer: _FTSWriter | None = None,
    ) -> dict[str, Any]:
        """Chunk one transcript and advance the watermark.

        Returns a dict with ``transcript_id``, ``units_added_or_updated``
        (the chunker's count), and ``watermark`` (the new
        ``last_indexed_segment_seq``, or ``None`` if no segments
        exist).
        """
        unit_count = await chunker.chunk_for_transcript(db, transcript_id)

        if fts_writer is not None:
            await fts_writer.sync_transcript(db, transcript_id)

        watermark: int | None = None
        row = await db.fetchrow(_MAX_SEG_SEQ_SQL, transcript_id)
        if row is not None and row["max_seq"] is not None:
            watermark = int(row["max_seq"])
            await db.execute(_UPDATE_WATERMARK_SQL, watermark, transcript_id)

        return {
            "transcript_id": transcript_id,
            "units_added_or_updated": unit_count,
            "watermark": watermark,
        }
