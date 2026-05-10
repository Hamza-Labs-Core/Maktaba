"""UPSERT helpers for the ``transcript_units`` table.

Lives in its own module so both :mod:`chunker` (live chunking from
the orchestrator) and :mod:`indexer` (reindex CLI) call into the
same write path. UPSERT clears ``indexed_at`` and
``indexed_at_in_chroma`` so the incremental indexer re-runs against
the new text.
"""

from __future__ import annotations

import json
from collections.abc import Sequence
from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol

from .models import UnitDraft

__all__ = ["DBConn", "upsert_units"]


class DBConn(Protocol):
    """Minimal connection shape :func:`upsert_units` needs."""

    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...

    async def fetchrow(self, sql: str, *args: Any) -> Any: ...


# Postgres: JSONB casts; the ON CONFLICT target is the UNIQUE
# (transcript_id, seq) constraint from slot 0017. Clearing the two
# index watermarks forces a re-index.
_UPSERT_PG = """
INSERT INTO transcript_units
       (transcript_id, seq, start_sec, end_sec, text, language,
        segment_ids, metadata, indexed_at, indexed_at_in_chroma)
VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb, NULL, NULL)
ON CONFLICT (transcript_id, seq) DO UPDATE
   SET start_sec            = EXCLUDED.start_sec,
       end_sec              = EXCLUDED.end_sec,
       text                 = EXCLUDED.text,
       language             = EXCLUDED.language,
       segment_ids          = EXCLUDED.segment_ids,
       metadata             = EXCLUDED.metadata,
       indexed_at           = NULL,
       indexed_at_in_chroma = NULL
"""

# SQLite: segment_ids and metadata are TEXT-encoded JSON.
_UPSERT_SQLITE = """
INSERT INTO transcript_units
       (transcript_id, seq, start_sec, end_sec, text, language,
        segment_ids, metadata, indexed_at, indexed_at_in_chroma)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)
ON CONFLICT (transcript_id, seq) DO UPDATE
   SET start_sec            = excluded.start_sec,
       end_sec              = excluded.end_sec,
       text                 = excluded.text,
       language             = excluded.language,
       segment_ids          = excluded.segment_ids,
       metadata             = excluded.metadata,
       indexed_at           = NULL,
       indexed_at_in_chroma = NULL
"""


async def upsert_units(
    db: DBConn,
    transcript_id: int,
    units: Sequence[UnitDraft],
) -> int:
    """UPSERT each unit; return the count written.

    Wraps the entire batch in a single transaction so chunk-and-write
    is atomic per transcript. The caller is responsible for tagging
    each unit's ``language`` field — this helper just persists.
    """
    if not units:
        return 0
    sql = _UPSERT_PG if db.dialect == "postgres" else _UPSERT_SQLITE
    async with db.transaction():
        for unit in units:
            await db.execute(
                sql,
                transcript_id,
                unit.seq,
                unit.start_sec,
                unit.end_sec,
                unit.text,
                unit.language,
                json.dumps(list(unit.segment_ids)),
                json.dumps(unit.metadata),
            )
    return len(units)
