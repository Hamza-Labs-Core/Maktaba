"""The ``exact_dates`` stage: per-video exact upload dates for series members, politely, and
only when the rules switch it on."""

from __future__ import annotations

import uuid
from collections.abc import Iterator
from datetime import date
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.graph import GRAPH, Subject
from maktaba_pipeline.engine.sql import dumps, is_pg, now, to_ts
from maktaba_pipeline.engine.stages import exact_dates
from maktaba_pipeline.engine.stages.youtube_sync import set_fetcher_factory
from maktaba_pipeline.integrations.youtube import BotCheck
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import add_episode, error_of, events, run_stage, setup_runtime
from tests.extensions.harness import summary_of as summary

A, B, C = "AAAAAAAAAAA", "BBBBBBBBBBB", "CCCCCCCCCCC"


class FakeFetcher:
    def __init__(self) -> None:
        self.pages: dict[str, dict[str, Any] | None] = {
            A: {"upload_date": "20240315", "description": "about A"},
            B: {"upload_date": "20230102", "description": "about B"},
            C: {"upload_date": "20220101"},
        }
        self.calls: list[tuple[str, str, str | None]] = []
        self.bot_after: int | None = None

    def video_info(
        self, youtube_id: str, *, lang: str, cookies_from_browser: str | None = None
    ) -> dict[str, Any] | None:
        if self.bot_after is not None and len(self.calls) >= self.bot_after:
            raise BotCheck("Sign in to confirm you're not a bot")
        self.calls.append((youtube_id, lang, cookies_from_browser))
        return self.pages.get(youtube_id)


@pytest.fixture
def fetcher() -> Iterator[FakeFetcher]:
    f = FakeFetcher()
    previous = set_fetcher_factory(lambda: f)  # type: ignore[arg-type,return-value]
    yield f
    set_fetcher_factory(previous)


@pytest.fixture
def delays() -> Iterator[list[float]]:
    seen: list[float] = []

    async def record(seconds: float) -> None:
        seen.append(seconds)

    previous = exact_dates.set_sleep(record)
    yield seen
    exact_dates.set_sleep(previous)


async def set_rules(db: Any, value: dict[str, Any]) -> None:
    await db.execute(
        "INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, $3)"
        " ON CONFLICT (key) DO UPDATE SET value = excluded.value",
        "rules.exactdates",
        dumps(value),
        to_ts(db, now()),
    )


async def world(db: Any) -> UUID:
    """A (series member), B (loose video), C (series member, exact date already)."""
    lib = await make_library(db, "YT")
    series, _ = await make_series_and_season(db, lib)
    for yid, member, quality in ((A, True, "approximate"), (B, False, "approximate"),
                                 (C, True, "exact")):  # fmt: skip
        v = await make_video(db, lib)
        if member:
            await add_episode(db, series, v)
        await db.execute(
            "INSERT INTO media_metadata_enrichment (id, video_id, provider, external_id, mapped,"
            " confidence, is_accepted) VALUES ($1,$2,'youtube',$3,'{}',1.0,$4)",
            str(uuid.uuid4()),
            str(v),
            yid,
            True,
        )
        d = date(2024, 3, 1)
        await db.execute(
            "INSERT INTO youtube_videos (youtube_id, channel_id, title, upload_date, date_quality)"
            " VALUES ($1,'UCx',$2,$3,$4)",
            yid,
            f"video {yid}",
            d if is_pg(db) else d.isoformat(),
            quality,
        )
    return lib


async def row(db: Any, yid: str) -> Any:
    return await db.fetchrow(
        "SELECT CAST(upload_date AS TEXT) AS d, date_quality, description FROM youtube_videos"
        " WHERE youtube_id = $1",
        yid,
    )


async def exact_job(db: Any, rt: Any, lib: UUID, **kw: Any) -> Any:
    return await run_stage(db, rt, "exact_dates", "library", lib, **kw)


