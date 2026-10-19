"""The display_title stage (Story 27.11): videos and series get their on-air titles after
detection, a hand-set title wins, the language model is asked once per title, and the
future blocks of the channels pick the new titles up at once."""

from __future__ import annotations

import json
from collections.abc import Iterator
from datetime import UTC, date, datetime, timedelta
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.classify import tag_check
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.graph import GRAPH, Subject, bound_graph
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import loads, to_ts
from maktaba_pipeline.engine.stages import display_title as stage
from maktaba_pipeline.runtime import Database
from tests.stages.world import (
    add_youtube_video,
    core_runtime,
    drain_core,
    make_fake_media,
    make_show,
    set_rules,
)

SHOW = "Stories of the Prophets"


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


async def _world(db: Database) -> Any:
    w = await make_show(db, episodes=8, loose=0, name=SHOW)
    vid, _ = await add_youtube_video(
        db,
        w.library,
        title="كيف نخاطب الجيل الجديد؟ - 1438⧸10⧸12 | Sheikh Test #دعوة",
        filename="talk.mp4",
        upload=date(2024, 6, 1),
        duration=2400.0,
    )
    w.loose.append(vid)
    return w


async def _detect(db: Database, lib: UUID) -> list[str]:
    rt = await core_runtime(db)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="detect")
    return await drain_core(db, rt, limit=900)


async def _titles(db: Database) -> dict[str, tuple[Any, Any, Any]]:
    return {
        str(r["id"]): (r["display_title"], r["display_title_source"], r["display_title_manual"])
        for r in await db.fetch(
            "SELECT id, display_title, display_title_source, display_title_manual FROM videos"
        )
    }


@pytest.mark.unit
def test_graph_display_title_follows_detect() -> None:
    by = {s.name: s for s in GRAPH}
    assert ("display_title", "same") in {(e.to, e.map) for e in by["detect"].edges}
    s = by["display_title"]
    assert s.subject_kinds == ("library",) and s.placement == "home"
    assert s.model_rules == "tagcheck"
    bound = {b.name: b for b in bound_graph()}
    assert bound["display_title"].run and bound["display_title"].input_fingerprint


@pytest.mark.asyncio
async def test_detect_then_display_titles(db: Database) -> None:
    w = await _world(db)
    ran = await _detect(db, w.library)
    assert "display_title" in ran
    titles = await _titles(db)
    talk = titles[str(w.loose[0])]
    assert talk == ("كيف نخاطب الجيل الجديد؟", "rules", None)
    # an episode titled «<show> - Episode n»: nothing but the series and its number
    assert {titles[str(e)][0] for e in w.episodes} == {""}
    name = await db.fetchval("SELECT display_name FROM series")
    assert name == SHOW
    job = (
        await db.fetch(
            "SELECT state FROM processing_jobs WHERE stage = 'display_title' ORDER BY id"
        )
    )[-1]
    assert job["state"] == "done"


@pytest.mark.asyncio
async def test_fingerprint_follows_titles_manual_and_rules(db: Database) -> None:
    w = await _world(db)
    await _detect(db, w.library)
    subj = Subject("library", w.library)
    fp = await stage.input_fingerprint(db, subj)
    assert fp == await stage.input_fingerprint(db, subj)
    await db.execute(
        "UPDATE videos SET display_title_manual = 'عنوان يدوي' WHERE id = $1", str(w.loose[0])
    )
    fp2 = await stage.input_fingerprint(db, subj)
    assert fp2 != fp
    await set_rules(db, "display_titles", {"max_length": 30})
    assert await stage.input_fingerprint(db, subj) != fp2


@pytest.mark.asyncio
async def test_future_blocks_get_the_display_title_and_a_manual_one_wins(db: Database) -> None:
    w = await _world(db)
    await _detect(db, w.library)
    cid = "00000000-0000-0000-0000-0000000000c7"
    await db.execute(
        "INSERT INTO channels (id, number, name, slug, mode, mode_config)"
        " VALUES ($1, 907, 'n', 'n7', 'schedule', '{}')",
        cid,
    )
    now = datetime.now(UTC)
    blocks = [(w.episodes[2], 1), (w.loose[0], 2)]
    for vid, seq in blocks:
        await db.execute(
            "INSERT INTO channel_programs (id, channel_id, seq, kind, video_id, start_at, end_at,"
            " source_duration, title_snapshot) VALUES ($1,$2,$3,'program',$4,$5,$6,600000,$7)",
            f"00000000-0000-0000-0000-0000000000e{seq}",
            cid,
            seq,
            str(vid),
            to_ts(db, now + timedelta(hours=seq)),
            to_ts(db, now + timedelta(hours=seq, minutes=10)),
            json.dumps({"title": "raw", "duration_ms": 600000}),
        )
    await db.execute(
        "UPDATE videos SET display_title_manual = 'الجيل الجديد' WHERE id = $1", str(w.loose[0])
    )
    rt = await core_runtime(db)
    await enqueue_job(
        db, subject_kind="library", subject_id=w.library, stage="display_title", force=True
    )
    await drain_core(db, rt, only={"display_title"})
    snaps = {
        int(r["seq"]): loads(r["title_snapshot"])
        for r in await db.fetch(
            "SELECT seq, title_snapshot FROM channel_programs WHERE channel_id = $1", cid
        )
    }
    ep = snaps[1]
    assert ep["source_title"] == "raw" and ep["series"] == SHOW
    assert ep["strip_episode"] == "Ep 3" and ep["on_air"] == f"{SHOW} · Ep 3"
    assert snaps[2]["title"] == "الجيل الجديد" and snaps[2]["on_air"] == "الجيل الجديد"


class Model:
    def __init__(self) -> None:
        self.questions = 0

    def __call__(self, text: str, cfg: Any) -> str:
        self.questions += 1
        rows = json.loads(text.splitlines()[-1])["titles"]
        return json.dumps(
            {"titles": [{"n": r["n"], "title": f"نظيف {r['n']}"} for r in rows]},
            ensure_ascii=False,
        )


@pytest.mark.asyncio
async def test_the_model_tidies_what_the_rules_leave_and_is_asked_once(
    db: Database, monkeypatch: pytest.MonkeyPatch
) -> None:
    model = Model()
    monkeypatch.setattr(tag_check, "ask", model)
    w = await _world(db)
    messy, _ = await add_youtube_video(
        db,
        w.library,
        title="درس الصبر - مجلس العلم - فوائد ومسائل - الجزء الثاني",
        filename="messy.mp4",
        upload=date(2024, 7, 1),
        duration=2400.0,
    )
    await _detect(db, w.library)
    await set_rules(db, "display_titles", {"use_llm": True, "llm_batch": 5})
    await set_rules(db, "tagcheck", {"enabled": True})
    rt = await core_runtime(db, models=True)
    await enqueue_job(
        db, subject_kind="library", subject_id=w.library, stage="display_title", force=True
    )
    await drain_core(db, rt, only={"display_title"})
    t = (await _titles(db))[str(messy)]
    assert t[1] == "llm" and t[0].startswith("نظيف")
    asked = model.questions
    assert asked >= 1
    assert await db.fetchval("SELECT count(*) FROM display_title_llm") >= 1
    await enqueue_job(
        db, subject_kind="library", subject_id=w.library, stage="display_title", force=True
    )
    await drain_core(db, rt, only={"display_title"})
    assert model.questions == asked  # every answer came from the cache
    assert (await _titles(db))[str(messy)][1] == "llm"
