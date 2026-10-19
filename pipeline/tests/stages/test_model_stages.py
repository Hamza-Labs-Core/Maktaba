"""check_tags, check_clips and books: the model's and the book's corrections, as queues.

The model server and the encyclopedia are stood in for (``tag_check.ask`` /
``books.http_get``): no test reaches them.
"""

from __future__ import annotations

import json
import re
import urllib.error
from collections.abc import Iterator
from datetime import date, timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.classify import books, tag_check
from maktaba_pipeline.classify.series_detector import DetectorConfig
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, bound_graph, validate
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.stages.plan_channels import load_inputs
from maktaba_pipeline.engine.stages.tag import _episode_tags, library_tags
from maktaba_pipeline.runtime import Database
from tests.stages.world import (
    add_youtube_video,
    core_runtime,
    drain_core,
    make_fake_media,
    make_show,
    set_playlist,
    set_rules,
)

DAY = timedelta(days=1)
SHOW = "قصص الأنبياء"
BOOK = "شرح بلوغ المرام"
#: clip title -> the topics the stand-in model keeps
CLIPS = {
    "هل يجوز الدعاء بحق الأنبياء": [],  # only mentions the prophets: not a prophets' story
    "قصة نبي الله نوح عليه السلام": ["prophets_stories"],
    "كيف عذب قوم شعيب": ["prophets_stories"],
}


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


class Model:
    """A stand-in for the language model: answers the series and the clip questions."""

    def __init__(self, topics: list[str] | None = None, *, down: bool = False) -> None:
        self.topics = ["tafsir"] if topics is None else topics
        self.down = down
        self.series_questions = 0
        self.clip_questions: list[list[str]] = []

    def __call__(self, text: str, cfg: Any) -> str:
        if self.down:
            raise urllib.error.URLError("connection refused")
        if "صنّف سلسلة" in text:
            self.series_questions += 1
            return json.dumps({"topics": self.topics, "ramadan": False, "hajj": False})
        titles = re.findall(r"^\d+\. \[[^\]]*\] (.*)$", text, flags=re.M)
        self.clip_questions.append(titles)
        return json.dumps(
            {"clips": [{"n": i, "keep": CLIPS.get(t, [])} for i, t in enumerate(titles, 1)]},
            ensure_ascii=False,
        )


def _encyclopedia(calls: list[str]) -> Any:
    """A MediaWiki answer for the book: filed under books of hadith."""

    def get(url: str) -> dict[str, Any]:
        calls.append(url)
        return {
            "query": {
                "pages": {
                    "1": {
                        "index": 1,
                        "title": "بلوغ المرام",
                        "fullurl": "https://ar.wikipedia.org/wiki/x",
                        "categories": [{"title": "تصنيف:كتب حديث"}],
                        "extract": "كتاب في أحاديث الأحكام",
                    }
                }
            }
        }

    return get


async def _world(db: Database, *, name: str = SHOW, clips: bool = True) -> tuple[UUID, UUID]:
    """A show of eight episodes and (``clips``) three short videos; returns (library, season)."""
    w = await make_show(db, episodes=8, loose=0, name=name)
    if clips:
        made = []
        for i, title in enumerate(CLIPS):
            made.append(
                await add_youtube_video(
                    db,
                    w.library,
                    title=title,
                    filename=f"clip_{i}.mp4",
                    upload=date(2023, 1, 1) + i * DAY,
                    duration=70.0,
                )
            )
        await set_playlist(db, "PLclips", "لقطات", made)
    return w.library, w.library  # the season id is read after detect


async def _run(db: Database, lib: UUID, *, models: bool = True) -> None:
    rt = await core_runtime(db, models=models)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    await drain_core(db, rt, limit=900)


async def _season(db: Database) -> UUID:
    return UUID(str(await db.fetchval("SELECT id FROM series_seasons WHERE retired_at IS NULL")))


async def _tags(db: Database, sid: UUID) -> dict[str, float]:
    return {
        str(r["tag"]): float(r["score"])
        for r in await db.fetch("SELECT tag, score FROM season_tags WHERE season_id = $1", str(sid))
    }


async def _clip_tags(db: Database) -> dict[str, dict[str, float]]:
    out: dict[str, dict[str, float]] = {}
    for r in await db.fetch(
        "SELECT COALESCE(yv.title, v.title) AS title, t.tag AS tag, t.score AS score"
        " FROM video_topic_tags t JOIN videos v ON v.id = t.video_id"
        " LEFT JOIN media_metadata_enrichment m ON m.video_id = v.id AND m.provider = 'youtube'"
        " LEFT JOIN youtube_videos yv ON yv.youtube_id = m.external_id"
    ):
        out.setdefault(str(r["title"]), {})[str(r["tag"])] = float(r["score"])
    return out


