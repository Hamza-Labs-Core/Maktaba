"""Query-string builders for the Postgres and SQLite FTS engines.

A search query goes through three transforms:

1. :func:`arabic_normalize` to fold diacritics, alef variants, etc.
2. Tokenize on whitespace.
3. Engine-specific re-assembly — Postgres ``tsquery`` uses ``&``
   (or ``<->`` for phrase), SQLite ``FTS5 MATCH`` wraps the joined
   tokens in double quotes.

The two forms are returned as a single :class:`FtsClause` so the
caller can pick the right field for its dialect.
"""

from __future__ import annotations

from dataclasses import dataclass

from .normalize import arabic_normalize

__all__ = ["FtsClause", "build_fts_query"]


@dataclass(frozen=True, slots=True)
class FtsClause:
    """Engine-specific query strings for one logical search.

    ``postgres_form`` is a ``tsquery``-shaped string (``token & token``
    or ``token <-> token`` for an exact phrase). ``sqlite_form`` is
    the MATCH right-hand-side, already wrapped in double quotes when
    the input is a phrase so the FTS5 parser treats it literally.

    ``tokens`` is the post-normalization token list; downstream code
    (e.g. the snippet builder) uses it to highlight matches in the
    hydrated unit text.
    """

    tsquery: str
    postgres_form: str
    sqlite_form: str
    tokens: tuple[str, ...]


def build_fts_query(query: str, *, exact_phrase: bool = False) -> FtsClause:
    """Build engine-specific FTS query forms from a user-typed query.

    Empty / whitespace-only input → an empty clause whose ``tokens``
    is empty; both engines reject empty queries upstream so the
    caller is expected to short-circuit before issuing the search.
    """
    normalized = arabic_normalize(query)
    tokens = tuple(t for t in normalized.split(" ") if t)

    if not tokens:
        return FtsClause(
            tsquery="",
            postgres_form="",
            sqlite_form="",
            tokens=(),
        )

    if exact_phrase:
        # tsquery phrase operator <-> demands adjacent positions.
        pg_form = " <-> ".join(tokens)
        # FTS5 MATCH treats a double-quoted string as a phrase.
        sqlite_form = '"' + " ".join(tokens) + '"'
    else:
        pg_form = " & ".join(tokens)
        # AND across tokens is the FTS5 default — but quoting each
        # token defends against operator chars (``-``, ``*``, ``"``)
        # leaking through and being parsed as syntax.
        sqlite_form = " ".join(f'"{t}"' for t in tokens)

    return FtsClause(
        tsquery=pg_form,
        postgres_form=pg_form,
        sqlite_form=sqlite_form,
        tokens=tokens,
    )
