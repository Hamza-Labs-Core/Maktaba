"""youtube_collections (Epic 56 story 56.06): a collection from each YouTube playlist and
channel a library's videos came from, kept in step, an admin's choices kept."""

from __future__ import annotations

from datetime import UTC, date, datetime, timedelta
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.sql import is_pg, to_ts
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library, make_series_and_season
from tests.extensions.harness import add_episode, run_stage, setup_runtime, summary_of
from tests.stages.world import add_channel, add_youtube_video, set_playlist

DAY = timedelta(days=1)


async def _videos(
    db: Database, lib: UUID, n: int, prefix: str, channel: str = "UCtest"
) -> list[tuple[UUID, str]]:
    out = []
    for i in range(n):
        out.append(
            await add_youtube_video(
                db,
                lib,
                title=f"{prefix} {i + 1}",
                filename=f"{prefix}_{channel}_{i + 1:02d}.mp4",
                upload=date(2024, 1, 1) + i * DAY,
                duration=600.0,
                channel_id=channel,
            )
        )
    return out


async def _collections(db: Database, lib: UUID) -> list[Any]:
    return list(
        await db.fetch(
            "SELECT id, name, source_kind, source_ref, name_override, hidden_at FROM collections"
            " WHERE library_id = $1 AND source_kind IS NOT NULL ORDER BY source_kind, source_ref",
            str(lib),
        )
    )


async def _items(db: Database, cid: Any) -> list[str]:
    rows = await db.fetch(
        "SELECT video_id FROM collection_items WHERE collection_id = $1 ORDER BY position", cid
    )
    return [str(r["video_id"]) for r in rows]


async def _run(db: Database, lib: UUID) -> Any:
    rt = await setup_runtime(db, [])
    return await run_stage(db, rt, "youtube_collections", "library", lib, force=True)


@pytest.mark.unit
def test_graph_runs_it_after_topic_collections() -> None:
    by = {s.name: s for s in GRAPH}
    assert ("youtube_collections", "same") in {(e.to, e.map) for e in by["topic_collections"].edges}
    st = by["youtube_collections"]
    assert st.subject_kinds == ("library",) and st.entry_for == ("library",)


@pytest.mark.asyncio
async def test_a_playlist_that_is_neither_a_season_nor_a_topic_is_a_collection(
    db: Database,
) -> None:
    lib = await make_library(db, "Lib")
    await add_channel(db, lib)
    lessons = await _videos(db, lib, 12, "lesson")
    show = await _videos(db, lib, 4, "show")
    talks = await _videos(db, lib, 3, "talk")
    # PLX: 12 lessons in reverse order; PLS: exactly a season; PLT: a topic's playlist
    await set_playlist(db, "PLX", "دروس الفقه", list(reversed(lessons)))
    await set_playlist(db, "PLS", "The show", show)
    await set_playlist(db, "PLT", "Talks", talks)
    series, _season = await make_series_and_season(db, lib, name="The show")
    for i, (v, _) in enumerate(show, 1):
        await add_episode(db, series, v, episode=i)
    await db.execute(
        "INSERT INTO topic_collections (id, library_id, playlist_id, name, membership_hash)"
        " VALUES ($1,$2,'PLT','Talks','h')",
        str(uuid4()),
        str(lib),
    )
    row = await _run(db, lib)
    assert row["state"] == "done", row["error"]
    cols = await _collections(db, lib)
    assert [(c["name"], c["source_kind"], c["source_ref"]) for c in cols] == [
        ("دروس الفقه", "youtube_playlist", "PLX")
    ]
    assert await _items(db, cols[0]["id"]) == [str(v) for v, _ in reversed(lessons)]
    # T04: a second run writes nothing
    again = summary_of(await _run(db, lib))
    assert (again["created"], again["updated"], again["deleted"]) == (0, 0, 0)


@pytest.mark.asyncio
async def test_channels_make_collections_only_in_a_library_of_several(db: Database) -> None:
    lib = await make_library(db, "Many")
    for ch in ("UCa", "UCb", "UCc"):
        await add_channel(db, lib, ch, f"Channel {ch}")
        await _videos(db, lib, 3, "v", channel=ch)
    await _run(db, lib)
    cols = await _collections(db, lib)
    assert [(c["source_kind"], c["source_ref"], c["name"]) for c in cols] == [
        ("youtube_channel", "UCa", "Channel UCa"),
        ("youtube_channel", "UCb", "Channel UCb"),
        ("youtube_channel", "UCc", "Channel UCc"),
    ]
    # newest published first
    items = await _items(db, cols[0]["id"])
    titles = [
        str((await db.fetchrow("SELECT filename FROM videos WHERE id = $1", v))["filename"])
        for v in items
    ]
    assert titles == ["v_UCa_03.mp4", "v_UCa_02.mp4", "v_UCa_01.mp4"]

    one = await make_library(db, "One")
    await add_channel(db, one, "UCone")
    await _videos(db, one, 5, "w", channel="UCone")
    await _run(db, one)
    assert await _collections(db, one) == []


@pytest.mark.asyncio
async def test_an_admins_choices_outlive_a_membership_change(db: Database) -> None:
    lib = await make_library(db, "Lib")
    await add_channel(db, lib)
    vids = await _videos(db, lib, 5, "lesson")
    await set_playlist(db, "PLX", "Lessons", vids[:4])
    await _run(db, lib)
    (col,) = await _collections(db, lib)
    await db.execute(
        "UPDATE collections SET name_override = 'Mine', hidden_at = $2 WHERE id = $1",
        col["id"],
        to_ts(db, datetime(2026, 11, 1, tzinfo=UTC)),
    )
    await set_playlist(db, "PLX", "Lessons", vids)
    row = await _run(db, lib)
    assert summary_of(row)["updated"] == 1
    (after,) = await _collections(db, lib)
    assert after["id"] == col["id"] and after["name_override"] == "Mine"
    assert after["hidden_at"] is not None
    assert len(await _items(db, col["id"])) == 5
    # T05: below the threshold, the collection goes
    await set_playlist(db, "PLX", "Lessons", vids[:2])
    await _run(db, lib)
    assert await _collections(db, lib) == []


@pytest.mark.asyncio
async def test_switched_off_it_makes_no_job(db: Database) -> None:
    lib = await make_library(db, "Lib")
    await setup_runtime(db, [])
    await db.execute(
        "UPDATE job_stages SET enabled = $1, disabled_reason = 'component_off'"
        " WHERE stage = 'youtube_collections'",
        False if is_pg(db) else 0,
    )
    res = await enqueue_job(db, subject_kind="library", subject_id=lib, stage="youtube_collections")
    assert res.job_id is None
    assert await _collections(db, lib) == []
