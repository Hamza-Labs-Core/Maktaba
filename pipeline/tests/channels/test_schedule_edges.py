"""Scheduler edge cases: a stale rewrite's filler cooldowns, a live broadcast in the
overhang of the last slot, slots across a DST change, the per-gap clip ceiling."""

from __future__ import annotations

from datetime import UTC, datetime, timedelta, tzinfo
from uuid import uuid4
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import pytest

from maktaba_pipeline.channels import packer, run_schedule
from maktaba_pipeline.channels.base import BlockKind, ChannelMode, FillerItem
from maktaba_pipeline.channels.filler import (
    MAX_CLIPS_PER_GAP,
    FillerList,
    FillerPicker,
    FillerSet,
)
from maktaba_pipeline.channels.pass_ import channel_tz
from maktaba_pipeline.channels.planner.schedule import SchedulePlanner, _span
from maktaba_pipeline.channels.repo import ScheduleState
from maktaba_pipeline.schedule.tv_settings import LiveSlot

from .helpers import FakeRepo, dt, mk_channel, mk_content

MIN = 60_000


def _zone(name: str) -> tzinfo:
    try:
        return ZoneInfo(name)
    except ZoneInfoNotFoundError:  # a host without tzdata (CI Linux has it)
        pytest.skip(f"time zone {name} unavailable")


def _ms(t: datetime) -> int:
    return int((t - datetime(1970, 1, 1, tzinfo=UTC)) / timedelta(milliseconds=1))


@pytest.mark.asyncio
async def test_a_stale_rewrite_forgets_filler_plays_after_its_anchor() -> None:
    """A rewrite replaces the plan from the anchor: plays the old plan put after it never
    aired, so they must not keep a clip cooling down (they would be in its future)."""
    cfg = {"slots": [{"start": "00:00", "end": "24:00", "kind": "flex"}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    clip = FillerItem(filler_item_id=uuid4(), video_id=uuid4(), duration_ms=MIN)
    fs = FillerSet(lists=(FillerList("t", (clip,)),), repeat_cooldown_ms=5 * MIN)
    t0 = dt("2026-06-14T00:00:00")
    played = {str(clip.video_id): _ms(t0 + timedelta(hours=1))}
    state = ScheduleState(
        channel_id=ch.id,
        horizon_until=t0 + timedelta(hours=2),
        cursor={"filler": {"clips": played, "lists": {"t": played[str(clip.video_id)]}}},
        stale=True,
    )
    repo = FakeRepo(ch, [], filler=fs, state=state)
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(minutes=30))
    first = min(repo.blocks, key=lambda b: b.start_at)
    assert first.kind == BlockKind.FILLER and first.video_id == clip.video_id


@pytest.mark.unit
def test_a_live_broadcast_in_the_last_slots_overhang_cuts_it() -> None:
    """The last slot runs past ``until`` to its end; a broadcast in that overhang still
    stops it (the next top-up starts with the broadcast)."""
    cfg = {"slots": [{"start": "00:00", "end": "24:00", "source": {"type": "lane", "lane": "a"}}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg, number=105)
    live = LiveSlot(channel=105, url="u", start=18 * 60, end=18 * 60 + 1, minutes=10)
    p = SchedulePlanner(ch, slot_content=[mk_content(60, dur_ms=30 * MIN)], live=[live])
    start = dt("2026-06-14T00:00:00")
    items = p.plan([], start_at=start, until=start + timedelta(hours=12))
    assert sum(i.duration_ms for i in items) == 18 * 60 * MIN
    assert all(i.kind != BlockKind.LIVE for i in items)


@pytest.mark.unit
def test_a_slot_across_spring_forward_is_as_long_as_it_really_is() -> None:
    ny = _zone("America/New_York")  # 2026-03-08 02:00 EST -> 03:00 EDT
    start = datetime(2026, 3, 8, 1, 0, tzinfo=ny)
    end = datetime(2026, 3, 8, 4, 0, tzinfo=ny)
    assert _span(start, end) == 2 * 60 * MIN
    cfg = {"slots": [{"start": "01:00", "end": "04:00", "source": {"type": "lane", "lane": "a"}}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    p = SchedulePlanner(ch, slot_content=[mk_content(20, dur_ms=30 * MIN)])
    items = p.plan([], start_at=start, until=end)
    assert sum(i.duration_ms for i in items) == 2 * 60 * MIN
    assert sum(1 for i in items if i.kind == BlockKind.PROGRAM) == 4


@pytest.mark.unit
def test_the_server_zone_fallback_is_a_real_zone(monkeypatch: pytest.MonkeyPatch) -> None:
    ny = _zone("America/New_York")
    monkeypatch.setenv("TZ", "America/New_York")
    tz = channel_tz({})
    assert isinstance(tz, ZoneInfo) and tz.key == "America/New_York"
    winter = datetime(2026, 1, 15, 12, tzinfo=tz).utcoffset()
    summer = datetime(2026, 7, 15, 12, tzinfo=tz).utcoffset()
    assert winter != summer and summer == datetime(2026, 7, 15, 12, tzinfo=ny).utcoffset()


@pytest.mark.unit
def test_a_gap_takes_at_most_the_clip_ceiling_then_slate() -> None:
    short = tuple(
        FillerItem(filler_item_id=uuid4(), video_id=uuid4(), duration_ms=1_000) for _ in range(5)
    )
    start = dt("2026-06-14T00:00:00")
    end = start + timedelta(hours=1)
    picker = FillerPicker(FillerSet(lists=(FillerList("t", short),)), seed=1)
    blocks = packer.pack([], channel_id=uuid4(), start_at=start, until=end, filler=picker)
    assert sum(1 for b in blocks if b.kind == BlockKind.FILLER) == MAX_CLIPS_PER_GAP
    assert blocks[-1].kind == BlockKind.SLATE and blocks[-1].end_at == end
    # an explicit rule above the ceiling is held to it
    assert FillerPicker(None, max_per_gap=10_000).max_per_gap == MAX_CLIPS_PER_GAP
