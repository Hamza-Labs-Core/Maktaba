"""topic_collections, promo_links and clip_groups: what detection leaves over, as queues."""

from __future__ import annotations

from collections.abc import Iterator
from datetime import date, timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages.plan_channels import load_inputs
from maktaba_pipeline.runtime import Database
from tests.engine.factory import make_library
from tests.stages.world import (
    add_channel,
    add_youtube_video,
    core_runtime,
    drain_core,
    make_fake_media,
    set_playlist,
)

DAY = timedelta(days=1)


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def _video(
    db: Database, lib: UUID, title: str, n: int, dur: float, day: date
) -> tuple[UUID, str]:
    return await add_youtube_video(
        db, lib, title=title, filename=f"v{n:03d}.mp4", upload=day, duration=dur
    )


async def make_catalog(db: Database) -> dict[str, Any]:
    """Two shows, a playlist cutting across both, clips, questions, a promo, talks."""
    lib = await make_library(db, "قناة الاختبار")
    await add_channel(db, lib)
    n = 0
    shows: dict[str, list[tuple[UUID, str]]] = {}
    for name, pid, start in (("كنوز السيرة", "PLA", date(2021, 1, 1)),
                             ("قصص الأنبياء", "PLB", date(2022, 1, 1))):  # fmt: skip
        eps = []
        for i in range(8):
            n += 1
            eps.append(await _video(db, lib, f"{name} {i + 1}", n, 1500.0 + i, start + i * 7 * DAY))
        await set_playlist(db, pid, name, eps)
        shows[name] = eps
    a, b = shows["كنوز السيرة"], shows["قصص الأنبياء"]
    await set_playlist(db, "PLT", "الشأن الفلسطيني", a[:3] + b[:3])
    clips = []
    for i, word in enumerate(("الصبر", "الشكر", "التوبة", "الرضا", "الخوف", "الرجاء")):
        n += 1
        clips.append(await _video(db, lib, f"مقطع عن {word}", n, 70.0, date(2023, 1, 1) + i * DAY))
    await set_playlist(db, "PLC", "لقطات قصيرة", clips)
    qa = []
    for i, q in enumerate(("ما حكم صلاة الوتر", "هل يجوز الجمع بين الصلاتين", "ما حكم الزكاة")):
        n += 1
        qa.append(await _video(db, lib, q, n, 120.0, date(2023, 6, 1) + i * DAY))
    n += 1
    promo = await _video(db, lib, "برومو كنوز السيرة", n, 45.0, date(2020, 12, 25))
    talks = []
    for i, t in enumerate(("لقاء مع ضيف عن الإعلام", "محاضرة عن تربية الأبناء")):
        n += 1
        talks.append(await _video(db, lib, t, n, 3000.0, date(2023, 9, 1) + i * 30 * DAY))
    return {"lib": lib, "a": a, "b": b, "clips": clips, "qa": qa, "promo": promo, "talks": talks}


