"""Time-slot planner — daypart windows, padding, helpers."""

from __future__ import annotations

from datetime import timedelta

from maktaba_pipeline.channels.base import BlockKind, ChannelMode
from maktaba_pipeline.channels.planner.schedule import (
    SchedulePlanner,
    parse_hhmm,
    slot_days,
)

from .helpers import dt, mk_channel, mk_content


def test_parse_hhmm() -> None:
    assert parse_hhmm("08:30") == 8 * 60 + 30
    assert parse_hhmm("00:00") == 0
    assert parse_hhmm("garbage") == 0


def test_slot_days_default_all() -> None:
    assert slot_days({}) == set(range(7))
    assert slot_days({"days": ["mon", "wed"]}) == {0, 2}
    assert slot_days({"days": [0, 6]}) == {0, 6}


def test_fills_active_slot_then_pads_gap() -> None:
    # One slot 08:00–10:00; horizon 06:00–12:00. Expect: pad 06–08,
    # programs 08–10, pad 10–12.
    cfg = {"slots": [{"start": "08:00", "end": "10:00"}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    content = mk_content(4, dur_ms=30 * 60_000)
    p = SchedulePlanner(ch, seed=1, slot_content=[content])
    start = dt("2026-06-14T06:00:00")
    items = p.plan(content, start_at=start, until=start + timedelta(hours=6))
    total = sum(i.duration_ms for i in items)
    assert total == 6 * 60 * 60_000  # full coverage
    # Leading + trailing pads present.
    assert items[0].kind == BlockKind.FILLER
    assert items[-1].kind == BlockKind.FILLER
    # At least one program in the middle.
    assert any(i.kind == BlockKind.PROGRAM for i in items)


def test_no_slots_yields_nothing() -> None:
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config={"slots": []})
    p = SchedulePlanner(ch)
    start = dt("2026-06-14T06:00:00")
    assert p.plan([], start_at=start, until=start + timedelta(hours=2)) == []


def test_day_filtered_slot_inactive_pads_whole_window() -> None:
    # Slot only on Monday; 2026-06-14 is a Sunday → no active slot → all pad.
    cfg = {"slots": [{"start": "00:00", "end": "24:00", "days": ["mon"]}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    content = mk_content(3)
    p = SchedulePlanner(ch, slot_content=[content])
    start = dt("2026-06-14T06:00:00")  # Sunday
    items = p.plan(content, start_at=start, until=start + timedelta(hours=2))
    assert all(i.kind == BlockKind.FILLER for i in items)
    assert sum(i.duration_ms for i in items) == 2 * 60 * 60_000


def _lane(name: str) -> dict[str, object]:
    return {"type": "lane", "lane": name, "series_ids": ["s"]}


def test_lane_slot_plays_its_own_content_not_the_channel_wide_pool() -> None:
    cfg = {
        "slots": [
            {"start": "08:00", "end": "09:00", "source": _lane("quran")},
            {"start": "09:00", "end": "10:00"},
        ]
    }
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    lane, wide = mk_content(4), mk_content(4)
    p = SchedulePlanner(ch, slot_content=[lane, None])
    start = dt("2026-06-14T08:00:00")
    played = [i.video_id for i in p.plan(wide, start_at=start, until=start + timedelta(hours=2))]
    assert played == [lane[0].video_id, lane[1].video_id, wide[0].video_id, wide[1].video_id]


def test_a_lane_in_two_slots_is_one_queue() -> None:
    # The same lane at 08:00 and 20:00: the evening slot carries on from where
    # the morning stopped, and the cursor survives a top-up.
    cfg = {
        "slots": [
            {"start": "08:00", "end": "09:00", "source": _lane("hafs")},
            {"start": "20:00", "end": "21:00", "source": _lane("hafs")},
        ]
    }
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    eps = mk_content(10)
    p = SchedulePlanner(ch, slot_content=[eps, eps])
    start = dt("2026-06-14T08:00:00")
    items = p.plan([], start_at=start, until=start + timedelta(hours=13))
    played = [i.video_id for i in items if i.kind == BlockKind.PROGRAM]
    assert played == [e.video_id for e in eps[:4]]

    again = SchedulePlanner(ch, cursor=p.export_state(), slot_content=[eps, eps])
    nxt = again.plan([], start_at=dt("2026-06-15T08:00:00"), until=dt("2026-06-15T09:00:00"))
    assert [i.video_id for i in nxt] == [eps[4].video_id, eps[5].video_id]


def test_lane_episodes_follow_the_plan_order_and_skip_unusable() -> None:
    from uuid import uuid4

    from maktaba_pipeline.channels.repo import lane_episodes, slot_lane

    a, b, c = (str(uuid4()) for _ in range(3))
    lane = {
        "units": [
            {"episodes": [{"path": a, "duration_s": 60, "title": "1"}]},
            {
                "episodes": [
                    {"path": "Y:/not/an/id.mp4", "duration_s": 60},
                    {"path": b, "duration_s": 0},
                    {"path": c, "duration_s": 90.5, "title": "3"},
                ]
            },
        ]
    }
    eps = lane_episodes(lane)
    assert [str(e.video_id) for e in eps] == [a, c]
    assert eps[1].duration_ms == 90_500
    assert slot_lane({"source": {"type": "lane", "lane": "quran.x"}}) == "quran.x"
    assert slot_lane({"source": {"type": "series"}}) is None
