"""How long the streaming service shares a live page's yt-dlp lookup is a setting
(rules ``tv``: live_lookup_reuse_seconds); the streaming service reads it."""

from __future__ import annotations

from maktaba_pipeline import rules
from maktaba_pipeline.schedule.tv_settings import MaktabaTvConfig


def test_default_is_ten_minutes() -> None:
    assert MaktabaTvConfig().live_lookup_reuse_seconds == 600


def test_limits_are_checked() -> None:
    for v in (5, 7200):
        _, bad = rules.config("tv", {"live_lookup_reuse_seconds": v})
        assert set(bad) == {"live_lookup_reuse_seconds"}
    cfg, bad = rules.config("tv", {"live_lookup_reuse_seconds": 60})
    assert not bad and cfg.live_lookup_reuse_seconds == 60


def test_schema_carries_help_and_bounds() -> None:
    tv = next(s for s in rules.schema()["sections"] if s["key"] == "tv")
    f = next(f for f in tv["fields"] if f["name"] == "live_lookup_reuse_seconds")
    assert (f["default"], f["minimum"], f["maximum"]) == (600, 10, 3600)
    assert f["help"]