async def _jobs(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


async def _run_all(db: Database, lib: UUID) -> None:
    rt = await core_runtime(db)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await drain_core(db, rt, limit=800)


@pytest.mark.unit
def test_graph_order_detect_to_topics_promos_clips_to_plan() -> None:
    by = {s.name: s for s in GRAPH}
    edges = {n: {(e.to, e.map) for e in by[n].edges} for n in by}
    assert {("topic_collections", "same"), ("promo_links", "same")} <= edges["detect"]
    assert ("clip_groups", "same") in edges["topic_collections"]
    assert ("clip_groups", "same") in edges["promo_links"]
    assert ("clip_groups", "library_of") in edges["tag"]
    for n in ("topic_collections", "promo_links", "clip_groups"):
        assert ("plan_channels", "catalog") in edges[n]
        assert by[n].subject_kinds == ("library",) and by[n].entry_for == ("library",)


@pytest.mark.asyncio
async def test_detection_flows_into_topics_promos_and_clip_groups(db: Database) -> None:
    w = await make_catalog(db)
    await _run_all(db, w["lib"])
    for stage in ("topic_collections", "promo_links", "clip_groups"):
        jobs = await _jobs(db, stage)
        assert jobs and jobs[-1]["state"] == "done", (stage, [j["error"] for j in jobs])

    # the playlist cutting across both shows is a topic collection; episodes keep their season
    topics = await db.fetch("SELECT id, name, playlist_id FROM topic_collections")
    assert [(t["name"], t["playlist_id"]) for t in topics] == [("الشأن الفلسطيني", "PLT")]
    items = await db.fetch(
        "SELECT video_id FROM topic_items WHERE topic_id = $1 ORDER BY position", topics[0]["id"]
    )
    expect = [str(v) for v, _ in w["a"][:3] + w["b"][:3]]
    assert [str(r["video_id"]) for r in items] == expect
    placed = await db.fetchval(
        "SELECT count(*) FROM series_episodes WHERE CAST(video_id AS TEXT) = $1", expect[0]
    )
    assert placed == 1

    # the promo names its show
    links = await db.fetch("SELECT * FROM promo_links")
    assert [str(r["video_id"]) for r in links] == [str(w["promo"][0])]
    series = await db.fetchval(
        "SELECT s.name FROM series s WHERE s.id = $1", str(links[0]["series_id"])
    )
    assert series == "كنوز السيرة"
    assert links[0]["season"] == 1 and links[0]["season_id"] is not None

    # leftovers: one group each; questions together; promos their own group
    groups = {
        r["name"]: r
        for r in await db.fetch("SELECT id, kind, basis, name, title, topic FROM clip_groups")
    }
    assert groups["فتاوى ومسائل"]["topic"] == "qa_fatwa"
    assert groups["إعلانات"]["basis"] == "promo"
    assert groups["إعلانات"]["title"] == "قناة الاختبار - مقاطع - إعلانات"
    members = {
        str(r["video_id"]): str(r["group_id"])
        for r in await db.fetch("SELECT group_id, video_id FROM clip_group_items")
    }
    for v, _ in w["qa"]:
        assert members[str(v)] == str(groups["فتاوى ومسائل"]["id"])
    for v, _ in w["clips"]:
        assert str(v) in members  # every clip is in exactly one group (unique index)
    for v, _ in w["talks"]:
        assert groups_of(members, groups, v)["kind"] == "standalone"
    for v, _ in w["a"] + w["b"]:
        assert str(v) not in members  # an episode is never a leftover


def groups_of(members: dict[str, str], groups: dict[str, Any], vid: UUID) -> Any:
    gid = members[str(vid)]
    return next(g for g in groups.values() if str(g["id"]) == gid)


@pytest.mark.asyncio
async def test_unchanged_library_skips_and_planner_sees_groups_and_promos(db: Database) -> None:
    w = await make_catalog(db)
    await _run_all(db, w["lib"])
    rt = await core_runtime(db)
    for stage in ("topic_collections", "promo_links", "clip_groups"):
        await enqueue_job(db, subject_kind="library", subject_id=w["lib"], stage=stage)
    await drain_core(db, rt, only={"topic_collections", "promo_links", "clip_groups"})
    for stage in ("topic_collections", "promo_links", "clip_groups"):
        assert (await _jobs(db, stage))[-1]["outcome"] == "skipped_unchanged", stage
    seasons, loose, _notes = await load_inputs(db)
    by = {v.path: v for v in loose}
    show = next(s for s in seasons if s.name == "كنوز السيرة")
    # the planner's own show key ("<folder>|<series key>"), so the lane pads with it
    assert by[str(w["promo"][0])].promo_of == f"{show.folder}|{show.series_key}"
    assert by[str(w["qa"][0][0])].group == "قناة الاختبار - مقاطع - فتاوى ومسائل"


@pytest.mark.asyncio
async def test_a_retrigger_of_the_library_reaches_every_new_queue(db: Database) -> None:
    from maktaba_pipeline.engine.graph import get_stage

    entry = [s.name for s in GRAPH if "library" in s.entry_for]
    for n in ("topic_collections", "promo_links", "clip_groups"):
        assert n in entry and get_stage(n) is not None
    w = await make_catalog(db)
    rt = await core_runtime(db)
    for n in ("topic_collections", "promo_links", "clip_groups"):
        await enqueue_job(db, subject_kind="library", subject_id=w["lib"], stage=n)
    await drain_core(db, rt, only={"topic_collections", "promo_links", "clip_groups"})
    # nothing detected yet: no topics or promos, every video a leftover
    assert await db.fetchval("SELECT count(*) FROM topic_collections") == 0
    summary = loads((await _jobs(db, "clip_groups"))[-1]["output_summary"])
    assert summary["clips"] + summary["standalone"] > 0


@pytest.mark.asyncio
async def test_named_seasons_are_stored_and_titled(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    from maktaba_pipeline.classify.series_detector import Detection, Season, Series
    from maktaba_pipeline.engine.stages import detect as detect_stage
    from maktaba_pipeline.extensions.seasons import collection_title, load_season

    lib = await make_library(db, "أحمد منصور")
    await add_channel(db, lib)
    start = date(2005, 1, 1)
    vids = [
        (await _video(db, lib, f"شاهد على العصر | ضيف {i}", i, 2900.0, start + i * DAY))[0]
        for i in range(6)
    ]

    def fake_detect(videos: Any, playlists: Any, **kw: Any) -> Detection:
        keys = [str(v) for v in vids]
        return Detection(
            series=[
                Series(
                    "شاهد على العصر",
                    "shahid",
                    [
                        Season(1, keys[:3], name="وليد جنبلاط", year=2005),
                        Season(2, keys[3:], name="عبدالناصر عيسى", year=2006),
                    ],
                    "episodic",
                    brand=True,
                )
            ],
            unassigned=[],
        )

    monkeypatch.setattr(detect_stage, "detect", fake_detect)
    rt = await core_runtime(db)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await drain_core(db, rt, only={"detect"})
    rows = await db.fetch("SELECT id, season, metadata FROM series_seasons ORDER BY season")
    meta = [loads(r["metadata"]) for r in rows]
    assert [(m["season_name"], m["season_year"], m["brand"]) for m in meta] == [
        ("وليد جنبلاط", 2005, True),
        ("عبدالناصر عيسى", 2006, True),
    ]
    info = await load_season(db, rows[0]["id"])
    assert info is not None and info.title == "أحمد منصور - شاهد على العصر - وليد جنبلاط"
    # a one-season series keeps the plain title; a numbered one without a name "الموسم N"
    assert collection_title("L", "S", 1, 1, "x") == "L - S"
    assert collection_title("L", "S", 2, 3) == "L - S - الموسم 2"
