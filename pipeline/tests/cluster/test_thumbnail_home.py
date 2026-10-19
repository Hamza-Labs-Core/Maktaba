"""Posters a node recorded in its own cache: the stage is home-only, the home clears
and remakes them (thumbnail/repair.py)."""

from __future__ import annotations

from pathlib import Path
from typing import Any

import pytest

from maktaba_pipeline.cluster.stages import select_stages
from maktaba_pipeline.engine.graph import GRAPH, get_stage
from maktaba_pipeline.engine.sync import sync_graph
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.thumbnail.repair import clear_foreign_thumbnails, is_foreign
from tests.engine.factory import make_library, make_video

NODE_POSTER = r"C:\Users\MEYWD\.maktaba\cache\thumbnails\ab12\poster.jpg"


@pytest.mark.unit
def test_posters_and_review_artefacts_stay_on_the_home() -> None:
    for name in ("thumbnail", "thumb_small", "chapter_thumbs"):
        st = get_stage(name)
        assert st is not None and st.placement == "home", name
    served, _ = select_stages(GRAPH, handlers={"thumbnail"}, role="node")
    assert "thumbnail" not in served


@pytest.mark.unit
def test_which_paths_are_foreign() -> None:
    cache = "/users/home/.maktaba/cache/thumbnails"
    assert is_foreign(NODE_POSTER, cache, "Darwin")  # another OS's spelling
    assert is_foreign("/home/other/.maktaba/cache/thumbnails/ab/poster.jpg", cache, "Darwin")
    assert not is_foreign("/Users/home/.maktaba/cache/thumbnails/ab/poster.jpg", cache, "Darwin")
    assert not is_foreign("/Volumes/Originals/posters/a.jpg", cache, "Darwin")  # not a cache
    assert not is_foreign("smb://home-nas/Originals/a.jpg", cache, "Darwin")


async def _paths(db: Database, vid: Any) -> tuple[Any, Any]:
    row = await db.fetchrow("SELECT poster_path, sprite_path FROM videos WHERE id = $1", str(vid))
    return row["poster_path"], row["sprite_path"]


@pytest.mark.asyncio
async def test_node_posters_are_cleared_and_made_again(db: Database, tmp_path: Path) -> None:
    await sync_graph(db, GRAPH)
    lib = await make_library(db)
    node, home, none = (
        await make_video(db, lib),
        await make_video(db, lib),
        await make_video(db, lib),
    )
    cache = tmp_path / "thumbnails"
    mine = (cache / "cd34" / "poster.jpg").as_posix()
    upd = "UPDATE videos SET poster_path = $2, sprite_path = $3 WHERE id = $1"
    await db.execute(upd, str(node), NODE_POSTER, NODE_POSTER.replace("poster", "sprite"))
    await db.execute(upd, str(home), mine, mine)

    dry = await clear_foreign_thumbnails(db, cache_root=cache, system="Darwin", apply=False)
    assert dry.cleared == 1 and await _paths(db, node) != (None, None)

    done = await clear_foreign_thumbnails(db, cache_root=cache, system="Darwin")
    assert done.cleared == 1 and done.enqueued == 1 and done.samples == [NODE_POSTER]
    assert await _paths(db, node) == (None, None)
    assert await _paths(db, home) == (mine, mine)
    assert await _paths(db, none) == (None, None)
    job = await db.fetchrow(
        "SELECT stage FROM processing_jobs WHERE subject_id = $1 AND stage = 'thumbnail'",
        str(node),
    )
    assert job is not None

    again = await clear_foreign_thumbnails(db, cache_root=cache, system="Darwin")
    assert again.cleared == 0  # idempotent
