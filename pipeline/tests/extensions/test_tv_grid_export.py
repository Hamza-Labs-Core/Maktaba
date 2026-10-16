"""The TV-grid planner (planner v2) through the Maktaba plan JSON into dizqueTV objects:
each slot queue is one "maktaba-plan · <slot>" show with its shows back to back, the Quran
channel has a lane per reciter, and a show the user made is still never overwritten."""

from __future__ import annotations

from dataclasses import replace
from typing import Any

import pytest

from maktaba_pipeline.engine.stages._plan_json import build_plan_json
from maktaba_pipeline.extensions.dizquetv import extension as dz_extension
from maktaba_pipeline.extensions.dizquetv.channels import plan_actions
from maktaba_pipeline.integrations.dizquetv import PREFIX, State
from maktaba_pipeline.schedule import PLANNER_VERSION, plan_catalog
from maktaba_pipeline.schedule.channel_planner import EpisodeRef, SeasonCandidate

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


def plan_json() -> dict[str, Any]:
    result = plan_catalog(catalog())
    assert result.version == PLANNER_VERSION == 2
    return build_plan_json(
        result, source="catalog", notes=[], exporters=dz_extension.plan_exporters()
    )


def test_every_slot_queue_is_one_ordered_show() -> None:
    plan = plan_json()
    actions = plan_actions(plan, State(server="PlexServer"))
    shows = [a for a in actions if a.kind == "show"]
    assert shows and all(a.name.startswith(PREFIX) for a in shows)
    used = {
        s["showId"][5:]
        for c in plan["channels"]
        if (c.get("exports") or {}).get("dizquetv")
        for s in c["exports"]["dizquetv"]["schedule"]["slots"]
        if s["showId"].startswith("lane:")
    }
    assert {a.ref for a in shows} == used  # one show per queue a slot plays, no more
    for a in shows:
        ln = plan["lanes"][a.ref]
        assert a.name == f"{PREFIX}{ln['name']} [{a.ref}]"
        expect = [str(e["plex_rating_key"]) for u in ln["units"] for e in u["episodes"]]
        assert [p["ratingKey"] for p in a.body["content"]] == expect  # shows back to back
        assert ln["pattern"] or ln["special"]  # a slot queue, or the dawn Quran / khutbah lane


def test_the_quran_channel_has_a_lane_per_reciter() -> None:
    plan = plan_json()
    quran = [c for c in plan["channels"] if c["kind"] == "quran"]
    assert len(quran) == 1
    lanes = [plan["lanes"][k] for k in quran[0]["lanes"]]
    libraries = [{u["series_id"] for u in ln["units"]} for ln in lanes]
    assert all(len(ids) == 1 for ids in libraries)  # one reciter per lane
    assert {next(iter(ids)) for ids in libraries} == {"h", "m", "bm"}
    shows = {a.ref for a in plan_actions(plan, State(server="PlexServer")) if a.kind == "show"}
    assert set(quran[0]["lanes"]) <= shows


def test_a_show_the_user_made_is_still_never_overwritten() -> None:
    plan = plan_json()
    first = next(a for a in plan_actions(plan, State(server="PlexServer")) if a.kind == "show")
    state = State(server="PlexServer", shows={first.name: {"id": "u1", "keys": None}})
    again = next(a for a in plan_actions(plan, state) if a.kind == "show" and a.ref == first.ref)
    assert again.verb == "conflict" and again.body is None
