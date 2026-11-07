"""Story 54.09 T01, T02: the plugins' circuit breaker. The windows' rule alone, and on
Postgres: five traps in ten minutes, or more than half of the last twenty jobs failed,
trip it once and ask the API to hold the plugin; an ok job counts; another plugin's
outcomes are its own."""

from __future__ import annotations

from typing import Any

import pytest

from maktaba_pipeline.plugins import breaker


def test_the_windows() -> None:
    now = 10_000.0
    crash = [{"at": now - i, "cause": "plugin_trap"} for i in range(5)]
    assert breaker.should_trip([], crash, [], now) == "crashes"
    old = [{"at": now - breaker.WINDOW_S - 1, "cause": "plugin_trap"}] + crash[:4]
    assert breaker.should_trip([], old, [], now) == ""
    recent = [{"at": now, "cause": "plugin_failed"}] * 11 + [{"at": now, "cause": ""}] * 9
    assert breaker.should_trip(recent, [], [], now) == "failures"
    assert breaker.should_trip(recent[:19], [], [], now) == ""  # fewer than 20 jobs
    half = [{"at": now, "cause": "plugin_failed"}] * 10 + [{"at": now, "cause": ""}] * 10
    assert breaker.should_trip(half, [], [], now) == ""
    assert breaker.should_trip([], [], [{"at": now, "n": 50}], now) == "refusals"
    assert breaker.should_trip([], [], [{"at": now - 601, "n": 50}], now) == ""


async def _setup(db: Any, monkeypatch: pytest.MonkeyPatch) -> list[tuple[str, str]]:
    from maktaba_pipeline.engine.sql import is_pg

    if not is_pg(db):
        pytest.skip("plugins need a Postgres home")
    asked: list[tuple[str, str]] = []

    async def hold(pid: str, why: str) -> None:
        asked.append((pid, why))

    monkeypatch.setattr(breaker, "_ask_hold", hold)
    return asked


@pytest.mark.asyncio
async def test_five_traps_trip_once(db: Any, monkeypatch: pytest.MonkeyPatch) -> None:
    asked = await _setup(db, monkeypatch)
    now = 50_000.0
    for i in range(4):
        assert await breaker.record(db, "dev.example.a", "plugin_trap", now=now + i) == ""
    await breaker.record(db, "dev.example.b", "plugin_trap", now=now)  # another plugin's
    assert await breaker.record(db, "dev.example.a", "", now=now + 4) == ""  # an ok job
    assert await breaker.record(db, "dev.example.a", "plugin_timeout", now=now + 5) == "crashes"
    assert asked == [("dev.example.a", "crashes")]
    row = await db.fetchrow(
        "SELECT tripped, trip_reason, jobs_done, jobs_failed, last_error_code"
        " FROM plugin_health WHERE plugin_id = 'dev.example.a'"
    )
    assert row is not None and row["tripped"] and row["trip_reason"] == "crashes"
    assert (row["jobs_done"], row["jobs_failed"], row["last_error_code"]) == (
        1,
        5,
        "plugin_timeout",
    )
    # tripped already: no second trip, the hold is asked again (it may not have been written)
    assert await breaker.record(db, "dev.example.a", "plugin_trap", now=now + 6) == ""
    assert asked[-1] == ("dev.example.a", "")
    b = await db.fetchrow("SELECT tripped FROM plugin_health WHERE plugin_id = 'dev.example.b'")
    assert b is not None and not b["tripped"]


@pytest.mark.asyncio
async def test_most_of_the_last_twenty_failed(db: Any, monkeypatch: pytest.MonkeyPatch) -> None:
    asked = await _setup(db, monkeypatch)
    now = 90_000.0
    outcomes = ["plugin_failed"] * 11 + [""] * 8
    for i, cause in enumerate(outcomes):
        assert await breaker.record(db, "dev.example.c", cause, now=now + i * 100) == ""
    assert await breaker.record(db, "dev.example.c", "", now=now + 2000) == "failures"
    assert asked == [("dev.example.c", "failures")]
    row = await db.fetchrow("SELECT recent FROM plugin_health WHERE plugin_id = 'dev.example.c'")
    assert row is not None and len(breaker._list(row["recent"])) == breaker.LAST_N
