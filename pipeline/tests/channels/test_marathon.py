"""Marathon planner — episode order, loop, cursor resume."""

from __future__ import annotations

from datetime import timedelta
from uuid import uuid4

from maktaba_pipeline.channels.base import ChannelMode, ContentItem
from maktaba_pipeline.channels.planner.marathon import MarathonPlanner

from .helpers import dt, mk_channel


def _episodes() -> list[ContentItem]:
    # Deliberately out of order; planner must sort by (season, episode).
    return [
        ContentItem(video_id=uuid4(), duration_ms=20 * 60_000, title="S1E2", season=1, episode=2),
        ContentItem(video_id=uuid4(), duration_ms=20 * 60_000, title="S1E1", season=1, episode=1),
        ContentItem(video_id=uuid4(), duration_ms=20 * 60_000, title="S2E1", season=2, episode=1),
    ]


def test_plays_in_episode_order():
    eps = _episodes()
    ch = mk_channel(ChannelMode.MARATHON, mode_config={"loop": True})
    p = MarathonPlanner(ch)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(eps, start_at=start, until=start + timedelta(hours=1))
    titles = [i.snapshot.get("title") for i in items[:3]]
    assert titles == ["S1E1", "S1E2", "S2E1"]


def test_loop_wraps_to_start():
    eps = _episodes()
    ch = mk_channel(ChannelMode.MARATHON, mode_config={"loop": True})
    p = MarathonPlanner(ch)
    start = dt("2026-06-14T00:00:00")
    # 3 eps × 20min = 60min; ask for 80min → 4 items, the 4th wraps to S1E1.
    items = p.plan(eps, start_at=start, until=start + timedelta(minutes=80))
    titles = [i.snapshot.get("title") for i in items]
    assert titles[:4] == ["S1E1", "S1E2", "S2E1", "S1E1"]


def test_no_loop_stops_after_last():
    eps = _episodes()
    ch = mk_channel(ChannelMode.MARATHON, mode_config={"loop": False})
    p = MarathonPlanner(ch)
    start = dt("2026-06-14T00:00:00")
    items = p.plan(eps, start_at=start, until=start + timedelta(hours=4))
    # Only the 3 episodes once; the packer pads the long tail.
    assert len(items) == 3


def test_cursor_resumes_position():
    eps = _episodes()
    ch = mk_channel(ChannelMode.MARATHON, mode_config={"loop": True})
    start = dt("2026-06-14T00:00:00")
    p1 = MarathonPlanner(ch)
    p1.plan(eps, start_at=start, until=start + timedelta(minutes=40))  # 2 eps
    cursor = p1.export_state()
    p2 = MarathonPlanner(ch, cursor=cursor)
    items = p2.plan(eps, start_at=start, until=start + timedelta(minutes=20))
    # Resumes at the 3rd episode, not the 1st.
    assert items[0].snapshot.get("title") == "S2E1"
