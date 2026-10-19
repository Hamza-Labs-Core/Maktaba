"""A slot never ends on a long stretch of filler: the next episode plays, past its end."""

from __future__ import annotations

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    ChannelSuggestion,
    EpisodeRef,
    Lane,
    PlannerConfig,
    PlanResult,
    ProgramUnit,
    Slot,
    dizquetv_time_slots,
    simulate,
)

pytestmark = pytest.mark.unit


def _plan(minutes: list[float], **cfg: object) -> tuple[ChannelSuggestion, PlanResult]:
    eps = tuple(EpisodeRef(path=f"Y:/a/{i}.mp4", duration_s=m * 60, title=str(i), episode=i)
                for i, m in enumerate(minutes, 1))  # fmt: skip
    u = ProgramUnit(series_id="s", name="درس", season=1, presenter="أحمد", folder="Y:/a",
                    kind="episodic", genre="fiqh", tier=None, style=None, ramadan=False,
                    first_air="2020-01-01", series_key="درس", episodes=eps)  # fmt: skip
    ln = Lane(key="fiqh.strip.0000", name="درس", tier_group="mixed", units=(u,), rank=0.0)
    slots = (Slot(0, 120, ln.key, "x", None), Slot(120, 1440, None, "x", None, kind="flex"))
    ch = ChannelSuggestion(key="fiqh", number=111, name="Fiqh", name_ar="الفقه", kind="theme",
                           category="islamic", lanes=(ln.key,), slots=slots, ramadan_slots=(),
                           filler_pools=(), hours=0.0, reasons=())  # fmt: skip
    plan = PlanResult(channels=(ch,), lanes={ln.key: ln}, filler_pools={}, unplaced=(),
                      config=PlannerConfig(**cfg))  # type: ignore[arg-type]  # fmt: skip
    return ch, plan


def test_a_long_gap_takes_the_next_episode_past_the_slot() -> None:
    ch, plan = _plan([30, 50, 60])  # 120 min: 30 + 50 = 80, 40 left; the 60 runs past
    progs = [a for a in simulate(ch, plan) if a.kind == "program" and a.start_s < 7200]
    assert [round((a.end_s - a.start_s) / 60) for a in progs] == [30, 50, 60]


def test_a_short_gap_stays_filler() -> None:
    ch, plan = _plan([60, 45, 60])  # 105, 15 left (≤ 20): filler
    progs = [a for a in simulate(ch, plan) if a.kind == "program" and a.start_s < 7200]
    assert len(progs) == 2


def test_off_keeps_the_slot() -> None:
    ch, plan = _plan([30, 50, 60], slot_max_filler_minutes=0)
    progs = [a for a in simulate(ch, plan) if a.kind == "program" and a.start_s < 7200]
    assert len(progs) == 2


def test_dizquetv_gets_the_most_filler_a_slot_ends_on() -> None:
    ch, plan = _plan([30])
    assert dizquetv_time_slots(ch, plan)["maxFlex"] == 20 * 60_000
