"""Postgres FTS search against ``transcript_units.tsv`` (slot 0021).

Uses ``plainto_tsquery(language_to_regconfig($lang), maktaba_normalize($q))``
on the query side so user-typed input gets the same normalization
and token-class as the indexed text. Ranks with ``ts_rank_cd`` —
higher is better — and joins through ``transcripts → videos`` to
scope by ``library_id``.
"""

from __future__ import annotations

from collections.abc import Awaitable
from typing import Any, Protocol
from uuid import UUID

__all__ = ["postgres_fts_search"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def fetch(self, sql: str, *args: Any) -> Awaitable[list[_Row]]: ...


# The query embeds plainto_tsquery() so each row's tsv can be matched
# against a freshly-built tsquery without us round-tripping through
# the application layer. `language_to_regconfig` is the IMMUTABLE
# helper from slot 0019.
_SQL_NO_LANG = """
SELECT u.id AS unit_id,
       ts_rank_cd(u.tsv, q) AS rank
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id,
       plainto_tsquery(
           language_to_regconfig($2),
           maktaba_normalize($1)
       ) q
 WHERE u.tsv @@ q
   AND v.library_id = $3
 ORDER BY rank DESC
 LIMIT $4
"""

_SQL_WITH_LANG = """
SELECT u.id AS unit_id,
       ts_rank_cd(u.tsv, q) AS rank
  FROM transcript_units u
  JOIN transcripts t ON t.id = u.transcript_id
  JOIN videos v ON v.id = t.video_id,
       plainto_tsquery(
           language_to_regconfig($2),
           maktaba_normalize($1)
       ) q
 WHERE u.tsv @@ q
   AND v.library_id = $3
   AND u.language = $5
 ORDER BY rank DESC
 LIMIT $4
"""


async def postgres_fts_search(
    db: _DBConn,
    query: str,
    *,
    library_id: UUID | str,
    language: str | None,
    limit: int,
) -> list[tuple[int, float]]:
    """Return ``[(unit_id, rank)]`` ordered by descending rank.

    ``language`` is used both to pick the regconfig (Arabic vs.
    English vs. simple) and to filter the candidate set. Passing
    ``None`` falls back to ``'simple'`` and skips the per-unit
    language filter — useful for mixed-language libraries.
    """
    if limit <= 0:
        return []
    lang_for_config = language or "simple"
    if language is None:
        rows = await db.fetch(_SQL_NO_LANG, query, lang_for_config, library_id, limit)
    else:
        rows = await db.fetch(
            _SQL_WITH_LANG,
            query,
            lang_for_config,
            library_id,
            limit,
            language,
        )
    return [(int(r["unit_id"]), float(r["rank"])) for r in rows]
