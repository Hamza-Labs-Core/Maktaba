"""Epic 5 — :mod:`maktaba_pipeline.search.fts` tests."""

from __future__ import annotations

import asyncio
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.search.fts import (
    FTSHit,
    build_tsquery,
    fts_search,
    normalize_query,
)

# --- normalize_query --------------------------------------------------


def test_normalize_lowercases_and_strips_diacritics() -> None:
    assert normalize_query("Café") == "cafe"


def test_normalize_strips_arabic_tashkil() -> None:
    # The fully-vowelled form must normalise to the bare consonants.
    assert normalize_query("النَّحْل") == "النحل"


def test_normalize_collapses_whitespace() -> None:
    assert normalize_query("  hello   world  ") == "hello world"


def test_normalize_returns_empty_for_blank() -> None:
    assert normalize_query("") == ""
    assert normalize_query("   ") == ""


# --- build_tsquery ----------------------------------------------------


def test_build_tsquery_ands_tokens() -> None:
    assert build_tsquery("hello world", prefix_last=False) == "hello & world"


def test_build_tsquery_marks_last_token_as_prefix() -> None:
    assert build_tsquery("hel", prefix_last=True) == "hel:*"
    assert build_tsquery("hello wor", prefix_last=True) == "hello & wor:*"


def test_build_tsquery_strips_operators() -> None:
    # User-typed query DSL is noise — operators get stripped, not escaped.
    out = build_tsquery("a & b | c", prefix_last=True)
    assert "&" not in out.replace(" & ", "")  # only the AND separator remains
    assert "|" not in out


def test_build_tsquery_empty_input() -> None:
    assert build_tsquery("") == ""
    assert build_tsquery("   ") == ""


# --- fts_search -------------------------------------------------------


class _FakePGFTSDB:
    """Captures the tsquery and returns canned rows."""

    dialect = "postgres"

    def __init__(self, rows: list[dict[str, Any]]) -> None:
        self._rows = rows
        self.calls: list[tuple[str, tuple[Any, ...]]] = []

    async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
        self.calls.append((sql, args))
        return self._rows


def _row(segment_id: int, *, rank: float = 0.5) -> dict[str, Any]:
    return {
        "segment_id": segment_id,
        "transcript_id": uuid4(),
        "video_id": uuid4(),
        "start_sec": 1.0,
        "end_sec": 2.0,
        "text": f"segment {segment_id}",
        "rank": rank,
    }


def test_fts_search_short_circuits_empty_query() -> None:
    async def run() -> None:
        db = _FakePGFTSDB([])
        out = await fts_search(db, "")
        assert out == []
        assert db.calls == []  # never hit DB

    asyncio.run(run())


def test_fts_search_passes_tsquery_string() -> None:
    async def run() -> None:
        db = _FakePGFTSDB([_row(1)])
        await fts_search(db, "hello world", prefix_last=False)
        assert len(db.calls) == 1
        # First positional arg is the tsquery expression.
        assert db.calls[0][1][0] == "hello & world"

    asyncio.run(run())


def test_fts_search_parses_rows_into_hits() -> None:
    async def run() -> None:
        db = _FakePGFTSDB([_row(1, rank=0.9), _row(2, rank=0.5)])
        hits = await fts_search(db, "hi")
        assert all(isinstance(h, FTSHit) for h in hits)
        assert [h.segment_id for h in hits] == [1, 2]
        assert [h.rank for h in hits] == [0.9, 0.5]

    asyncio.run(run())


def test_fts_search_scopes_to_video_id() -> None:
    async def run() -> None:
        db = _FakePGFTSDB([])
        vid = uuid4()
        await fts_search(db, "x", video_id=vid)
        # Second positional arg is the video_id filter.
        assert db.calls[0][1][1] == vid

    asyncio.run(run())


def test_fts_search_rejects_sqlite_dialect() -> None:
    async def run() -> None:
        class _Sqlite:
            dialect = "sqlite"

            async def fetch(self, sql: str, *args: Any) -> list[dict[str, Any]]:
                return []

        with pytest.raises(NotImplementedError):
            await fts_search(_Sqlite(), "hi")

    asyncio.run(run())


def test_fts_search_uuid_round_trips() -> None:
    async def run() -> None:
        tid = uuid4()
        vid = uuid4()
        row = {
            "segment_id": 1,
            "transcript_id": tid,
            "video_id": vid,
            "start_sec": 0.0,
            "end_sec": 1.0,
            "text": "a",
            "rank": 1.0,
        }
        hits = await fts_search(_FakePGFTSDB([row]), "a")
        assert hits[0].transcript_id == tid
        assert isinstance(hits[0].transcript_id, UUID)

    asyncio.run(run())
