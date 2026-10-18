"""Planner: Arabic lane names, clip-group filler and a show's own promos next to it."""

from __future__ import annotations

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    EpisodeRef,
    LooseVideo,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    simulate,
    suggest_channels,
)

from ._legacy import SMALL

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


def _plan() -> PlanResult:
    seasons = [
        _season("s1", "شاهد على العصر", "islamic_history", 1),
        _season("s2", "كنوز السيرة", "seerah", 2),
        _season("s3", "بلا حدود", "current_affairs", 1),
    ]
    clips = [_clip(i, group="أحمد - مقاطع - السيرة النبوية") for i in range(20)]
    promos = [_clip(100 + i, promo_of="Y:/a|كنوز السيرة") for i in range(3)]
    return suggest_channels(seasons, clips + promos, PlannerConfig(**SMALL))  # type: ignore[arg-type]


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


def test_break_clips_belong_to_their_channel() -> None:
    seasons = [
        _season("s1", "شاهد على العصر", "islamic_history", 1),
        _season("s2", "كنوز السيرة", "seerah", 2),
        _season("s3", "دروس العقيدة", "aqeedah", 1),
    ]

    def clip(path: str, tags: dict[str, float], genre: str, folder: str = "Y:/a") -> LooseVideo:
        return LooseVideo(path=path, duration_s=120, title=path, folder=folder,
                          presenter="سعيد الكملي" if "k" in folder else "أحمد", tags=tags,
                          genre=genre, is_short=True)  # fmt: skip

    kamali = "Y:/قناة الشَّيخ سَعيد الكَمَلي"
    vids = [
        clip("n-seerah.mp4", {"topic:nasheed": 0.9, "topic:seerah": 0.9}, "nasheed"),
        clip("n-plain.mp4", {"topic:nasheed": 0.9}, "nasheed"),
        clip("d-aqeedah.mp4", {"topic:dua_dhikr": 0.9, "topic:aqeedah": 0.9}, "dua_dhikr"),
        clip("k-seerah.mp4", {"topic:seerah": 0.9}, "seerah", kamali),
        clip("k-aqeedah.mp4", {"topic:aqeedah": 0.9}, "aqeedah", kamali),
        clip("k-long.mp4", {"topic:seerah": 0.9}, "seerah", kamali),
    ]
    vids[-1] = LooseVideo(**{**{f: getattr(vids[-1], f) for f in vids[-1].__slots__},
                             "duration_s": 20 * 60})  # fmt: skip
    plan = suggest_channels(seasons, vids, PlannerConfig(**SMALL))  # type: ignore[arg-type]

    def breaks(key: str) -> set[str]:
        k = f"{key}.breaks"
        return {i.path for i in plan.filler_pools[k].items} if k in plan.filler_pools else set()

    keys = {c.key for c in plan.channels}
    assert {"seerah", "aqeedah"} <= keys, keys
    if "seerah" in keys:  # a light channel: its nasheed and its clip, nothing else
        assert breaks("seerah") == {"n-seerah.mp4", "k-seerah.mp4"}
    if "aqeedah" in keys:  # not a light one: no nasheed; its dua and its clip
        assert breaks("aqeedah") == {"d-aqeedah.mp4", "k-aqeedah.mp4"}
    assert "k-long.mp4" not in set().union(*(breaks(k) for k in keys))  # longer than a break
    for c in plan.channels:
        if c.kind == "quran":
            assert not any(p.endswith(".breaks") for p in c.filler_pools)
    off = suggest_channels(seasons, vids, PlannerConfig(**{**SMALL, "break_filler": False}))  # type: ignore[arg-type]
    assert not any(k.endswith(".breaks") for k in off.filler_pools)


def test_current_affairs_clips_pad_only_the_news_channel() -> None:
    from maktaba_pipeline.schedule.channel_planner import ThemeSpec

    seasons = [
        _season("s1", "بلا حدود", "current_affairs", 1),
        _season("s2", "كنوز السيرة", "seerah", 2),
    ]
    news_clips = [_clip(300 + i, genre="current_affairs") for i in range(5)]
    themes = (ThemeSpec("news", "News", "الأخبار", 124, ("current_affairs",)),
              ThemeSpec("seerah", "Seerah", "السيرة", 103, ("seerah",)))  # fmt: skip
    plan = suggest_channels(seasons, news_clips, PlannerConfig(**{**SMALL, "themes": themes}))  # type: ignore[arg-type]
    news = {i.path for i in plan.filler_pools["news.topic"].items}
    assert {v.path for v in news_clips} <= news
    others = {i.path for k, p in plan.filler_pools.items() if not k.startswith("news.")
              for i in p.items}  # fmt: skip
    assert not others & news  # dated news never pads another channel


def test_the_quran_channel_takes_recitations_not_talk_about_dua() -> None:
    from maktaba_pipeline.schedule.channel_planner import ThemeSpec

    reciter = [
        SeasonCandidate(
            series_id=f"q{i}", name=f"تلاوة {i}", kind="episodic", series_key=f"تلاوة {i}",
            folder="Y:/q", presenter="القارئ", first_air="2015-01-01", genre="quran_recitation",
            tier=1, tags={"topic:quran_recitation": 1.0, "format:recitation": 1.0},
            episodes=tuple(EpisodeRef(path=f"Y:/q/{i}/{j}.mp4", duration_s=3600, title=str(j),
                                      episode=j) for j in range(1, 30)),
        ) for i in range(3)
    ]  # fmt: skip
    talk = _clip(400, genre="dua_dhikr")
    recited = LooseVideo(path="Y:/q/r.mp4", duration_s=200, title="سورة الإخلاص", folder="Y:/q",
                         presenter="القارئ", tags={"topic:quran_recitation": 0.9},
                         genre="quran_recitation", is_short=True)  # fmt: skip
    themes = (
        ThemeSpec("quran", "Quran", "القرآن الكريم", 101, ("quran_recitation",), kind="quran"),
    )
    plan = suggest_channels(reciter, [talk, recited],
                            PlannerConfig(**{**SMALL, "themes": themes}))  # type: ignore[arg-type]  # fmt: skip
    pool = (
        {i.path for i in plan.filler_pools.get("quran.filler", ()).items}
        if "quran.filler" in plan.filler_pools
        else set()
    )
    assert "Y:/q/r.mp4" in pool and talk.path not in pool


def test_kids_breaks_are_made_for_kids_not_just_on_its_topics() -> None:
    from maktaba_pipeline.schedule.channel_planner import ThemeSpec

    seasons = [_season("s2", "كنوز السيرة", "seerah", 2),
               _season("s3", "قصص للأطفال", "seerah", 2)]  # fmt: skip
    kamali = "Y:/قناة الشَّيخ سَعيد الكَمَلي"
    adult = LooseVideo(path="Y:/k/adult.mp4", duration_s=120, title="درس", folder=kamali,
                       presenter="سعيد الكملي", tags={"topic:seerah": 0.9}, genre="seerah",
                       is_short=True)  # fmt: skip
    child = LooseVideo(path="Y:/k/child.mp4", duration_s=120, title="للأطفال", folder=kamali,
                       presenter="سعيد الكملي", tags={"topic:seerah": 0.9, "audience:kids": 0.9},
                       genre="seerah", is_short=True)  # fmt: skip
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",), audiences=("kids",))
    plan = suggest_channels(seasons, [adult, child],
                            PlannerConfig(**{**SMALL, "themes": (kids,)}))  # type: ignore[arg-type]  # fmt: skip
    breaks = {i.path for i in plan.filler_pools["kids.breaks"].items}
    assert breaks == {"Y:/k/child.mp4"}
