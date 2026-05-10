"""PostgreSQL full-text search over ``transcript_segments``.

The FTS column (``search_tsv``, migration 0016) is populated by a
generated column using the ``maktaba_search`` text-search
configuration, which couples ``unaccent`` with the ``simple`` token
mapping. ``simple`` preserves every token (no stemming), so the same
configuration works for both Arabic and English without over-collapsing
forms in mixed-language libraries.

This module exposes:

- :func:`normalize_query` — lowercase + unaccent + collapse whitespace.
  Used for cache keys and for the suggest module's prefix matcher.
- :func:`build_tsquery` — turn a user-typed string into a ``tsquery``
  expression. Multi-word inputs are ANDed; the trailing token gets a
  ``:*`` prefix marker so typeahead works without a separate query.
- :func:`fts_search` — execute a search and return :class:`FTSHit`
  rows ranked by ``ts_rank_cd``.

The SQLite path is intentionally absent here: migration 0016 ships an
FTS5 virtual table whose query syntax is different (``MATCH ?`` with
the FTS5 special characters). Pipeline-side callers using SQLite
should go through the dialect-aware database layer.
"""

from __future__ import annotations

import re
import unicodedata
from dataclasses import dataclass
from typing import Any, Protocol
from uuid import UUID

__all__ = [
    "FTSHit",
    "build_tsquery",
    "fts_search",
    "normalize_query",
]

# Characters that have meaning to the tsquery parser. We strip rather
# than escape because the user-typed forms (quotes, parens) never carry
# meaning in the typeahead surface — they're noise.
_TSQUERY_OPERATORS = re.compile(r"[!&|()<>:*'\"\\]")

# Combining marks (Mn) are stripped after NFKD decomposition. Mirrors
# what the Postgres unaccent extension does for Latin scripts; Arabic
# diacritics (Mn category) are stripped the same way so typing without
# tashkīl matches text with it.
_COMBINING_MARK_CATEGORY = "Mn"


def normalize_query(text: str) -> str:
    """Lowercase + strip diacritics + collapse whitespace.

    The same normalisation runs on the search-history ``query_norm``
    column (so a query typed two ways matches one row) and on the
    incoming text before :func:`build_tsquery`. Empty / whitespace-only
    input returns an empty string.
    """
    if not text:
        return ""
    decomposed = unicodedata.normalize("NFKD", text)
    stripped = "".join(
        ch for ch in decomposed if unicodedata.category(ch) != _COMBINING_MARK_CATEGORY
    )
    # ``casefold`` is more aggressive than ``lower`` (handles e.g.
    # German ß → ss); both English and Arabic land on the same surface.
    return " ".join(stripped.casefold().split())


def build_tsquery(text: str, *, prefix_last: bool = True) -> str:
    """Compose a ``tsquery`` expression from a user string.

    All tokens are ANDed together. When ``prefix_last`` is set (the
    default for typeahead), the trailing token is rewritten as
    ``token:*`` so prefix matches succeed mid-word. Tsquery operators
    embedded in user input are stripped, not escaped — the user surface
    never carries query DSL.

    Returns an empty string for empty / whitespace-only input; callers
    should short-circuit to "no results" rather than execute the query.
    """
    if not text:
        return ""
    cleaned = _TSQUERY_OPERATORS.sub(" ", text)
    tokens = [tok for tok in cleaned.split() if tok]
    if not tokens:
        return ""
    if prefix_last:
        tokens[-1] = f"{tokens[-1]}:*"
    return " & ".join(tokens)


@dataclass(slots=True, frozen=True)
class FTSHit:
    """One FTS result.

    ``rank`` is the raw ``ts_rank_cd`` score — higher is better. The
    hybrid module re-ranks by rank order, so absolute magnitudes don't
    matter beyond the relative ordering.
    """

    segment_id: int
    transcript_id: UUID
    video_id: UUID
    start_sec: float
    end_sec: float
    text: str
    rank: float


class _DBConn(Protocol):
    dialect: str

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]: ...


_PG_FTS_SQL = """
SELECT  s.id            AS segment_id,
        s.transcript_id AS transcript_id,
        t.video_id      AS video_id,
        s.start_sec     AS start_sec,
        s.end_sec       AS end_sec,
        s.text          AS text,
        ts_rank_cd(s.search_tsv, query) AS rank
FROM    transcript_segments s
        JOIN transcripts t ON t.id = s.transcript_id,
        to_tsquery('maktaba_search', $1) AS query
WHERE   s.search_tsv @@ query
        AND t.is_active = true
        AND ($2::uuid IS NULL OR t.video_id = $2)
ORDER BY rank DESC, s.id ASC
LIMIT   $3
"""


async def fts_search(
    db: _DBConn,
    query: str,
    *,
    video_id: UUID | None = None,
    limit: int = 50,
    prefix_last: bool = False,
) -> list[FTSHit]:
    """Run an FTS query against ``transcript_segments``.

    Only the Postgres dialect is supported by this function. Callers on
    SQLite must route through the dialect-aware DB layer (which uses
    the FTS5 virtual table from migration 0016).

    ``video_id`` scopes the search to one video; ``None`` searches the
    whole active corpus.
    """
    if db.dialect != "postgres":
        raise NotImplementedError("fts_search requires Postgres; use SQLite FTS5 path elsewhere")
    tsq = build_tsquery(query, prefix_last=prefix_last)
    if not tsq:
        return []
    rows = await db.fetch(_PG_FTS_SQL, tsq, video_id, limit)
    return [
        FTSHit(
            segment_id=int(r["segment_id"]),
            transcript_id=r["transcript_id"],
            video_id=r["video_id"],
            start_sec=float(r["start_sec"]),
            end_sec=float(r["end_sec"]),
            text=str(r["text"]),
            rank=float(r["rank"]),
        )
        for r in rows
    ]
