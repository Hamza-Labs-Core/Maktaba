"""Smart-mix planner — daypart distribution + shuffle fallback (D7)."""

from __future__ import annotations

from datetime import timedelta
from uuid import uuid4

from maktaba_pipeline.channels.base import ChannelMode, ContentItem
from maktaba_pipeline.channels.planner.smartmix import SmartMixPlanner

from .helpers import dt, mk_channel, mk_content


def _classified(genre: str, n: int) -> list[ContentItem]:
    return [
        ContentItem(video_id=uuid4(), duration_ms=20 * 60_000, title=f"{genre}{i}", genre=genre)
        for i in range(n)
    ]


def test_falls_back_to_shuffle_without_classification() -> None:
    ch = mk_channel(ChannelMode.SMART_MIX)
    content = mk_content(5, dur_ms=20 * 60_000)  # no genre fields
    p = SmartMixPlanner(ch, seed=1)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=2))
    assert items, "fallback must still produce a schedule"
    assert sum(i.duration_ms for i in items) >= 2 * 60 * 60_000
    state = p.export_state()
    assert "fallback" in state  # fallback bag persisted for continuity


def test_balances_genres_toward_uniform_target() -> None:
    ch = mk_channel(ChannelMode.SMART_MIX)
    content = _classified("comedy", 10) + _classified("drama", 10)
    p = SmartMixPlanner(ch, seed=2)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=10))
    genres = [i.snapshot.get("genre") for i in items]
    comedy = genres.count("comedy")
    drama = genres.count("drama")
    # Uniform target → roughly balanced (within a few items).
    assert abs(comedy - drama) <= 2, f"comedy={comedy} drama={drama}"


def test_weights_bias_distribution() -> None:
    ch = mk_channel(ChannelMode.SMART_MIX, mode_config={"weights": {"comedy": 3, "drama": 1}})
    content = _classified("comedy", 10) + _classified("drama", 10)
    p = SmartMixPlanner(ch, seed=2)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=10))
    genres = [i.snapshot.get("genre") for i in items]
    assert genres.count("comedy") > genres.count("drama")


def test_empty_content_yields_nothing() -> None:
    ch = mk_channel(ChannelMode.SMART_MIX)
    p = SmartMixPlanner(ch, seed=1)
    start = dt("2026-06-14T00:00:00")
    assert p.plan([], start_at=start, until=start + timedelta(hours=1)) == []
