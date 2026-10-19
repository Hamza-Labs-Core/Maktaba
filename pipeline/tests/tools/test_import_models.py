# ruff: noqa: E501  (long SQL strings and test data read better unwrapped)
"""The mediaintel import of the language model's answers, the books and the episodes' own tags:
nothing is asked again afterwards, and a second import adds only what the first lacked."""

from __future__ import annotations

import json
import sqlite3
import time
from collections.abc import Iterator
from datetime import timedelta
from pathlib import Path
from typing import Any
from uuid import UUID

import pytest

from maktaba_pipeline.classify import books as bk
from maktaba_pipeline.classify import tag_check as tc
from maktaba_pipeline.classify.series_detector import DetectorConfig
from maktaba_pipeline.engine.enqueue import enqueue_job
from maktaba_pipeline.engine.media import reset_runner
from maktaba_pipeline.engine.sql import loads
from maktaba_pipeline.engine.stages.tag import library_tags
from maktaba_pipeline.runtime import Database
from maktaba_pipeline.tools.import_mediaintel import run_import
from tests.engine.factory import make_video
from tests.stages.world import core_runtime, drain_core, make_fake_media
from tests.tools.test_import_mediaintel import EPOCH, _cache, _library

BOOK_SERIES = "شرح بلوغ المرام"
CLIPS = {
    "Y:/chan/clip0.mp4": ("هل يجوز الدعاء بحق الأنبياء", []),
    "Y:/chan/clip1.mp4": ("قصة نبي الله نوح عليه السلام", ["prophets_stories"]),
}
ANSWER = json.dumps({"topics": ["tafsir"], "ramadan": False, "hajj": False})
RAW = {"topic:seerah": (0.9, "name"), "genre:seerah": (0.9, "primary")}


@pytest.fixture(autouse=True)
def _media() -> Iterator[None]:
    make_fake_media({})
    yield
    reset_runner()


@pytest.fixture(autouse=True)
def _nothing_may_ask(monkeypatch: pytest.MonkeyPatch) -> None:
    """After the import the model and the encyclopedia are never asked."""

    def refuse(*_a: object, **_k: object) -> None:
        raise AssertionError("asked the model / encyclopedia again after the import")

    monkeypatch.setattr(tc, "ask", refuse)
    monkeypatch.setattr(bk, "http_get", refuse)


def _book() -> bk.BookRef:
    ref = bk.book_of(BOOK_SERIES)
    assert ref is not None
    return ref


def _corrected() -> dict[str, tuple[float, str]]:
    """The series' tags as the prototype left them: the model's verdict, then the book's."""
    verdict = tc.parse(ANSWER, tc.TagCheckConfig())
    assert verdict is not None
    checked = tc.apply(RAW, verdict, tc.TagCheckConfig())
    return bk.apply(checked, ("hadith",), {}, "book «بلوغ المرام»", bk.BooksConfig())


def _cache_with_clips(path: Path) -> Path:
    """The cache before any check: the series named after a book, and two clips next to it
    with their keyword tags."""
    _cache(path)
    c = sqlite3.connect(path)
    c.execute("UPDATE series SET name = ?", (BOOK_SERIES,))
    for i, (clip, (title, _keep)) in enumerate(CLIPS.items()):
        c.execute(
            "INSERT INTO files VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
            (clip, "Y:/chan", f"clip{i}", "mp4", 2000 + i, (EPOCH + timedelta(seconds=100 + i)).timestamp(),
             70.0, 1280, 720, None, None, f"ytc{i}", "info_json", None, None, None, None, None, None),
        )  # fmt: skip
        c.execute(
            "INSERT INTO yt_videos VALUES (?,?,?,?,?,?,?,?,?,?)",
            (f"ytc{i}", title, f"2023-01-0{i + 1}", "exact", 70.0, "UCc", "Sheikh", None, "videos", "info_json"),
        )  # fmt: skip
        c.executemany(
            "INSERT INTO video_tags VALUES (?,?,?)",
            [(clip, t, s) for t, s in _clip_raw(title).items()],
        )
    c.commit()
    c.close()
    return path


