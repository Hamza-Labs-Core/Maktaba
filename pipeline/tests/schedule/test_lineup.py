"""The lineup: at most max_channels, each with enough content; library tags; the
kids' hour; a slot owned by one presenter."""

from __future__ import annotations

from collections.abc import Sequence

import pytest

from maktaba_pipeline.schedule.channel_planner import (
    DEFAULT_DAYPARTS,
    EpisodeRef,
    PlannerConfig,
    PlanResult,
    SeasonCandidate,
    ThemeSpec,
    suggest_channels,
)

pytestmark = pytest.mark.unit


def season(sid: str, name: str, *, genre: str = "fiqh", presenter: str = "أحمد",
           library: str = "", n: int = 20, minutes: float = 30,
           tags: dict[str, float] | None = None) -> SeasonCandidate:  # fmt: skip
    t = {f"topic:{genre}": 1.0, f"genre:{genre}": 1.0, "format:lecture": 0.9, **(tags or {})}
    folder = f"Y:/{library or presenter}"
    return SeasonCandidate(
        series_id=sid, name=name, season=1, kind="episodic", series_key=name, folder=folder,
        presenter=presenter, library=library or presenter, first_air="2010-01-01", genre=genre,
        tags=t,
        episodes=tuple(EpisodeRef(path=f"{folder}/{sid}/{i}.mp4", duration_s=minutes * 60,
                                  title=f"ح{i}", episode=i, plex_rating_key=f"{sid}{i}")
                       for i in range(1, n + 1)),
    )  # fmt: skip


def plan(seasons: Sequence[SeasonCandidate], **cfg: object) -> PlanResult:
    base: dict[str, object] = {"channel_min_days": 1.0, "channel_day_hours": 10.0}
    base.update(cfg)
    return suggest_channels(seasons, (), PlannerConfig(**base))  # type: ignore[arg-type]


THEMES = (
    ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",)),
    ThemeSpec("seerah", "Seerah", "السيرة", 103, ("seerah",)),
    ThemeSpec("tafsir", "Tafsir", "التفسير", 105, ("tafsir",)),
)


def test_a_channel_needs_days_of_content_without_a_repeat() -> None:
    lib = [season("f1", "فقه 1", n=30), season("s1", "سيرة", genre="seerah", n=6)]
    p = plan(lib, themes=THEMES, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["fiqh"]  # 15 h ≥ 10 h; seerah 3 h < 10 h
    why = dict(p.unplaced)["theme seerah"]
    assert "needs 10 h" in why and "1 days at 10 h a day" in why


def test_at_most_max_channels_themes_first_in_their_order() -> None:
    lib = [season(f"{g}{i}", f"{g} {i}", genre=g, presenter=f"شيخ {i}", n=30)
           for g in ("fiqh", "seerah", "tafsir") for i in range(2)]  # fmt: skip
    p = plan(lib, themes=THEMES, max_channels=2, presenter_min_genres=1)
    assert [c.key for c in p.channels] == ["seerah", "fiqh"]  # sorted by number
    left = dict(p.unplaced)
    assert "past max_channels (2)" in left["theme tafsir"]
    assert "past max_channels (2)" in left["mix"]


def test_presenter_channels_only_for_the_largest_libraries() -> None:
    lib = [season(f"p{i}", f"برنامج {i}", presenter=f"شيخ {i}", n=10 * (i + 3))
           for i in range(4)]  # fmt: skip
    p = plan(
        lib,
        themes=(),
        mix_channel=False,
        presenter_max_channels=2,
        presenter_min_genres=1,
        presenter_min_hours=0.0,
    )
    assert [c.name for c in p.channels] == ["شيخ 3", "شيخ 2"]  # the two largest
    assert "past presenter_max_channels (2)" in dict(p.unplaced)["presenter شيخ 1"]


def test_library_tags_make_a_kids_channel_and_adults_stay_off_it() -> None:
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",),
                     audiences=("kids", "kids_friendly"), exclude=("audience:adults",))  # fmt: skip
    lib = [
        season("h1", "تاريخنا", genre="islamic_history", library="تاريخنا", n=30),
        season("s1", "سيرة للكبار", genre="seerah", library="الشريف", n=30),
        season("s2", "سيرة", genre="seerah", library="إلهامي", n=30),
    ]
    p = plan(lib, themes=(kids,), mix_channel=False, presenter_channels=False,
             library_tags={"تاريخنا": ("audience:kids_friendly",),
                           "الشريف": ("audience:adults",)})  # fmt: skip
    ch = p.channels[0]
    names = {u.name for k in ch.lanes for u in p.lanes[k].units}
    assert names == {"تاريخنا", "سيرة"}  # the adults' seerah stays off


def test_the_kids_hour_takes_kids_shows_and_never_adults() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    lib = [season(f"f{i}", f"فقه {i}", presenter=f"شيخ {i}", n=60, minutes=60) for i in range(6)]
    lib.append(season("k1", "قصص للأطفال", tags={"topic:fiqh": 1.0, "audience:kids": 1.0},
                      genre="stories", n=60, minutes=60))  # fmt: skip
    p = plan(lib, themes=(fiqh,), mix_channel=False, presenter_channels=False,
             dayparts=DEFAULT_DAYPARTS)  # fmt: skip
    ch = p.channels[0]
    at4 = next(s for s in ch.slots if s.start <= 16 * 60 < s.end and s.on("mon"))
    assert at4.lane is not None and p.lanes[at4.lane].shows[0] == "قصص للأطفال"


