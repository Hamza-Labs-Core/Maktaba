"""The TV-grid planner (planner v2) through the Maktaba plan JSON: the Quran channel has a
lane per reciter, and a filler clip is stored once with the pools indexing it."""

from __future__ import annotations

from dataclasses import replace
from typing import Any

import pytest

from maktaba_pipeline.engine.stages._plan_json import PLAN_FORMAT, build_plan_json, pool_items
from maktaba_pipeline.schedule import PLANNER_VERSION, plan_catalog
from maktaba_pipeline.schedule.channel_planner import (
    EpisodeRef,
    LooseVideo,
    PlannerConfig,
    SeasonCandidate,
)

#: a test catalog has hours, not the month a channel needs by default
SMALL = PlannerConfig(channel_min_days=1.0, channel_day_hours=10.0)

pytestmark = pytest.mark.unit


def show(sid: str, name: str, *, n: int = 30, minutes: float = 50, genre: str = "seerah",
         folder: str = "Y:/a", fmt: str = "program") -> SeasonCandidate:  # fmt: skip
    tags = {f"topic:{genre}": 1.0, f"genre:{genre}": 1.0, f"format:{fmt}": 0.9}
    return SeasonCandidate(
        series_id=sid, name=name, series_key=name, folder=folder, presenter="أحمد",
        first_air="2010-01-01", genre=genre, tags=tags, library=folder.rsplit("/", 1)[-1],
        episodes=tuple(
            EpisodeRef(path=f"{sid}/{i}", duration_s=minutes * 60, title=f"ح{i}", episode=i,
                       plex_rating_key=f"{sid}-{i}")
            for i in range(1, n + 1)
        ),
    )  # fmt: skip


def reciter(sid: str, name: str, folder: str, minutes: float = 15) -> SeasonCandidate:
    s = show(sid, name, n=114, minutes=minutes, genre="quran_recitation", folder=folder,
             fmt="recitation")  # fmt: skip
    return replace(s, tags={**s.tags, "format:program": 0.0}, presenter="Quranic Recitations")


def catalog() -> list[SeasonCandidate]:
    shows = [show(f"s{i}", f"برنامج {i}", genre=("seerah", "fiqh", "tafsir")[i % 3])
             for i in range(12)]  # fmt: skip
    quran = [
        reciter("h", "المصحف المرتل", "Y:/الحصري"),
        reciter("m", "المصحف المرتل", "Y:/المنشاوي"),
        reciter("bm", "المصحف المجود", "Y:/عبد الباسط - مجود", minutes=25),
    ]
    return shows + quran


def clips() -> list[LooseVideo]:
    return [
        LooseVideo(path=f"Y:/a/clip{i}.mp4", duration_s=120 + i, title=f"مقطع {i}", folder="Y:/a",
                   presenter="أحمد", tags={f"topic:{g}": 0.9}, genre=g, is_short=True)
        for i, g in enumerate(["seerah", "fiqh", "tafsir"] * 20)
    ]  # fmt: skip


@pytest.fixture(scope="module")
def plan() -> dict[str, Any]:
    """The catalog and its clips, planned and exported once: every test here only reads
    the plan (planning and simulating each channel's week was most of each test's time)."""
    result = plan_catalog(catalog(), clips(), SMALL)
    assert result.version == PLANNER_VERSION
    return build_plan_json(result, source="catalog", notes=[], exporters={})


def test_the_quran_channel_has_a_lane_per_reciter(plan: dict[str, Any]) -> None:
    quran = [c for c in plan["channels"] if c["kind"] == "quran"]
    assert len(quran) == 1
    lanes = [plan["lanes"][k] for k in quran[0]["lanes"]]
    libraries = [{u["series_id"] for u in ln["units"]} for ln in lanes]
    assert all(len(ids) == 1 for ids in libraries)  # one reciter per lane
    assert {next(iter(ids)) for ids in libraries} == {"h", "m", "bm"}


def test_filler_clips_are_stored_once_and_pools_index_them(plan: dict[str, Any]) -> None:
    """Plan format 3: a clip in many pools is one record in ``clips``; pools hold indexes
    (format 2 repeated every record per pool: 3.7 GB on the real catalog)."""
    assert plan["format"] == PLAN_FORMAT == 3
    pools = [p for p in plan["filler_pools"].values() if p["items"]]
    assert pools and all(isinstance(p["items"], str) for p in pools)  # hex indexes, one string
    paths = [c["path"] for c in plan["clips"]]
    assert len(paths) == len(set(paths))  # every clip once
    for p in pools:
        recs = pool_items(plan, p)
        assert len(recs) == p["count"] and all("duration_s" in r for r in recs)
    # a stored format-2 plan still reads: its pools carry the records themselves
    pool: dict[str, Any] = {"items": [{"path": "a"}]}
    old: dict[str, Any] = {"format": 2, "filler_pools": {"x": pool}}
    assert pool_items(old, pool) == [{"path": "a"}]
