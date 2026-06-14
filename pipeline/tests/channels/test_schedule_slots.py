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
