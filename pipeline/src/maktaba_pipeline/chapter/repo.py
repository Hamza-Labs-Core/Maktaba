"""Persistence helper for inferred chapters.

A successful infer pass replaces *all* inferred rows for a video in a
single transaction. Embedded and manual chapters are left untouched
because they have a different ``source`` value — only rows where
``source = 'inferred'`` are wiped before the new batch is inserted.
"""

from __future__ import annotations

from collections.abc import Sequence
from contextlib import AbstractAsyncContextManager
from typing import Any, Protocol

from .inferer import ChapterRow

__all__ = ["save_chapters"]


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def execute(self, sql: str, *args: Any) -> Any: ...


_DELETE_SQL = """
DELETE FROM chapters
 WHERE video_id = $1
   AND source = 'inferred'
"""

_INSERT_SQL = """
INSERT INTO chapters
       (video_id, transcript_id, seq, start_sec, end_sec, title, source, lang, confidence)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
"""


async def save_chapters(
    db: _DBConn,
    *,
    video_id: str,
    transcript_id: int,
    chapters: Sequence[ChapterRow],
) -> int:
    """Replace the inferred chapter rows for ``video_id``.

    Runs DELETE + INSERTs in one transaction so a partial failure
    leaves the previous (consistent) state in place. Returns the
    number of rows inserted; callers can use the return value for
    logging without an extra count query.

    The function intentionally rejects rows whose ``source`` is not
    ``'inferred'`` — the slot 0026 schema accepts other sources, but
    this repo helper only owns inferred rows; persisting embedded or
    manual chapters is a different code path.
    """
    written = 0
    async with db.transaction():
        await db.execute(_DELETE_SQL, video_id)
        for row in chapters:
            if row.source != "inferred":
                raise ValueError(
                    f"save_chapters: refusing to write non-inferred row "
                    f"(source={row.source!r}); use a different repo helper"
                )
            await db.execute(
                _INSERT_SQL,
                row.video_id,
                row.transcript_id,
                row.seq,
                row.start_sec,
                row.end_sec,
                row.title,
                row.source,
                row.lang,
                row.confidence,
            )
            written += 1
    return written