async def _jobs(db: Database, stage: str) -> list[Any]:
    return list(await db.fetch("SELECT * FROM processing_jobs WHERE stage = $1 ORDER BY id", stage))


# ------------------------------------------------------------------------------ the graph


@pytest.mark.unit
def test_graph_order_tag_checks_clips_books_to_plan() -> None:
    validate()
    by = {s.name: s for s in GRAPH}
    edges = {n: {(e.to, e.map) for e in by[n].edges} for n in by}
    assert {("check_tags", "same"), ("check_clips", "library_of")} <= edges["tag"]
    assert ("books", "same") in edges["check_tags"]
    assert ("clip_groups", "same") in edges["check_clips"]
    for n in ("check_tags", "check_clips", "books"):
        assert ("plan_channels", "catalog") in edges[n]
        assert by[n].pool == "lookup" and by[n].lane == "seasons"
    assert by["check_tags"].subject_kinds == ("season",)
    assert by["books"].subject_kinds == ("season",)
    assert by["check_clips"].subject_kinds == ("library",)
    bound = {s.name: s for s in bound_graph()}
    assert all(bound[n].run and bound[n].input_fingerprint for n in by if n in (
        "check_tags", "check_clips", "books"))  # fmt: skip


# ------------------------------------------------------------------------------ check_tags


@pytest.mark.asyncio
async def test_the_model_corrects_a_series_tags_and_is_asked_once(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model(["tafsir"])
    monkeypatch.setattr(tag_check, "ask", model)
    lib, _ = await _world(db, clips=False)
    await _run(db, lib)
    sid = await _season(db)

    tags = await _tags(db, sid)
    assert tags["topic:tafsir"] >= 0.8 and tags["genre:tafsir"] >= 0.8
    assert tags["topic:prophets_stories"] < 0.5  # the keywords saw it; the model did not
    assert model.series_questions == 1
    assert await db.fetchval("SELECT count(*) FROM tag_checks WHERE season_id = $1", str(sid)) == 1
    assert (await _jobs(db, "check_tags"))[-1]["state"] == "done"

    # a new tag run restores the keyword tags; the cached verdict is applied again, unasked
    await db.execute("DELETE FROM season_tags WHERE season_id = $1", str(sid))
    await db.execute(
        "INSERT INTO season_tags (season_id, tag, score, evidence)"
        " VALUES ($1,'topic:prophets_stories',0.99,'{\"why\": \"name\"}')",
        str(sid),
    )
    rt = await core_runtime(db, models=True)
    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="check_tags", force=True)
    await drain_core(db, rt, only={"check_tags"})
    again = await _tags(db, sid)
    assert again["topic:tafsir"] >= 0.8 and again["topic:prophets_stories"] < 0.5
    assert model.series_questions == 1


@pytest.mark.asyncio
async def test_a_tag_run_that_changes_nothing_keeps_the_corrections(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tag_check, "ask", Model(["tafsir"]))
    lib, _ = await _world(db, clips=False)
    await _run(db, lib)
    sid = await _season(db)
    corrected = await _tags(db, sid)
    rt = await core_runtime(db, models=True)
    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="tag", force=True)
    await drain_core(db, rt)
    assert await _tags(db, sid) == corrected  # the same keyword tags: nothing rewritten


@pytest.mark.asyncio
async def test_an_unreachable_model_fails_the_job_and_leaves_the_tags(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tag_check, "ask", Model(down=True))
    lib, _ = await _world(db, clips=False)
    await _run(db, lib)
    sid = await _season(db)
    jobs = await _jobs(db, "check_tags")
    assert jobs and all(j["state"] == "failed" for j in jobs[-1:])
    assert "model_unavailable" in str(jobs[-1]["error"])
    assert await db.fetchval("SELECT count(*) FROM tag_checks") == 0
    assert (await _tags(db, sid))["topic:prophets_stories"] >= 0.5
    assert await _jobs(db, "books") == []  # nothing downstream of a failed check ran