def _add_model_tables(path: Path) -> dict[str, Any]:
    """The cache as the prototype leaves it after check-tags, check-clips and books: two clips
    next to the series, the corrected tags and the answers behind them."""
    cfg = tc.TagCheckConfig()
    verdict = tc.parse(ANSWER, cfg)
    assert verdict is not None
    checked = tc.apply(RAW, verdict, cfg)
    final = _corrected()
    ref = _book()
    c = sqlite3.connect(path)
    c.executescript(
        """
        CREATE TABLE tag_checks (series_id TEXT PRIMARY KEY, input_hash TEXT NOT NULL, model TEXT NOT NULL,
          answer TEXT NOT NULL, applied_hash TEXT, checked_at TEXT NOT NULL);
        CREATE TABLE clip_checks (path TEXT PRIMARY KEY, input_hash TEXT NOT NULL, model TEXT NOT NULL,
          topics TEXT NOT NULL, applied_hash TEXT, checked_at TEXT NOT NULL);
        CREATE TABLE books (name TEXT PRIMARY KEY, version INTEGER NOT NULL, title TEXT NOT NULL, url TEXT NOT NULL,
          categories TEXT NOT NULL, summary TEXT NOT NULL, checked_at REAL NOT NULL);
        CREATE TABLE series_books (series_id TEXT PRIMARY KEY, name TEXT NOT NULL, chapter TEXT NOT NULL, fields TEXT NOT NULL);
        CREATE TABLE episode_tags (path TEXT NOT NULL, tag TEXT NOT NULL, score REAL NOT NULL, PRIMARY KEY (path, tag));
        """
    )
    c.execute("DELETE FROM series_tags")
    c.executemany(
        "INSERT INTO series_tags VALUES ('s1',?,?,?)", [(t, s, e) for t, (s, e) in final.items()]
    )
    c.execute(
        "INSERT INTO tag_checks VALUES ('s1','cache-own-hash','qwen2.5:7b',?,?,'2026-10-18 01:00:00')",
        (ANSWER, tc.tags_hash(checked)),
    )
    c.execute(
        "INSERT INTO books VALUES (?,?,?,?,?,?,?)",
        (
            ref.name,
            bk.BOOKS_VERSION,
            "بلوغ المرام",
            "https://x/y",
            json.dumps(["تصنيف:كتب حديث"]),
            "كتاب",
            time.time(),
        ),
    )
    c.execute("INSERT INTO series_books VALUES ('s1',?,?,?)", (ref.name, ref.chapter, "hadith"))
    for i in range(3):
        c.execute(
            "INSERT INTO episode_tags VALUES (?, 'topic:fasting', 0.9)", (f"Y:/chan/ep{i:02d}.mp4",)
        )
    for clip_path, (title, keep) in CLIPS.items():
        damped = tc.apply_clip(_clip_raw(title), keep, cfg)
        c.execute("DELETE FROM video_tags WHERE path = ?", (clip_path,))
        c.executemany(
            "INSERT INTO video_tags VALUES (?,?,?)", [(clip_path, t, s) for t, s in damped.items()]
        )
        c.execute(
            "INSERT INTO clip_checks VALUES (?,?,?,?,?,'2026-10-18 01:00:00')",
            (
                clip_path,
                "cache-own-hash",
                "qwen2.5:7b",
                json.dumps(keep),
                tc.clip_tags_hash(damped),
            ),
        )
    c.commit()
    c.close()
    return {"checked": checked, "final": final}


def _clip_raw(title: str) -> dict[str, float]:
    from maktaba_pipeline.classify.topic_tags import tag_video

    return {t.tag: float(t.score) for t in tag_video(title, "", 70.0)}


async def _world(db: Database) -> tuple[UUID, list[UUID]]:
    lib, vids = await _library(db)
    for i in range(len(CLIPS)):
        vids.append(
            await make_video(
                db, lib, filename=f"clip{i}.mp4", duration=70.0, size=2000 + i,
                mtime=EPOCH + timedelta(seconds=100 + i), path=f"Y:/chan/clip{i}.mp4",
            )
        )  # fmt: skip
    return lib, vids


async def _season_tags(db: Database) -> dict[str, tuple[float, str]]:
    return {
        str(r["tag"]): (round(float(r["score"]), 4), (loads(r["evidence"]) or {}).get("why", ""))
        for r in await db.fetch("SELECT tag, score, evidence FROM season_tags")
    }


async def _clip_scores(db: Database) -> dict[str, dict[str, float]]:
    out: dict[str, dict[str, float]] = {}
    for r in await db.fetch(
        "SELECT v.path AS path, t.tag AS tag, t.score AS score FROM video_topic_tags t JOIN videos v ON v.id = t.video_id"
    ):
        out.setdefault(str(r["path"]), {})[str(r["tag"])] = round(float(r["score"]), 4)
    return out


async def _restore_keyword_tags(db: Database, lib: UUID) -> None:
    """What the version sweep does to an imported library: the keyword tags come back."""
    await db.execute("DELETE FROM season_tags")
    for t, (s, e) in RAW.items():
        await db.execute(
            "INSERT INTO season_tags (season_id, tag, score, evidence) SELECT id, $1, $2, $3 FROM series_seasons",
            t, s, json.dumps({"why": e}),
        )  # fmt: skip
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


