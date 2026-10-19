"""The ``dizquetv_audit`` stage: is what each channel airs about the channel? Results go to
two tables and to ``channel-audit.html`` beside the review pages."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.engine.graph import CATALOG_ID, Subject
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages import review_pages
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.schedule.channel_planner import PlannerConfig
from tests.extensions.harness import error_of, run_stage, setup_runtime, summary_of
from tests.extensions.test_dizquetv import (
    SETTINGS,
    World,
    channel_world,
    channels_job,
    make_world,
)
from tests.extensions.test_dizquetv_parity import set_rules

THEME = next(t for t in PlannerConfig().themes if t.topics and t.kind != "quran")
HOUR = 3_600_000


@pytest.fixture(autouse=True)
def cache(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> Path:
    out = tmp_path / "cache" / "reviews"
    monkeypatch.setattr(review_pages, "reviews_dir", lambda: out)
    return out


def programme(rk: str, show: str) -> dict[str, Any]:
    return {"ratingKey": rk, "title": f"Episode {rk}", "duration": HOUR, "customShowName": show}


async def tag_season(db: Any, w: World, tag: str) -> None:
    await db.execute(
        "INSERT INTO season_tags (season_id, tag, score, evidence) VALUES ($1,$2,1.0,$3)",
        str(w.season),
        tag,
        '{"why": "name:x"}',
    )


async def audited(db: Any) -> tuple[World, Any]:
    """A theme channel airing two episodes of a show about the theme and one loose video, a
    channel that is no theme's, and one filler list."""
    w = await make_world(db)
    await tag_season(db, w, f"genre:{THEME.topics[0]}")
    await db.execute(
        "DELETE FROM series_episodes WHERE video_id = $1", str(w.videos[2])
    )  # the third is in no series
    w.dz.channels[5] = {
        "number": 5,
        "name": "Theme channel",
        "maktabaPlan": {"key": THEME.key},
        "startTime": "2026-01-01T00:00:00.000Z",
        "programs": [
            programme("101", "maktaba-plan · Show [L1]"),
            programme("102", "maktaba-plan · Show [L1]"),
            programme("103", "maktaba-plan · Loose [L2]"),
            programme("199", "maktaba-plan · Dawn Quran [library.quran]"),
            {"duration": HOUR, "isOffline": True},
        ],
        "fillerCollections": [{"id": "f1", "weight": 100}],
    }
    w.dz.channels[6] = {
        "number": 6,
        "name": "Mix",
        "maktabaPlan": {"key": "mix"},
        "startTime": "2026-01-01T00:00:00.000Z",
        "programs": [programme("103", "maktaba-plan · Loose [L2]")],
        "fillerCollections": [],
    }
    w.dz.fillers["f1"] = {
        "name": "maktaba-plan · Breaks [ch.breaks]",
        "content": [{"ratingKey": "103", "title": "a clip"}],
    }
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    return w, rt


async def audit_job(db: Any, rt: Any, **kw: Any) -> Any:
    return await run_stage(db, rt, "dizquetv_audit", "catalog", CATALOG_ID, **kw)


@pytest.mark.asyncio
async def test_each_channels_airtime_is_judged_against_its_theme(db: Database, cache: Path) -> None:
    w, rt = await audited(db)
    job = await audit_job(db, rt)
    assert job["state"] == "done", error_of(job)
    assert summary_of(job)["channels"] == 2

    audit_id = await db.fetchval("SELECT max(id) FROM channel_audits")
    rows = {
        int(r["number"]): r
        for r in await db.fetch(
            "SELECT * FROM channel_audit_channels WHERE audit_id = $1", audit_id
        )
    }
    theme = rows[5]
    shares = loads(theme["shares"])
    assert theme["theme"] == THEME.key and theme["filler_clips"] == 1
    # 2 h strong (the show's genre is the theme), 1 h of the dawn Quran hour (by design: the
    # theme is not the Quran channel), 1 h of a video in no series (nothing ties it)
    assert theme["hours"] > 0
    assert shares["strong"] > 40 and shares["design"] > 15 and shares["none"] > 15
    detail = loads(theme["detail"])
    loose = next(s for s in detail["shows"] if "Loose" in s["show"])
    assert loose["examples"][0]["title"] == "Episode 103"
    assert loads(theme["filler_share"])["none"] == 100.0  # the one clip is no theme's
    assert detail["filler_unrelated"][0]["title"] == "a clip"

    mix = rows[6]
    assert mix["theme"] is None and loads(mix["shares"])["strong"] == 100.0  # not a theme channel


@pytest.mark.asyncio
async def test_the_report_is_written_beside_the_review_pages(db: Database, cache: Path) -> None:
    w, rt = await audited(db)
    await audit_job(db, rt)
    page = cache / "channel-audit.html"
    assert page.is_file() and (cache / "channel-audit.txt").is_file()
    html = page.read_text(encoding="utf-8")
    assert "<h1>Channel audit</h1>" in html and "Theme channel" in html and "Mix" in html
    stored = await db.fetchval("SELECT html_path FROM channel_audits")
    assert Path(stored) == page


@pytest.mark.asyncio
async def test_only_the_latest_audits_are_kept(db: Database) -> None:
    w, rt = await audited(db)
    await set_rules(db, "audit", {"keep": 2, "window_days": 7})
    for _ in range(3):
        await audit_job(db, rt, force=True)
    assert await db.fetchval("SELECT count(*) FROM channel_audits") == 2
    assert await db.fetchval("SELECT max(window_days) FROM channel_audits") == 7
    left = await db.fetchval("SELECT count(*) FROM channel_audit_channels")
    assert left == 4  # the removed audit's channel rows went with it


@pytest.mark.asyncio
async def test_it_can_be_switched_off(db: Database, cache: Path) -> None:
    w, rt = await audited(db)
    await set_rules(db, "audit", {"enabled": False})
    job = await audit_job(db, rt)
    assert summary_of(job)["enabled"] is False and not cache.exists()
    assert await db.fetchval("SELECT count(*) FROM channel_audits") == 0
    assert w.dz.calls == []  # dizqueTV was not even read


@pytest.mark.asyncio
async def test_an_unreachable_dizquetv_is_a_retryable_failure(db: Database) -> None:
    w, rt = await audited(db)
    w.dz.down = True
    job = await audit_job(db, rt)
    err = error_of(job)
    assert err["kind"] == "dizquetv_unreachable" and err["retryable"] is True


@pytest.mark.asyncio
async def test_the_audit_only_reads_dizquetv(db: Database) -> None:
    w, rt = await audited(db)
    await audit_job(db, rt)
    assert w.dz.writes == [] and all(m == "GET" for m, _ in w.dz.calls)


@pytest.mark.asyncio
async def test_it_follows_the_channel_write_and_the_rules(db: Database) -> None:
    w, rt = await channel_world(db)
    fp = w.extensions[1].impls()["dizquetv_audit"].input_fingerprint
    assert fp is not None
    subject = Subject("catalog", CATALOG_ID)
    before = await fp(db, subject)
    assert before == await fp(db, subject)
    await channels_job(db, rt)  # a channel write is a reason to audit again
    after = await fp(db, subject)
    assert after != before
    await set_rules(db, "audit", {"window_days": 14})
    assert await fp(db, subject) != after
    by = {s.name: s for s in rt.graph.values()}
    assert ("dizquetv_audit", "same") in {(e.to, e.map) for e in by["dizquetv_channels"].edges}
    assert (by["dizquetv_audit"].pool, by["dizquetv_audit"].extension) == ("audits", "dizquetv")