@pytest.mark.asyncio
async def test_the_model_is_not_asked_when_switched_off(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model()
    monkeypatch.setattr(tag_check, "ask", model)
    lib, _ = await _world(db)
    await set_rules(db, "tagcheck", {"enabled": False})
    await _run(db, lib)
    assert model.series_questions == 0 and model.clip_questions == []
    assert [j["state"] for j in await _jobs(db, "check_tags")] == ["done"]


# ------------------------------------------------------------------------------ check_clips


@pytest.mark.asyncio
async def test_clips_go_to_the_model_in_batches_and_rejected_topics_are_damped(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model()
    monkeypatch.setattr(tag_check, "ask", model)
    lib, _ = await _world(db)
    await set_rules(db, "tagcheck", {"clip_batch": 2})
    await _run(db, lib)

    assert sorted(len(q) for q in model.clip_questions) == [1, 2]  # three clips, two questions
    assert await db.fetchval("SELECT count(*) FROM clip_checks") == 3
    tags = await _clip_tags(db)
    dua = tags["هل يجوز الدعاء بحق الأنبياء"]
    assert dua["topic:prophets_stories"] < 0.5  # a fatwa that names the prophets is no story
    assert tags["قصة نبي الله نوح عليه السلام"]["topic:prophets_stories"] >= 0.5
    assert [j["state"] for j in await _jobs(db, "check_clips")] == ["done"]

    # again: every clip's tags carry its verdict; the model is not asked
    asked = len(model.clip_questions)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="check_clips", force=True)
    await drain_core(db, await core_runtime(db, models=True), only={"check_clips"})
    assert len(model.clip_questions) == asked


@pytest.mark.asyncio
async def test_a_restored_clip_gets_its_cached_verdict_again_without_asking(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model()
    monkeypatch.setattr(tag_check, "ask", model)
    lib, _ = await _world(db)
    await _run(db, lib)
    asked = len(model.clip_questions)
    # the keyword tagger runs again and restores the raw tags
    raw = await library_tags(db, lib, DetectorConfig())
    await db.execute("DELETE FROM video_topic_tags")
    for vid, tags in raw.items():
        for tag, score in tags.items():
            await db.execute(
                "INSERT INTO video_topic_tags (video_id, tag, score) VALUES ($1,$2,$3)",
                vid,
                tag,
                score,
            )
    assert (await _clip_tags(db))["هل يجوز الدعاء بحق الأنبياء"]["topic:prophets_stories"] > 0.5
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="check_clips", force=True)
    await drain_core(db, await core_runtime(db, models=True), only={"check_clips"})
    assert len(model.clip_questions) == asked
    assert (await _clip_tags(db))["هل يجوز الدعاء بحق الأنبياء"]["topic:prophets_stories"] < 0.5


@pytest.mark.asyncio
async def test_clips_left_unanswered_are_kept_for_the_retry(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model()
    clip_questions = 0

    def flaky(text: str, cfg: Any) -> str:
        nonlocal clip_questions
        if "صنّف سلسلة" not in text:
            clip_questions += 1
            if clip_questions == 1:  # the first question of the first run is lost
                raise urllib.error.URLError("busy")
        return model(text, cfg)

    monkeypatch.setattr(tag_check, "ask", flaky)
    lib, _ = await _world(db)
    await set_rules(db, "tagcheck", {"clip_batch": 1})
    await _run(db, lib)
    jobs = await _jobs(db, "check_clips")
    assert jobs[-1]["state"] == "done" and jobs[-1]["attempts"] >= 2  # the retry asked the rest
    assert clip_questions == 4  # three clips, one question lost on the way
    assert await db.fetchval("SELECT count(*) FROM clip_checks") == 3
    assert sum(len(q) for q in model.clip_questions) == 3


# ------------------------------------------------------------------------------ books


@pytest.mark.asyncio
async def test_the_book_a_series_explains_decides_its_topics_and_is_looked_up_once(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tag_check, "ask", Model(["fiqh"]))
    calls: list[str] = []
    monkeypatch.setattr(books, "http_get", lambda url, cfg: _encyclopedia(calls)(url))
    await set_rules(db, "books", {"request_gap_s": 0})
    lib, _ = await _world(db, name=BOOK, clips=False)
    await _run(db, lib)
    sid = await _season(db)

    tags = await _tags(db, sid)
    assert tags["topic:hadith"] > 0.89 and tags["genre:hadith"] > 0.89  # the book's subject
    row = await db.fetchrow("SELECT name, fields FROM series_books WHERE season_id = $1", str(sid))
    ref = books.book_of(BOOK)
    assert ref is not None
    assert row["name"] == ref.name and "hadith" in row["fields"]
    assert await db.fetchval("SELECT title FROM books") == "بلوغ المرام"
    assert len(calls) == 1
    assert (await _jobs(db, "books"))[-1]["state"] == "done"

    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="books", force=True)
    await drain_core(db, await core_runtime(db, models=True), only={"books"})
    assert len(calls) == 1  # the book is cached


@pytest.mark.asyncio
async def test_a_series_that_names_no_book_makes_no_lookup(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(tag_check, "ask", Model())
    calls: list[str] = []
    monkeypatch.setattr(books, "http_get", lambda url, cfg: _encyclopedia(calls)(url))
    lib, _ = await _world(db, clips=False)
    await _run(db, lib)
    assert calls == [] and (await _jobs(db, "books"))[-1]["state"] == "done"


# ------------------------------------------------------------------------------ tag


@pytest.mark.asyncio
async def test_an_episode_in_a_chapter_playlist_takes_its_topic_and_the_planner_reads_it(
    db: Database,
) -> None:
    w = await make_show(db, episodes=12, loose=0, name=SHOW)
    # three of the episodes are also in a chapter playlist of the show
    await set_playlist(db, "PLch", "كتاب الصيام", [(v, w.youtube[v]) for v in w.episodes[:3]])
    await _run(db, w.library, models=False)

    stored: dict[str, dict[str, float]] = {}
    for r in await db.fetch("SELECT video_id, tag, score FROM episode_tags"):
        stored.setdefault(str(r["video_id"]), {})[str(r["tag"])] = float(r["score"])
    chapter = {str(v) for v in w.episodes[:3]}
    assert all("topic:fasting" in stored.get(v, {}) for v in chapter), stored
    assert not any("topic:fasting" in t for v, t in stored.items() if v not in chapter)

    seasons, _loose, _notes = await load_inputs(db)
    eps = [e for s in seasons for e in s.episodes]
    assert {e.path for e in eps if "topic:fasting" in e.tags} == chapter  # the planner's input


async def _episode_tag_map(db: Database) -> dict[str, dict[str, float]]:
    out: dict[str, dict[str, float]] = {}
    for r in await db.fetch("SELECT video_id, tag, score FROM episode_tags"):
        out.setdefault(str(r["video_id"]), {})[str(r["tag"])] = float(r["score"])
    return out


async def _retag(db: Database, sid: UUID) -> None:
    rt = await core_runtime(db, models=False)
    await enqueue_job(db, subject_kind="season", subject_id=sid, stage="tag")
    await drain_core(db, rt, limit=300)


@pytest.mark.asyncio
async def test_a_playlist_sync_tags_the_episodes_again(db: Database) -> None:
    """Chapter playlists are evidence: a playlist synced later re-tags the season (the
    playlists are in its fingerprint), though its episodes did not change."""
    w = await make_show(db, episodes=12, loose=0, name=SHOW)
    await _run(db, w.library, models=False)
    assert not any("topic:fasting" in t for t in (await _episode_tag_map(db)).values())
    await set_playlist(db, "PLch", "كتاب الصيام", [(v, w.youtube[v]) for v in w.episodes[:3]])
    await _retag(db, await _season(db))
    stored = await _episode_tag_map(db)
    assert all("topic:fasting" in stored.get(str(v), {}) for v in w.episodes[:3]), stored


@pytest.mark.asyncio
async def test_an_episode_that_leaves_the_season_loses_its_episode_tags(db: Database) -> None:
    w = await make_show(db, episodes=12, loose=0, name=SHOW)
    await set_playlist(db, "PLch", "كتاب الصيام", [(v, w.youtube[v]) for v in w.episodes[:3]])
    await _run(db, w.library, models=False)
    gone = str(w.episodes[0])
    assert "topic:fasting" in (await _episode_tag_map(db)).get(gone, {})
    await db.execute("DELETE FROM series_episodes WHERE video_id = $1", gone)
    await _retag(db, await _season(db))
    stored = await _episode_tag_map(db)
    assert gone not in stored
    assert all("topic:fasting" in stored.get(str(v), {}) for v in w.episodes[1:3]), stored


@pytest.mark.unit
def test_a_playlist_holding_several_shows_is_no_chapter() -> None:
    ep = {"id": "v1", "title": "المجلس 14", "description": "", "dur": 1500.0, "yid": "yt1"}

    def tags(shows: dict[str, int]) -> dict[str, dict[str, float]]:
        return _episode_tags(
            [ep], ["قناة"], frozenset(), {"yt1": ["PL"]}, {"PL": "كتاب الصيام"}, shows
        )

    assert "topic:fasting" in tags({"PL": 1})["v1"]
    assert "v1" not in tags({"PL": 2})  # a collection of shows, not a chapter
