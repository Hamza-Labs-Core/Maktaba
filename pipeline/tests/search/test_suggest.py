"""Epic 5 — :mod:`maktaba_pipeline.search.suggest` tests."""

from __future__ import annotations

import asyncio
from datetime import UTC, datetime, timedelta
from typing import Any
from uuid import uuid4

import pytest

from maktaba_pipeline.search.suggest import (
    rank_by_recency,
    record_search,
    suggest,
)

# --- rank_by_recency --------------------------------------------------


def test_rank_by_recency_orders_recent_first() -> None:
    now = datetime(2026, 5, 10, tzinfo=UTC)
    rows = [
        {"query": "old", "query_norm": "old", "hits": 5, "last_used_at": now - timedelta(days=60)},
        {"query": "new", "query_norm": "new", "hits": 5, "last_used_at": now - timedelta(days=1)},
    ]
    ranked = rank_by_recency(rows, now=now, half_life_days=30)
    assert [s.query for s in ranked] == ["new", "old"]


def test_rank_by_recency_half_life_drops_to_half() -> None:
    now = datetime(2026, 5, 10, tzinfo=UTC)
    rows = [
        {"query": "fresh", "query_norm": "fresh", "hits": 10, "last_used_at": now},
        {
            "query": "aged",
            "query_norm": "aged",
            "hits": 10,
            "last_used_at": now - timedelta(days=30),
        },
    ]
    ranked = {s.query: s for s in rank_by_recency(rows, now=now, half_life_days=30)}
    # 30-day-old row → exactly half the score of the fresh row (modulo
    # ln(2) rounding).
    assert abs(ranked["aged"].score / ranked["fresh"].score - 0.5) < 1e-6


def test_rank_by_recency_iso_string_timestamps() -> None:
    # SQLite returns ISO8601 strings; the helper has to accept them.
    now = datetime(2026, 5, 10, tzinfo=UTC)
    rows = [
        {
            "query": "x",
            "query_norm": "x",
            "hits": 1,
            "last_used_at": "2026-05-09T00:00:00.000Z",
        },
    ]
    ranked = rank_by_recency(rows, now=now)
    assert ranked[0].query == "x"


def test_rank_by_recency_rejects_non_positive_half_life() -> None:
    with pytest.raises(ValueError):
        rank_by_recency([], half_life_days=0)


# --- record_search ----------------------------------------------------


class _FakeHistoryDB:
    dialect = "postgres"

    def __init__(self) -> None:
        self.executes: list[tuple[str, tuple[Any, ...]]] = []
        self.fetch_rows: list[dict[str, Any]] = []

    async def execute(self, sql: str, *args: Any) -> None:
        self.executes.append((sql, args))

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        return self.fetch_rows


def test_record_search_normalises_query() -> None:
    async def run() -> None:
        db = _FakeHistoryDB()
        ok = await record_search(db, "Café  ")
        assert ok is True
        # query is preserved verbatim; query_norm is the cleaned form.
        _, args = db.executes[0]
        assert args[1] == "Café  "  # original `query`
        assert args[2] == "cafe"     # normalised `query_norm`

    asyncio.run(run())


def test_record_search_skips_empty() -> None:
    async def run() -> None:
        db = _FakeHistoryDB()
        ok = await record_search(db, "   ")
        assert ok is False
        assert db.executes == []

    asyncio.run(run())


def test_record_search_sqlite_passes_str_user_id() -> None:
    async def run() -> None:
        db = _FakeHistoryDB()
        db.dialect = "sqlite"
        uid = uuid4()
        await record_search(db, "hi", user_id=uid)
        _, args = db.executes[0]
        assert args[0] == str(uid)

    asyncio.run(run())


# --- suggest ----------------------------------------------------------


def test_suggest_empty_prefix_returns_empty() -> None:
    async def run() -> None:
        db = _FakeHistoryDB()
        out = await suggest(db, "  ")
        assert out == []

    asyncio.run(run())


def test_suggest_ranks_by_recency_weighted_hits() -> None:
    async def run() -> None:
        now = datetime(2026, 5, 10, tzinfo=UTC)
        db = _FakeHistoryDB()
        db.fetch_rows = [
            {
                "query": "hello world",
                "query_norm": "hello world",
                "hits": 100,
                "last_used_at": now - timedelta(days=365),
            },
            {
                "query": "hello there",
                "query_norm": "hello there",
                "hits": 3,
                "last_used_at": now,
            },
        ]
        out = await suggest(db, "hel", now=now, half_life_days=30)
        # Even though "hello world" has 33× the raw hits, a year of
        # decay puts it well below the fresh "hello there".
        assert out[0].query == "hello there"

    asyncio.run(run())
