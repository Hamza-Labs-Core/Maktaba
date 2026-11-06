"""plex_collections_import (Epic 56 story 56.07): Plex's own collections read into Maktaba,
in Plex's order, never the ones Maktaba wrote, gone when Plex drops them, no token logged."""

from __future__ import annotations

import logging
from dataclasses import dataclass
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.loader import build_full_graph
from maktaba_pipeline.extensions.links import Link, put_link
from maktaba_pipeline.extensions.plex import PlexExtension
from maktaba_pipeline.extensions.plex.collection import LABEL
from maktaba_pipeline.integrations.plex import PlexClient
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_video
from tests.extensions.harness import FakePlex, error_of, run_stage, setup_runtime, summary_of

TOKEN = "PLEX-SECRET-TOKEN-1234"
SETTINGS = {"plex": {"url": "http://plex.test", "token": TOKEN, "path_map": "Y:/=/volume1/share/"}}


@dataclass
class World:
    library: UUID
    videos: list[UUID]
    fake: FakePlex
    ext: PlexExtension


async def make_world(db: Any) -> World:
    lib = await make_library(db, "Lib")
    fake = FakePlex()
    vids = []
    for i in range(1, 8):
        v = await make_video(db, lib, path=f"Y:/Show/e{i}.mp4", title=f"Episode {i}")
        fake.add_item(str(100 + i), f"Episode {i}", f"/volume1/share/Show/e{i}.mp4")
        vids.append(v)
    # one Plex item that is no Maktaba video
    fake.add_item("199", "Not here", "/volume1/share/Other/x.mp4")
    ext = PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))
    return World(lib, vids, fake, ext)


async def _collections(db: Database, lib: UUID) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT id, name, source_ref, auto_rule FROM collections"
            " WHERE library_id = $1 AND source_kind = 'plex' ORDER BY name",
            str(lib),
        )
    )


async def _items(db: Database, cid: Any) -> list[str]:
    rows = await db.fetch(
        "SELECT video_id FROM collection_items WHERE collection_id = $1 ORDER BY position", cid
    )
    return [str(r["video_id"]) for r in rows]


async def _index_then_import(db: Database, w: World) -> Any:
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    return await run_stage(db, rt, "plex_collections_import", "library", w.library, force=True)


def test_graph_reads_collections_after_each_index() -> None:
    fake = FakePlex()
    ext = PlexExtension(client_factory=lambda s: PlexClient(s["url"], s["token"], transport=fake))
    by = {s.name: s for s in build_full_graph([ext])}
    assert ("plex_collections_import", "same") in {(e.to, e.map) for e in by["plex_index"].edges}
    assert by["plex_collections_import"].placement == "home"


@pytest.mark.asyncio
async def test_a_plex_collection_is_imported_in_plex_order(db: Database) -> None:
    w = await make_world(db)
    # 8 children, 7 of them Maktaba videos, in a custom order
    rk = w.fake.add_collection(
        "Favourites", ["107", "101", "199", "103", "102", "104", "105", "106"]
    )
    row = await _index_then_import(db, w)
    assert row["state"] == "done", error_of(row)
    (col,) = await _collections(db, w.library)
    assert col["name"] == "Favourites" and col["source_ref"] == f"machine-1:{rk}"
    order = [w.videos[i] for i in (6, 0, 2, 1, 3, 4, 5)]
    assert await _items(db, col["id"]) == [str(v) for v in order]
    assert summary_of(row)["missing_videos"] == 1
    assert '"missing": 1' in str(col["auto_rule"]) or '"missing":1' in str(col["auto_rule"])


@pytest.mark.asyncio
async def test_a_collection_maktaba_wrote_is_not_imported(db: Database) -> None:
    w = await make_world(db)
    linked = w.fake.add_collection("Lib - Show", ["101", "102", "103"])
    labelled = w.fake.add_collection("Lib - ملف - x", ["104", "105"])
    w.fake.collections[labelled]["labels"] = [LABEL]
    await put_link(db, Link("plex_collection", "season", "s1", linked, "Lib - Show", "h", True))
    await _index_then_import(db, w)
    assert await _collections(db, w.library) == []


@pytest.mark.asyncio
async def test_members_follow_plex_and_a_deleted_collection_goes(db: Database) -> None:
    w = await make_world(db)
    rk = w.fake.add_collection("Smart", ["101", "102"])
    w.fake.collections[rk]["smart"] = True
    await _index_then_import(db, w)
    (col,) = await _collections(db, w.library)
    await db.execute("UPDATE collections SET name_override = 'Kept' WHERE id = $1", col["id"])
    w.fake.collections[rk]["children"] = ["103", "104", "105"]
    await _index_then_import(db, w)
    (after,) = await _collections(db, w.library)
    assert await _items(db, after["id"]) == [str(w.videos[i]) for i in (2, 3, 4)]
    over = await db.fetchrow("SELECT name_override FROM collections WHERE id = $1", after["id"])
    assert over["name_override"] == "Kept"
    del w.fake.collections[rk]
    await _index_then_import(db, w)
    assert await _collections(db, w.library) == []
    # one with fewer than 2 Maktaba videos is skipped
    w.fake.add_collection("Thin", ["101", "199"])
    await _index_then_import(db, w)
    assert await _collections(db, w.library) == []


@pytest.mark.asyncio
async def test_plex_down_parks_with_no_writes_and_no_token_anywhere(
    db: Database, caplog: pytest.LogCaptureFixture
) -> None:
    w = await make_world(db)
    w.fake.add_collection("Favourites", ["101", "102", "103"])
    rt = await setup_runtime(db, [w.ext], SETTINGS)
    await run_stage(db, rt, "plex_index", "library", w.library)
    w.fake.down = True
    caplog.set_level(logging.DEBUG)
    row = await run_stage(db, rt, "plex_collections_import", "library", w.library, force=True)
    assert row["state"] != "done"
    assert error_of(row).get("kind") == "plex_unreachable" or "plex_unreachable" in str(
        row["error"]
    )
    assert await _collections(db, w.library) == []
    assert TOKEN not in str(row["error"])
    assert TOKEN not in caplog.text
