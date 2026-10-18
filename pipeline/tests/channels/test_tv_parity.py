# ruff: noqa: E501  (test data reads better unwrapped)
"""What the dizqueTV channels air, Maktaba's own scheduler airs: filler lists by weight and
cooldown, the Ramadan grid, nonstop padding, flex slots, live broadcasts, keep airing."""

from __future__ import annotations

from collections import Counter
from datetime import date, timedelta
from uuid import uuid4

import pytest

from maktaba_pipeline.channels import packer, run_schedule
from maktaba_pipeline.channels.base import BlockKind, ChannelMode, ContentItem, FillerItem, PlanItem
from maktaba_pipeline.channels.filler import FillerList, FillerPicker, FillerSet
from maktaba_pipeline.channels.hijri import hijri, in_hijri_month
from maktaba_pipeline.channels.planner.schedule import SchedulePlanner, live_windows
from maktaba_pipeline.schedule.tv_settings import (
    DizqueExportConfig,
    LiveSlot,
    MaktabaTvConfig,
    live_slots,
)

from .helpers import FakeRepo, dt, mk_channel, mk_content

MIN = 60_000


def clips(n: int, ms: int, tag: str = "c") -> tuple[FillerItem, ...]:
    return tuple(
        FillerItem(filler_item_id=uuid4(), video_id=uuid4(), duration_ms=ms,
                   snapshot={"title": f"{tag}{i}"})
        for i in range(n)
    )  # fmt: skip


def _lane(name: str, **kw: object) -> dict[str, object]:
    return {"type": "lane", "lane": name, **kw}


def _contiguous(blocks: list) -> None:  # type: ignore[type-arg]
    for a, b in zip(blocks, blocks[1:], strict=False):
        assert a.end_at == b.start_at, (a, b)


# ------------------------------------------------------------------------- filler


def test_a_gap_is_several_whole_clips_then_slate_only_for_what_no_clip_fits() -> None:
    start = dt("2026-06-14T00:00:00")
    lst = FillerList("t", clips(3, 2 * MIN) + clips(1, 3 * MIN))
    blocks = packer.pack(
        [PlanItem(video_id=None, duration_ms=10 * MIN + 30_000, kind=BlockKind.FILLER)],
        channel_id=uuid4(),
        start_at=start,
        until=start + timedelta(minutes=10, seconds=30),
        filler=FillerSet(lists=(lst,)),
    )
    _contiguous(blocks)
    fill = [b for b in blocks if b.kind == BlockKind.FILLER]
    assert len(fill) >= 3
    for b in fill:  # engine contract: video + slice, the clip played whole
        assert b.video_id is not None and b.filler_item_id is not None
        assert b.source_offset == 0 and b.source_duration == int(
            (b.end_at - b.start_at).total_seconds() * 1000
        )
    assert blocks[-1].kind == BlockKind.SLATE  # the 30 s no clip fits
    assert blocks[-1].video_id is None
    assert (blocks[-1].end_at - blocks[-1].start_at) < timedelta(minutes=2)


def test_lists_are_drawn_by_weight() -> None:
    heavy = FillerList("heavy", clips(50, MIN, "h"), weight=300)
    light = FillerList("light", clips(50, MIN, "l"), weight=100)
    p = FillerPicker(FillerSet(lists=(heavy, light)), seed=7)
    start = dt("2026-06-14T00:00:00")
    got = p.fill(start, start + timedelta(hours=40))
    by = Counter(c.snapshot["title"][0] for c, _ in got)
    assert 2.2 < by["h"] / by["l"] < 4.0  # 3:1


def test_a_clip_does_not_repeat_within_its_cooldown() -> None:
    lst = FillerList("t", clips(5, MIN))
    p = FillerPicker(FillerSet(lists=(lst,), repeat_cooldown_ms=5 * MIN), seed=1)
    start = dt("2026-06-14T00:00:00")
    got = p.fill(start, start + timedelta(minutes=30))
    last: dict[object, object] = {}
    for c, at in got:
        if c.video_id in last:
            assert at - last[c.video_id] >= timedelta(minutes=5)  # type: ignore[operator]
        last[c.video_id] = at
    # 6 clips would be needed every 6 minutes, only 5 cool down in time: the picker stops
    # rather than repeat, and resumes once one is free again
    assert len(got) >= 25


