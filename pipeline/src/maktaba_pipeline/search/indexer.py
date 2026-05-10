"""Incremental indexer — Story 5.3 / plan-05-05.

Pulls ``transcript_units`` rows with ``indexed_at_in_chroma IS NULL``,
embeds them with the configured :class:`EmbeddingService`, upserts
into the per-library Chroma collection, then stamps
``indexed_at_in_chroma = now()`` so the next pass skips them.
"""

from __future__ import annotations

from collections.abc import Awaitable, Sequence
from dataclasses import dataclass
from typing import Any, Protocol

import structlog

from .chroma_client import ChromaClient
from .embedder import EmbeddingService

__all__ = ["IndexerConfig", "IndexerWorker"]

_log = structlog.get_logger(__name__)


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def fetch(self, sql: str, *args: Any) -> Awaitable[list[_Row]]: ...

    def execute(self, sql: str, *args: Any) -> Awaitable[Any]: ...


@dataclass(slots=True)
class IndexerConfig:
    """How many units to embed/upload per Chroma call."""

    batch_size: int = 32


# Pull the units, their text, language, transcript-level start, plus
# the owning video's id and library_id. The library_id drives which
# Chroma collection we write into.
_FETCH_UNITS_PG = """
SELECT u.id            AS unit_id,
       u.text          AS text,
       u.language      AS language,
       u.start_sec     AS start_sec,
       u.end_sec       AS end_sec,
       u.segment_ids   AS segment_ids,
       u.transcript_id AS transcript_id,
       t.video_id      AS video_id,
       v.library_id    AS library_id
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id
 WHERE u.id = ANY($1::bigint[])
"""

_FETCH_UNITS_SQLITE_TMPL = """
SELECT u.id            AS unit_id,
       u.text          AS text,
       u.language      AS language,
       u.start_sec     AS start_sec,
       u.end_sec       AS end_sec,
       u.segment_ids   AS segment_ids,
       u.transcript_id AS transcript_id,
       t.video_id      AS video_id,
       v.library_id    AS library_id
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id
 WHERE u.id IN ({placeholders})
"""

_LIST_UNINDEXED_PG = """
SELECT u.id AS unit_id
  FROM transcript_units u
 WHERE u.transcript_id = $1
   AND u.indexed_at_in_chroma IS NULL
 ORDER BY u.seq
"""

_LIST_UNINDEXED_SQLITE = """
SELECT u.id AS unit_id
  FROM transcript_units u
 WHERE u.transcript_id = ?
   AND u.indexed_at_in_chroma IS NULL
 ORDER BY u.seq
"""

_STAMP_PG = "UPDATE transcript_units SET indexed_at_in_chroma = now() WHERE id = ANY($1::bigint[])"
_STAMP_SQLITE_TMPL = (
    "UPDATE transcript_units SET indexed_at_in_chroma = "
    "strftime('%Y-%m-%dT%H:%M:%fZ', 'now') WHERE id IN ({placeholders})"
)


class IndexerWorker:
    """Bulk-embed + write units into the per-library Chroma collection.

    Two entry points: :meth:`index_unit_batch` (caller supplies the
    ids) and :meth:`index_unindexed_for_transcript` (sweep one
    transcript). The second method chunks into ``batch_size`` groups
    so we don't load the full transcript into memory at once.
    """

    def __init__(
        self,
        *,
        db: _DBConn,
        embedder: EmbeddingService,
        chroma: ChromaClient,
        config: IndexerConfig | None = None,
    ) -> None:
        self._db = db
        self._embedder = embedder
        self._chroma = chroma
        self._config = config or IndexerConfig()

    async def index_unit_batch(self, unit_ids: Sequence[int]) -> int:
        """Embed the named units and upsert them into Chroma.

        Returns the number actually written. Units that don't resolve
        (e.g. deleted between dispatch and processing) are silently
        skipped so a stale batch can't wedge the worker.
        """
        if not unit_ids:
            return 0

        ids_list = list(unit_ids)
        if self._db.dialect == "postgres":
            rows = await self._db.fetch(_FETCH_UNITS_PG, ids_list)
        else:
            placeholders = ",".join("?" for _ in ids_list)
            sql = _FETCH_UNITS_SQLITE_TMPL.format(placeholders=placeholders)
            rows = await self._db.fetch(sql, *ids_list)

        if not rows:
            return 0

        # Bucket rows by library so each Chroma collection only gets
        # the ids that belong to it.
        by_library: dict[str, list[_Row]] = {}
        for row in rows:
            lib = str(row["library_id"])
            by_library.setdefault(lib, []).append(row)

        written = 0
        for library_id, lib_rows in by_library.items():
            texts = [str(r["text"]) for r in lib_rows]
            embeddings = self._embedder.embed_passages(texts)
            metadatas: list[dict[str, Any]] = [
                {
                    "unit_id": int(r["unit_id"]),
                    "transcript_id": int(r["transcript_id"]),
                    "video_id": str(r["video_id"]),
                    "library_id": library_id,
                    "language": str(r["language"]),
                    "start_sec": float(r["start_sec"]),
                    "end_sec": float(r["end_sec"]),
                }
                for r in lib_rows
            ]
            id_strs = [str(r["unit_id"]) for r in lib_rows]
            self._chroma.collection(library_id).upsert(
                unit_ids=id_strs,
                embeddings=embeddings,
                metadatas=metadatas,
                documents=texts,
            )
            written += len(lib_rows)

        stamp_ids = [int(r["unit_id"]) for r in rows]
        if self._db.dialect == "postgres":
            await self._db.execute(_STAMP_PG, stamp_ids)
        else:
            placeholders = ",".join("?" for _ in stamp_ids)
            await self._db.execute(
                _STAMP_SQLITE_TMPL.format(placeholders=placeholders),
                *stamp_ids,
            )

        _log.info(
            "search.indexer.batch",
            count=written,
            unit_ids=stamp_ids,
        )
        return written

    async def index_unindexed_for_transcript(self, transcript_id: int) -> int:
        """Sweep all unindexed units for one transcript in batches.

        Returns the total number written. Empty list → 0 (no work).
        """
        sql = _LIST_UNINDEXED_PG if self._db.dialect == "postgres" else _LIST_UNINDEXED_SQLITE
        rows = await self._db.fetch(sql, transcript_id)
        ids = [int(r["unit_id"]) for r in rows]
        if not ids:
            return 0

        total = 0
        bs = self._config.batch_size
        for i in range(0, len(ids), bs):
            chunk = ids[i : i + bs]
            total += await self.index_unit_batch(chunk)
        return total
