"""The Plex extension's plex_groups: topic and clip collections, labelled, retired when gone."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.links import get_link
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.extensions.plex.collection import LABEL
from maktaba_pipeline.integrations.plex import PlexClient
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season, make_video
from tests.extensions.harness import (
    FakePlex,
    add_episode,
    error_of,
    run_stage,
    setup_runtime,
    summary_of,
)

SETTINGS = {"plex": {"url": "http://plex.test", "token": "T", "path_map": "Y:/=/volume1/share/"}}


@dataclass
class World:
    library: UUID
    season: UUID
    topic: UUID
    group: UUID
    clips: list[UUID]
    episodes: list[UUID]
    fake: FakePlex
    ext: PlexExtension


async def _group(db: Any, lib: UUID, name: str, title: str, vids: list[UUID]) -> UUID:
    gid = uuid4()
    await db.execute(
        "INSERT INTO clip_groups (id, library_id, kind, basis, name, topic, title,"
        " membership_hash) VALUES ($1,$2,'clips','playlist',$3,NULL,$4,'h')",
        str(gid),
        str(lib),
        name,
        title,
    )
    for i, v in enumerate(vids, 1):
        await db.execute(
            "INSERT INTO clip_group_items (group_id, video_id, position) VALUES ($1,$2,$3)",
            str(gid),
            str(v),
            i,
        )
    return gid


async def make_world(db: Any) -> World:
    lib = await make_library(db, "Lib")
    series, season = await make_series_and_season(db, lib, name="Show")
    fake = FakePlex()
    eps, clips = [], []
    for i in range(1, 5):
        v = await make_video(db, lib, path=f"Y:/Show/e{i}.mp4", title=f"Episode {i}")
        await add_episode(db, series, v, episode=i)
        fake.add_item(str(100 + i), f"Episode {i}", f"/volume1/share/Show/e{i}.mp4")
        eps.append(v)
    for i in range(1, 6):
        v = await make_video(db, lib, path=f"Y:/Show/c{i}.mp4", title=f"Clip {i}", duration=60.0)
        fake.add_item(str(200 + i), f"Clip {i}", f"/volume1/share/Show/c{i}.mp4")
        clips.append(v)
    tid = uuid4()
    await db.execute(
        "INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash)"
        " VALUES ($1,$2,'PLT','الشأن الفلسطيني','h')",
        str(tid),
        str(lib),
    )
    for i, v in enumerate(eps[:3], 1):
        await db.execute(
            "INSERT INTO topic_items (topic_id, video_id, position) VALUES ($1,$2,$3)",
            str(tid),
            str(v),
            i,
        )
    gid = await _group(db, lib, "لقطات", "Lib - مقاطع - لقطات", clips)
    ext = PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))
    return World(lib, season, tid, gid, clips, eps, fake, ext)


def test_graph_feeds_plex_groups_from_topics_clips_and_the_index() -> None:
    fake = FakePlex()
    ext = PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))
    by = {s.name: s for s in build_full_graph([ext])}
    pg = by["plex_groups"]
    assert pg.subject_kinds == ("library",) and pg.entry_for == ("library",)
    assert pg.external_write and pg.pool == "network_plex"
    for src in ("topic_collections", "clip_groups", "plex_index"):
        assert ("plex_groups", "same") in {(e.to, e.map) for e in by[src].edges}


@pytest.mark.asyncio
async def test_writes_topic_and_clip_collections_with_the_label(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    row = await run_stage(db, rt, "plex_groups", "library", w.library)
    assert row["state"] == "done", error_of(row)
    by_title = {c["title"]: c for c in w.fake.collections.values()}
    topic = by_title["Lib - ملف - الشأن الفلسطيني"]
    assert topic["children"] == ["101", "102", "103"] and topic["labels"] == [LABEL]
    clips = by_title["Lib - مقاطع - لقطات"]
    assert clips["children"] == ["201", "202", "203", "204", "205"]
    assert clips["labels"] == ["maktaba-auto"]
    link = await get_link(db, "plex_clip_group", "clip_group", str(w.group))
    assert link is not None and link.created_by_tool
    assert link.content_hash.startswith(str(w.library))
    # a season collection is labelled too
    srow = await run_stage(db, rt, "plex_collection", "season", w.season)
    assert srow["state"] == "done", error_of(srow)
    season = next(c for c in w.fake.collections.values() if c["title"] == "Lib - Show")
    assert season["labels"] == [LABEL]


@pytest.mark.asyncio
async def test_second_run_writes_nothing(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    await run_stage(db, rt, "plex_groups", "library", w.library)
    before = len(w.fake.writes)
    row = await run_stage(db, rt, "plex_groups", "library", w.library, force=True)
    assert row["state"] == "done" and len(w.fake.writes) == before
    assert summary_of(row)["status"] == {"noop": 2}


@pytest.mark.asyncio
async def test_a_foreign_collection_with_the_title_is_left_alone(db: Database) -> None:
    w = await make_world(db)
    mine = w.fake.add_collection("Lib - مقاطع - لقطات", ["201"])
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    row = await run_stage(db, rt, "plex_groups", "library", w.library)
    assert row["state"] == "done"
    assert w.fake.collections[mine]["children"] == ["201"]  # untouched
    assert summary_of(row)["status"].get("conflict") == 1


@pytest.mark.asyncio
async def test_a_renamed_group_retires_its_old_collection(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    await run_stage(db, rt, "plex_groups", "library", w.library)
    old_key = (await get_link(db, "plex_clip_group", "clip_group", str(w.group))).external_id  # type: ignore[union-attr]
    # the group step renamed the group: new id, same clips
    await db.execute("DELETE FROM clip_groups WHERE id = $1", str(w.group))
    new = await _group(db, w.library, "لقطات مختارة", "Lib - مقاطع - لقطات مختارة", w.clips)
    row = await run_stage(db, rt, "plex_groups", "library", w.library, force=True)
    s = summary_of(row)
    assert s["retired"] == ["Lib - مقاطع - لقطات"]
    assert old_key not in w.fake.collections
    assert await get_link(db, "plex_clip_group", "clip_group", str(w.group)) is None
    assert await get_link(db, "plex_clip_group", "clip_group", str(new)) is not None


@pytest.mark.asyncio
async def test_a_gone_group_whose_items_went_nowhere_is_kept(db: Database) -> None:
    w = await make_world(db)
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    await run_stage(db, rt, "plex_groups", "library", w.library)
    await db.execute("DELETE FROM clip_groups WHERE id = $1", str(w.group))
    row = await run_stage(db, rt, "plex_groups", "library", w.library, force=True)
    s = summary_of(row)
    assert s["retired"] == [] and s["kept_old"] == ["Lib - مقاطع - لقطات"]
    assert any(c["title"] == "Lib - مقاطع - لقطات" for c in w.fake.collections.values())


@pytest.mark.asyncio
async def test_an_item_plex_refuses_stays_out_and_the_stage_goes_on(db: Database) -> None:
    w = await make_world(db)
    w.fake.refuse = {"203"}
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    row = await run_stage(db, rt, "plex_groups", "library", w.library)
    assert row["state"] == "done", error_of(row)
    by_title = {c["title"]: c for c in w.fake.collections.values()}
    assert by_title["Lib - مقاطع - لقطات"]["children"] == ["201", "202", "204", "205"]
    assert "Lib - ملف - الشأن الفلسطيني" in by_title  # the other collections are written
    assert summary_of(row)["status"].get("items_refused") == 1


@pytest.mark.asyncio
async def test_a_group_over_two_plex_sections_is_a_collection_in_each(db: Database) -> None:
    w = await make_world(db)
    w.fake.sections["2"] = "TV 2"
    for rk in ("204", "205"):  # two clips live in a second Plex library of the same name
        w.fake.items[rk]["section"] = "2"
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    row = await run_stage(db, rt, "plex_groups", "library", w.library)
    assert row["state"] == "done", error_of(row)
    clips = {c["section"]: c["children"] for c in w.fake.collections.values()
             if c["title"] == "Lib - مقاطع - لقطات"}  # fmt: skip
    assert clips == {"1": ["201", "202", "203"], "2": ["204", "205"]}
    again = await run_stage(db, rt, "plex_groups", "library", w.library)
    assert again["state"] == "done", error_of(again)
    assert sum(c["title"] == "Lib - مقاطع - لقطات" for c in w.fake.collections.values()) == 2


def test_a_collection_in_order_needs_no_moves() -> None:
    fake = FakePlex()
    client = PlexClient("http://plex.test", "T", transport=fake)
    keys = [str(300 + i) for i in range(40)]
    new, refused = client.create_collection_report("m", "1", "x", keys)
    assert fake.collections[new]["children"] == keys and not refused
    assert not any(path.endswith("/move") for _, path in fake.calls)