def test_a_cooling_list_makes_way_for_the_others() -> None:
    a = FillerList("a", clips(10, MIN, "a"), weight=1000, cooldown_ms=10 * MIN)
    b = FillerList("b", clips(10, MIN, "b"), weight=1)
    p = FillerPicker(FillerSet(lists=(a, b)), seed=3)
    start = dt("2026-06-14T00:00:00")
    titles = [c.snapshot["title"][0] for c, _ in p.fill(start, start + timedelta(minutes=20))]
    assert titles.count("a") == 2  # once every 10 minutes, though it weighs 1000x


def test_a_slot_pool_is_tried_first_and_the_rule_turns_it_off() -> None:
    own = FillerList("ch.promos", clips(3, MIN, "p"))
    gen = FillerList("library.general", clips(3, MIN, "g"))
    fs = FillerSet(lists=(gen,), pools={"ch.promos": own})
    start = dt("2026-06-14T00:00:00")
    first = FillerPicker(fs, seed=1).fill(start, start + timedelta(minutes=2), "ch.promos")
    assert {c.snapshot["title"][0] for c, _ in first} == {"p"}
    off = FillerPicker(fs, seed=1, slot_first=False).fill(
        start, start + timedelta(minutes=2), "ch.promos"
    )
    assert {c.snapshot["title"][0] for c, _ in off} == {"g"}


def test_at_most_max_filler_per_gap_then_slate() -> None:
    start = dt("2026-06-14T00:00:00")
    blocks = packer.pack(
        [],
        channel_id=uuid4(),
        start_at=start,
        until=start + timedelta(minutes=30),
        filler=FillerPicker(FillerSet(lists=(FillerList("t", clips(40, MIN)),)), max_per_gap=4),
    )
    assert [b.kind for b in blocks] == [BlockKind.FILLER] * 4 + [BlockKind.SLATE]
    assert blocks[-1].end_at == start + timedelta(minutes=30)