def test_a_slot_belongs_to_a_presenter_his_next_show_follows() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    # many shows of two sheikhs, more than the grid has slots: the rest queue behind
    lib = [season(f"{p}{i}", f"{p} {i}", presenter=p, n=40, minutes=60)
           for p in ("أ", "ب") for i in range(40)]  # fmt: skip
    p = plan(lib, themes=(fiqh,), mix_channel=False, presenter_channels=False)
    for k in p.channels[0].lanes:
        ln = p.lanes[k]
        assert len({u.presenter for u in ln.units}) == 1, ln.name  # one sheikh per slot


def test_tajweed_lessons_are_not_recitation_by_default() -> None:
    lib = [season("t1", "دروس التجويد", genre="tajweed", n=30)]
    themes = (ThemeSpec("quran_learning", "Quran Learning", "تعليم القرآن", 118, ("tajweed",)),)
    p = plan(lib, themes=themes, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["quran_learning"]


def test_a_themes_own_minimum_replaces_the_month_rule() -> None:
    lib = [season("f1", "فقه", n=10)]  # 5 h: short of the 10 h rule
    own = (ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",), min_hours=2.0),)
    p = plan(lib, themes=own, mix_channel=False, presenter_channels=False)
    assert [c.key for c in p.channels] == ["fiqh"]


def test_a_theme_takes_a_talk_shows_episodes_about_it_as_one_collection() -> None:
    from dataclasses import replace

    talk = season("bh", "بلا حدود", genre="current_affairs", presenter="أحمد منصور", n=40,
                  minutes=50)  # fmt: skip
    eps = [replace(e, tags={"topic:palestine": 0.9}) if i % 2 else e
           for i, e in enumerate(talk.episodes)]  # fmt: skip
    talk = replace(talk, episodes=tuple(eps))
    pal = (ThemeSpec("palestine", "Palestine", "فلسطين", 125, ("palestine",)),)
    p = plan([talk], themes=pal, mix_channel=False, presenter_channels=False)
    ch = p.channels[0]
    units = [u for k in ch.lanes for u in p.lanes[k].units]
    assert {u.name for u in units} == {"بلا حدود · فلسطين"}  # a special run of the show
    assert sum(len(u.episodes) for u in units) == 20  # only its Palestine episodes
    off = plan([talk], themes=pal, mix_channel=False, presenter_channels=False,
               theme_episodes=False)  # fmt: skip
    assert off.channels == ()


def test_a_show_on_two_channels_never_airs_at_the_same_time() -> None:
    fiqh = ThemeSpec("fiqh", "Fiqh", "الفقه", 111, ("fiqh",))
    salah = ThemeSpec("salah", "Salah", "فقه الصلاة", 121, ("salah",))
    lib = [season(f"s{i}", f"صلاة {i}", tags={"topic:salah": 1.0}, n=200, minutes=60)
           for i in range(3)]  # fmt: skip
    p = plan(lib, themes=(fiqh, salah), mix_channel=False, presenter_channels=False)
    assert {c.key for c in p.channels} == {"fiqh", "salah"}  # the shows air on both
    by_ch = {c.key: {(sl.start, p.lanes[sl.lane].shows[0]) for sl in c.slots
                     if sl.lane and sl.kind == "program"} for c in p.channels}  # fmt: skip
    assert not by_ch["fiqh"] & by_ch["salah"]  # never the same show at the same time


def test_a_show_goes_to_its_topics_channel_before_an_audience_one() -> None:
    hist = ThemeSpec("history", "History", "التاريخ", 108, ("islamic_history",))
    kids = ThemeSpec("kids", "Kids", "الأطفال", 110, ("seerah",), audiences=("beginners",))
    lib = [season("h1", "100 سؤال في التاريخ", genre="islamic_history", n=40, minutes=60,
                  tags={"audience:beginners": 1.0}),
           season("k1", "سيرة", genre="seerah", n=40, minutes=60)]  # fmt: skip
    p = plan(lib, themes=(hist, kids), mix_channel=False, presenter_channels=False,
             show_max_channels=1)  # fmt: skip
    on = {c.key: {u.name for k in c.lanes for u in p.lanes[k].units} for c in p.channels}
    assert "100 سؤال في التاريخ" in on["history"]
    assert "100 سؤال في التاريخ" not in on["kids"]


def test_talk_shows_give_up_a_special_runs_episodes_a_topic_channel_keeps_its_show() -> None:
    from dataclasses import replace

    talk = season("bh", "بلا حدود", genre="current_affairs", presenter="أحمد منصور", n=40,
                  minutes=50, tags={"format:program": 0.9})  # fmt: skip
    eps = [replace(e, tags={"topic:palestine": 0.9}) if i % 2 else e
           for i, e in enumerate(talk.episodes)]  # fmt: skip
    talk = replace(talk, episodes=tuple(eps))
    themes = (
        ThemeSpec("palestine", "Palestine", "فلسطين", 125, ("palestine",)),
        ThemeSpec("news", "News", "الأخبار", 124, ("current_affairs",)),
        ThemeSpec("talk", "Talk", "البرامج الحوارية", 126, formats=("program",)),
    )
    # one show: the same-time rule would leave a second channel no time for it
    p = plan([talk], themes=themes, mix_channel=False, presenter_channels=False,
             show_same_time=True)  # fmt: skip

    def eps_on(key: str) -> int:
        c = next(c for c in p.channels if c.key == key)
        return sum(len(u.episodes) for k in c.lanes for u in p.lanes[k].units
                   if u.name == "بلا حدود")  # fmt: skip

    assert eps_on("talk") == 20  # without the 20 the Palestine channel runs specially
    assert eps_on("news") == 40  # its topic's channel keeps the show whole
