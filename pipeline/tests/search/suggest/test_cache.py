"""SuggestCache: LRU eviction + TTL expiry under a monkeypatched clock."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.search.suggest.cache import SuggestCache
from maktaba_pipeline.search.suggest.service import Suggestion


def _sug(term: str) -> list[Suggestion]:
    return [Suggestion(term=term, source="ngram", score=1.0)]


@pytest.mark.unit
def test_put_and_get_round_trip() -> None:
    cache: SuggestCache = SuggestCache(max_entries=4, ttl_sec=60.0)
    cache.put("a", _sug("alpha"))
    got = cache.get("a")
    assert got is not None
    assert got[0].term == "alpha"


@pytest.mark.unit
def test_get_missing_returns_none() -> None:
    cache: SuggestCache = SuggestCache()
    assert cache.get("nope") is None


@pytest.mark.unit
def test_lru_evicts_oldest_when_full() -> None:
    cache: SuggestCache = SuggestCache(max_entries=3, ttl_sec=60.0)
    cache.put("a", _sug("alpha"))
    cache.put("b", _sug("beta"))
    cache.put("c", _sug("gamma"))
    # Touch "a" so "b" becomes the LRU.
    assert cache.get("a") is not None
    cache.put("d", _sug("delta"))  # Evicts "b".

    assert cache.get("a") is not None
    assert cache.get("b") is None
    assert cache.get("c") is not None
    assert cache.get("d") is not None
    assert len(cache) == 3


@pytest.mark.unit
def test_ttl_expires_old_entries(monkeypatch: pytest.MonkeyPatch) -> None:
    fake_now = {"t": 1000.0}

    def fake_monotonic() -> float:
        return fake_now["t"]

    monkeypatch.setattr("time.monotonic", fake_monotonic)

    cache: SuggestCache = SuggestCache(max_entries=4, ttl_sec=5.0)
    cache.put("a", _sug("alpha"))

    # Still warm at t=1003.
    fake_now["t"] = 1003.0
    assert cache.get("a") is not None

    # Past TTL at t=1010 — expired.
    fake_now["t"] = 1010.0
    assert cache.get("a") is None
    # Expired key was dropped, not just hidden.
    assert len(cache) == 0


@pytest.mark.unit
def test_put_refreshes_ttl(monkeypatch: pytest.MonkeyPatch) -> None:
    fake_now = {"t": 0.0}

    def fake_monotonic() -> float:
        return fake_now["t"]

    monkeypatch.setattr("time.monotonic", fake_monotonic)

    cache: SuggestCache = SuggestCache(max_entries=4, ttl_sec=10.0)
    cache.put("k", _sug("v"))
    fake_now["t"] = 8.0
    # Refresh the entry.
    cache.put("k", _sug("v2"))
    fake_now["t"] = 15.0  # 7s after the refresh.
    got = cache.get("k")
    assert got is not None
    assert got[0].term == "v2"


@pytest.mark.unit
def test_rejects_invalid_construction() -> None:
    with pytest.raises(ValueError):
        SuggestCache(max_entries=0)
    with pytest.raises(ValueError):
        SuggestCache(ttl_sec=0)


@pytest.mark.unit
def test_clear_drops_all_entries() -> None:
    cache: SuggestCache = SuggestCache()
    cache.put("a", _sug("alpha"))
    cache.put("b", _sug("beta"))
    cache.clear()
    assert len(cache) == 0
    _ = Any  # silence unused-import on strict ruff