@pytest.mark.asyncio
async def test_filler_cooldowns_survive_a_topup() -> None:
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config={"slots": [{"start": "00:00", "end": "24:00",
                                                                   "kind": "flex"}]})  # fmt: skip
    fs = FillerSet(lists=(FillerList("t", clips(30, MIN)),), repeat_cooldown_ms=20 * MIN)
    repo = FakeRepo(ch, [], filler=fs)
    t0 = dt("2026-06-14T00:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=1))
    assert repo.state.cursor.get("filler", {}).get("clips")
    await run_schedule(repo, ch.id, now=t0 + timedelta(minutes=30), horizon=timedelta(hours=2))
    seen: dict[object, object] = {}
    for b in sorted(repo.blocks, key=lambda b: b.start_at):
        if b.kind != BlockKind.FILLER:
            continue
        if b.video_id in seen:
            assert b.start_at - seen[b.video_id] >= timedelta(minutes=20)  # type: ignore[operator]
        seen[b.video_id] = b.start_at


# ------------------------------------------------------------------------- Ramadan


def test_hijri_dates() -> None:
    assert hijri(date(2025, 3, 1)) == (1446, 9, 1)  # 1 Ramadan 1446
    assert hijri(date(2025, 3, 31)) == (1446, 10, 1)  # Eid al-Fitr
    assert hijri(date(2024, 3, 11)) == (1445, 9, 1)
    assert hijri(date(2026, 2, 18)) == (1447, 9, 1)
    assert in_hijri_month(date(2026, 3, 1), 9)
    assert not in_hijri_month(date(2026, 6, 14), 9)
    assert not in_hijri_month(date(2025, 3, 1), 9, adjust_days=-1)  # a day later here


def _ramadan_channel() -> tuple[object, list[ContentItem], list[ContentItem]]:
    reg, ram = mk_content(10), mk_content(10)
    cfg = {
        "slots": [{"start": "20:00", "end": "21:00", "source": _lane("regular")}],
        "ramadan": {
            "activate": {"hijri_month": 9},
            "slots": [{"start": "20:00", "end": "21:00", "source": _lane("ramadan")}],
        },
    }
    return mk_channel(ChannelMode.SCHEDULE, mode_config=cfg), reg, ram


def test_the_ramadan_grid_airs_in_ramadan_only() -> None:
    ch, reg, ram = _ramadan_channel()

    def played(day: str, **kw: object) -> set[object]:
        p = SchedulePlanner(ch, slot_content=[reg, ram], **kw)  # type: ignore[arg-type]
        items = p.plan([], start_at=dt(f"{day}T20:00:00"), until=dt(f"{day}T21:00:00"))
        return {i.video_id for i in items if i.kind == BlockKind.PROGRAM}

    ram_ids, reg_ids = {c.video_id for c in ram}, {c.video_id for c in reg}
    assert played("2026-02-25") <= ram_ids and played("2026-02-25")  # 8 Ramadan 1447
    assert played("2026-06-14") <= reg_ids and played("2026-06-14")
    assert played("2026-02-25", ramadan_grids=False) <= reg_ids  # the rule off: as before


# ------------------------------------------------------------------------- grid kinds


def test_a_nonstop_lane_airs_one_episode_a_slot_padded_to_it() -> None:
    lane = _lane("n", pattern="nonstop", slot_minutes=30)
    slots = [{"start": f"{h:02d}:{m:02d}", "end": f"{h + (m + 30) // 60:02d}:{(m + 30) % 60:02d}",
              "source": lane} for h in range(0, 2) for m in (0, 30)]  # fmt: skip
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config={"slots": slots})
    eps = mk_content(10, dur_ms=20 * MIN)
    start = dt("2026-06-14T00:00:00")
    items = SchedulePlanner(ch, slot_content=[eps] * 4).plan(
        [], start_at=start, until=start + timedelta(hours=2))  # fmt: skip
    t, starts = start, []
    for i in items:
        if i.kind == BlockKind.PROGRAM:
            starts.append(t)
        t += timedelta(milliseconds=i.duration_ms)
    assert starts == [start + timedelta(minutes=30 * k) for k in range(4)]  # on the half hour


def test_a_back_to_back_lane_never_leaves_a_gap() -> None:
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config={"slots": [
        {"start": "00:00", "end": "24:00", "source": _lane("n", pattern="nonstop", slot_minutes=0)}
    ]})  # fmt: skip
    eps = mk_content(5, dur_ms=50 * MIN)
    start = dt("2026-06-14T00:00:00")
    items = SchedulePlanner(ch, slot_content=[eps]).plan(
        [], start_at=start, until=start + timedelta(hours=6))  # fmt: skip
    assert all(i.kind == BlockKind.PROGRAM for i in items)


def test_a_flex_slot_airs_filler_not_the_library() -> None:
    cfg = {"slots": [{"start": "08:00", "end": "09:00", "kind": "flex", "source": {"type": "filler"},
                      "filler_pool": "ch.breaks"}]}  # fmt: skip
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    start = dt("2026-06-14T08:00:00")
    items = SchedulePlanner(ch).plan(
        mk_content(5), start_at=start, until=start + timedelta(hours=1)
    )
    assert [i.kind for i in items] == [BlockKind.FILLER] and items[0].pool == "ch.breaks"


def test_a_long_gap_takes_the_next_episode_past_the_slot() -> None:
    """dizqueTV's maxFlex: 20 min of filler at most, else the next episode runs over."""
    cfg = {"slots": [{"start": "20:00", "end": "21:00", "source": _lane("x")}],
           "overrun_tolerance_minutes": 0, "slot_max_filler_minutes": 20}  # fmt: skip
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    eps = mk_content(4, dur_ms=35 * MIN)
    items = SchedulePlanner(ch, slot_content=[eps]).plan(
        [], start_at=dt("2026-06-14T20:00:00"), until=dt("2026-06-14T21:00:00"))  # fmt: skip
    assert [i.video_id for i in items if i.kind == BlockKind.PROGRAM] == [
        eps[0].video_id, eps[1].video_id]  # fmt: skip


