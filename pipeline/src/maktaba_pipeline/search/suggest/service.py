"""Prefix-driven suggest service.

A request takes a raw user prefix, normalizes it (Arabic-aware), and
either:

- returns popular terms for the library when the normalized prefix is
  shorter than 2 characters (so empty / single-character inputs still
  produce useful suggestions), or
- looks up matching rows in ``search_suggestion_terms`` ordered by
  frequency.

Saved searches and speaker pools are stubbed as empty result lists in
this slot; the merging skeleton is in place so wiring them in later
is purely additive.
"""

from __future__ import annotations

import math
from contextlib import AbstractAsyncContextManager
from dataclasses import dataclass
from typing import Any, Literal, Protocol

from ..fts.normalize import arabic_normalize
from .cache import SuggestCache

__all__ = ["SuggestRequest", "SuggestService", "Suggestion"]


SuggestionSource = Literal["saved", "speaker", "ngram"]


class _Row(Protocol):
    def __getitem__(self, key: str) -> Any: ...


class _DBConn(Protocol):
    dialect: str

    def transaction(self) -> AbstractAsyncContextManager[Any]: ...

    async def fetch(self, sql: str, *args: Any) -> list[_Row]: ...


@dataclass(frozen=True, slots=True)
class SuggestRequest:
    """One suggest call.

    ``library_id`` scopes the lookup to a single library; cross-
    library suggestion is intentionally not supported — each library
    is its own corpus.
    """

    prefix: str
    library_id: str
    limit: int = 8


@dataclass(frozen=True, slots=True)
class Suggestion:
    """One returned suggestion.

    ``source`` distinguishes ngram suggestions (corpus-driven) from
    saved-search and speaker-pool suggestions so the UI can render
    them differently. ``score`` is comparable only within a single
    source bucket — the service does a log-scaled normalisation
    before merging.
    """

    term: str
    source: SuggestionSource
    score: float


_POPULAR_SQL = """
SELECT term, frequency
  FROM search_suggestion_terms
 WHERE library_id = $1
 ORDER BY frequency DESC
 LIMIT $2
"""

_PREFIX_SQL = """
SELECT term, term_normalized, frequency
  FROM search_suggestion_terms
 WHERE library_id = $1
   AND term_normalized LIKE $2
 ORDER BY frequency DESC
 LIMIT $3
"""


def _score_from_frequency(freq: int) -> float:
    """Log-scale a raw frequency into a comparable score.

    ``log1p`` keeps small frequencies separable while compressing the
    long tail; it never returns negative or NaN values for valid
    integer input.
    """
    if freq <= 0:
        return 0.0
    return math.log1p(float(freq))


class SuggestService:
    """Library-scoped prefix suggester.

    A new request is normalized via
    :func:`maktaba_pipeline.search.fts.normalize.arabic_normalize`;
    short prefixes fall back to a popular-term lookup. Results from
    the three sources (saved searches, speakers, ngrams) are merged
    by best-of on ``term_normalized`` and the top ``limit`` are
    returned sorted by score descending.
    """

    def __init__(self, *, db: _DBConn, cache: SuggestCache | None = None) -> None:
        self._db = db
        self._cache = cache if cache is not None else SuggestCache()

    async def suggest(self, req: SuggestRequest) -> list[Suggestion]:
        """Run a suggest, honoring the cache.

        The cache key is composed from ``library_id``, the normalized
        prefix, and the limit so two requests with the same intent
        share a slot regardless of how the user typed the raw prefix.
        """
        normalized = arabic_normalize(req.prefix).strip()
        cache_key = f"{req.library_id}|{normalized}|{req.limit}"
        cached = self._cache.get(cache_key)
        if cached is not None:
            return cached

        if len(normalized) < 2:
            results = await self._popular(req.library_id, req.limit)
        else:
            ngram = await self._ngram_prefix(req.library_id, normalized, req.limit)
            saved = await self._saved_searches(req.library_id, normalized, req.limit)
            speaker = await self._speaker_pool(req.library_id, normalized, req.limit)
            results = self._merge(ngram + saved + speaker, req.limit)

        self._cache.put(cache_key, results)
        return results

    async def _popular(self, library_id: str, limit: int) -> list[Suggestion]:
        rows = await self._db.fetch(_POPULAR_SQL, library_id, limit)
        out: list[Suggestion] = []
        for row in rows:
            out.append(
                Suggestion(
                    term=str(row["term"]),
                    source="ngram",
                    score=_score_from_frequency(int(row["frequency"])),
                )
            )
        return out

    async def _ngram_prefix(
        self, library_id: str, normalized: str, limit: int
    ) -> list[Suggestion]:
        like_pattern = normalized + "%"
        rows = await self._db.fetch(
            _PREFIX_SQL,
            library_id,
            like_pattern,
            limit,
        )
        out: list[Suggestion] = []
        for row in rows:
            out.append(
                Suggestion(
                    term=str(row["term"]),
                    source="ngram",
                    score=_score_from_frequency(int(row["frequency"])),
                )
            )
        return out

    async def _saved_searches(
        self, library_id: str, normalized: str, limit: int
    ) -> list[Suggestion]:
        """Stub — saved-search source returns empty until wired up."""
        _ = (library_id, normalized, limit)
        return []

    async def _speaker_pool(
        self, library_id: str, normalized: str, limit: int
    ) -> list[Suggestion]:
        """Stub — speaker-pool source returns empty until wired up."""
        _ = (library_id, normalized, limit)
        return []

    def _merge(
        self, items: list[Suggestion], limit: int
    ) -> list[Suggestion]:
        """Deduplicate by normalized term and keep the highest score.

        Source rank ties are broken by score; if a term appears in
        multiple sources we keep the best-scoring representative
        (so a saved search will win over an ngram of equal score
        only if its score is strictly greater, which keeps the
        behavior predictable).
        """
        best: dict[str, Suggestion] = {}
        for sug in items:
            key = arabic_normalize(sug.term).strip()
            current = best.get(key)
            if current is None or sug.score > current.score:
                best[key] = sug
        ordered = sorted(best.values(), key=lambda s: s.score, reverse=True)
        return ordered[:limit]
