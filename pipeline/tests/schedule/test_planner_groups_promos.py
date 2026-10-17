"""Planner: Arabic lane names, clip-group filler and a show's own promos next to it."""

from __future__ import annotations

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    EpisodeRef,
    LooseVideo,
    SeasonCandidate,
    simulate,
    suggest_channels,
)

pytestmark = pytest.mark.unit


def _season(sid: str, name: str, genre: str, tier: int, n: int = 40) -> SeasonCandidate:
    return SeasonCandidate(
        series_id=sid, name=name, kind="episodic", series_key=name, folder="Y:/a",
        presenter="أحمد", first_air="2015-01-01", genre=genre, tier=tier,
        tags={f"topic:{genre}": 1.0, f"genre:{genre}": 1.0},
        episodes=tuple(
            EpisodeRef(path=f"Y:/a/{sid}/{i}.mp4", duration_s=50 * 60, title=f"{name} {i}",
                       episode=i, plex_rating_key=f"{sid}{i}")
            for i in range(1, n + 1)
        ),
    )  # fmt: skip


def _clip(i: int, *, group: str = "", promo_of: str = "", genre: str = "seerah") -> LooseVideo:
    return LooseVideo(
        path=f"Y:/a/c{i}.mp4", duration_s=90 + i, title=f"مقطع {i}", folder="Y:/a",
        presenter="أحمد", tags={f"topic:{genre}": 0.9}, genre=genre, is_short=True,
        group=group, promo_of=promo_of,
    )  # fmt: skip


def _plan():  # type: ignore[no-untyped-def]
    seasons = [
        _season("s1", "شاهد على العصر", "islamic_history", 1),
        _season("s2", "كنوز السيرة", "seerah", 2),
        _season("s3", "بلا حدود", "current_affairs", 1),
    ]
    clips = [_clip(i, group="أحمد - مقاطع - السيرة النبوية") for i in range(20)]
    promos = [_clip(100 + i, promo_of="Y:/a|كنوز السيرة") for i in range(3)]
    return suggest_channels(seasons, clips + promos)


def test_lane_names_are_arabic_and_keys_stay_stable() -> None:
    plan = _plan()
    names = {k: ln.name for k, ln in plan.lanes.items()}
    mix = {k: n for k, n in names.items() if k.startswith("mix.")}
    assert mix, names
    assert all(n.startswith("منوعات") for n in mix.values())
    assert not any("unknown" in n or "Mix" in n for n in mix.values())
    # keys name the slot (a weekday or "strip", and the time), so re-planning finds them
    days = {"mon", "tue", "wed", "thu", "fri", "sat", "sun", "strip", "strip7"}
    assert all(k.split(".")[1] in days and k.split(".")[2].isdigit() for k in mix), mix
    assert plan.lanes == _plan().lanes  # same input, same keys and queues


def test_promos_pad_their_own_show_and_nowhere_else() -> None:
    plan = _plan()
    promo_pools = {k: p for k, p in plan.filler_pools.items() if k.endswith(".promo")}
    assert promo_pools
    for key, pool in promo_pools.items():
        lane = plan.lanes[key.removesuffix(".promo")]
        assert any(u.series_key == "كنوز السيرة" for u in lane.units)
        assert all(i.promo_of == "Y:/a|كنوز السيرة" for i in pool.items)
        assert pool.fallback is not None  # then the channel's on-topic filler
    general = [p for k, p in plan.filler_pools.items() if not k.endswith((".promo", ".promos"))]
    assert not any(i.promo_of for p in general for i in p.items)
    # clip-group items are on-topic filler
    assert any(i.source == "clip_group" for p in general for i in p.items)
    mix = next(c for c in plan.channels if c.key == "mix")
    day = simulate(mix, plan, weekday="fri")
    assert any(a.kind == "filler" and a.path and "/c10" in a.path for a in day)