def test_spare_time_is_shared_between_the_episodes() -> None:
    cfg = {"slots": [{"start": "20:00", "end": "21:00", "source": _lane("x")}],
           "filler_between_episodes": True}  # fmt: skip
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    eps = mk_content(4, dur_ms=25 * MIN)
    items = SchedulePlanner(ch, slot_content=[eps]).plan(
        [], start_at=dt("2026-06-14T20:00:00"), until=dt("2026-06-14T21:00:00"))  # fmt: skip
    assert [(i.kind, i.duration_ms) for i in items] == [
        (BlockKind.PROGRAM, 25 * MIN), (BlockKind.FILLER, 5 * MIN),
        (BlockKind.PROGRAM, 25 * MIN), (BlockKind.FILLER, 5 * MIN),
    ]  # fmt: skip


# ------------------------------------------------------------------------- live


NEWS = LiveSlot(channel=105, url="https://www.youtube.com/@aljazeera/live", title="الأخبار",
                start=20 * 60, end=23 * 60, every_minutes=60, minutes=10)  # fmt: skip


def test_live_windows_follow_start_every_end_and_days() -> None:
    start = dt("2026-06-14T00:00:00")  # a Sunday
    ws = live_windows([NEWS], 105, start, start + timedelta(days=1))
    assert [w[0] for w in ws] == [dt(f"2026-06-14T{h}:00:00") for h in (20, 21, 22)]
    assert all(w[1] - w[0] == timedelta(minutes=10) for w in ws)
    assert live_windows([NEWS], 101, start, start + timedelta(days=1)) == []
    fri = LiveSlot(channel=105, url="u", start=12 * 60, end=12 * 60 + 1, minutes=30, days=("fri",))
    got = live_windows([fri], 105, start, start + timedelta(days=7))
    assert [w[0] for w in got] == [dt("2026-06-19T12:00:00")]


def test_live_blocks_air_on_time_and_nothing_runs_into_them() -> None:
    cfg = {"slots": [{"start": "19:00", "end": "23:00", "source": _lane("talks")}],
           "overrun_tolerance_minutes": 5}  # fmt: skip
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg, number=105)
    eps = mk_content(20, dur_ms=25 * MIN)
    start = dt("2026-06-14T19:00:00")
    p = SchedulePlanner(ch, slot_content=[eps], live=[NEWS])
    blocks = packer.pack(
        p.plan([], start_at=start, until=start + timedelta(hours=4)),
        channel_id=ch.id, start_at=start, until=start + timedelta(hours=4),
        filler=FillerSet(lists=(FillerList("t", clips(20, MIN)),)),
    )  # fmt: skip
    _contiguous(blocks)
    live = [b for b in blocks if b.kind == BlockKind.LIVE]
    assert [b.start_at for b in live] == [dt(f"2026-06-14T{h}:00:00") for h in (20, 21, 22)]
    for b in live:  # engine contract: the stream address, the guide title, no video
        assert b.source_url == NEWS.url and b.title_snapshot == {"title": "الأخبار"}
        assert b.video_id is None and b.end_at - b.start_at == timedelta(minutes=10)
    for b in blocks:
        if b.kind == BlockKind.PROGRAM:
            assert not any(b.start_at < x.start_at < b.end_at for x in live), b
    # the 25-min episodes: two from 19:00 (19:50), filler to 20:00; then the show goes on
    progs = [b for b in blocks if b.kind == BlockKind.PROGRAM]
    assert [b.video_id for b in progs] == [e.video_id for e in eps[: len(progs)]]
    assert progs[2].start_at == dt("2026-06-14T20:10:00")
    before = [
        b
        for b in blocks
        if b.start_at >= dt("2026-06-14T19:50:00") and b.end_at <= live[0].start_at
    ]
    assert before and all(b.kind in (BlockKind.FILLER, BlockKind.SLATE) for b in before)