@pytest.mark.asyncio
async def test_the_answers_come_across_and_the_stages_ask_nothing_after(
    db: Database, tmp_path: Path
) -> None:
    lib, _ = await _world(db)
    cache = _cache_with_clips(tmp_path / "cache.db")
    want = _add_model_tables(cache)

    dry = await run_import(db, cache, [str(lib)], apply=False)
    assert dry.tag_checks == 1 and dry.clip_checks == 2
    assert await db.fetchval("SELECT count(*) FROM tag_checks") == 0  # rolled back

    report = await run_import(db, cache, [str(lib)], apply=True)
    assert (report.tag_checks, report.clip_checks, report.books, report.series_books) == (
        1,
        2,
        1,
        1,
    )
    assert report.episode_tags == 3
    for table, n in (("tag_checks", 1), ("clip_checks", 2), ("books", 1), ("series_books", 1)):
        assert report.tables[table] == (0, n)
    assert "model answers: 1 series, 2 clips" in report.render()

    # the corrected tags stand as the prototype left them
    assert await _season_tags(db) == {t: (round(s, 4), e) for t, (s, e) in want["final"].items()}
    clips = await _clip_scores(db)
    assert clips["Y:/chan/clip0.mp4"]["topic:prophets_stories"] < 0.5  # a fatwa, no prophets' story
    assert clips["Y:/chan/clip1.mp4"]["topic:prophets_stories"] >= 0.5
    assert await db.fetchval("SELECT count(*) FROM episode_tags WHERE tag = 'topic:fasting'") == 3

    # synthetic history for the three stages
    done = {
        (r["stage"], r["subject_kind"])
        for r in await db.fetch(
            "SELECT stage, subject_kind FROM processing_jobs WHERE outcome = 'imported'"
        )
    }
    assert {("check_tags", "season"), ("books", "season"), ("check_clips", "library")} <= done

    # live: the stages run again and ask nothing (the autouse guard fails any question)
    rt = await core_runtime(db, models=True)
    season = UUID(str(await db.fetchval("SELECT id FROM series_seasons")))
    before = int(await db.fetchval("SELECT max(id) FROM processing_jobs"))
    for stage in ("check_tags", "books"):
        await enqueue_job(db, subject_kind="season", subject_id=season, stage=stage)
    await enqueue_job(db, subject_kind="library", subject_id=lib, stage="check_clips")
    await drain_core(db, rt, only={"check_tags", "books", "check_clips"})
    live = await db.fetch("SELECT stage, state, outcome FROM processing_jobs WHERE id > $1", before)
    assert {(r["stage"], r["outcome"]) for r in live} == {
        ("check_tags", "skipped_unchanged"),
        ("books", "skipped_unchanged"),
        ("check_clips", "skipped_unchanged"),
    }
    assert await _season_tags(db) == {t: (round(s, 4), e) for t, (s, e) in want["final"].items()}


@pytest.mark.asyncio
async def test_a_second_import_adds_only_the_new_tables_and_restores_the_corrections(
    db: Database, tmp_path: Path
) -> None:
    """The first import ran with the tool as it was (no model tables); the version sweep then
    restored the keyword tags; the second brings the answers, applies them and records the
    stages as done."""
    lib, _ = await _world(db)
    cache = _cache_with_clips(tmp_path / "cache.db")
    first = await run_import(db, cache, [str(lib)], apply=True)
    assert first.tag_checks == first.clip_checks == first.books == 0
    jobs = int(await db.fetchval("SELECT count(*) FROM processing_jobs"))
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'check_tags'") == 0

    want = _add_model_tables(cache)
    await _restore_keyword_tags(db, lib)
    assert (await _clip_scores(db))["Y:/chan/clip0.mp4"]["topic:prophets_stories"] > 0.5

    second = await run_import(db, cache, [str(lib)], apply=True)
    assert (second.tag_checks, second.clip_checks, second.books, second.series_books) == (
        1,
        2,
        1,
        1,
    )
    assert second.jobs_skipped_existing > 0  # what the first import brought is left alone
    added = int(await db.fetchval("SELECT count(*) FROM processing_jobs")) - jobs
    # check_tags + books (the season) and check_clips (the library); the detect queued by
    # the first import is still pending, so no second one
    assert added == 3
    # restored and corrected, without a question
    assert await _season_tags(db) == {t: (round(s, 4), e) for t, (s, e) in want["final"].items()}
    assert (await _clip_scores(db))["Y:/chan/clip0.mp4"]["topic:prophets_stories"] < 0.5

    # and a third adds nothing
    third = await run_import(db, cache, [str(lib)], apply=True)
    assert (third.tag_checks, third.clip_checks, third.books, third.series_books) == (0, 0, 0, 0)
    assert third.jobs_imported == 0
    for table, n in (("tag_checks", 1), ("clip_checks", 2), ("books", 1), ("series_books", 1)):
        assert int(await db.fetchval(f"SELECT count(*) FROM {table}")) == n


@pytest.mark.asyncio
async def test_an_answer_under_another_model_is_kept_but_the_stage_will_ask_again(
    db: Database, tmp_path: Path
) -> None:
    lib, _ = await _world(db)
    cache = _cache_with_clips(tmp_path / "cache.db")
    _add_model_tables(cache)
    c = sqlite3.connect(cache)
    c.execute("UPDATE tag_checks SET model = 'another:model'")
    c.commit()
    c.close()
    report = await run_import(db, cache, [str(lib)], apply=True)
    assert report.tag_checks == 1
    assert await db.fetchval("SELECT count(*) FROM processing_jobs WHERE stage = 'check_tags'") == 0
