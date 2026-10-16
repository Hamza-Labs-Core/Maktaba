"""The Plex extension's ``plex_index`` queue: one listing per library, saved once."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.dizquetv import DizqueTVExtension
from maktaba_pipeline.extensions.links import get_links
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.extensions.plex.index import sections_for
from maktaba_pipeline.extensions.plex_items import details_for, items_for_videos
from maktaba_pipeline.integrations.dizquetv import DizqueTV
from maktaba_pipeline.integrations.plex import PlexClient, PlexSection
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import (
    FakeDizque,
    FakePlex,
    add_episode,
    error_of,
    run_stage,
    setup_runtime,
    summary_of,
)

TOKEN = "SECRET-PLEX-TOKEN"
SETTINGS = {
    "plex": {"url": "http://plex.test", "token": TOKEN, "path_map": "Y:/=/volume1/share/"},
    "dizquetv": {"url": "http://dz.test"},
}


@dataclass
class World:
    library: UUID
    season: UUID
    videos: list[UUID]
    plex: FakePlex
    dz: FakeDizque
    extensions: list[Any]


def make_extensions(plex: FakePlex, dz: FakeDizque) -> list[Any]:
    return [
        PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=plex)),
        DizqueTVExtension(
            dz_factory=lambda s: DizqueTV(s["url"], transport=dz),
            plex_factory=lambda s: PlexClient(s["url"], s["token"], transport=plex),
            tz_offset=lambda: 0,
        ),
    ]


async def make_world(db: Any, n: int = 3) -> World:
    lib = await make_library(db, "Lib")
    series, season = await make_series_and_season(db, lib, name="Show")
    plex, dz = FakePlex(), FakeDizque()
    videos: list[UUID] = []
    for i in range(1, n + 1):
        # no length yet: Plex's measured one is attached by the index
        v = await make_video(db, lib, path=f"Y:/Show/e{i}.mp4", title=f"Episode {i}", duration=None)
        await add_episode(db, series, v, episode=i)
        videos.append(v)
        plex.add_item(str(100 + i), f"Episode {i}", f"/volume1/share/Show/e{i}.mp4")
    plex.add_item("999", "Not ours", "/volume1/other/x.mp4")  # outside the library
    return World(lib, season, videos, plex, dz, make_extensions(plex, dz))


def listing_calls(fake: FakePlex) -> int:
    return sum(1 for m, p in fake.calls if m == "GET" and p.endswith("/all"))


def metadata_calls(fake: FakePlex) -> int:
    return sum(1 for m, p in fake.calls if m == "GET" and p.startswith("/library/metadata/"))


# ------------------------------------------------------------------ graph


def test_graph_wires_the_index_into_collections_and_shows() -> None:
    plex, dz = FakePlex(), FakeDizque()
    graph = {s.name: s for s in build_full_graph(make_extensions(plex, dz))}
    idx = graph["plex_index"]
    assert idx.subject_kinds == ("library",) and idx.entry_for == ("library",)
    assert idx.pool == "network_plex" and idx.extension == "plex"
    targets = {(e.to, e.map) for e in idx.edges}
    assert ("plex_collection", "seasons_changed") in targets
    assert ("dizquetv_show", "seasons_changed") in targets
    assert ("plex_index", "same") in {(e.to, e.map) for e in graph["scan"].edges}


def test_sections_for_picks_the_sections_holding_the_library() -> None:
    a = PlexSection("1", "A", "movie", ("/volume1/share/Show",))
    b = PlexSection("2", "B", "movie", ("/volume1/other",))
    assert sections_for([a, b], ["/volume1/share/Show/e1.mp4"]) == [a]
    # nothing matches (locations outside the map): every section, never none
    assert sections_for([a, b], ["/elsewhere/x.mp4"]) == [a, b]


# ------------------------------------------------------------------ stage


@pytest.mark.asyncio
async def test_index_saves_items_links_videos_and_attaches_lengths(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    row = await run_stage(db, rt, "plex_index", "library", w.library)
    assert row["state"] == "done", error_of(row)
    s = summary_of(row)
    assert s["items"] == 4 and s["matched"] == 3 and s["linked"] == 3 and s["lengths"] == 3
    assert s["seasons_changed"] == 1
    links = await get_links(db, "plex_item", "video", [str(v) for v in w.videos])
    assert [links[str(v)].external_id for v in w.videos] == ["101", "102", "103"]
    saved = await details_for(db, ["101", "999"])
    assert saved["101"]["title"] == "Episode 1" and "999" in saved  # foreign items are saved too
    assert saved["101"]["Media"][0]["Part"][0]["file"] == "/volume1/share/Show/e1.mp4"
    by_video = await items_for_videos(db, [str(w.videos[0])])
    assert by_video[str(w.videos[0])].rating_key == "101"
    dur = await db.fetchval("SELECT duration_sec FROM videos WHERE id = $1", str(w.videos[0]))
    assert dur == pytest.approx(1500.0)
    # the season was handed to both downstream queues
    follow = await db.fetch(
        "SELECT stage FROM processing_jobs WHERE subject_id = $1 AND stage <> 'plex_index'",
        str(w.season),
    )
    assert {r["stage"] for r in follow} >= {"plex_collection", "dizquetv_show"}


@pytest.mark.asyncio
async def test_second_index_with_nothing_new_changes_no_season(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    first = await run_stage(db, rt, "plex_index", "library", w.library)
    await db.execute("DELETE FROM processing_jobs WHERE stage <> 'plex_index'")
    second = await run_stage(db, rt, "plex_index", "library", w.library, force=True)
    assert second["output_hash"] == first["output_hash"]
    s = summary_of(second)
    assert s["linked"] == 0 and s["unlinked"] == 0 and s["seasons_changed"] == 0


@pytest.mark.asyncio
async def test_items_plex_dropped_are_removed_and_their_season_rerun(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    del w.plex.items["103"]
    await db.execute("DELETE FROM processing_jobs WHERE stage <> 'plex_index'")
    row = await run_stage(db, rt, "plex_index", "library", w.library, force=True)
    s = summary_of(row)
    assert s["unlinked"] == 1 and s["seasons_changed"] == 1
    assert "103" not in await details_for(db, ["103"])
    links = await get_links(db, "plex_item", "video", [str(w.videos[2])])
    assert links == {}


@pytest.mark.asyncio
async def test_collection_after_the_index_does_not_list_plex_again(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    # forget the links: the collection must resolve members from plex_items, not a listing
    await db.execute("DELETE FROM external_links WHERE target = 'plex_item'")
    before = listing_calls(w.plex)
    row = await run_stage(db, rt, "plex_collection", "season", w.season, force=True)
    assert row["state"] == "done", error_of(row)
    assert listing_calls(w.plex) == before
    assert sorted(c["children"] for c in w.plex.collections.values()) == [["101", "102", "103"]]


@pytest.mark.asyncio
async def test_dizquetv_show_is_built_from_saved_records(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    before = metadata_calls(w.plex)
    row = await run_stage(db, rt, "dizquetv_show", "season", w.season, force=True)
    assert row["state"] == "done", error_of(row)
    assert metadata_calls(w.plex) == before  # every programme came from plex_items
    (show,) = w.dz.shows.values()
    assert [p["ratingKey"] for p in show["content"]] == ["101", "102", "103"]


@pytest.mark.asyncio
async def test_plex_down_is_retryable_and_classified_unreachable(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, w.extensions, SETTINGS)
    w.plex.down = True
    row = await run_stage(db, rt, "plex_index", "library", w.library)
    assert row["state"] == "pending" and row["attempts"] == 1
    assert row["error_cause"] == "plex_unreachable"
    assert TOKEN not in str(row["error"])