def test_maktaba_tv_live_falls_back_to_the_dizquetv_rows() -> None:
    dz = DizqueExportConfig(live=(NEWS,))
    assert live_slots(MaktabaTvConfig(), dz) == (NEWS,)
    own = LiveSlot(channel=101, url="x", minutes=5)
    assert live_slots(MaktabaTvConfig(live=(own,)), dz) == (own,)


@pytest.mark.asyncio
async def test_the_pass_writes_live_blocks_from_the_tv_rules() -> None:
    from datetime import UTC, datetime

    from maktaba_pipeline.channels.pass_ import channel_tz

    cfg = {"slots": [{"start": "00:00", "end": "24:00", "source": _lane("talks")}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg, number=105)
    repo = FakeRepo(ch, [], slot_content=[mk_content(40, dur_ms=25 * MIN)],
                    tv=MaktabaTvConfig(live=(NEWS,)))  # fmt: skip
    tz = channel_tz(cfg)  # the server's zone: the live times are its wall clock
    now = datetime(2026, 6, 14, 18, 0, tzinfo=tz).astimezone(UTC)
    await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=6))
    live = [b for b in repo.blocks if b.kind == BlockKind.LIVE]
    assert [b.start_at.astimezone(tz).hour for b in live] == [20, 21, 22]
    assert all(b.source_url == NEWS.url for b in live)


# ------------------------------------------------------------------------- keep airing


@pytest.mark.asyncio
async def test_a_rewrite_keeps_the_block_on_air_and_continues_the_queue() -> None:
    """A stale channel (a new plan) is rewritten from the end of what is on air, and
    each lane goes on with the episode that would have aired next, not one past the
    old horizon."""
    cfg = {"slots": [{"start": "00:00", "end": "24:00", "source": _lane("talks")}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    eps = mk_content(200, dur_ms=30 * MIN)
    repo = FakeRepo(ch, [], slot_content=[eps])
    t0 = dt("2026-06-14T00:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=12))
    first = sorted(repo.blocks, key=lambda b: b.start_at)
    on_air = first[2]  # 01:00-01:30
    repo.state.stale = True
    t1 = dt("2026-06-14T01:10:00")
    await run_schedule(repo, ch.id, now=t1, horizon=timedelta(hours=12))
    after = sorted(repo.blocks, key=lambda b: b.start_at)
    _contiguous(after)
    kept = next(b for b in after if b.start_at == on_air.start_at)
    assert (kept.video_id, kept.end_at, kept.seq) == (on_air.video_id, on_air.end_at, on_air.seq)
    nxt = next(b for b in after if b.start_at == on_air.end_at)
    assert nxt.video_id == eps[3].video_id  # the queue goes on after the episode on air
    vids = [b.video_id for b in after if b.kind == BlockKind.PROGRAM]
    assert vids == [e.video_id for e in eps[: len(vids)]]  # nothing dropped, nothing doubled


@pytest.mark.asyncio
async def test_a_horizon_never_cuts_an_episode_in_two() -> None:
    cfg = {"slots": [{"start": "00:00", "end": "24:00", "source": _lane("talks")}]}
    ch = mk_channel(ChannelMode.SCHEDULE, mode_config=cfg)
    eps = mk_content(100, dur_ms=50 * MIN)
    repo = FakeRepo(ch, [], slot_content=[eps])
    t0 = dt("2026-06-14T00:00:00")
    await run_schedule(repo, ch.id, now=t0, horizon=timedelta(hours=5))
    await run_schedule(repo, ch.id, now=t0 + timedelta(hours=4), horizon=timedelta(hours=30))
    progs = [
        b for b in sorted(repo.blocks, key=lambda b: b.start_at) if b.kind == BlockKind.PROGRAM
    ]
    assert all(b.source_duration == 50 * MIN for b in progs)
    assert [b.video_id for b in progs] == [e.video_id for e in eps[: len(progs)]]
