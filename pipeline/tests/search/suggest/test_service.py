"""SuggestService: prefix lookup, dedup, and short-prefix fallback.

The service is tested against a fake DB that hands back canned rows
keyed by the SQL fragment, so we exercise the same SQL strings the
production module uses without needing a real driver.
"""

from __future__ import annotations

from contextlib import asynccontextmanager
from typing import Any

import pytest

from maktaba_pipeline.search.suggest.cache import SuggestCache
from maktaba_pipeline.search.suggest.service import (
    SuggestRequest,
    SuggestService,
)

LIBRARY_ID = "lib-1"


class _FakeRow(dict[str, object]):
    pass


class _FakeDB:
    dialect = "postgres"

    def __init__(
        self,
        *,
        popular: list[_FakeRow] | None = None,
        prefix_rows: list[_FakeRow] | None = None,
    ) -> None:
        self.popular = popular or []
        self.prefix_rows = prefix_rows or []
        self.fetches: list[tuple[str, tuple[Any, ...]]] = []

    @asynccontextmanager
    async def transaction(self):  # type: ignore[no-untyped-def]
        yield self

    async def fetch(self, sql: str, *args: Any) -> list[Any]:
        self.fetches.append((sql, args))
        if "ORDER BY frequency DESC" in sql and "LIKE" in sql:
            return list(self.prefix_rows)
        if "ORDER BY frequency DESC" in sql:
            return list(self.popular)
        return []


@pytest.mark.asyncio
async def test_short_prefix_returns_popular_terms() -> None:
    db = _FakeDB(
        popular=[
            _FakeRow(term="alpha", frequency=100),
            _FakeRow(term="beta", frequency=50),
            _FakeRow(term="gamma", frequency=10),
        ]
    )
    svc = SuggestService(db=db, cache=SuggestCache(max_entries=8, ttl_sec=10.0))

    out = await svc.suggest(SuggestRequest(prefix="a", library_id=LIBRARY_ID, limit=5))

    terms = [s.term for s in out]
    assert terms == ["alpha", "beta", "gamma"]
    # Should have hit the popular SQL only (not the prefix SQL).
    assert any("ORDER BY frequency DESC" in sql and "LIKE" not in sql for sql, _ in db.fetches)


@pytest.mark.asyncio
async def test_prefix_lookup_returns_ngrams() -> None:
    db = _FakeDB(
        prefix_rows=[
            _FakeRow(term="hello world", term_normalized="hello world", frequency=20),
            _FakeRow(term="hello there", term_normalized="hello there", frequency=12),
        ]
    )
    svc = SuggestService(db=db)

    out = await svc.suggest(
        SuggestRequest(prefix="hello", library_id=LIBRARY_ID, limit=5)
    )

    terms = [s.term for s in out]
    assert "hello world" in terms
    assert "hello there" in terms
    # Both come from the ngram source.
    assert all(s.source == "ngram" for s in out)


@pytest.mark.asyncio
async def test_dedup_by_normalized_term() -> None:
    # Two rows that normalize to the same term; service should keep
    # one — the higher-frequency one wins.
    db = _FakeDB(
        prefix_rows=[
            _FakeRow(term="hello", term_normalized="hello", frequency=5),
            _FakeRow(term="hello", term_normalized="hello", frequency=20),
        ]
    )
    svc = SuggestService(db=db)

    out = await svc.suggest(
        SuggestRequest(prefix="hello", library_id=LIBRARY_ID, limit=5)
    )

    assert len(out) == 1
    # Higher-frequency input wins → its score is log1p(20) > log1p(5).
    assert out[0].term == "hello"


@pytest.mark.asyncio
async def test_cache_hits_avoid_db() -> None:
    db = _FakeDB(prefix_rows=[_FakeRow(term="hello", term_normalized="hello", frequency=3)])
    svc = SuggestService(db=db)

    req = SuggestRequest(prefix="hello", library_id=LIBRARY_ID, limit=5)
    first = await svc.suggest(req)
    fetches_after_first = len(db.fetches)
    second = await svc.suggest(req)

    assert first == second
    # Second call hit cache → no extra fetches.
    assert len(db.fetches) == fetches_after_first


@pytest.mark.asyncio
async def test_limit_truncates_results() -> None:
    db = _FakeDB(
        prefix_rows=[
            _FakeRow(term=f"hello{i}", term_normalized=f"hello{i}", frequency=100 - i)
            for i in range(10)
        ]
    )
    svc = SuggestService(db=db)

    out = await svc.suggest(
        SuggestRequest(prefix="hello", library_id=LIBRARY_ID, limit=3)
    )
    assert len(out) == 3
    # Sorted by frequency desc → hello0 first.
    assert out[0].term == "hello0"
