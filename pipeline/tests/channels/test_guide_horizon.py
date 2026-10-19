"""The guide horizon is a setting (rules ``tv``: horizon_days, topup_below_days)."""

from __future__ import annotations

import contextlib
from collections.abc import AsyncIterator
from datetime import timedelta
from typing import Any
from uuid import UUID, uuid4

import pytest

from maktaba_pipeline import rules
from maktaba_pipeline.channels import pass_, run_schedule, topup_all
from maktaba_pipeline.channels.base import BlockKind, ChannelMode
from maktaba_pipeline.channels.repo import ScheduleState, SqlRepo
from maktaba_pipeline.schedule.tv_settings import LiveSlot, MaktabaTvConfig

from .helpers import FakeRepo, dt, mk_channel, mk_content


def test_defaults_are_a_week_topped_up_below_three_days() -> None:
    tv = MaktabaTvConfig()
    assert (tv.horizon_days, tv.topup_below_days) == (7, 3)
    assert pass_.guide_window(tv) == (timedelta(days=7), timedelta(days=3))
    assert timedelta(days=7) == pass_.DEFAULT_HORIZON
    # the low water never exceeds the horizon
    assert pass_.guide_window(MaktabaTvConfig(horizon_days=2, topup_below_days=5))[1] == (
        timedelta(days=2)
    )


def test_horizon_limits_are_checked() -> None:
    _, bad = rules.config("tv", {"horizon_days": 0, "topup_below_days": 40})
    assert set(bad) == {"horizon_days", "topup_below_days"}
    cfg, bad = rules.config("tv", {"horizon_days": 14, "topup_below_days": 4})
    assert not bad and (cfg.horizon_days, cfg.topup_below_days) == (14, 4)


@pytest.mark.asyncio
async def test_run_schedule_plans_the_setting_s_days() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    now = dt("2026-06-14T20:00:00")
    repo = FakeRepo(ch, mk_content(5), tv=MaktabaTvConfig(horizon_days=2))
    await run_schedule(repo, ch.id, now=now)
    assert repo.state.horizon_until == now + timedelta(days=2)
    # an explicit horizon still wins
    repo = FakeRepo(ch, mk_content(5))
    await run_schedule(repo, ch.id, now=now, horizon=timedelta(hours=3))
    assert repo.state.horizon_until == now + timedelta(hours=3)


@pytest.mark.asyncio
async def test_default_run_covers_seven_days() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    now = dt("2026-06-14T20:00:00")
    repo = FakeRepo(ch, mk_content(5))
    await run_schedule(repo, ch.id, now=now)
    assert repo.state.horizon_until == now + timedelta(days=7)
    assert max(b.end_at for b in repo.blocks) == now + timedelta(days=7)


@pytest.mark.asyncio
async def test_topup_all_uses_topup_below_days() -> None:
    ch = mk_channel(ChannelMode.SHUFFLE)
    now = dt("2026-06-14T20:00:00")
    state = ScheduleState(channel_id=ch.id, horizon_until=now + timedelta(days=4))
    # 4 days planned, top up below 3: not due
    repo = FakeRepo(ch, mk_content(5), state=state)
    assert await topup_all(repo, now=now) == 0
    # top up below 5: due, extended to the 7-day horizon
    repo = FakeRepo(ch, mk_content(5), state=state, tv=MaktabaTvConfig(topup_below_days=5))
    assert await topup_all(repo, now=now) == 1
    assert repo.state.horizon_until == now + timedelta(days=7)


class _DB:
    dialect = "postgres"

    async def fetchval(self, *_: Any) -> bool:
        return True

    @contextlib.asynccontextmanager
    async def transaction(self) -> AsyncIterator[None]:
        yield


@pytest.mark.asyncio
async def test_topup_once_passes_the_setting_through(monkeypatch: pytest.MonkeyPatch) -> None:
    ch = uuid4()
    seen: dict[str, Any] = {}

    async def load_tv(self: Any) -> MaktabaTvConfig:
        return MaktabaTvConfig(horizon_days=10, topup_below_days=4)

    async def due(self: Any, now: Any, low_water: Any) -> list[UUID]:
        seen["low"] = low_water - now
        return [ch]

    async def run(repo: Any, channel_id: UUID, *, now: Any, horizon: Any) -> int:
        seen["horizon"] = horizon
        return 1

    monkeypatch.setattr(SqlRepo, "load_tv", load_tv)
    monkeypatch.setattr(SqlRepo, "channels_needing_topup", due)
    monkeypatch.setattr(pass_, "run_schedule", run)
    assert await pass_.topup_once(_DB()) == 1
    assert seen == {"low": timedelta(days=4), "horizon": timedelta(days=10)}
    # an explicit window (run_topup_loop's) overrides the rules
    assert await pass_.topup_once(_DB(), horizon=timedelta(days=2)) == 1
    assert seen == {"low": timedelta(days=2), "horizon": timedelta(days=2)}


@pytest.mark.asyncio
async def test_a_week_on_22_channels_is_planned_to_the_end() -> None:
    """The planners' safety caps leave room for 7 days on every channel: shuffle,
    marathon, smart mix and a grid of short slots with a live break every 15 minutes."""
    now = dt("2026-06-14T20:00:00")
    slots = [
        {"start": f"{m // 60:02d}:{m % 60:02d}", "end": f"{(m + 10) // 60:02d}:{(m + 10) % 60:02d}"}
        for m in range(0, 24 * 60, 10)
    ]
    slots[-1]["end"] = "24:00"
    modes = [ChannelMode.SHUFFLE, ChannelMode.MARATHON, ChannelMode.SCHEDULE]
    for n in range(22):
        mode = modes[n % len(modes)]
        cfg: dict[str, Any] = {"slots": slots} if mode == ChannelMode.SCHEDULE else {}
        ch = mk_channel(mode, mode_config=cfg, number=100 + n)
        content = mk_content(40, dur_ms=4 * 60_000)
        live = (
            LiveSlot(channel=100 + n, url="https://example.com/live.m3u8", title="News",
                     start=5, end=0, every_minutes=15, minutes=2),
        )  # fmt: skip
        repo = FakeRepo(
            ch,
            content,
            slot_content=[content] * len(slots) if mode == ChannelMode.SCHEDULE else None,
            tv=MaktabaTvConfig(live=live if mode == ChannelMode.SCHEDULE else ()),
        )
        await run_schedule(repo, ch.id, now=now)
        end = max(b.end_at for b in repo.blocks)
        assert end >= now + timedelta(days=7), (mode, end)
        if mode == ChannelMode.SCHEDULE:
            assert sum(b.kind == BlockKind.LIVE for b in repo.blocks) >= 7 * 24 * 4 - 1