def test_the_stage_follows_the_listing_and_feeds_detect() -> None:
    by = {s.name: s for s in GRAPH}
    assert ("exact_dates", "same") in {(e.to, e.map) for e in by["youtube_sync"].edges}
    assert [(e.to, e.map) for e in by["exact_dates"].edges] == [("detect", "same")]
    assert by["exact_dates"].pool == "network_yt" and by["exact_dates"].entry_for == ("library",)


@pytest.mark.asyncio
async def test_it_is_off_until_the_rules_enable_it(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    rt = await setup_runtime(db, [])
    job = await exact_job(db, rt, lib)
    assert job["state"] == "done" and summary(job)["enabled"] is False
    assert fetcher.calls == [] and (await row(db, A))["date_quality"] == "approximate"


@pytest.mark.asyncio
async def test_series_members_get_their_exact_date_politely(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    await set_rules(
        db, {"enabled": True, "delay_s": 3.0, "lang": "en", "cookies_from_browser": "firefox"}
    )
    rt = await setup_runtime(db, [])
    job = await exact_job(db, rt, lib)
    assert job["state"] == "done" and job["outcome"] == "changed", error_of(job)
    assert fetcher.calls == [(A, "en", "firefox")]  # not B (no series), not C (exact already)
    a = await row(db, A)
    assert str(a["d"])[:10] == "2024-03-15" and a["date_quality"] == "exact"
    assert a["description"] == "about A"
    assert (await row(db, B))["date_quality"] == "approximate"
    assert len(delays) == 1 and 1.5 <= delays[0] <= 4.5  # delay_s x (0.5 .. 1.5)
    assert summary(job)["exact"] == 1
    triggered = [t for e in await events(db, job["id"], "triggered") for t in e["to"]]
    assert {t["stage"] for t in triggered} == {"detect"}  # detect runs again on the new dates


@pytest.mark.asyncio
async def test_scope_all_takes_every_linked_video(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    await set_rules(db, {"enabled": True, "scope": "all"})
    rt = await setup_runtime(db, [])
    await exact_job(db, rt, lib)
    assert sorted(c[0] for c in fetcher.calls) == [A, B]


@pytest.mark.asyncio
async def test_a_bot_check_stops_the_run_and_keeps_what_was_fetched(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    await set_rules(db, {"enabled": True, "scope": "all"})
    fetcher.bot_after = 1
    rt = await setup_runtime(db, [])
    job = await exact_job(db, rt, lib)
    assert error_of(job)["kind"] == "youtube_bot_check" and error_of(job)["retryable"] is False
    assert (await row(db, A))["date_quality"] == "exact"  # the first one stays
    assert (await row(db, B))["date_quality"] == "approximate"
    fetcher.bot_after = None
    again = await exact_job(db, rt, lib)  # the next run carries on with the rest
    assert again["state"] == "done" and (await row(db, B))["date_quality"] == "exact"


@pytest.mark.asyncio
async def test_a_run_is_bounded_and_unreadable_videos_are_not_fatal(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    await set_rules(db, {"enabled": True, "scope": "all", "max_per_run": 1})
    fetcher.pages[A] = None  # private or removed
    rt = await setup_runtime(db, [])
    job = await exact_job(db, rt, lib)
    assert job["state"] == "done" and len(fetcher.calls) == 1
    assert summary(job)["unreadable"] == 1 and (await row(db, A))["date_quality"] == "approximate"


@pytest.mark.asyncio
async def test_nothing_new_is_not_asked_again(
    db: Database, fetcher: FakeFetcher, delays: list[float]
) -> None:
    lib = await world(db)
    await set_rules(db, {"enabled": True})
    rt = await setup_runtime(db, [])
    await exact_job(db, rt, lib)
    again = await exact_job(db, rt, lib)
    assert again["outcome"] in ("skipped_unchanged", "unchanged") and len(fetcher.calls) == 1
    fp = exact_dates.IMPL.input_fingerprint
    assert fp is not None and await fp(db, Subject("library", lib)) is not None
