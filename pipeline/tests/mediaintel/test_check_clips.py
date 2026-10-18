"""Step check-clips: the model's verdict on each clip corrects its tags, once."""

from __future__ import annotations

import json
import sqlite3
from typing import Any

import pytest

from maktaba_pipeline.classify import tag_check as tc
from scripts.mediaintel import check_clips

pytestmark = pytest.mark.unit


def _db() -> sqlite3.Connection:
    conn = sqlite3.connect(":memory:")
    conn.executescript("""
        CREATE TABLE files (path TEXT PRIMARY KEY, folder TEXT, stem TEXT, duration_s REAL,
                            youtube_id TEXT, plex_library TEXT);
        CREATE TABLE yt_videos (youtube_id TEXT PRIMARY KEY, title TEXT);
        CREATE TABLE series_episodes (series_id TEXT, path TEXT);
        CREATE TABLE video_tags (path TEXT, tag TEXT, score REAL, PRIMARY KEY (path, tag));
    """)  # fmt: skip
    rows = [("Y:/k/1.mp4", "هل يجوز الدعاء بحق الأنبياء", 120),
            ("Y:/k/2.mp4", "قصة نوح عليه السلام", 300),
            ("Y:/k/long.mp4", "محاضرة طويلة عن الأنبياء", 3600)]  # fmt: skip
    for path, stem, dur in rows:
        conn.execute("INSERT INTO files VALUES (?,?,?,?,NULL,'عثمان الخميس')",
                     (path, "Y:/k", stem, dur))  # fmt: skip
        conn.execute(
            "INSERT INTO video_tags VALUES (?,?,?)", (path, "topic:prophets_stories", 0.95)
        )
    return conn


def test_the_model_keeps_the_story_and_drops_the_mention(monkeypatch: Any) -> None:
    asked: list[str] = []

    def ask(text: str, cfg: tc.TagCheckConfig) -> str:
        asked.append(text)
        return json.dumps({"clips": [{"n": 1, "keep": []}, {"n": 2, "keep": ["prophets_stories"]}]})

    monkeypatch.setattr(tc, "ask", ask)
    conn = _db()
    cfg = tc.TagCheckConfig(clip_batch=4)
    got = check_clips.run(conn, cfg)
    assert got["clips"] == 2 and got["asked"] == 2  # the hour-long video is no clip
    score = dict(conn.execute("SELECT path, score FROM video_tags").fetchall())
    assert score["Y:/k/1.mp4"] == round(0.95 * cfg.damp, 3)
    assert score["Y:/k/2.mp4"] == 0.95
    again = check_clips.run(conn, cfg)  # answered and applied: nothing to do
    assert len(asked) == 1 and again["asked"] == 0 and again["applied"] == 0


def test_a_corrected_clip_is_not_asked_again(monkeypatch: Any) -> None:
    # two topics, one dropped: the clip still has a topic, with fewer candidates than asked
    asked: list[str] = []

    def ask(text: str, cfg: tc.TagCheckConfig) -> str:
        asked.append(text)
        return json.dumps(
            {"clips": [{"n": 1, "keep": ["fiqh"]}, {"n": 2, "keep": ["prophets_stories"]}]}
        )

    monkeypatch.setattr(tc, "ask", ask)
    conn = _db()
    conn.execute("INSERT INTO video_tags VALUES ('Y:/k/1.mp4', 'topic:fiqh', 0.9)")
    cfg = tc.TagCheckConfig(clip_batch=4)
    check_clips.run(conn, cfg)
    again = check_clips.run(conn, cfg)
    assert len(asked) == 1 and again["asked"] == 0 and again["cached"] == 2
