"""FTS subpackage — Postgres tsvector + SQLite FTS5 search.

Public API mirrors the structure described in plan-05-02: a
normalization helper, a query builder, two dialect-specific search
functions, and a snippet builder. The two engines return rows in
the same shape (``(unit_id, score)`` with higher = better) so the
:mod:`engine` module can treat them uniformly.
"""

from __future__ import annotations

from .normalize import arabic_normalize
from .postgres import postgres_fts_search
from .query import FtsClause, build_fts_query
from .snippet import build_snippet
from .sqlite import register_arabic_normalize, sqlite_fts_search

__all__ = [
    "FtsClause",
    "arabic_normalize",
    "build_fts_query",
    "build_snippet",
    "postgres_fts_search",
    "register_arabic_normalize",
    "sqlite_fts_search",
]
