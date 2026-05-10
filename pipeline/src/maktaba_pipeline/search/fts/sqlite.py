"""SQLite FTS5 search against the ``transcripts_fts`` virtual table.

The companion to :mod:`postgres` — same join shape (units →
transcripts → videos for library scoping), but the ranking function
is FTS5's ``bm25()`` which is *lower-is-better*. We negate the score
on the way out so callers can compare ranks across engines without
worrying about sort direction.

The arabic_normalize Python function should be registered on the
connection at open time via :func:`register_arabic_normalize`; the
query path then pre-normalizes the user query so the indexed text
(also normalized) matches.
"""

from __future__ import annotations

from collections.abc import Awaitable
from typing import Any, Protocol

from .normalize import arabic_normalize
from .query import build_fts_query

__all__ = ["register_arabic_normalize", "sqlite_fts_search"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def fetch(self, sql: str, *args: Any) -> Awaitable[list[_Row]]: ...


_SQL_NO_LANG = """
SELECT f.unit_id AS unit_id,
       bm25(transcripts_fts) AS score
  FROM transcripts_fts f
  JOIN transcript_units u ON u.id = f.unit_id
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id
 WHERE transcripts_fts MATCH ?
   AND v.library_id = ?
 ORDER BY bm25(transcripts_fts) ASC
 LIMIT ?
"""

_SQL_WITH_LANG = """
SELECT f.unit_id AS unit_id,
       bm25(transcripts_fts) AS score
  FROM transcripts_fts f
  JOIN transcript_units u ON u.id = f.unit_id
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id
 WHERE transcripts_fts MATCH ?
   AND v.library_id = ?
   AND u.language = ?
 ORDER BY bm25(transcripts_fts) ASC
 LIMIT ?
"""


def register_arabic_normalize(conn: Any) -> None:
    """Register ``arabic_normalize`` as a SQLite user function.

    Call this once per connection at open time. The trigger from
    slot 0022 references the function by name; without this call,
    INSERTs into ``transcript_units`` will fail with
    ``no such function: arabic_normalize``.
    """
    conn.create_function("arabic_normalize", 1, arabic_normalize, deterministic=True)


async def sqlite_fts_search(
    db: _DBConn,
    query: str,
    *,
    library_id: str,
    language: str | None,
    limit: int,
) -> list[tuple[int, float]]:
    """Return ``[(unit_id, score)]`` where higher score is better.

    BM25 returns lower-is-better; we negate so the score has the
    same orientation as Postgres ``ts_rank_cd`` — RRF and the
    snippet code can treat both engines uniformly.
    """
    if limit <= 0:
        return []
    clause = build_fts_query(query)
    if not clause.tokens:
        return []

    # Pre-normalize the MATCH text via the same Python function the
    # trigger uses. The MATCH string itself is built by build_fts_query.
    match_text = arabic_normalize(clause.sqlite_form)

    if language is None:
        rows = await db.fetch(_SQL_NO_LANG, match_text, library_id, limit)
    else:
        rows = await db.fetch(_SQL_WITH_LANG, match_text, library_id, language, limit)

    return [(int(r["unit_id"]), -float(r["score"])) for r in rows]
